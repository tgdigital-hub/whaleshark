package view

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/theme"
)

var widths = []int{100, 80, 60}

// looks is every way a screen is printed: with the marks or without, and
// plain, in the default colours, or for a terminal that wants no colour.
var looks = []struct {
	name   string
	plain  bool
	colour map[string]uint32
}{
	{"", false, nil}, {"-plain", true, nil}, {"-colour", false, theme.Schemes[0].Colours}, {"-plain-colour", true, theme.Schemes[0].Colours},
	{"-bold", false, map[string]uint32{}},
}

// later is the evening a few minutes on, as the person finds it coming back:
// away since 21:10, the email sender's failed check not looked at yet, and an
// answer that was given, taken back and given again.
func later(t *testing.T) *testkit.Fixture {
	t.Helper()
	f := evening(t)
	s := &f.State
	f.UI.LastHere = f.Now.Add(-4 * time.Minute)
	s.Tasks["T7"].SeenAt = f.Now.Add(-10 * time.Minute)
	typed, pane := &contract.Origin{Caller: contract.Human, Where: contract.WhereTyped}, &contract.Origin{Caller: contract.Human, Where: contract.WherePane}
	q := s.Questions["q10"]
	q.State, q.Answer, q.AnsweredAt, q.AnsweredBy = contract.QuestionAnswered, "orders, at the shop's own name", f.Now.Add(-time.Minute), pane
	q.Earlier = []contract.Undone{{Answer: "the old one", At: f.Now.Add(-2 * time.Minute), By: typed, UndoneAt: f.Now.Add(-90 * time.Second)}}
	s.Attempts["T7.1"].Mail = []contract.Mail{{Seq: 1, At: f.Now.Add(-30 * time.Minute), Text: "the bounce address is in the settings file"}}
	s.Inbox.Events = append(s.Inbox.Events,
		contract.Event{At: f.Now.Add(-2 * time.Minute), Kind: "check_failed", Task: "T7", Attempt: "T7.1", Text: "2 of 41 tests fail: bounce handling"},
		contract.Event{At: f.Now.Add(-time.Minute), Kind: "human", Text: "The person ran stop themselves.", Data: map[string]any{"what": "stop", "on": "T9", "where": contract.WherePhone}})
	return f
}

// plain takes a printer's own colour and bold out of a text.
func plain(s string) string { return switches.ReplaceAllString(s, "") }

// frame holds any screen to what all of them share: nothing wider than the
// terminal, no line that ends in spaces, the age on the last line, nothing
// but ASCII where the marks cannot be shown, and no colour that was not asked for.
func frame(t *testing.T, got string, width int, noMarks, colour bool) []string {
	t.Helper()
	if !colour && strings.Contains(got, "\x1b") {
		t.Errorf("colour where none was asked for:\n%q", got)
	}
	lines := strings.Split(strings.TrimRight(plain(got), "\n"), "\n")
	for _, l := range lines {
		if cells(l) > width || l != strings.TrimRight(l, " ") {
			t.Errorf("wider than %d columns, or ends in spaces: %q", width, l)
		}
		for _, r := range l {
			if noMarks && r > 127 && r != '·' {
				t.Fatalf("%q is printed where the marks cannot be shown: %q", r, l)
			}
		}
	}
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "live · checked ") {
		t.Errorf("the last line does not say how fresh the text is: %q", last)
	}
	return lines
}

