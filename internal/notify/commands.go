// Package notify reaches the person when they are not looking: the pop-up
// and its sound through the terminals, then the phone, and the person's own
// hook programs; held back by Do not disturb, silenced by Mute, and counted
// as seen only when a window was there to show it.
package notify

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const (
	// perTask is the least time between two nudges about one task.
	perTask = 30 * time.Second
	// snapshotWait is how long the pass waits for the terminals' picture.
	snapshotWait = 3 * time.Second
	// reach is all the time the hook programs and the phone's request get
	// for one nudge: no command waits longer for it.
	reach = 10 * time.Second
)

// Nudger is the kit's Notifier. Its Items is the pass that runs after each
// sweep and after each new item.
type Nudger struct {
	k *contract.Kit
	// next are the channels after the pop-up, tried in order until one says
	// it reached the person.
	next []channel
}

// Plug puts the nudger into the kit. The package binds no command.
func Plug(k *contract.Kit) { k.Notifier = Nudger{k, []channel{phone}} }

// sentFile is nudged.json in the login's state folder. At is when a task
// was last nudged about, kept for as long as the limit lasts. Said is, by
// run, what the pass has said that is no item and still holds.
type sentFile struct {
	contract.Versioned
	At   map[string]time.Time `json:"at"`
	Said map[string][]string  `json:"said,omitempty"`
}

// settings is the person's switches with the phone's address, and what
// ui.json says of Do not disturb and Mute.
type settings struct {
	contract.PersonConfig
	ui   contract.UIFile
	dirs contract.Dirs
}

func (n Nudger) settings() (set settings, err error) {
	set.PersonConfig = contract.PersonDefaults()
	if set.dirs, err = n.k.Platform.Dirs(); err != nil {
		return set, err
	}
	if set.PersonConfig, err = contract.ReadPerson(n.k.Platform.Peek, set.dirs.Config); err != nil {
		return set, err
	}
	// Read, not Peek: a file missed at the moment it is replaced would read
	// as Do not disturb switched off.
	err = contract.ReadVersioned(n.k.Platform.Read, filepath.Join(set.dirs.State, contract.UIFileName), contract.FileVersion, &set.ui)
	return set, err
}

// remember reads nudged.json, lets change alter it, and writes it back when
// change says it did.
func (n Nudger) remember(set settings, change func(*sentFile) bool) error {
	file, sent := filepath.Join(set.dirs.State, "nudged.json"), sentFile{At: map[string]time.Time{}, Said: map[string][]string{}}
	if err := contract.ReadVersioned(n.k.Platform.Read, file, contract.FileVersion, &sent); err != nil {
		return err
	}
	if !change(&sent) {
		return nil
	}
	sent.Version = contract.FileVersion
	return contract.WriteVersioned(n.k.Platform, file, contract.FileVersion, sent)
}

// over reports whether Do not disturb has an end time and it has passed.
func over(ui contract.UIFile, now time.Time) bool {
	return ui.DND && !ui.DNDUntil.IsZero() && !now.Before(ui.DNDUntil)
}

// send starts the person's hook programs and goes through the channels
// until one has reached the person. The pop-up counts when the keeper says
// it was shown, which it says only with a window attached and the pop-up
// switched on.
func (n Nudger) send(set settings, c contract.Nudge) {
	sound := set.Nudge.Sound && !set.ui.Mute
	ctx, cancel := context.WithTimeout(context.Background(), reach)
	defer cancel()
	defer hooks(ctx, n.k, set, c, sound)()
	if set.Nudge.Popup {
		reason, delivery, err := n.k.Terms.Notify(c.Title, c.Body, sound)
		if err == nil && delivery != contract.NotifyOff && reason == contract.NotifyShown {
			return
		}
	}
	for _, channel := range n.next {
		if channel(ctx, set, c) {
			return
		}
	}
}

// Nudge is one call about something that is no item. Under Do not disturb
// only an urgent one is sent, and a task is nudged about at most once in 30
// seconds.
func (n Nudger) Nudge(c contract.Nudge) error {
	now := contract.Now()
	set, err := n.settings()
	if err != nil || set.ui.DND && !over(set.ui, now) && !c.Urgent {
		return err
	}
	if c.Task != "" {
		key, fresh := strings.Join([]string{c.Root, c.Run, c.Task}, "|"), false
		err := n.remember(set, func(sent *sentFile) bool {
			if fresh = now.Sub(sent.At[key]) >= perTask; fresh {
				maps.DeleteFunc(sent.At, func(_ string, at time.Time) bool { return now.Sub(at) >= perTask })
				sent.At[key] = now
			}
			return fresh
		})
		if err != nil || !fresh {
			return err
		}
	}
	n.send(set, c)
	return nil
}

// news is what the record shows that the person is nudged for and that is
// no item: a task that failed for good and the whole job finished. The
// terminals out of reach is the sweep's to say, which knows since when.
func news(s *contract.State) (out []contract.Nudge) {
	settled := len(s.Tasks) > 0
	for _, id := range slices.Sorted(maps.Keys(s.Tasks)) {
		t := s.Tasks[id]
		settled = settled && !t.SettledAt.IsZero()
		if t.Status == contract.TaskFailed {
			out = append(out, contract.Nudge{Task: id, Title: t.Name + " — " + string(contract.LookFailed),
				Body: fmt.Sprintf("failed %d times and is not started again until it is reset", t.Failures)})
		}
	}
	if settled {
		out = append(out, contract.Nudge{Title: s.Run.Objective + " — finished", Body: "every task is done, failed or cancelled"})
	}
	return out
}

