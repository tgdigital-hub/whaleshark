package inbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/rules"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// morning is the fixture of three fleets as a project on disk: the
// orchestrator's run r1 is the current one, and billing (r2), search (r3) and
// help pages (r4) lie beside it, each leader and worker in a pane of the
// engine's double.
func morning(t *testing.T) *world {
	t.Helper()
	three := testkit.Fleets()
	runs := three.In.Projects[0].Runs
	f := &testkit.Fixture{Version: testkit.FixtureVersion, Name: "three fleets", Now: three.In.Now, Checked: three.In.Now, State: *runs[0], Terms: three.In.Terms}
	w := &world{T: t, p: scenario.Prepare(t, f, nil)}
	for _, low := range runs[1:] {
		if err := w.p.Kit.Store.Create(w.p.Root, low); err != nil {
			t.Fatal(err)
		}
	}
	rules.Plug(w.p.Kit)
	_, w.dir = w.p.Record()
	// The fixture is written by hand and counts nothing: each run has had one question.
	for _, run := range []string{"r1", "r2"} {
		w.in(run, func(s *contract.State, _ time.Time) { s.Counters.Question = 1 })
	}
	return w
}

func (w *world) of(run string) *contract.State {
	w.Helper()
	s, err := w.p.Kit.Store.Read(w.p.Root, run)
	if err != nil {
		w.Fatal(err)
	}
	return s
}

func (w *world) in(run string, fn func(s *contract.State, now time.Time)) {
	w.Helper()
	if err := w.p.Kit.Store.Change(w.p.Root, run, func(s *contract.State) error { fn(s, contract.Now()); return nil }); err != nil {
		w.Fatal(err)
	}
}

// low sweeps a fleet's own run, as its leader's wait and its panes do.
func (w *world) low(run string, by time.Duration) {
	w.Helper()
	w.p.Clock(by)
	if _, _, err := (sweeper{w.p.Kit}).sweep(w.p.Root, run, contract.Now(), true); err != nil {
		w.Fatal(err)
	}
}

