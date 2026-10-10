package question

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/internal/rules"
	"github.com/tgdigital-hub/whaleshark/internal/store"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func TestMain(m *testing.M) { scenario.Main(m) }

// evening is the fixture as a project, changed first where a test needs
// another moment than 21:14.
func evening(t *testing.T, change func(*contract.State)) *scenario.Project {
	t.Helper()
	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	if change != nil {
		change(&f.State)
	}
	return scenario.Prepare(t, f, nil)
}

// run runs the real program as a caller and returns what it printed and its
// exit code.
func run(p *scenario.Project, who string, args ...string) (string, int) {
	cmd := p.Command(who, args...)
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

// must is run for a command that has to end as said and print the words.
func must(t *testing.T, p *scenario.Project, exit int, who string, line string, words ...string) string {
	t.Helper()
	out, got := run(p, who, strings.Fields(line)...)
	if got != exit {
		t.Fatalf("%s: %s: exit %d, expected %d\n%s", who, line, got, exit, out)
	}
	for _, w := range words {
		if !strings.Contains(out, w) {
			t.Fatalf("%s: %s: expected %q in\n%s", who, line, w, out)
		}
	}
	return out
}

// blocked is a command left running, as a worker's ask is.
type blocked struct {
	cmd *exec.Cmd
	out bytes.Buffer
}

func begin(t *testing.T, p *scenario.Project, who string, args ...string) *blocked {
	t.Helper()
	b := &blocked{cmd: p.Command(who, args...)}
	b.cmd.Stdout, b.cmd.Stderr = &b.out, &b.out
	if err := b.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.cmd.Process.Kill() })
	return b
}

// end waits for the command and returns what it printed and its exit code.
func (b *blocked) end(t *testing.T) (string, int) {
	t.Helper()
	late := time.AfterFunc(20*time.Second, func() { b.cmd.Process.Kill() })
	b.cmd.Wait()
	if !late.Stop() {
		t.Fatalf("%v was still running after 20 s\n%s", b.cmd.Args[1:], &b.out)
	}
	return b.out.String(), b.cmd.ProcessState.ExitCode()
}

func until(t *testing.T, what string, so func() bool) {
	t.Helper()
	for end := time.Now().Add(20 * time.Second); !so(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("it never came about: %s", what)
		}
	}
}

// waiting reports whether an ask holds the lock of a question.
func waiting(p *scenario.Project, id string) bool {
	_, dir := p.Record()
	unlock, free, err := p.Kit.Platform.TryLock(filepath.Join(dir, "questions", id+".lock"))
	if free {
		unlock()
	}
	return err == nil && !free
}

// edit changes the record the way a command of another package would.
func edit(t *testing.T, p *scenario.Project, fn func(*contract.State) error) {
	t.Helper()
	s, _ := p.Record()
	if err := p.Kit.Store.Change(p.Root, s.Run.ID, fn); err != nil {
		t.Fatal(err)
	}
}

func screen(p *scenario.Project, pane string) string {
	text, _ := p.Herdr.Screen(pane)
	return text
}

func kinds(s *contract.State) []string {
	var out []string
	for _, e := range s.Inbox.Events {
		out = append(out, e.Kind)
	}
	return out
}

