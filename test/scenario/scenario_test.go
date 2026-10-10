package scenario

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/panes"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/view"
)

func TestMain(m *testing.M) { Main(m) }

// tabs is a pane for this test only: one row for each pane of the
// terminals' picture, the clock, and on the last line what was last done to
// it. Done reads idle: the two are one state to the tool, and only herdr
// tells them apart.
func tabs(p *Project, name string, t *term.Term) {
	said, typed := name+", notices "+os.Getenv(contract.EnvNotices), ""
	for {
		t.Clear()
		picture, _ := p.Kit.Terms.Snapshot(context.Background())
		for y, pane := range picture.Panes {
			if pane.Status == contract.StatusDone {
				pane.Status = contract.StatusIdle
			}
			t.Put(0, y, t.W, pane.Label+" · "+pane.Status, term.Style{})
		}
		t.Put(0, t.H-2, t.W, contract.Now().Format("15:04:05"), term.Style{})
		t.Put(0, t.H-1, t.W, said, term.Style{})
		t.Flush()
		select {
		case ev := <-t.Events:
			switch {
			case ev.Kind == term.End:
				return
			case ev.Kind == term.Mouse && ev.Action == term.Press:
				label, _, _ := strings.Cut(t.Row(ev.Y), " · ")
				said = "clicked " + label
			case ev.Key == "enter":
				said, typed = "typed "+typed, ""
			case ev.Kind == term.KeyPress:
				typed += string(ev.Rune)
			}
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestSelf(t *testing.T) {
	p := Run(t, filepath.Join("testdata", "self.scn"), tabs)

	// The fake agent of T1.1 was started in a tab of its own and played its
	// script, each step a run of the real program, whatever that answers yet.
	s, dir := p.Record()
	pane := s.Attempts["T1.1"].Place.Pane
	if pane == "" || pane == "w1:p2" {
		t.Fatalf("T1.1 is recorded in pane %q, not in the tab its agent was started in", pane)
	}
	var shown string
	for range 500 {
		if shown, _ = p.Herdr.Screen(pane); strings.Contains(shown, "report done ok") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, want := range []string{contract.EnvAttempt + "=T1.1", contract.EnvPane + "=" + pane,
		"fakeagent: whaleshark progress 85 nearly there:", "fakeagent: whaleshark report done ok:"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the agent's tab does not show %q:\n%s", want, shown)
		}
	}
	token, _ := os.ReadFile(filepath.Join(dir, "attempts", "T1.1", "token"))
	if _, err := os.Stat(filepath.Join(dir, "attempts", "T1.1", "result.md")); err != nil || string(token) != "not-the-token\n" {
		t.Errorf("the result file (%v) or the wrong token (%q) is not beside the prompt", err, token)
	}

	// The commands reached the fake herdr through the stand-in on the PATH.
	if !slices.ContainsFunc(p.Herdr.Calls(), func(call []string) bool { return slices.Equal(call, []string{"status", "--json"}) }) {
		t.Error("version did not ask the fake herdr for its version")
	}

	// Nothing was typed as a key, and the tab of T1.1 was opened without the focus.
	for _, call := range p.Herdr.Calls() {
		if slices.Contains(call, "send-keys") || slices.Contains(call, "--focus") {
			t.Errorf("herdr was called with %v", call)
		}
	}
}

// The store, the view builder, status and the panes stand on one another:
// each line of the scenario goes through all of them.
func TestJoin(t *testing.T) {
	p := Run(t, filepath.Join("testdata", "join.scn"), func(p *Project, name string, tm *term.Term) {
		view.Plug(p.Kit)
		panes.Run(name, tm, p.Kit, p.Root, "")
	})
	// The click on a card went to that card's tab, and nothing else was asked of herdr.
	went := func(call []string) bool { return slices.Equal(call, []string{"tab", "focus", "w1:t4"}) }
	for range 500 {
		if slices.ContainsFunc(p.Herdr.Calls(), went) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("the click on the card of T3 did not go to its tab: %v", p.Herdr.Calls())
}

// After a restart of herdr a worker's tab holds its pane and none of our variables.
func TestRestart(t *testing.T) {
	p := Run(t, filepath.Join("testdata", "restart.scn"), tabs)
	if env := p.Command("T2.1", "version").Env; !slices.Contains(env, contract.EnvPane+"=w1:p3") || has(env, contract.EnvAttempt) {
		t.Errorf("a worker's command is run with %v", env)
	}
}

// A fixture is a project in three lines, changed first where a test needs another moment.
func TestPrepare(t *testing.T) {
	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	f.State.Tasks["T7"].Failures = 2
	p := Prepare(t, f, nil)
	if s, _ := p.Record(); s.Tasks["T7"].Failures != 2 || s.Run.Orchestrator.Pane != p.Lead {
		t.Errorf("the record on disk is not the fixture: %+v", s.Tasks["T7"])
	}
	// Each caller is given its own variables and none the test was started with.
	t.Setenv(contract.EnvPane, "w9:p9")
	t.Setenv(contract.EnvAttempt, "T9.9")
	t.Setenv(contract.EnvActivePane, "w9:p9")
	human, page, worker := p.Command(Human, "pause", "--everywhere"), p.Command(Page, "pause"), p.Command("T2.1", "mail")
	switch {
	case !slices.Equal(human.Args[1:], []string{"pause", "--human", "--everywhere"}),
		!slices.Contains(human.Env, contract.EnvPane+"="+p.Own), has(human.Env, contract.EnvAttempt), has(human.Env, contract.EnvActivePane):
		t.Errorf("the human's command is %v with %v", human.Args, human.Env)
	case has(page.Env, contract.EnvPane), has(page.Env, contract.EnvAttempt), !slices.Contains(page.Env, contract.EnvFrom+"="+contract.WherePage):
		t.Errorf("the page's command is run with %v", page.Env)
	case !slices.Contains(worker.Env, contract.EnvPane+"=w1:p3"), !slices.Contains(worker.Env, contract.EnvAttempt+"=T2.1"),
		!slices.Contains(worker.Env, contract.EnvRun+"=r3"), !slices.Contains(p.Command(Orch, "wait").Env, contract.EnvPane+"=w1:p1"):
		t.Errorf("the worker's command is run with %v", worker.Env)
	}
	out, err := p.Command("T2.1", "help", "ask").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ask a question") {
		t.Errorf("help ask as T2.1: %v\n%s", err, out)
	}
	state, _ := p.Kit.Platform.Dirs()
	var ctx contract.CtxFile
	if err := contract.ReadVersioned(p.Kit.Platform.Peek, contract.CtxPath(state.State, "w1:p2"), contract.FileVersion, &ctx); err != nil || ctx != f.Ctx["w1:p2"] {
		t.Errorf("the context file of w1:p2 is %+v (%v), the fixture says %+v", ctx, err, f.Ctx["w1:p2"])
	}
}

// has reports whether an environment sets a variable.
func has(env []string, name string) bool {
	return slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, name+"=") })
}

func TestWords(t *testing.T) {
	for line, want := range map[string][]string{
		`task add T1 "first task" --check 'make test' --name ""`: {"task", "add", "T1", "first task", "--check", "make test", "--name", ""},
		`  answer q1 it"s here"  `:                               {"answer", "q1", "its here"},
	} {
		if got := words(line); !slices.Equal(got, want) {
			t.Errorf("%s: %q", line, got)
		}
	}
	if id := deliveryID.FindString(`{"ok":true,"result":{"delivery":"d17","events":[{"kind":"done"}]}}`); id != "d17" {
		t.Errorf("the delivery id found is %q", id)
	}
}
