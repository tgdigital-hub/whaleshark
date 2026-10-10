package launch

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/rules"
	"github.com/tgdigital-hub/whaleshark/test/fakeengine"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func TestMain(m *testing.M) { scenario.Main(m) }

// evening is the fixture's run with n more tasks, R1 to Rn, ready to start
// and named "ready 1" to "ready n", and room for all of them.
func evening(t *testing.T, n int, edit func(*testkit.Fixture)) *scenario.Project {
	t.Helper()
	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	f.State.Run.Limit = 60
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("R%d", i)
		f.State.Tasks[id] = &contract.Task{ID: id, Name: fmt.Sprintf("ready %d", i), Title: "a ready task",
			Brief: "briefs/" + id + ".md", Check: contract.CheckNone, Placement: contract.PlaceShared,
			Status: contract.TaskReady, CreatedAt: f.Now}
	}
	if edit != nil {
		edit(f)
	}
	return scenario.Prepare(t, f, nil)
}

// between stands between a command and the engine's double: it refuses one
// kind of call with an error's name, or holds one kind until the test lets
// it go, and passes everything else on.
type between struct {
	p                  *scenario.Project
	addr, to           string
	hold, refuse, with string
	held               chan struct{} // one for every call that is being held
	let                chan bool     // true passes a held call on, false refuses it
}

// gone, as what a call is refused with, is a keeper that ends the
// connection and answers nothing.
const gone = "gone"

func stand(t *testing.T, p *scenario.Project, hold, refuse, with string) *between {
	t.Helper()
	dir, err := os.MkdirTemp("", "ln") // a short path: a socket's name has little room
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	b := &between{p: p, addr: l.Addr().String(), hold: hold, refuse: refuse, with: with, held: make(chan struct{}, 64), let: make(chan bool)}
	for _, kv := range p.Env("") {
		if to, ok := strings.CutPrefix(kv, contract.EnvSocket+"="); ok {
			b.to = to
		}
	}
	t.Cleanup(func() {
		l.Close()
		close(b.let)
		os.RemoveAll(dir)
	})
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go b.serve(conn)
		}
	}()
	return b
}

func (b *between) serve(conn net.Conn) {
	defer conn.Close()
	double, err := net.Dial("unix", b.to)
	if err != nil {
		return
	}
	defer double.Close()
	for {
		kind, body, err := contract.ReadFrame(conn)
		var c contract.WireCall
		if err != nil || json.Unmarshal(body, &c) != nil {
			return
		}
		name := ""
		if c.Op == b.refuse {
			name = b.with
		}
		if c.Op == b.hold {
			b.held <- struct{}{}
			if !<-b.let {
				name = contract.ErrNoPane.Error()
			}
		}
		switch {
		case name == gone:
			return
		case name != "":
			body, _ = json.Marshal(contract.WireReply{ID: c.ID, Err: name, Message: "said by the test"})
		case contract.WriteFrame(double, kind, body) != nil:
			return
		default:
			if _, body, err = contract.ReadFrame(double); err != nil {
				return
			}
		}
		if contract.WriteFrame(conn, contract.FrameReply, body) != nil {
			return
		}
	}
}

func (b *between) command(who string, args ...string) *exec.Cmd {
	cmd := b.p.Command(who, args...)
	cmd.Env = append(cmd.Env, contract.EnvSocket+"="+b.addr)
	return cmd
}

// run ends a command and returns what it printed and its exit code.
func run(t *testing.T, cmd *exec.Cmd) (string, int) {
	t.Helper()
	out, err := cmd.CombinedOutput()
	if exit := new(exec.ExitError); err != nil && !errors.As(err, &exit) {
		t.Fatal(err)
	}
	return string(out), cmd.ProcessState.ExitCode()
}

// must holds a command to its exit code and to words it prints.
func must(t *testing.T, cmd *exec.Cmd, exit int, words ...string) string {
	t.Helper()
	out, got := run(t, cmd)
	if got != exit {
		t.Fatalf("%s: exit %d, expected %d\n%s", strings.Join(cmd.Args[1:], " "), got, exit, out)
	}
	for _, w := range words {
		if !strings.Contains(out, w) {
			t.Fatalf("%s: expected %q in:\n%s", strings.Join(cmd.Args[1:], " "), w, out)
		}
	}
	return out
}

// calls is every call of one kind the double got.
func calls(p *scenario.Project, op string) (out []contract.WireCall) {
	for _, c := range p.Double.Calls() {
		if c.Op == op {
			out = append(out, c)
		}
	}
	return out
}

func attempt(t *testing.T, p *scenario.Project, id string, state contract.AttemptState) *contract.Attempt {
	t.Helper()
	s, _ := p.Record()
	a := s.Attempts[id]
	if a == nil || a.State != state {
		t.Fatalf("attempt %s is %+v, expected %s", id, a, state)
	}
	return a
}

func tabOpen(t *testing.T, p *scenario.Project, tab string) bool {
	t.Helper()
	snap, err := p.Double.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(snap.Panes, func(pane contract.Pane) bool { return pane.Tab == tab })
}