// The plan, the messages and the team at every width, with and without the
// marks and the colours: the golden files, each held to the layout rules.
func TestScreens(t *testing.T) {
	f := later(t)
	in := whole(f.Input(contract.Human))
	v, s := Build(in), &f.State
	pl, list := plan(v, s), said(s, s.Inbox.Events, f.UI.LastHere)
	if pl.Loop || strings.Join(pl.Chain, " ") != "T2 T13 T14" || pl.Holding != "Nothing new can start until T1, T2, T3 and 9 more are done." {
		t.Errorf("the plan: loop %v, chain %v, %q", pl.Loop, pl.Chain, pl.Holding)
	}
	team := Build(f.Input(contract.Human))
	for _, width := range widths {
		for _, l := range looks {
			o := Options{Width: width, PlainMarks: l.plain, Colours: l.colour}
			name := fmt.Sprintf("-%d%s.txt", width, l.name)
			mark := func(look contract.Look) string { return (&printer{o: o}).mark(look) }

			var b bytes.Buffer
			planText(&b, v, pl, o)
			golden(t, "graph"+name, b.String())
			lines := frame(t, b.String(), width, l.plain, l.colour != nil)
			// Rule 1: what is for the person stands above the list. No task
			// stands above one it waits for, and each has its look's mark.
			row := map[string]int{}
			for i, line := range lines {
				for _, r := range pl.Rows {
					if strings.HasPrefix(line, " "+mark(r.Card.Look)+" "+r.Card.Task+" ") {
						row[r.Card.Task] = i
					}
				}
			}
			for _, r := range pl.Rows {
				at, ok := row[r.Card.Task]
				if !ok || !strings.Contains(lines[at], " "+r.Card.Name+" ") {
					t.Fatalf("%s: the plan has no row for %s", name, r.Card.Task)
				}
				for _, dep := range r.After {
					if row[dep] >= at {
						t.Errorf("%s: %s stands above %s, which it waits for", name, r.Card.Task, dep)
					}
				}
			}
			for _, it := range v.Items {
				if i := slices.IndexFunc(lines, func(line string) bool { return strings.HasPrefix(line, " "+mark(it.Look)+" "+it.ID+" ") }); i < 0 || i > row["P1"] {
					t.Errorf("%s: item %s is not above the plan", name, it.ID)
				}
			}

			b.Reset()
			saidText(&b, v, list, "today", o)
			golden(t, "log"+name, b.String())
			lines = frame(t, b.String(), width, l.plain, l.colour != nil)
			// The one screen in time order: each row starts with its time.
			last, rows := "", 0
			for _, line := range lines[2:] {
				if clock := strings.TrimSpace(line); len(line) > 6 && line[0] == ' ' && line[1] != ' ' {
					if rows++; clock[:5] < last {
						t.Errorf("%s: %q comes after %s", name, line, last)
					}
					last = clock[:5]
				}
			}
			if rows != len(list) {
				t.Errorf("%s: %d rows for %d messages", name, rows, len(list))
			}

			if l.colour != nil {
				got := text(team, o)
				golden(t, "status-human"+name, got)
				layout(t, team, plain(got), width, l.plain)
			}
		}
	}
}

// What is new, what is dim, who did it and what was taken back are the
// model's; so is a name in bold until the person has looked.
func TestSaidAndUnseen(t *testing.T) {
	f := later(t)
	s := &f.State
	list := said(s, s.Inbox.Events, f.UI.LastHere)
	find := func(kind, text string) contract.Message {
		t.Helper()
		i := slices.IndexFunc(list, func(m contract.Message) bool { return m.Kind == kind && strings.Contains(m.Text, text) })
		if i < 0 {
			t.Fatalf("no %s message %q", kind, text)
		}
		return list[i]
	}
	if m := find("80%", "form and checks"); !m.Dim || m.New || m.Dir != "<" {
		t.Errorf("a progress note: %+v", m)
	}
	if m := find("answer", "the old one"); !m.Struck || by(m.By) != "by you, typed" || m.Dir != ">" {
		t.Errorf("an answer taken back: %+v", m)
	}
	if m := find("answer", "orders, at"); m.Struck || !m.New || by(m.By) != "by you, from the pane" {
		t.Errorf("the answer that replaced it: %+v", m)
	}
	if m := find("stop", "themselves"); by(m.By) != "by you, from your phone" || m.Task != "T9" {
		t.Errorf("what was done as the person: %+v", m)
	}
	if m := find("brief", "email"); m.Dir != ">" || m.New || m.Task != "T7" {
		t.Errorf("a brief: %+v", m)
	}
	if got := by(&contract.Origin{Caller: contract.Orchestrator, Where: contract.WhereRelayed}); got != "relayed by the lead agent" || by(&contract.Origin{Caller: contract.Orchestrator}) != "" {
		t.Errorf("relayed: %q", got)
	}
	for i := 1; i < len(list); i++ {
		if list[i].At.Before(list[i-1].At) {
			t.Fatalf("%+v comes after %+v", list[i], list[i-1])
		}
	}

	v := Build(f.Input(contract.Human))
	if c, _ := card(t, v, "T7"); !c.Unseen {
		t.Error("a check that failed after the person last looked is not unseen")
	}
	if c, _ := card(t, v, "T1"); c.Unseen {
		t.Error("a card nothing happened to is unseen")
	}
	got := text(v, Options{Width: 100, Colours: map[string]uint32{}})
	if !strings.Contains(got, "\x1b[1memail sender\x1b[22m") || strings.Count(got, "\x1b[1m") != 1 {
		t.Errorf("bold until seen:\n%q", got)
	}
}

