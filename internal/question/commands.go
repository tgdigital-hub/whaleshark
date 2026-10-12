// Package question is the commands a question and an item go through: a
// worker's ask, which blocks until its answer may be used, and answer,
// escalate and need (6.6, 8b, 8d, 8n). The rules decide what is legal; this
// package locks, waits, and says who is to be told.
package question

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) {
	k.Handle("ask", ask)
	k.Handle("answer", answer)
	k.Handle("escalate", escalate)
	k.Handle("need", need)
}

// askLock is held by a worker blocked in ask: that is how answer knows
// somebody is waiting.
func askLock(c *contract.Call, run, id string) string {
	return contract.AskLock(c.Kit.Store.Dir(c.Root, run), id)
}

// fleet names the two runs a step crosses (19.1): from a fleet's run the
// orchestrator's above it, and from a twin, when one is named, the fleet's
// run below. Both are empty for a step that stays in the caller's run.
func fleet(c *contract.Call, twin string) (up, low string) {
	s, err := c.Kit.Store.Read(c.Root, c.Run)
	switch {
	case err != nil:
	case twin == "" && s.Run.Parent != nil:
		return s.Run.Parent.Run, c.Run
	case twin != "" && s.Questions[twin] != nil && s.Questions[twin].Down != nil:
		return c.Run, s.Questions[twin].Down.Run
	}
	return "", ""
}

func usage(c *contract.Call, format string, a ...any) error {
	return &contract.Refusal{Exit: contract.ExitUsage, Code: "usage",
		Message: fmt.Sprintf(format, a...), Next: []string{"whaleshark help " + c.Command.Name}}
}

// said is a free text of the caller's: it must be text and say something.
func said(c *contract.Call, what, v string) (string, error) {
	if v = strings.TrimSpace(v); v == "" || cli.Valid("text", v, "") != nil {
		return "", usage(c, "%s is empty, or is not text.", what)
	}
	return v, nil
}

// options is what --options was given: each is the label of a button.
func options(c *contract.Call) ([]string, error) {
	var out []string
	for _, o := range contract.List(c.Flags["options"]...) {
		o = strings.TrimSpace(o)
		if err := cli.Valid("title", o, ""); err != nil {
			return nil, usage(c, "--options: %v.", err)
		}
		out = append(out, o)
	}
	return out, nil
}

// change is one locked step on the caller's run, taken once the rules allow
// the caller the command. It returns the record as it was saved, and whether
// the step left the lead agent a new event. An item of the tool's own that
// the step brought up is nudged for. With up and low named it is one step on
// both runs under the project's lock, and fn gets the other run second.
func change(c *contract.Call, sub, up, low string, fn func(s, other *contract.State) error) (s *contract.State, raised bool, err error) {
	fresh := false
	step := func(now, other *contract.State) error {
		if err := c.Kit.Rules.Allowed(now, c.Caller, c.Command.Name, sub); err != nil {
			return err
		}
		before := len(now.Inbox.Events)
		err := fn(now, other)
		if err == nil {
			fresh = trust(c, now)
		}
		s, raised = now, len(now.Inbox.Events) > before
		return err
	}
	switch {
	case up == "":
		err = c.Kit.Store.Change(c.Root, c.Run, func(now *contract.State) error { return step(now, nil) })
	case up == c.Run:
		err = c.Kit.Store.Both(c.Root, up, low, step)
	default:
		err = c.Kit.Store.Both(c.Root, up, low, func(over, now *contract.State) error { return step(now, over) })
	}
	if errors.Is(err, contract.ErrNoRun) {
		err = &contract.Refusal{Exit: contract.ExitMissing, Code: "no_run", Message: "No run is open here.", Next: []string{"whaleshark run list"}}
	}
	if err == nil && fresh {
		nudge(c)
	}
	return s, raised, err
}

// overdue is how long a worker's question may wait for the lead agent before
// the person is shown that it waits (8e).
const overdue = 10 * time.Minute

const textTrust = "This project's settings would run or reach what nobody has approved (%s). Look at it in a tab of your own: whaleshark trust --human"

