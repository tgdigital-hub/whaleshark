package fakeherdr

import (
	"bufio"
	"context"
	"encoding/json"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/herdr"
)

type platform struct{ contract.NoPlatform }

func (platform) Replace(tmp, final string) error { return os.Rename(tmp, final) }

func kit() *contract.Kit {
	k := contract.NewKit()
	k.Platform = platform{}
	return k
}

func start(t *testing.T) *Fake {
	f, err := New(kit())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Close)
	return f
}

// build compiles one of the two test programs into a folder of its own.
func build(t *testing.T, pkg, name string) string {
	out := filepath.Join(t.TempDir(), name)
	if msg, err := exec.Command("go", "build", "-o", out, pkg).CombinedOutput(); err != nil {
		t.Fatalf("building %s: %v\n%s", pkg, err, msg)
	}
	return out
}

// until waits for something another process or goroutine is doing.
func until(t *testing.T, what string, done func() bool) {
	t.Helper()
	for range 500 {
		if done() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("after five seconds still not: %s", what)
}

func panes(t *testing.T, h contract.Herdr) []contract.Pane {
	t.Helper()
	s, err := h.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s.Panes
}

func find(t *testing.T, h contract.Herdr, id string) contract.Pane {
	t.Helper()
	for _, p := range panes(t, h) {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("no pane %s", id)
	return contract.Pane{}
}

func TestStartsAFakeAgentThatPrintsItsEnvironment(t *testing.T) {
	t.Setenv(testkit.EnvAgent, build(t, "../fakeagent", "fakeagent"))
	t.Setenv(contract.EnvRun, "left-over-from-outside")
	f := start(t)
	f.Script("T1.1", `sleep 0.05 | edit notes.txt | hang`)
	cwd := t.TempDir()
	tab, err := f.TabCreate(cwd, "url parser", []string{contract.EnvAttempt + "=T1.1", contract.EnvTask + "=T1"})
	if err != nil || tab.Label != "url parser" || tab.Focused || tab.Agent != "" {
		t.Fatalf("tab create: %+v, %v", tab, err)
	}
	if err := f.AgentStart("h1-r1-t1-1", "claude", tab.ID, []string{"--model", "small"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	screen := func() string { s, _ := f.Screen(tab.ID); return s }
	until(t, "the fake agent printing its environment", func() bool { return strings.Contains(screen(), "fakeagent: ready") })
	for _, want := range []string{contract.EnvAttempt + "=T1.1\n", contract.EnvTask + "=T1\n", contract.EnvPane + "=" + tab.ID + "\n", "HERDR_ENV=1\n"} {
		if !strings.Contains(screen(), want) {
			t.Errorf("the agent's environment lacks %q:\n%s", want, screen())
		}
	}
	if strings.Contains(screen(), "left-over-from-outside") {
		t.Error("a variable of ours from outside the tab reached the agent")
	}
	if p := find(t, f, tab.ID); p.Agent != "claude" || p.Name != "h1-r1-t1-1" || p.Session == "" || p.Status != contract.StatusIdle {
		t.Errorf("the pane after agent start: %+v", p)
	}

	if err := f.Prompt("h1-r1-t1-1", "Read and follow "+filepath.Join(cwd, "prompt.md"), 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if p := find(t, f, tab.ID); p.Status != contract.StatusWorking {
		t.Errorf("after the prompt the agent is %s", p.Status)
	}
	until(t, "the script reaching its end", func() bool { return find(t, f, tab.ID).Status == contract.StatusDone })
	if _, err := os.Stat(filepath.Join(cwd, "notes.txt")); err != nil {
		t.Errorf("the edit step wrote nothing: %v", err)
	}
	if err := f.Point(tab.ID, contract.PointMail, ""); err != nil {
		t.Fatal(err)
	}
	until(t, "the pointer reaching the agent", func() bool { return strings.Contains(screen(), "fakeagent: told: whaleshark: a message") })

	// A restart, as the real one does it.
	_, events, err := f.Events(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before := find(t, f, tab.ID)
	f.Restart()
	until(t, "the event connection closing", func() bool {
		select {
		case _, open := <-events:
			return !open
		default:
			return false
		}
	})
	after := find(t, f, tab.ID)
	if after.Terminal == before.Terminal || after.Tab != before.Tab || after.Name != before.Name || after.Session != before.Session {
		t.Errorf("a restart must keep ids, name and session and change the terminal id:\n%+v\n%+v", before, after)
	}
	until(t, "the resumed agent printing its environment", func() bool { return strings.Contains(screen(), "fakeagent: ready") })
	if strings.Contains(screen(), "WHALESHARK_") || !strings.Contains(screen(), contract.EnvPane+"="+tab.ID+"\n") {
		t.Errorf("a restored tab has herdr's variables and none of ours:\n%s", screen())
	}

	for _, call := range f.Calls() {
		if slices.Contains(call, "send-keys") || slices.Contains(call, "send-text") {
			t.Errorf("a key was sent: %v", call)
		}
	}
}

func TestAnAgentThatDiesLeavesABareShell(t *testing.T) {
	t.Setenv(testkit.EnvAgent, build(t, "../fakeagent", "fakeagent"))
	f := start(t)
	f.Script("T2.1", "die")
	tab, _ := f.TabCreate(t.TempDir(), "second", []string{contract.EnvAttempt + "=T2.1"})
	if err := f.AgentStart("h1-r1-t2-1", "claude", tab.ID, nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := f.Prompt("h1-r1-t2-1", "Read and follow /nowhere/prompt.md", time.Minute); err != nil {
		t.Fatal(err)
	}
	until(t, "the agent leaving its pane", func() bool { return find(t, f, tab.ID).Agent == "" })
	if p := find(t, f, tab.ID); p.Name != "" || p.Status != contract.StatusUnknown {
		t.Errorf("the pane after its agent left: %+v", p)
	}
	if err := f.Point(tab.ID, contract.PointMail, ""); herdr.Code(err) != "agent_not_found" {
		t.Errorf("a pointer into a pane with no agent: %v", err)
	}
}

func TestTheRealAdapterDrivesTheFakeAsAProgram(t *testing.T) {
	f := start(t)
	dir := filepath.Dir(build(t, "./herdr", "herdr"))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, kv := range f.Env() {
		name, value, _ := strings.Cut(kv, "=")
		t.Setenv(name, value)
	}
	real := herdr.New(kit())
	if v, err := real.Version(); err != nil || v != Version {
		t.Fatalf("version %q, %v", v, err)
	}
	tab, err := real.TabCreate("/work/shop", "from outside", nil)
	if err != nil {
		t.Fatal(err)
	}
	side, err := real.Split(tab.ID, "right", 0.7)
	if err != nil || side.Tab != tab.Tab {
		t.Fatalf("split: %+v, %v", side, err)
	}
	pic, events, err := real.Events(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(pic.Panes, panes(t, f)) {
		t.Errorf("through the program:\n%+v\nin this process:\n%+v", pic.Panes, panes(t, f))
	}
	f.Push(contract.StatusBlocked, side.ID)
	if e := <-events; e.Kind != herdr.Status || e.Pane.ID != side.ID || e.Pane.Status != contract.StatusBlocked {
		t.Errorf("the event was %+v", e)
	}
	if err := real.TabRename(tab.Tab, "--focus"); err != nil || find(t, f, tab.ID).Label != "--focus" || find(t, f, tab.ID).Focused {
		t.Errorf("a label that looks like an option: %+v, %v", find(t, f, tab.ID), err)
	}
	if err := real.PaneClose("w1:p99"); herdr.Code(err) != "pane_not_found" {
		t.Errorf("closing a pane that is not there: %v", err)
	}
	if err := real.AgentStart("a", "claude", side.ID, nil, time.Second); herdr.Code(err) != "invalid_agent_timeout" {
		t.Errorf("a start timeout herdr refuses: %v", err)
	}
}

func TestRefusesAStatusSubscriptionThatNamesNoPane(t *testing.T) {
	f := start(t)
	for request, want := range map[string]string{
		`{"id":"a","method":"events.subscribe","params":{"subscriptions":[{"type":"pane.agent_status_changed"}]}}`:                    "invalid_request",
		`{"id":"a","method":"events.subscribe","params":{"subscriptions":[{"type":"pane.agent_status_changed","pane_id":"w1:p99"}]}}`: "pane_not_found",
	} {
		conn, err := net.Dial("unix", f.events.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		conn.Write([]byte(request + "\n"))
		lines := bufio.NewReader(conn)
		var answer struct{ Error herdr.Error }
		line, _ := lines.ReadBytes('\n')
		json.Unmarshal(line, &answer)
		if answer.Error.Code != want {
			t.Errorf("answer %s, want the error %s", line, want)
		}
		if _, err := lines.ReadBytes('\n'); err == nil {
			t.Error("the connection stayed open after the refusal")
		}
		conn.Close()
	}
}

func TestALostLineIsOnlyFoundByASnapshot(t *testing.T) {
	f := start(t)
	a, _ := f.TabCreate("/work/shop", "first", nil)
	b, _ := f.TabCreate("/work/shop", "second", nil)
	pic, events, err := f.Events(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.Lose()
	f.Push(contract.StatusBlocked, a.ID)
	f.Drop(contract.StatusWorking, a.ID)
	f.Push(contract.StatusWorking, b.ID)
	herdr.Apply(pic, <-events)
	if slices.Equal(pic.Panes, panes(t, f)) {
		t.Error("the picture built from events shows a change whose line was lost")
	}
	if p := find(t, f, a.ID); p.Status != contract.StatusWorking {
		t.Errorf("the snapshot shows %s", p.Status)
	}
}

func TestTakesTheFixturesPicture(t *testing.T) {
	fixture, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	f := start(t)
	f.Load(fixture.Herdr)
	slices.SortFunc(fixture.Herdr.Panes, func(a, b contract.Pane) int { return strings.Compare(a.ID, b.ID) })
	if got := panes(t, f); !slices.Equal(got, fixture.Herdr.Panes) {
		t.Errorf("the fake shows\n%+v\nthe fixture has\n%+v", got, fixture.Herdr.Panes)
	}
}

// A subscription cut at random misses no change: whatever is done to the
// fake, and however often the connection is closed under the reader, the
// picture built from one snapshot and the events after it ends up equal to
// herdr's own.
func TestASubscriptionCutAtRandomMissesNoChange(t *testing.T) {
	f := start(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	built := make(chan []contract.Pane, 1)
	built <- nil
	var connections atomic.Int32
	go func() {
		for ctx.Err() == nil {
			pic, events, err := f.Events(ctx)
			if err != nil {
				continue
			}
			connections.Add(1)
			<-built
			built <- pic.Panes
			for e := range events {
				herdr.Apply(pic, e)
				<-built
				built <- pic.Panes
			}
		}
	}()

	random := rand.New(rand.NewPCG(1, 2))
	states := []string{contract.StatusWorking, contract.StatusIdle, contract.StatusDone, contract.StatusBlocked, "gone", "focused"}
	for step := range 1500 {
		all := panes(t, f)
		if len(all) == 0 {
			f.TabCreate("/work/shop", "tab", nil)
			continue
		}
		pick := all[random.IntN(len(all))]
		switch n := random.IntN(100); {
		case n < 4 && len(all) < 25:
			tab, _ := f.TabCreate("/work/shop", "tab", nil)
			f.AgentStart("agent-"+strings.ReplaceAll(tab.ID, ":", "-"), "claude", tab.ID, nil, time.Minute)
		case n < 6 && len(all) < 25:
			f.Split(pick.ID, "down", 0.5)
		case n < 8 && len(all) > 3:
			f.PaneClose(pick.ID)
		case n < 10 && len(all) > 3:
			f.TabClose(pick.Tab)
		case n < 14:
			f.TabRename(pick.Tab, "name "+pick.ID)
		case n < 16:
			f.TabFocus(pick.Tab)
		case n < 18:
			f.Cut()
		case n < 20 && pick.Agent == "":
			f.AgentStart("again-"+strings.ReplaceAll(pick.ID, ":", "-"), "claude", pick.ID, nil, time.Minute)
		default:
			f.Push(states[random.IntN(len(states))], pick.ID)
		}
		if step%300 == 299 {
			caughtUp := func() bool {
				got := <-built
				built <- got
				return slices.Equal(got, panes(t, f))
			}
			for range 300 {
				if caughtUp() {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !caughtUp() {
				got := <-built
				t.Fatalf("after step %d the picture built from events is\n%+v\nherdr's own is\n%+v", step, got, panes(t, f))
			}
		}
	}
	if connections.Load() < 10 {
		t.Errorf("the connection was cut only %d times; the test proves little", connections.Load())
	}
}