// Tasks that wait for each other in a circle are listed flat, in the order
// they were added, and the screen says so.
func TestPlanLoop(t *testing.T) {
	f := evening(t)
	s := &f.State
	s.Tasks["T13"].After = append(s.Tasks["T13"].After, "T14")
	s.Tasks["T14"].CreatedAt = s.Tasks["T14"].CreatedAt.Add(time.Minute)
	v := Build(whole(f.Input(contract.Orchestrator)))
	pl := plan(v, s)
	if !pl.Loop || len(pl.Rows) != len(s.Tasks) || pl.Chain != nil || pl.Rows[len(pl.Rows)-1].Card.Task != "T14" || pl.Rows[0].Card.Task != "P1" {
		t.Fatalf("a circle: loop %v, %d rows, chain %v", pl.Loop, len(pl.Rows), pl.Chain)
	}
	var b bytes.Buffer
	planText(&b, v, pl, Options{Width: 80})
	if got := b.String(); !strings.Contains(got, "\n"+loopLine+"\n") || strings.Contains(got, "Longest chain") {
		t.Errorf("a circle prints\n%s", got)
	}
	// One ready task is what can start; one held by an item says by which.
	f = evening(t)
	s = &f.State
	s.Tasks["T14"].After, s.Tasks["T14"].Status = nil, contract.TaskReady
	if pl := plan(Build(whole(f.Input(contract.Human))), s); pl.Holding != "Can start now: T14." {
		t.Errorf("a ready task: %q", pl.Holding)
	}
	s.Questions["n9"] = &contract.Question{ID: "n9", Kind: contract.KindNeed, Form: contract.FormTodo, For: contract.ForHuman, From: contract.FromLead,
		Task: "T14", Holds: true, Text: "Buy the domain", State: contract.QuestionOpen, CreatedAt: f.Now}
	v = Build(whole(f.Input(contract.Human)))
	pl = plan(v, s)
	b.Reset()
	planText(&b, v, pl, Options{Width: 100})
	if row := pl.Rows[len(pl.Rows)-1]; row.HeldBy != "n9" || !strings.Contains(b.String(), "held by n9") || strings.HasPrefix(pl.Holding, "Can start") {
		t.Errorf("a held task: %+v, %q\n%s", row, pl.Holding, b.String())
	}
}

// The lines under the team and on top of it: most workers stopped together,
// and a project whose approved agent line switches the prompts off.
func TestLinesAndAlerts(t *testing.T) {
	f := evening(t)
	s := &f.State
	s.Questions["n8"] = &contract.Question{ID: "n8", Kind: contract.KindNeed, Form: contract.FormTodo, For: contract.ForHuman, From: contract.FromTool,
		Cause: contract.CauseTogether, Urgent: true, Text: "Most workers stopped together; check your usage limit.", State: contract.QuestionOpen, CreatedAt: f.Now}
	in := f.Input(contract.Human)
	in.Limits.Agent.Args = []string{"--model", "small", "--dangerously-" + "skip-permissions"}
	s.Run.Paused = &contract.Pause{At: f.Now}
	v := Build(in)
	if len(v.Lines) != 1 || !strings.HasPrefix(v.Lines[0], "most workers stopped together") || len(v.Alerts) != 2 || v.Alerts[0] != "workers in this project run without permission prompts" {
		t.Fatalf("lines %q, alerts %q", v.Lines, v.Alerts)
	}
	got := text(v, Options{Width: 100})
	if !strings.HasPrefix(got, v.Alerts[0]+"\n") || !strings.Contains(got, "\n\n"+v.Lines[0]+"\n12 agents") {
		t.Errorf("the text is\n%s", got)
	}
	in.Limits.Agent.Args = []string{"--permission-mode", "acceptEdits"}
	if v := Build(in); len(v.Alerts) != 1 {
		t.Errorf("an agent line that keeps the prompts: %q", v.Alerts)
	}
}

