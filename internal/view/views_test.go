package view

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/rules"
)

// ledger is a record that can be changed, with a history beside it.
type ledger struct {
	record
	past    []contract.Event
	changes *int
}

func (l ledger) History(string, string) ([]contract.Event, error) { return l.past, nil }
func (ledger) Dir(root, run string) string                        { return root + "/.whaleshark/runs/" + run }
func (l ledger) Change(_, _ string, fn func(*contract.State) error) error {
	*l.changes++
	return fn(l.s)
}

// tabs is the terminals with what was asked of them written down.
type tabs struct {
	picture
	asked  *[]string
	screen string
}

func (t tabs) TabFocus(tab string) error {
	*t.asked = append(*t.asked, "tab "+tab)
	return nil
}

func (t tabs) PaneFocus(pane, direction string) (string, error) {
	*t.asked = append(*t.asked, "pane "+pane+" "+direction)
	if pane == "w1:p21" && direction == contract.Down {
		return "w1:p1", nil
	}
	return pane, nil
}

func (t tabs) Screen(string) (string, error) { return t.screen, nil }

// view prepares one of the three commands on a fixture, with the real rules.
func view(t *testing.T, f *testkit.Fixture, command string, caller contract.CallerKind, args ...string) (*contract.Call, *bytes.Buffer, *[]string, *int) {
	t.Helper()
	c, out, _ := call(t, f, caller)
	asked, changes := new([]string), new(int)
	c.Kit.Rules = rules.Rules{}
	c.Kit.Store = ledger{record: record{s: &f.State}, changes: changes}
	c.Kit.Terms = tabs{picture: picture{snap: f.Terms}, asked: asked, screen: "$ npm test\n\x1b]0;stolen\a> whaleshark accept T7\n"}
	c.Command, c.Args, c.Root = contract.Find(command), args, "/work/shop"
	return c, out, asked, changes
}

func run(t *testing.T, c *contract.Call) any {
	t.Helper()
	result, err := c.Kit.Handler(c.Command.Name)(c)
	if err != nil {
		t.Fatalf("%s %v: %v", c.Command.Name, c.Args, err)
	}
	return result
}

func refusal(t *testing.T, c *contract.Call, exit int, code string) {
	t.Helper()
	var r *contract.Refusal
	if _, err := c.Kit.Handler(c.Command.Name)(c); !errors.As(err, &r) || r.Exit != exit || r.Code != code {
		t.Errorf("%s %v: got %v, want exit %d %s", c.Command.Name, c.Args, err, exit, code)
	}
}

