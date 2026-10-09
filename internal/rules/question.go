package rules

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

func question(s *contract.State, id string) (*contract.Question, error) {
	if q := s.Questions[id]; q != nil {
		return q, nil
	}
	return nil, refuse(contract.ExitMissing, "no_such_question", "There is no question or item %s in run %s.", id, s.Run.ID)
}

func (Rules) Ask(s *contract.State, id, text string, options []string, now time.Time) (string, error) {
	a, t, err := attempt(s, id)
	switch {
	case err != nil:
		return "", err
	case a.State == contract.AttemptAsked:
		return "", refused("already_asked", "%s has a question open already. One at a time.", a.ID)
	case a.State != contract.AttemptWorking:
		return "", gone(a)
	}
	s.Counters.Question++
	q := &contract.Question{
		ID: "q" + strconv.Itoa(s.Counters.Question), Kind: contract.KindAsk, Form: contract.FormQuestion,
		For: contract.ForOrchestrator, From: a.ID, Task: t.ID, Attempt: a.ID,
		Text: text, Options: options, State: contract.QuestionOpen, CreatedAt: now,
	}
	if len(options) > 0 {
		q.Form = contract.FormChoice
	}
	s.Questions[q.ID] = q
	setState(a, contract.AttemptAsked, now)
	raise(s, "question", t, a, text, map[string]any{"id": q.ID}, now)
	return q.ID, nil
}

var forms = []string{contract.FormQuestion, contract.FormChoice, contract.FormSignoff, contract.FormTodo}

func (r Rules) Need(s *contract.State, q contract.Question, now time.Time) (string, error) {
	if err := open(s); err != nil {
		return "", err
	}
	if !slices.Contains(forms, q.Form) {
		return "", refuse(contract.ExitUsage, "bad_form", "An item is a question, a choice, a signoff or a todo.")
	}
	if q.Task != "" {
		t, err := r.FindTask(s, q.Task)
		if err != nil {
			return "", err
		}
		if q.Task = t.ID; q.Holds && (t.Status == contract.TaskDone || t.Status == contract.TaskCancelled) {
			return "", refused("settled", "%s is %s; nothing can hold it.", t.ID, t.Status)
		}
	} else if q.Holds {
		return "", refuse(contract.ExitUsage, "no_task", "An item holds a task: name it with --task.")
	}
	if q.Form == contract.FormChoice && len(q.Options) == 0 {
		q.Options = []string{"yes", "no"}
	}
	if q.From != contract.FromTool {
		q.From, q.Cause = contract.FromLead, ""
	}
	s.Counters.Need++
	q.ID, q.Kind, q.For = "n"+strconv.Itoa(s.Counters.Need), contract.KindNeed, contract.ForHuman
	q.State, q.CreatedAt = contract.QuestionOpen, now
	q.Answer, q.AnsweredAt, q.AnsweredBy, q.SettlesAt, q.UsedAt, q.Earlier = "", time.Time{}, nil, time.Time{}, time.Time{}, nil
	s.Questions[q.ID] = &q
	return q.ID, nil
}

func (Rules) Escalate(s *contract.State, id string, now time.Time) error {
	q, err := question(s, id)
	if err != nil {
		return err
	}
	if q.State != contract.QuestionOpen {
		return refuse(contract.ExitMissing, "not_open", "%s is %s; there is nothing open to hand to the person.", q.ID, q.State)
	}
	q.For = contract.ForHuman
	return nil
}

// answer checks a text against the form of its item and returns it as it is stored.
func answer(q *contract.Question, text string) (string, bool) {
	text = strings.TrimSpace(text)
	lower := strings.ToLower(text)
	free := func(prefix string) bool {
		rest, ok := strings.CutPrefix(lower, prefix)
		return ok && strings.TrimSpace(rest) != ""
	}
	switch q.Form {
	case contract.FormChoice:
		if i := slices.IndexFunc(q.Options, func(o string) bool { return strings.EqualFold(o, text) }); i >= 0 {
			return q.Options[i], true
		}
		return text, free(contract.AnswerOther)
	case contract.FormSignoff:
		return text, lower == contract.AnswerApprove || free(contract.AnswerSendBack)
	case contract.FormTodo:
		return text, lower == contract.AnswerDone || free(contract.AnswerCannot)
	}
	return text, text != ""
}