// How long the waiting results take is said only from a check that was timed.
func TestTheQueueLine(t *testing.T) {
	f := evening(t)
	s, waiting := &f.State, 0
	for _, task := range s.Tasks {
		if task.Status == contract.TaskReview {
			waiting++
		}
	}
	for id := range s.Tasks {
		if waiting >= 3 {
			break
		}
		if s.Tasks[id].Status == contract.TaskPending {
			s.Tasks[id].Status = contract.TaskReview
			waiting++
		}
	}
	line := func() string {
		for _, l := range Build(f.Input(contract.Human)).Lines {
			if strings.HasPrefix(l, "to check") {
				return l
			}
		}
		return ""
	}
	if got := line(); got != "" {
		t.Fatalf("no check was timed, yet: %q", got)
	}
	for _, a := range s.Attempts {
		a.Check = &contract.CheckResult{OK: true, At: f.Now, Took: 100 * time.Second}
		break
	}
	want := fmt.Sprintf("to check: %d, about %d min one by one, about 2 min together", waiting, (waiting*100+59)/60)
	if got := line(); got != want {
		t.Fatalf("the line is %q, want %q", got, want)
	}
	if got := changed(" a.txt | 3 ++-\n b.txt | 9 ---------\n 2 files changed, 2 insertions(+), 10 deletions(-)\n"); got != "2 files, +2 -10" {
		t.Errorf("git's count reads %q", got)
	}
	if got := changed(" 1 file changed, 4 insertions(+)\n") + changed("nothing"); got != "1 file, +4 -0" {
		t.Errorf("git's count of one file reads %q", got)
	}
}

// several is a store with more than one project in it.
type several struct {
	ledger
	runs map[string]*contract.State // by project folder
}

func (s several) Runs(root string) ([]string, error) {
	if r := s.runs[root]; r != nil {
		return []string{r.Run.ID}, nil
	}
	return nil, nil
}

func (s several) Current(root string) (string, error) {
	if root == "/work/old" {
		return "", nil
	}
	runs, err := s.Runs(root)
	return strings.Join(runs, ""), err
}

func (s several) Read(root, run string) (*contract.State, error) {
	if r := s.runs[root]; r != nil && r.Run.ID == run {
		return r, nil
	}
	return s.ledger.Read(root, run)
}

// status counts the person's other open runs that need them, and
// --everywhere lists every open run, those first.
func TestEverywhere(t *testing.T) {
	f := evening(t)
	jobs := func(flags ...string) (*contract.Call, *bytes.Buffer) {
		c, out, _, _ := view(t, f, "status", contract.Human)
		for _, flag := range flags {
			c.Flags[flag] = []string{""}
		}
		quiet, needs, closed := evening(t).State, evening(t).State, evening(t).State
		quiet.Run.ID, quiet.Run.Objective, quiet.Questions = "r1", "tidy the docs", nil
		needs.Run.ID, needs.Run.Objective = "r8", "api clean-up"
		closed.Run.ID, closed.Run.ClosedAt = "r2", f.Now
		c.Kit.Store = several{c.Kit.Store.(ledger), map[string]*contract.State{"/work/docs": &quiet, "/work/api": &needs, "/work/old": &closed, c.Root: &f.State}}
		dirs, _ := c.Kit.Platform.Dirs()
		if err := contract.WriteProjects(login{}, dirs.State, []string{"/work/docs", c.Root, "/work/api", "/work/old", "/work/gone"}); err != nil {
			t.Fatal(err)
		}
		return c, out
	}
	c, out := jobs()
	if v := run(t, c).(*contract.View); v.Counts.Elsewhere != 1 || !strings.Contains(out.String(), "1 needs you in another job (--everywhere)") {
		t.Errorf("%d other jobs need the person\n%s", v.Counts.Elsewhere, out)
	}
	c, out = jobs("everywhere")
	all := run(t, c).([]job)
	golden(t, "status-everywhere.txt", out.String())
	frame(t, out.String(), 100, false, false)
	var got []string
	for _, j := range all {
		got = append(got, j.View.Run)
	}
	if strings.Join(got, " ") != "r3 r8 r1" || all[1].Next != "whaleshark status --root /work/api --run r8" || all[1].View.Counts.Elsewhere != 0 {
		t.Errorf("the jobs are %v, the second shown by %q", got, all[1].Next)
	}
	// Without a run here the others are still listed.
	c, out = jobs("everywhere")
	c.Root, c.Run = "/work/nowhere", ""
	if all := run(t, c).([]job); len(all) != 3 || !strings.HasPrefix(out.String(), "JOBS   3 open runs · 2 need you") {
		t.Errorf("without a run: %d jobs\n%s", len(all), out)
	}
}

// changes is an integrator that says what a task changed.
type changes struct {
	contract.NoIntegrator
	text string
	err  error
}

