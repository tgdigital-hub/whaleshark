package inbox

import (
	"bytes"
	"encoding/json"
	"fmt"
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

var rule = rules.Rules{}

// patience is how long another process is given, on a machine that is busy.
const patience = time.Minute

// world is the evening of the fixture as a project on disk, with the fake
// herdr beside it and the real program for every command.
type world struct {
	*testing.T
	p   *scenario.Project
	dir string // the run's folder
}

func evening(t *testing.T, edit func(f *testkit.Fixture)) *world {
	t.Helper()
	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(f)
	}
	w := &world{T: t, p: scenario.Prepare(t, f, nil)}
	_, w.dir = w.p.Record()
	return w
}

// run runs the real program as a caller and returns what it printed on each
// side and its exit code.
func (w *world) run(who string, args ...string) (out, errOut string, exit int) {
	w.Helper()
	cmd := w.p.Command(who, args...)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	cmd.Run()
	return o.String(), e.String(), cmd.ProcessState.ExitCode()
}

// result runs a command with --json, wants it to succeed, and decodes its result.
func (w *world) result(v any, who string, args ...string) {
	w.Helper()
	out, errOut, exit := w.run(who, append(args, "--json")...)
	if exit != 0 || json.Unmarshal([]byte(out), &struct{ Result any }{v}) != nil {
		w.Fatalf("%v: exit %d\n%s%s", args, exit, out, errOut)
	}
}

// sweep is the child a pane starts every five seconds.
func (w *world) sweep() (s contract.Swept) {
	w.Helper()
	w.result(&s, scenario.Orch, "status", "--sweep", "--quiet")
	return s
}

// after moves the clock and sweeps.
func (w *world) after(d time.Duration) contract.Swept {
	w.Helper()
	w.p.Clock(d)
	return w.sweep()
}

func (w *world) change(fn func(s *contract.State, now time.Time) error) {
	w.Helper()
	if err := w.p.Kit.Store.Change(w.p.Root, "r3", func(s *contract.State) error { return fn(s, contract.Now()) }); err != nil {
		w.Fatal(err)
	}
}

func (w *world) state() *contract.State {
	w.Helper()
	s, _ := w.p.Record()
	return s
}

// file is a file of the run's folder, read so that it stands in no writer's
// way; nil while it cannot be read.
func (w *world) file(name string) []byte {
	data, _ := w.p.Kit.Platform.Peek(filepath.Join(w.dir, name))
	return data
}

func (w *world) pane(attempt string) string { return w.state().Attempts[attempt].Place.Pane }

// until waits for something another process is about to do.
func (w *world) until(what string, ok func() bool) {
	w.Helper()
	for end := time.Now().Add(patience); !ok(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			w.Fatalf("waited in vain for %s", what)
		}
	}
}

// waiting starts a wait and returns once it holds the lock and blocks: its
// first sweep, made after its first look at the record, has stamped the run.
func (w *world) waiting(args ...string) (cmd *exec.Cmd, out *bytes.Buffer) {
	w.Helper()
	w.p.Clock(time.Second)
	before := w.file(stampFile)
	cmd, out = w.p.Command(scenario.Orch, append([]string{"wait"}, args...)...), new(bytes.Buffer)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		w.Fatal(err)
	}
	w.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	w.until("the wait to block", func() bool {
		now := w.file(stampFile)
		return now != nil && !bytes.Equal(now, before)
	})
	return cmd, out
}

// ended waits for a command to end by itself and returns its exit code.
func (w *world) ended(cmd *exec.Cmd) int {
	w.Helper()
	late := time.AfterFunc(patience, func() { cmd.Process.Kill() })
	cmd.Wait()
	if !late.Stop() {
		w.Fatal("the command did not end")
	}
	return cmd.ProcessState.ExitCode()
}

func kinds(events []contract.Event, attempt string) (out []string) {
	for _, e := range events {
		if attempt == "" || e.Attempt == attempt {
			out = append(out, e.Kind)
		}
	}
	return out
}

