package rules

import (
	"cmp"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

func pending(q *contract.Question) bool {
	return q != nil && (q.State == contract.QuestionOpen || q.State == contract.QuestionAnswered)
}

// asks puts one of the tool's own items in front of the person, or brings
// the one that waits up to date. An answer given to what it showed before,
// or to an older fingerprint (fresh), is taken back: a yes covers what was
// seen and nothing else.
func asks(s *contract.State, id string, q contract.Question, fresh bool, now time.Time) string {
	old := s.Questions[id]
	if !pending(old) {
		q.From = contract.FromTool
		id, _ = Rules{}.Need(s, q, now)
		return id
	}
	if fresh || fmt.Sprint(old.Text, old.Tasks, old.Set) != fmt.Sprint(q.Text, q.Tasks, q.Set) {
		if old.Answer != "" {
			shelve(old, old.AnsweredBy, now)
		}
		old.State, old.Text, old.Tasks, old.Set = contract.QuestionOpen, q.Text, q.Tasks, q.Set
	}
	return id
}

// listed names the first files of a list, and counts the rest.
func listed(files []string) string {
	if n := len(files) - 3; n > 0 {
		return strings.Join(files[:3], ", ") + " (and " + strconv.Itoa(n) + " more)"
	}
	return strings.Join(files, ", ")
}

func (Rules) Planned(s *contract.State, tasks []string, text string, by *contract.Origin, now time.Time) *contract.Refusal {
	lack := contract.Unplanned(s, tasks)
	if len(lack) == 0 || s.Run.Parent != nil || open(s) != nil {
		return nil
	}
	p := cmp.Or(s.Run.Plan, &contract.Approval{})
	s.Run.Plan = p
	if by != nil && by.Caller == contract.Human && by.Where != contract.WhereRelayed {
		p.Approved, p.At, p.By = append(p.Approved, lack...), now, by
		return nil
	}
	p.Item = asks(s, p.Item, contract.Question{Form: contract.FormSignoff, Cause: contract.CausePlan, Text: text, Tasks: lack}, false, now)
	return next(refused(contract.RefusedPlanFirst, "Plan first is on: the person has not approved this plan yet. It is in front of them now."), "whaleshark wait")
}

func (r Rules) Protect(s *contract.State, task, seen string, files []string, now time.Time) bool {
	if s.Run.Parent != nil || open(s) != nil || s.Run.Protect == nil && seen == "" {
		return false
	}
	p := cmp.Or(s.Run.Protect, &contract.Approval{})
	q, fresh := s.Questions[p.Item], seen != p.Seen
	s.Run.Protect, p.Seen, p.Files = p, seen, files
	switch t := s.Tasks[task]; {
	case seen == "" || seen == p.Print:
		if pending(q) {
			q.State = contract.QuestionClosed
		}
	case fresh || !pending(q):
		p.Item = asks(s, p.Item, contract.Question{Form: contract.FormSignoff, Cause: contract.CauseProtect, Task: cmp.Or(t, &contract.Task{}).ID,
			Text: "Protected files changed in this job"}, true, now) // its first line: the view lists the files under it
		raise(s, "protected", t, nil, cmp.Or(task, "This job")+" changed protected files: "+listed(files)+". The person must approve them before this job can land. Carry on.",
			map[string]any{"files": files, "id": p.Item}, now)
		return true
	}
	return false
}

func (Rules) Landable(s *contract.State, seen string, files []string) error {
	if p := s.Run.Protect; seen == "" || p != nil && p.Print == seen {
		return nil
	}
	return next(refused(contract.RefusedProtected, "Nothing was landed: this job changes protected files the person has not approved: %s. They approve them in the action pane.", listed(files)), "whaleshark status --items")
}

// spending is the guard's state and its open choice for a job, or for one
// fleet of it: the choice names a group of the fleet as its task.
func spending(s *contract.State, fleet string) (sp *contract.Spend, group string, item *contract.Question) {
	sp = &s.Run.Spend
	if f := s.Run.Fleets[fleet]; f != nil && len(f.Groups) > 0 {
		sp, group = &f.Spend, f.Groups[0]
	} else if fleet != "" {
		return nil, "", nil
	}
	for _, q := range s.Questions {
		if t := s.Tasks[q.Task]; q.Cause == contract.CauseBudget && pending(q) && (t == nil && fleet == "" || t != nil && t.Fleet == fleet) {
			item = q
		}
	}
	return sp, group, item
}

// Spend also lets a hold go that the person's settings no longer ask for,
// and puts back a hold or a choice that was taken away while it is owed. A
// choice carries the grant it offers as a change of the limit (Question.Set).
func (r Rules) Spend(s *contract.State, fleet string, spent time.Duration, limit int, now time.Time) (did string) {
	sp, group, q := spending(s, fleet)
	if sp == nil || s.Run.Parent != nil || open(s) != nil {
		return ""
	}
	who, key := cmp.Or(fleet, "This job"), map[bool]string{true: "spend.job", false: "spend.fleet"}[fleet == ""]
	text := strconv.FormatFloat(math.Round(spent.Hours()*10)/10, 'f', -1, 64) + " of " + strconv.Itoa(limit) + " agent-hours used"
	switch over := limit != contract.Unlimited && spent >= time.Duration(limit)*time.Hour; {
	case over:
		if !sp.Held {
			sp.Held, sp.Warned, did = true, true, contract.SpendHeld
			raise(s, "budget", s.Tasks[group], nil, text+": held", map[string]any{"fleet": fleet, "held": true}, now)
		}
		if fleet == "" {
			r.Pause(s, contract.HeldBy, now)
		}
		if more := max(1, int(float64(limit)*contract.SpendMore+0.5)); q == nil {
			r.Need(s, contract.Question{Form: contract.FormChoice, From: contract.FromTool, Cause: contract.CauseBudget, Urgent: true, Task: group,
				Text:    fmt.Sprintf("%s has used its %d agent-hours", who, limit), // its first line: the view says who is held
				Options: []string{contract.AnswerMore, contract.AnswerNoLimit, contract.AnswerLeave},
				Set:     &contract.SetAsk{Key: key, From: strconv.Itoa(limit), To: strconv.Itoa(limit + more)}}, now)
		}
	case sp.Held:
		if unhold(s, sp, fleet, now); q != nil {
			q.State = contract.QuestionClosed
		}
	case !sp.Warned && limit != contract.Unlimited && spent.Hours() >= contract.SpendWarnAt*float64(limit):
		sp.Warned, did = true, contract.SpendWarned
		raise(s, "budget", s.Tasks[group], nil, text, map[string]any{"fleet": fleet}, now)
	}
	return did
}

// unhold ends a hold, and the guard's own pause of a job; a fleet's run is Hold's.
func unhold(s *contract.State, sp *contract.Spend, fleet string, now time.Time) {
	if p := s.Run.Paused; fleet == "" && p != nil && p.By == contract.HeldBy {
		Rules{}.Resume(s, now)
	}
	sp.Held = false
}

func (r Rules) Hold(up, low *contract.State, now time.Time) bool {
	name, _ := group(up, low)
	f, p := up.Run.Fleets[name], low.Run.Paused
	switch held := f != nil && (up.Run.Spend.Held || f.Spend.Held); {
	case f == nil || open(low) != nil:
	case held && p == nil:
		return r.Pause(low, contract.HeldBy, now) == nil
	case !held && p != nil && p.By == contract.HeldBy:
		return r.Resume(low, now) == nil
	}
	return false
}

func (Rules) SetAsked(s *contract.State, ask contract.SetAsk, text string, now time.Time) (id string, err error) {
	if err := open(s); err != nil {
		return "", err
	}
	for _, q := range questions(s) {
		switch {
		case q.Cause != contract.CauseSetting || q.Set == nil || q.Set.Key != ask.Key:
		case q.Answer == contract.AnswerLeave:
			return "", refused(contract.RefusedPersonOnly, "The person left %s as it is in this job, and is not asked again: it is theirs to set.", ask.Key)
		case pending(q):
			id = q.ID
		}
	}
	return asks(s, id, contract.Question{Form: contract.FormSignoff, Cause: contract.CauseSetting, Text: text, Set: &ask}, false, now), nil
}

// decided makes final a settled answer that the person alone gives, writes
// what it decides and hands it to the lead agent. A hold that is left
// standing is not final: that answer can still be taken back.
func (r Rules) decided(s *contract.State, q *contract.Question, now time.Time) {
	run, yes := &s.Run, q.Answer == contract.AnswerApprove
	switch {
	case q.Cause == contract.CausePlan && yes && run.Plan != nil:
		run.Plan.Approved = append(run.Plan.Approved, contract.Unplanned(s, q.Tasks)...)
		run.Plan.At, run.Plan.By = now, q.AnsweredBy
	case q.Cause == contract.CauseProtect && yes && run.Protect != nil && run.Protect.Item == q.ID:
		run.Protect.Print, run.Protect.At, run.Protect.By = run.Protect.Seen, now, q.AnsweredBy
	case q.Cause == contract.CauseBudget:
		fleet, grant := cmp.Or(s.Tasks[q.Task], &contract.Task{}).Fleet, cmp.Or(q.Set, &contract.SetAsk{})
		sp, _, _ := spending(s, fleet)
		from, _ := strconv.Atoi(grant.From)
		to, _ := strconv.Atoi(grant.To)
		switch {
		case sp == nil || q.Answer != contract.AnswerMore && q.Answer != contract.AnswerNoLimit:
			return
		case q.Answer == contract.AnswerNoLimit:
			sp.Off = true
		default:
			sp.More += max(0, to-from)
		}
		unhold(s, sp, fleet, now)
	}
	q.State, q.UsedAt = contract.QuestionUsed, now
	raise(s, "answered", s.Tasks[q.Task], nil, q.Answer, map[string]any{"id": q.ID, "cause": q.Cause}, now)
}
