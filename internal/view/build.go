package view

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const (
	// askAfter is how long a question to the lead agent may wait before it
	// is put in front of the person as well.
	askAfter = 10 * time.Minute
	// answeredKept is how many of the last answers the view carries.
	answeredKept = 10

	newsPrompt   = "waiting at a permission prompt in its tab"
	newsExited   = "its agent ended without reporting"
	newsNoStart  = "its agent did not start"
	sourceWorker = "a question from the worker"
	sourceLate   = "waiting for the lead agent"
	leadName     = "lead agent"
	forHuman     = "waiting for the human"
)

// order is each look's place in the one order, and its row of the table.
var order = func() map[contract.Look]int {
	m := map[contract.Look]int{}
	for i, l := range contract.Looks {
		m[l.Look] = i
	}
	return m
}()

// seat is a card while it is built: the card, its task, its last attempt,
// and whether that attempt still occupies an agent.
type seat struct {
	card *contract.Card
	task *contract.Task
	try  *contract.Attempt
	ask  *contract.Question
	live bool
}

type builder struct {
	in    contract.ViewInput
	s     *contract.State
	panes map[string]*contract.Pane
	seats map[string]*seat
}

// Build makes the one view model from a run, the terminals' picture and the small
// files beside them. It reads nothing and keeps nothing.
func Build(in contract.ViewInput) *contract.View {
	all := in.All
	b := &builder{in: in, s: in.State, seats: map[string]*seat{}}
	if in.Terms != nil {
		b.panes = map[string]*contract.Pane{}
		for i := range in.Terms.Panes {
			b.panes[in.Terms.Panes[i].ID] = &in.Terms.Panes[i]
		}
	}
	v := &contract.View{
		Version: contract.ViewVersion, At: in.Now, Caller: in.Caller, Run: b.s.Run.ID,
		Objective: cli.Plain(b.s.Run.Objective), Items: []contract.Item{}, Sections: []contract.Section{},
		Fresh: contract.Fresh{Checked: in.Checked},
	}
	for _, t := range b.s.Tasks {
		b.seats[t.ID] = b.seat(t)
	}
	b.items(v)
	b.clashes()

	var seats []*seat
	year, month, day := in.Now.Date()
	for _, st := range b.seats {
		c := st.card
		if y, m, d := st.task.SettledAt.In(in.Now.Location()).Date(); c.Look == contract.LookDone && y == year && m == month && d == day {
			v.Counts.DoneToday++
		}
		if order[c.Look] >= order[contract.LookStopped] && !all {
			continue
		}
		b.finish(st)
		seats = append(seats, st)
		switch c.Look {
		case contract.LookNeedsYou:
			v.Counts.NeedYou++
		case contract.LookCheckFailed:
			v.Counts.CheckFailed++
		case contract.LookToCheck:
			v.Counts.ToCheck++
		}
		if st.live {
			v.Counts.Agents++
		}
		if c.Word == contract.WordStillRunning {
			v.Strip.StillRunning++
		}
	}
	slices.SortFunc(seats, func(x, y *seat) int {
		return cmp.Or(cmp.Compare(order[x.card.Look], order[y.card.Look]),
			cmp.Compare(len(x.task.ID), len(y.task.ID)), strings.Compare(x.task.ID, y.task.ID))
	})
	for _, st := range seats {
		name := contract.Looks[order[st.card.Look]].Section
		if n := len(v.Sections); n == 0 || v.Sections[n-1].Name != name {
			v.Sections = append(v.Sections, contract.Section{Name: name})
		}
		last := &v.Sections[len(v.Sections)-1]
		last.Cards = append(last.Cards, *st.card)
	}
	v.Counts.Waiting, v.Counts.Elsewhere = len(v.Items), in.Elsewhere
	for _, it := range v.Items {
		if it.Holds {
			v.Counts.Holding++
		}
	}
	b.around(v)
	return v
}