// item is the tool's own item for a cause, whatever its state.
func item(s *contract.State, cause string) *contract.Question {
	for _, q := range s.Questions {
		if q.From == contract.FromTool && q.Cause == cause {
			return q
		}
	}
	return nil
}

// typed counts how often a line was typed into a pane.
func (w *world) typed(pane, text string) (n int) {
	for _, call := range w.p.Herdr.Calls() {
		if slices.Equal(call, []string{"agent", "prompt", pane, text}) {
			n++
		}
	}
	return n
}

// waits puts events into the fixture's inbox, as workers that reported and
// asked would have.
func waits(n int, text string) func(*testkit.Fixture) {
	return func(f *testkit.Fixture) {
		for range n {
			f.State.Counters.Seq++
			f.State.Inbox.Events = append(f.State.Inbox.Events, contract.Event{Seq: f.State.Counters.Seq, At: f.Now,
				Kind: "question", Task: "T2", Attempt: "T2.1", Text: text})
		}
		f.State.Run.Lead.SilentSince = f.Now // the lead agent is known not to listen, so this is no news to a sweep
	}
}

func TestTheRunnersOwnSteps(t *testing.T) { scenario.Run(t, "testdata/wait.scn", nil) }

func TestAWaitKilledMidBatchReplays(t *testing.T) {
	// Fifty long events are more than a pipe holds: the wait has chosen its
	// batch and written the record, and is killed while it hands the batch out.
	w := evening(t, waits(contract.MaxBatch+1, strings.Repeat("which base? ", 400)))
	cmd := w.p.Command(scenario.Orch, "wait", "--json")
	r, pipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cmd.Stdout = pipe
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pipe.Close()
	w.until("the batch to be chosen", func() bool { return w.state().Inbox.Outstanding != nil })
	cmd.Process.Kill()
	cmd.Wait()

	var first, again, next delivery
	w.result(&first, scenario.Orch, "wait")
	w.result(&again, scenario.Orch, "wait", "--for", "done")
	if first.ID != "d10" || !first.Replayed || len(first.Events) != contract.MaxBatch || first.Events[0].Seq != 58 {
		t.Fatalf("the batch was not handed out again: %s replayed %v, %d events", first.ID, first.Replayed, len(first.Events))
	}
	if a, b := fmt.Sprint(first), fmt.Sprint(again); a != b {
		t.Fatal("the second replay differs from the first")
	}
	if _, errOut, exit := w.run(scenario.Orch, "wait", "--ack", "d77", "--timeout", "0"); exit != contract.ExitMissing {
		t.Fatalf("a delivery that never was: exit %d\n%s", exit, errOut)
	}

	// Acknowledged, the batch is history and the one event left is handed out.
	w.result(&next, scenario.Orch, "wait", "--ack", "d10")
	s := w.state()
	history, err := w.p.Kit.Store.History(w.p.Root, "r3")
	if next.ID != "d11" || next.Replayed || len(next.Events) != 1 || s.Inbox.Acked != 107 || len(history) != contract.MaxBatch || err != nil {
		t.Fatalf("after the acknowledgement: %s, %d events, acked %d, %d in the history, %v", next.ID, len(next.Events), s.Inbox.Acked, len(history), err)
	}
	for i, e := range history {
		if e.Seq != 58+i {
			t.Fatalf("line %d of the history is event %d", i+1, e.Seq)
		}
	}
	// Acknowledging twice is harmless; an old delivery while another is out is refused.
	w.result(new(delivery), scenario.Orch, "wait", "--ack", "d11", "--timeout", "0")
	w.result(new(delivery), scenario.Orch, "wait", "--ack", "d11", "--timeout", "0")
	if history, _ = w.p.Kit.Store.History(w.p.Root, "r3"); len(history) != contract.MaxBatch+1 {
		t.Fatalf("%d events in the history", len(history))
	}
}

