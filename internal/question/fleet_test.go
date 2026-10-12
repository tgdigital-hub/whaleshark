package question

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/internal/rules"
	"github.com/tgdigital-hub/whaleshark/internal/store"
	"github.com/tgdigital-hub/whaleshark/test/fakeengine"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// The callers of the morning of three fleets, by pane, run and attempt: the
// orchestrator of r1, the leader of billing, which is the attempt of group
// G1 above and the lead agent of r2, and its worker "refund flow".
var (
	orch   = [3]string{"p1"}
	leader = [3]string{"p2", "r1", "G1.1"}
	refund = [3]string{"p3", "r2", "T1.1"}
)

// morning is the fixture of three fleets as a project on disk, before the
// worker of billing has asked anything: the orchestrator's run and its
// fleets' in the store, a token for every attempt, the panes in the double.
// idle names the panes whose agents rest.
func morning(t *testing.T, idle ...string) *scenario.Project {
	t.Helper()
	f, p := testkit.Fleets(), scenario.Prepare(t, nil, nil)
	if err := contract.ListProject(p.Kit.Platform, p.Root); err != nil {
		t.Fatal(err)
	}
	for _, s := range f.In.Projects[0].Runs {
		clear(s.Questions)
		for id, a := range s.Attempts {
			if a.State == contract.AttemptAsked {
				a.State = contract.AttemptWorking
			}
			token := filepath.Join(p.Kit.Store.Dir(p.Root, s.Run.ID), "attempts", id, "token")
			if err := errors.Join(os.MkdirAll(filepath.Dir(token), 0o700), os.WriteFile(token, []byte("token-of-"+id+"\n"), 0o600)); err != nil {
				t.Fatal(err)
			}
			a.TokenHash = contract.TokenHash("token-of-" + id)
		}
		if err := p.Kit.Store.Create(p.Root, s); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Kit.Store.SetCurrent(p.Root, "r1"); err != nil {
		t.Fatal(err)
	}
	panes := append(slices.Clone(f.In.Terms.Panes), contract.Pane{ID: p.Own, Tab: "w1:t0", Label: "you", Cwd: p.Root})
	for i := range panes {
		if slices.Contains(idle, panes[i].ID) {
			panes[i].Status = contract.StatusIdle
		}
	}
	p.Double.Load(&contract.Snapshot{Panes: panes})
	return p
}

// from is the real program started in a pane of the morning.
func from(p *scenario.Project, who [3]string, args ...string) *exec.Cmd {
	cmd := p.Command(scenario.Unbound, args...)
	cmd.Env = append(cmd.Env, contract.EnvPane+"="+who[0])
	if who[2] != "" {
		cmd.Env = append(cmd.Env, contract.EnvRoot+"="+p.Root, contract.EnvRun+"="+who[1], contract.EnvAttempt+"="+who[2])
	}
	return cmd
}

// does runs a command from a pane; it has to end as said and print the words.
func does(t *testing.T, p *scenario.Project, who [3]string, exit int, line string, words ...string) string {
	t.Helper()
	cmd := from(p, who, strings.Fields(line)...)
	out, _ := cmd.CombinedOutput()
	if got := cmd.ProcessState.ExitCode(); got != exit {
		t.Fatalf("%s: %s: exit %d, expected %d\n%s", who[0], line, got, exit, out)
	}
	for _, w := range words {
		if !strings.Contains(string(out), w) {
			t.Fatalf("%s: %s: expected %q in\n%s", who[0], line, w, out)
		}
	}
	return string(out)
}

// asks leaves a command of a pane running, as a worker's ask is, until it
// holds the lock of what it asked in its run.
func asks(t *testing.T, p *scenario.Project, who [3]string, run, id string, args ...string) *blocked {
	t.Helper()
	b := &blocked{cmd: from(p, who, args...)}
	b.cmd.Stdout, b.cmd.Stderr = &b.out, &b.out
	if err := b.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.cmd.Process.Kill() })
	lock := contract.AskLock(p.Kit.Store.Dir(p.Root, run), id)
	until(t, strings.Join(args, " ")+" waits", func() bool {
		unlock, free, err := p.Kit.Platform.TryLock(lock)
		if free {
			unlock()
		}
		return err == nil && !free
	})
	return b
}

// both reads the orchestrator's run and billing's.
func both(t *testing.T, p *scenario.Project) (up, low *contract.State) {
	t.Helper()
	up, err := p.Kit.Store.Read(p.Root, "r1")
	if err == nil {
		low, err = p.Kit.Store.Read(p.Root, "r2")
	}
	if err != nil {
		t.Fatal(err)
	}
	return up, low
}