// seat gives a task its card from what the record says and what the terminals show.
func (b *builder) seat(t *contract.Task) *seat {
	c := &contract.Card{Task: t.ID, Name: cli.Plain(t.Name), Look: contract.LookQueued, Since: t.CreatedAt}
	st := &seat{card: c, task: t}
	if n := len(t.Attempts); n > 0 {
		st.try = b.s.Attempts[t.Attempts[n-1]]
	}
	a := st.try
	if a == nil {
		if t.Status == contract.TaskCancelled {
			c.Look = contract.LookStopped
		}
		if waits := b.unmet(t); len(waits) > 0 {
			c.News = "after " + strings.Join(waits, ", ")
		}
		return st
	}
	st.live = a.State.Live()
	c.Try, c.Agent, c.Since = a.N, cli.Plain(a.Agent.Kind), a.StateSince
	if p := a.Progress; p != nil {
		c.Progress, c.Said, c.News = p.Pct, p.At, cli.Plain(p.Note)
	}
	if f, ok := b.in.Ctx[a.Place.Pane]; ok && f.Known {
		c.Ctx, c.CtxKnown = f.Pct, true
	}
	// What the terminals show now: its picture, or without one what the sweep last saw.
	status, open := a.Seen.Status, a.Place.Tab != ""
	if b.panes != nil {
		p := b.panes[a.Place.Pane]
		if status, open = "", open && p != nil; p != nil && p.Agent != "" {
			status = p.Status
		}
	}
	if open {
		c.Tab = a.Place.Tab
	}
	summary := ""
	if a.Report != nil {
		summary = cli.Plain(a.Report.Summary)
	}

	switch a.State {
	case contract.AttemptStarting:
		c.Look, c.Word = contract.LookBuilding, contract.WordStarting
	case contract.AttemptWorking:
		c.Look = contract.LookBuilding
		if status == "" || status == contract.StatusIdle {
			c.Look, c.Since = contract.LookIdle, first(a.Seen.GoneSince, a.Seen.LastWorking, a.StateSince)
		}
	case contract.AttemptAsked:
		c.Look = contract.LookAsking
	case contract.AttemptReported, contract.AttemptChecking:
		c.Look, c.News = contract.LookToCheck, summary
		if a.State == contract.AttemptChecking {
			c.Word = contract.WordChecking
		} else if a.Check != nil && !a.Check.OK {
			c.Look, c.Since, c.News = contract.LookCheckFailed, a.Check.At, cli.Plain(a.Check.Tail)
		}
	case contract.AttemptStopped:
		c.Look, c.Since = contract.LookStopped, a.EndedAt
	case contract.AttemptFailed, contract.AttemptExited, contract.AttemptStartFailed:
		c.Look, c.Since = contract.LookFailed, a.EndedAt
		switch {
		case a.State == contract.AttemptExited:
			c.News = newsExited
		case a.State == contract.AttemptStartFailed:
			c.News = newsNoStart
		case summary != "":
			c.News = summary
		}
	}
	switch {
	case t.Status == contract.TaskDone:
		c.Look, c.Since = contract.LookDone, t.SettledAt
		if summary != "" {
			c.News = summary
		}
		if t.Accepted != nil && t.Accepted.How == contract.AcceptByHand {
			c.Word, c.News = contract.WordDoneByHand, cli.Plain(t.Accepted.Note)
		}
	case t.Status == contract.TaskCancelled:
		c.Look = contract.LookStopped
	case !st.live:
	case status == contract.StatusBlocked:
		c.Look, c.Since, c.News = contract.LookAtPrompt, first(a.Seen.BlockedSince, a.StateSince), newsPrompt
	case b.s.Run.Paused != nil && c.Look != contract.LookToCheck && c.Look != contract.LookCheckFailed:
		// "Paused" is two facts: the run is paused and the terminals show the agent at rest.
		c.Look, c.Since = contract.LookPaused, b.s.Run.Paused.At
		if status == contract.StatusWorking {
			c.Word = contract.WordFinishing
			if !a.Gated || !contract.Gates[a.Agent.Kind].Holds || a.Seen.StillFlagged {
				c.Word = contract.WordStillRunning
			}
		}
	}
	return st
}