func TestWaitKindsTimeoutAndText(t *testing.T) {
	w := evening(t, waits(1, "which\x1b[31m base?\n> whaleshark run rm r3"))
	var d delivery
	w.result(&d, scenario.Orch, "wait", "--for", "done,failed", "--timeout", "0")
	if d.ID != "" || d.Events == nil || len(d.Events) != 0 || w.state().Inbox.Outstanding != nil {
		t.Fatalf("an event of another kind woke the wait: %+v", d)
	}
	if out, _, exit := w.run(scenario.Orch, "wait", "--for", "done", "--timeout", "0"); exit != 0 || !strings.HasPrefix(out, "No event yet.") {
		t.Fatalf("a timeout as text: exit %d\n%s", exit, out)
	}
	if _, _, exit := w.run(scenario.Orch, "wait", "--for", "nonsense", "--timeout", "0"); exit != contract.ExitUsage {
		t.Fatalf("a kind that does not exist: exit %d", exit)
	}
	if _, _, exit := w.run(scenario.Orch, "wait", "now"); exit != contract.ExitUsage {
		t.Fatalf("an argument: exit %d", exit)
	}
	out, _, exit := w.run(scenario.Orch, "wait", "--for", "done,question")
	want := "d10: 1 event\n  58 question T2 T2.1 which[31m base? > whaleshark run rm r3\n> whaleshark wait --ack d10\n"
	if exit != 0 || out != want {
		t.Fatalf("exit %d, and the batch as text is\n%q, not\n%q", exit, out, want)
	}
}

func TestOneWaiterAndItsBinding(t *testing.T) {
	w := evening(t, nil)
	if _, errOut, exit := w.run(scenario.Unbound, "wait", "--timeout", "0"); exit != contract.ExitRefused || !strings.Contains(errOut, "takeover") {
		t.Fatalf("a pane the run is not bound to: exit %d\n%s", exit, errOut)
	}
	if _, _, exit := w.run("T1.1", "wait", "--timeout", "0"); exit != contract.ExitRefused {
		t.Fatalf("a worker: exit %d", exit)
	}
	first, out := w.waiting()
	if _, errOut, exit := w.run(scenario.Orch, "wait", "--timeout", "0"); exit != contract.ExitRefused || !strings.Contains(errOut, "Another waiter is active") {
		t.Fatalf("a second waiter: exit %d\n%s", exit, errOut)
	}
	// Another pane takes the run over: the wait that blocks ends by itself.
	old := w.p.Lead
	w.change(func(s *contract.State, now time.Time) error {
		return rule.Takeover(s, contract.Binding{Pane: w.p.Own, Workspace: "w1"}, now)
	})
	if exit := w.ended(first); exit != contract.ExitRefused || !strings.Contains(out.String(), "You have been replaced") {
		t.Fatalf("the replaced wait: exit %d\n%s", exit, out)
	}
	if _, errOut, exit := w.run(scenario.Orch, "wait", "--timeout", "0"); exit != contract.ExitRefused || !strings.Contains(errOut, "takeover") {
		t.Fatalf("the old lead agent's next wait from %s: exit %d\n%s", old, exit, errOut)
	}
	// A killed waiter leaves the lock free for the next.
	w.p.Lead = w.p.Own
	second, _ := w.waiting()
	second.Process.Kill()
	second.Wait()
	if _, errOut, exit := w.run(scenario.Orch, "wait", "--timeout", "0"); exit != 0 {
		t.Fatalf("a wait after a killed one: exit %d\n%s", exit, errOut)
	}
}