// Items puts every item of a run that waits for the person in front of
// them: it stamps each as shown, which only this does, and then nudges, so
// an item is in the pane before its nudge. Do not disturb holds everything
// but the urgent back, unstamped; when its end time has passed it is
// switched off here and one nudge announces all that was held. What the
// lead agent said unread is in the pane at once and is nudged about only
// when it has lain unread for nudge.lead_minutes. The pass also says the
// news of the record, each once for as long as it holds.
func (n Nudger) Items(root, run string, snap *contract.Snapshot, now time.Time) error {
	set, err := n.settings()
	if err != nil {
		return err
	}
	ended := over(set.ui, now)
	if ended {
		set.ui.Version, set.ui.DND, set.ui.DNDUntil = contract.FileVersion, false, time.Time{}
		file := filepath.Join(set.dirs.State, contract.UIFileName)
		if err := contract.WriteVersioned(n.k.Platform, file, contract.FileVersion, set.ui); err != nil {
			return err
		}
	}
	s, err := n.k.Store.Read(root, run)
	if err != nil {
		return err
	}
	if snap == nil {
		ctx, cancel := context.WithTimeout(context.Background(), snapshotWait)
		snap, _ = n.k.Terms.Snapshot(ctx)
		cancel()
	}
	var fresh []contract.Nudge
	err = n.remember(set, func(sent *sentFile) bool {
		key, said := root+"|"+run, []string(nil)
		for _, c := range news(s) {
			switch c.Root, c.Run = root, run; {
			case slices.Contains(sent.Said[key], c.Title):
			case set.ui.DND && !c.Urgent:
				continue
			case !looking(s, snap, c.Task):
				fresh = append(fresh, c)
			}
			said = append(said, c.Title)
		}
		same := slices.Equal(said, sent.Said[key])
		if sent.Said[key] = said; said == nil {
			delete(sent.Said, key)
		}
		return !same
	})
	if err != nil {
		return err
	}
	for _, c := range fresh {
		n.send(set, c)
	}
	// The view model says what waits, what holds work up and what Do not
	// disturb keeps back; the nudge is one more printer of it.
	v := n.k.View(contract.ViewInput{Now: now, Caller: contract.Human, State: s,
		Limits: contract.ProjectDefaults(), Terms: snap, UI: set.ui, Checked: now})
	var due, loud []contract.Item
	for _, it := range v.Items {
		q := s.Questions[it.ID]
		if q == nil || !q.ShownAt.IsZero() || q.Cause == contract.CauseLeadUnread &&
			now.Sub(s.Run.Lead.DoneSince) < time.Duration(set.Nudge.LeadMinutes)*time.Minute {
			continue
		}
		due = append(due, it)
		if !looking(s, snap, it.Task) {
			loud = append(loud, it)
		}
	}
	if len(due) == 0 {
		return nil
	}
	err = n.k.Store.Change(root, run, func(s *contract.State) error {
		for _, it := range due {
			if err := n.k.Rules.Shown(s, it.ID, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	c := contract.Nudge{Root: root, Run: run}
	switch {
	case ended:
		c.Title, c.Body = things(len(due))+" waited while you were not to be disturbed", names(due)
	case len(loud) == 1:
		look := contract.LookNeedsYou
		if loud[0].Look == contract.LookAtPrompt {
			look = contract.LookAtPrompt
		}
		c.Title, c.Body, c.Task = names(loud)+" — "+string(look), loud[0].Text, loud[0].Task
	case len(loud) > 1:
		c.Title, c.Body = things(len(loud))+" wait for you", names(loud)
	default:
		return nil
	}
	n.send(set, c)
	return nil
}

// looking reports whether the tab of a task, or of the lead agent, is the
// one in front.
func looking(s *contract.State, snap *contract.Snapshot, task string) bool {
	pane := ""
	if t := s.Tasks[task]; task == contract.Lead && s.Run.Orchestrator != nil {
		pane = s.Run.Orchestrator.Pane
	} else if t != nil && len(t.Attempts) > 0 {
		pane = s.Attempts[t.Attempts[len(t.Attempts)-1]].Place.Pane
	}
	if snap == nil || pane == "" {
		return false
	}
	var tab, front string
	for _, p := range snap.Panes {
		if p.ID == pane {
			tab = p.Tab
		}
		if p.Focused {
			front = p.Tab
		}
	}
	return tab != "" && tab == front
}

// things is the count a nudge about several items begins with.
func things(n int) string {
	if n == 1 {
		return "1 thing"
	}
	return fmt.Sprintf("%d things", n)
}

// names lists what the items are about, each once.
func names(items []contract.Item) string {
	var out []string
	for _, it := range items {
		name := it.Name
		if name == "" {
			name = "whaleshark"
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return strings.Join(out, ", ")
}
