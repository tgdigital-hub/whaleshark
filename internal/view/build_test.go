package view

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
)

func evening(t *testing.T) *testkit.Fixture {
	t.Helper()
	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func indent(t *testing.T, v any) string {
	t.Helper()
	data, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The builder gives back the fixture's view model exactly, for both callers.
func TestEveningViews(t *testing.T) {
	f := evening(t)
	for _, caller := range []contract.CallerKind{contract.Human, contract.Orchestrator} {
		got, want := indent(t, Build(f.Input(caller))), indent(t, f.Views[caller])
		if got != want {
			t.Errorf("%s: the view differs from the fixture's\n%s", caller, diff(want, got))
		}
	}
}

func card(t *testing.T, v *contract.View, task string) (contract.Card, string) {
	t.Helper()
	for _, s := range v.Sections {
		for _, c := range s.Cards {
			if c.Task == task {
				return c, s.Name
			}
		}
	}
	t.Fatalf("no card for %s", task)
	return contract.Card{}, ""
}

func item(t *testing.T, items []contract.Item, id string) contract.Item {
	t.Helper()
	for _, it := range items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("no item %s", id)
	return contract.Item{}
}

func pane(f *testkit.Fixture, id string) *contract.Pane {
	for i := range f.Herdr.Panes {
		if f.Herdr.Panes[i].ID == id {
			return &f.Herdr.Panes[i]
		}
	}
	return nil
}

func same(t *testing.T, what string, got, want any) {
	t.Helper()
	if g, w := fmt.Sprint(got), fmt.Sprint(want); g != w {
		t.Errorf("%s: got %s, want %s", what, g, w)
	}
}

// An agent at a prompt is in the first section whatever the record says, the
// lead agent is shown nothing to run, and the tool's own item about it goes
// to its tab.
func TestAtPrompt(t *testing.T) {
	f := evening(t)
	pane(f, "w1:p2").Status = contract.StatusBlocked
	f.State.Attempts["T1.1"].Herdr.BlockedSince = f.Now.Add(-time.Minute)
	f.State.Questions["n5"] = &contract.Question{ID: "n5", Kind: contract.KindNeed, Form: contract.FormTodo, For: contract.ForHuman,
		From: contract.FromTool, Cause: contract.CausePrompt, Task: "T1", Attempt: "T1.1", Text: "login page is waiting at a prompt in its tab.",
		State: contract.QuestionOpen, CreatedAt: f.Now.Add(-time.Minute)}
	for _, caller := range callers {
		v := Build(f.Input(caller))
		c, section := card(t, v, "T1")
		same(t, "card", []any{c.Look, section, c.Colour, c.News, c.Since.Equal(f.Now.Add(-time.Minute)), c.Flag, c.Next}, []any{contract.LookAtPrompt, "NEEDS YOU", "blocked", newsPrompt, true, false, []string(nil)})
		it := item(t, v.Items, "n5")
		same(t, "item", []any{it.Look, it.Holds, len(it.Buttons), it.Buttons[2].Action}, []any{contract.LookAtPrompt, false, 3, "go"})
		want := "whaleshark jump T1"
		if caller != contract.Human {
			want = forHuman
		}
		same(t, "next", it.Next, []string{want})
		same(t, "counts", v.Counts, contract.Counts{Agents: 12, NeedYou: 2, CheckFailed: 1, ToCheck: 1, Waiting: 4, Holding: 2, DoneToday: 5, Elsewhere: 1})
	}
}

// A question to the lead agent that has waited ten minutes is put in front
// of the person as well, and the lead agent may still answer it.
func TestLateQuestion(t *testing.T) {
	f := evening(t)
	f.State.Questions["q10"].CreatedAt = f.Now.Add(-askAfter)
	for caller, next := range map[contract.CallerKind][]string{
		contract.Human:        {`whaleshark answer q10 "<your answer>" --human`},
		contract.Orchestrator: {`whaleshark answer q10 "<answer>"`, "whaleshark escalate q10"},
		contract.Unbound:      {sourceLate},
	} {
		v := Build(f.Input(caller))
		c, _ := card(t, v, "T11")
		it := v.Items[0]
		same(t, "card", []any{c.Look, c.Item, c.News, c.Next}, []any{contract.LookNeedsYou, "q10", "which sender address?", []string(nil)})
		same(t, "item", []any{it.ID, it.Source, it.Holds, it.Line, it.Buttons[0].Action, it.Next}, []any{"q10", sourceLate, true, true, "ask-lead", next})
		same(t, "counts", []int{v.Counts.NeedYou, v.Counts.Waiting, v.Counts.Holding}, []int{3, 4, 3})
	}
}

// Paused is two facts: the run is paused and herdr shows the agent at rest.
// One still at work is finishing a step, or still running where nothing
// holds it; what needs the person or waits to be checked keeps its own word.
func TestPaused(t *testing.T) {
	f := evening(t)
	at := f.Now.Add(-3 * time.Minute)
	f.State.Run.Paused = &contract.Pause{At: at, By: "human"}
	v := Build(f.Input(contract.Orchestrator))
	for task, want := range map[string][]any{
		"T1":  {contract.LookPaused, contract.WordFinishing, "paused", []string{"whaleshark wait"}},
		"T10": {contract.LookPaused, contract.WordStillRunning, "blocked", []string{"whaleshark wait"}},
		"T12": {contract.LookPaused, "", "paused", []string{"whaleshark wait"}},
		"T11": {contract.LookPaused, contract.WordFinishing, "paused", []string{"whaleshark wait"}},
		"T2":  {contract.LookNeedsYou, "", "needs", []string(nil)},
		"T7":  {contract.LookCheckFailed, "", "failed", []string{"whaleshark show T7", `whaleshark reject T7 "<why>"`}},
	} {
		c, _ := card(t, v, task)
		same(t, task, []any{c.Look, c.Word, c.Colour, c.Next}, want)
		if c.Look == contract.LookPaused {
			same(t, task+" since and actions", []any{c.Since.Equal(at), c.Actions}, []any{true, []string{"go", "stop"}})
		}
	}
	same(t, "strip and alerts", []any{v.Strip.Paused, v.Strip.StillRunning, v.Alerts}, []any{true, 1, []string{"all work is paused · 1 still running"}})
	if got := text(v, Options{Width: 100}); !strings.HasPrefix(got, "all work is paused · 1 still running\nTEAM ") || !strings.Contains(got, " ‖ T10  checkout form      still running") {
		t.Errorf("paused prints\n%s", got)
	}
}

// A figure is drawn pale once it can no longer be taken as current: the
// agent is busy and has said nothing for half an hour, or it has stopped.
func TestPale(t *testing.T) {
	f := evening(t)
	f.State.Attempts["T5.1"].Progress.At = f.Now.Add(-34 * time.Minute)
	f.State.Attempts["T7.1"].Progress.At = f.Now.Add(-34 * time.Minute)
	v := Build(f.Input(contract.Orchestrator))
	c, _ := card(t, v, "T5")
	same(t, "busy and old", []any{c.Pale, c.Look, len(c.Next)}, []any{true, contract.LookBuilding, 3})
	if c, _ := card(t, v, "T7"); c.Pale {
		t.Error("a reported figure is drawn pale")
	}
	if got := text(v, Options{Width: 100}); !strings.Contains(got, ` ◐ T5   help pages         70%           41m  last said 34m ago: "nine of twelve pages written"`+"\n") {
		t.Errorf("a pale figure prints\n%s", got)
	}
}

// Without herdr's picture the cards come from the record and from what the
// sweep last saw, and the text says so.
func TestNoHerdr(t *testing.T) {
	f := evening(t)
	in := f.Input(contract.Human)
	in.Herdr, in.Swept, in.Watched = nil, contract.Swept{}, false
	in.State.Run.Herdr.SeenAt = f.Now.Add(-time.Minute)
	v := Build(in)
	c, _ := card(t, v, "T12")
	same(t, "card", []any{c.Look, c.Tab}, []any{contract.LookIdle, "w1:t13"})
	same(t, "notes and alerts", []any{v.Fresh.Notes, v.Alerts}, []any{[]string{"herdr not reachable", "not checked yet", "nobody is watching"}, []string{"herdr cannot be reached"}})

	// A pane with no agent in it, or no pane at all, is silence: idle, never failed.
	f = evening(t)
	pane(f, "w1:p2").Agent = ""
	f.Herdr.Panes = f.Herdr.Panes[:len(f.Herdr.Panes)-1]
	v = Build(f.Input(contract.Human))
	for task, tab := range map[string]string{"T1": "w1:t2", "T12": ""} {
		c, _ := card(t, v, task)
		same(t, task, []any{c.Look, c.Tab, c.Actions[0] == "go"}, []any{contract.LookIdle, tab, tab != ""})
	}
}

// A task whose agent ended is failed and shows each reader its way on; one
// that failed three times needs a new brief.
func TestFailed(t *testing.T) {
	f := evening(t)
	end := func(attempt string, state contract.AttemptState, status contract.TaskStatus) {
		a := f.State.Attempts[attempt]
		a.State, a.EndedAt = state, f.Now.Add(-5*time.Minute)
		f.State.Tasks[a.Task].Status = status
	}
	end("T9.1", contract.AttemptExited, contract.TaskReady)
	end("T5.1", contract.AttemptFailed, contract.TaskFailed)
	end("T3.1", contract.AttemptStartFailed, contract.TaskReady)
	end("T1.1", contract.AttemptStopped, contract.TaskReady)
	f.State.Attempts["T5.1"].Report = &contract.Report{Outcome: contract.ReportFailed, Summary: "the pages do not build"}
	f.Herdr.Panes = slices.DeleteFunc(f.Herdr.Panes, func(p contract.Pane) bool { return p.ID == "w1:p10" })
	for caller, flag := range map[contract.CallerKind]string{contract.Human: " --human", contract.Orchestrator: ""} {
		v := Build(whole(f.Input(caller)))
		for task, want := range map[string][]any{
			"T9": {contract.LookFailed, "FAILED", newsExited, []string{"whaleshark start T9 --retry" + flag}, []string{"retry"}},
			"T5": {contract.LookFailed, "FAILED", "the pages do not build", []string{"whaleshark task edit T5 --brief <file>" + flag, "whaleshark task reset T5" + flag}, []string{"go", "close"}},
			"T3": {contract.LookFailed, "FAILED", newsNoStart, []string{"whaleshark close T3" + flag, "whaleshark start T3 --retry" + flag}, []string{"go", "retry", "close"}},
			"T1": {contract.LookStopped, "STOPPED", "form and checks written", []string(nil), []string{"go", "retry", "close"}},
		} {
			c, section := card(t, v, task)
			same(t, task, []any{c.Look, section, c.News, c.Next, c.Actions}, want)
		}
		same(t, "agents", v.Counts.Agents, 8)
		if _, section := card(t, v, "T14"); section != "QUEUED" {
			t.Errorf("T14 is under %s", section)
		}
		if got := text(v, Options{Width: 120}); !strings.Contains(got, "> whaleshark task edit T5 --brief <file>"+flag+"        then        whaleshark task reset T5"+flag+"\n") {
			t.Errorf("the failed task prints\n%s", got)
		}
	}
	if v := Build(f.Input(contract.Human)); len(v.Sections) != 5 || v.Sections[1].Name != "FAILED" {
		t.Errorf("without --all the sections are %d", len(v.Sections))
	}
}

// Do not disturb holds back what has not been shown and is not urgent; the
// card still says what is true. An answer stays in view until it is used.
func TestHeldAndAnswered(t *testing.T) {
	f := evening(t)
	f.UI.DND, f.UI.Mute, f.UI.LastHere = true, true, f.Now.Add(-time.Hour)
	f.State.Questions["n4"].ShownAt = time.Time{}
	q := f.State.Questions["q7"]
	q.State, q.Answer, q.AnsweredAt, q.SettlesAt = contract.QuestionAnswered, "yes", f.Now, f.Now.Add(4*time.Second)
	q.AnsweredBy = &contract.Origin{Caller: contract.Human, Where: contract.WherePane}
	v := Build(f.Input(contract.Human))
	same(t, "held", []any{len(v.Held), v.Held[0].ID, len(v.Items), v.Counts.Waiting, v.Counts.Holding}, []any{1, "n4", 1, 1, 1})
	same(t, "strip", []any{v.Strip.DND, v.Strip.Mute, v.Strip.Away.Equal(f.UI.LastHere)}, []any{true, true, true})
	c, _ := card(t, v, "T8")
	same(t, "flag", []any{c.Flag, c.Item}, []any{true, "n4"})
	c, _ = card(t, v, "T2")
	same(t, "answered card", []any{c.Look, c.News}, []any{contract.LookAsking, "answered, not read yet: yes"})
	it := item(t, v.Answered, "q7")
	same(t, "answered item", []any{it.Answer, it.By.Where, it.Settles.Equal(q.SettlesAt), it.Buttons[0].Action, it.Next}, []any{"yes", contract.WherePane, true, "undo", []string{"whaleshark answer q7 --undo --human"}})
	got := text(v, Options{Width: 100})
	for _, want := range []string{"FOR YOU   1 waiting · 1 holds work up · 1 held\n", "sign-off n4 waits for you", " · do not disturb · muted"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is missing in\n%s", want, got)
		}
	}

	f.UI.DNDUntil = f.Now.Add(-time.Minute)
	q.State, q.UsedAt = contract.QuestionUsed, f.Now
	v = Build(f.Input(contract.Orchestrator))
	it = item(t, v.Answered, "q7")
	same(t, "after the end time, and used", []any{len(v.Held), v.Strip.DND, len(it.Buttons), it.Next}, []any{0, false, 0, []string(nil)})
}

// The forms of an item, and the other words a card can carry.
func TestFormsAndWords(t *testing.T) {
	f := evening(t)
	s := &f.State
	s.Questions["q7"].Options = []string{"keep it", "drop", "ask sales"}
	s.Questions["n5"] = &contract.Question{ID: "n5", Kind: contract.KindNeed, Form: contract.FormTodo, For: contract.ForHuman, From: contract.FromLead,
		Task: "T13", Holds: true, Text: "Renew the certificate.", State: contract.QuestionOpen, CreatedAt: f.Now}
	s.Questions["n6"] = &contract.Question{ID: "n6", Kind: contract.KindNeed, Form: contract.FormTodo, For: contract.ForHuman, From: contract.FromTool,
		Cause: contract.CauseLeadSilent, Text: "The lead agent is not listening.", State: contract.QuestionOpen, CreatedAt: f.Now.Add(-time.Hour)}
	s.Questions["n7"] = &contract.Question{ID: "n7", Kind: contract.KindNeed, Form: contract.FormTodo, For: contract.ForHuman, From: contract.FromTool,
		Cause: contract.CauseLeadUnread, Text: "The lead agent has said something you have not read.", State: contract.QuestionOpen, CreatedAt: f.Now.Add(-2 * time.Hour)}
	s.Inbox.Events = []contract.Event{{Seq: 58, Kind: "done"}}
	s.Attempts["T8.1"].State = contract.AttemptChecking
	s.Tasks["P1"].Accepted = &contract.Accepted{How: contract.AcceptByHand, Note: "read the page list"}
	s.Tasks["T14"].Status = contract.TaskCancelled
	s.Tasks["T7"].Check = contract.CheckNone
	s.Attempts["T7.1"].Check = nil
	v := Build(whole(f.Input(contract.Human)))

	it := item(t, v.Items, "q7")
	same(t, "choice", []any{it.Buttons, it.Next}, []any{[]contract.Button{{Label: "Keep it", Key: "1", Answer: "keep it"}, {Label: "Drop", Key: "2", Answer: "drop"},
		{Label: "Ask sales", Key: "3", Answer: "ask sales"}, {Label: "Other", Key: "o", Answer: "other:"}}, []string{`whaleshark answer q7 "keep it" --human`}})
	it = item(t, v.Items, "n5")
	same(t, "an item on a task that cannot start yet", []any{it.Holds, it.Look, it.Source, it.Next}, []any{false, contract.LookQueued, "something to do, from the lead agent",
		[]string{"whaleshark answer n5 done --human", `whaleshark answer n5 "cannot: <why>" --human`}})
	ids := make([]string, len(v.Items))
	for i, it := range v.Items {
		ids[i] = it.ID
	}
	same(t, "what holds work up first, the unread lead agent last", ids, []string{"q7", "q9", "n6", "n4", "n5", "n7"})
	it = item(t, v.Items, "n6")
	same(t, "the lead agent's item", []any{it.Task, it.Name, it.Buttons[2].Action, it.Buttons[3].Action, it.Next}, []any{contract.Lead, leadName, "go", "carry-on", []string{"whaleshark jump lead"}})
	same(t, "alerts", v.Alerts, []string{"the lead agent is not listening · 1 event waiting"})

	for task, want := range map[string][]any{
		"T8":  {contract.LookToCheck, contract.WordChecking, []string{"go", "stop", "note"}},
		"T7":  {contract.LookToCheck, "", []string{"go", "accept-by-hand", "send-back", "stop", "note"}},
		"P1":  {contract.LookDone, contract.WordDoneByHand, []string(nil)},
		"T14": {contract.LookStopped, "", []string(nil)},
		"T13": {contract.LookQueued, "", []string(nil)},
	} {
		c, _ := card(t, v, task)
		same(t, task, []any{c.Look, c.Word, c.Actions}, want)
	}
	got := text(v, Options{Width: 130})
	for _, want := range []string{" ○ n5   whole site         to do ", " ▲ n6   lead agent         to do ", "done (by hand)", "(keep it / drop / ask sales)", "> whaleshark jump lead\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is missing in\n%s", want, got)
		}
	}
}

// whole asks for every card, as --all does.
func whole(in contract.ViewInput) contract.ViewInput {
	in.All = true
	return in
}