func TestAnAnswerSettlesByTheClock(t *testing.T) {
	w := evening(t, nil)
	w.change(func(s *contract.State, now time.Time) error {
		return rule.Answer(s, "n4", contract.AnswerApprove, contract.Origin{Caller: contract.Human, Where: contract.WherePane}, 3*time.Second, now)
	})
	cmd, out := w.waiting()
	record := w.file(stateFile)
	w.p.Clock(2 * time.Second) // no file of the run changes with it
	if exit := w.ended(cmd); exit != 0 || !strings.Contains(out.String(), "answered T8 approve") {
		t.Fatalf("exit %d\n%s", exit, out)
	}
	if bytes.Equal(record, w.file(stateFile)) || w.state().Questions["n4"].State != contract.QuestionUsed {
		t.Fatal("the answer was handed out and not marked used")
	}
}

func TestKeepaliveAndTimeout(t *testing.T) {
	w := evening(t, nil)
	rules.Plug(w.p.Kit)
	Plug(w.p.Kit)
	keepalive = 100 * time.Millisecond
	defer func() { keepalive = 15 * time.Second }()
	var out, errOut bytes.Buffer
	c := &contract.Call{Kit: w.p.Kit, Root: w.p.Root, Run: "r3", Out: &out, Err: &errOut,
		Caller: contract.Caller{Kind: contract.Orchestrator, Pane: w.p.Lead}, Flags: map[string][]string{"timeout": {"1"}}}
	began := time.Now()
	got, err := wait(c)
	if d, ok := got.(delivery); err != nil || !ok || d.ID != "" || time.Since(began) < time.Second {
		t.Fatalf("%+v, %v, after %v", got, err, time.Since(began))
	}
	if n := strings.Count(errOut.String(), "whaleshark wait: still waiting after "); n < 1 || n > 10 {
		t.Fatalf("%d keepalive lines in a second at ten a second:\n%s", n, &errOut)
	}
}

func TestAPaneGoneForFiveSecondsIsNotExited(t *testing.T) {
	w := evening(t, nil)
	pane := w.pane("T1.1")
	w.p.Herdr.Drop(testkit.PushGone, pane)
	w.sweep()
	if a := w.state().Attempts["T1.1"]; a.Seen.GoneSince.IsZero() || a.State != contract.AttemptWorking {
		t.Fatalf("first sighting: %+v", a.Seen)
	}
	w.after(5 * time.Second)
	if a := w.state().Attempts["T1.1"]; a.State != contract.AttemptWorking || a.Seen.Liveness == contract.Gone {
		t.Fatalf("gone for 5 s: %s, %+v", a.State, a.Seen)
	}
	w.p.Herdr.Handle(testkit.FakeCall{Args: []string{"pane", "report-agent", pane, "--agent", "claude", "--state", contract.StatusWorking}})
	w.after(5 * time.Second)
	if a := w.state().Attempts["T1.1"]; a.State != contract.AttemptWorking || !a.Seen.GoneSince.IsZero() || a.Seen.Liveness != contract.Live {
		t.Fatalf("seen again: %s, %+v", a.State, a.Seen)
	}
	if got := kinds(w.state().Inbox.Events, ""); got != nil {
		t.Fatalf("events %v", got)
	}

	// Gone at 0 s and still gone at 30 s is an exit, whether the agent left
	// its pane or the pane is not in the picture at all.
	w.p.Herdr.Drop(testkit.PushGone, pane)
	w.p.Herdr.Drop(testkit.PushClosed, w.pane("T3.1"))
	w.after(5 * time.Second)
	w.after(29 * time.Second)
	if s := w.state(); s.Attempts["T1.1"].State != contract.AttemptWorking || s.Attempts["T3.1"].State != contract.AttemptWorking {
		t.Fatal("exited before the thirty seconds were over")
	}
	w.after(5 * time.Second)
	s := w.state()
	for _, id := range []string{"T1", "T3"} {
		a, task := s.Attempts[id+".1"], s.Tasks[id]
		if a.State != contract.AttemptExited || a.Seen.Liveness != contract.Gone || task.Status != contract.TaskReady || task.Failures != 1 {
			t.Fatalf("%s gone at 0 s and at 30 s: %s, %s, task %s with %d failures", id, a.State, a.Seen.Liveness, task.Status, task.Failures)
		}
	}
	if got := kinds(s.Inbox.Events, ""); !slices.Equal(got, []string{"exited", "exited"}) {
		t.Fatalf("events %v", got)
	}
}