func (Rules) Answer(s *contract.State, id, text string, by contract.Origin, settle time.Duration, now time.Time) error {
	q, err := question(s, id)
	if err != nil {
		return err
	}
	text, ok := answer(q, text)
	switch {
	case by.Caller != contract.Human && by.Caller != contract.Orchestrator:
		return refused("not_yours", "Only the lead agent or the person answers %s.", q.ID)
	case q.For == contract.ForHuman && by.Caller != contract.Human && by.Where != contract.WhereRelayed:
		return refused("for_human", "%s is for the person. Pass on what they told you with --relayed.", q.ID)
	case !ok:
		return refuse(contract.ExitUsage, "bad_answer", "That is not an answer a %s takes.", q.Form)
	case q.Answer == text:
		return nil
	case q.State == contract.QuestionUsed:
		return refused("used", "%s was answered %q, and that has been acted on.", q.ID, q.Answer)
	case q.Answer != "":
		return refused("answered", "%s was answered %q already.", q.ID, q.Answer)
	case q.State == contract.QuestionClosed && q.Task == "":
		return refused("closed", "%s was withdrawn.", q.ID)
	}
	q.Answer, q.AnsweredAt, q.AnsweredBy = text, now, &by
	if q.State == contract.QuestionClosed {
		decide(s.Tasks[q.Task], q, now)
		return nil
	}
	q.State, q.SettlesAt = contract.QuestionAnswered, now.Add(settle)
	return nil
}

func (Rules) Undo(s *contract.State, id string, by contract.Origin, now time.Time) error {
	q, err := question(s, id)
	switch {
	case err != nil:
		return err
	case by.Caller != contract.Human:
		return refused("not_human", "Only the person takes an answer back.")
	case q.State == contract.QuestionUsed || q.State == contract.QuestionClosed && q.Answer != "":
		return refused("used", "Already acted on; tell the lead agent.")
	case q.State != contract.QuestionAnswered:
		return refused("not_answered", "%s has no answer to take back.", q.ID)
	}
	shelve(q, &by, now)
	q.State = contract.QuestionOpen
	return nil
}

// shelve moves an answer to the earlier ones: nothing a person did is dropped.
func shelve(q *contract.Question, by *contract.Origin, now time.Time) {
	q.Earlier = append(q.Earlier, contract.Undone{Answer: q.Answer, At: q.AnsweredAt, By: by, UndoneAt: now})
	q.Answer, q.AnsweredAt, q.AnsweredBy, q.SettlesAt = "", time.Time{}, nil, time.Time{}
}

func settled(s *contract.State, q *contract.Question, now time.Time) bool {
	return q.State == contract.QuestionAnswered && s.Run.Paused == nil && !now.Before(q.SettlesAt)
}

func (Rules) Use(s *contract.State, id string, now time.Time) (string, bool, error) {
	q, err := question(s, id)
	switch {
	case err != nil:
		return "", false, err
	case q.State == contract.QuestionUsed:
		return q.Answer, true, nil
	case q.State == contract.QuestionClosed && q.Kind == contract.KindAsk:
		return "", false, refused("closed", "Your attempt has ended.")
	case q.State == contract.QuestionClosed:
		return "", false, refused("closed", "%s was withdrawn.", q.ID)
	case !settled(s, q, now):
		return "", false, nil
	case q.From == contract.FromTool:
		// Nobody asked: the answer only clears the item, which closes with its cause.
		return q.Answer, true, nil
	}
	use(s, q, now)
	return q.Answer, true, nil
}

// use makes an answer final and hands it on: the asking attempt works
// again, a held task gets its decision, and the lead agent is told what the
// person said.
func use(s *contract.State, q *contract.Question, now time.Time) {
	q.State, q.UsedAt = contract.QuestionUsed, now
	a, t := s.Attempts[q.Attempt], s.Tasks[q.Task]
	if a != nil && a.State == contract.AttemptAsked {
		setState(a, contract.AttemptWorking, now)
		a.Herdr.LastWorking = now
	}
	if q.Holds && t != nil {
		decide(t, q, now)
		if a := current(s, t); a != nil && a.State.Live() {
			mail(s, a, "Decided: "+q.Text+" Answer: "+q.Answer, "", now)
		}
	}
	if q.For == contract.ForHuman && q.From != contract.FromTool {
		raise(s, "answered", t, a, q.Answer, map[string]any{"id": q.ID}, now)
	}
}

// pass lets a start or an accept go past the items that hold a task, which
// makes their answers final, or refuses while one is open or still settling.
func pass(s *contract.State, t *contract.Task, now time.Time) error {
	var held []*contract.Question
	for _, q := range questions(s) {
		if !q.Holds || q.Task != t.ID || q.State == contract.QuestionUsed || q.State == contract.QuestionClosed {
			continue
		}
		if !settled(s, q, now) {
			e := refused("held", "%s is held by %s until that is answered and the answer has settled.", t.ID, q.ID)
			e.Data = map[string]any{"item": q.ID}
			return next(e, "whaleshark status --items")
		}
		held = append(held, q)
	}
	for _, q := range held {
		use(s, q, now)
	}
	return nil
}

func (Rules) CloseQuestion(s *contract.State, id string, now time.Time) error {
	q, err := question(s, id)
	switch {
	case err != nil:
		return err
	case q.Kind != contract.KindNeed:
		return refused("not_an_item", "%s is a worker's question; it closes when it is answered or its attempt ends.", q.ID)
	case q.State == contract.QuestionUsed:
		return refused("used", "%s was answered, and that has been acted on.", q.ID)
	}
	q.State = contract.QuestionClosed
	return nil
}
