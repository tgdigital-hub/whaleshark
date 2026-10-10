package view

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// messages prints what was said both ways and what happened, in the order
// it happened: today's, or with --all everything, or one task's.
func messages(c *contract.Call) (any, error) {
	if len(c.Args) > 0 {
		return nil, usage("log takes no argument.")
	}
	s, in, v, err := lookOr(c)
	if s == nil {
		return nil, err
	}
	scope, only := "today", ""
	if c.Flags["all"] != nil {
		scope = "all"
	}
	if f := c.Flags["task"]; f != nil {
		t, err := c.Kit.Rules.FindTask(s, f[0])
		if err != nil {
			return nil, err
		}
		only, scope = t.ID, scope+" · "+t.ID
	}
	past, err := c.Kit.Reader().History(c.Root, c.Run)
	if err != nil {
		return nil, unreadable("history", err)
	}
	day := func(t time.Time) string { return t.In(c.Now.Location()).Format(time.DateOnly) }
	list := slices.DeleteFunc(said(s, append(past, s.Inbox.Events...), in.UI.LastHere), func(m contract.Message) bool {
		return only != "" && m.Task != only || c.Flags["all"] == nil && day(m.At) != day(c.Now)
	})
	saidText(c.Out, v, list, scope, options(c))
	return list, nil
}

// said gathers the messages of a run from its record and its events, the
// oldest first. What the record holds in full is taken from there: a start
// with its brief, each message down, the last progress note, a report, a
// check, each question and each answer with those taken back. Everything
// else is an event, and so is each failed check that has one, since the
// record keeps only the last. New is what came since the person was last here.
func said(s *contract.State, events []contract.Event, lastHere time.Time) []contract.Message {
	var out []contract.Message
	add := func(at time.Time, dir string, look contract.Look, task, kind, text string) *contract.Message {
		out = append(out, contract.Message{At: at, Dir: dir, Look: look, Task: task, Kind: kind, Text: cli.Plain(text)})
		return &out[len(out)-1]
	}
	failed := map[string]bool{}
	for _, e := range events {
		failed[e.Kind+e.Attempt+e.At.String()] = true
	}
	for _, id := range slices.Sorted(maps.Keys(s.Attempts)) {
		a := s.Attempts[id]
		t := s.Tasks[a.Task]
		if t == nil {
			continue
		}
		add(a.StartedAt, ">", "", t.ID, "brief", t.Title)
		for _, m := range a.Mail {
			add(m.At, ">", "", t.ID, "message", m.Text)
		}
		if p := a.Progress; p != nil {
			add(p.At, "<", "", t.ID, fmt.Sprint(p.Pct, "%"), p.Note).Dim = true
		}
		if r := a.Report; r != nil {
			add(r.At, "<", map[bool]contract.Look{true: contract.LookToCheck, false: contract.LookFailed}[r.Outcome == contract.ReportDone], t.ID, r.Outcome, r.Summary)
		}
		if k := a.Check; k != nil && k.OK {
			add(k.At, "", contract.LookDone, t.ID, "check passed", k.Tail)
		} else if k != nil && !failed["check_failed"+a.ID+k.At.String()] {
			add(k.At, "", contract.LookCheckFailed, t.ID, "check failed", k.Tail)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(s.Questions)) {
		q := s.Questions[id]
		up, down, look := "", "", contract.LookNeedsYou
		if q.Kind == contract.KindAsk {
			up, down = "<", ">"
		}
		if q.For != contract.ForHuman {
			look = contract.LookAsking
		}
		add(q.CreatedAt, up, look, q.Task, formWord(q.Form), news(q.Text))
		for _, u := range q.Earlier {
			m := add(u.At, down, "", q.Task, "answer", u.Answer)
			m.By, m.Struck = u.By, true
		}
		if !q.AnsweredAt.IsZero() {
			add(q.AnsweredAt, down, "", q.Task, "answer", q.Answer).By = q.AnsweredBy
		}
	}
	looks := map[string]contract.Look{"check_failed": contract.LookCheckFailed, "blocked": contract.LookAtPrompt, "exited": contract.LookFailed, "start_failed": contract.LookFailed}
	for _, e := range events {
		switch e.Kind {
		case "done", "failed", "question", "answered":
		case "human":
			// What was done as the person: the command, on what, and from where.
			what, _ := e.Data["what"].(string)
			on, _ := e.Data["on"].(string)
			where, _ := e.Data["where"].(string)
			if s.Tasks[on] == nil {
				on = e.Task
			}
			add(e.At, "", "", on, what, e.Text).By = &contract.Origin{Caller: contract.Human, Where: where}
		default:
			add(e.At, "", looks[e.Kind], e.Task, strings.ReplaceAll(e.Kind, "_", " "), e.Text)
		}
	}
	slices.SortStableFunc(out, func(x, y contract.Message) int { return x.At.Compare(y.At) })
	for i := range out {
		out[i].New = !out[i].Dim && !lastHere.IsZero() && out[i].At.After(lastHere)
	}
	return out
}

var from = map[string]string{contract.WherePane: "by you, from the pane", contract.WherePage: "by you, from the page",
	contract.WherePhone: "by you, from your phone", contract.WhereRelayed: "relayed by the lead agent"}

// by says where something done as the person came from.
func by(o *contract.Origin) string {
	switch {
	case o == nil:
		return ""
	case from[o.Where] == "" && o.Caller == contract.Human:
		return "by you, typed"
	}
	return from[o.Where]
}

// saidText prints the messages, one line each: when, the mark of what it
// made of its task, which way it went, the task, what it was and the text.
func saidText(w io.Writer, v *contract.View, list []contract.Message, scope string, o Options) {
	p := newPrinter(v, o)
	clock, fresh := "15:04", 0
	if strings.HasPrefix(scope, "all") {
		clock = "02 Jan 15:04"
	}
	for _, m := range list {
		p.measure(row{id: m.Task, word: m.Kind})
		if m.New {
			fresh++
		}
	}
	p.alerts()
	p.line("MESSAGES  ", fmt.Sprint(fresh, " new · ", clean(scope)), v.At.Format("2 Jan 15:04"))
	p.out.WriteByte('\n')
	for _, m := range list {
		mark, text, trail := " ", clean(m.Text), by(m.By)
		if m.Look != "" {
			mark = p.paint(contract.Looks[order[m.Look]].Colour, p.mark(m.Look))
		}
		if m.Struck && o.Colours == nil {
			text += " (taken back)"
		}
		text = p.styled(m.Struck, "9", "29", text)
		if m.Dim {
			text = p.paint("dim", text)
		}
		if m.New {
			trail = strings.TrimSpace(trail + "  " + p.styled(true, "1", "22", "NEW"))
		}
		p.line(" "+m.At.In(v.At.Location()).Format(clock)+" "+mark+pad(m.Dir, 3)+pad(clean(m.Task), p.idw)+pad(clean(m.Kind), p.wordw+2), text, trail)
	}
	p.fresh(w)
}