func TestTheTerminalsDownChangesNothing(t *testing.T) {
	w := evening(t, nil)
	w.p.Herdr.Drop(testkit.PushGone, w.pane("T1.1"))
	w.sweep()
	record := w.file(stateFile)
	w.p.Herdr.Close()
	for _, d := range []time.Duration{5 * time.Second, time.Minute, 10 * time.Minute} {
		w.p.Clock(d)
		if out, errOut, exit := w.run(scenario.Orch, "status", "--sweep", "--quiet"); exit != contract.ExitEnv || !strings.Contains(errOut, "changed nothing") {
			t.Fatalf("a sweep without the terminals: exit %d\n%s%s", exit, out, errOut)
		}
	}
	if out, errOut, exit := w.run(scenario.Orch, "status"); exit != 0 || !strings.Contains(out, "login page") {
		t.Fatalf("status without the terminals: exit %d\n%s%s", exit, out, errOut)
	}
	if !bytes.Equal(record, w.file(stateFile)) {
		t.Fatal("the record changed while the terminals could not be reached")
	}
}

func TestTwentyWorkingAgentsCauseNoWrite(t *testing.T) {
	w := evening(t, func(f *testkit.Fixture) {
		task, attempt, pane := *f.State.Tasks["T1"], *f.State.Attempts["T1.1"], f.Terms.Panes[1]
		lead := f.Terms.Panes[0]
		f.State.Tasks, f.State.Attempts, f.State.Questions = map[string]*contract.Task{}, map[string]*contract.Attempt{}, map[string]*contract.Question{}
		f.State.Run.Findings, f.Terms.Panes, f.Ctx = nil, []contract.Pane{lead}, nil
		for i := range 20 {
			tk, a, p := task, attempt, pane
			tk.ID, tk.Name, tk.After = fmt.Sprint("W", i), fmt.Sprint("worker ", i), nil
			a.ID, a.Task, a.StartedAt, a.StateSince, a.Progress = tk.ID+".1", tk.ID, f.Now, f.Now, nil
			tk.Attempts = []string{a.ID}
			a.Agent.Name = fmt.Sprintf("h3fa1-r3-w%d-1", i)
			p.ID, p.Tab, p.Terminal, p.Name = fmt.Sprint("w1:p", 30+i), fmt.Sprint("w1:t", 30+i), fmt.Sprint("term-", 30+i), a.Agent.Name
			a.Place.Pane, a.Place.Tab, a.Place.Terminal = p.ID, p.Tab, p.Terminal
			f.State.Tasks[tk.ID], f.State.Attempts[a.ID] = &tk, &a
			f.Terms.Panes = append(f.Terms.Panes, p)
		}
	})
	record, _ := os.Stat(filepath.Join(w.dir, stateFile))
	bytesBefore, snapshots := w.file(stateFile), 0
	// The sweeps of ten minutes, in this process for their number; the last
	// one is the real program's.
	rules.Plug(w.p.Kit)
	Plug(w.p.Kit)
	for range 10*60/5 - 1 {
		w.p.Clock(5 * time.Second)
		if s, err := w.p.Kit.Sweeper.Sweep(w.p.Root, "r3", contract.Now()); err != nil || !s.Ran || s.Changed {
			t.Fatalf("a sweep of twenty working agents: %+v, %v", s, err)
		}
	}
	if s := w.after(5 * time.Second); !s.Ran || s.Changed {
		t.Fatalf("a sweep of twenty working agents: %+v", s)
	}
	for _, call := range w.p.Herdr.Calls() {
		if slices.Equal(call, []string{"api", "snapshot"}) {
			snapshots++
		}
	}
	after, _ := os.Stat(filepath.Join(w.dir, stateFile))
	if !after.ModTime().Equal(record.ModTime()) || !bytes.Equal(bytesBefore, w.file(stateFile)) || snapshots != 120 {
		t.Fatalf("ten minutes of twenty working agents wrote the record (%v, then %v) or did not look each time (%d pictures)",
			record.ModTime(), after.ModTime(), snapshots)
	}
}