// Every step of 8b, with real commands: a worker asks and blocks, the lead
// agent answers or hands the question to the person, the ask returns the
// answer, gives up with the resume command, is pointed at when it had
// stopped waiting, and ends with its attempt.
func TestEightB(t *testing.T) {
	p := evening(t, nil)

	// Steps 1 and 2: the question is recorded under the lock, the attempt is
	// asked, the lead agent has a question event, and the ask holds its lock.
	ask := begin(t, p, "T1.1", "ask", "Which base branch?", "--options", "main,release")
	until(t, "T1.1's ask waits", func() bool { return waiting(p, "q11") })
	s, _ := p.Record()
	if q := s.Questions["q11"]; q.For != contract.ForOrchestrator || q.Form != contract.FormChoice || q.Attempt != "T1.1" ||
		!slices.Equal(q.Options, []string{"main", "release"}) || s.Attempts["T1.1"].State != contract.AttemptAsked || !slices.Equal(kinds(s), []string{"question"}) {
		t.Fatalf("after ask: %+v, T1.1 %s, events %v", q, s.Attempts["T1.1"].State, kinds(s))
	}
	// No wait is running and the lead agent's tab is idle: it is pointed at the event (8e).
	until(t, "the lead agent is pointed at its event", func() bool { return strings.Contains(screen(p, p.Lead), "1 events waiting. Run: whaleshark wait") })
	must(t, p, 5, "T1.1", "ask Another?", "One at a time")

	// Steps 4 and 5: the lead agent knows. The blocked ask prints the answer
	// and the worker works again.
	must(t, p, 0, scenario.Orch, "answer q11 MAIN --json", `"told":"waiting"`, `"answer":"main"`)
	if out, exit := ask.end(t); exit != 0 || out != "main\n" {
		t.Fatalf("the ask ended %d with %q", exit, out)
	}
	s, _ = p.Record()
	if q := s.Questions["q11"]; q.State != contract.QuestionUsed || q.UsedAt.IsZero() || s.Attempts["T1.1"].State != contract.AttemptWorking {
		t.Fatalf("after the answer: %+v, T1.1 %s", q, s.Attempts["T1.1"].State)
	}

	// Step 4, the other way: only the person can say. Their answer goes
	// straight to the worker, and the lead agent is told by an answered event.
	ask = begin(t, p, "T11.1", "ask", "--resume", "q10")
	until(t, "T11.1's ask waits", func() bool { return waiting(p, "q10") })
	must(t, p, 0, scenario.Orch, "escalate q10", "for the person")
	must(t, p, 0, scenario.Human, "answer q10 the-shop-address --json", `"told":"waiting"`)
	if out, exit := ask.end(t); exit != 0 || out != "the-shop-address\n" {
		t.Fatalf("the escalated ask ended %d with %q", exit, out)
	}
	s, _ = p.Record()
	if e := s.Inbox.Events[len(s.Inbox.Events)-1]; e.Kind != "answered" || e.Data["id"] != "q10" || e.Text != "the-shop-address" ||
		s.Questions["q10"].AnsweredBy.Where != contract.WhereTyped {
		t.Fatalf("the lead agent was not told of the person's answer: %+v", e)
	}

	// Step 6: the time is up. Exit 75, the resume command, the question still
	// open and nothing asked twice; and the same when the time passes while it waits.
	must(t, p, 75, "T6.1", "ask --resume q9 --timeout 0", "q9 is still open", "Next: whaleshark ask --resume q9")
	ask = begin(t, p, "T6.1", "ask", "--resume", "q9")
	until(t, "T6.1's ask waits", func() bool { return waiting(p, "q9") })
	p.Clock(539 * time.Second)
	time.Sleep(3 * look / 2)
	if !waiting(p, "q9") {
		t.Fatal("the ask gave up before its 540 seconds")
	}
	p.Clock(time.Second)
	if out, exit := ask.end(t); exit != 75 || !strings.Contains(out, "whaleshark ask --resume q9") {
		t.Fatalf("the ask whose time passed ended %d with %q", exit, out)
	}
	if s, _ = p.Record(); s.Questions["q9"].State != contract.QuestionOpen || s.Counters.Question != 11 {
		t.Fatalf("after the time was up: %+v, %d questions", s.Questions["q9"], s.Counters.Question)
	}

	// Step 7: nobody is waiting. The worker's tab gets the pointer and never
	// the answer; the resumed ask returns it.
	pane := s.Attempts["T6.1"].Place.Pane
	must(t, p, 0, scenario.Human, "answer q9 800-and-1600 --json", `"told":"pointer"`)
	if shown := screen(p, pane); !strings.Contains(shown, "Your question q9 was answered. Run: whaleshark ask --resume q9") || strings.Contains(shown, "800") {
		t.Fatalf("T6.1's tab shows %q", shown)
	}
	must(t, p, 0, "T6.1", "ask --resume q9", "800-and-1600")
	must(t, p, 4, "T6.1", "ask --resume q7", "no question q7")

	// Step 8: the attempt ends while its ask is blocked. The ask ends with
	// exit 5, and an answer that comes afterwards is kept for the next attempt.
	ask = begin(t, p, "T2.1", "ask", "--resume", "q7")
	until(t, "T2.1's ask waits", func() bool { return waiting(p, "q7") })
	edit(t, p, func(s *contract.State) error { return rules.Rules{}.Stop(s, "T2", true, contract.Now()) })
	if out, exit := ask.end(t); exit != 5 || !strings.Contains(out, "Your attempt has ended.") {
		t.Fatalf("the ask of a stopped attempt ended %d with %q", exit, out)
	}
	must(t, p, 0, scenario.Human, "answer q7 yes", "T2's attempt has ended; your answer will be in the next attempt's prompt.")
	s, _ = p.Record()
	if d := s.Tasks["T2"].Decisions; len(d) != 1 || d[0].Answer != "yes" || s.Questions["q7"].State != contract.QuestionClosed {
		t.Fatalf("the late answer was not kept: %+v", d)
	}
	must(t, p, 5, scenario.Human, "answer q7 --undo", "Already acted on")

	// Nothing but the fixed pointers was ever typed, and no key was sent.
	for _, call := range p.Herdr.Calls() {
		if slices.Contains(call, "send-keys") || len(call) > 3 && call[1] == "prompt" && !strings.Contains(call[3], "Run: whaleshark") {
			t.Errorf("herdr was called with %v", call)
		}
	}
}