func locked(t *testing.T, p *scenario.Project, id string) bool {
	t.Helper()
	_, dir := p.Record()
	unlock, free, err := p.Kit.Platform.TryLock(filepath.Join(dir, "attempts", id, "start.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if free {
		unlock()
	}
	return !free
}

func TestCleanStart(t *testing.T) {
	p := evening(t, 2, nil)
	must(t, p.Command(scenario.Orch, "start", "R1", "ready 2"), 0, "R1 ready 1: working (R1.1)", "R2 ready 2: working (R2.1)")

	s, dir := p.Record()
	a := attempt(t, p, "R1.1", contract.AttemptWorking)
	if s.Tasks["R1"].Status != contract.TaskRunning {
		t.Errorf("R1 is %s", s.Tasks["R1"].Status)
	}
	if pl := a.Place; pl.Tab == "" || pl.Pane == "" || pl.Terminal == "" || pl.Cwd != p.Root || !pl.OpenedByUs {
		t.Errorf("place: %+v", pl)
	}
	token, err := os.ReadFile(filepath.Join(dir, "attempts", "R1.1", "token"))
	if err != nil || contract.TokenHash(string(token)) != a.TokenHash {
		t.Errorf("the token file does not hash to the record's %s: %v", a.TokenHash, err)
	}
	prompt, _ := os.ReadFile(filepath.Join(dir, "attempts", "R1.1", "prompt.md"))
	for _, want := range []string{`task R1 "ready 1"`, filepath.Join(dir, "briefs", "R1.md"), filepath.Join(dir, "attempts", "R1.1", "result.md"), "Other workers"} {
		if !strings.Contains(string(prompt), want) {
			t.Errorf("the prompt lacks %q:\n%s", want, prompt)
		}
	}
	if locked(t, p, "R1.1") {
		t.Error("start.lock is still held")
	}

	var tab contract.WireCall
	for _, c := range calls(p, contract.OpTabCreate) {
		if c.Label == "ready 1" {
			tab = c
		}
	}
	var names []string
	for _, kv := range tab.Env {
		name, _, _ := strings.Cut(kv, "=")
		names = append(names, name)
	}
	want := []string{contract.EnvRoot, contract.EnvRun, contract.EnvTask, contract.EnvAttempt, contract.EnvDepth, contract.EnvBin, "PORT_BASE", "PORT"}
	if !slices.Equal(names, want) {
		t.Errorf("the tab was made with %+v, expected exactly the variables %v", tab, want)
	}
	// Each task has ports of its own, ten of them, written down for the login.
	dirs, _ := p.Kit.Platform.Dirs()
	taken, err := contract.ReadSlots(os.ReadFile, dirs.State)
	if len(taken) != 2 || err != nil || taken[0].N == taken[1].N || taken[0].Size != 10 {
		t.Fatalf("the slots: %+v, %v", taken, err)
	}
	for _, slot := range taken {
		if port := fmt.Sprintf("PORT=%d", 20000+10*slot.N); slot.Task == "R1" && !slices.Contains(tab.Env, port) {
			t.Errorf("R1 has the slot %d and its tab %v", slot.N, tab.Env)
		}
	}
	if !slices.ContainsFunc(calls(p, contract.OpAgentStart), func(c contract.WireCall) bool {
		return c.Name == a.Agent.Name && c.Pane == a.Place.Pane && c.Millis == 180000
	}) {
		t.Errorf("no agent start for %s in %s: %+v", a.Agent.Name, a.Place.Pane, calls(p, contract.OpAgentStart))
	}
	if !slices.ContainsFunc(calls(p, contract.OpPrompt), func(c contract.WireCall) bool {
		return c.Name == a.Agent.Name && c.Text == "Read and follow "+filepath.Join(dir, "attempts", "R1.1", "prompt.md")
	}) {
		t.Errorf("no first prompt for %s: %+v", a.Agent.Name, calls(p, contract.OpPrompt))
	}

	must(t, p.Command(scenario.Orch, "start", "R1"), 5, "R1 is running")
	must(t, p.Command("T1.1", "start", "R2"), 5)
	must(t, p.Command(scenario.Orch, "start"), 2)
}

func TestReadyStartsUpToTheLimit(t *testing.T) {
	p := evening(t, 3, func(f *testkit.Fixture) { f.State.Run.Limit = 14 }) // twelve are live
	must(t, p.Command(scenario.Orch, "start", "--ready"), 0, "R1 ready 1: working", "R2 ready 2: working", "1 more ready tasks wait")
	s, _ := p.Record()
	if s.Tasks["R3"].Status != contract.TaskReady {
		t.Errorf("R3 is %s", s.Tasks["R3"].Status)
	}
	must(t, p.Command(scenario.Orch, "start", "R3"), 5, "limit")
}

func TestTerminalsFailingAtEachStage(t *testing.T) {
	for _, tc := range []struct {
		call, with, code string
		exit             int
		tab, item        bool
	}{
		{contract.OpTabCreate, "failed", "start_failed", 1, false, false},
		{contract.OpAgentStart, "failed", "start_failed", 1, true, false},
		{contract.OpAgentStart, contract.ErrAgentNotReady.Error(), "at_prompt", 1, true, true},
		{contract.OpPrompt, contract.ErrPromptStalled.Error(), "prompt_stalled", 1, true, false},
		{contract.OpPrompt, contract.ErrAgentBlocked.Error(), "at_prompt", 1, true, true},
		{contract.OpAgentStart, gone, "unreachable", 3, true, false},
	} {
		t.Run(tc.call+" "+tc.with, func(t *testing.T) {
			p := evening(t, 1, nil)
			b := stand(t, p, "", tc.call, tc.with)
			out := must(t, b.command(scenario.Orch, "start", "R1", "--json"), tc.exit)
			var e struct {
				Error struct {
					Code string
					Data struct{ Created []outcome }
				}
			}
			if err := json.Unmarshal([]byte(out), &e); err != nil || e.Error.Code != tc.code || len(e.Error.Data.Created) != 1 {
				t.Fatalf("expected the code %s and one thing created: %v\n%s", tc.code, err, out)
			}
			a := attempt(t, p, "R1.1", contract.AttemptStartFailed)
			s, _ := p.Record()
			if task := s.Tasks["R1"]; task.Status != contract.TaskReady || task.Failures != 0 {
				t.Errorf("R1 is %s with %d failures: a failed start counts none", task.Status, task.Failures)
			}
			if got := e.Error.Data.Created[0].Tab; (got != "") != tc.tab || got != a.Place.Tab || tc.tab && !tabOpen(t, p, got) {
				t.Errorf("created says the tab %q, the record %q: a failed start leaves its tab open and says so", got, a.Place.Tab)
			}
			if n := len(calls(p, contract.OpPrompt)); n > 0 {
				t.Errorf("%d prompts were typed", n)
			}
			item := ""
			for id, q := range s.Questions {
				if q.Cause == contract.CausePrompt && q.Attempt == "R1.1" && q.State == contract.QuestionOpen {
					item = id
				}
			}
			if (item != "") != tc.item {
				t.Fatalf("the to-do item about a prompt: %q", item)
			}
			if tc.tab {
				must(t, p.Command(scenario.Orch, "close", "R1"), 0, "closed: ready 1 (R1.1)")
				if s, _ := p.Record(); tabOpen(t, p, a.Place.Tab) || tc.item && s.Questions[item].State != contract.QuestionClosed {
					t.Error("close left the tab or its item open")
				}
			}
			must(t, p.Command(scenario.Orch, "start", "R1"), 5, "--retry")
			must(t, p.Command(scenario.Orch, "start", "R1", "--retry"), 0, "working (R1.2)")
		})
	}
}

// begin starts a command and waits until n of its calls are being held.
func (b *between) begin(t *testing.T, n int, cmd *exec.Cmd) (out *strings.Builder) {
	t.Helper()
	out = new(strings.Builder)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	for range n {
		select {
		case <-b.held:
		case <-time.After(30 * time.Second):
			t.Fatalf("the command never got to %s:\n%s", b.hold, out)
		}
	}
	return out
}

func TestStopDuringAStart(t *testing.T) {
	p := evening(t, 1, nil)
	b := stand(t, p, contract.OpAgentStart, "", "")
	cmd := b.command(scenario.Orch, "start", "R1")
	out := b.begin(t, 1, cmd)
	a := attempt(t, p, "R1.1", contract.AttemptStarting)
	if a.Place.Tab == "" || !locked(t, p, "R1.1") {
		t.Fatalf("mid-start the record should name the tab and start.lock be held: %+v", a.Place)
	}
	must(t, p.Command(scenario.Orch, "stop", "R1"), 0, "its tab: closed")
	b.let <- true
	cmd.Wait()
	if exit := cmd.ProcessState.ExitCode(); exit != 5 {
		t.Fatalf("the overtaken start: exit %d, expected 5\n%s", exit, out)
	}
	attempt(t, p, "R1.1", contract.AttemptStopped)
	if tabOpen(t, p, a.Place.Tab) || len(calls(p, contract.OpPrompt)) > 0 {
		t.Error("the tab is still open, or a prompt was typed after the stop")
	}
}

func TestStopKeepingTheTabDuringAStart(t *testing.T) {
	p := evening(t, 1, nil)
	b := stand(t, p, contract.OpAgentStart, "", "")
	cmd := b.command(scenario.Orch, "start", "R1")
	b.begin(t, 1, cmd)
	tab := attempt(t, p, "R1.1", contract.AttemptStarting).Place.Tab
	must(t, p.Command(scenario.Orch, "stop", "R1", "--keep-tab"), 0, "its tab: kept")
	b.let <- true
	cmd.Wait()
	if exit := cmd.ProcessState.ExitCode(); exit != 5 || tabOpen(t, p, tab) || len(calls(p, contract.OpPrompt)) > 0 {
		t.Errorf("exit %d: the start should give up with 5, close the tab it opened and type nothing", exit)
	}
}

func TestAStartKilledHalfway(t *testing.T) {
	p := evening(t, 1, nil)
	b := stand(t, p, contract.OpAgentStart, "", "")
	cmd := b.command(scenario.Orch, "start", "R1")
	b.begin(t, 1, cmd)
	cmd.Process.Kill()
	cmd.Wait()
	a := attempt(t, p, "R1.1", contract.AttemptStarting)
	if locked(t, p, "R1.1") {
		t.Fatal("start.lock outlived its start")
	}
	// What the sweep does with a starting attempt whose lock is free.
	s, _ := p.Record()
	err := p.Kit.Store.Change(p.Root, s.Run.ID, func(s *contract.State) error {
		if !(rules.Rules{}).Seen(s, "R1.1", nil, false, contract.ProjectDefaults(), contract.Now()) {
			return errors.New("the sweep left it")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	attempt(t, p, "R1.1", contract.AttemptStartFailed)
	must(t, p.Command(scenario.Orch, "start", "R1"), 5, "start R1 --retry")
	must(t, p.Command(scenario.Orch, "start", "R1", "--retry", "--model", "a b"), 2, "plain word")
	must(t, p.Command(scenario.Orch, "start", "R1", "--retry", "--model", "fast-1"), 0, "working (R1.2)")
	_, dir := p.Record()
	retried := attempt(t, p, "R1.2", contract.AttemptWorking)
	if retried.RetryOf != "R1.1" || retried.Place.Tab == a.Place.Tab || retried.Agent.Model != "fast-1" {
		t.Errorf("the retry: %+v", retried)
	}
	if !slices.ContainsFunc(calls(p, contract.OpAgentStart), func(c contract.WireCall) bool {
		// The agent's own arguments: the settings of the hooks and then the model.
		return c.Name == retried.Agent.Name && len(c.Argv) > 1 && slices.Equal(c.Argv[len(c.Argv)-2:], []string{"--model", "fast-1"})
	}) {
		t.Errorf("the model is not on the agent's own line: %+v", calls(p, contract.OpAgentStart))
	}
	if prompt, _ := os.ReadFile(filepath.Join(dir, "attempts", "R1.2", "prompt.md")); !strings.Contains(string(prompt), "R1.1 ended as start_failed") {
		t.Errorf("the retry's prompt says nothing of the attempt before it:\n%s", prompt)
	}
}

func TestRetryRefusedWhileTheOldPaneHoldsAnAgent(t *testing.T) {
	p := evening(t, 0, nil)
	must(t, p.Command(scenario.Orch, "start", "--ready"), 0, "Nothing is ready to start.")
	must(t, p.Command(scenario.Orch, "close", "T1"), 5, "live attempt")
	must(t, p.Command(scenario.Orch, "stop", "T1", "--keep-tab"), 0)
	old := attempt(t, p, "T1.1", contract.AttemptStopped)
	must(t, p.Command(scenario.Orch, "start", "T1", "--retry"), 5, "still holds an agent")
	if !tabOpen(t, p, old.Place.Tab) {
		t.Fatal("the kept tab is gone")
	}
	must(t, p.Command(scenario.Orch, "close", "--settled"), 0, "closed: login page (T1.1)")
	must(t, p.Command(scenario.Orch, "start", "T1", "--retry"), 0, "working (T1.2)")
	must(t, p.Command(scenario.Orch, "close", "--settled"), 0, "No tab to close.")
}

func TestStopAllDuringAStartOfTwenty(t *testing.T) {
	p := evening(t, 20, nil)
	b := stand(t, p, contract.OpAgentStart, "", "")
	cmd := b.command(scenario.Orch, "start", "--ready", "--json")
	out := b.begin(t, 20, cmd)
	before := len(p.Double.Calls())
	must(t, p.Command(scenario.Unbound, "pause"), 5, "the person's")
	must(t, p.Command(scenario.Orch, "pause"), 5)
	paused := must(t, p.Command(scenario.Human, "pause", "--everywhere"), 0, "Paused. 11 agents stop at their next step", "still running: checkout form")
	if strings.Contains(paused, "ready") {
		t.Errorf("an attempt that is only starting has no agent to be still running:\n%s", paused)
	}
	if n := len(p.Double.Calls()); n != before {
		t.Errorf("pause reached for the terminals: %+v", p.Double.Calls()[before:])
	}
	for range 20 {
		b.let <- true
	}
	cmd.Wait()
	var e struct {
		Error struct {
			Code string
			Data struct{ Created []outcome }
		}
	}
	if err := json.Unmarshal([]byte(out.String()), &e); err != nil || cmd.ProcessState.ExitCode() != 1 || e.Error.Code != "paused" || len(e.Error.Data.Created) != 20 {
		t.Fatalf("exit %d: expected 1, the code paused and twenty tabs listed: %v\n%s", cmd.ProcessState.ExitCode(), err, out)
	}
	s, _ := p.Record()
	for i := 1; i <= 20; i++ {
		id := fmt.Sprintf("R%d", i)
		if a, task := s.Attempts[id+".1"], s.Tasks[id]; a.State != contract.AttemptStartFailed || task.Status != contract.TaskReady || task.Failures != 0 {
			t.Errorf("%s: attempt %s, task %s, %d failures", id, a.State, task.Status, task.Failures)
		}
	}

	// Nothing was typed into any agent, and what changes the run is refused.
	for _, c := range p.Double.Calls()[before:] {
		if c.Op != contract.OpAgentStart {
			t.Errorf("after Stop all the terminals were asked: %+v", c)
		}
	}
	must(t, p.Command(scenario.Orch, "start", "R1", "--retry"), 5, "paused")
	must(t, p.Command(scenario.Orch, "close", "--settled"), 5, "paused")
	must(t, p.Command(scenario.Human, "pause"), 0)
	must(t, p.Command(scenario.Orch, "stop", "T9"), 0, "its tab: closed")
}

func TestResumeSkipsAnAgentAtAPrompt(t *testing.T) {
	p := evening(t, 0, func(f *testkit.Fixture) {
		q := f.State.Questions["q7"] // answered while its worker's ask is no longer waiting
		q.State, q.Answer, q.AnsweredAt = contract.QuestionAnswered, "yes", f.Now
	})
	if err := os.WriteFile(filepath.Join(p.Root, "whaleshark.toml"), []byte("[limits]\nresume_per_second = 20\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirs, _ := p.Kit.Platform.Dirs()
	must(t, p.Command(scenario.Human, "resume"), 5, "Nothing here is paused")
	must(t, p.Command(scenario.Human, "pause"), 0)
	if mark, err := contract.Paused(os.ReadFile, dirs.State); err != nil || mark == nil {
		t.Fatalf("no pause mark: %v", err)
	}
	// A second run of the login is paused too: the one mark holds both.
	other := rules.Rules{}.NewRun("r4", "another job", 0, nil, contract.Binding{Pane: "w1:p30"}, contract.Now())
	other.Run.Paused = &contract.Pause{At: contract.Now(), By: contract.WhereTyped}
	if err := p.Kit.Store.Create(p.Root, other); err != nil {
		t.Fatal(err)
	}
	must(t, p.Command(scenario.Human, "resume"), 5, "resume --everywhere --human")
	p.Double.Push(contract.StatusBlocked, "w1:p4") // the pane of T3, search box
	before := len(p.Double.Calls())
	must(t, p.Command(scenario.Orch, "resume"), 5)
	began := time.Now()
	must(t, p.Command(scenario.Human, "resume", "--everywhere"), 0, "9 agents were told", "at a prompt, not resumed: search box")
	if took := time.Since(began); took < 8*50*time.Millisecond {
		t.Errorf("nine agents at twenty a second were told in %v", took)
	}
	if mark, _ := contract.Paused(os.ReadFile, dirs.State); mark != nil {
		t.Error("the pause mark is still there")
	}
	if r4, err := p.Kit.Store.Read(p.Root, "r4"); err != nil || r4.Run.Paused != nil {
		t.Errorf("the other run is still paused: %v", err)
	}

	told := map[string]string{}
	for _, c := range p.Double.Calls()[before:] {
		if line := fakeengine.Typed(c); line != "" {
			told[c.Pane] = line
		} else if c.Op != contract.OpSnapshot {
			t.Errorf("resume asked the terminals: %+v", c)
		}
	}
	s, _ := p.Record()
	if s.Run.Paused != nil || told[p.Lead] != string(contract.PointLeadOn) {
		t.Errorf("the run is still paused, or the lead agent was told %q", told[p.Lead])
	}
	for id, a := range s.Attempts {
		if a.State != contract.AttemptWorking && a.State != contract.AttemptAsked {
			if len(a.Mail) > 0 || told[a.Place.Pane] != "" {
				t.Errorf("%s is %s and was told", id, a.State)
			}
			continue
		}
		mail := a.Mail[len(a.Mail)-1]
		if !strings.HasPrefix(mail.Text, "You were paused at ") || strings.Contains(mail.Text, "ask --resume q7") != (id == "T2.1") {
			t.Errorf("%s got the message %q", id, mail.Text)
		}
		if blocked := id == "T3.1"; blocked != (told[a.Place.Pane] == "") || blocked != mail.NudgedAt.IsZero() ||
			!blocked && told[a.Place.Pane] != string(contract.PointResumed) {
			t.Errorf("%s: pointer %q, nudged at %v", id, told[a.Place.Pane], mail.NudgedAt)
		}
	}
}

func TestAButtonsStartRunsInATabOfItsOwn(t *testing.T) {
	p := evening(t, 2, nil)
	must(t, p.Command(scenario.Page, "start", "ready 1"), 0, "a tab of its own: start ready 1")
	if s, _ := p.Record(); s.Tasks["R1"].Status != contract.TaskReady {
		t.Error("the button's own child started the task")
	}
	made := calls(p, contract.OpTabCreate)
	ran := calls(p, contract.OpRun)
	if len(made) != 1 || made[0].Label != "start ready 1" || len(ran) != 1 {
		t.Fatalf("the tab %+v and the program started in it %+v", made, ran)
	}
	// The program is started from a list of arguments: nothing is quoted
	// for any shell.
	line := strings.Join(ran[0].Argv[1:], " ")
	if !strings.HasPrefix(line, "start R1 --human --root ") || !strings.HasSuffix(line, " --run r3") {
		t.Fatalf("the program started in the tab: %q", ran[0].Argv)
	}

	// The same line, run in such a tab, is still the button's and is not
	// moved again; it closes the tab when it ends well.
	pane, err := p.Double.TabCreate(p.Root, "start ready 2", nil)
	if err != nil {
		t.Fatal(err)
	}
	cmd := p.Command(scenario.Human, "start", "R2")
	cmd.Env = append(cmd.Env, contract.EnvPane+"="+pane.ID, contract.EnvOwnTab+"="+contract.WherePane)
	must(t, cmd, 0)
	attempt(t, p, "R2.1", contract.AttemptWorking)
	if tabOpen(t, p, pane.Tab) {
		t.Error("the tab of a command that ended well is still open")
	}

	// One that fails stays, with what it printed, until the person presses
	// Enter: its tab ends with its program.
	failing := p.Command(scenario.Human, "start", "R2")
	failing.Env = append(failing.Env, contract.EnvPane+"="+pane.ID, contract.EnvOwnTab+"="+contract.WherePane)
	keys, err := failing.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var said strings.Builder
	failing.Stdout, failing.Stderr = &said, &said
	if err := failing.Start(); err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() { failing.Wait(); close(ended) }()
	select {
	case <-ended:
		t.Fatalf("a failed command in a tab of its own ended without waiting:\n%s", &said)
	case <-time.After(time.Second):
	}
	keys.Write([]byte("\n"))
	<-ended
	if failing.ProcessState.ExitCode() != 5 || !strings.Contains(said.String(), "R2 is running") || !strings.Contains(said.String(), "press Enter") {
		t.Errorf("the failed command: exit %d\n%s", failing.ProcessState.ExitCode(), &said)
	}
}

func TestAgentName(t *testing.T) {
	a, b := agentName("/one/project", "r3", "T3.1"), agentName("/another/project", "r3", "T3.1")
	if a == b || !strings.HasSuffix(a, "-r3-t3-1") || len(a) != len("h0000-r3-t3-1") || strings.ToLower(a) != a {
		t.Errorf("%s and %s", a, b)
	}
}

// started is the call that started the agent of an attempt.
func started(t *testing.T, p *scenario.Project, id string) contract.WireCall {
	t.Helper()
	s, _ := p.Record()
	for _, c := range calls(p, contract.OpAgentStart) {
		if a := s.Attempts[id]; a != nil && c.Name == a.Agent.Name {
			return c
		}
	}
	t.Fatalf("no agent was started for %s", id)
	return contract.WireCall{}
}

// card is how a task's card reads on the person's status.
func card(t *testing.T, p *scenario.Project, task string) string {
	t.Helper()
	var v struct{ Result contract.View }
	p.Clock(10 * time.Second) // past the sweep's own interval: the status is of this moment
	out := must(t, p.Command(scenario.Human, "status", "--json"), 0)
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, sec := range v.Result.Sections {
		for _, c := range sec.Cards {
			if c.Task == task {
				return strings.TrimSpace(string(c.Look) + " " + c.Word)
			}
		}
	}
	t.Fatalf("no card for %s:\n%s", task, out)
	return ""
}

func approve(t *testing.T, p *scenario.Project, toml string) {
	t.Helper()
	err := os.WriteFile(filepath.Join(p.Root, "whaleshark.toml"), []byte(toml), 0o600)
	lines, lerr := contract.TrustLines(p.Root)
	if err = errors.Join(err, lerr); err == nil {
		err = contract.WriteTrust(p.Kit.Platform, p.Root, lines, contract.Now())
	}
	if err != nil {
		t.Fatal(err)
	}
}

// Two kinds of agent, one the gate holds and one it does not, through Stop
// all, Resume and a retry in the earlier conversation; and a project's
// arguments for its agents, which count only once a person approved them.
func TestTwoKindsThroughStopAllAndResume(t *testing.T) {
	p := evening(t, 2, nil)
	toml := "[agent]\nargs = [\"--permission-mode\", \"acceptEdits\", \"--allowedTools\", \"Bash({whaleshark}:*)\", \"Bash(git:*)\"]\n" +
		"[check]\ntimeout_seconds = 86400\n[worktrees]\nshare = [\"node_modules\"]\n"
	if err := os.WriteFile(filepath.Join(p.Root, "whaleshark.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	p.Double.Script("R2.1", "sleep 300") // at work until the test says otherwise
	must(t, p.Command(scenario.Orch, "start", "R1"), 0, "working (R1.1)")
	must(t, p.Command(scenario.Orch, "start", "R2", "--agent", "codex", "--model", "small-1"), 0, "working (R2.1)")
	one, two := attempt(t, p, "R1.1", contract.AttemptWorking), attempt(t, p, "R2.1", contract.AttemptWorking)
	if !one.Gated || two.Gated || two.Agent.Kind != "codex" {
		t.Fatalf("the gate holds Claude Code and not Codex, which has not been run: %+v, %+v", one, two)
	}
	first, second := started(t, p, "R1.1"), started(t, p, "R2.1")
	if first.Kind != "claude" || len(first.Argv) != 2 || first.Argv[0] != "--settings" {
		t.Errorf("Claude Code was started with %q: its hooks, and nothing nobody approved", first.Argv)
	}
	if second.Kind != "codex" || !slices.Equal(second.Argv, []string{"--model", "small-1"}) {
		t.Errorf("Codex was started with %q", second.Argv)
	}
	s, _ := p.Record()
	said := 0
	for _, e := range s.Inbox.Events {
		if e.Kind == "untrusted" && strings.Contains(e.Text, "[agent] args") {
			said++
		}
	}
	if said != 1 {
		t.Errorf("%d events say that the project's arguments were not used, expected one for both starts", said)
	}
	// Every other setting nobody approved says so as well, once; one that
	// is for copies of the code only where a task has one.
	if got := strings.Join(kindsOf(s, "untrusted"), "\n"); strings.Count(got, "[check] timeout_seconds") != 1 || strings.Contains(got, "[worktrees]") {
		t.Errorf("the settings nobody approved were said as:\n%s", got)
	}

	// Stop all: the held one reads paused once it rests, the other still
	// running for as long as the terminals show it at work.
	must(t, p.Command(scenario.Human, "pause"), 0, "still running: ready 2")
	p.Double.Push(contract.StatusIdle, one.Place.Pane)
	if a, b := card(t, p, "R1"), card(t, p, "R2"); a != "paused" || b != "paused still running" {
		t.Errorf("after Stop all the cards read %q and %q", a, b)
	}
	p.Double.Push(contract.StatusIdle, two.Place.Pane)
	if b := card(t, p, "R2"); b != "paused" {
		t.Errorf("at rest the second card reads %q", b)
	}
	must(t, p.Command(scenario.Human, "resume"), 0, "Resumed.")

	// The sweep has written each conversation's id down; a retry goes on in it.
	one, two = attempt(t, p, "R1.1", contract.AttemptWorking), attempt(t, p, "R2.1", contract.AttemptWorking)
	if one.Agent.Session == "" || two.Agent.Session == "" || one.Agent.Session == two.Agent.Session {
		t.Fatalf("the sessions recorded: %q and %q", one.Agent.Session, two.Agent.Session)
	}
	must(t, p.Command(scenario.Orch, "start", "R1", "--resume"), 2, "--retry")
	must(t, p.Command(scenario.Orch, "stop", "R1"), 0)
	must(t, p.Command(scenario.Orch, "stop", "R2"), 0)
	approve(t, p, toml)
	must(t, p.Command(scenario.Orch, "start", "R1", "--retry", "--resume"), 0, "working (R1.2), in the conversation "+one.Agent.Session)
	// The line that was tried on a real agent: edits, this program by the
	// path a worker calls it by, and git, which a worker commits with.
	self, _ := exec.LookPath("whaleshark")
	approved := []string{"--permission-mode", "acceptEdits", "--allowedTools", "Bash(" + self + ":*)", "Bash(git:*)"}
	if argv := started(t, p, "R1.2").Argv; len(argv) != 9 || !slices.Equal(argv[:2], []string{"--resume", one.Agent.Session}) || !slices.Equal(argv[4:], approved) {
		t.Errorf("the retry was started with %q: the conversation first, the approved arguments %q last", argv, approved)
	}
	if got := attempt(t, p, "R1.2", contract.AttemptWorking).Agent.Session; got != one.Agent.Session {
		t.Errorf("the new attempt records the conversation %q", got)
	}
	// Another kind cannot go on in it, and says so.
	must(t, p.Command(scenario.Orch, "start", "R2", "--retry", "--resume"), 0, "working (R2.2), in a new conversation")
	must(t, p.Command(scenario.Orch, "stop", "R2"), 0)
	must(t, p.Command(scenario.Orch, "start", "R2", "--retry", "--resume", "--agent", "codex"), 0, "working (R2.3), in a new conversation")
	if argv := started(t, p, "R2.3").Argv; slices.Contains(argv, "resume") {
		t.Errorf("Codex went on in a conversation of Claude Code's: %q", argv)
	}
}

// In a shared folder nothing keeps two workers apart: a task whose files
// meet those of one at work there is not started, unless told to.
func TestSharedTasksOnTheSameFiles(t *testing.T) {
	p := evening(t, 4, func(f *testkit.Fixture) {
		for id, owns := range map[string][]string{"R1": {"lib/url/**"}, "R2": {"notes/*.md", "lib/**"}, "R3": {"lib/urls.go"}, "R4": {"lib/url"}} {
			f.State.Tasks[id].Owns = owns
		}
	})
	must(t, p.Command(scenario.Orch, "start", "R1"), 0)
	must(t, p.Command(scenario.Orch, "start", "R2"), 5, "in one folder with R1", "start R2 --allow-overlap")
	if s, _ := p.Record(); len(s.Tasks["R2"].Attempts) != 0 {
		t.Error("the refused task has an attempt")
	}
	dirs, _ := p.Kit.Platform.Dirs()
	if taken, _ := contract.ReadSlots(os.ReadFile, dirs.State); len(taken) != 1 || taken[0].Task != "R1" {
		t.Errorf("the refused start kept its ports: %+v", taken)
	}
	must(t, p.Command(scenario.Orch, "start", "R3"), 0)
	must(t, p.Command(scenario.Orch, "start", "R4", "--json"), 5, "shared_overlap")
	must(t, p.Command(scenario.Orch, "start", "R2", "R4", "--allow-overlap"), 0, "R2 ready 2: working", "R4 ready 4: working")
}

const brief = "# Target\nthe parser\n## Change\nadd it\nConstraints: none\n**Ownership** src\nAcceptance\nit passes\n"

// A Codex worker in a copy of the code of its own: its hooks in that copy
// and out of git's sight, the two folders that lie outside it, its ports in
// the copy's record, and the brief of a sync it still owes in its prompt.
func TestCodexInItsOwnCopy(t *testing.T) {
	// The program's git and the test's read the same settings, the test
	// kit's: a machine whose own turn line ends on the way out would have
	// the one write a file the other then reads as changed.
	for _, pair := range testkit.GitEnv()[len(os.Environ()):] {
		name, value, _ := strings.Cut(pair, "=")
		t.Setenv(name, value)
	}
	p := scenario.Prepare(t, nil, nil)
	testkit.Git(t, p.Root, "init", "-q", "-b", "main")
	testkit.Git(t, p.Root, "config", "user.name", "a test")
	testkit.Git(t, p.Root, "config", "user.email", "test@localhost")
	testkit.Commit(t, p.Root, "start", map[string]string{"b.md": brief, ".gitignore": "whaleshark.toml\n"})
	must(t, p.Command(scenario.Orch, "init"), 0)
	must(t, p.Command(scenario.Orch, "run", "new", "a parser"), 0)
	approve(t, p, "[worktrees]\nport_block = 4\n[agent]\nargs = [\"--sandbox\", \"workspace-write\"]\n")
	must(t, p.Command(scenario.Orch, "task", "add", "T1", "the parser", "--brief", "b.md", "--check", "none", "--agent", "codex"), 0)
	s, run := p.Record()
	if err := os.WriteFile(filepath.Join(run, "sync-T1.md"), []byte("Merge main first: two commits behind.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := p.Kit.Store.Change(p.Root, s.Run.ID, func(s *contract.State) error {
		s.Tasks["T1"].SyncBrief = "sync-T1.md"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	must(t, p.Command(scenario.Orch, "start", "T1"), 0, "working (T1.1)")

	s, _ = p.Record()
	tree := s.Tasks["T1"].Worktree
	a := attempt(t, p, "T1.1", contract.AttemptWorking)
	if tree == nil || a.Place.Cwd != tree.Path || a.Gated {
		t.Fatalf("the copy %+v, the attempt %+v", tree, a)
	}
	if _, err := os.Stat(filepath.Join(tree.Path, ".codex", "hooks.json")); err != nil {
		t.Error(err)
	}
	if left := testkit.Git(t, tree.Path, "status", "--porcelain"); left != "" {
		t.Errorf("git sees in the copy: %s", left)
	}
	dir := filepath.Join(run, "attempts", "T1.1")
	want := []string{"--add-dir", dir, "--add-dir", filepath.Dir(filepath.Join(run, s.Tasks["T1"].Brief)), "--sandbox", "workspace-write"}
	if argv := started(t, p, "T1.1").Argv; !slices.Equal(argv, want) {
		t.Errorf("Codex was started with %q, expected %q", argv, want)
	}
	dirs, _ := p.Kit.Platform.Dirs()
	taken, _ := contract.ReadSlots(os.ReadFile, dirs.State)
	if len(taken) != 1 || taken[0].Size != 4 || tree.Slot != taken[0].N {
		t.Errorf("the slot %+v, the copy's %d", taken, tree.Slot)
	}
	// The guide puts one line of its own between the heading and the brief.
	prompt, _ := os.ReadFile(filepath.Join(dir, "prompt.md"))
	if _, after, found := strings.Cut(string(prompt), "## Sync\n"); !found || !strings.Contains(after, "\nMerge main first: two commits behind.\n\n") {
		t.Errorf("the prompt lacks the sync it owes:\n%s", prompt)
	}
}

// kindsOf is the text of every waiting event of one kind.
func kindsOf(s *contract.State, kind string) (texts []string) {
	for _, e := range s.Inbox.Events {
		if e.Kind == kind {
			texts = append(texts, e.Text)
		}
	}
	return texts
}