func TestTheLeadAgentUnread(t *testing.T) {
	w := evening(t, nil)
	w.waiting() // a lead agent that follows the rules: its wait runs in the background
	w.p.Herdr.Push(testkit.PushFocused, w.pane("T1.1"))
	w.p.Herdr.Push(contract.StatusDone, w.p.Lead)
	w.after(5 * time.Second)
	if s := w.state(); s.Run.Lead.DoneSince.IsZero() || item(s, contract.CauseLeadUnread) != nil {
		t.Fatalf("done and unfocused, first seen: %+v", s.Run.Lead)
	}
	if w.after(59 * time.Second); item(w.state(), contract.CauseLeadUnread) != nil {
		t.Fatal("the item came before the minute was over")
	}
	w.after(5 * time.Second)
	if q := item(w.state(), contract.CauseLeadUnread); q == nil || q.State != contract.QuestionOpen || q.For != contract.ForHuman || !strings.Contains(q.Text, "you have not read") {
		t.Fatalf("done and unfocused for a minute: %+v", q)
	}
	if item(w.state(), contract.CauseLeadSilent) != nil {
		t.Fatal("a lead agent with a wait running was called not listening")
	}
	// The person looks at the tab, which is what turns done into idle.
	w.p.Herdr.Push(testkit.PushFocused, w.p.Lead)
	w.p.Herdr.Push(contract.StatusIdle, w.p.Lead)
	w.after(5 * time.Second)
	if s := w.state(); item(s, contract.CauseLeadUnread).State != contract.QuestionClosed || !s.Run.Lead.DoneSince.IsZero() {
		t.Fatalf("after the focus: %+v", item(s, contract.CauseLeadUnread))
	}
}

func TestTheLeadAgentNotListening(t *testing.T) {
	w := evening(t, func(f *testkit.Fixture) {
		waits(1, "which base?")(f)
		f.State.Run.Lead.SilentSince = time.Time{}
	})
	w.sweep()
	if w.after(59 * time.Second); item(w.state(), contract.CauseLeadSilent) != nil {
		t.Fatal("the item came before the minute was over")
	}
	w.after(5 * time.Second)
	if q := item(w.state(), contract.CauseLeadSilent); q == nil || q.State != contract.QuestionOpen {
		t.Fatalf("events unread, no wait, the lead agent idle for a minute: %+v", q)
	}
	// It clears when a wait starts: the wait's own first sweep sees its lock held.
	w.waiting("--for", "done")
	if q := item(w.state(), contract.CauseLeadSilent); q.State != contract.QuestionClosed {
		t.Fatalf("with a wait running: %+v", q)
	}
}

