package mail_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func TestMain(m *testing.M) { scenario.Main(m) }

// evening is the fixture's run with what one test changes in it.
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

type answer struct {
	OK     bool
	Result struct {
		Seq      int
		Task     string
		Attempt  string
		Body     string
		Status   string
		Pointer  string
		Why      string
		Paused   bool
		Messages []contract.Mail
	}
	Error contract.Refusal
}

// run is one real command with --json, its answer and its exit code.
func run(t *testing.T, p *scenario.Project, who string, args ...string) (answer, int) {
	t.Helper()
	cmd := p.Command(who, append(args, "--json")...)
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	err := cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatal(err)
	}
	var a answer
	if err := json.Unmarshal(out.Bytes(), &a); err != nil {
		t.Fatalf("%v: %v\n%s%s", args, err, out.String(), errs.String())
	}
	return a, cmd.ProcessState.ExitCode()
}

// pointers is every line typed into a pane so far, as "pane: text".
func pointers(p *scenario.Project) []string {
	var out []string
	for _, call := range p.Herdr.Calls() {
		if len(call) >= 4 && call[0] == "agent" && call[1] == "prompt" {
			out = append(out, call[2]+": "+call[3])
		}
	}
	return out
}

func want(t *testing.T, what string, got, wanted any) {
	t.Helper()
	if got != wanted {
		t.Fatalf("%s: got %v, want %v", what, got, wanted)
	}
}

