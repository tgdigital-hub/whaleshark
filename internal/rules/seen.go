package rules

import (
	"fmt"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const (
	goneAfter     = 30 * time.Second  // between the two sightings that prove an exit
	recovery      = 120 * time.Second // after herdr restarted, nobody is declared gone
	stillAfter    = 2 * time.Minute   // working this long into a pause is "still running"
	leadAfter     = 60 * time.Second  // before a lead agent counts as unread or not listening
	together      = 2 * time.Minute   // most workers going idle within this is one event
	stampWorking  = time.Minute       // how often last_working moves while nothing else does
	textPrompt    = "%s is waiting at a prompt in its tab."
	textTogether  = "Most workers stopped together; check your usage limit."
	textUnread    = "The lead agent has said something you have not read. It may be asking you something."
	textSilent    = "The lead agent is not listening."
	textStartDied = "the start did not finish"
)

// Seen keeps the record of what a sweep saw. It reports a change only when a
// state, a flag or a liveness value moved: last_working alone is stamped in
// passing and saved with the next real change.
func (r Rules) Seen(s *contract.State, id string, pane *contract.Pane, startLockHeld bool, limits contract.ProjectFile, now time.Time) bool {
	a := s.Attempts[id]
	if a == nil || !a.State.Live() {
		return false
	}
	if a.State == contract.AttemptStarting {
		return !startLockHeld && r.StartFailed(s, id, textStartDied, now) == nil
	}
	t, h := s.Tasks[a.Task], &a.Herdr
	before, state, agent, place := *h, a.State, a.Agent, a.Place
	seq, items, withdrawn := s.Counters.Seq, s.Counters.Need, false
	pause := s.Run.Paused

	if pause == nil {
		h.StillFlagged = false
		if max := time.Duration(limits.Limits.MaxMinutes) * time.Minute; max > 0 && !h.OvertimeFlagged && now.Sub(a.StartedAt) >= max {
			h.OvertimeFlagged = true
			raise(s, "overtime", t, a, "", nil, now)
		}
	}
	switch {
	case pane != nil && pane.Terminal != "" && a.Place.Terminal != "" && pane.Terminal != a.Place.Terminal:
		a.Place.Terminal = pane.Terminal
		h.Status, h.Liveness, h.GoneSince = "", contract.Unverifiable, time.Time{}
	case pane == nil || pane.Agent == "":
		switch {
		case h.Status == "" && h.GoneSince.IsZero() && now.Sub(h.At) < recovery:
		case h.GoneSince.IsZero():
			h.GoneSince = now
		case now.Sub(h.GoneSince) >= goneAfter && h.Liveness != contract.Gone:
			h.Liveness = contract.Gone
			if a.State == contract.AttemptWorking || a.State == contract.AttemptAsked {
				end(s, a, contract.AttemptExited, contract.ExitExited, now)
				back(t, true, now)
				raise(s, "exited", t, a, "", nil, now)
			}
		}
	default:
		was := h.Status
		h.Status, h.GoneSince, h.Liveness = pane.Status, time.Time{}, contract.Live
		if pane.Terminal != "" {
			a.Place.Terminal = pane.Terminal
		}
		if pane.Session != "" {
			a.Agent.Session = pane.Session
		}
		if pane.Status != contract.StatusBlocked && !h.BlockedSince.IsZero() {
			h.BlockedSince = time.Time{}
			withdraw(s, contract.CausePrompt, a.ID)
		}
		switch pane.Status {
		case contract.StatusWorking:
			h.QuietFlagged = false
			if now.Sub(h.LastWorking) >= stampWorking {
				h.LastWorking = now
			}
			withdrawn = was != contract.StatusWorking && withdraw(s, contract.CauseTogether, "")
			stale := time.Duration(limits.Limits.StaleMinutes) * time.Minute
			switch {
			case pause != nil:
				if !h.StillFlagged && now.Sub(pause.At) >= stillAfter {
					h.StillFlagged = true
					raise(s, "still_running", t, a, "", nil, now)
				}
			case a.State == contract.AttemptWorking && stale > 0 && !h.StaleFlagged && now.Sub(lastNote(a)) >= stale:
				h.StaleFlagged = true
				raise(s, "stale", t, a, "", nil, now)
			}
		case contract.StatusBlocked:
			if h.BlockedSince.IsZero() {
				h.BlockedSince = now
				raise(s, "blocked", t, a, "waiting at a prompt in its tab", nil, now)
				item(s, contract.CausePrompt, t, a, textPrompt, false, now)
			}
		case contract.StatusIdle, contract.StatusDone:
			if was == contract.StatusWorking {
				h.LastWorking = now
			}
			if a.State != contract.AttemptWorking {
				break
			}
			quiet := time.Duration(limits.Limits.QuietSeconds) * time.Second
			if pause == nil && quiet > 0 && !h.QuietFlagged && now.Sub(h.LastWorking) >= quiet {
				h.QuietFlagged = true
				raise(s, "quiet", t, a, "", nil, now)
			}
			if h.QuietFlagged {
				h.Liveness = contract.Unverifiable
			}
			if was == contract.StatusWorking {
				stoppedTogether(s, now)
			}
		default:
			h.Liveness = contract.Unverifiable
		}
	}
	before.LastWorking = h.LastWorking
	changed := *h != before || a.State != state || a.Agent != agent || a.Place != place || s.Counters.Seq != seq || s.Counters.Need != items || withdrawn
	if changed {
		h.At = now
	}
	return changed
}

func lastNote(a *contract.Attempt) time.Time {
	if a.Progress != nil && a.Progress.At.After(a.StateSince) {
		return a.Progress.At
	}
	return a.StateSince
}

// stoppedTogether raises one event and one urgent item when more than half
// of the working attempts, and at least two, went idle within two minutes.
func stoppedTogether(s *contract.State, now time.Time) {
	working, idle := 0, 0
	for _, a := range s.Attempts {
		if a.State != contract.AttemptWorking {
			continue
		}
		working++
		if st := a.Herdr.Status; (st == contract.StatusIdle || st == contract.StatusDone) && now.Sub(a.Herdr.LastWorking) <= together {
			idle++
		}
	}
	if idle >= 2 && idle*2 > working && find(s, contract.CauseTogether, "") == nil {
		raise(s, "together", nil, nil, textTogether, nil, now)
		item(s, contract.CauseTogether, nil, nil, textTogether, true, now)
	}
}

// find returns the tool's own item for a cause that is still in view.
func find(s *contract.State, cause, attempt string) *contract.Question {
	for _, q := range s.Questions {
		if q.From == contract.FromTool && q.Cause == cause && q.Attempt == attempt && q.State != contract.QuestionClosed {
			return q
		}
	}
	return nil
}

func item(s *contract.State, cause string, t *contract.Task, a *contract.Attempt, text string, urgent bool, now time.Time) {
	q := contract.Question{Form: contract.FormTodo, From: contract.FromTool, Cause: cause, Urgent: urgent, Text: text}
	if t != nil {
		q.Task, q.Attempt, q.Text = t.ID, a.ID, fmt.Sprintf(text, t.Name)
	}
	Rules{}.Need(s, q, now)
}

// withdraw closes the tool's own item once its cause is gone.
func withdraw(s *contract.State, cause, attempt string) bool {
	q := find(s, cause, attempt)
	if q != nil {
		q.State = contract.QuestionClosed
	}
	return q != nil
}

// LeadSeen raises and withdraws the two items about the lead agent's own
// pane. Each waits a minute first, counted from the moment kept on the run.
func (Rules) LeadSeen(s *contract.State, pane *contract.Pane, waiting bool, now time.Time) bool {
	status := ""
	if open(s) != nil {
		return false
	}
	if pane != nil {
		status = pane.Status
	}
	resting, l := status == contract.StatusIdle || status == contract.StatusDone, &s.Run.Lead
	unread := leadItem(s, &l.DoneSince, contract.CauseLeadUnread, textUnread, status == contract.StatusDone, now)
	silent := leadItem(s, &l.SilentSince, contract.CauseLeadSilent, textSilent, resting && !waiting && len(s.Inbox.Events) > 0, now)
	return unread || silent
}

// leadItem keeps one record for its cause and opens it again for each new
// episode; one the person dealt with is not raised again in the same episode.
func leadItem(s *contract.State, since *time.Time, cause, text string, on bool, now time.Time) bool {
	var q *contract.Question
	for _, x := range s.Questions {
		if x.From == contract.FromTool && x.Cause == cause {
			q = x
		}
	}
	switch {
	case !on && since.IsZero():
		return false
	case !on:
		if *since = (time.Time{}); q != nil {
			q.State = contract.QuestionClosed
		}
	case since.IsZero():
		*since = now
	case now.Sub(*since) < leadAfter || q != nil && !q.CreatedAt.Before(*since):
		return false
	case q == nil:
		item(s, cause, nil, nil, text, false, now)
	default:
		if q.Answer != "" {
			shelve(q, q.AnsweredBy, now)
		}
		q.State, q.CreatedAt, q.ShownAt, q.UsedAt = contract.QuestionOpen, now, time.Time{}, time.Time{}
	}
	return true
}
