package inbox

import (
	"bytes"
	"context"
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
	"github.com/tgdigital-hub/whaleshark/test/fakeengine"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func TestMain(m *testing.M) { scenario.Main(m) }

var rule = rules.Rules{}

// patience is how long another process is given, on a machine that is busy.
const patience = time.Minute

// world is the evening of the fixture as a project on disk, with the engine's
// double beside it and the real program for every command.
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
	for _, c := range w.p.Double.Calls() {
		if c.Pane == pane && fakeengine.Typed(c) == text {
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

func TestARestartInTheMiddleOfARun(t *testing.T) { scenario.Run(t, "testdata/8g-restart.scn", nil) }

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
	record := w.file(contract.StateFile)
	w.p.Clock(2 * time.Second) // no file of the run changes with it
	if exit := w.ended(cmd); exit != 0 || !strings.Contains(out.String(), "answered T8 approve") {
		t.Fatalf("exit %d\n%s", exit, out)
	}
	if bytes.Equal(record, w.file(contract.StateFile)) || w.state().Questions["n4"].State != contract.QuestionUsed {
		t.Fatal("the answer was handed out and not marked used")
	}
}

// said is where a wait writes that it is still there: each line is handed
// to the test as it is written.
type said chan string

func (c said) Write(b []byte) (int, error) { c <- string(b); return len(b), nil }

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
	took := time.Since(began)
	if d, ok := got.(delivery); err != nil || !ok || d.ID != "" || took < time.Second {
		t.Fatalf("%+v, %v, after %v", got, err, took)
	}
	// How many lines a wait of one second says is not asked: its first look
	// and its first sweep may take all of that second on a busy machine, and
	// then its time is out before it has blocked once.
	if n := strings.Count(errOut.String(), "whaleshark wait: still waiting after "); n > int(took/keepalive) {
		t.Fatalf("%d keepalive lines in %v at ten a second:\n%s", n, took, &errOut)
	}

	// A wait that blocks says so again and again, until it ends.
	heard, ended := make(said, 1), make(chan error, 1)
	c.Err, c.Flags = heard, nil
	go func() {
		_, err := wait(c)
		ended <- err
	}()
	late := time.After(patience)
	for n := 0; n < 3; {
		select {
		case line := <-heard:
			if n++; !strings.HasPrefix(line, "whaleshark wait: still waiting after ") {
				t.Fatalf("the wait said %q", line)
			}
		case err := <-ended:
			t.Fatalf("the wait ended after %d keepalive lines: %v", n, err)
		case <-late:
			t.Fatalf("%d keepalive lines in a minute at ten a second", n)
		}
	}
	w.change(func(s *contract.State, now time.Time) error {
		return rule.Takeover(s, contract.Binding{Pane: w.p.Own, Workspace: "w1"}, now)
	})
	for {
		select {
		case <-heard:
		case err := <-ended:
			if r, ok := err.(*contract.Refusal); !ok || r.Code != "replaced" {
				t.Fatalf("the replaced wait: %v", err)
			}
			return
		case <-late:
			t.Fatal("the replaced wait did not end")
		}
	}
}

func TestAPaneGoneForFiveSecondsIsNotExited(t *testing.T) {
	w := evening(t, nil)
	pane := w.pane("T1.1")
	w.p.Double.Drop(testkit.PushGone, pane)
	w.sweep()
	if a := w.state().Attempts["T1.1"]; a.Seen.GoneSince.IsZero() || a.State != contract.AttemptWorking {
		t.Fatalf("first sighting: %+v", a.Seen)
	}
	w.after(5 * time.Second)
	if a := w.state().Attempts["T1.1"]; a.State != contract.AttemptWorking || a.Seen.Liveness == contract.Gone {
		t.Fatalf("gone for 5 s: %s, %+v", a.State, a.Seen)
	}
	w.p.Double.Hook("UserPromptSubmit", pane, "", "") // an agent is in the pane again, and at work
	w.after(5 * time.Second)
	if a := w.state().Attempts["T1.1"]; a.State != contract.AttemptWorking || !a.Seen.GoneSince.IsZero() || a.Seen.Liveness != contract.Live {
		t.Fatalf("seen again: %s, %+v", a.State, a.Seen)
	}
	if got := kinds(w.state().Inbox.Events, ""); got != nil {
		t.Fatalf("events %v", got)
	}

	// Gone at 0 s and still gone at 30 s is an exit, whether the agent left
	// its pane or the pane is not in the picture at all.
	w.p.Double.Drop(testkit.PushGone, pane)
	w.p.Double.Drop(testkit.PushClosed, w.pane("T3.1"))
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
	w.p.Double.Drop(testkit.PushGone, w.pane("T1.1"))
	w.sweep()
	record := w.file(contract.StateFile)
	w.p.Double.Close()
	for _, d := range []time.Duration{5 * time.Second, time.Minute, 10 * time.Minute} {
		w.p.Clock(d)
		if out, errOut, exit := w.run(scenario.Orch, "status", "--sweep", "--quiet"); exit != contract.ExitEnv || !strings.Contains(errOut, "changed nothing") {
			t.Fatalf("a sweep without the terminals: exit %d\n%s%s", exit, out, errOut)
		}
	}
	if out, errOut, exit := w.run(scenario.Orch, "status"); exit != 0 || !strings.Contains(out, "login page") {
		t.Fatalf("status without the terminals: exit %d\n%s%s", exit, out, errOut)
	}
	if !bytes.Equal(record, w.file(contract.StateFile)) {
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
	// The first sweep of a run writes down which start of the keeper it saw.
	rules.Plug(w.p.Kit)
	Plug(w.p.Kit)
	if s, _, err := w.p.Kit.Sweeper.Sweep(w.p.Root, "r3", contract.Now()); err != nil || !s.Changed {
		t.Fatalf("the first sweep: %+v, %v", s, err)
	}
	record, _ := os.Stat(filepath.Join(w.dir, contract.StateFile))
	bytesBefore, snapshots := w.file(contract.StateFile), -1
	// The sweeps of ten minutes, in this process for their number; the last
	// one is the real program's.
	for range 10*60/5 - 1 {
		w.p.Clock(5 * time.Second)
		if s, _, err := w.p.Kit.Sweeper.Sweep(w.p.Root, "r3", contract.Now()); err != nil || !s.Ran || s.Changed {
			t.Fatalf("a sweep of twenty working agents: %+v, %v", s, err)
		}
	}
	if s := w.after(5 * time.Second); !s.Ran || s.Changed {
		t.Fatalf("a sweep of twenty working agents: %+v", s)
	}
	for _, c := range w.p.Double.Calls() {
		if c.Op == contract.OpSnapshot {
			snapshots++
		}
	}
	after, _ := os.Stat(filepath.Join(w.dir, contract.StateFile))
	if !after.ModTime().Equal(record.ModTime()) || !bytes.Equal(bytesBefore, w.file(contract.StateFile)) || snapshots != 120 {
		t.Fatalf("ten minutes of twenty working agents wrote the record (%v, then %v) or did not look each time (%d pictures)",
			record.ModTime(), after.ModTime(), snapshots)
	}
}

func TestTheLeadAgentUnread(t *testing.T) {
	w := evening(t, nil)
	w.waiting() // a lead agent that follows the rules: its wait runs in the background
	w.p.Double.Push(testkit.PushFocused, w.pane("T1.1"))
	w.p.Double.Push(contract.StatusWorking, w.p.Lead)
	w.after(5 * time.Second)
	w.p.Double.Push(contract.StatusIdle, w.p.Lead)
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
	// The person looks at the tab.
	w.p.Double.Push(testkit.PushFocused, w.p.Lead)
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
	w.p.Double.Drop(contract.StatusBlocked, pane)
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
		t.Fatalf("the pointer to the lead agent: %v", w.p.Double.Calls())
	}
	w.p.Double.Drop(contract.StatusIdle, w.p.Lead)
	w.p.Double.Drop(contract.StatusWorking, pane)
	if w.after(5 * time.Second); item(w.state(), contract.CausePrompt).State != contract.QuestionClosed {
		t.Fatal("the item stayed when the prompt was gone")
	}

	// Idle with no report for 120 s is quiet, once, and nothing is stopped.
	w.p.Double.Drop(contract.StatusIdle, pane)
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
	w.p.Double.Drop(contract.StatusIdle, w.pane("T3.1"))
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
	for _, c := range w.p.Double.Calls() {
		if fakeengine.Typed(c) != "" {
			t.Fatalf("something was typed while the run was paused: %+v", c)
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
	w.p.Double.Drop(contract.StatusIdle, pane)
	w.change(func(s *contract.State, now time.Time) error { return rule.Pause(s, "human", now) })
	if w.after(5 * time.Second); w.typed(pane, pointer) != 0 {
		t.Fatal("the pointer was typed while the run was paused")
	}
	w.change(func(s *contract.State, now time.Time) error { return rule.Resume(s, now) })
	if s := w.after(5 * time.Second); w.typed(pane, pointer) != 1 || !s.Changed || w.state().Attempts["T1.1"].Mail[0].NudgedAt.IsZero() {
		t.Fatalf("the deferred pointer: typed %d times, %+v", w.typed(pane, pointer), s)
	}
	w.p.Double.Drop(contract.StatusIdle, pane)
	if w.after(5 * time.Second); w.typed(pane, pointer) != 1 {
		t.Fatal("the pointer was typed twice for one message")
	}
	// An agent at a prompt is never typed at; the pointer waits for it.
	w.change(tell)
	w.p.Double.Drop(contract.StatusBlocked, pane)
	w.after(5 * time.Second)
	w.p.Double.Drop(contract.StatusIdle, pane)
	if w.typed(pane, pointer) != 1 || w.after(5*time.Second).Changed == false || w.typed(pane, pointer) != 2 {
		t.Fatalf("after the prompt the pointer was typed %d times", w.typed(pane, pointer))
	}
}

func TestTheStampKeepsSweepsApart(t *testing.T) {
	w := evening(t, nil)
	first := w.sweep()
	w.p.Double.Drop(testkit.PushGone, w.pane("T1.1"))
	w.p.Clock(4 * time.Second)
	record, calls := w.file(contract.StateFile), len(w.p.Double.Calls())
	if second := w.sweep(); !second.Ran || second.Changed || !second.At.Equal(first.At) || len(w.p.Double.Calls()) != calls || !bytes.Equal(record, w.file(contract.StateFile)) {
		t.Fatalf("a second sweep within five seconds: %+v after %+v", second, first)
	}
	if third := w.after(time.Second); !third.Changed || !third.At.Equal(first.At.Add(5*time.Second)) || len(w.p.Double.Calls()) != calls+1 {
		t.Fatalf("a sweep five seconds later: %+v", third)
	}
	// It is printed as it is, and a closed run is left alone.
	out, _, _ := w.run(scenario.Orch, "status", "--sweep", "--quiet", "--json")
	var keys struct{ Result map[string]any }
	if json.Unmarshal([]byte(out), &keys); len(keys.Result) != 3 || keys.Result["ran"] != true || keys.Result["changed"] != false || keys.Result["at"] == nil {
		t.Fatalf("the sweep as JSON: %s", out)
	}
	w.change(func(s *contract.State, now time.Time) error { s.Run.ClosedAt = now; return nil })
	record = w.file(contract.StateFile)
	w.p.Double.Drop(testkit.PushGone, w.pane("T3.1"))
	if s := w.after(time.Minute); s.Changed || !bytes.Equal(record, w.file(contract.StateFile)) {
		t.Fatalf("a sweep of a closed run: %+v", s)
	}
}

// picture is the terminals' picture now.
func (w *world) picture() *contract.Snapshot {
	w.Helper()
	snap, err := w.p.Kit.Terms.Snapshot(context.Background())
	if err != nil {
		w.Fatal(err)
	}
	return snap
}

func TestTheKeeperStartedAgain(t *testing.T) {
	w := evening(t, func(f *testkit.Fixture) {
		// One agent has no conversation to take up again: its pane comes back as a shell.
		f.State.Attempts["T3.1"].Agent.Session = ""
		for i := range f.Terms.Panes {
			if f.Terms.Panes[i].ID == f.State.Attempts["T3.1"].Place.Pane {
				f.Terms.Panes[i].Session = ""
			}
		}
	})
	w.sweep()
	before := w.state()
	if before.Run.Terms.Instance == "" {
		t.Fatal("the first sweep did not write down the keeper's instance")
	}
	w.p.Double.Restart()
	// One pane did not come back at all.
	w.p.Double.Drop(testkit.PushClosed, before.Attempts["T5.1"].Place.Pane)
	w.after(5 * time.Second)
	s := w.state()
	back, lost := s.Attempts["T1.1"], s.Attempts["T3.1"]
	if s.Run.Terms.Instance == before.Run.Terms.Instance || !s.Run.Terms.SeenAt.Equal(contract.Now()) {
		t.Fatalf("the restart was not seen: %+v", s.Run.Terms)
	}
	if back.Place.Terminal == before.Attempts["T1.1"].Place.Terminal || back.Place.Pane != before.Attempts["T1.1"].Place.Pane || back.State != contract.AttemptWorking {
		t.Fatalf("an agent that came back: %+v, %s", back.Place, back.State)
	}
	for id, a := range s.Attempts {
		if a.State.Live() && (a.Seen.Liveness != contract.Unverifiable || !a.Seen.GoneSince.IsZero()) {
			t.Fatalf("%s is believed %s, gone since %v, in the first sweep after a restart", id, a.Seen.Liveness, a.Seen.GoneSince)
		}
	}
	// What is seen is believed: the agent that came back is live at the next look.
	if w.after(5 * time.Second); w.state().Attempts["T1.1"].Seen.Liveness != contract.Live {
		t.Fatal("an agent that came back is not live")
	}
	// Recovery: for 120 s the shell under the old pane id is not even stamped gone.
	if w.after(110 * time.Second); !w.state().Attempts["T3.1"].Seen.GoneSince.IsZero() || !w.state().Attempts["T5.1"].Seen.GoneSince.IsZero() ||
		lost.Place.Pane != before.Attempts["T3.1"].Place.Pane {
		t.Fatal("stamped gone while the keeper may still be bringing agents back")
	}
	// After it the two steps of any exit apply: a pane that is there is not an agent that is.
	if w.after(10 * time.Second); w.state().Attempts["T3.1"].Seen.GoneSince.IsZero() || w.state().Attempts["T5.1"].Seen.GoneSince.IsZero() {
		t.Fatal("not stamped gone after recovery")
	}
	if w.after(29 * time.Second); w.state().Attempts["T3.1"].State != contract.AttemptWorking {
		t.Fatal("exited before the second sighting was due")
	}
	w.after(5 * time.Second)
	s = w.state()
	if a := s.Attempts["T3.1"]; a.State != contract.AttemptExited || s.Tasks["T3"].Status != contract.TaskReady ||
		!slices.Equal(kinds(s.Inbox.Events, "T3.1"), []string{"exited"}) {
		t.Fatalf("after recovery: %s, %s, %v", a.State, s.Tasks["T3"].Status, kinds(s.Inbox.Events, ""))
	}
	// An agent that took its conversation up again rests until it is told
	// to go on: the lead agent learns of each as of any worker gone quiet.
	if got := kinds(s.Inbox.Events, "T1.1"); !slices.Equal(got, []string{"quiet"}) || s.Attempts["T1.1"].State != contract.AttemptWorking {
		t.Fatalf("a resumed agent at rest: %v", got)
	}
}

func TestFoundByItsNameNeverByItsLabel(t *testing.T) {
	w := evening(t, nil)
	w.sweep()
	// Two tabs carry one label, and T1's agent now runs in a third pane
	// while its old pane is a shell under the old id.
	snap, s := w.picture(), w.state()
	one, three := s.Attempts["T1.1"], s.Attempts["T3.1"]
	var label string
	for i, p := range snap.Panes {
		switch p.ID {
		case three.Place.Pane:
			label = p.Label
		case one.Place.Pane:
			moved := p
			moved.ID, moved.Tab, moved.Terminal = "w1:p90", "w1:t90", "term-90"
			snap.Panes[i].Agent, snap.Panes[i].Name, snap.Panes[i].Session, snap.Panes[i].Status = "", "", "", contract.StatusUnknown
			snap.Panes = append(snap.Panes, moved)
		}
	}
	for i := range snap.Panes {
		if id := snap.Panes[i].ID; id == one.Place.Pane || id == "w1:p90" {
			snap.Panes[i].Label = label
		}
	}
	w.p.Double.Load(snap)
	w.after(5 * time.Second)
	w.after(40 * time.Second)
	s = w.state()
	if a := s.Attempts["T1.1"]; a.Place.Pane != "w1:p90" || a.Place.Tab != "w1:t90" || a.Place.Terminal != "term-90" || a.State != contract.AttemptWorking || a.Seen.Liveness != contract.Live {
		t.Fatalf("an agent in another pane under its own name: %+v, %s, %s", a.Place, a.State, a.Seen.Liveness)
	}
	if a := s.Attempts["T3.1"]; a.Place != three.Place || a.State != contract.AttemptWorking {
		t.Fatalf("its namesake by label was touched: %+v", a.Place)
	}
	// The agent of the other tab with that label leaves: only its own attempt ends.
	w.p.Double.Drop(testkit.PushGone, three.Place.Pane)
	w.after(5 * time.Second)
	w.after(35 * time.Second)
	s = w.state()
	if s.Attempts["T3.1"].State != contract.AttemptExited || s.Attempts["T1.1"].State != contract.AttemptWorking || !slices.Equal(kinds(s.Inbox.Events, ""), []string{"exited"}) {
		t.Fatalf("two tabs with one label: T3.1 %s, T1.1 %s, %v", s.Attempts["T3.1"].State, s.Attempts["T1.1"].State, kinds(s.Inbox.Events, ""))
	}
}

// crew replaces the fixture's work with n attempts at work, each in a tab of its own.
func crew(n int) func(*testkit.Fixture) {
	return func(f *testkit.Fixture) {
		task, attempt, pane, lead := *f.State.Tasks["T1"], *f.State.Attempts["T1.1"], f.Terms.Panes[1], f.Terms.Panes[0]
		f.State.Tasks, f.State.Attempts, f.State.Questions = map[string]*contract.Task{}, map[string]*contract.Attempt{}, map[string]*contract.Question{}
		f.State.Run.Findings, f.Terms.Panes, f.Ctx = nil, []contract.Pane{lead}, nil
		for i := range n {
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
	}
}

func TestElevenOfTwentyGoingIdleInAMinuteIsOneTogether(t *testing.T) {
	w := evening(t, crew(20))
	w.sweep()
	// Ten at rest are not more than half.
	for i := range 10 {
		w.p.Double.Drop(contract.StatusIdle, fmt.Sprint("w1:p", 30+i))
	}
	if w.after(30 * time.Second); len(w.state().Inbox.Events) != 0 {
		t.Fatalf("ten of twenty: %v", kinds(w.state().Inbox.Events, ""))
	}
	w.p.Double.Drop(contract.StatusIdle, "w1:p40")
	w.after(30 * time.Second)
	w.p.Double.Drop(contract.StatusIdle, "w1:p41")
	w.after(5 * time.Second)
	s := w.state()
	q := item(s, contract.CauseTogether)
	if !slices.Equal(kinds(s.Inbox.Events, ""), []string{"together"}) || q == nil || !q.Urgent || q.State != contract.QuestionOpen {
		t.Fatalf("eleven of twenty within a minute: %v, %+v", kinds(s.Inbox.Events, ""), q)
	}
	for id, a := range s.Attempts {
		if a.State != contract.AttemptWorking {
			t.Fatalf("%s was changed to %s", id, a.State)
		}
	}
}

func TestStaleAndOvertime(t *testing.T) {
	w := evening(t, crew(2))
	if err := os.WriteFile(filepath.Join(w.p.Root, "whaleshark.toml"), []byte("[limits]\nstale_minutes = 30\nmax_minutes = 45\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.sweep()
	w.after(29 * time.Minute)
	w.run("W1.1", "progress", "40", "half way")
	if w.after(2 * time.Minute); !slices.Equal(kinds(w.state().Inbox.Events, ""), []string{"stale"}) || w.state().Inbox.Events[0].Attempt != "W0.1" {
		t.Fatalf("working for 31 minutes with no note: %+v", w.state().Inbox.Events)
	}
	w.after(15 * time.Minute)
	w.after(15 * time.Minute)
	s := w.state()
	if got := kinds(s.Inbox.Events, ""); !slices.Equal(got, []string{"stale", "overtime", "overtime", "stale"}) || s.Attempts["W0.1"].State != contract.AttemptWorking {
		t.Fatalf("past the time limit, once each and nothing stopped: %v", got)
	}
}

func TestStuckOnceForEachEpisode(t *testing.T) {
	w := evening(t, func(f *testkit.Fixture) {
		// One task has failed for good, one waits on it, and nothing runs.
		crew(2)(f)
		failed, held := f.State.Tasks["W0"], f.State.Tasks["W1"]
		failed.Status, failed.Failures, held.Status, held.After = contract.TaskFailed, contract.MaxFailures, contract.TaskPending, []string{"W0"}
		for _, a := range f.State.Attempts {
			a.State, a.EndedAt = contract.AttemptExited, f.Now
		}
		f.State.Attempts["W1.1"].State = contract.AttemptWorking
		held.Status = contract.TaskRunning
	})
	var d delivery
	if w.result(&d, scenario.Orch, "wait", "--timeout", "0"); len(d.Events) != 0 {
		t.Fatalf("stuck while an attempt is at work: %+v", d)
	}
	w.change(func(s *contract.State, now time.Time) error {
		s.Attempts["W1.1"].State, s.Tasks["W1"].Status = contract.AttemptStopped, contract.TaskPending
		return nil
	})
	if w.result(&d, scenario.Orch, "wait", "--timeout", "0"); !slices.Equal(kinds(d.Events, ""), []string{"stuck"}) {
		t.Fatalf("nothing runs and nothing can: %+v", d)
	}
	// Once for the episode, however often the lead agent waits.
	for range 2 {
		if w.result(&d, scenario.Orch, "wait", "--ack", "d10", "--timeout", "0"); len(d.Events) != 0 {
			t.Fatalf("stuck was raised twice: %+v", d)
		}
	}
	// The task is set going again and fails again: a new episode.
	if _, errOut, exit := w.run(scenario.Orch, "task", "reset", "W0"); exit != 0 {
		t.Fatal(errOut)
	}
	if w.result(&d, scenario.Orch, "wait", "--timeout", "0"); len(d.Events) != 0 || w.state().Run.StuckFlagged {
		t.Fatalf("a task that can be started is not stuck: %+v", d)
	}
	w.change(func(s *contract.State, now time.Time) error {
		s.Tasks["W0"].Status = contract.TaskFailed
		return nil
	})
	if w.result(&d, scenario.Orch, "wait", "--timeout", "0"); !slices.Equal(kinds(d.Events, ""), []string{"stuck"}) || d.ID != "d11" {
		t.Fatalf("the second episode: %+v", d)
	}
	// An open item of the lead agent's for the person can still bring an event.
	w.change(func(s *contract.State, now time.Time) error {
		_, err := rule.Need(s, contract.Question{Form: contract.FormQuestion, From: contract.FromLead, Text: "which one?"}, now)
		s.Run.StuckFlagged = false
		return err
	})
	if w.result(&d, scenario.Orch, "wait", "--ack", "d11", "--timeout", "0"); len(d.Events) != 0 || w.state().Run.StuckFlagged {
		t.Fatalf("stuck while the person is asked: %+v", d)
	}
}

func TestAHumanEventIsHandedOverWithItsOutcome(t *testing.T) {
	w := evening(t, func(f *testkit.Fixture) {
		for _, data := range []map[string]any{
			{"what": "accept", "where": contract.WherePane, "on": "T7"},
			{"what": "stop", "where": contract.WhereTyped, "on": "login page"},
			{"what": "answer", "where": contract.WherePage, "id": "q10"},
			{"what": "pause", "where": contract.WherePane},
		} {
			f.State.Counters.Seq++
			f.State.Inbox.Events = append(f.State.Inbox.Events, contract.Event{Seq: f.State.Counters.Seq, At: f.Now, Kind: "human", Data: data})
		}
		f.State.Run.Lead.SilentSince = f.Now
	})
	var d delivery
	w.result(&d, scenario.Orch, "wait", "--timeout", "0")
	var got []any
	for _, e := range d.Events {
		got = append(got, e.Data["outcome"])
	}
	if want := []any{"review", "running", "open", nil}; !slices.Equal(got, want) {
		t.Fatalf("the outcomes handed over: %v, want %v", got, want)
	}
	if out, _, _ := w.run(scenario.Orch, "wait"); !strings.Contains(out, `"outcome":"review"`) {
		t.Fatalf("the outcome is not in the text:\n%s", out)
	}
	for _, e := range w.state().Inbox.Events {
		if _, kept := e.Data["outcome"]; kept {
			t.Fatal("the record keeps an outcome, which is only true of the moment it was handed over")
		}
	}
}

// nudges is a notifier that keeps what it was asked to say.
type nudges struct {
	contract.NoNotifier
	said *[]contract.Nudge
}

func (n nudges) Nudge(c contract.Nudge) error { *n.said = append(*n.said, c); return nil }

// outage is the terminals with a keeper that can be made not to answer.
type outage struct {
	contract.Terminals
	down *bool
}

func (o outage) Snapshot(ctx context.Context) (*contract.Snapshot, error) {
	if *o.down {
		return nil, contract.ErrEngineUnreachable
	}
	return o.Terminals.Snapshot(ctx)
}

func TestTheEngineOutOfReachForAMinuteIsOneUrgentNudge(t *testing.T) {
	w := evening(t, nil)
	rules.Plug(w.p.Kit)
	Plug(w.p.Kit)
	var said []contract.Nudge
	var down bool
	w.p.Kit.Notifier, w.p.Kit.Terms = nudges{said: &said}, outage{w.p.Kit.Terms, &down}
	look := func(d time.Duration) error {
		w.p.Clock(d)
		_, _, err := w.p.Kit.Sweeper.Sweep(w.p.Root, "r3", contract.Now())
		return err
	}
	if err := look(0); err != nil {
		t.Fatal(err)
	}
	record := w.file(contract.StateFile)
	down = true
	for _, d := range []time.Duration{5 * time.Second, 30 * time.Second, 24 * time.Second} {
		if err := look(d); err == nil || len(said) != 0 {
			t.Fatalf("%v into the outage: %v, %v", d, err, said)
		}
	}
	for range 3 {
		look(5 * time.Second)
	}
	if len(said) != 1 || !said[0].Urgent || said[0].Run != "r3" || said[0].Root != w.p.Root || !strings.Contains(said[0].Title, "engine is not running") {
		t.Fatalf("a minute without the engine: %+v", said)
	}
	if !bytes.Equal(record, w.file(contract.StateFile)) {
		t.Fatal("the record changed while the engine could not be reached")
	}
	// It is back, and gone again: a new outage is told anew, a minute in.
	down = false
	if err := look(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	down = true
	look(5 * time.Second)
	if look(59 * time.Second); len(said) != 1 {
		t.Fatal("told before the second outage was a minute old")
	}
	if look(5 * time.Second); len(said) != 2 {
		t.Fatalf("a second outage: %d nudges", len(said))
	}
}