func TestAtAPromptQuietAndThePointerToTheLead(t *testing.T) {
	w := evening(t, nil)
	pane, pointer := w.pane("T1.1"), "whaleshark: 1 events waiting. Run: whaleshark wait"
	w.p.Herdr.Drop(contract.StatusBlocked, pane)
	if s := w.sweep(); !s.Ran || !s.Changed {
		t.Fatalf("%+v", s)
	}
	s := w.state()
	if q := item(s, contract.CausePrompt); q == nil || q.State != contract.QuestionOpen || q.Attempt != "T1.1" || q.Form != contract.FormTodo ||
		!slices.Equal(kinds(s.Inbox.Events, "T1.1"), []string{"blocked"}) || !s.Run.Terms.SeenAt.Equal(s.Attempts["T1.1"].Seen.At) {
		t.Fatalf("at a prompt: %+v, events %v", q, kinds(s.Inbox.Events, ""))
	}
	// Nobody waits and the lead agent rests: it is told in one line, once.
	if w.typed(w.p.Lead, pointer) != 1 || w.typed(pane, pointer) != 0 {
		t.Fatalf("the pointer to the lead agent: %v", w.p.Herdr.Calls())
	}
	w.p.Herdr.Drop(contract.StatusIdle, w.p.Lead)
	w.p.Herdr.Drop(contract.StatusWorking, pane)
	if w.after(5 * time.Second); item(w.state(), contract.CausePrompt).State != contract.QuestionClosed {
		t.Fatal("the item stayed when the prompt was gone")
	}

	// Idle with no report for 120 s is quiet, once, and nothing is stopped.
	w.p.Herdr.Drop(contract.StatusIdle, pane)
	w.after(5 * time.Second)
	w.after(119 * time.Second)
	if a := w.state().Attempts["T1.1"]; a.Seen.QuietFlagged {
		t.Fatal("quiet before the two minutes were over")
	}
	w.after(5 * time.Second)
	w.after(5 * time.Second)
	s = w.state()
	if a := s.Attempts["T1.1"]; !slices.Equal(kinds(s.Inbox.Events, "T1.1"), []string{"blocked", "quiet"}) || a.State != contract.AttemptWorking || a.Seen.Liveness != contract.Unverifiable {
		t.Fatalf("idle for two minutes: %v, %s, %s", kinds(s.Inbox.Events, "T1.1"), a.State, a.Seen.Liveness)
	}
	if n := w.typed(w.p.Lead, "whaleshark: 2 events waiting. Run: whaleshark wait"); n != 1 {
		t.Fatalf("the second pointer was typed %d times", n)
	}
}

func TestStillRunningAfterAPause(t *testing.T) {
	w := evening(t, nil)
	w.p.Herdr.Drop(contract.StatusIdle, w.pane("T3.1"))
	w.change(func(s *contract.State, now time.Time) error { return rule.Pause(s, "human", now) })
	w.sweep()
	w.after(119 * time.Second)
	if a := w.state().Attempts["T1.1"]; a.Seen.StillFlagged {
		t.Fatal("still running before the two minutes were over")
	}
	w.after(5 * time.Second)
	w.after(10 * time.Minute)
	s := w.state()
	if got := kinds(s.Inbox.Events, "T1.1"); !slices.Equal(got, []string{"still_running"}) {
		t.Fatalf("working two minutes into a pause: %v", got)
	}
	// The one that stopped as told is neither still running nor quiet.
	if got := kinds(s.Inbox.Events, "T3.1"); got != nil {
		t.Fatalf("idle under a pause: %v", got)
	}
	for _, call := range w.p.Herdr.Calls() {
		if call[0] == "agent" {
			t.Fatalf("something was typed while the run was paused: %v", call)
		}
	}
}

func TestAStartNobodyIsBringingUp(t *testing.T) {
	w := evening(t, func(f *testkit.Fixture) {
		a := f.State.Attempts["T9.1"]
		a.State, a.StateSince = contract.AttemptStarting, f.Now
	})
	lock := filepath.Join(w.dir, "attempts", "T9.1", "start.lock")
	if w.sweep(); w.state().Attempts["T9.1"].State != contract.AttemptStarting {
		t.Fatal("a start was given no time to take its lock")
	}
	unlock, ok, err := w.p.Kit.Platform.TryLock(lock)
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	if w.after(10 * time.Minute); w.state().Attempts["T9.1"].State != contract.AttemptStarting {
		t.Fatal("a start whose lock is held was touched")
	}
	unlock()
	w.after(5 * time.Second)
	s := w.state()
	if a := s.Attempts["T9.1"]; a.State != contract.AttemptStartFailed || !slices.Equal(kinds(s.Inbox.Events, "T9.1"), []string{"start_failed"}) {
		t.Fatalf("a start whose lock is free: %s, %v", a.State, kinds(s.Inbox.Events, ""))
	}
}