// trust keeps the tool's own item about project settings that nobody has
// approved in step with its cause, inside a step that is being saved. It is
// raised once while the project file's reader holds something back, and
// closes when it holds nothing; one the person has dealt with is not raised
// again while the cause lasts. It reports whether it raised the item.
func trust(c *contract.Call, s *contract.State) bool {
	project, err := contract.ReadProjectFile(c.Root)
	if err != nil {
		return false // settings that cannot be read say nothing
	}
	for _, q := range s.Questions {
		if q.From == contract.FromTool && q.Cause == contract.CauseTrust && q.State != contract.QuestionClosed {
			if len(project.Held) == 0 {
				q.State = contract.QuestionClosed
			}
			return false
		}
	}
	if len(project.Held) == 0 {
		return false
	}
	_, err = c.Kit.Rules.Need(s, contract.Question{Form: contract.FormTodo, From: contract.FromTool, Cause: contract.CauseTrust,
		Text: fmt.Sprintf(textTrust, strings.Join(project.Held, ", "))}, c.Now)
	return err == nil
}

// pointLead types one of the fixed pointers into the tab of a run's lead
// agent when no wait is running there and the tab is idle (8e). It is best effort: what
// it points at is safe in the record. Nothing is typed while all work is paused.
func pointLead(c *contract.Call, s *contract.State, p contract.Pointer, arg string) bool {
	o, k := s.Run.Orchestrator, c.Kit
	if o == nil || s.Run.Paused != nil {
		return false
	}
	unlock, free, err := k.Platform.TryLock(filepath.Join(k.Store.Dir(c.Root, s.Run.ID), contract.WaitLock))
	if err != nil || !free {
		return false
	}
	unlock()
	picture, err := k.Terms.Snapshot(context.Background())
	if err != nil {
		return false
	}
	resting := func(pane contract.Pane) bool {
		return pane.ID == o.Pane && pane.Agent != "" && pane.Status == contract.StatusIdle
	}
	return slices.ContainsFunc(picture.Panes, resting) && k.Terms.Point(o.Pane, p, arg) == nil
}

// unread points an idle lead agent at the events a step has just left it.
func unread(c *contract.Call, s *contract.State) {
	if n := len(s.Inbox.Events); n > 0 {
		pointLead(c, s, contract.PointEvents, strconv.Itoa(n))
	}
}

// nudge calls for the person once an item of theirs is in the record. The
// notifier's pass decides whether, how and when: it knows Do not disturb.
func nudge(c *contract.Call) { c.Kit.Notifier.Items(c.Root, c.Run, nil, c.Now) }

// button reports whether an answer was one click or one key: given in a
// pane, on the page or on a phone, and not a typed line, whose Enter was the
// confirmation. Only such an answer settles before it may be used (8n).
func button(q *contract.Question, text string, by contract.Origin) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	typed := q.Form == contract.FormQuestion || by.Where == contract.WhereTyped ||
		strings.HasPrefix(t, contract.AnswerOther) || strings.HasPrefix(t, contract.AnswerSendBack) || strings.HasPrefix(t, contract.AnswerCannot)
	return by.Caller == contract.Human && !typed
}

// settling is how long a button's answer can be taken back before anything
// may use it: [ui] settle_seconds of the person's own settings.
func settling(k *contract.Kit) time.Duration {
	cfg := contract.PersonDefaults()
	if dirs, err := k.Platform.Dirs(); err == nil {
		cfg, _ = contract.ReadPerson(k.Platform.Peek, dirs.Config)
	}
	return time.Duration(max(0, cfg.UI.SettleSeconds)) * time.Second
}

// answered is what answer reports. Told says how the answer reaches whoever
// asked: "waiting" (its ask is blocked and will return it), "pointer" (the
// one line was typed into its tab), "mail" (a late answer, sent to the
// attempt now at work), or nothing: it is in the record for the next wait,
// ask --resume or Resume.
type answered struct {
	ID        string    `json:"id"`
	State     string    `json:"state"`
	Answer    string    `json:"answer,omitempty"`
	SettlesAt time.Time `json:"settles_at,omitzero"`
	Told      string    `json:"told,omitempty"`
}