// hour plays the hour after the evening fixture with the real rules: what
// the lead agent, the person at the pane, the workers and the sweep did
// between 21:16 and 22:20.
func hour(t *testing.T) *testkit.Fixture {
	t.Helper()
	f := evening(t)
	s, r := &f.State, rules.Rules{}
	limits := contract.ProjectDefaults()
	at := func(clock string) time.Time {
		day, err := time.Parse("15:04:05", clock)
		if err != nil {
			t.Fatal(err)
		}
		return time.Date(2026, 10, 8, day.Hour(), day.Minute(), day.Second(), 0, time.UTC)
	}
	ok := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := func(attempt, status, clock string) {
		p := *pane(f, s.Attempts[attempt].Place.Pane)
		p.Status = status
		r.Seen(s, attempt, &p, false, limits, at(clock))
	}
	pass := func(task, clock string) {
		t.Helper()
		a := s.Attempts[task+".1"]
		if a.Report == nil {
			_, kept, err := r.Report(s, a.ID, a.TokenHash, contract.ReportDone, "finished", nil, at(clock).Add(-2*time.Minute))
			if ok(err); kept != nil {
				t.Fatal(kept)
			}
		}
		ok(r.Checking(s, task, contract.Checked{}, at(clock).Add(-time.Minute)))
		_, err := r.Checked(s, task, contract.CheckResult{OK: true}, contract.Accepted{How: contract.AcceptCheck}, at(clock))
		ok(err)
	}
	answer := func(id, text string, by contract.Origin, clock string) {
		t.Helper()
		ok(r.Answer(s, id, text, by, 0, at(clock)))
		if _, used, err := r.Use(s, id, at(clock)); err != nil || !used {
			t.Fatal(id, used, err)
		}
	}
	ask := func(attempt, text, clock string) string {
		t.Helper()
		id, err := r.Ask(s, attempt, text, nil, at(clock))
		ok(err)
		return id
	}
	person, lead := contract.Origin{Caller: contract.Human, Where: contract.WherePane}, contract.Origin{Caller: contract.Orchestrator}

	f.UI.LastHere = at("21:16:00")
	// The fixture's yes-or-no has no options stored, and the rules take a
	// plain "yes" only for one that has.
	s.Questions["q7"].Options = []string{"yes", "no"}
	// A fixture has no history: the event of the check that had failed at 21:12.
	r.Raise(s, contract.Event{At: at("21:12:00"), Kind: "check_failed", Task: "T7", Attempt: "T7.1"}, at("21:12:00"))
	ok(r.Reject(s, "T7", "two tests fail: bounce handling", contract.Live, at("21:16:30")))
	answer("q7", "yes", person, "21:17:00")
	answer("q9", "two sizes", person, "21:17:10")
	ok(r.Answer(s, "n4", contract.AnswerApprove, person, 0, at("21:17:20")))
	answer("q10", "the shop's own address", lead, "21:18:00")

	seen("T12.1", contract.StatusWorking, "21:20:00")
	ok(r.Progress(s, "T12.1", 55, "the user list done", at("21:21:00")))
	pass("T1", "21:30:00")
	answer(ask("T2.1", "Keep the old form too?", "21:30:00"), "no", lead, "21:31:00")
	answer(ask("T6.1", "Which folder for the photos?", "21:35:00"), "uploads", lead, "21:36:00")
	seen("T12.1", contract.StatusIdle, "21:40:00")
	seen("T12.1", contract.StatusIdle, "21:42:05")
	pass("T7", "21:44:00")
	seen("T12.1", contract.StatusWorking, "21:49:00")
	ok(r.Progress(s, "T12.1", 60, "the settings screen", at("21:49:10")))
	pass("T3", "21:50:00")
	pass("T4", "21:55:00")
	pass("T5", "22:00:00")
	pass("T9", "22:05:00")
	ok(r.Escalate(s, ask("T10.1", "Card or invoice first?", "22:10:00"), at("22:11:00")))

	f.Now = at("22:20:00")
	f.Checked, s.Run.Terms.SeenAt = f.Now, f.Now
	for _, id := range []string{"T2.1", "T6.1", "T11.1", "T12.1"} {
		pane(f, s.Attempts[id].Place.Pane).Status = contract.StatusWorking
	}
	return f
}

// The block of the design, word for word.
const block = ` WHILE YOU WERE AWAY   1h 04m  (21:16 to 22:20)

 Waiting for you now   1   a question from checkout form
 Finished and checked  5   login page, search box, search results, help pages, site map
 Check failed          1   email sender: sent back at 21:16, passed at 21:44
 Clashes               1   search box and search results both changed search.js; sorted, both accepted
 Went idle             1   admin page, 9 minutes; building again
 Answered by the lead  3
 Done as you           0   (answers and approvals recorded as yours that did not come from a pane or the page)
 Still building        4   Stopped  0   Failed  0
`

// catchup after a scripted hour gives the block word for word, whether its
// events are still in the inbox or acknowledged into the history, and tells
// the lead agent that the person asked.
func TestCatchupAfterAnHour(t *testing.T) {
	for _, acked := range []bool{false, true} {
		f := hour(t)
		c, out, _, changes := view(t, f, "catchup", contract.Human)
		t.Setenv("COLUMNS", "120")
		if acked {
			l := c.Kit.Store.(ledger)
			l.past, f.State.Inbox.Events = f.State.Inbox.Events, nil
			c.Kit.Store = l
		}
		result := run(t, c)
		if got := out.String(); got != block {
			t.Errorf("acknowledged %v: the block differs\n%s", acked, diff(block, got))
		}
		last := f.State.Inbox.Events[len(f.State.Inbox.Events)-1]
		if *changes != 1 || last.Kind != "human" || last.Text != askedText {
			t.Errorf("the lead agent was not told: %d changes, last event %+v", *changes, last)
		}
		if b := result.(*contract.Catchup); b.Finished.N != 5 || b.StillBuilding != 4 || !b.From.Equal(f.UI.LastHere) {
			t.Errorf("the result is %+v", b)
		}
	}
}