// as runs the real program from the tab of an attempt of any run, with the
// variables a start gives that tab and the attempt's token in its place.
func (w *world) as(run, attempt string, args ...string) (string, int) {
	w.Helper()
	token := filepath.Join(w.p.Kit.Store.Dir(w.p.Root, run), "attempts", attempt, "token")
	if err := os.MkdirAll(filepath.Dir(token), 0o700); err != nil {
		w.Fatal(err)
	}
	if err := os.WriteFile(token, []byte("token-of-"+attempt+"\n"), 0o600); err != nil {
		w.Fatal(err)
	}
	w.in(run, func(s *contract.State, _ time.Time) {
		s.Attempts[attempt].TokenHash = contract.TokenHash("token-of-" + attempt)
	})
	a := w.of(run).Attempts[attempt]
	bin, _ := exec.LookPath("whaleshark")
	cmd := w.p.Command(scenario.Unbound, args...)
	cmd.Env = append(cmd.Env, contract.EnvPane+"="+a.Place.Pane, contract.EnvRoot+"="+w.p.Root, contract.EnvRun+"="+run,
		contract.EnvTask+"="+a.Task, contract.EnvAttempt+"="+attempt, contract.EnvBin+"="+bin)
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

// The row's second scenario: a leader is killed. The orchestrator is handed
// exited and its group is ready again; the fleet's run stays open and its
// workers go on; what they report waits in the record, with nobody called
// for a lead agent that is not listening; and the leader started next takes
// the run over and finds the report in its first wait.
func TestALeaderKilledAndItsFleetTakenOverWithReportsWaiting(t *testing.T) {
	w := morning(t)
	w.after(5 * time.Second)
	old := w.state().Run.Fleets["billing"].Pane
	w.p.Double.Push(testkit.PushGone, old)
	w.after(5 * time.Second)
	if a := w.state().Attempts["G1.1"]; a.State != contract.AttemptWorking {
		t.Fatalf("one sighting of an empty pane ended the leader: %s", a.State)
	}
	w.after(31 * time.Second)
	up := w.state()
	if a, g, f := up.Attempts["G1.1"], up.Tasks["G1"], up.Run.Fleets["billing"]; a.State != contract.AttemptExited || g.Status != contract.TaskReady ||
		g.Failures != 1 || f.Pane != "" || !f.EndedAt.IsZero() || strings.Join(kinds(up.Inbox.Events, "G1.1"), ",") != "exited" {
		t.Fatalf("after the leader was gone: attempt %s, group %s with %d failures, fleet %+v, events %v", a.State, g.Status, g.Failures, f, kinds(up.Inbox.Events, ""))
	}
	if low := w.of("r2"); !low.Run.ClosedAt.IsZero() || low.Attempts["T3.1"].State != contract.AttemptWorking {
		t.Fatalf("the fleet's run did not stay as it was: closed %v, T3.1 %s", low.Run.ClosedAt, low.Attempts["T3.1"].State)
	}

	// A worker reports to the record, not to an agent.
	result := filepath.Join(w.p.Kit.Store.Dir(w.p.Root, "r2"), "attempts", "T3.1", "result.md")
	if err := os.MkdirAll(filepath.Dir(result), 0o700); err != nil || os.WriteFile(result, []byte("rounded\n"), 0o600) != nil {
		t.Fatal("the result file could not be written")
	}
	if out, exit := w.as("r2", "T3.1", "report", "done", "the rounding is right to the cent"); exit != 0 {
		t.Fatalf("a worker's report with no leader: exit %d\n%s", exit, out)
	}
	w.low("r2", 5*time.Second)
	w.low("r2", 3*time.Minute)
	low := w.of("r2")
	if got := strings.Join(kinds(low.Inbox.Events, "T3.1"), ","); got != "done" || item(low, contract.CauseLeadSilent) != nil || item(low, contract.CauseLeadUnread) != nil {
		t.Fatalf("the report waits as %q; an item for the person about a leader: %v, %v", got, item(low, contract.CauseLeadSilent), item(low, contract.CauseLeadUnread))
	}

	// The group is started again, as start does it: a new attempt in a tab of
	// its own, which leads the run that was left.
	k, root := w.p.Kit, w.p.Root
	tab, err := k.Terms.TabCreate(root, "billing", nil)
	if err == nil {
		err = k.Terms.AgentStart("a-new-leader", "codex", tab.ID, nil, 10*time.Second)
	}
	if err != nil {
		t.Fatal(err)
	}
	gen := low.Run.Gen
	err = k.Store.Both(root, "r1", "r2", func(up, low *contract.State) error {
		now := contract.Now()
		id, err := k.Rules.Start(up, "G1", true, false, "", contract.Agent{Kind: "codex", Name: "a-new-leader"}, false, now)
		if err == nil {
			err = k.Rules.Placed(up, id, contract.Place{Tab: tab.Tab, Pane: tab.ID, Cwd: root, OpenedByUs: true}, now)
		}
		if err == nil {
			err = k.Rules.Lead(up, low, id, contract.Binding{Pane: tab.ID}, "", now)
		}
		if err == nil {
			err = k.Rules.Working(up, id, now)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if up, low = w.state(), w.of("r2"); up.Run.Fleets["billing"].Pane != tab.ID || low.Run.Orchestrator.Pane != tab.ID || low.Run.Gen != gen+1 || up.Attempts["G1.2"].Leads != "r2" {
		t.Fatalf("the fleet was not taken over: entry %+v, run bound to %+v", up.Run.Fleets["billing"], low.Run.Orchestrator)
	}
	out, exit := w.as("r1", "G1.2", "wait", "--timeout", "20")
	if exit != 0 || !strings.Contains(out, "done T3 T3.1 the rounding is right to the cent") {
		t.Fatalf("the new leader's first wait: exit %d\n%s", exit, out)
	}
	// And the orchestrator's sweep now sees a leader at work, and parks nothing.
	w.after(3 * time.Minute)
	if f := w.state().Run.Fleets["billing"]; f.Pane != tab.ID {
		t.Fatalf("the new leader's fleet: %+v", f)
	}
}

// A leader that holds no group at work is seen by no row of the sweep about
// attempts when its program ends: its fleet's entry names a pane with no
// agent. After two minutes the fleet whose groups are all settled ends, and
// one whose group still waits to be checked is parked and the orchestrator told.
func TestALeaderThatEndsWhileItHoldsNoGroup(t *testing.T) {
	w := morning(t)
	w.in("r1", func(s *contract.State, now time.Time) {
		// Help pages' group is collected; search's is reported and waits to be checked.
		s.Tasks["G3"].Status, s.Tasks["G3"].SettledAt = contract.TaskDone, now
		s.Attempts["G3.1"].State, s.Attempts["G3.1"].EndedAt = contract.AttemptAccepted, now
		s.Tasks["G2"].Status, s.Attempts["G2.1"].State = contract.TaskReview, contract.AttemptReported
		s.Attempts["G2.1"].Report = &contract.Report{Outcome: contract.ReportDone, At: now}
	})
	w.in("r4", func(s *contract.State, now time.Time) {
		for _, a := range s.Attempts {
			a.State, s.Tasks[a.Task].Status = contract.AttemptAccepted, contract.TaskDone
		}
	})
	fleets := w.state().Run.Fleets
	search, help, billing := fleets["search"].Pane, fleets["help pages"].Pane, fleets["billing"].Pane
	w.after(5 * time.Second)
	if up := w.state(); up.Run.Fleets["search"].Pane != search || up.Run.Fleets["help pages"].Pane != help {
		t.Fatal("a leader that sits in its pane with no group was taken for gone")
	}
	w.p.Double.Push(testkit.PushGone, search)
	w.p.Double.Push(testkit.PushClosed, help)
	w.after(5 * time.Second)
	w.after(90 * time.Second)
	if up := w.state(); up.Run.Fleets["search"].Pane != search || !up.Run.Fleets["help pages"].EndedAt.IsZero() {
		t.Fatalf("a pane without its agent for ninety seconds ended a fleet: %+v, %+v", up.Run.Fleets["search"], up.Run.Fleets["help pages"])
	}
	events := len(w.state().Inbox.Events)
	w.after(31 * time.Second)
	up := w.state()
	if f := up.Run.Fleets["help pages"]; f.EndedAt.IsZero() || f.Pane != "" || w.of("r4").Run.ClosedAt.IsZero() {
		t.Fatalf("the fleet whose groups are settled did not end: %+v, its run closed at %v", f, w.of("r4").Run.ClosedAt)
	}
	if f := up.Run.Fleets["search"]; f.Pane != "" || !f.EndedAt.IsZero() || !w.of("r3").Run.ClosedAt.IsZero() || up.Tasks["G2"].Status != contract.TaskReview {
		t.Fatalf("the fleet whose group waits to be checked is not parked as it stood: %+v, G2 %s", f, up.Tasks["G2"].Status)
	}
	if n := len(up.Inbox.Events); n != events+1 || up.Inbox.Events[n-1].Kind != "exited" || !strings.Contains(up.Inbox.Events[n-1].Text, "search") {
		t.Fatalf("the orchestrator was told %v", up.Inbox.Events[events:])
	}
	if f := up.Run.Fleets["billing"]; f.Pane != billing || up.Attempts["G1.1"].State != contract.AttemptWorking {
		t.Fatalf("a leader at work was touched: %+v", f)
	}
	// It is said once.
	w.after(3 * time.Minute)
	if n := len(w.state().Inbox.Events); n != events+1 {
		t.Fatalf("a parked fleet was told again: %v", w.state().Inbox.Events[events:])
	}
}

// A leader with a group at work whose program ends is ended by its
// attempt's own row, once: the row for a leader that holds no group says
// nothing of it, though that proof takes longer than its two minutes, as it
// does after the keeper gave the pane a new terminal.
func TestALeaderAtWorkIsItsAttemptsToEnd(t *testing.T) {
	w := morning(t)
	billing := w.state().Run.Fleets["billing"].Pane
	w.p.Double.Push(testkit.PushGone, billing)
	w.in("r1", func(s *contract.State, now time.Time) {
		a := s.Attempts["G1.1"]
		a.Seen.Status, a.Seen.Liveness, a.Seen.GoneSince, a.Seen.At = "", contract.Unverifiable, time.Time{}, now
	})
	events := len(w.state().Inbox.Events)
	w.after(6 * time.Second)
	w.after(125 * time.Second)
	if up := w.state(); up.Run.Fleets["billing"].Pane != billing || up.Attempts["G1.1"].State != contract.AttemptWorking || len(up.Inbox.Events) != events {
		t.Fatalf("the leader's end was said before its attempt showed it: %+v, told %v", up.Run.Fleets["billing"], up.Inbox.Events[events:])
	}
	w.after(35 * time.Second)
	w.after(3 * time.Minute)
	up := w.state()
	if n := len(up.Inbox.Events); up.Attempts["G1.1"].State != contract.AttemptExited || n != events+1 || up.Inbox.Events[n-1].Kind != "exited" || up.Inbox.Events[n-1].Task != "G1" {
		t.Fatalf("G1.1 is %s and the orchestrator was told %v", up.Attempts["G1.1"].State, up.Inbox.Events[events:])
	}
}

// A question and its twin lie in two runs, written one after the other. The
// sweep of either run finishes what a kill between the two writes left: the
// lower half of a twin, an orchestrator's answer that did not go down. And a
// person's answer goes down only once it has settled.
func TestTheSweepFinishesWhatLiesInTwoRuns(t *testing.T) {
	w := morning(t)
	k := w.p.Kit
	// A worker asks its leader; nothing is handed up, and no sweep hands it up.
	w.in("r2", func(s *contract.State, now time.Time) {
		if _, err := k.Rules.Ask(s, "T3.1", "Round half up, or to even?", nil, now); err != nil {
			t.Fatal(err)
		}
	})
	asked := len(w.state().Questions)
	w.after(5 * time.Second)
	w.low("r2", 5*time.Second)
	if q := w.of("r2").Questions["q2"]; q.Up != nil || len(w.state().Questions) != asked {
		t.Fatalf("a question for a leader climbed by itself: %+v", q)
	}

	// The leader hands it up, and the command is killed after the first of
	// its two writes: the twin is above, the question below knows nothing.
	twin, below := "", w.of("r2") // read before the lock is taken: the project has one
	w.in("r1", func(up *contract.State, now time.Time) {
		var err error
		if twin, err = k.Rules.Twin(up, below, "q2", now); err != nil {
			t.Fatal(err)
		}
	})
	if q := w.of("r2").Questions["q2"]; q.Up != nil {
		t.Fatal("the lower half was written too: this is not the kill the test is about")
	}
	w.after(5 * time.Second)
	if q := w.of("r2").Questions["q2"]; q.Up == nil || q.Up.Run != "r1" || q.Up.ID != twin || len(w.state().Questions) != asked+1 {
		t.Fatalf("the orchestrator's sweep did not finish the twin: %+v, %d questions above", q.Up, len(w.state().Questions))
	}

	// The orchestrator answers, and is killed before the answer goes down:
	// this time it is the fleet's own sweep that finishes it.
	w.in("r1", func(up *contract.State, now time.Time) {
		if err := k.Rules.Answer(up, twin, "to even", contract.Origin{Caller: contract.Orchestrator}, 0, now); err != nil {
			t.Fatal(err)
		}
	})
	w.low("r2", 5*time.Second)
	if q, above := w.of("r2").Questions["q2"], w.state().Questions[twin]; q.Answer != "to even" || q.AnsweredBy.Caller != contract.Orchestrator || above.State != contract.QuestionUsed {
		t.Fatalf("the answer did not go down: %+v; the twin is %s", q, above.State)
	}

	// The person answers the question that climbed to them. Until it has
	// settled they can take it back, so nothing goes down; then it does.
	w.in("r1", func(up *contract.State, now time.Time) {
		if err := k.Rules.Answer(up, "q1", "Card", contract.Origin{Caller: contract.Human, Where: contract.WherePane}, 4*time.Second, now); err != nil {
			t.Fatal(err)
		}
	})
	w.sweepNow()
	if q := w.of("r2").Questions["q1"]; q.Answer != "" {
		t.Fatalf("an answer went down before it had settled: %+v", q)
	}
	asker := w.of("r2").Attempts["T1.1"].Place.Pane
	w.p.Double.Push(contract.StatusIdle, asker) // it gave up waiting, and rests
	w.after(5 * time.Second)
	if n := w.typed(asker, "Your question q1 was answered. Run: whaleshark ask --resume q1"); n != 1 {
		t.Fatalf("the worker that rests was pointed at its answer %d times", n)
	}
	if q, above := w.of("r2").Questions["q1"], w.state().Questions["q1"]; q.Answer != "Card" || q.AnsweredBy.Caller != contract.Human || above.State != contract.QuestionUsed {
		t.Fatalf("the person's answer did not go down once settled: %+v; the twin is %s", q, above.State)
	}
	// Nothing is left to do, and a sweep that finds nothing writes nothing.
	before := w.file(contract.StateFile)
	w.after(5 * time.Second)
	w.low("r2", 5*time.Second)
	if after := w.file(contract.StateFile); string(before) != string(after) {
		t.Fatal("a sweep with nothing to finish wrote the orchestrator's record")
	}
}

// sweepNow sweeps the orchestrator's run without moving the clock.
func (w *world) sweepNow() {
	w.Helper()
	if _, _, err := (sweeper{w.p.Kit}).sweep(w.p.Root, "r1", contract.Now(), true); err != nil {
		w.Fatal(err)
	}
}