func (c changes) Diff(_ string, _ *contract.State, task string, stat bool) (string, error) {
	return fmt.Sprint(task, " stat=", stat, "\n", c.text), c.err
}

// show --diff prints what the integrator says a task changed, with nothing
// in it that could rewrite the terminal, and counts as a look.
func TestShowDiff(t *testing.T) {
	f := evening(t)
	c, out, _, n := view(t, f, "show", contract.Human, "email sender")
	c.Kit.Integrator = changes{text: "+\x1b[2Jred\x1b]0;title\a\n-old\tline\n\n"}
	c.Flags["diff"], c.Flags["stat"] = []string{""}, []string{""}
	result := run(t, c).(map[string]string)
	if want := "T7 stat=true\n+[2Jred]0;title\n-old\tline\n"; out.String() != want || result["diff"] != want || *n != 1 {
		t.Errorf("--diff --stat prints %q after %d changes", out, *n)
	}
	c, _, _, _ = view(t, f, "show", contract.Human, "T7")
	c.Flags["stat"] = []string{""}
	refusal(t, c, contract.ExitUsage, "usage")
	c, _, _, _ = view(t, f, "show", contract.Human, "T7")
	c.Flags["diff"] = []string{""}
	refusal(t, c, contract.ExitMissing, "no_diff")
	c.Kit.Integrator = changes{err: &contract.Refusal{Exit: contract.ExitMissing, Code: "no_worktree"}}
	refusal(t, c, contract.ExitMissing, "no_worktree")
	c.Kit.Integrator = changes{err: errors.New("git said no")}
	refusal(t, c, contract.ExitMissing, "no_diff")
}

// graph and log as commands: the plan and the messages of the run, --ready
// and --task, and how to start a run where there is none.
func TestGraphAndLog(t *testing.T) {
	f := later(t)
	f.State.Tasks["T14"].After, f.State.Tasks["T14"].Status = nil, contract.TaskReady
	c, out, _, n := view(t, f, "graph", contract.Orchestrator)
	if pl := run(t, c).(*contract.Plan); len(pl.Rows) != len(f.State.Tasks) || !strings.HasPrefix(out.String(), "PLAN   run r3 · 19 tasks · 5 done · 12 open · 2 queued") {
		t.Errorf("graph: %d rows\n%s", len(pl.Rows), out)
	}
	c, out, _, _ = view(t, f, "graph", contract.Unbound)
	c.Flags["ready"] = []string{""}
	if pl := run(t, c).(*contract.Plan); len(pl.Rows) != 1 || pl.Rows[0].Card.Task != "T14" || !strings.Contains(out.String(), "Can start now: T14.") {
		t.Errorf("graph --ready: %+v\n%s", pl.Rows, out)
	}

	c, out, _, _ = view(t, f, "log", contract.Human)
	c.Kit.Store = ledger{record: record{s: &f.State}, changes: n, past: []contract.Event{
		{At: f.Now.AddDate(0, 0, -1), Kind: "paused"}, {At: f.Now.Add(-40 * time.Minute), Kind: "quiet", Task: "T7"}}}
	today := run(t, c).([]contract.Message)
	if strings.Contains(out.String(), "paused") || !strings.Contains(out.String(), " quiet\n") || !strings.HasPrefix(out.String(), "MESSAGES  7 new · today") {
		t.Errorf("log prints\n%s", out)
	}
	c.Flags["all"], c.Flags["task"] = []string{""}, []string{"email sender"}
	out.Reset()
	one := run(t, c).([]contract.Message)
	if len(one) >= len(today) || slices.ContainsFunc(one, func(m contract.Message) bool { return m.Task != "T7" }) || !strings.Contains(out.String(), "new · all · T7") || !strings.Contains(out.String(), " 08 Oct 20:") {
		t.Errorf("log --all --task prints %d of %d\n%s", len(one), len(today), out)
	}
	c.Flags["task"] = []string{"T99"}
	refusal(t, c, contract.ExitMissing, "no_such_task")
	if *n != 0 {
		t.Errorf("graph and log changed the record %d times", *n)
	}

	for _, command := range []string{"graph", "log"} {
		c, out, _, _ := view(t, f, command, contract.Human)
		c.Run = ""
		if result := run(t, c); result != nil || !strings.Contains(out.String(), "> whaleshark run new") {
			t.Errorf("%s without a run: %v\n%s", command, result, out)
		}
		c, _, _, _ = view(t, f, command, contract.Human, "T1")
		refusal(t, c, contract.ExitUsage, "usage")
	}
}
