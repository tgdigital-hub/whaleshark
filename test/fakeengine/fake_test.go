package fakeengine_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/test/fakeengine"
)

// agent builds the fake agent once and names it for the double.
func agent(t *testing.T) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fakeagent")
	if runtime.GOOS == "windows" {
		bin += ".exe" // Windows starts a program only from a file with that ending
	}
	if out, err := exec.Command("go", "build", "-o", bin, "../fakeagent").CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	t.Setenv(testkit.EnvAgent, bin)
}

// listening is a double on a socket file, as a program a test starts reaches it.
func listening(t *testing.T) *fakeengine.Fake {
	t.Helper()
	dir, err := os.MkdirTemp("", "fe") // a socket's path is short
	if err != nil {
		t.Fatal(err)
	}
	f := fakeengine.New()
	t.Cleanup(func() { f.Close(); os.RemoveAll(dir) })
	if err := f.Listen(filepath.Join(dir, "s")); err != nil {
		t.Fatal(err)
	}
	return f
}

func pane(t *testing.T, f *fakeengine.Fake, id string) contract.Pane {
	t.Helper()
	snap, err := f.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(snap.Panes, func(p contract.Pane) bool { return p.ID == id })
	if i < 0 {
		return contract.Pane{}
	}
	return snap.Panes[i]
}

// until waits for something a fake agent brings about in its own time.
func until(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(20 * time.Second); !ok(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("it never came about: %s", what)
		}
	}
}