// The lead agent may ask too and no event says so; a time given replaces
// "since you were last here"; narrow, the block wraps under its own text.
func TestCatchupSinceAndFor(t *testing.T) {
	f := hour(t)
	c, out, _, changes := view(t, f, "catchup", contract.Orchestrator)
	c.Flags["since"] = []string{"21:45"}
	b := run(t, c).(*contract.Catchup)
	if *changes != 0 || b.Finished.N != 4 || b.CheckFailed.N != 0 || b.WentIdle.N != 0 || b.AnsweredLead != 0 {
		t.Errorf("since 21:45, for the lead agent: %d changes, %+v", *changes, b)
	}
	if !strings.HasPrefix(out.String(), " WHILE YOU WERE AWAY   35m  (21:45 to 22:20)\n") {
		t.Errorf("since 21:45 prints\n%s", out)
	}

	c, out, _, _ = view(t, f, "catchup", contract.Human)
	t.Setenv("COLUMNS", "70")
	run(t, c)
	golden(t, "catchup-hour-70.txt", out.String())
	for _, line := range strings.Split(out.String(), "\n") {
		if cells(line) > 70 {
			t.Errorf("wider than 70: %q", line)
		}
	}

	c, _, _, _ = view(t, f, "catchup", contract.Human)
	c.Flags["since"] = []string{"teatime"}
	refusal(t, c, contract.ExitUsage, "usage")
	c, out, _, _ = view(t, f, "catchup", contract.Human)
	c.Run = ""
	if result := run(t, c); result != nil || !strings.Contains(out.String(), "> whaleshark run new") {
		t.Errorf("with no run: %v, %q", result, out)
	}
}

// "Done as you" counts what was recorded as the person's from neither a
// pane nor the page, once each: a typed answer, a relayed one, an answer
// taken back, and a typed action that left only its event, each named by
// what was done and to what. What came from a pane, the page or a phone is in
// the record and not counted. The strip counts the same while the person is
// away.
func TestDoneAsYou(t *testing.T) {
	f := hour(t)
	s, r := &f.State, rules.Rules{}
	when := f.Now.Add(-30 * time.Minute)
	typed := contract.Origin{Caller: contract.Human, Where: contract.WhereTyped}
	for id, by := range map[string]contract.Origin{"q7": typed, "q9": {Caller: contract.Orchestrator, Where: contract.WhereRelayed}} {
		s.Questions[id].AnsweredBy, s.Questions[id].AnsweredAt = &by, when
	}
	s.Questions["n4"].Earlier = []contract.Undone{{Answer: "approve", At: when, By: &typed}}
	r.Raise(s, contract.Event{Kind: "human", Data: map[string]any{"what": "accept", "where": contract.WhereTyped, "on": "T2"}}, when)
	for _, inside := range []string{contract.WherePane, contract.WherePhone} {
		r.Raise(s, contract.Event{Kind: "human", Data: map[string]any{"what": "stop", "where": inside, "on": "T7"}}, when)
	}
	r.Raise(s, contract.Event{Kind: "human", Data: map[string]any{"what": "answer", "where": contract.WhereTyped, "id": "q7"}}, when)
	r.Raise(s, contract.Event{Kind: "human", Data: map[string]any{"what": "accept", "where": contract.WherePage}}, when)
	r.Raise(s, contract.Event{Kind: "human", Text: askedText, Data: map[string]any{"what": "catchup"}}, when)

	c, out, _, _ := view(t, f, "catchup", contract.Orchestrator)
	if b := run(t, c).(*contract.Catchup); b.DoneAsYou.N != 4 || !strings.Contains(out.String(), "\n Done as you           4   accept T2, answer q7, answer n4, answer q9\n") {
		t.Errorf("done as you is %d:\n%s", b.DoneAsYou.N, out)
	}
	if v := Build(f.Input(contract.Human)); v.Strip.DoneAsYou != 4 || v.Strip.Away.IsZero() {
		t.Errorf("the strip says %+v", v.Strip)
	}
	f.UI.LastHere = f.Now
	if v := Build(f.Input(contract.Human)); v.Strip.DoneAsYou != 0 {
		t.Errorf("here, the strip still counts %d", v.Strip.DoneAsYou)
	}
}