func TestTheDeferredPointerToUnreadMail(t *testing.T) {
	w := evening(t, nil)
	pane, pointer := w.pane("T1.1"), string(contract.PointMail)
	tell := func(s *contract.State, now time.Time) error {
		_, err := rule.Tell(s, "T1", "use the new colours", "", now)
		return err
	}
	w.change(tell)
	if w.sweep(); w.typed(pane, pointer) != 0 {
		t.Fatal("the pointer was typed at an agent that is working")
	}
	// Idle, but paused: never. Resumed: once, and the record says so.
	w.p.Herdr.Drop(contract.StatusIdle, pane)
	w.change(func(s *contract.State, now time.Time) error { return rule.Pause(s, "human", now) })
	if w.after(5 * time.Second); w.typed(pane, pointer) != 0 {
		t.Fatal("the pointer was typed while the run was paused")
	}
	w.change(func(s *contract.State, now time.Time) error { return rule.Resume(s, now) })
	if s := w.after(5 * time.Second); w.typed(pane, pointer) != 1 || !s.Changed || w.state().Attempts["T1.1"].Mail[0].NudgedAt.IsZero() {
		t.Fatalf("the deferred pointer: typed %d times, %+v", w.typed(pane, pointer), s)
	}
	w.p.Herdr.Drop(contract.StatusIdle, pane)
	if w.after(5 * time.Second); w.typed(pane, pointer) != 1 {
		t.Fatal("the pointer was typed twice for one message")
	}
	// An agent at a prompt is never typed at; the pointer waits for it.
	w.change(tell)
	w.p.Herdr.Drop(contract.StatusBlocked, pane)
	w.after(5 * time.Second)
	w.p.Herdr.Drop(contract.StatusIdle, pane)
	if w.typed(pane, pointer) != 1 || w.after(5*time.Second).Changed == false || w.typed(pane, pointer) != 2 {
		t.Fatalf("after the prompt the pointer was typed %d times", w.typed(pane, pointer))
	}
}

func TestTheStampKeepsSweepsApart(t *testing.T) {
	w := evening(t, nil)
	first := w.sweep()
	w.p.Herdr.Drop(testkit.PushGone, w.pane("T1.1"))
	w.p.Clock(4 * time.Second)
	record, calls := w.file(stateFile), len(w.p.Herdr.Calls())
	if second := w.sweep(); !second.Ran || second.Changed || !second.At.Equal(first.At) || len(w.p.Herdr.Calls()) != calls || !bytes.Equal(record, w.file(stateFile)) {
		t.Fatalf("a second sweep within five seconds: %+v after %+v", second, first)
	}
	if third := w.after(time.Second); !third.Changed || !third.At.Equal(first.At.Add(5*time.Second)) || len(w.p.Herdr.Calls()) != calls+1 {
		t.Fatalf("a sweep five seconds later: %+v", third)
	}
	// It is printed as it is, and a closed run is left alone.
	out, _, _ := w.run(scenario.Orch, "status", "--sweep", "--quiet", "--json")
	var keys struct{ Result map[string]any }
	if json.Unmarshal([]byte(out), &keys); len(keys.Result) != 3 || keys.Result["ran"] != true || keys.Result["changed"] != false || keys.Result["at"] == nil {
		t.Fatalf("the sweep as JSON: %s", out)
	}
	w.change(func(s *contract.State, now time.Time) error { s.Run.ClosedAt = now; return nil })
	record = w.file(stateFile)
	w.p.Herdr.Drop(testkit.PushGone, w.pane("T3.1"))
	if s := w.after(time.Minute); s.Changed || !bytes.Equal(record, w.file(stateFile)) {
		t.Fatalf("a sweep of a closed run: %+v", s)
	}
}