// The double starts the fake agent with its tab's variables, the pane's id
// and the way back, and of the test's own variables of ours only the clock.
func TestStartsAFakeAgentThatPrintsItsSurroundings(t *testing.T) {
	agent(t)
	t.Setenv(contract.EnvRun, "not-for-the-agent")
	t.Setenv(contract.EnvClock, filepath.Join(t.TempDir(), "clock"))
	f := listening(t)
	f.Script("T1.1", "hang")
	p, err := f.TabCreate(t.TempDir(), "login page", []string{contract.EnvAttempt + "=T1.1", contract.EnvTask + "=T1"})
	if err != nil || p.Focused != true || p.Label != "login page" {
		t.Fatalf("the first tab: %+v, %v", p, err)
	}
	if err := f.AgentStart("worker-one", "claude", p.ID, []string{"--model", "small"}, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := f.AgentStart("worker-two", "claude", p.ID, nil, time.Second); err == nil {
		t.Error("a second agent was started in a pane that holds one")
	}
	if err := f.Prompt("worker-one", "Read and follow "+filepath.Join(t.TempDir(), "prompt.md"), time.Second); err != nil {
		t.Fatal(err)
	}
	shown := ""
	until(t, "the agent's surroundings on its screen", func() bool {
		shown, _ = f.Screen(p.ID)
		return strings.Contains(shown, "fakeagent: ready")
	})
	for _, want := range []string{contract.EnvAttempt + "=T1.1", contract.EnvTask + "=T1", contract.EnvPane + "=" + p.ID, contract.EnvSocket + "=", contract.EnvClock + "="} {
		if !strings.Contains(shown, want) {
			t.Errorf("the agent's screen lacks %q:\n%s", want, shown)
		}
	}
	if strings.Contains(shown, "not-for-the-agent") {
		t.Errorf("a variable of the test's own reached the agent:\n%s", shown)
	}
	// Its script hangs: it says through its hook that it is idle.
	until(t, "idle after its script", func() bool { return pane(t, f, p.ID).Status == contract.StatusIdle })
	if got := pane(t, f, p.ID); got.Agent != "claude" || got.Name != "worker-one" || got.Session == "" {
		t.Errorf("the agent's pane: %+v", got)
	}
	// An agent at a prompt of its own is typed at by nobody.
	f.Push(contract.StatusBlocked, p.ID)
	if err := f.Point(p.ID, contract.PointMail, ""); !errors.Is(err, contract.ErrAgentBlocked) {
		t.Errorf("a pointer at an agent at a prompt: %v", err)
	}
}

func TestAnAgentThatDiesLeavesABareShell(t *testing.T) {
	agent(t)
	f := listening(t)
	f.Script("T1.1", "die")
	p, _ := f.TabCreate(t.TempDir(), "login page", []string{contract.EnvAttempt + "=T1.1"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, _, err := f.Events(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.AgentStart("worker-one", "claude", p.ID, nil, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := f.Prompt("worker-one", "Read and follow x", time.Second); err != nil {
		t.Fatal(err)
	}
	until(t, "the pane without its agent", func() bool { return pane(t, f, p.ID).Agent == "" })
	if got := pane(t, f, p.ID); got.ID != p.ID || got.Name != "" || got.Session != "" || got.Status != contract.StatusUnknown {
		t.Errorf("the pane the agent left: %+v", got)
	}
	if err := f.Point(p.ID, contract.PointMail, ""); !errors.Is(err, contract.ErrNoPane) {
		t.Errorf("a pointer at a bare shell: %v", err)
	}
}

// A prepared picture is taken as it is, what is opened after it is numbered
// past it, a change that is dropped reaches no reader of events, and a
// restart keeps the ids and the variables under a new instance.
func TestTakesAPreparedPictureAndRestarts(t *testing.T) {
	f := fakeengine.New()
	defer f.Close()
	fx, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	f.Load(fx.Terms)
	first, events, err := f.Events(t.Context())
	if err != nil || len(first.Panes) != len(fx.Terms.Panes)+1 {
		t.Fatalf("the picture has %d panes, the fixture %d and the dock (%v)", len(first.Panes), len(fx.Terms.Panes), err)
	}
	// The dock is in every picture, a prepared one too, and in no tab.
	dock := first.Panes[0]
	if !dock.Dock || !dock.Ours || dock.Tab != "" || dock.Label != contract.SideTitle || f.PaneClose(dock.ID) != contract.ErrDock {
		t.Errorf("the dock of the double: %+v", dock)
	}
	for _, want := range fx.Terms.Panes {
		if got := pane(t, f, want.ID); got != want {
			t.Errorf("pane %s is %+v, the fixture says %+v", want.ID, got, want)
		}
	}
	opened, err := f.TabCreate("/work", "one more", []string{"A=1"})
	if err != nil || slices.ContainsFunc(fx.Terms.Panes, func(p contract.Pane) bool { return p.ID == opened.ID || p.Tab == opened.Tab }) {
		t.Fatalf("a tab opened after the fixture: %+v, %v", opened, err)
	}
	if e := <-events; e.Kind != contract.EvOpened || e.Pane.ID != opened.ID || e.Seq != first.Seq+1 {
		t.Errorf("the event of the new tab: %+v", e)
	}
	f.Drop(contract.StatusBlocked, opened.ID)
	f.Push(testkit.PushFocused, opened.ID)
	if e := <-events; e.Kind != contract.EvFocus || e.Pane.Status != contract.StatusBlocked {
		t.Errorf("after a dropped change and a told one the next event is %+v", e)
	}
	f.Restart()
	if _, open := <-events; open {
		t.Error("a reader of events outlived the restart")
	}
	again, err := f.Snapshot(t.Context())
	if err != nil || again.Instance == first.Instance || len(again.Panes) != len(first.Panes)+1 {
		t.Fatalf("after the restart: %+v, %v", again, err)
	}
	if again.Panes[0].ID != dock.ID || again.Panes[0].Terminal == dock.Terminal {
		t.Errorf("the dock after the restart: %+v", again.Panes[0])
	}
	if got := pane(t, f, opened.ID); got.Tab != opened.Tab || got.Terminal == opened.Terminal || got.Label != "one more" {
		t.Errorf("the tab after the restart: %+v", got)
	}
	f.Close()
	if _, err := f.Version(); !errors.Is(err, contract.ErrEngineUnreachable) {
		t.Errorf("a closed double answered: %v", err)
	}
}

// beatFile names the file a command of the tests writes into for as long as
// it lives.
const beatFile = "FAKEENGINE_TEST_BEAT"

// TestMain is also a command an agent of the tests runs in the place of
// the real program: started as one, it says every few milliseconds that it
// is still there, for half a minute at most.
func TestMain(m *testing.M) {
	if out := os.Getenv(beatFile); out != "" && len(os.Args) > 1 && os.Args[1] == "progress" {
		for end := time.Now().Add(30 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
			os.WriteFile(out, []byte(time.Now().String()), 0o600)
		}
		return
	}
	os.Exit(m.Run())
}

// A command an agent is in the middle of ends with the agent, and has ended
// when Close returns: it writes nothing into a folder the test then removes.
func TestClosingEndsWhatAnAgentStarted(t *testing.T) {
	agent(t)
	beat := filepath.Join(t.TempDir(), "beat")
	t.Setenv(beatFile, beat)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f := listening(t)
	f.Script("T1.1", "progress 50 half")
	p, _ := f.TabCreate(t.TempDir(), "login page", []string{contract.EnvAttempt + "=T1.1", contract.EnvBin + "=" + self})
	if err := f.AgentStart("worker-one", "claude", p.ID, nil, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := f.Prompt("worker-one", "Read and follow x", time.Second); err != nil {
		t.Fatal(err)
	}
	until(t, "the agent's command at work", func() bool { _, err := os.Stat(beat); return err == nil })
	f.Close()
	was, _ := os.ReadFile(beat)
	time.Sleep(300 * time.Millisecond)
	if now, _ := os.ReadFile(beat); string(now) != string(was) {
		t.Errorf("the command an agent started went on after Close: it wrote %s, then %s", was, now)
	}
}

// An agent started again by a restart is at its prompt with its conversation
// behind it: a line typed at it sets it to nothing it has done before.
func TestARestartedAgentPlaysNothingAgain(t *testing.T) {
	agent(t)
	f := listening(t)
	f.Script("T1.1", "write-result")
	dir := t.TempDir()
	result, prompt := filepath.Join(dir, "result.md"), "Read and follow "+filepath.Join(dir, "prompt.md")
	p, _ := f.TabCreate(t.TempDir(), "login page", []string{contract.EnvAttempt + "=T1.1"})
	if err := f.AgentStart("worker-one", "claude", p.ID, nil, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := f.Prompt("worker-one", prompt, time.Second); err != nil {
		t.Fatal(err)
	}
	until(t, "the agent's result", func() bool { _, err := os.Stat(result); return err == nil })
	if err := os.Rename(result, result+".first"); err != nil {
		t.Fatal(err)
	}
	f.Restart()
	for _, line := range []string{prompt, "a second line"} {
		if err := f.Prompt("worker-one", line, time.Second); err != nil {
			t.Fatal(err)
		}
	}
	until(t, "the second line taken", func() bool {
		shown, _ := f.Screen(p.ID)
		return strings.Contains(shown, "fakeagent: told: a second line")
	})
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(result); err == nil {
		t.Error("an agent started again by a restart played its script a second time")
	}
}