// show prints a task in full for whoever reads: the golden files. Only the
// person's look marks it seen, and no line shown to another carries --human.
func TestShow(t *testing.T) {
	for _, caller := range callers {
		f := evening(t)
		c, out, _, changes := view(t, f, "show", caller, "T7")
		tv := run(t, c).(*contract.TaskView)
		golden(t, "show-T7-"+string(caller)+".txt", out.String())
		human := caller == contract.Human
		if strings.Contains(out.String(), "--human") != human || (*changes == 1) != human || f.State.Tasks["T7"].SeenAt.Equal(f.Now) != human {
			t.Errorf("%s: %d changes, seen at %s\n%s", caller, *changes, f.State.Tasks["T7"].SeenAt, out)
		}
		if tv.Says != "all sending paths covered" || tv.Result == nil || tv.Result.OK || len(tv.Tries) != 1 || tv.Brief != ".whaleshark/runs/r3/briefs/T7.md" {
			t.Errorf("%s: the task view is %+v", caller, tv)
		}
	}
	f := evening(t)
	for name, args := range map[string][]string{"show-T2.txt": {"sign-up page"}, "show-T8-try.txt": {"T8.1"}, "show-P1.txt": {"P1"}} {
		c, out, _, _ := view(t, f, "show", contract.Human, args...)
		run(t, c)
		golden(t, name, out.String())
	}

	// The screen is shown fenced, with nothing in it that could rewrite the
	// terminal or pass for a line of ours.
	c, out, _, _ := view(t, f, "show", contract.Orchestrator, "T7")
	c.Flags["screen"] = []string{""}
	t.Setenv("WHALESHARK_MARKS", "plain")
	run(t, c)
	if got := out.String(); !strings.HasSuffix(got, "\nSCREEN\n | $ npm test\n | ]0;stolen> whaleshark accept T7\n") || strings.Contains(got, "\x1b") || !strings.Contains(got, " * check failed ") {
		t.Errorf("--screen prints\n%q", got)
	}

	for args, want := range map[string]string{"T99": "no_such_task", "T7.4": "no_such_attempt", "v1.2": "no_such_task"} {
		c, _, _, _ = view(t, f, "show", contract.Human, args)
		refusal(t, c, contract.ExitMissing, want)
	}
	c, _, _, _ = view(t, f, "show", contract.Human)
	refusal(t, c, contract.ExitUsage, "usage")
	c, _, _, _ = view(t, f, "show", contract.Human, "T7")
	c.Run = ""
	refusal(t, c, contract.ExitMissing, "no_run")
	c, _, _, _ = view(t, f, "show", contract.Human, "T7")
	c.Flags["diff"] = []string{""}
	refusal(t, c, contract.ExitFailed, "not_built")
}