// Each form answered rightly and wrongly; the record says who answered and
// that the lead agent's answer was passed on.
func TestForms(t *testing.T) {
	p := scenario.Run(t, filepath.Join("testdata", "forms.scn"), nil)
	s, _ := p.Record()
	relayed := contract.Origin{Caller: contract.Orchestrator, Where: contract.WhereRelayed}
	if by := s.Questions["n10"].AnsweredBy; by == nil || *by != relayed || *s.Questions["n9"].AnsweredBy != (contract.Origin{Caller: contract.Human, Where: contract.WhereTyped}) {
		t.Errorf("n10 was answered by %+v and n9 by %+v", by, s.Questions["n9"].AnsweredBy)
	}
}

// An answer undone before it was used and after; nothing a person did is
// dropped from the record.
func TestUndo(t *testing.T) {
	p := scenario.Run(t, filepath.Join("testdata", "undo.scn"), nil)
	s, _ := p.Record()
	q := s.Questions["q7"]
	if e := q.Earlier; len(e) != 1 || e[0].Answer != "no" || e[0].By.Where != contract.WhereTyped || q.AnsweredBy.Where != contract.WherePage || !q.SettlesAt.Equal(q.AnsweredAt.Add(4*time.Second)) {
		t.Errorf("q7 keeps %+v, answered by %+v, settling %v after", e, q.AnsweredBy, q.SettlesAt.Sub(q.AnsweredAt))
	}
	if e := s.Questions["n4"].Earlier; len(e) != 1 || e[0].Answer != "approve" {
		t.Errorf("n4 keeps %+v", e)
	}
}

// Two answers at once: of different ones exactly one stands and every other
// is refused; the same answer twice is one answer and no error.
func TestTwoAnswersAtOnce(t *testing.T) {
	p := evening(t, nil)
	both := func(a, b string) (exits [2]int) {
		var wg sync.WaitGroup
		for i, text := range []string{a, b} {
			wg.Go(func() { _, exits[i] = run(p, scenario.Orch, "answer", "q10", text) })
		}
		wg.Wait()
		return exits
	}
	for round := range 8 {
		exits := both("first", "second")
		s, _ := p.Record()
		q := s.Questions["q10"]
		won := slices.Index(exits[:], 0)
		if exits != [2]int{0, 5} && exits != [2]int{5, 0} || q.Answer != []string{"first", "second"}[won] || len(q.Earlier) != 0 {
			t.Fatalf("round %d: the two ended %v and the record says %q", round, exits, q.Answer)
		}
		if exits = both(q.Answer, q.Answer); exits != [2]int{} {
			t.Fatalf("round %d: the same answer twice ended %v", round, exits)
		}
		edit(t, p, func(s *contract.State) error {
			*s.Questions["q10"] = contract.Question{ID: "q10", Kind: contract.KindAsk, Form: contract.FormQuestion,
				For: contract.ForOrchestrator, From: "T11.1", Task: "T11", Attempt: "T11.1", Text: "Which sender address?", State: contract.QuestionOpen}
			return nil
		})
	}
}

// An undo racing an ask: both run under the lock, so either the undo finds
// the answer used and is refused, or the ask finds the item open and keeps
// waiting. Never both, never neither.
func TestUndoRacesAsk(t *testing.T) {
	p := evening(t, nil)
	var undone, used int
	for round := range 12 {
		must(t, p, 0, scenario.Human, "answer q7 yes")
		var wg sync.WaitGroup
		var out [2]string
		var exits [2]int
		wg.Go(func() { out[0], exits[0] = run(p, "T2.1", "ask", "--resume", "q7", "--timeout", "0") })
		wg.Go(func() {
			// The undo sets out a little later each round, so that both orders are met.
			time.Sleep(time.Duration(round) * time.Millisecond)
			out[1], exits[1] = run(p, scenario.Human, "answer", "q7", "--undo")
		})
		wg.Wait()
		s, _ := p.Record()
		q, a := s.Questions["q7"], s.Attempts["T2.1"]
		switch {
		case exits == [2]int{0, 5} && out[0] == "yes\n" && q.State == contract.QuestionUsed && a.State == contract.AttemptWorking && len(q.Earlier) == undone:
			used++
		case exits == [2]int{75, 0} && q.State == contract.QuestionOpen && q.Answer == "" && a.State == contract.AttemptAsked && len(q.Earlier) == undone+1:
			undone++
		default:
			t.Fatalf("round %d: ask ended %d %q, undo %d %q, and the record says %s, T2.1 %s, %d undone", round, exits[0], out[0], exits[1], out[1], q.State, a.State, len(q.Earlier))
		}
		// The next round starts from an open question either way.
		edit(t, p, func(s *contract.State) error {
			q, a := s.Questions["q7"], s.Attempts["T2.1"]
			q.State, q.Answer, q.UsedAt, a.State = contract.QuestionOpen, "", time.Time{}, contract.AttemptAsked
			return nil
		})
	}
	t.Logf("the ask won %d times and the undo %d times", used, undone)
}