// sweep is the part of the next sweep that is another package's: a twin's
// settled answer is copied down (Rules.Down), under the one lock.
func sweep(t *testing.T, p *scenario.Project, twin string) bool {
	t.Helper()
	done := false
	err := p.Kit.Store.Both(p.Root, "r1", "r2", func(up, low *contract.State) (err error) {
		done, err = rules.Rules{}.Down(up, low, twin, contract.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return done
}

func typed(p *scenario.Project, pane string) (lines []string) {
	for _, c := range p.Double.Calls() {
		if line := fakeengine.Typed(c); c.Pane == pane && line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// A worker's question goes up through three levels, one at a time, and its
// answer comes back the same way (19.1): the leader hands it to the
// orchestrator, the orchestrator to the person, and what the person said is
// in the worker's ask once it has settled. Then a leader's own item: the
// orchestrator answers it and the leader's need returns the answer.
func TestAQuestionThroughThreeLevelsAndBack(t *testing.T) {
	p := morning(t, "p1")
	ask := asks(t, p, refund, "r2", "q1", "ask", "Refund to the card, or to store credit?", "--options", "Card,Credit")

	// The leader may answer it; it does not know, and hands it up. Whether
	// the orchestrator is pointed at it is asked of the orchestrator's own
	// wait, not of one that runs in the fleet.
	inFleet, free, err := p.Kit.Platform.TryLock(filepath.Join(p.Kit.Store.Dir(p.Root, "r2"), contract.WaitLock))
	if err != nil || !free {
		t.Fatal("the lock of the fleet's wait: ", err)
	}
	does(t, p, leader, 0, "escalate q1", "q1 is now for the orchestrator, as q1 of run r1")
	inFleet()
	up, low := both(t, p)
	twin, first := up.Questions["q1"], low.Questions["q1"]
	if twin == nil || twin.For != contract.ForOrchestrator || twin.Task != "G1" || twin.Attempt != "G1.1" || twin.Form != contract.FormChoice ||
		*twin.Down != (contract.Twin{Run: "r2", ID: "q1"}) || *first.Up != (contract.Twin{Run: "r1", ID: "q1"}) || first.For != contract.ForOrchestrator {
		t.Fatalf("the twin: %+v of %+v", twin, first)
	}
	if e := up.Inbox.Events[len(up.Inbox.Events)-1]; e.Kind != "question" || e.Data["fleet"] != "billing" || e.Data["task"] != "T1" {
		t.Fatalf("the orchestrator is told: %+v", e)
	}
	if got := typed(p, "p1"); len(got) != 1 || !strings.Contains(got[0], "1 events waiting") {
		t.Fatalf("the resting orchestrator is pointed at its event: %q", got)
	}
	does(t, p, leader, 5, "answer q1 Card", "went to the orchestrator")
	does(t, p, leader, 4, "escalate q9")

	// The orchestrator does not know either: the twin is the person's, and
	// no word of the lead agent's own passes for theirs.
	does(t, p, orch, 0, "escalate q1", "q1 is now for the person")
	does(t, p, orch, 5, "answer q1 Card", "for the person")
	if up, _ = both(t, p); up.Questions["q1"].For != contract.ForHuman {
		t.Fatalf("the twin is for %s", up.Questions["q1"].For)
	}

	// The person presses a button on the page: the answer settles first.
	cmd := p.Command(scenario.Page, "answer", "q1", "Credit")
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "It can be taken back") {
		t.Fatalf("the person's answer: %v\n%s", err, out)
	}
	if _, low = both(t, p); low.Questions["q1"].Answer != "" || sweep(t, p, "q1") {
		t.Fatal("an answer went down before it had settled")
	}
	p.Clock(5 * time.Second)
	if !sweep(t, p, "q1") {
		t.Fatal("a settled answer did not go down")
	}
	if out, exit := ask.end(t); exit != 0 || strings.TrimSpace(out) != "Credit" {
		t.Fatalf("the worker's ask ended %d with %q", exit, out)
	}
	up, low = both(t, p)
	if q := low.Questions["q1"]; q.State != contract.QuestionUsed || *q.AnsweredBy != (contract.Origin{Caller: contract.Human, Where: contract.WherePage}) ||
		up.Questions["q1"].State != contract.QuestionUsed || low.Attempts["T1.1"].State != contract.AttemptWorking {
		t.Fatalf("after the answer: %+v above %+v", q, up.Questions["q1"])
	}
	for run, s := range map[string]*contract.State{"r1": up, "r2": low} {
		if e := s.Inbox.Events[len(s.Inbox.Events)-1]; e.Kind != "answered" || e.Text != "Credit" {
			t.Errorf("the lead agent of %s is not told the answer: %+v", run, e)
		}
	}

	// A leader's own item is the orchestrator's: it keeps its mark on the
	// way up, the orchestrator's answer goes down in the same step, and the
	// leader's need hands it over.
	need := asks(t, p, leader, "r2", "n1", "need", "question", "Which tax table?", "--urgent", "--wait")
	up, low = both(t, p)
	if n, tw := low.Questions["n1"], up.Questions["q2"]; n.For != contract.ForOrchestrator || *n.Up != (contract.Twin{Run: "r1", ID: "q2"}) ||
		tw == nil || !tw.Urgent || tw.Text != "Which tax table?" || tw.For != contract.ForOrchestrator {
		t.Fatalf("a leader's item: %+v above %+v", n, tw)
	}
	does(t, p, orch, 0, "answer q2 the-new-one --json", `"told":"waiting"`)
	if out, exit := need.end(t); exit != 0 || strings.TrimSpace(out) != "the-new-one" {
		t.Fatalf("the leader's need ended %d with %q", exit, out)
	}
	if up, low = both(t, p); up.Questions["q2"].State != contract.QuestionUsed || low.Questions["n1"].AnsweredBy.Caller != contract.Orchestrator {
		t.Fatalf("after the orchestrator's answer: %+v above %+v", low.Questions["n1"], up.Questions["q2"])
	}
	// A leader that did not wait is pointed at the answer when it rests.
	q := morning(t, "p2")
	does(t, q, leader, 0, "need todo Renew-the-test-card", "n1")
	does(t, q, orch, 0, "answer q1 done", "q1 answered")
	if got := typed(q, "p2"); len(got) != 1 || got[0] != string(contract.PointAnswer) {
		t.Fatalf("the resting leader is pointed at the answer: %q", got)
	}
	// What the leader answers in its own fleet is its lead agent's answer.
	asks(t, q, refund, "r2", "q1", "ask", "Which base?")
	does(t, q, leader, 0, "answer q1 main", "q1 answered")
	if _, low = both(t, q); low.Questions["q1"].AnsweredBy.Caller != contract.Orchestrator || low.Questions["q1"].Up != nil {
		t.Fatalf("the leader's own answer: %+v", low.Questions["q1"])
	}
}

// The person's answer can be taken back at each level until it is used, as
// in a run without fleets, and nothing reaches the level below before it
// has settled: in the fleet's run, on the twin while it is the
// orchestrator's, and on the twin that is the person's own.
func TestAnAnswerUndoneAtEachLevel(t *testing.T) {
	p := morning(t)
	page := func(exit int, words string, args ...string) {
		t.Helper()
		cmd := p.Command(scenario.Page, args...)
		out, _ := cmd.CombinedOutput()
		if got := cmd.ProcessState.ExitCode(); got != exit || !strings.Contains(string(out), words) {
			t.Fatalf("%v: exit %d, expected %d and %q\n%s", args, got, exit, words, out)
		}
	}
	open := func(level string, s *contract.State, id string) {
		t.Helper()
		if q := s.Questions[id]; q.State != contract.QuestionOpen || q.Answer != "" || len(q.Earlier) == 0 {
			t.Fatalf("%s: %s of %s after the undo: %+v", level, id, s.Run.ID, q)
		}
	}
	ask := asks(t, p, refund, "r2", "q1", "ask", "Card or credit?", "--options", "Card,Credit")

	// In the fleet's run, past the orchestrator and the leader.
	page(0, "It can be taken back", "answer", "q1", "Card", "--run", "r2")
	page(0, "q1 is open again", "answer", "q1", "--undo", "--run", "r2")
	_, low := both(t, p)
	open("the fleet's run", low, "q1")

	// On the twin, which the leader made and the orchestrator has not read.
	does(t, p, leader, 0, "escalate q1")
	page(0, "It can be taken back", "answer", "q1", "Card")
	if sweep(t, p, "q1") {
		t.Fatal("an answer went down while it could be taken back")
	}
	page(0, "q1 is open again", "answer", "q1", "--undo")
	up, low := both(t, p)
	open("the orchestrator's", up, "q1")
	if low.Questions["q1"].Answer != "" || len(low.Questions["q1"].Earlier) != 1 {
		t.Fatalf("the fleet's question after an undo above it: %+v", low.Questions["q1"])
	}

	// On the twin once it is the person's own; then it settles and goes down.
	does(t, p, orch, 0, "escalate q1")
	page(0, "It can be taken back", "answer", "q1", "Card")
	page(0, "q1 is open again", "answer", "q1", "--undo")
	up, _ = both(t, p)
	open("the person's", up, "q1")
	page(0, "It can be taken back", "answer", "q1", "Credit")
	p.Clock(5 * time.Second)
	// Given again once it has settled, it goes down without a sweep.
	page(0, "q1 answered", "answer", "q1", "Credit")
	if out, exit := ask.end(t); exit != 0 || strings.TrimSpace(out) != "Credit" {
		t.Fatalf("the worker's ask ended %d with %q", exit, out)
	}
	page(5, "Already acted on", "answer", "q1", "--undo")
	page(5, "Already acted on", "answer", "q1", "--undo", "--run", "r2")
	if up, low = both(t, p); len(up.Questions["q1"].Earlier) != 2 || low.Questions["q1"].Answer != "Credit" {
		t.Fatalf("what was taken back is not kept: %+v above %+v", low.Questions["q1"], up.Questions["q1"])
	}
}

// dying is the store of a program that is killed between the two writes of
// one step on two runs: the upper run is saved and the lower is not.
type dying struct{ contract.Store }

var errKilled = errors.New("killed between the two writes")

func (d dying) Both(root, up, low string, fn func(up, low *contract.State) error) error {
	under, err := d.Store.Read(root, low)
	if err == nil {
		err = d.Store.Change(root, up, func(s *contract.State) error { return fn(s, under) })
	}
	return errors.Join(err, errKilled)
}

// killed runs a command of this package in the test itself, as a pane's
// caller, with the store that dies half way.
func killed(t *testing.T, p *scenario.Project, who [3]string, args ...string) {
	t.Helper()
	k := contract.NewKit()
	for _, plug := range []func(*contract.Kit){platform.Plug, rules.Plug, store.Plug, cli.Plug, Plug} {
		plug(k)
	}
	k.Store, k.Terms = dying{k.Store}, p.Double
	t.Chdir(p.Root)
	for name, v := range map[string]string{contract.EnvPane: who[0], contract.EnvRun: who[1], contract.EnvAttempt: who[2], contract.EnvRoot: p.Root} {
		if t.Setenv(name, v); v == "" {
			os.Unsetenv(name)
		}
	}
	var out strings.Builder
	if exit := k.Main(k, args, strings.NewReader(""), &out, &out); exit == 0 || !strings.Contains(out.String(), errKilled.Error()) {
		t.Fatalf("%v was not killed: exit %d\n%s", args, exit, &out)
	}
}

// A command killed between its two writes leaves the orchestrator's run
// changed and the fleet's not. The same command given again finds the half
// that is there: one twin, one event, one answer; and so does the sweep.
func TestACommandKilledBetweenTheTwoWrites(t *testing.T) {
	p := morning(t)
	ask := asks(t, p, refund, "r2", "q1", "ask", "Which base?")
	count := func(s *contract.State, kind string) (n int) {
		for _, e := range s.Inbox.Events {
			if e.Kind == kind {
				n++
			}
		}
		return n
	}

	killed(t, p, leader, "escalate", "q1")
	up, low := both(t, p)
	if up.Questions["q1"] == nil || low.Questions["q1"].Up != nil {
		t.Fatalf("the half a killed escalate left: %+v above %+v", low.Questions["q1"], up.Questions["q1"])
	}
	does(t, p, leader, 0, "escalate q1", "as q1 of run r1")
	if up, low = both(t, p); len(up.Questions) != 1 || count(up, "question") != 1 || low.Questions["q1"].Up == nil {
		t.Fatalf("escalate again: %d twins, %d events, %+v", len(up.Questions), count(up, "question"), low.Questions["q1"])
	}

	killed(t, p, orch, "answer", "q1", "main")
	if up, low = both(t, p); up.Questions["q1"].State != contract.QuestionUsed || low.Questions["q1"].Answer != "" {
		t.Fatalf("the half a killed answer left: %+v above %+v", low.Questions["q1"], up.Questions["q1"])
	}
	does(t, p, orch, 5, "answer q1 next", "acted on")
	does(t, p, orch, 0, "answer q1 main", "q1 answered")
	if out, exit := ask.end(t); exit != 0 || strings.TrimSpace(out) != "main" {
		t.Fatalf("the worker's ask ended %d with %q", exit, out)
	}
	// The leader is told once; the orchestrator, whose answer it was, is not.
	if up, low = both(t, p); count(up, "answered") != 0 || count(low, "answered") != 1 {
		t.Fatalf("told %d times above and %d below", count(up, "answered"), count(low, "answered"))
	}

	// The same answer, mended by the sweep and not by the command.
	ask = asks(t, p, refund, "r2", "q2", "ask", "Which port?")
	does(t, p, leader, 0, "escalate q2", "as q2 of run r1")
	killed(t, p, orch, "answer", "q2", "the-first")
	if !sweep(t, p, "q2") || sweep(t, p, "q2") {
		t.Fatal("the sweep copies what a kill left above once")
	}
	if out, exit := ask.end(t); exit != 0 || strings.TrimSpace(out) != "the-first" {
		t.Fatalf("the worker's ask ended %d with %q", exit, out)
	}

	// A leader's item whose twin alone was written: the item made again,
	// in other words, is the one twin's other half.
	killed(t, p, leader, "need", "choice", "Keep the old API?")
	if up, low = both(t, p); up.Questions["q3"] == nil || low.Questions["n1"] != nil {
		t.Fatalf("the half a killed need left: %+v above %+v", low.Questions["n1"], up.Questions["q3"])
	}
	does(t, p, leader, 0, "need question Keep-the-older-API?", "n1")
	up, low = both(t, p)
	if tw := up.Questions["q3"]; len(up.Questions) != 3 || tw.Text != "Keep-the-older-API?" || tw.Form != contract.FormQuestion || len(tw.Options) != 0 ||
		*low.Questions["n1"].Up != (contract.Twin{Run: "r1", ID: "q3"}) {
		t.Fatalf("need again: %+v above %+v", low.Questions["n1"], tw)
	}
}

// The person answers inside a fleet's run, past the orchestrator (19.1):
// the orchestrator's run gets a human event of it beside the fleet's own,
// and a twin above takes the same answer and the same undo in that step,
// so it neither stays open above nor brings an undone answer back down.
func TestThePersonAnswersInsideAFleet(t *testing.T) {
	p := morning(t)
	ask := asks(t, p, refund, "r2", "q1", "ask", "Refund to the card, or to store credit?", "--options", "Card,Credit")
	does(t, p, leader, 0, "escalate q1", "as q1 of run r1")
	page := func(args ...string) {
		t.Helper()
		if out, err := p.Command(scenario.Page, append(args, "--run", "r2")...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	last := func(s *contract.State) contract.Event { return s.Inbox.Events[len(s.Inbox.Events)-1] }

	page("answer", "q1", "Credit")
	up, low := both(t, p)
	if tw, q := up.Questions["q1"], low.Questions["q1"]; tw.State != contract.QuestionAnswered || tw.Answer != "Credit" || !tw.SettlesAt.Equal(q.SettlesAt) || q.Answer != "Credit" {
		t.Fatalf("the twin above did not take the answer given below: %+v of %+v", tw, q)
	}
	if e := last(up); e.Kind != "human" || e.Task != "G1" || e.Data["what"] != "answer" || e.Data["fleet"] != "billing" || e.Data["id"] != "q1" ||
		last(low).Kind != "human" || strings.Contains(e.Text, "Credit") {
		t.Fatalf("the orchestrator's run is told %+v, the fleet's %+v", e, last(low))
	}
	page("answer", "q1", "--undo")
	if up, low = both(t, p); up.Questions["q1"].State != contract.QuestionOpen || low.Questions["q1"].State != contract.QuestionOpen || last(up).Data["what"] != "undo" {
		t.Fatalf("after the undo: %+v above, %+v below, told %+v", up.Questions["q1"], low.Questions["q1"], last(up))
	}
	p.Clock(5 * time.Second)
	if sweep(t, p, "q1") {
		t.Fatal("an answer that was taken back went down")
	}
	page("answer", "q1", "Card")
	p.Clock(5 * time.Second)
	if out, exit := ask.end(t); exit != 0 || strings.TrimSpace(out) != "Card" {
		t.Fatalf("the worker's ask ended %d with %q", exit, out)
	}
	if sweep(t, p, "q1") {
		t.Fatal("the sweep answered the question a second time")
	}
	if up, low = both(t, p); up.Questions["q1"].State != contract.QuestionUsed || low.Questions["q1"].State != contract.QuestionUsed || low.Questions["q1"].Answer != "Card" {
		t.Fatalf("at the end: %+v above, %+v below", up.Questions["q1"], low.Questions["q1"])
	}

	// A question that never climbed has no twin: the event above is all.
	asks(t, p, refund, "r2", "q2", "ask", "Which base?")
	before := len(up.Inbox.Events)
	page("answer", "q2", "main")
	if up, _ = both(t, p); len(up.Inbox.Events) != before+1 || last(up).Data["id"] != "q2" || len(up.Questions) != 1 {
		t.Fatalf("a question with no twin: the run above holds %+v and %d questions", last(up), len(up.Questions))
	}
}