// items sorts every question into what waits for the person, what Do not
// disturb holds back and the last answers, and applies the one rule for
// "needs you": something for the person holds that agent's work up.
func (b *builder) items(v *contract.View) {
	qs := make([]*contract.Question, 0, len(b.s.Questions))
	for _, q := range b.s.Questions {
		qs = append(qs, q)
	}
	slices.SortFunc(qs, func(x, y *contract.Question) int {
		return cmp.Or(x.CreatedAt.Compare(y.CreatedAt), cmp.Compare(len(x.ID), len(y.ID)), strings.Compare(x.ID, y.ID))
	})
	ui := b.in.UI
	quiet := ui.DND && (ui.DNDUntil.IsZero() || b.in.Now.Before(ui.DNDUntil))
	var rest, last []contract.Item
	for _, q := range qs {
		st := b.seats[q.Task]
		asking := q.Kind == contract.KindAsk && st != nil && st.live && st.try.ID == q.Attempt
		late := q.For == contract.ForOrchestrator && asking && b.in.Now.Sub(q.CreatedAt) >= askAfter
		switch {
		case q.State == contract.QuestionClosed:
		case q.State != contract.QuestionOpen:
			if q.For == contract.ForHuman {
				v.Answered = append(v.Answered, b.item(q, st, false, false))
			}
			if asking && q.State == contract.QuestionAnswered {
				st.card.News = "answered, not read yet: " + cli.Plain(q.Answer)
			}
		case q.For != contract.ForHuman && !late:
			if asking {
				st.ask, st.card.News = q, "asked the lead: "+news(cli.Plain(q.Text))
			}
		default:
			holds := asking || q.Holds && st != nil && (st.live || st.task.Status == contract.TaskReady)
			it := b.item(q, st, holds, late)
			switch {
			case st == nil:
			case holds && st.card.Look != contract.LookNeedsYou:
				c := st.card
				c.Look, c.Word, c.Since, c.Item, c.Flag = contract.LookNeedsYou, "", q.CreatedAt, q.ID, false
				c.News = news(cli.Plain(q.Text))
			case !holds && st.card.Item == "" && q.Cause == "":
				st.card.Flag, st.card.Item = true, q.ID
			}
			switch {
			case quiet && !q.Urgent && q.ShownAt.IsZero():
				v.Held = append(v.Held, it)
			case holds:
				v.Items = append(v.Items, it)
			case q.Cause == contract.CauseLeadUnread:
				last = append(last, it)
			default:
				rest = append(rest, it)
			}
		}
	}
	v.Items = append(append(v.Items, rest...), last...)
	slices.Reverse(v.Answered)
	v.Answered = v.Answered[:min(len(v.Answered), answeredKept)]
}

// item is one question as the person is shown it, with its ways to answer.
func (b *builder) item(q *contract.Question, st *seat, holds, late bool) contract.Item {
	it := contract.Item{
		ID: q.ID, Form: q.Form, Look: contract.LookNeedsYou, Task: q.Task, Since: q.CreatedAt,
		Text: cli.Plain(q.Text), News: news(cli.Plain(q.Text)), Holds: holds, Urgent: q.Urgent, Line: q.Form == contract.FormQuestion,
	}
	lead := q.Cause == contract.CauseLeadUnread || q.Cause == contract.CauseLeadSilent
	switch {
	case lead:
		it.Task, it.Name = contract.Lead, leadName
	case st != nil:
		if it.Name = st.card.Name; q.Cause == contract.CausePrompt {
			it.Look = contract.LookAtPrompt
		} else if !holds {
			it.Look = st.card.Look
		}
	}
	switch {
	case late:
		it.Source = sourceLate
	case q.Kind == contract.KindAsk:
		it.Source = sourceWorker
	case q.From == contract.FromTool:
		it.Source = "from the tool itself"
	case q.Form == contract.FormSignoff:
		it.Source = "sign-off asked by the lead agent"
	case q.Form == contract.FormTodo:
		it.Source = "something to do, from the lead agent"
	default:
		it.Source = "a question from the lead agent"
	}
	id, human := q.ID, b.in.Caller == contract.Human
	answer := func(text string) string { return "whaleshark answer " + id + " " + text + " --human" }
	if q.State != contract.QuestionOpen {
		it.Answer, it.By, it.Settles, it.Used = cli.Plain(q.Answer), q.AnsweredBy, q.SettlesAt, q.UsedAt
		if q.State == contract.QuestionAnswered {
			it.Buttons = []contract.Button{{Label: "Undo", Key: "u", Action: "undo"}}
			if human {
				it.Next = []string{"whaleshark answer " + id + " --undo --human"}
			}
		}
		return it
	}
	switch q.Form {
	case contract.FormChoice:
		options := q.Options
		if len(options) == 0 {
			options = []string{"yes", "no"}
		}
		for i, o := range options {
			o = cli.Plain(o)
			key := fmt.Sprint(i + 1)
			if len(options) <= 2 {
				key = "yn"[i : i+1]
			} else if i > 8 {
				key = ""
			}
			r, n := utf8.DecodeRuneInString(o)
			it.Buttons = append(it.Buttons, contract.Button{Label: string(unicode.ToUpper(r)) + o[n:], Key: key, Answer: o})
		}
		it.Buttons = append(it.Buttons, contract.Button{Label: "Other", Key: "o", Answer: contract.AnswerOther})
		it.Next = []string{answer(quote(it.Buttons[0].Answer))}
	case contract.FormSignoff:
		it.Buttons = []contract.Button{{Label: "Approve", Key: "y", Answer: contract.AnswerApprove}, {Label: "Send back", Key: "n", Answer: contract.AnswerSendBack}}
		it.Next = []string{answer(contract.AnswerApprove), answer(`"` + contract.AnswerSendBack + ` <why>"`)}
	case contract.FormTodo:
		it.Buttons = []contract.Button{{Label: "Done", Key: "y", Answer: contract.AnswerDone}, {Label: "Cannot", Key: "n", Answer: contract.AnswerCannot}}
		it.Next = []string{answer(contract.AnswerDone), answer(`"` + contract.AnswerCannot + ` <why>"`)}
		if lead || q.Cause == contract.CausePrompt && st != nil && st.card.Tab != "" {
			it.Buttons = append(it.Buttons, contract.Button{Label: "Go there", Action: "go"})
			it.Next = []string{"whaleshark jump " + quote(it.Task)}
		}
		if q.Cause == contract.CauseLeadSilent {
			it.Buttons = append(it.Buttons, contract.Button{Label: "Tell it to carry on", Action: "carry-on"})
		}
	default:
		it.Next = []string{answer(`"<your answer>"`)}
	}
	switch {
	case late:
		it.Buttons = append(it.Buttons, contract.Button{Label: "Ask the lead", Action: "ask-lead"})
		if b.in.Caller == contract.Orchestrator {
			it.Next = []string{"whaleshark answer " + id + ` "<answer>"`, "whaleshark escalate " + id}
		} else if !human {
			it.Next = []string{sourceLate}
		}
	case !human:
		it.Next = []string{forHuman}
	}
	return it
}

