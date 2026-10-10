package inbox

import (
	"context"
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const (
	// sweepEvery is how far apart the stamp keeps everybody's sweeps. A start
	// is also given that long before a free start.lock is believed: its
	// process may not have taken the lock yet.
	sweepEvery   = 5 * time.Second
	snapshotWait = 3 * time.Second
	// downAfter is how long the picture may fail before the person is told.
	downAfter = time.Minute
)

// stamp is sweep.at: when the run was last held against the picture, and
// since when no picture could be had and whether the person was told so.
type stamp struct {
	contract.Versioned
	At   time.Time `json:"at"`
	Down time.Time `json:"down,omitzero"`
	Told bool      `json:"told,omitempty"`
}

type sweeper struct{ k *contract.Kit }

// pointer is one line to type once the lock is free again: into the lead
// agent's pane, or into an attempt's for the messages it has not read.
type pointer struct {
	pane, attempt, arg string
	text               contract.Pointer
	mail               []int
}

// look is what one sweep holds the record against.
type look struct {
	rules   contract.Rules
	panes   map[string]*contract.Pane
	names   map[string]*contract.Pane // by the name the keeper knows an agent by
	keeper  string                    // this start of the keeper
	limits  contract.ProjectFile
	dead    map[string]bool // attempts whose start nobody is bringing up any more
	waiting bool            // a wait holds wait.lock
	now     time.Time
}

var errSame = errors.New("nothing changed")

func (sw sweeper) Sweep(root, run string, now time.Time) (contract.Swept, *contract.Snapshot, error) {
	return sw.sweep(root, run, now, false)
}

// sweep takes one picture and applies it. The record is read under the
// shared lock and tried on that copy first: only when a state, a flag or a
// liveness value moved is it changed under the exclusive one. always is for
// wait, whose own five seconds are not held back by the stamp. After it the
// notifier's pass runs: an item is in the record before its nudge.
func (sw sweeper) sweep(root, run string, now time.Time, always bool) (contract.Swept, *contract.Snapshot, error) {
	k, dir := sw.k, sw.k.Store.Dir(root, run)
	var last stamp
	contract.ReadVersioned(k.Platform.Peek, filepath.Join(dir, stampFile), contract.FileVersion, &last)
	swept := contract.Swept{Ran: !last.At.IsZero(), At: last.At}
	if age := now.Sub(last.At); !always && age >= 0 && age < sweepEvery {
		return swept, nil, nil
	}
	s, err := k.Store.Read(root, run)
	if err != nil {
		return swept, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapshotWait)
	snap, err := k.Terms.Snapshot(ctx)
	cancel()
	if err != nil {
		// Out of reach for a minute is one urgent nudge for each outage (8e).
		was := last
		if last.Version = contract.FileVersion; last.Down.IsZero() {
			last.Down = now
		}
		if !last.Told && now.Sub(last.Down) >= downAfter {
			last.Told = true
			k.Notifier.Nudge(contract.Nudge{Root: root, Run: run, Urgent: true, Title: "The engine is not running",
				Body: "Nothing has been seen of run " + run + " for a minute. `whaleshark open` starts it."})
		}
		if last != was {
			contract.WriteVersioned(k.Platform, filepath.Join(dir, stampFile), contract.FileVersion, last)
		}
		return swept, nil, &contract.Refusal{Exit: contract.ExitEnv, Code: "no_picture",
			Message: "The sweep changed nothing, because it could not see the terminals: " + err.Error() + "."}
	}
	free := func(lock string) bool {
		unlock, ok, err := k.Platform.TryLock(lock)
		if ok {
			unlock()
		}
		return ok && err == nil
	}
	l := &look{rules: k.Rules, panes: map[string]*contract.Pane{}, names: map[string]*contract.Pane{}, keeper: snap.Instance,
		limits: contract.ProjectDefaults(), dead: map[string]bool{}, waiting: !free(filepath.Join(dir, contract.WaitLock)), now: now}
	if p, err := contract.ReadProjectFile(root); err == nil {
		l.limits = p
	}
	for i := range snap.Panes {
		p := &snap.Panes[i]
		if l.panes[p.ID] = p; p.Agent != "" && p.Name != "" {
			l.names[p.Name] = p
		}
	}
	for id, a := range s.Attempts {
		if a.State == contract.AttemptStarting && now.Sub(a.StateSince) >= sweepEvery {
			l.dead[id] = free(filepath.Join(contract.AttemptDir(dir, id), "start.lock"))
		}
	}

	changed, todo := l.apply(s)
	if changed {
		err = k.Store.Change(root, run, func(s *contract.State) error {
			if changed, todo = l.apply(s); !changed {
				return errSame
			}
			return nil
		})
		if err != nil && !errors.Is(err, errSame) {
			return swept, nil, err
		}
	}
	todo = slices.DeleteFunc(todo, func(p pointer) bool {
		return k.Terms.Point(p.pane, p.text, p.arg) != nil || p.attempt == ""
	})
	if len(todo) > 0 {
		err := k.Store.Change(root, run, func(s *contract.State) error {
			for _, p := range todo {
				for _, seq := range p.mail {
					k.Rules.Pointed(s, p.attempt, seq, now)
				}
			}
			return nil
		})
		changed = changed || err == nil
	}
	contract.WriteVersioned(k.Platform, filepath.Join(dir, stampFile), contract.FileVersion,
		stamp{Versioned: contract.Versioned{Version: contract.FileVersion}, At: now})
	// The nudge pass, only while an item has not been put in front of the
	// person yet: twenty agents at work cost it nothing.
	for _, q := range s.Questions {
		if q.State == contract.QuestionOpen && q.ShownAt.IsZero() {
			k.Notifier.Items(root, run, snap, now)
			break
		}
	}
	return contract.Swept{Ran: true, At: now, Changed: changed}, snap, nil
}

func resting(p *contract.Pane) bool {
	return p != nil && p.Agent != "" && p.Status == contract.StatusIdle
}

// apply runs the rows of the sweep table over a record and lists the
// pointers that are due. It reports a change only where a rule does, so
// last_working alone is never a reason to write.
func (l *look) apply(s *contract.State) (changed bool, todo []pointer) {
	if !s.Run.ClosedAt.IsZero() {
		return false, nil
	}
	events := len(s.Inbox.Events)
	// Another instance is the keeper started again (8g). For two minutes
	// from now Seen believes nothing it does not see: no attempt is stamped
	// gone while agents may still be coming back.
	was := s.Run.Terms.Instance
	if l.keeper != "" && was != l.keeper {
		s.Run.Terms.Instance, changed = l.keeper, true
	}
	restarted := changed && was != ""
	for _, id := range slices.Sorted(maps.Keys(s.Attempts)) {
		a := s.Attempts[id]
		if restarted && a.State.Live() && a.State != contract.AttemptStarting {
			a.Seen.Status, a.Seen.Liveness, a.Seen.GoneSince, a.Seen.At = "", contract.Unverifiable, time.Time{}, l.now
		}
		pane := l.panes[a.Place.Pane]
		// An agent that is not where its place says is found by its name,
		// never by a label; a pane that is still there says nothing of it.
		if moved := l.names[a.Agent.Name]; moved != nil && (pane == nil || pane.Agent == "") && a.State.Live() {
			pane, changed = moved, true
			a.Place.Pane, a.Place.Tab = moved.ID, moved.Tab
		}
		if l.rules.Seen(s, id, pane, !l.dead[id], l.limits, l.now) {
			changed = true
		}
		if s.Run.Paused != nil || !resting(pane) || a.State != contract.AttemptWorking && a.State != contract.AttemptAsked {
			continue
		}
		var unread []int
		for _, m := range a.Mail {
			if m.ReadAt.IsZero() && m.NudgedAt.IsZero() {
				unread = append(unread, m.Seq)
			}
		}
		if unread != nil {
			todo = append(todo, pointer{pane: pane.ID, attempt: id, text: contract.PointMail, mail: unread})
		}
	}
	var lead *contract.Pane
	if o := s.Run.Orchestrator; o != nil && l.panes[o.Pane] != nil {
		// In front is its tab, whichever pane of the tab has the keys.
		in := *l.panes[o.Pane]
		for _, p := range l.panes {
			in.Focused = in.Focused || p.Focused && p.Tab == in.Tab
		}
		lead = &in
	}
	if l.rules.LeadSeen(s, lead, l.waiting, l.now) {
		changed = true
	}
	// What this sweep raised reaches a lead agent that is resting and not
	// waiting as one line in its pane.
	if n := len(s.Inbox.Events); n > events && !l.waiting && s.Run.Paused == nil && resting(lead) {
		todo = append(todo, pointer{pane: lead.ID, text: contract.PointEvents, arg: strconv.Itoa(n)})
	}
	if changed {
		s.Run.Terms.SeenAt = l.now
	}
	return changed, todo
}
