package view

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const (
	asYouNote = "(answers and approvals recorded as yours that did not come from a pane or the page)"
	askedText = "the person asked to be caught up"
)

// catchup prints what happened while the person was away, or since a time
// they give, from the record alone. It tells the lead agent that they asked.
func catchup(c *contract.Call) (any, error) {
	var since time.Time
	if v := c.Flags["since"]; v != nil {
		// A time of day is the last such moment; a full time is itself.
		day, err := time.Parse("15:04", v[0])
		y, m, d := c.Now.Date()
		if since = time.Date(y, m, d, day.Hour(), day.Minute(), 0, 0, c.Now.Location()); since.After(c.Now) {
			since = since.AddDate(0, 0, -1)
		}
		if err != nil {
			if since, err = time.Parse(time.RFC3339, v[0]); err != nil {
				return nil, usage("--since takes a time of day (21:16) or a full time (RFC 3339).")
			}
		}
	}
	if len(c.Args) > 0 {
		return nil, usage("catchup takes no argument.")
	}
	s, in, v, err := look(c)
	if r, ok := err.(*contract.Refusal); ok && r.Code == "no_run" {
		fmt.Fprintln(c.Out, noRun)
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	past, err := c.Kit.Reader().History(c.Root, c.Run)
	if err != nil {
		return nil, unreadable("history", err)
	}
	block := away(v, s, append(past, s.Inbox.Events...), in.Limits, first(since, in.UI.LastHere, s.Run.CreatedAt))
	catchText(c.Out, block, width())
	// The lead agent is told that the person asked, while there is a lead
	// agent to tell: a closed run has none.
	if c.Caller.Kind == contract.Human && s.Run.ClosedAt.IsZero() {
		_ = c.Kit.Store.Change(c.Root, c.Run, func(s *contract.State) error {
			c.Kit.Rules.Raise(s, contract.Event{Kind: "human", Text: askedText, Data: map[string]any{"what": "catchup"}}, c.Now)
			return nil
		})
	}
	return block, nil
}

// away builds the block from a view with every card in it, the record and
// its events. A task stands on one line: one whose check failed is told
// there, also once it has passed.
func away(v *contract.View, s *contract.State, events []contract.Event, limits contract.ProjectFile, from time.Time) *contract.Catchup {
	to := v.At
	in := func(t time.Time) bool { return t.After(from) && !t.After(to) }
	name := func(task string) string { return clean(s.Tasks[task].Name) }
	at := func(t time.Time) string { return t.In(to.Location()).Format("15:04") }
	c := &contract.Catchup{From: from, To: to}
	line := func(parts []string, sep string) contract.CatchLine {
		return contract.CatchLine{N: len(parts), Text: strings.Join(parts, sep)}
	}

	var parts []string
	for _, it := range append(slices.Clone(v.Items), v.Held...) {
		switch {
		case it.Source == sourceWorker:
			parts = append(parts, "a question from "+it.Name)
		case it.Name == "" || it.Name == leadName:
			parts = append(parts, it.Source)
		default:
			parts = append(parts, it.Source+": "+it.Name)
		}
	}
	c.Waiting = line(parts, ", ")

	// The last failed check of each task and the last time each went quiet,
	// earliest first.
	failed, quiet := map[string]contract.Event{}, map[string]contract.Event{}
	for _, e := range events {
		switch {
		case s.Tasks[e.Task] == nil || s.Attempts[e.Attempt] == nil:
		case e.Kind == "check_failed" && !e.At.After(to):
			failed[e.Task] = e
		case e.Kind == "quiet" && in(e.At):
			quiet[e.Task] = e
		}
	}
	byTime := func(m map[string]contract.Event) []contract.Event {
		out := make([]contract.Event, 0, len(m))
		for _, e := range m {
			out = append(out, e)
		}
		slices.SortFunc(out, func(x, y contract.Event) int { return cmp.Or(x.At.Compare(y.At), cmp.Compare(x.Seq, y.Seq)) })
		return out
	}
	parts = nil
	for _, e := range byTime(failed) {
		// A result is sent back with a message: the first one after the check.
		t, mail := s.Tasks[e.Task], s.Attempts[e.Attempt].Mail
		story, last := []string{"failed at " + at(e.At)}, e.At
		if i := slices.IndexFunc(mail, func(m contract.Mail) bool { return !m.At.Before(e.At) }); i >= 0 {
			story, last = append(story, "sent back at "+at(mail[i].At)), mail[i].At
		}
		if t.Status == contract.TaskDone && t.Accepted != nil && !t.Accepted.At.Before(e.At) {
			story, last = append(story, "passed at "+at(t.Accepted.At)), t.Accepted.At
		}
		if !in(last) {
			delete(failed, e.Task)
			continue
		}
		if !in(e.At) {
			story = story[1:]
		}
		parts = append(parts, name(e.Task)+": "+strings.Join(story, ", "))
	}
	c.CheckFailed = line(parts, " · ")

	var done []*contract.Task
	for _, t := range s.Tasks {
		if _, told := failed[t.ID]; t.Status == contract.TaskDone && in(t.SettledAt) && !told {
			done = append(done, t)
		}
	}
	slices.SortFunc(done, func(x, y *contract.Task) int {
		return cmp.Or(x.SettledAt.Compare(y.SettledAt), strings.Compare(x.ID, y.ID))
	})
	parts = nil
	for _, t := range done {
		parts = append(parts, clean(t.Name))
	}
	c.Finished = line(parts, ", ")

	parts = nil
	for _, f := range s.Run.Findings {
		if len(f.Tasks) != 2 || len(f.Files) == 0 || s.Tasks[f.Tasks[0]] == nil || s.Tasks[f.Tasks[1]] == nil {
			continue
		}
		x, y := s.Tasks[f.Tasks[0]], s.Tasks[f.Tasks[1]]
		both := name(x.ID) + " and " + name(y.ID) + " both change"
		switch sorted := x.Status == contract.TaskDone && y.Status == contract.TaskDone; {
		case sorted && !in(x.SettledAt) && !in(y.SettledAt):
		case sorted:
			parts = append(parts, both+"d "+clean(f.Files[0])+"; sorted, both accepted")
		default:
			parts = append(parts, both+" "+clean(f.Files[0])+"; not sorted yet")
		}
	}
	c.Clashes = line(parts, " · ")

	// The sweep says "quiet" once an agent has rested for the project's quiet
	// time; what it said or reported next is the first sign that it went on.
	parts = nil
	for _, e := range byTime(quiet) {
		a, card := s.Attempts[e.Attempt], cardOf(v, e.Task)
		var end time.Time
		if r := a.Report; r != nil && r.At.After(e.At) {
			end = r.At
		}
		if p := a.Progress; p != nil && p.At.After(e.At) && (end.IsZero() || p.At.Before(end)) {
			end = p.At
		}
		state := wordOf(*card)
		switch card.Look {
		case contract.LookIdle:
			end, state = to, "still idle"
		case contract.LookBuilding:
			state += " again"
		}
		text := name(e.Task)
		if !end.IsZero() {
			start := e.At.Add(-time.Duration(limits.Limits.QuietSeconds) * time.Second)
			text += ", " + plural(int(end.Sub(start)/time.Minute), "minute", "minutes")
		}
		parts = append(parts, text+"; "+state)
	}
	c.WentIdle = line(parts, " · ")

	for _, q := range s.Questions {
		if by := q.AnsweredBy; q.For == contract.ForOrchestrator && by != nil && by.Caller == contract.Orchestrator && in(q.AnsweredAt) {
			c.AnsweredLead++
		}
	}
	c.DoneAsYou = contract.CatchLine{N: asYou(s, events, from, to), Text: asYouNote}
	for _, sec := range v.Sections {
		for _, card := range sec.Cards {
			switch {
			case card.Look == contract.LookBuilding || card.Look == contract.LookAsking:
				c.StillBuilding++
			case card.Look == contract.LookStopped && in(card.Since):
				c.Stopped++
			case card.Look == contract.LookFailed && in(card.Since):
				c.Failed++
			}
		}
	}
	return c
}

// asYou counts what the record holds as the person's doing that came from
// neither a pane nor the page: a typed --human, an answer passed on. A human
// event says where it came from in its data, and names the item it answered.
func asYou(s *contract.State, events []contract.Event, from, to time.Time) int {
	n, told := 0, map[string]bool{}
	count := func(at time.Time, by *contract.Origin, item string) {
		if by == nil || at.After(to) || !at.After(from) || told[item] {
			return
		}
		inside := by.Where == contract.WherePane || by.Where == contract.WherePage || by.Where == contract.WherePhone
		if by.Where == contract.WhereRelayed || by.Caller == contract.Human && !inside {
			n++
		}
	}
	for _, e := range events {
		if where, _ := e.Data["where"].(string); e.Kind == "human" && where != "" {
			count(e.At, &contract.Origin{Caller: contract.Human, Where: where}, "")
			if id, _ := e.Data["id"].(string); id != "" {
				told[id] = true
			}
		}
	}
	for _, q := range s.Questions {
		count(q.AnsweredAt, q.AnsweredBy, q.ID)
		for _, u := range q.Earlier {
			count(u.At, u.By, q.ID)
		}
	}
	return n
}

// catchText prints the block, the same words every time.
func catchText(w io.Writer, c *contract.Catchup, width int) {
	p := &printer{out: new(strings.Builder), o: Options{Width: width}}
	from, day := c.From.In(c.To.Location()), "15:04"
	if from.Format(time.DateOnly) != c.To.Format(time.DateOnly) {
		day = "2 Jan 15:04"
	}
	fmt.Fprintf(p.out, " WHILE YOU WERE AWAY   %s  (%s to %s)\n\n", age(c.To.Sub(from)), from.Format(day), c.To.Format(day))
	for _, r := range []struct {
		label string
		contract.CatchLine
	}{
		{"Waiting for you now", c.Waiting}, {"Finished and checked", c.Finished}, {"Check failed", c.CheckFailed},
		{"Clashes", c.Clashes}, {"Went idle", c.WentIdle}, {"Answered by the lead", contract.CatchLine{N: c.AnsweredLead}},
		{"Done as you", c.DoneAsYou},
	} {
		p.line(fmt.Sprintf(" %-22s%-4d", r.label, r.N), clean(r.Text), "")
	}
	fmt.Fprintf(p.out, " %-22s%-4dStopped  %d   Failed  %d\n", "Still building", c.StillBuilding, c.Stopped, c.Failed)
	io.WriteString(w, p.out.String())
}
