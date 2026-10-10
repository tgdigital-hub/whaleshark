package rules

import (
	"fmt"
	"slices"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const defaultLimit = 20

func (Rules) NewRun(id, objective string, limit int, base *contract.GitRef, by contract.Binding, now time.Time) *contract.State {
	if limit <= 0 {
		limit = defaultLimit
	}
	by.BoundAt = now
	return &contract.State{
		Version:   contract.StateVersion,
		Run:       contract.Run{ID: id, Objective: objective, CreatedAt: now, Orchestrator: &by, Limit: limit, Base: base},
		Tasks:     map[string]*contract.Task{},
		Attempts:  map[string]*contract.Attempt{},
		Questions: map[string]*contract.Question{},
		Inbox:     contract.Inbox{Events: []contract.Event{}},
	}
}

func open(s *contract.State) error {
	if !s.Run.ClosedAt.IsZero() {
		return refused("closed", "Run %s is closed.", s.Run.ID)
	}
	return nil
}

func live(s *contract.State) (n int) {
	for _, a := range s.Attempts {
		if a.State.Live() {
			n++
		}
	}
	return n
}

func (Rules) CloseRun(s *contract.State, abandon bool, now time.Time) error {
	if err := open(s); err != nil {
		return err
	}
	if n := live(s); n > 0 {
		return next(refused("still_live", "%d attempts are still live.", n), "whaleshark status")
	}
	unlanded := false
	for _, t := range s.Tasks {
		if t.Status == contract.TaskReview {
			return next(refused("in_review", "%s is still waiting to be checked.", t.ID), "whaleshark show "+t.ID)
		}
		unlanded = unlanded || t.Accepted != nil && t.Accepted.Commit != ""
	}
	if in := s.Run.Integration; unlanded && !abandon && in != nil && in.Landed != in.Tip {
		return next(refused("not_landed", "Accepted work has not been landed."), "whaleshark land", "whaleshark run close --abandon")
	}
	s.Run.ClosedAt = now
	return nil
}

// Takeover from the pane the run is already bound to changes nothing, so a
// lead agent cannot fence itself.
func (Rules) Takeover(s *contract.State, by contract.Binding, now time.Time) error {
	if err := open(s); err != nil {
		return err
	}
	if o := s.Run.Orchestrator; o != nil && o.Pane == by.Pane {
		return nil
	}
	by.BoundAt = now
	s.Run.Orchestrator = &by
	s.Run.Gen++
	return nil
}

func (Rules) Pause(s *contract.State, by string, now time.Time) error {
	if err := open(s); err != nil {
		return err
	}
	if s.Run.Paused == nil {
		s.Run.Paused = &contract.Pause{At: now, By: by}
		raise(s, "paused", nil, nil, "", nil, now)
	}
	return nil
}

// Resume also starts the quiet clock again: an agent that was held idle has
// not been quiet.
func (Rules) Resume(s *contract.State, now time.Time) error {
	if s.Run.Paused == nil {
		return refused("not_paused", "Run %s is not paused.", s.Run.ID)
	}
	s.Run.Paused = nil
	for _, a := range s.Attempts {
		if a.State == contract.AttemptWorking {
			a.Seen.LastWorking = now
		}
	}
	raise(s, "resumed", nil, nil, "", nil, now)
	return nil
}

// waits lists what is refused while the run is paused. A worker's own
// commands are not among them: what it was saying is still recorded.
var waits = []string{"task", "start", "escalate", "need", "tell", "accept", "reject", "close", "sync", "land", "run close", "run rm", "team run"}

func (Rules) Allowed(s *contract.State, c contract.Caller, command, sub string) error {
	cmd := contract.Find(command)
	if cmd == nil {
		return next(refuse(contract.ExitUsage, "unknown_command", "There is no command %q.", command), "whaleshark help")
	}
	if s != nil && c.Kind == contract.Human && c.Pane != "" && agentPane(s, c.Pane) {
		return refused("not_human", "This pane belongs to an agent, so nothing here is done as the person.")
	}
	if !cmd.May(c.Kind, sub) {
		switch c.Kind {
		case contract.Worker:
			return refused("worker", "A worker may not run %s. Use report, progress, ask and mail.", command)
		case contract.Unbound:
			return next(refused("not_bound", "No run is bound to this pane."), "whaleshark run takeover")
		}
		if !cmd.May(contract.Human, sub) {
			return refused("worker_only", "Only a worker runs %s.", command)
		}
		return refused("human_only", "Only the person may run %s.", command)
	}
	if s == nil || cmd.May(contract.Unbound, sub) {
		return nil
	}
	if o := s.Run.Orchestrator; c.Kind == contract.Orchestrator && (o == nil || o.Pane != c.Pane) {
		return next(refused("not_bound", "Run %s is bound to another pane.", s.Run.ID), "whaleshark run takeover")
	}
	if c.Kind == contract.Worker && s.Attempts[c.Attempt] == nil {
		return refused("not_active", "This attempt is not part of run %s. Stop.", s.Run.ID)
	}
	if command != "run" {
		if err := open(s); err != nil {
			return err
		}
	}
	held := slices.Contains(waits, command) || slices.Contains(waits, command+" "+sub)
	if s.Run.Paused != nil && c.Kind != contract.Worker && held && command+" "+sub != "need close" {
		return paused(c.Kind == contract.Human)
	}
	return nil
}

func agentPane(s *contract.State, pane string) bool {
	if o := s.Run.Orchestrator; o != nil && o.Pane == pane {
		return true
	}
	for _, a := range s.Attempts {
		if a.State.Live() && a.Place.Pane == pane {
			return true
		}
	}
	return false
}

func (Rules) Integrated(s *contract.State, in contract.Integration) error {
	if err := open(s); err != nil {
		return err
	}
	if s.Run.Integration == nil {
		s.Run.Integration = &contract.Integration{}
	}
	for _, f := range []struct{ to, from *string }{
		{&s.Run.Integration.Branch, &in.Branch}, {&s.Run.Integration.Tip, &in.Tip}, {&s.Run.Integration.Landed, &in.Landed}} {
		if *f.from != "" {
			*f.to = *f.from
		}
	}
	return nil
}

func (Rules) Stuck(s *contract.State, on bool, now time.Time) bool {
	raised := on && !s.Run.StuckFlagged
	if s.Run.StuckFlagged = on; raised {
		raise(s, "stuck", nil, nil, "", nil, now)
	}
	return raised
}

// Found knows a finding again by its kind, its tasks and its files, so one
// that is still there after the next scan is not said twice.
func (Rules) Found(s *contract.State, findings []contract.Finding, now time.Time) (fresh []contract.Finding) {
	key := func(f contract.Finding) string { return fmt.Sprint(f.Kind, f.Tasks, f.Files) }
	had := map[string]bool{}
	for _, f := range s.Run.Findings {
		had[key(f)] = true
	}
	for _, f := range findings {
		if had[key(f)] {
			continue
		}
		had[key(f)], fresh = true, append(fresh, f)
		kind := map[string]string{"overlap": "overlap", "lands": "overlap", "scope": "scope"}[f.Kind]
		if kind == "" || len(f.Tasks) == 0 {
			continue
		}
		data := map[string]any{"kind": f.Kind, "tasks": f.Tasks, "files": f.Files}
		raise(s, kind, s.Tasks[f.Tasks[len(f.Tasks)-1]], nil, f.How, data, now)
	}
	s.Run.Findings, s.Run.Scan = findings, contract.ScanSeen{At: now}
	return fresh
}