// While all work is paused nothing is used and nobody is woken: an answer is
// kept, no pointer is typed, a blocked ask stays blocked, and escalate and
// need wait. After the pause the ask returns the answer.
func TestPaused(t *testing.T) {
	p := evening(t, func(s *contract.State) {
		s.Run.Paused = &contract.Pause{At: s.Run.CreatedAt, By: "typed"}
		s.Attempts["T1.1"].Gated = false
	})
	ask := begin(t, p, "T11.1", "ask", "--resume", "q10")
	until(t, "T11.1's ask waits", func() bool { return waiting(p, "q10") })
	must(t, p, 0, scenario.Orch, "answer q10 the-shop-address")
	must(t, p, 0, scenario.Human, "answer q9 800-and-1600 --json", `"state":"answered"`)
	must(t, p, 0, scenario.Human, "answer n4 approve")
	must(t, p, 5, scenario.Orch, "escalate q10", "paused")
	must(t, p, 5, scenario.Orch, "need todo Look", "paused")
	must(t, p, 0, scenario.Orch, "need close n4")
	must(t, p, 75, "T6.1", "ask --resume q9 --timeout 0")
	// A worker with no gate is told to stop, and its question is recorded all the same.
	out, exit := run(p, "T1.1", "ask", "Which base branch?", "--timeout", "0")
	if exit != 75 || !strings.Contains(out, contract.PausedReply) {
		t.Fatalf("an ask during the pause ended %d with %q", exit, out)
	}
	time.Sleep(3 * look / 2)
	s, _ := p.Record()
	if !waiting(p, "q10") || s.Questions["q10"].State != contract.QuestionAnswered || s.Questions["q9"].State != contract.QuestionAnswered {
		t.Fatalf("during the pause: q10 %s, q9 %s, the ask waiting: %v", s.Questions["q10"].State, s.Questions["q9"].State, waiting(p, "q10"))
	}
	for _, call := range p.Herdr.Calls() {
		if slices.Contains(call, "prompt") {
			t.Errorf("a pointer was typed during the pause: %v", call)
		}
	}
	must(t, p, 0, scenario.Human, "answer q9 --undo")

	edit(t, p, func(s *contract.State) error { return rules.Rules{}.Resume(s, contract.Now()) })
	if out, exit := ask.end(t); exit != 0 || out != "the-shop-address\n" {
		t.Fatalf("after the pause the ask ended %d with %q", exit, out)
	}
}

// The lead agent is pointed at an answer to an item of its own only when no
// wait is running in its tab and the tab is idle; and at the event that says
// the person answered a question in its place.
func TestLeadIsPointedAt(t *testing.T) {
	p := evening(t, nil)
	_, dir := p.Record()
	typed := func() int { return strings.Count(screen(p, p.Lead), "Run: whaleshark wait") }

	unlock, free, err := p.Kit.Platform.TryLock(filepath.Join(dir, "wait.lock"))
	if err != nil || !free {
		t.Fatal(free, err)
	}
	must(t, p, 0, scenario.Human, "answer n4 approve")
	unlock()
	if typed() != 0 {
		t.Fatalf("a pointer was typed while a wait was running:\n%s", screen(p, p.Lead))
	}
	must(t, p, 0, scenario.Human, "answer n4 --undo")
	p.Herdr.Drop(contract.StatusWorking, p.Lead)
	must(t, p, 0, scenario.Human, "answer n4 approve")
	if typed() != 0 {
		t.Fatalf("a pointer was typed into a working lead agent's tab:\n%s", screen(p, p.Lead))
	}
	must(t, p, 0, scenario.Human, "answer n4 --undo")
	p.Herdr.Drop(contract.StatusDone, p.Lead)
	must(t, p, 0, scenario.Human, "answer n4 approve")
	if !strings.Contains(screen(p, p.Lead), string(contract.PointAnswer)) {
		t.Fatalf("the lead agent was not pointed at the answer:\n%s", screen(p, p.Lead))
	}

	// q10 was the lead agent's to answer, and the person did: a human event
	// that says so, without the answer.
	p.Herdr.Drop(contract.StatusIdle, p.Lead)
	must(t, p, 0, scenario.Human, "answer q10 the-shop-address")
	s, _ := p.Record()
	if e := s.Inbox.Events; len(e) != 1 || e[0].Kind != "human" || e[0].Data["id"] != "q10" || e[0].Data["where"] != contract.WhereTyped || strings.Contains(e[0].Text, "shop") {
		t.Fatalf("the events are %+v", e)
	}
	if !strings.Contains(screen(p, p.Lead), "1 events waiting") {
		t.Fatalf("the lead agent was not pointed at the event:\n%s", screen(p, p.Lead))
	}
}

