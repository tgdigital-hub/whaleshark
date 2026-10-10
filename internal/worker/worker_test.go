package worker_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func TestMain(m *testing.M) { scenario.Main(m) }

// evening is the 21:14 fixture: T1.1 works behind a gate, T10.1 without one.
func evening(t *testing.T) *testkit.Fixture {
	t.Helper()
	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// as runs the real program from an attempt's tab and wants this exit code
// and each of these words in what it printed.
func as(t *testing.T, p *scenario.Project, attempt string, exit int, want string, args ...string) string {
	t.Helper()
	out, err := p.Command(attempt, args...).CombinedOutput()
	var ended *exec.ExitError
	got := 0
	if errors.As(err, &ended) {
		got = ended.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	if got != exit {
		t.Fatalf("%s: %v: exit %d, want %d\n%s", attempt, args, got, exit, out)
	}
	for _, word := range strings.Fields(want) {
		if !strings.Contains(string(out), word) {
			t.Fatalf("%s: %v: no %q in what it printed:\n%s", attempt, args, word, out)
		}
	}
	return string(out)
}

// attemptFile writes one of an attempt's own files.
func attemptFile(t *testing.T, p *scenario.Project, attempt, name, text string) string {
	t.Helper()
	_, dir := p.Record()
	path := filepath.Join(dir, "attempts", attempt, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// kept counts the refused reports of an attempt that the record holds.
func kept(s *contract.State, attempt string) (n int) {
	for _, e := range s.Inbox.Events {
		if e.Kind == "rejected" && e.Attempt == attempt {
			n++
		}
	}
	return n
}

func is(t *testing.T, p *scenario.Project, attempt string, state contract.AttemptState, task contract.TaskStatus, refused int) *contract.State {
	t.Helper()
	s, _ := p.Record()
	a := s.Attempts[attempt]
	if got := s.Tasks[a.Task].Status; a.State != state || got != task || kept(s, attempt) != refused {
		t.Fatalf("%s is %s, its task %s, with %d refused reports kept; want %s, %s, %d",
			attempt, a.State, got, kept(s, attempt), state, task, refused)
	}
	return s
}

// The fake agent writes another token into the attempt's file and reports:
// the report is refused and kept, and the attempt goes on as it was.
func TestWrongToken(t *testing.T) {
	p := scenario.Prepare(t, evening(t), map[string]string{"T1.1": "write-result | report-with-token not-the-token"})
	for end := time.Now().Add(20 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if s, _ := p.Record(); kept(s, "T1.1") == 1 {
			break
		}
		if time.Now().After(end) {
			t.Fatal("the report with another token was not kept as a rejected event")
		}
	}
	s := is(t, p, "T1.1", contract.AttemptWorking, contract.TaskRunning, 1)
	if e := s.Inbox.Events[len(s.Inbox.Events)-1]; !strings.Contains(e.Text, "token") || e.Data["outcome"] != contract.ReportDone {
		t.Fatalf("the kept event is %+v", e)
	}
	before := *s.Attempts["T1.1"].Progress
	as(t, p, "T1.1", contract.ExitRefused, "token Stop", "progress", "99", "nearly")
	as(t, p, "T1.1", contract.ExitRefused, "bad_token", "report", "failed", "gave up", "--json")
	if s = is(t, p, "T1.1", contract.AttemptWorking, contract.TaskRunning, 2); *s.Attempts["T1.1"].Progress != before {
		t.Fatal("a progress note with the wrong token was recorded")
	}

	// No token file at all is no identity either.
	_, dir := p.Record()
	if err := os.Remove(filepath.Join(dir, "attempts", "T4.1", "token")); err != nil {
		t.Fatal(err)
	}
	as(t, p, "T4.1", contract.ExitRefused, "token", "report", "failed", "no token")
	as(t, p, "T4.1", contract.ExitRefused, "token", "progress", "10", "no token")
	is(t, p, "T4.1", contract.AttemptWorking, contract.TaskRunning, 1)
}

// A report from the attempt before a retry cannot finish the new one.
func TestOldAttempt(t *testing.T) {
	f := evening(t)
	old, task := f.State.Attempts["T3.1"], f.State.Tasks["T3"]
	retry := *old
	retry.ID, retry.N, retry.RetryOf = "T3.2", 2, old.ID
	f.State.Attempts[retry.ID], task.Attempts = &retry, append(task.Attempts, retry.ID)
	old.State, old.EndedAt, old.Exit = contract.AttemptStopped, f.Now, &contract.Exit{Cause: contract.ExitStopped, At: f.Now}
	old.TokenHash = contract.TokenHash("token-of-T3.1")
	p := scenario.Prepare(t, f, nil)
	attemptFile(t, p, "T3.1", "token", "token-of-T3.1")
	attemptFile(t, p, "T3.1", "result.md", "late")

	as(t, p, "T3.1", contract.ExitRefused, "no longer active; stop", "report", "done", "late")
	as(t, p, "T3.1", contract.ExitRefused, "no longer active; stop", "progress", "90", "late")
	is(t, p, "T3.1", contract.AttemptStopped, contract.TaskRunning, 1)
	is(t, p, "T3.2", contract.AttemptWorking, contract.TaskRunning, 0)
}

func TestDuplicateReport(t *testing.T) {
	p := scenario.Prepare(t, evening(t), nil)
	attemptFile(t, p, "T1.1", "result.md", "The login page works.")
	if out := as(t, p, "T1.1", 0, "", "report", "done", "login works"); out != "recorded\n" {
		t.Fatalf("the first report printed %q", out)
	}
	s := is(t, p, "T1.1", contract.AttemptReported, contract.TaskReview, 0)
	seq := s.Counters.Seq
	if e := s.Inbox.Events[len(s.Inbox.Events)-1]; e.Kind != "done" || e.Task != "T1" || e.Text != "login works" {
		t.Fatalf("the report raised %+v", e)
	}
	if out := as(t, p, "T1.1", 0, "", "report", "done", "login works, again"); out != "already recorded\n" {
		t.Fatalf("the second report printed %q", out)
	}
	as(t, p, "T1.1", 0, `"already":true`, "report", "done", "once more", "--json")
	if s = is(t, p, "T1.1", contract.AttemptReported, contract.TaskReview, 0); s.Counters.Seq != seq || s.Attempts["T1.1"].Report.Summary != "login works" {
		t.Fatal("a repeated report changed the record")
	}
	// The other outcome after the first is a refused report, and it is kept.
	as(t, p, "T1.1", contract.ExitRefused, "no longer active; stop", "report", "failed", "changed my mind")
	as(t, p, "T1.1", contract.ExitRefused, "no longer active", "progress", "100", "done")
	is(t, p, "T1.1", contract.AttemptReported, contract.TaskReview, 1)
}

func TestMissingResult(t *testing.T) {
	p := scenario.Prepare(t, evening(t), nil)
	_, dir := p.Record()
	before, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	as(t, p, "T1.1", contract.ExitFailed, "result file result.md", "report", "done", "no result written")
	as(t, p, "T1.1", contract.ExitFailed, "no_result", "report", "done", "no result written", "--json")
	if after, _ := os.ReadFile(filepath.Join(dir, "state.json")); string(after) != string(before) {
		t.Fatal("a report without its result file changed the record")
	}
	// Once the file is there the same report passes; a failure needs none.
	attemptFile(t, p, "T1.1", "result.md", "The login page works.")
	as(t, p, "T1.1", 0, "recorded", "report", "done", "result written")
	is(t, p, "T1.1", contract.AttemptReported, contract.TaskReview, 0)
	as(t, p, "T9.1", 0, "recorded", "report", "failed", "the site map cannot be built")
	is(t, p, "T9.1", contract.AttemptFailed, contract.TaskReady, 0)
}

// While the person has paused all work a worker's commands are recorded as
// usual, and the worker whose agent has no gate is told to stop.
func TestPaused(t *testing.T) {
	f := evening(t)
	f.State.Run.Paused = &contract.Pause{At: f.Now, By: string(contract.Human)}
	p := scenario.Prepare(t, f, nil)
	const line = "paused: stop now and wait to be told\n"

	if out := as(t, p, "T10.1", 0, "", "progress", "40", "form drawn"); out != line {
		t.Fatalf("progress without a gate printed %q", out)
	}
	if out := as(t, p, "T1.1", 0, "", "progress", "40", "page drawn"); out != "" {
		t.Fatalf("progress behind a gate printed %q", out)
	}
	as(t, p, "T10.1", 0, `"paused":true`, "progress", "45", "form wired", "--json")
	s, _ := p.Record()
	if got := s.Attempts["T10.1"].Progress; got.Pct != 45 || got.Note != "form wired" || !got.At.Equal(f.Now) {
		t.Fatalf("the progress recorded is %+v", got)
	}
	attemptFile(t, p, "T10.1", "result.md", "The form works.")
	if out := as(t, p, "T10.1", 0, "", "report", "done", "form works"); out != "recorded\n"+line {
		t.Fatalf("a report without a gate printed %q", out)
	}
	is(t, p, "T10.1", contract.AttemptReported, contract.TaskReview, 0)
}

// A usage error is exit 2 and touches nothing.
func TestUsage(t *testing.T) {
	p := scenario.Prepare(t, evening(t), nil)
	for _, args := range [][]string{
		{"report"}, {"report", "done"}, {"report", "maybe", "x"}, {"report", "done", "x", "y"},
		{"progress", "40"}, {"progress", "forty", "x"}, {"progress", "101", "x"}, {"progress", "-1", "x"},
	} {
		as(t, p, "T1.1", contract.ExitUsage, "", args...)
	}
	is(t, p, "T1.1", contract.AttemptWorking, contract.TaskRunning, 0)
}

func TestRestoredTab(t *testing.T) {
	p := scenario.Run(t, filepath.Join("testdata", "restored.scn"), nil)
	if s, _ := p.Record(); s.Attempts["T1.1"].Progress.Note != "after the restart" {
		t.Fatalf("the progress recorded is %+v", s.Attempts["T1.1"].Progress)
	}
}
