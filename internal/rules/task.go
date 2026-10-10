package rules

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const (
	maxWords = 3
	maxName  = 24
	lead     = contract.Lead // what tell and jump call the lead agent; no task may take it
)

func (r Rules) AddTask(s *contract.State, t contract.Task, taken []string, now time.Time) error {
	if err := open(s); err != nil {
		return err
	}
	if t.Check == "" {
		return refuse(contract.ExitUsage, "no_check", "A task needs a check: a command, or the word none.")
	}
	if err := name(s, &t, taken); err != nil {
		return err
	}
	for id, other := range s.Tasks {
		if strings.EqualFold(id, t.ID) || strings.EqualFold(other.Name, t.ID) {
			return refused("id_taken", "The id %s is taken by %s %q.", t.ID, id, other.Name)
		}
	}
	if strings.EqualFold(t.ID, lead) {
		return refused("id_taken", "The id %s means the lead agent.", t.ID)
	}
	if err := known(s, t.After); err != nil {
		return err
	}
	t.Status, t.Failures, t.Attempts, t.Accepted = contract.TaskPending, 0, nil, nil
	t.CreatedAt, t.SettledAt = now, time.Time{}
	s.Tasks[t.ID] = &t
	promote(s)
	return nil
}

// name fills in and checks the name a task's tab and card carry: three words
// and 24 characters at most, and on no other tab of the login.
func name(s *contract.State, t *contract.Task, taken []string) error {
	given := t.Name != ""
	if !given {
		t.Name = t.Title
	}
	words := strings.Fields(t.Name)
	t.Name = strings.Join(words, " ")
	if len(words) == 0 || len(words) > maxWords || utf8.RuneCountInString(t.Name) > maxName {
		if !given {
			return refuse(contract.ExitUsage, "name_needed", "%q is too long to name a tab. Give a name of at most three words with --name.", t.Title)
		}
		return refuse(contract.ExitUsage, "bad_name", "A name is at most three words and %d characters: %q is not.", maxName, t.Name)
	}
	taken = slices.Clone(taken)
	for id, other := range s.Tasks {
		taken = append(taken, id, other.Name)
	}
	if slices.ContainsFunc(append(taken, lead), func(n string) bool { return strings.EqualFold(n, t.Name) }) {
		return refused("name_taken", "The name %q is taken. Give another with --name.", t.Name)
	}
	return nil
}

func known(s *contract.State, ids []string) error {
	for _, id := range ids {
		if s.Tasks[id] == nil {
			return refuse(contract.ExitMissing, "no_such_task", "There is no task %s to wait for.", id)
		}
	}
	return nil
}

func unmet(s *contract.State, t *contract.Task) (ids []string) {
	for _, id := range t.After {
		if s.Tasks[id].Status != contract.TaskDone {
			ids = append(ids, id)
		}
	}
	return ids
}

// promote moves every task between pending and ready as its dependencies
// say, and returns those that became ready, in order.
func promote(s *contract.State) (ready []string) {
	for id, t := range s.Tasks {
		waiting := len(unmet(s, t)) > 0
		switch {
		case t.Status == contract.TaskPending && !waiting:
			t.Status = contract.TaskReady
			ready = append(ready, id)
		case t.Status == contract.TaskReady && waiting:
			t.Status = contract.TaskPending
		}
	}
	slices.Sort(ready)
	return ready
}

func (r Rules) EditTask(s *contract.State, id string, edit contract.TaskEdit, now time.Time) ([]string, error) {
	t, err := r.FindTask(s, id)
	if err != nil {
		return nil, err
	}
	switch t.Status {
	case contract.TaskPending, contract.TaskReady, contract.TaskFailed:
	default:
		return nil, refused("not_editable", "%s is %s; only a pending, ready or failed task can be changed.", t.ID, t.Status)
	}
	if edit.Check != nil && *edit.Check == "" {
		return nil, refuse(contract.ExitUsage, "no_check", "A task needs a check: a command, or the word none.")
	}
	if edit.After != nil {
		if err := known(s, *edit.After); err != nil {
			return nil, err
		}
		if loops(s, t.ID, *edit.After) {
			return nil, refused("loop", "%s would wait for itself.", t.ID)
		}
		t.After = slices.Clone(*edit.After)
	}
	if edit.Brief != nil {
		t.Brief = *edit.Brief
	}
	if edit.Check != nil {
		t.Check = *edit.Check
	}
	if edit.Owns != nil {
		t.Owns = slices.Clone(*edit.Owns)
	}
	return promote(s), nil
}

// loops reports whether id can be reached from any of after.
func loops(s *contract.State, id string, after []string) bool {
	for _, dep := range after {
		if dep == id || loops(s, id, s.Tasks[dep].After) {
			return true
		}
	}
	return false
}

func (r Rules) CancelTask(s *contract.State, id string, now time.Time) error {
	t, a, err := r.taskAttempt(s, id)
	if err != nil {
		return err
	}
	switch {
	case t.Status == contract.TaskDone || t.Status == contract.TaskCancelled:
		return refused("final", "%s is %s, and that is final.", t.ID, t.Status)
	case a != nil && a.State.Live():
		return next(refused("still_live", "%s has a live attempt. Stop it first.", t.ID), "whaleshark stop "+t.ID)
	}
	t.Status, t.SettledAt = contract.TaskCancelled, now
	for _, q := range s.Questions {
		if q.Task == t.ID && (q.State == contract.QuestionOpen || q.State == contract.QuestionAnswered) {
			q.State = contract.QuestionClosed
		}
	}
	return nil
}

func (r Rules) ResetTask(s *contract.State, id string, now time.Time) error {
	t, err := r.FindTask(s, id)
	if err != nil {
		return err
	}
	if t.Status != contract.TaskFailed {
		return refused("not_failed", "%s is %s; only a failed task can be reset.", t.ID, t.Status)
	}
	t.Status, t.Failures, t.SettledAt = contract.TaskPending, 0, time.Time{}
	promote(s)
	return nil
}

func (Rules) FindTask(s *contract.State, idOrName string) (*contract.Task, error) {
	if t := s.Tasks[idOrName]; t != nil {
		return t, nil
	}
	for _, t := range s.Tasks {
		if strings.EqualFold(t.Name, idOrName) {
			return t, nil
		}
	}
	return nil, next(refuse(contract.ExitMissing, "no_such_task", "There is no task %q in run %s.", idOrName, s.Run.ID), "whaleshark status")
}

func (r Rules) Looked(s *contract.State, task string, now time.Time) error {
	t, err := r.FindTask(s, task)
	if err == nil {
		t.SeenAt = now
	}
	return err
}

func (r Rules) Synced(s *contract.State, task, brief string, now time.Time) (int, error) {
	t, err := r.FindTask(s, task)
	switch {
	case err != nil:
		return 0, err
	case t.Worktree == nil:
		return 0, refused("no_worktree", "%s has no copy of the code of its own, so there is nothing to bring up to date.", t.ID)
	case t.Status == contract.TaskDone || t.Status == contract.TaskCancelled:
		return 0, refused("settled", "%s is %s.", t.ID, t.Status)
	}
	t.Synced, t.SyncBrief = t.Synced+1, brief
	return t.Synced, nil
}

func (r Rules) SetWorktree(s *contract.State, task string, w *contract.Worktree, now time.Time) error {
	t, err := r.FindTask(s, task)
	if err != nil {
		return err
	}
	if w != nil {
		w = new(*w)
		if w.CreatedAt.IsZero() {
			w.CreatedAt = now
		}
	}
	t.Worktree = w
	return nil
}
