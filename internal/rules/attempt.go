package rules

import (
	"fmt"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const (
	howCheck  = contract.AcceptCheck
	howByHand = contract.AcceptByHand
	outDone   = contract.ReportDone
	outFailed = contract.ReportFailed
)

func (r Rules) Start(s *contract.State, task string, retry, paneHoldsAgent bool, tokenHash string, a contract.Agent, gated bool, now time.Time) (string, error) {
	t, prev, err := r.taskAttempt(s, task)
	if err == nil {
		err = open(s)
	}
	if err != nil {
		return "", err
	}
	switch {
	case s.Run.Paused != nil:
		return "", paused(false)
	case t.Status == contract.TaskPending:
		waits := unmet(s, t)
		e := refused("not_startable", "%s waits on %s.", t.ID, strings.Join(waits, ", "))
		e.Data = map[string]any{"waits_on": waits}
		return "", next(e, "whaleshark graph")
	case t.Status != contract.TaskReady:
		return "", next(refused("not_startable", "%s is %s, not ready.", t.ID, t.Status), "whaleshark show "+t.ID)
	case live(s) >= s.Run.Limit:
		return "", next(refused("limit", "%d agents are live, which is the limit of this run.", s.Run.Limit), "whaleshark status")
	case prev != nil && !retry:
		return "", next(refused("needs_retry", "%s was tried before.", t.ID), "whaleshark start "+t.ID+" --retry")
	case prev != nil && paneHoldsAgent:
		return "", next(refused("pane_busy", "The tab of %s still holds an agent.", prev.ID), "whaleshark show "+t.ID+" --screen", "whaleshark close "+t.ID)
	}
	if err := pass(s, t, now); err != nil {
		return "", err
	}
	if a.Kind == "" {
		a.Kind = t.Agent
	}
	if a.Model == "" {
		a.Model = t.Model
	}
	n := &contract.Attempt{
		ID: fmt.Sprintf("%s.%d", t.ID, len(t.Attempts)+1), Task: t.ID, N: len(t.Attempts) + 1,
		State: contract.AttemptStarting, StateSince: now, TokenHash: tokenHash, Agent: a, Gated: gated,
		Seen: contract.AgentSeen{Liveness: contract.Unverifiable, At: now}, StartedAt: now,
	}
	if prev != nil {
		n.RetryOf = prev.ID
	}
	s.Attempts[n.ID] = n
	t.Attempts = append(t.Attempts, n.ID)
	t.Status = contract.TaskRunning
	return n.ID, nil
}

// starting resolves an attempt a start is still bringing up. A start stops
// at its next step once the person has pressed Stop all.
func starting(s *contract.State, id string, pause bool) (*contract.Attempt, *contract.Task, error) {
	a, t, err := attempt(s, id)
	switch {
	case err != nil:
		return nil, nil, err
	case a.State != contract.AttemptStarting:
		return nil, nil, refused("not_starting", "%s is %s; its start was overtaken.", a.ID, a.State)
	case pause && s.Run.Paused != nil:
		return nil, nil, paused(false)
	}
	return a, t, nil
}

func (Rules) Placed(s *contract.State, id string, p contract.Place, now time.Time) error {
	a, _, err := starting(s, id, true)
	if err != nil {
		return err
	}
	a.Place = p
	return nil
}

func (Rules) Working(s *contract.State, id string, now time.Time) error {
	a, _, err := starting(s, id, true)
	if err != nil {
		return err
	}
	setState(a, contract.AttemptWorking, now)
	a.Seen = contract.AgentSeen{Status: contract.StatusWorking, At: now, Liveness: contract.Live, LastWorking: now}
	return nil
}

func (Rules) StartFailed(s *contract.State, id, why string, now time.Time) error {
	a, t, err := starting(s, id, false)
	if err != nil {
		return err
	}
	end(s, a, contract.AttemptStartFailed, contract.ExitStartFailed, now)
	back(t, false, now)
	created := map[string]any{}
	if a.Place.Tab != "" {
		created["tab"] = a.Place.Tab
	}
	if t.Worktree != nil {
		created["worktree"] = t.Worktree.Path
	}
	raise(s, "start_failed", t, a, why, map[string]any{"created": created}, now)
	return nil
}

func gone(a *contract.Attempt) *contract.Refusal {
	return refused("not_active", "%s is %s. This attempt is no longer active; stop.", a.ID, a.State)
}

func (Rules) Token(s *contract.State, id, tokenHash string) error {
	a, _, err := attempt(s, id)
	if err == nil && (tokenHash == "" || tokenHash != a.TokenHash) {
		err = refused("bad_token", "The token does not match %s. Stop.", a.ID)
	}
	return err
}

func (Rules) Progress(s *contract.State, id string, pct int, note string, now time.Time) error {
	a, _, err := attempt(s, id)
	if err != nil {
		return err
	}
	switch a.State {
	case contract.AttemptStarting, contract.AttemptWorking, contract.AttemptAsked:
	default:
		return gone(a)
	}
	if pct < 0 || pct > 100 {
		return refuse(contract.ExitUsage, "bad_percent", "Progress is a number from 0 to 100.")
	}
	a.Progress = &contract.Progress{Pct: pct, Note: note, At: now}
	a.Seen.StaleFlagged = false
	return nil
}

// Report changes the record even when it refuses: the refusal is kept as a
// rejected event, so whoever calls it saves the run all the same. The one
// exception is a report the worker can still mend, which is exit 1 and
// leaves nothing behind.
func (Rules) Report(s *contract.State, id, tokenHash, outcome, summary string, evidence []contract.EvidenceFile, now time.Time) (bool, *contract.Refusal, error) {
	a, t, err := attempt(s, id)
	if err != nil {
		return false, nil, err
	}
	if outcome != outDone && outcome != outFailed {
		return false, nil, refuse(contract.ExitUsage, "bad_outcome", "A report is done or failed.")
	}
	reject := func(e *contract.Refusal) (bool, *contract.Refusal, error) {
		raise(s, "rejected", t, a, e.Message, map[string]any{"outcome": outcome, "summary": summary}, now)
		return false, e, nil
	}
	switch {
	case tokenHash == "" || tokenHash != a.TokenHash:
		return reject(refused("bad_token", "The token does not match %s. Stop.", a.ID))
	case a.Report != nil && a.Report.Outcome == outcome && (a.State == contract.AttemptReported || a.State == contract.AttemptFailed):
		return true, nil, nil
	case a.State != contract.AttemptWorking && a.State != contract.AttemptAsked:
		return reject(gone(a))
	case outcome == outDone && t.BrowserCheck && len(evidence) == 0:
		return false, nil, refuse(contract.ExitFailed, "no_evidence", "%s needs a browser check: save the evidence first, then report again.", t.ID)
	}
	a.Report = &contract.Report{Outcome: outcome, Summary: summary, At: now, Evidence: evidence}
	if outcome == outFailed {
		end(s, a, contract.AttemptFailed, contract.ExitReported, now)
		back(t, true, now)
	} else {
		setState(a, contract.AttemptReported, now)
		closeAsks(s, a, now)
		t.Status = contract.TaskReview
		s.Run.Scan.Due = s.Run.Scan.Due || t.Worktree != nil
	}
	raise(s, outcome, t, a, summary, nil, now)
	return false, nil, nil
}

// Checking may be run again on an attempt that is already being checked:
// whoever holds the accept lock is the only accept there is, so the earlier
// one died, or this is the same one recording what it is about to check.
func (r Rules) Checking(s *contract.State, task string, c contract.Checked, now time.Time) error {
	t, a, err := r.taskAttempt(s, task)
	if err != nil {
		return err
	}
	if t.Status != contract.TaskReview {
		return next(refused("not_in_review", "%s is %s, not waiting to be checked.", t.ID, t.Status), "whaleshark show "+t.ID)
	}
	if a.State != contract.AttemptChecking {
		switch {
		case s.Run.Paused != nil:
			return paused(false)
		case t.BrowserCheck && len(a.Report.Evidence) == 0:
			return refused("no_evidence", "%s needs a browser check and its report has no evidence.", t.ID)
		}
		if err := pass(s, t, now); err != nil {
			return err
		}
		setState(a, contract.AttemptChecking, now)
		a.Accept = &contract.Accepting{StartedAt: now}
	}
	a.Accept.CheckedOID, a.Accept.ExpectedTip = c.OID, c.Tip
	return nil
}

func (r Rules) Checked(s *contract.State, task string, result contract.CheckResult, how contract.Accepted, now time.Time) ([]string, error) {
	t, a, err := r.taskAttempt(s, task)
	if err != nil {
		return nil, err
	}
	byHand := how.How == howByHand
	switch {
	case a == nil || a.State != contract.AttemptChecking:
		return nil, next(refused("not_checking", "%s is not being checked.", t.ID), "whaleshark accept "+t.ID)
	case how.How != howCheck && !byHand:
		return nil, refuse(contract.ExitUsage, "bad_how", "Work is accepted by its check or by hand.")
	case byHand != (t.Check == contract.CheckNone):
		return nil, refused("by_hand", "Only a task whose check is none is accepted by hand, and such a task only so.")
	}
	if !byHand {
		if result.At.IsZero() {
			result.At = now
		}
		a.Check = &result
		if !result.OK {
			setState(a, contract.AttemptReported, now)
			a.Accept = nil
			raise(s, "check_failed", t, a, result.Tail, nil, now)
			return nil, nil
		}
	}
	end(s, a, contract.AttemptAccepted, contract.ExitReported, now)
	how.At = now
	t.Status, t.Accepted, t.SettledAt, t.SyncBrief = contract.TaskDone, &how, now, ""
	if in := s.Run.Integration; in != nil && how.Commit != "" {
		in.Tip, s.Run.Scan.Due = how.Commit, true
	}
	return promote(s), nil
}

// Unchecked ends an accept in which no check ran: the result waits again.
func (r Rules) Unchecked(s *contract.State, task string, now time.Time) error {
	t, a, err := r.taskAttempt(s, task)
	if err != nil {
		return err
	}
	if a == nil || a.State != contract.AttemptChecking {
		return next(refused("not_checking", "%s is not being checked.", t.ID), "whaleshark accept "+t.ID)
	}
	setState(a, contract.AttemptReported, now)
	a.Accept = nil
	return nil
}

func (r Rules) Reject(s *contract.State, task, why string, agent contract.Liveness, now time.Time) error {
	t, a, err := r.taskAttempt(s, task)
	if err != nil {
		return err
	}
	switch {
	case t.Status != contract.TaskReview:
		return refused("not_in_review", "%s is %s; there is no result to send back.", t.ID, t.Status)
	case a.State == contract.AttemptChecking:
		return refused("checking", "%s is being accepted. Wait for it.", t.ID)
	case agent == contract.Live:
		setState(a, contract.AttemptWorking, now)
		a.Round++
		a.Report, a.Check = nil, nil
		a.Seen.LastWorking = now
		t.Status = contract.TaskRunning
		// A worker that was told to report once will not report twice
		// unless it is told to: seen with a real agent.
		mail(s, a, "Your result was sent back: "+why+"\nMend it, update the result file, then report again.", "", now)
	case agent == contract.Gone:
		end(s, a, contract.AttemptStopped, contract.ExitExited, now)
		back(t, false, now)
		t.Decisions = append(t.Decisions, contract.Decision{Question: "Why was the last result sent back?", Answer: why, At: now})
	default:
		return next(refused("unverifiable", "The terminals cannot say whether the agent of %s is still there.", t.ID), "whaleshark show "+t.ID+" --screen")
	}
	return nil
}

func (r Rules) Stop(s *contract.State, task string, keepTab bool, now time.Time) error {
	t, a, err := r.taskAttempt(s, task)
	if err != nil {
		return err
	}
	switch {
	case a == nil || !a.State.Live():
		return refused("no_live_attempt", "%s has no live attempt.", t.ID)
	case a.State == contract.AttemptChecking:
		return refused("checking", "%s is being accepted. Wait for it, or end the accept.", t.ID)
	}
	cause := contract.ExitClosedByUs
	if keepTab {
		cause = contract.ExitStopped
	}
	end(s, a, contract.AttemptStopped, cause, now)
	back(t, false, now)
	return nil
}