// clashes puts each announced finding about two tasks on both their cards.
// Of two that would conflict the first keeps its place.
func (b *builder) clashes() {
	for _, f := range b.s.Run.Findings {
		if len(f.Tasks) != 2 || len(f.Files) == 0 || f.Kind != "same" && f.Kind != "overlap" {
			continue
		}
		for i, id := range f.Tasks {
			st, other := b.seats[id], b.seats[f.Tasks[1-i]]
			if st != nil && other != nil && st.card.Clash == nil {
				st.card.Clash = &contract.Clash{Task: other.task.ID, Name: other.card.Name, File: cli.Plain(f.Files[0]), Conflicts: f.Kind == "overlap" && i == 1}
			}
		}
	}
}

// finish gives a card what depends on its final look: its colour, the pale
// bar, the lines its reader may run and the actions offered on it.
func (b *builder) finish(st *seat) {
	c, t, a := st.card, st.task, st.try
	c.Colour = contract.Looks[order[c.Look]].Colour
	if c.Word == contract.WordStillRunning {
		c.Colour = "blocked"
	}
	reported := c.Look == contract.LookToCheck || c.Look == contract.LookCheckFailed
	stale := time.Duration(b.in.Limits.Limits.StaleMinutes) * time.Minute
	old := stale > 0 && b.in.Now.Sub(c.Said) >= stale
	c.Pale = st.live && !c.Said.IsZero() && !reported && (c.Look == contract.LookIdle || old)

	paused := b.s.Run.Paused != nil
	if c.Tab != "" {
		c.Actions = append(c.Actions, "go")
	}
	switch {
	case st.live:
		if c.Look == contract.LookToCheck && c.Word == "" && t.Check == contract.CheckNone {
			c.Actions = append(c.Actions, "accept-by-hand")
		} else if c.Look == contract.LookToCheck && c.Word == "" {
			c.Actions = append(c.Actions, "accept")
		}
		if reported && c.Word == "" {
			c.Actions = append(c.Actions, "send-back")
		}
		if c.Actions = append(c.Actions, "stop"); !paused {
			c.Actions = append(c.Actions, "note")
		}
	case t.Status == contract.TaskReady && a != nil:
		c.Actions = append(c.Actions, "retry")
	case t.Status == contract.TaskReady:
		c.Actions = append(c.Actions, "start")
	}
	if !st.live && c.Tab != "" {
		c.Actions = append(c.Actions, "close")
	}

	// The lines of 8e, written for the lead agent. A person is shown the way
	// out of a problem only, with --human on what changes something; a tab no
	// run is bound to is shown what only reads.
	var lines []string
	id := t.ID
	switch c.Look {
	case contract.LookCheckFailed:
		lines = []string{"show " + id, "reject " + id + ` "<why>"`}
	case contract.LookToCheck:
		if lines = []string{"show " + id, "accept " + id}; c.Word != "" {
			lines = []string{"wait"}
		}
	case contract.LookBuilding, contract.LookPaused:
		if lines = []string{"wait"}; c.Pale && !paused {
			lines = []string{"show " + id + " --screen", "tell " + id + ` --now "report or ask"`, "stop " + id}
		}
	case contract.LookIdle:
		lines = []string{"show " + id + " --screen", "tell " + id + ` --now "report or ask"`, "stop " + id}
	case contract.LookAsking:
		if st.ask != nil {
			lines = []string{"answer " + st.ask.ID + ` "<answer>"`, "escalate " + st.ask.ID}
		}
	case contract.LookFailed:
		switch {
		case t.Status == contract.TaskFailed:
			lines = []string{"task edit " + id + " --brief <file>", "task reset " + id}
		case a != nil && a.State == contract.AttemptStartFailed && c.Tab != "":
			lines = []string{"close " + id, "start " + id + " --retry"}
		case t.Status == contract.TaskReady:
			lines = []string{"start " + id + " --retry"}
		}
	case contract.LookDone:
		if c.Tab != "" {
			lines = []string{"close " + id}
		}
	}
	human := b.in.Caller == contract.Human
	if human && c.Look != contract.LookCheckFailed && c.Look != contract.LookFailed {
		return
	}
	for _, line := range lines {
		reads := strings.HasPrefix(line, "show ")
		switch {
		case human && !reads:
			line += " --human"
		case b.in.Caller != contract.Orchestrator && !human && !reads:
			continue
		}
		c.Next = append(c.Next, "whaleshark "+line)
	}
}