// How long a button's answer settles is the person's own setting; a typed
// line settles at once wherever it is typed.
func TestSettleSeconds(t *testing.T) {
	p := evening(t, nil)
	dirs, err := p.Kit.Platform.Dirs()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirs.Config, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirs.Config, "config.toml"), []byte("[ui]\nsettle_seconds = 9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	must(t, p, 0, scenario.Page, "answer n4 approve", "taken back for 9s")
	must(t, p, 0, scenario.Page, "answer q7 other:-ask-me-on-Monday")
	must(t, p, 0, scenario.Page, "answer q9 800-and-1600")
	must(t, p, 0, scenario.Orch, "answer q10 the-shop-address")
	s, _ := p.Record()
	for id, want := range map[string]time.Duration{"n4": 9 * time.Second, "q7": 0, "q9": 0, "q10": 0} {
		if q := s.Questions[id]; q.SettlesAt.Sub(q.AnsweredAt) != want {
			t.Errorf("%s settles %v after its answer, expected %v", id, q.SettlesAt.Sub(q.AnsweredAt), want)
		}
	}
}

// counting is the store with its reads counted.
type counting struct {
	contract.Store
	reads atomic.Int32
}

func (c *counting) Read(root, run string) (*contract.State, error) {
	c.reads.Add(1)
	return c.Store.Read(root, run)
}

// writer is a place to print to from another goroutine.
type writer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *writer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// A blocked ask reads the record only when the file has changed, however
// often it looks, and says that it is alive while it waits.
func TestAskReadsOnlyWhatChanged(t *testing.T) {
	p := evening(t, nil)
	was, beat := look, keepalive
	t.Cleanup(func() { look, keepalive = was, beat })
	look, keepalive = 2*time.Millisecond, 20*time.Millisecond
	k := contract.NewKit()
	for _, plug := range []func(*contract.Kit){platform.Plug, rules.Plug, store.Plug, cli.Plug, Plug} {
		plug(k)
	}
	counted := &counting{Store: k.Store}
	k.Store, k.Terms = counted, p.Herdr
	t.Setenv(contract.EnvRoot, p.Root)
	t.Setenv(contract.EnvRun, "r3")
	t.Setenv(contract.EnvAttempt, "T11.1")
	t.Setenv(contract.EnvPane, "w1:p12")

	var out, errw writer
	exit := make(chan int)
	go func() { exit <- k.Main(k, []string{"ask", "--resume", "q10"}, strings.NewReader(""), &out, &errw) }()
	until(t, "the ask waits", func() bool { return waiting(p, "q10") })
	until(t, "the ask says that it is alive", func() bool { return strings.Count(errw.String(), "still waiting for the answer to q10") >= 2 })
	before := counted.reads.Load()
	time.Sleep(100 * look)
	if got := counted.reads.Load(); got != before {
		t.Fatalf("the ask read the record %d times while nothing changed", got-before)
	}
	edit(t, p, func(s *contract.State) error { return rules.Rules{}.Progress(s, "T1.1", 50, "half", contract.Now()) })
	until(t, "the ask reads the changed record", func() bool { return counted.reads.Load() == before+1 })
	time.Sleep(100 * look)
	if got := counted.reads.Load(); got != before+1 {
		t.Fatalf("the ask read the record %d times for one change", got-before)
	}
	must(t, p, 0, scenario.Orch, "answer q10 the-shop-address")
	if got := <-exit; got != 0 || out.String() != "the-shop-address\n" {
		t.Fatalf("the ask ended %d with %q", got, out.String())
	}
}