// A message to a working agent is stored and its pointer waits; once the
// agent is idle the next one is typed, once, and the worker's mail command
// hands over both and marks them read.
func TestTellDeferredThenDelivered(t *testing.T) {
	p := evening(t, nil)
	a, exit := run(t, p, scenario.Orch, "tell", "search box", "use the v2 endpoint")
	want(t, "exit", exit, 0)
	want(t, "pointer", a.Result.Pointer, "deferred")
	want(t, "task", a.Result.Task+" "+a.Result.Attempt, "T3 T3.1")
	want(t, "number", a.Result.Seq, 58)
	want(t, "typed lines", len(pointers(p)), 0)
	s, dir := p.Record()
	m := s.Attempts["T3.1"].Mail
	if len(m) != 1 || m[0].Text != "use the v2 endpoint" || m[0].Body != "" || !m[0].NudgedAt.IsZero() || !m[0].ReadAt.IsZero() {
		t.Fatalf("stored: %+v", m)
	}

	// The sweep sees the agent idle; this wave has no sweep, so the test plays it.
	if err := p.Kit.Store.Change(p.Root, s.Run.ID, func(s *contract.State) error {
		s.Attempts["T3.1"].Seen.Status = contract.StatusIdle
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	long := filepath.Join(t.TempDir(), "notes.md")
	os.WriteFile(long, []byte("  three things\n1. one\n2. two\n\n"), 0o600)
	a, _ = run(t, p, scenario.Orch, "tell", "T3", "--file", long)
	want(t, "pointer", a.Result.Pointer, "typed")
	want(t, "body", a.Result.Body, filepath.Join(dir, "msg", "59.md"))
	want(t, "typed lines", strings.Join(pointers(p), "|"), "w1:p4: "+string(contract.PointMail))
	body, err := os.ReadFile(a.Result.Body)
	want(t, "body file", string(body), "three things\n1. one\n2. two\n")
	if info, _ := os.Stat(a.Result.Body); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("body file: %v, %v", err, info.Mode())
	}
	s, _ = p.Record()
	if m = s.Attempts["T3.1"].Mail; len(m) != 2 || m[1].Text != "three things" || m[1].NudgedAt.IsZero() {
		t.Fatalf("stored: %+v", m)
	}

	a, exit = run(t, p, "T3.1", "mail")
	want(t, "exit", exit, 0)
	if m = a.Result.Messages; len(m) != 2 || m[0].Seq != 58 || m[1].Body != filepath.Join(dir, "msg", "59.md") {
		t.Fatalf("handed over: %+v", m)
	}
	text, _ := p.Command("T3.1", "mail").CombinedOutput()
	want(t, "second read", strings.TrimSpace(string(text)), "No new messages.")
	s, _ = p.Record()
	for _, m := range s.Attempts["T3.1"].Mail {
		if m.ReadAt.IsZero() {
			t.Fatalf("not marked read: %+v", m)
		}
	}
	want(t, "numbers given out", s.Counters.Seq, 59)
}

// Each row of the table of 6.4 and the two flags.
func TestTellPointer(t *testing.T) {
	p := evening(t, func(s *contract.State) {
		s.Attempts["T5.1"].State = contract.AttemptStarting
		s.Attempts["T5.1"].Seen.Status = contract.StatusIdle
	})
	for _, c := range []struct {
		args         []string
		pointer, why string
	}{
		{[]string{"tell", "T3", "x", "--now"}, "typed", ""},
		{[]string{"tell", "T12", "x", "--quiet"}, "skipped", "--quiet"},
		{[]string{"tell", "T12", "x"}, "typed", ""},
		{[]string{"tell", "T7", "x"}, "skipped", "sent back"},
		{[]string{"tell", "T5", "x", "--now"}, "deferred", "started"},
	} {
		a, exit := run(t, p, scenario.Orch, c.args...)
		want(t, c.args[1]+" exit", exit, 0)
		want(t, c.args[1]+" pointer", a.Result.Pointer, c.pointer)
		if !strings.Contains(a.Result.Why, c.why) {
			t.Fatalf("%v: why %q", c.args, a.Result.Why)
		}
	}
	want(t, "typed lines", len(pointers(p)), 2)
	// A quiet message is stamped, so that no sweep types its pointer later.
	if s, _ := p.Record(); s.Attempts["T12.1"].Mail[0].NudgedAt.IsZero() {
		t.Fatal("a quiet message is left for the sweep to point at")
	}

	// An agent at a prompt of its own is typed into by nobody.
	p.Herdr.Push(contract.StatusBlocked, "w1:p13")
	a, _ := run(t, p, scenario.Orch, "tell", "T12", "y")
	want(t, "pointer", a.Result.Pointer+": "+a.Result.Why, "deferred: the agent is at a prompt of its own")

	for _, c := range []struct {
		who, code string
		exit      int
		args      []string
	}{
		{scenario.Orch, "no_live_attempt", 5, []string{"tell", "T13", "x"}},
		{scenario.Orch, "no_such_task", 4, []string{"tell", "T99", "x"}},
		{scenario.Orch, "usage", 2, []string{"tell", "T3"}},
		{scenario.Orch, "usage", 2, []string{"tell", "T3", " "}},
		{scenario.Orch, "human_only", 5, []string{"tell", "lead", "x"}},
		{"T3.1", "not_for_worker", 5, []string{"tell", "T4", "x"}},
		{scenario.Orch, "worker_only", 5, []string{"mail"}},
	} {
		a, exit := run(t, p, c.who, c.args...)
		want(t, strings.Join(c.args, " "), a.Error.Code+" "+strconv.Itoa(exit), c.code+" "+strconv.Itoa(c.exit))
	}
	s, dir := p.Record()
	want(t, "numbers given out", s.Counters.Seq, 57+6)
	if _, err := os.Stat(filepath.Join(dir, "msg")); err == nil {
		t.Fatal("a one-line message made a file")
	}
}

// The person's note reaches the lead agent as an event. The pointer is typed
// only when no wait of its is running and its pane is idle, and the note
// itself is never typed.
func TestTellLead(t *testing.T) {
	p := evening(t, nil)
	a, exit := run(t, p, scenario.Human, "tell", "lead", "stop work on the search\nI changed my mind")
	want(t, "exit", exit, 0)
	want(t, "pointer", a.Result.Pointer, "typed")
	want(t, "typed lines", strings.Join(pointers(p), "|"), "w1:p1: whaleshark: 1 events waiting. Run: whaleshark wait")
	s, dir := p.Record()
	e := s.Inbox.Events
	if len(e) != 1 || e[0].Kind != "human" || e[0].Text != "stop work on the search" || e[0].Data["body"] != filepath.Join(dir, "msg", "58.md") {
		t.Fatalf("event: %+v", e)
	}

	// The fake left the lead agent working on the pointer.
	a, _ = run(t, p, scenario.Page, "tell", "lead", "one more thing")
	want(t, "pointer", a.Result.Pointer+": "+a.Result.Why, "deferred: until it is idle")

	unlock, ok, err := p.Kit.Platform.TryLock(filepath.Join(dir, "wait.lock"))
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	defer unlock()
	a, _ = run(t, p, scenario.Human, "tell", "lead", "and another", "--now")
	want(t, "pointer", a.Result.Pointer+": "+a.Result.Why, "skipped: its wait hands the note over")
	want(t, "typed lines", len(pointers(p)), 1)
}

// The three outcomes of reject: the agent is there, is proven gone, or the
// last sweep could not say.
func TestReject(t *testing.T) {
	seen := func(l contract.Liveness) func(*contract.State) {
		return func(s *contract.State) { s.Attempts["T7.1"].Seen.Liveness = l }
	}

	t.Run("live", func(t *testing.T) {
		p := evening(t, seen(contract.Live))
		a, exit := run(t, p, scenario.Orch, "reject", "email sender", "tests for the bounce case are missing")
		want(t, "exit", exit, 0)
		want(t, "answer", a.Result.Task+" "+a.Result.Attempt+" "+a.Result.Status+" "+a.Result.Pointer, "T7 T7.1 running typed")
		want(t, "typed lines", strings.Join(pointers(p), "|"), "w1:p8: "+string(contract.PointMail))
		s, _ := p.Record()
		at := s.Attempts["T7.1"]
		want(t, "attempt", at.State, contract.AttemptWorking)
		want(t, "round", at.Round, 1)
		want(t, "failures", s.Tasks["T7"].Failures, 0)
		if m := at.Mail; len(m) != 1 || !strings.Contains(m[0].Text, "sent back: tests for the bounce case are missing") || !strings.Contains(m[0].Text, "report again") || m[0].NudgedAt.IsZero() {
			t.Fatalf("mail: %+v", m)
		}
		a, _ = run(t, p, "T7.1", "mail")
		want(t, "handed over", len(a.Result.Messages), 1)
		a, exit = run(t, p, scenario.Orch, "reject", "T7", "again")
		want(t, "second reject", a.Error.Code+" "+strconv.Itoa(exit), "not_in_review 5")
	})

	t.Run("gone", func(t *testing.T) {
		p := evening(t, seen(contract.Gone))
		a, exit := run(t, p, scenario.Human, "reject", "T7", "tests for the bounce case are missing")
		want(t, "exit", exit, 0)
		want(t, "answer", a.Result.Status+" "+a.Result.Pointer, "ready ")
		want(t, "typed lines", len(pointers(p)), 0)
		s, _ := p.Record()
		task := s.Tasks["T7"]
		want(t, "attempt", s.Attempts["T7.1"].State, contract.AttemptStopped)
		want(t, "failures", task.Failures, 0)
		if d := task.Decisions; len(d) != 1 || d[0].Answer != "tests for the bounce case are missing" {
			t.Fatalf("kept for the next prompt: %+v", d)
		}
	})

	t.Run("unverifiable", func(t *testing.T) {
		p := evening(t, seen(contract.Unverifiable))
		before, _ := p.Record()
		a, exit := run(t, p, scenario.Orch, "reject", "T7", "tests for the bounce case are missing")
		want(t, "exit", exit, 5)
		want(t, "code", a.Error.Code, "unverifiable")
		want(t, "next", strings.Join(a.Error.Next, "|"), "whaleshark show T7 --screen")
		want(t, "typed lines", len(pointers(p)), 0)
		s, _ := p.Record()
		want(t, "task", s.Tasks["T7"].Status, contract.TaskReview)
		want(t, "attempt", s.Attempts["T7.1"].State, contract.AttemptReported)
		want(t, "numbers given out", s.Counters.Seq, before.Counters.Seq)
	})
}

// A worker's mail command proves itself with the token, and while the person
// has paused all work it says so and keeps the messages.
func TestMailTokenAndPause(t *testing.T) {
	p := evening(t, func(s *contract.State) {
		s.Attempts["T3.1"].Mail = []contract.Mail{{Seq: 40, Text: "carry on"}}
		s.Run.Paused = &contract.Pause{By: "you"}
	})
	a, exit := run(t, p, "T3.1", "mail")
	want(t, "exit", exit, 0)
	want(t, "paused", a.Result.Paused, true)
	want(t, "handed over", len(a.Result.Messages), 0)
	text, _ := p.Command("T3.1", "mail").CombinedOutput()
	want(t, "words", strings.TrimSpace(string(text)), contract.PausedReply)
	s, dir := p.Record()
	want(t, "still unread", s.Attempts["T3.1"].Mail[0].ReadAt.IsZero(), true)
	a, exit = run(t, p, scenario.Orch, "tell", "T3", "x")
	want(t, "tell while paused", a.Error.Code+" "+strconv.Itoa(exit), "paused 5")

	os.WriteFile(filepath.Join(dir, "attempts", "T3.1", "token"), []byte("another\n"), 0o600)
	a, exit = run(t, p, "T3.1", "mail")
	want(t, "wrong token", a.Error.Code+" "+strconv.Itoa(exit), "bad_token 5")
}