// around fills in what stands round the cards: the lines on top, the strip
// and what the age line says about its sources.
func (b *builder) around(v *contract.View) {
	in, run := b.in, b.s.Run
	if run.Paused != nil {
		v.Strip.Paused = true
		line := "all work is paused"
		if n := v.Strip.StillRunning; n > 0 {
			line += fmt.Sprintf(" · %d still running", n)
		}
		v.Alerts = append(v.Alerts, line)
	}
	for _, it := range append(slices.Clone(v.Items), v.Held...) {
		switch b.s.Questions[it.ID].Cause {
		case contract.CauseLeadSilent:
			v.Alerts = append(v.Alerts, fmt.Sprintf("the lead agent is not listening · %s waiting", plural(len(b.s.Inbox.Events), "event", "events")))
		case contract.CauseTogether:
			v.Alerts = append(v.Alerts, "most workers stopped together; check your usage limit")
		}
	}
	if in.Terms == nil {
		v.Fresh.Notes = append(v.Fresh.Notes, "the engine is not running")
		if in.Now.Sub(run.Terms.SeenAt) >= time.Minute {
			v.Alerts = append(v.Alerts, "the engine is not running: `whaleshark open` starts it")
		}
	}
	// One note about the sweep, never two: that none has run yet says all
	// there is to say until one has.
	if !in.Swept.Ran {
		v.Fresh.Notes = append(v.Fresh.Notes, "no sweep yet")
	} else if !in.Watched {
		v.Fresh.Notes = append(v.Fresh.Notes, "nobody is sweeping")
	}
	v.Fresh.Notes = append(v.Fresh.Notes, in.Notes...)
	ui := in.UI
	if ui.DND && (ui.DNDUntil.IsZero() || in.Now.Before(ui.DNDUntil)) {
		v.Strip.DND, v.Strip.DNDUntil = true, ui.DNDUntil
	}
	v.Strip.Mute = ui.Mute
	if !ui.LastHere.IsZero() && in.Now.Sub(ui.LastHere) > contract.AwayAfter {
		v.Strip.Away = ui.LastHere
		v.Strip.DoneAsYou = asYou(b.s, b.s.Inbox.Events, ui.LastHere, in.Now)
	}
}

// unmet lists the tasks a task still waits for.
func (b *builder) unmet(t *contract.Task) []string {
	var out []string
	for _, id := range t.After {
		if d := b.s.Tasks[id]; d != nil && d.Status != contract.TaskDone {
			out = append(out, id)
		}
	}
	return out
}

// news is a question as the news of a row that begins with other words: its
// first letter lower-cased when the letter after it is lower-case too, so
// "API keys?" stays as it is.
func news(text string) string {
	r, n := utf8.DecodeRuneInString(text)
	if next, _ := utf8.DecodeRuneInString(text[n:]); unicode.IsUpper(r) && unicode.IsLower(next) {
		return string(unicode.ToLower(r)) + text[n:]
	}
	return text
}

// quote puts a value in quotes where a shell would split it.
func quote(s string) string {
	if strings.ContainsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' }) || s == "" {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(s) + `"`
	}
	return s
}

func first(times ...time.Time) time.Time {
	for _, t := range times {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprint(n, " ", many)
}