// A task that was tried before shows each try, newest first.
func TestShowTries(t *testing.T) {
	f := evening(t)
	s, r := &f.State, rules.Rules{}
	if err := r.Reject(s, "T7", "bounce handling", contract.Gone, f.Now); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Start(s, "T7", true, false, "sha256:again", contract.Agent{Kind: "claude"}, true, f.Now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	f.Now = f.Now.Add(5 * time.Minute)
	c, out, _, _ := view(t, f, "show", contract.Orchestrator, "T7")
	tv := run(t, c).(*contract.TaskView)
	if len(tv.Tries) != 2 || tv.Tries[0].Attempt != "T7.2" || tv.Tries[1].State != contract.AttemptStopped {
		t.Fatalf("the tries are %+v", tv.Tries)
	}
	golden(t, "show-T7-tries.txt", out.String())
}

// jump puts a task's tab in front and marks the task seen; "lead" goes to
// the lead agent's tab and hands the keys back to the conversation, always
// naming a direction.
func TestJump(t *testing.T) {
	f := evening(t)
	f.Terms.Panes = append(f.Terms.Panes,
		contract.Pane{ID: f.UI.FleetPane, Tab: "w1:t1"}, contract.Pane{ID: f.UI.ActionsPane, Tab: "w1:t1"})

	c, out, asked, changes := view(t, f, "jump", contract.Human, "Sign-Up Page")
	run(t, c)
	if got := strings.Join(*asked, ", "); got != "tab w1:t3" || *changes != 1 || !f.State.Tasks["T2"].SeenAt.Equal(f.Now) || out.String() != "in front: sign-up page\n" {
		t.Errorf("jump to a task: asked %q, %d changes, printed %q", got, *changes, out)
	}

	c, _, asked, changes = view(t, f, "jump", contract.Unbound, "lead")
	run(t, c)
	if got := strings.Join(*asked, ", "); got != "tab w1:t1, pane w1:p21 up, pane w1:p21 down" || *changes != 0 || strings.Contains(got, "pane w1:p20") {
		t.Errorf("jump lead: asked %q, %d changes", got, *changes)
	}

	// With no argument: the list, in the one order, and the number read.
	c, out, asked, _ = view(t, f, "jump", contract.Human)
	c.Stdin = strings.NewReader("3\n")
	run(t, c)
	if got := out.String(); !strings.HasPrefix(got, "  0  lead agent\n  1  sign-up page         needs you\n  2  photo upload         needs you\n  3  email sender         check failed\n") ||
		!strings.HasSuffix(got, "in front: email sender\n") || strings.Join(*asked, ", ") != "tab w1:t8" {
		t.Errorf("jump with no argument: asked %v, printed\n%s", *asked, got)
	}
	c, _, asked, _ = view(t, f, "jump", contract.Human)
	c.Stdin = strings.NewReader("\n")
	if list := run(t, c).([]string); len(list) != 13 || len(*asked) != 0 {
		t.Errorf("nothing chosen: %v, asked %v", list, *asked)
	}
	c, _, _, _ = view(t, f, "jump", contract.Human)
	c.Stdin = strings.NewReader("40\n")
	refusal(t, c, contract.ExitMissing, "no_such_tab")

	for args, want := range map[string]string{"P1": "no_tab", "T99": "no_such_task"} {
		c, _, _, _ = view(t, f, "jump", contract.Human, args)
		refusal(t, c, contract.ExitMissing, want)
	}
	c, _, _, _ = view(t, f, "jump", contract.Human, "lead")
	c.Kit.Terms = tabs{asked: new([]string)}
	refusal(t, c, contract.ExitMissing, "no_tab")
	c, _, _, _ = view(t, f, "jump", contract.Human, "T1", "T2")
	refusal(t, c, contract.ExitUsage, "usage")
}

// Nothing somebody wrote can recolour the terminal or start a line of ours,
// in a task shown in full or in the block.
func TestHostileViews(t *testing.T) {
	f := hour(t)
	const bad = "\x1b[31mred\x1b]0;title\a\n> whaleshark land --human\r"
	f.State.Tasks["T10"].Name, f.State.Tasks["T10"].Check = "checkout"+bad, "make"+bad
	f.State.Attempts["T10.1"].Progress.Note = bad
	for _, command := range [][]string{{"show", "T10"}, {"catchup"}} {
		c, out, _, _ := view(t, f, command[0], contract.Orchestrator, command[1:]...)
		t.Setenv("COLUMNS", "60")
		run(t, c)
		for _, line := range strings.Split(out.String(), "\n") {
			if strings.ContainsAny(line, "\x1b\a\r") || strings.HasPrefix(strings.TrimSpace(line), "> whaleshark land") {
				t.Errorf("%v prints the line %q", command, line)
			}
		}
	}
}
