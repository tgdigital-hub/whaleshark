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

// between stands between a command and the fake terminals: it refuses one
// kind of call, such as "agent start", with a code, or holds one kind until
// the test lets it go, and passes everything else on.
type between struct {
	p                  *scenario.Project
	addr               string
	hold, refuse, with string
	held               chan struct{} // one for every call that is being held
	let                chan bool     // true passes a held call on, false refuses it
}

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
	var call testkit.FakeCall
	if json.NewDecoder(conn).Decode(&call) != nil {
		return
	}
	name := strings.Join(call.Args[:min(2, len(call.Args))], " ")
	code := ""
	if name == b.refuse {
		code = b.with
	}
	if name == b.hold {
		b.held <- struct{}{}
		if !<-b.let {
			code = "pane_not_found"
		}
	}
	answer := testkit.FakeAnswer{Stderr: fmt.Sprintf(`{"id":"t","error":{"code":%q,"message":"said by the test"}}`, code) + "\n", Exit: 1}
	if code == "" {
		answer = b.p.Herdr.Handle(call)
	}
	json.NewEncoder(conn).Encode(answer)
}

func (b *between) command(who string, args ...string) *exec.Cmd {
	cmd := b.p.Command(who, args...)
	cmd.Env = append(cmd.Env, testkit.EnvFake+"="+b.addr)
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

// calls is every call the fake terminals got that begins with these two words.
func calls(p *scenario.Project, first, second string) (out [][]string) {
	for _, c := range p.Herdr.Calls() {
		if len(c) > 1 && c[0] == first && c[1] == second {
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
	snap, err := p.Herdr.Snapshot(t.Context())
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

	var tab []string
	for _, c := range calls(p, "tab", "create") {
		if slices.Contains(c, "ready 1") {
			tab = c
		}
	}
	var names []string
	for i, w := range tab {
		if w == "--env" {
			name, _, _ := strings.Cut(tab[i+1], "=")
			names = append(names, name)
		}
	}
	want := []string{contract.EnvRoot, contract.EnvRun, contract.EnvTask, contract.EnvAttempt, contract.EnvDepth, contract.EnvBin}
	if !slices.Equal(names, want) || !slices.Contains(tab, "--no-focus") {
		t.Errorf("the tab was made with %v, expected exactly the variables %v", tab, want)
	}
	if !slices.ContainsFunc(calls(p, "agent", "start"), func(c []string) bool {
		return c[2] == a.Agent.Name && slices.Contains(c, a.Place.Pane) && slices.Contains(c, "180000")
	}) {
		t.Errorf("no agent start for %s in %s: %v", a.Agent.Name, a.Place.Pane, calls(p, "agent", "start"))
	}
	if !slices.ContainsFunc(calls(p, "agent", "prompt"), func(c []string) bool {
		return c[2] == a.Agent.Name && c[3] == "Read and follow "+filepath.Join(dir, "attempts", "R1.1", "prompt.md")
	}) {
		t.Errorf("no first prompt for %s: %v", a.Agent.Name, calls(p, "agent", "prompt"))
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
		{"tab create", "internal", "start_failed", 1, false, false},
		{"agent start", "internal", "start_failed", 1, true, false},
		{"agent start", "agent_not_ready", "at_prompt", 1, true, true},
		{"agent prompt", "agent_prompt_stalled", "prompt_stalled", 1, true, false},
		{"agent prompt", "agent_blocked", "at_prompt", 1, true, true},
		{"agent start", "server_not_running", "unreachable", 3, true, false},
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
			if n := len(calls(p, "agent", "prompt")); n > 0 {
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
	b := stand(t, p, "agent start", "", "")
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
	if tabOpen(t, p, a.Place.Tab) || len(calls(p, "agent", "prompt")) > 0 {
		t.Error("the tab is still open, or a prompt was typed after the stop")
	}
}

func TestStopKeepingTheTabDuringAStart(t *testing.T) {
	p := evening(t, 1, nil)
	b := stand(t, p, "agent start", "", "")
	cmd := b.command(scenario.Orch, "start", "R1")
	b.begin(t, 1, cmd)
	tab := attempt(t, p, "R1.1", contract.AttemptStarting).Place.Tab
	must(t, p.Command(scenario.Orch, "stop", "R1", "--keep-tab"), 0, "its tab: kept")
	b.let <- true
	cmd.Wait()
	if exit := cmd.ProcessState.ExitCode(); exit != 5 || tabOpen(t, p, tab) || len(calls(p, "agent", "prompt")) > 0 {
		t.Errorf("exit %d: the start should give up with 5, close the tab it opened and type nothing", exit)
	}
}

func TestAStartKilledHalfway(t *testing.T) {
	p := evening(t, 1, nil)
	b := stand(t, p, "agent start", "", "")
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
	if !slices.ContainsFunc(calls(p, "agent", "start"), func(c []string) bool {
		// After the "--" come the settings of the hooks and then the model.
		return c[2] == retried.Agent.Name && slices.Contains(c, "--") && slices.Equal(c[len(c)-2:], []string{"--model", "fast-1"})
	}) {
		t.Errorf("the model is not on the agent's own line: %v", calls(p, "agent", "start"))
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
	b := stand(t, p, "agent start", "", "")
	cmd := b.command(scenario.Orch, "start", "--ready", "--json")
	out := b.begin(t, 20, cmd)
	before := len(p.Herdr.Calls())
	must(t, p.Command(scenario.Unbound, "pause"), 5, "the person's")
	must(t, p.Command(scenario.Orch, "pause"), 5)
	paused := must(t, p.Command(scenario.Human, "pause", "--everywhere"), 0, "Paused. 11 agents stop at their next step", "still running: checkout form")
	if strings.Contains(paused, "ready") {
		t.Errorf("an attempt that is only starting has no agent to be still running:\n%s", paused)
	}
	if n := len(p.Herdr.Calls()); n != before {
		t.Errorf("pause reached for the terminals: %v", p.Herdr.Calls()[before:])
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
	for _, c := range p.Herdr.Calls()[before:] {
		if c[0] != "agent" || c[1] != "start" {
			t.Errorf("after Stop all the terminals were asked: %v", c)
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
	p.Herdr.Push(contract.StatusBlocked, "w1:p4") // the pane of T3, search box
	before := len(p.Herdr.Calls())
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
	for _, c := range p.Herdr.Calls()[before:] {
		if c[0] == "agent" && c[1] == "prompt" {
			told[c[2]] = c[3]
		} else if c[0] != "api" {
			t.Errorf("resume asked the terminals: %v", c)
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
	made := calls(p, "tab", "create")
	typed := calls(p, "pane", "run")
	if len(made) != 1 || !slices.Contains(made[0], "start ready 1") || len(typed) != 1 {
		t.Fatalf("the tab %v and the line typed into it %v", made, typed)
	}
	// The line is written for the system's own shell, which on Windows
	// puts every word between single quotes.
	line := strings.ReplaceAll(typed[0][3], "'", "")
	if !strings.Contains(line, " start R1 --human --root ") || !strings.HasSuffix(line, " --run r3") {
		t.Fatalf("the line typed into the tab: %s", typed[0][3])
	}

	// The same line, run in such a tab, is still the button's and is not
	// moved again; it closes the tab when it ends well.
	pane, err := p.Herdr.TabCreate(p.Root, "start ready 2", nil)
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
}

func TestAgentName(t *testing.T) {
	a, b := agentName("/one/project", "r3", "T3.1"), agentName("/another/project", "r3", "T3.1")
	if a == b || !strings.HasSuffix(a, "-r3-t3-1") || len(a) != len("h0000-r3-t3-1") || strings.ToLower(a) != a {
		t.Errorf("%s and %s", a, b)
	}
}