func answer(c *contract.Call) (any, error) {
	undo := c.Flags["undo"] != nil
	if len(c.Args) < 1 || undo != (len(c.Args) == 1) || len(c.Args) > 2 {
		return nil, usage(c, "answer takes an id and the answer, or an id and --undo.")
	}
	id, text := c.Args[0], ""
	if err := cli.Valid("id", id, ""); err != nil {
		return nil, usage(c, "%v.", err)
	}
	by, up, low := contract.Origin{Caller: c.Caller.Kind, Where: c.Caller.Where}, "", ""
	if !undo {
		up, low = fleet(c, id)
	}
	if by.Caller == contract.Orchestrator && c.Flags["relayed"] != nil {
		by.Where = contract.WhereRelayed
	}
	if !undo {
		var err error
		if text, err = said(c, "The answer", c.Args[1]); err != nil {
			return nil, err
		}
	}
	fresh, mailed, down := false, false, false
	var under *contract.State
	s, raised, err := change(c, "", up, low, func(s, other *contract.State) error {
		r := c.Kit.Rules
		if undo {
			return r.Undo(s, id, by, c.Now)
		}
		q, settle := s.Questions[id], time.Duration(0)
		if q != nil && button(q, text, by) {
			settle = settling(c.Kit)
		}
		fresh = q != nil && q.Answer == ""
		err := r.Answer(s, id, text, by, settle, c.Now)
		if err == nil && fresh && by.Caller == contract.Human && q.For == contract.ForOrchestrator {
			// The lead agent was asked and the person answered: it is told so,
			// and not what, which nothing may use before it has settled.
			r.Raise(s, contract.Event{Kind: "human", Task: q.Task, Attempt: q.Attempt, Text: "The person answered " + q.ID + " themselves.",
				Data: map[string]any{"what": "answer", "id": q.ID, "where": by.Where}}, c.Now)
		}
		if err == nil && fresh && q.State == contract.QuestionClosed {
			// A late answer is with the task's decisions, which an attempt
			// already at work has not read: it gets the answer as a message.
			_, none := r.Tell(s, q.Task, "Decided: "+q.Text+" Answer: "+q.Answer, "", c.Now)
			mailed = none == nil
		}
		if err == nil && fresh && by.Caller == contract.Leader {
			q.AnsweredBy.Caller = contract.Orchestrator // its fleet's lead agent, as its workers know it
		}
		if under = other; err == nil && other != nil {
			// A twin's answer goes down in the same step once it has settled:
			// the person's at the first sweep after it has, or when it is given again.
			down, err = r.Down(s, other, id, c.Now)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	q := s.Questions[id]
	out := answered{ID: id, State: q.State, Answer: q.Answer, SettlesAt: q.SettlesAt}
	switch {
	case undo:
		fmt.Fprintf(c.Out, "%s is open again.\n", id)
	case q.State == contract.QuestionClosed && q.Attempt != "":
		fmt.Fprintf(c.Out, "%s's attempt has ended; your answer will be in the next attempt's prompt.\n", q.Task)
	case q.State == contract.QuestionClosed:
		fmt.Fprintf(c.Out, "%s is closed; your answer is kept with the decisions of %s.\n", id, q.Task)
	case !fresh && !down:
		fmt.Fprintf(c.Out, "%s has that answer already.\n", id)
	default:
		if out.Told = tell(c, s, q, by, raised); down {
			out.Told = tell(c, under, under.Questions[q.Down.ID], by, false)
		}
		fmt.Fprintf(c.Out, "%s answered.\n", id)
		if left := q.SettlesAt.Sub(c.Now); left > 0 && by.Caller == contract.Human {
			fmt.Fprintf(c.Out, "It can be taken back for %v: whaleshark answer %s --undo --human\n", left, id)
		}
	}
	if mailed {
		out.Told = "mail"
		fmt.Fprintf(c.Out, "%s's attempt at work now gets it as a message.\n", q.Task)
	}
	return out, nil
}

// tell does what a new answer needs done outside the record, and says how it
// reaches whoever asked. A worker blocked in ask, like the lead agent in
// need --wait, holds the lock of what it asked and returns the answer
// itself; a worker that has stopped waiting gets the one
// line that says to resume, never the answer (8b step 7). The lead agent is
// pointed at an item of its own that the person answered, and at the event
// that says the person answered in its place. A twin has nobody waiting: its
// other half in the fleet's run has, and a leader is pointed at the answer
// to what it handed up.
func tell(c *contract.Call, s *contract.State, q *contract.Question, by contract.Origin, raised bool) string {
	if s.Run.Paused != nil {
		return ""
	}
	if raised {
		unread(c, s)
	}
	if unlock, free, err := c.Kit.Platform.TryLock(askLock(c, s.Run.ID, q.ID)); err == nil && !free {
		return "waiting"
	} else if free {
		unlock()
	}
	if q.Kind == contract.KindNeed || q.Down != nil {
		if q.From == contract.FromLead && (by.Caller == contract.Human || q.Up != nil) {
			pointLead(c, s, contract.PointAnswer, "")
		}
		return ""
	}
	if a := s.Attempts[q.Attempt]; a != nil && c.Kit.Terms.Point(a.Place.Pane, contract.PointAnswered, q.ID) == nil {
		return "pointer"
	}
	return ""
}

func escalate(c *contract.Call) (any, error) {
	if len(c.Args) != 1 || cli.Valid("id", c.Args[0], "") != nil {
		return nil, usage(c, "escalate takes the id of one question.")
	}
	id, twin := c.Args[0], ""
	up, low := fleet(c, "")
	var over *contract.State
	if _, _, err := change(c, "", up, low, func(s, above *contract.State) (err error) {
		if over = above; above == nil {
			return c.Kit.Rules.Escalate(s, id, c.Now)
		}
		twin, err = c.Kit.Rules.Twin(above, s, id, c.Now)
		return err
	}); err != nil {
		return nil, err
	}
	if over != nil {
		// In a fleet a question climbs one level: to the orchestrator (19.1).
		unread(c, over)
		fmt.Fprintf(c.Out, "%s is now for the orchestrator, as %s of run %s. Its answer comes back by itself.\n", id, twin, up)
		return map[string]string{"id": id, "for": contract.ForOrchestrator, "twin": twin}, nil
	}
	nudge(c)
	fmt.Fprintf(c.Out, "%s is now for the person, in the action pane. Do not ask it again in the chat.\n", id)
	return map[string]string{"id": id, "for": contract.ForHuman}, nil
}

var forms = []string{contract.FormQuestion, contract.FormChoice, contract.FormSignoff, contract.FormTodo}

func need(c *contract.Call) (any, error) {
	form, blocks := "", c.Flags["wait"] != nil
	if len(c.Args) > 0 {
		form = c.Args[0]
	}
	switch {
	case form == "close":
		if len(c.Args) != 2 || cli.Valid("id", c.Args[1], "") != nil {
			return nil, usage(c, "need close takes the id of one item.")
		}
		id := c.Args[1]
		if _, _, err := change(c, form, "", "", func(s, _ *contract.State) error { return c.Kit.Rules.CloseQuestion(s, id, c.Now) }); err != nil {
			return nil, err
		}
		fmt.Fprintf(c.Out, "%s is withdrawn.\n", id)
		return map[string]string{"id": id, "state": contract.QuestionClosed}, nil
	case !slices.Contains(forms, form):
		return nil, cli.NoSuch("form of an item", form, append(slices.Clone(forms), "close"), "whaleshark help need")
	case len(c.Args) != 2:
		return nil, usage(c, "need %s takes one text: what the person is asked.", form)
	case form != contract.FormChoice && c.Flags["options"] != nil:
		return nil, usage(c, "--options is for a choice.")
	case !blocks && c.Flags["timeout"] != nil:
		return nil, usage(c, "--timeout is for --wait.")
	}
	q := contract.Question{Form: form, Urgent: c.Flags["urgent"] != nil, Holds: c.Flags["holds"] != nil}
	var err error
	if q.Text, err = said(c, "The text", c.Args[1]); err != nil {
		return nil, err
	}
	if q.Options, err = options(c); err != nil {
		return nil, err
	}
	if t := c.Flags["task"]; t != nil {
		q.Task = t[len(t)-1]
	}
	id := ""
	up, low := fleet(c, "")
	var over *contract.State
	_, _, err = change(c, form, up, low, func(s, above *contract.State) (err error) {
		if id, err = c.Kit.Rules.Need(s, q, c.Now); err == nil && above != nil {
			// A leader's item is the orchestrator's to answer or to pass on (19.1).
			over = above
			var twin string
			if twin, err = c.Kit.Rules.Twin(above, s, id, c.Now); err == nil {
				// A twin that a killed need left alone above takes this item's words.
				above.Questions[twin].Form, above.Questions[twin].Text, above.Questions[twin].Options = form, q.Text, s.Questions[id].Options
			}
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if nudge(c); over != nil {
		unread(c, over)
	}
	if blocks {
		// The lead agent has no resume: what comes after the time is up is
		// its wait, which hands the answer over as an answered event.
		return wait(c, id, "", " Its answer will come as an answered event.", "whaleshark wait")
	}
	fmt.Fprintln(c.Out, id)
	return map[string]string{"id": id}, nil
}
