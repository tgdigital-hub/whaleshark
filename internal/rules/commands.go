// Package rules is every legal change to a run, as pure functions on the
// record: no file, no herdr, and no clock but the one passed in.
package rules

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Rules implements contract.Rules. It holds nothing.
type Rules struct{}

var _ contract.Rules = Rules{}

// Plug puts the rules into the kit. The package binds no command.
func Plug(k *contract.Kit) { k.Rules = Rules{} }

func refuse(exit int, code, format string, a ...any) *contract.Refusal {
	return &contract.Refusal{Exit: exit, Code: code, Message: fmt.Sprintf(format, a...)}
}

func refused(code, format string, a ...any) *contract.Refusal {
	return refuse(contract.ExitRefused, code, format, a...)
}

func next(r *contract.Refusal, lines ...string) *contract.Refusal {
	r.Next = lines
	return r
}

func paused(human bool) error {
	if human {
		return next(refused("paused", "All work is paused."), "whaleshark resume --human")
	}
	return next(refused("paused", "All work is paused. Change nothing until the resumed event."), "whaleshark wait")
}

// current is the task's last attempt, or nil before the first start.
func current(s *contract.State, t *contract.Task) *contract.Attempt {
	if len(t.Attempts) == 0 {
		return nil
	}
	return s.Attempts[t.Attempts[len(t.Attempts)-1]]
}

// taskAttempt resolves a task and its current attempt.
func (r Rules) taskAttempt(s *contract.State, task string) (*contract.Task, *contract.Attempt, error) {
	t, err := r.FindTask(s, task)
	if err != nil {
		return nil, nil, err
	}
	return t, current(s, t), nil
}

func attempt(s *contract.State, id string) (*contract.Attempt, *contract.Task, error) {
	a := s.Attempts[id]
	if a == nil {
		return nil, nil, refuse(contract.ExitMissing, "no_such_attempt", "There is no attempt %s in run %s.", id, s.Run.ID)
	}
	return a, s.Tasks[a.Task], nil
}

func setState(a *contract.Attempt, state contract.AttemptState, now time.Time) {
	a.State, a.StateSince = state, now
}

// end makes an attempt final. Its open questions are closed; one that was
// answered and not yet handed over is kept for the next attempt's prompt.
func end(s *contract.State, a *contract.Attempt, state contract.AttemptState, cause string, now time.Time) {
	setState(a, state, now)
	a.EndedAt, a.Accept = now, nil
	a.Exit = &contract.Exit{Cause: cause, At: now}
	closeAsks(s, a, now)
	withdraw(s, contract.CausePrompt, a.ID)
}

func closeAsks(s *contract.State, a *contract.Attempt, now time.Time) {
	for _, q := range questions(s) {
		if q.Attempt != a.ID || q.State == contract.QuestionUsed || q.State == contract.QuestionClosed {
			continue
		}
		if q.State == contract.QuestionAnswered && q.Kind == contract.KindAsk {
			decide(s.Tasks[a.Task], q, now)
		}
		q.State = contract.QuestionClosed
	}
}

// back returns a task whose attempt ended without being accepted to ready,
// or to failed at the third failure.
func back(t *contract.Task, failure bool, now time.Time) {
	if failure {
		t.Failures++
	}
	t.Status = contract.TaskReady
	if t.Failures >= contract.MaxFailures {
		t.Status, t.SettledAt = contract.TaskFailed, now
	}
}

func decide(t *contract.Task, q *contract.Question, now time.Time) {
	t.Decisions = append(t.Decisions, contract.Decision{Question: q.Text, Answer: q.Answer, At: now})
}

// questions lists the questions oldest first, so that what they cause
// happens in one order.
func questions(s *contract.State) []*contract.Question {
	out := make([]*contract.Question, 0, len(s.Questions))
	for _, q := range s.Questions {
		out = append(out, q)
	}
	slices.SortFunc(out, func(a, b *contract.Question) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(len(a.ID), len(b.ID)), strings.Compare(a.ID, b.ID))
	})
	return out
}

func raise(s *contract.State, kind string, t *contract.Task, a *contract.Attempt, text string, data map[string]any, now time.Time) {
	e := contract.Event{Kind: kind, Text: text, Data: data}
	if t != nil {
		e.Task = t.ID
	}
	if a != nil {
		e.Attempt = a.ID
	}
	Rules{}.Raise(s, e, now)
}
