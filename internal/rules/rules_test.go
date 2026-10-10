package rules

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

var (
	r      Rules
	t0     = time.Date(2026, 10, 8, 20, 0, 0, 0, time.UTC)
	limits = contract.ProjectDefaults()
	human  = contract.Origin{Caller: contract.Human, Where: contract.WherePane}
	orch   = contract.Origin{Caller: contract.Orchestrator}
	agent  = contract.Agent{Kind: "claude", Name: "h0000-r1-t1-1"}
)

const token = "token-hash"

func after(d time.Duration) time.Time { return t0.Add(d) }

func newRun() *contract.State {
	return r.NewRun("r1", "a test run", 3, nil, contract.Binding{Pane: "w1:p1", Workspace: "w1"}, t0)
}

// code is the code of a refusal, "" for no error, and "?" for any other error.
func code(err error) string {
	var ref *contract.Refusal
	switch {
	case err == nil:
		return ""
	case errors.As(err, &ref):
		return ref.Code
	}
	return "?"
}

func exit(err error) int {
	var ref *contract.Refusal
	if errors.As(err, &ref) {
		return ref.Exit
	}
	return 0
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
}

func add(t *testing.T, s *contract.State, id string, deps ...string) {
	t.Helper()
	must(t, r.AddTask(s, contract.Task{ID: id, Title: "task " + id, Check: "true", After: deps}, nil, t0))
}

func pane(status string) *contract.Pane {
	p := &contract.Pane{ID: "w1:p2", Tab: "w1:t2", Terminal: "term1", Agent: "claude", Status: status}
	if status == "" {
		p.Agent = ""
	}
	return p
}

// in returns a run whose task T1 has its first attempt in the given state,
// reached by legal changes only.
func in(t *testing.T, state contract.AttemptState) *contract.State {
	t.Helper()
	s := newRun()
	add(t, s, "T1")
	_, err := r.Start(s, "T1", false, false, token, agent, true, t0)
	must(t, err)
	if state == contract.AttemptStarting {
		return s
	}
	if state == contract.AttemptStartFailed {
		must(t, r.StartFailed(s, "T1.1", "no tab", t0))
		return s
	}
	must(t, r.Placed(s, "T1.1", contract.Place{Tab: "w1:t2", Pane: "w1:p2", Terminal: "term1"}, t0))
	must(t, r.Working(s, "T1.1", t0))
	report := func(outcome string) {
		_, err := report(r.Report(s, "T1.1", token, outcome, "summary", nil, t0))
		must(t, err)
	}
	switch state {
	case contract.AttemptAsked:
		_, err := r.Ask(s, "T1.1", "which base?", nil, t0)
		must(t, err)
	case contract.AttemptReported:
		report(outDone)
	case contract.AttemptChecking, contract.AttemptAccepted:
		report(outDone)
		must(t, r.Checking(s, "T1", contract.Checked{}, t0))
		if state == contract.AttemptAccepted {
			_, err := r.Checked(s, "T1", contract.CheckResult{OK: true}, contract.Accepted{How: howCheck}, t0)
			must(t, err)
		}
	case contract.AttemptFailed:
		report(outFailed)
	case contract.AttemptExited:
		r.Seen(s, "T1.1", nil, false, limits, t0)
		r.Seen(s, "T1.1", nil, false, limits, after(goneAfter))
	case contract.AttemptStopped:
		must(t, r.Stop(s, "T1", false, t0))
	}
	if got := s.Attempts["T1.1"].State; got != state {
		t.Fatalf("built %s, wanted %s", got, state)
	}
	return s
}

var states = []contract.AttemptState{
	contract.AttemptStarting, contract.AttemptWorking, contract.AttemptAsked, contract.AttemptReported, contract.AttemptChecking,
	contract.AttemptAccepted, contract.AttemptFailed, contract.AttemptExited, contract.AttemptStopped, contract.AttemptStartFailed,
}

// Every change that can be tried on an attempt, in every state: the diagram
// of 6.4 and its "everything else" table. A result is the state the attempt
// is left in, or "!" and the code of the refusal; "=" is every state not named.
func TestAttemptChanges(t *testing.T) {
	later := after(time.Minute)
	ops := []struct {
		name string
		do   func(s *contract.State) error
		want map[contract.AttemptState]string
		task map[contract.AttemptState]contract.TaskStatus
	}{
		{"placed", func(s *contract.State) error { return r.Placed(s, "T1.1", contract.Place{Tab: "t"}, later) },
			map[contract.AttemptState]string{"starting": "starting", "=": "!not_starting"}, nil},
		{"working", func(s *contract.State) error { return r.Working(s, "T1.1", later) },
			map[contract.AttemptState]string{"starting": "working", "=": "!not_starting"}, nil},
		{"start failed", func(s *contract.State) error { return r.StartFailed(s, "T1.1", "why", later) },
			map[contract.AttemptState]string{"starting": "start_failed", "=": "!not_starting"},
			map[contract.AttemptState]contract.TaskStatus{"starting": "ready"}},
		{"progress", func(s *contract.State) error { return r.Progress(s, "T1.1", 40, "tests written", later) },
			map[contract.AttemptState]string{"starting": "starting", "working": "working", "asked": "asked", "=": "!not_active"}, nil},
		{"report done", func(s *contract.State) error {
			_, err := report(r.Report(s, "T1.1", token, outDone, "ok", nil, later))
			return err
		},
			map[contract.AttemptState]string{"working": "reported", "asked": "reported", "reported": "reported", "=": "!not_active"},
			map[contract.AttemptState]contract.TaskStatus{"working": "review", "asked": "review"}},
		{"report failed", func(s *contract.State) error {
			_, err := report(r.Report(s, "T1.1", token, outFailed, "no", nil, later))
			return err
		},
			map[contract.AttemptState]string{"working": "failed", "asked": "failed", "failed": "failed", "=": "!not_active"},
			map[contract.AttemptState]contract.TaskStatus{"working": "ready", "asked": "ready"}},
		{"report with a wrong token", func(s *contract.State) error {
			_, err := report(r.Report(s, "T1.1", "other", outDone, "ok", nil, later))
			return err
		},
			map[contract.AttemptState]string{"=": "!bad_token"}, nil},
		{"a command with a wrong token", func(s *contract.State) error { return r.Token(s, "T1.1", "other") },
			map[contract.AttemptState]string{"=": "!bad_token"}, nil},
		{"ask", func(s *contract.State) error { _, err := r.Ask(s, "T1.1", "which?", nil, later); return err },
			map[contract.AttemptState]string{"working": "asked", "asked": "!already_asked", "=": "!not_active"}, nil},
		{"checking", func(s *contract.State) error { return r.Checking(s, "T1", contract.Checked{OID: "abc"}, later) },
			map[contract.AttemptState]string{"reported": "checking", "checking": "checking", "=": "!not_in_review"}, nil},
		{"check passed", func(s *contract.State) error {
			_, err := r.Checked(s, "T1", contract.CheckResult{OK: true, Tail: "ok"}, contract.Accepted{How: howCheck}, later)
			return err
		}, map[contract.AttemptState]string{"checking": "accepted", "=": "!not_checking"},
			map[contract.AttemptState]contract.TaskStatus{"checking": "done"}},
		{"check failed", func(s *contract.State) error {
			_, err := r.Checked(s, "T1", contract.CheckResult{Tail: "2 tests fail"}, contract.Accepted{How: howCheck}, later)
			return err
		}, map[contract.AttemptState]string{"checking": "reported", "=": "!not_checking"},
			map[contract.AttemptState]contract.TaskStatus{"checking": "review"}},
		{"accepted by hand with a check", func(s *contract.State) error {
			_, err := r.Checked(s, "T1", contract.CheckResult{OK: true}, contract.Accepted{How: howByHand}, later)
			return err
		}, map[contract.AttemptState]string{"checking": "!by_hand", "=": "!not_checking"}, nil},
		{"reject, agent alive", func(s *contract.State) error { return r.Reject(s, "T1", "tests missing", contract.Live, later) },
			map[contract.AttemptState]string{"reported": "working", "checking": "!checking", "=": "!not_in_review"},
			map[contract.AttemptState]contract.TaskStatus{"reported": "running"}},
		{"reject, agent gone", func(s *contract.State) error { return r.Reject(s, "T1", "tests missing", contract.Gone, later) },
			map[contract.AttemptState]string{"reported": "stopped", "checking": "!checking", "=": "!not_in_review"},
			map[contract.AttemptState]contract.TaskStatus{"reported": "ready"}},
		{"reject, herdr cannot say", func(s *contract.State) error { return r.Reject(s, "T1", "why", contract.Unverifiable, later) },
			map[contract.AttemptState]string{"reported": "!unverifiable", "checking": "!checking", "=": "!not_in_review"}, nil},
		{"stop", func(s *contract.State) error { return r.Stop(s, "T1", false, later) },
			map[contract.AttemptState]string{"starting": "stopped", "working": "stopped", "asked": "stopped", "reported": "stopped", "checking": "!checking", "=": "!no_live_attempt"},
			map[contract.AttemptState]contract.TaskStatus{"starting": "ready", "working": "ready", "asked": "ready", "reported": "ready"}},
		{"tell", func(s *contract.State) error { _, err := r.Tell(s, "T1", "note", "", later); return err },
			map[contract.AttemptState]string{"starting": "starting", "working": "working", "asked": "asked", "reported": "reported", "checking": "checking", "=": "!no_live_attempt"}, nil},
		{"a second start", func(s *contract.State) error {
			_, err := r.Start(s, "T1", false, false, token, agent, true, later)
			return err
		},
			map[contract.AttemptState]string{"failed": "!needs_retry", "exited": "!needs_retry", "stopped": "!needs_retry", "start_failed": "!needs_retry", "=": "!not_startable"}, nil},
		{"proven gone by the sweep", func(s *contract.State) error {
			r.Seen(s, "T1.1", nil, true, limits, later)
			r.Seen(s, "T1.1", nil, true, limits, later.Add(goneAfter))
			return nil
		}, map[contract.AttemptState]string{"starting": "starting", "working": "exited", "asked": "exited", "reported": "reported", "checking": "checking",
			"accepted": "accepted", "failed": "failed", "exited": "exited", "stopped": "stopped", "start_failed": "start_failed"},
			map[contract.AttemptState]contract.TaskStatus{"working": "ready", "asked": "ready", "reported": "review"}},
		{"its start died", func(s *contract.State) error { r.Seen(s, "T1.1", nil, false, limits, later); return nil },
			map[contract.AttemptState]string{"starting": "start_failed", "working": "working", "asked": "asked", "reported": "reported", "checking": "checking",
				"accepted": "accepted", "failed": "failed", "exited": "exited", "stopped": "stopped", "start_failed": "start_failed"}, nil},
	}
	for _, op := range ops {
		for _, state := range states {
			t.Run(op.name+" while "+string(state), func(t *testing.T) {
				s := in(t, state)
				was := snapshot(s)
				err := op.do(s)
				want, named := op.want[state]
				if !named {
					want = op.want["="]
				}
				got := string(s.Attempts["T1.1"].State)
				if err != nil {
					got = "!" + code(err)
					if exit(err) != contract.ExitRefused {
						t.Errorf("exit %d, want %d", exit(err), contract.ExitRefused)
					}
					if s.Attempts["T1.1"].State != state {
						t.Errorf("a refusal moved the attempt to %s", s.Attempts["T1.1"].State)
					}
					if !strings.HasPrefix(op.name, "report") && snapshot(s) != was {
						t.Error("a refusal changed the record")
					}
				}
				if got != want {
					t.Fatalf("got %s, want %s", got, want)
				}
				if task := op.task[state]; task != "" && s.Tasks["T1"].Status != task {
					t.Errorf("task is %s, want %s", s.Tasks["T1"].Status, task)
				}
				if msg := invariants(s); msg != "" {
					t.Error(msg)
				}
			})
		}
	}
}

// The rows of 6.4's table that are about more than the state.
func TestEverythingElse(t *testing.T) {
	t.Run("the same report twice is already recorded", func(t *testing.T) {
		s := in(t, contract.AttemptReported)
		already, err := report(r.Report(s, "T1.1", token, outDone, "again", nil, t0))
		if !already || err != nil || len(s.Inbox.Events) != 1 {
			t.Errorf("already %v, err %v, %d events", already, err, len(s.Inbox.Events))
		}
	})
	t.Run("a refused report is kept as a rejected event", func(t *testing.T) {
		for _, state := range []contract.AttemptState{contract.AttemptChecking, contract.AttemptStopped, contract.AttemptAccepted} {
			s := in(t, state)
			n := len(s.Inbox.Events)
			_, err := report(r.Report(s, "T1.1", token, outDone, "late", nil, t0))
			if code(err) != "not_active" || len(s.Inbox.Events) != n+1 || s.Inbox.Events[n].Kind != "rejected" {
				t.Errorf("%s: %v, events %v", state, err, s.Inbox.Events)
			}
		}
	})
	t.Run("a report from an old attempt after a retry", func(t *testing.T) {
		s := in(t, contract.AttemptStopped)
		_, err := r.Start(s, "T1", true, false, "second", agent, true, t0)
		must(t, err)
		if _, err := report(r.Report(s, "T1.1", token, outDone, "old", nil, t0)); code(err) != "not_active" {
			t.Errorf("got %v", err)
		}
		if a := s.Attempts["T1.2"]; a.RetryOf != "T1.1" || a.N != 2 || a.State != contract.AttemptStarting {
			t.Errorf("retry is %+v", a)
		}
	})
	t.Run("a browser check needs evidence, from the worker and at accept", func(t *testing.T) {
		s := in(t, contract.AttemptWorking)
		s.Tasks["T1"].BrowserCheck = true
		n := len(s.Inbox.Events)
		_, err := report(r.Report(s, "T1.1", token, outDone, "ok", nil, t0))
		if code(err) != "no_evidence" || exit(err) != contract.ExitFailed || len(s.Inbox.Events) != n {
			t.Errorf("without evidence: %v", err)
		}
		_, err = report(r.Report(s, "T1.1", token, outDone, "ok", []contract.EvidenceFile{{Name: "home.png", Size: 9}}, t0))
		must(t, err)
		s.Attempts["T1.1"].Report.Evidence = nil
		if err := r.Checking(s, "T1", contract.Checked{}, t0); code(err) != "no_evidence" || exit(err) != contract.ExitRefused {
			t.Errorf("accept without evidence: %v", err)
		}
	})
	t.Run("tell is stored and read once, and the pointer is recorded", func(t *testing.T) {
		s := in(t, contract.AttemptStarting)
		seq, err := r.Tell(s, "task T1", "read the notes", "msg/1.md", t0)
		must(t, err)
		must(t, r.Pointed(s, "T1.1", seq, after(time.Second)))
		got, _ := r.ReadMail(s, "T1.1", after(time.Minute))
		again, _ := r.ReadMail(s, "T1.1", after(time.Hour))
		if len(got) != 1 || got[0].Body != "msg/1.md" || got[0].NudgedAt.IsZero() || len(again) != 0 {
			t.Errorf("read %+v, then %+v", got, again)
		}
		if err := r.Pointed(s, "T1.1", 99, t0); exit(err) != contract.ExitMissing {
			t.Errorf("a pointer for no message: %v", err)
		}
	})
	t.Run("tell lead is a human event", func(t *testing.T) {
		s := newRun()
		_, err := r.Tell(s, "lead", "look at T3", "msg/2.md", t0)
		must(t, err)
		if e := s.Inbox.Events[0]; e.Kind != "human" || e.Text != "look at T3" || e.Data["body"] != "msg/2.md" {
			t.Errorf("got %+v", e)
		}
	})
	t.Run("words the rules do not know are usage errors", func(t *testing.T) {
		s := in(t, contract.AttemptChecking)
		if _, err := report(r.Report(s, "T1.1", token, "maybe", "", nil, t0)); exit(err) != contract.ExitUsage {
			t.Errorf("a report that is neither done nor failed: %v", err)
		}
		if _, err := r.Checked(s, "T1", contract.CheckResult{OK: true}, contract.Accepted{How: "luck"}, t0); exit(err) != contract.ExitUsage {
			t.Errorf("accepted in an unknown way: %v", err)
		}
		if err := r.Progress(in(t, contract.AttemptWorking), "T1.1", 101, "", t0); exit(err) != contract.ExitUsage {
			t.Errorf("progress of 101: %v", err)
		}
	})
	t.Run("a failed check is stored on the attempt until it is sent back", func(t *testing.T) {
		s := in(t, contract.AttemptChecking)
		_, err := r.Checked(s, "T1", contract.CheckResult{Tail: "2 of 41 tests fail"}, contract.Accepted{How: howCheck}, t0)
		must(t, err)
		a := s.Attempts["T1.1"]
		if a.Check == nil || a.Check.OK || a.Check.Tail != "2 of 41 tests fail" || a.Check.At != t0 || a.Accept != nil {
			t.Fatalf("check %+v, accept %+v", a.Check, a.Accept)
		}
		if e := s.Inbox.Events[len(s.Inbox.Events)-1]; e.Kind != "check_failed" || e.Text != a.Check.Tail {
			t.Errorf("event %+v", e)
		}
		must(t, r.Reject(s, "T1", "fix the bounce tests", contract.Live, t0))
		if a.Check != nil || a.Report != nil || a.Round != 1 || len(a.Mail) != 1 || a.Mail[0].Text != "fix the bounce tests" {
			t.Errorf("after reject: %+v", a)
		}
	})
	t.Run("a passed check is stored too, and accepting by hand stores none", func(t *testing.T) {
		s := in(t, contract.AttemptAccepted)
		if c := s.Attempts["T1.1"].Check; c == nil || !c.OK || s.Tasks["T1"].Accepted.How != howCheck {
			t.Errorf("check %+v", c)
		}
		s = in(t, contract.AttemptReported)
		s.Tasks["T1"].Check = contract.CheckNone
		must(t, r.Checking(s, "T1", contract.Checked{}, t0))
		if _, err := r.Checked(s, "T1", contract.CheckResult{OK: true}, contract.Accepted{How: howCheck}, t0); code(err) != "by_hand" {
			t.Errorf("a check on a task that has none: %v", err)
		}
		_, err := r.Checked(s, "T1", contract.CheckResult{}, contract.Accepted{How: howByHand, Note: "read the diff"}, t0)
		must(t, err)
		if acc := s.Tasks["T1"].Accepted; s.Attempts["T1.1"].Check != nil || acc.How != howByHand || acc.Note != "read the diff" {
			t.Errorf("accepted %+v", acc)
		}
	})
	t.Run("checking records what is checked", func(t *testing.T) {
		s := in(t, contract.AttemptReported)
		must(t, r.Checking(s, "T1", contract.Checked{}, t0))
		must(t, r.Checking(s, "T1", contract.Checked{OID: "abc", Tip: "def"}, after(time.Second)))
		if acc := s.Attempts["T1.1"].Accept; acc.CheckedOID != "abc" || acc.ExpectedTip != "def" || acc.StartedAt != t0 {
			t.Errorf("accept %+v", acc)
		}
	})
	t.Run("the third failure stops the loop until a reset", func(t *testing.T) {
		s := newRun()
		add(t, s, "T1")
		for n := 1; n <= contract.MaxFailures; n++ {
			id, err := r.Start(s, "T1", n > 1, false, token, agent, true, t0)
			must(t, err)
			must(t, r.Working(s, id, t0))
			_, err = report(r.Report(s, id, token, outFailed, "no", nil, t0))
			must(t, err)
		}
		if task := s.Tasks["T1"]; task.Status != contract.TaskFailed || task.Failures != 3 {
			t.Fatalf("task %s after %d failures", task.Status, task.Failures)
		}
		if _, err := r.Start(s, "T1", true, false, token, agent, true, t0); code(err) != "not_startable" {
			t.Errorf("start of a failed task: %v", err)
		}
		must(t, r.ResetTask(s, "T1", t0))
		if task := s.Tasks["T1"]; task.Status != contract.TaskReady || task.Failures != 0 {
			t.Errorf("after reset %s, %d failures", task.Status, task.Failures)
		}
	})
	t.Run("start guards", func(t *testing.T) {
		s := in(t, contract.AttemptStopped)
		if _, err := r.Start(s, "T1", true, true, token, agent, true, t0); code(err) != "pane_busy" {
			t.Errorf("a retry while the old tab holds an agent: %v", err)
		}
		s = newRun()
		for _, id := range []string{"T1", "T2", "T3", "T4"} {
			add(t, s, id)
		}
		add(t, s, "T5", "T1")
		for _, id := range []string{"T1", "T2", "T3"} {
			_, err := r.Start(s, id, false, false, token, contract.Agent{}, true, t0)
			must(t, err)
		}
		_, err := r.Start(s, "T4", false, false, token, agent, true, t0)
		if code(err) != "limit" {
			t.Errorf("a fourth start under a limit of 3: %v", err)
		}
		_, err = r.Start(s, "T5", false, false, token, agent, true, t0)
		var ref *contract.Refusal
		if !errors.As(err, &ref) || ref.Code != "not_startable" || ref.Message != "T5 waits on T1." || ref.Next[0] != "whaleshark graph" {
			t.Errorf("a start of a pending task: %+v", err)
		}
		if _, err := r.Start(s, "T9", false, false, token, agent, true, t0); exit(err) != contract.ExitMissing {
			t.Errorf("a start of no task: %v", err)
		}
	})
}

// The table of 6.3: every change of a task, allowed and refused.
func TestTaskChanges(t *testing.T) {
	task := func(status contract.TaskStatus) *contract.State {
		switch status {
		case contract.TaskPending:
			s := newRun()
			add(t, s, "T0")
			add(t, s, "T1", "T0")
			return s
		case contract.TaskReady:
			return in(t, contract.AttemptStopped)
		case contract.TaskRunning:
			return in(t, contract.AttemptWorking)
		case contract.TaskReview:
			return in(t, contract.AttemptReported)
		case contract.TaskDone:
			return in(t, contract.AttemptAccepted)
		case contract.TaskFailed:
			s := in(t, contract.AttemptStopped)
			s.Tasks["T1"].Status, s.Tasks["T1"].Failures = contract.TaskFailed, 3
			return s
		}
		s := in(t, contract.AttemptStopped)
		must(t, r.CancelTask(s, "T1", t0))
		return s
	}
	all := []contract.TaskStatus{contract.TaskPending, contract.TaskReady, contract.TaskRunning, contract.TaskReview, contract.TaskDone, contract.TaskFailed, contract.TaskCancelled}
	check := "make test"
	ops := []struct {
		name string
		do   func(s *contract.State) error
		want map[contract.TaskStatus]string
	}{
		{"cancel", func(s *contract.State) error { return r.CancelTask(s, "T1", t0) },
			map[contract.TaskStatus]string{"pending": "cancelled", "ready": "cancelled", "failed": "cancelled", "running": "!still_live", "review": "!still_live", "=": "!final"}},
		{"reset", func(s *contract.State) error { return r.ResetTask(s, "T1", t0) },
			map[contract.TaskStatus]string{"failed": "ready", "=": "!not_failed"}},
		{"edit", func(s *contract.State) error {
			_, err := r.EditTask(s, "T1", contract.TaskEdit{Check: &check, Owns: &[]string{"src/**"}}, t0)
			return err
		}, map[contract.TaskStatus]string{"pending": "pending", "ready": "ready", "failed": "failed", "=": "!not_editable"}},
		{"start", func(s *contract.State) error {
			_, err := r.Start(s, "T1", true, false, token, agent, true, t0)
			return err
		},
			map[contract.TaskStatus]string{"ready": "running", "=": "!not_startable"}},
		{"accept", func(s *contract.State) error { return r.Checking(s, "T1", contract.Checked{}, t0) },
			map[contract.TaskStatus]string{"review": "review", "=": "!not_in_review"}},
		{"an item that holds it", func(s *contract.State) error {
			_, err := r.Need(s, contract.Question{Form: contract.FormChoice, Text: "ship it?", Task: "T1", Holds: true}, t0)
			return err
		}, map[contract.TaskStatus]string{"done": "!settled", "cancelled": "!settled"}},
	}
	for _, op := range ops {
		for _, status := range all {
			t.Run(op.name+" while "+string(status), func(t *testing.T) {
				s := task(status)
				if s.Tasks["T1"].Status != status {
					t.Fatalf("built %s", s.Tasks["T1"].Status)
				}
				was := snapshot(s)
				err := op.do(s)
				want, named := op.want[status]
				if !named {
					want = cmpOr(op.want["="], string(status))
				}
				got := string(s.Tasks["T1"].Status)
				if err != nil {
					got = "!" + code(err)
					if snapshot(s) != was || exit(err) != contract.ExitRefused {
						t.Errorf("a refusal changed the record, or exit %d", exit(err))
					}
				}
				if got != want {
					t.Fatalf("got %s, want %s", got, want)
				}
				if msg := invariants(s); msg != "" {
					t.Error(msg)
				}
			})
		}
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func TestDependencies(t *testing.T) {
	s := newRun()
	add(t, s, "T1")
	add(t, s, "T2")
	add(t, s, "T8", "T1", "T2")
	if err := r.AddTask(s, contract.Task{ID: "T9", Title: "nine", Check: "true", After: []string{"T7"}}, nil, t0); exit(err) != contract.ExitMissing {
		t.Errorf("an unknown dependency: %v", err)
	}
	if s.Tasks["T8"].Status != contract.TaskPending || s.Tasks["T1"].Status != contract.TaskReady {
		t.Fatalf("T8 %s, T1 %s", s.Tasks["T8"].Status, s.Tasks["T1"].Status)
	}
	accept := func(id string) []string {
		a, err := r.Start(s, id, false, false, token, agent, true, t0)
		must(t, err)
		must(t, r.Working(s, a, t0))
		_, err = report(r.Report(s, a, token, outDone, "ok", nil, t0))
		must(t, err)
		must(t, r.Checking(s, id, contract.Checked{}, t0))
		ready, err := r.Checked(s, id, contract.CheckResult{OK: true}, contract.Accepted{How: howCheck}, t0)
		must(t, err)
		return ready
	}
	if ready := accept("T1"); len(ready) != 0 || s.Tasks["T8"].Status != contract.TaskPending {
		t.Errorf("after T1: ready %v", ready)
	}
	if ready := accept("T2"); !slices.Equal(ready, []string{"T8"}) || s.Tasks["T8"].Status != contract.TaskReady {
		t.Errorf("after T2: ready %v", ready)
	}

	// A failed or cancelled dependency does not cascade; removing it frees the dependant.
	add(t, s, "T3")
	add(t, s, "T4", "T3")
	must(t, r.CancelTask(s, "T3", t0))
	if s.Tasks["T4"].Status != contract.TaskPending {
		t.Errorf("T4 is %s behind a cancelled task", s.Tasks["T4"].Status)
	}
	ready, err := r.EditTask(s, "T4", contract.TaskEdit{After: &[]string{"T1"}}, t0)
	if err != nil || !slices.Equal(ready, []string{"T4"}) {
		t.Errorf("freed: %v, %v", ready, err)
	}
	if ready, _ := r.EditTask(s, "T4", contract.TaskEdit{After: &[]string{"T8"}}, t0); len(ready) != 0 || s.Tasks["T4"].Status != contract.TaskPending {
		t.Errorf("T4 is %s behind T8", s.Tasks["T4"].Status)
	}
	// A loop cannot be written with edit either.
	for _, deps := range [][]string{{"T8"}, {"T4"}} {
		id := map[string]string{"T8": "T8", "T4": "T8"}[deps[0]]
		if _, err := r.EditTask(s, id, contract.TaskEdit{After: &deps}, t0); code(err) != "loop" {
			t.Errorf("%s after %v: %v", id, deps, err)
		}
	}
	if _, err := r.EditTask(s, "T4", contract.TaskEdit{After: &[]string{"T7"}}, t0); exit(err) != contract.ExitMissing {
		t.Errorf("edit to an unknown dependency: %v", err)
	}
	empty := ""
	if _, err := r.EditTask(s, "T4", contract.TaskEdit{Check: &empty}, t0); code(err) != "no_check" {
		t.Errorf("edit to no check: %v", err)
	}
	brief := "briefs/T4.md"
	if _, err := r.EditTask(s, "T4", contract.TaskEdit{Brief: &brief}, t0); err != nil || s.Tasks["T4"].Brief != brief {
		t.Errorf("edit of the brief: %v", err)
	}
}

// The three-word name and its uniqueness (6.3).
func TestNames(t *testing.T) {
	taken := []string{"Search Box"}
	rows := []struct {
		id, title, name string
		want            string
		exit            int
		stored          string
	}{
		{"T2", "url parser", "", "", 0, "url parser"},
		{"T2", "Add tests for the parser", "", "name_needed", contract.ExitUsage, ""},
		{"T2", "Add tests for the parser", "parser tests", "", 0, "parser tests"},
		{"T2", "anything", "one two three four", "bad_name", contract.ExitUsage, ""},
		{"T2", "anything", "abcdefghij klmnopqrs tuv", "", 0, "abcdefghij klmnopqrs tuv"},
		{"T2", "anything", "abcdefghij klmnopqrst uvw", "bad_name", contract.ExitUsage, ""},
		{"T2", "extraordinarily long words", "", "name_needed", contract.ExitUsage, ""},
		{"T2", "anything", "  sign-up   page ", "", 0, "sign-up page"},
		{"T2", "anything", "search box", "name_taken", contract.ExitRefused, ""},
		{"T2", "Login Page", "", "name_taken", contract.ExitRefused, ""},
		{"T2", "anything", "t1", "name_taken", contract.ExitRefused, ""},
		{"T2", "anything", "Lead", "name_taken", contract.ExitRefused, ""},
		{"t1", "another", "", "id_taken", contract.ExitRefused, ""},
		{"LOGIN PAGE", "another", "", "id_taken", contract.ExitRefused, ""},
		{"lead", "another", "", "id_taken", contract.ExitRefused, ""},
	}
	for _, row := range rows {
		s := newRun()
		must(t, r.AddTask(s, contract.Task{ID: "T1", Title: "login page", Check: "true"}, taken, t0))
		was := snapshot(s)
		err := r.AddTask(s, contract.Task{ID: row.id, Title: row.title, Name: row.name, Check: "true"}, taken, t0)
		if code(err) != row.want || exit(err) != row.exit {
			t.Errorf("%s %q --name %q: %v (exit %d), want %q", row.id, row.title, row.name, err, exit(err), row.want)
		}
		if err != nil && snapshot(s) != was {
			t.Errorf("%s %q: a refusal changed the record", row.id, row.title)
		}
		if err == nil && s.Tasks[row.id].Name != row.stored {
			t.Errorf("stored %q, want %q", s.Tasks[row.id].Name, row.stored)
		}
	}
	if len(taken) != 1 {
		t.Error("the caller's list of names was changed")
	}
	s := newRun()
	if err := r.AddTask(s, contract.Task{ID: "T1", Title: "no check"}, nil, t0); code(err) != "no_check" || exit(err) != contract.ExitUsage {
		t.Errorf("a task with no check: %v", err)
	}
	add(t, s, "T1")
	for _, word := range []string{"T1", "task t1", "Task T1"} {
		if task, err := r.FindTask(s, word); err != nil || task.ID != "T1" {
			t.Errorf("find %q: %v", word, err)
		}
	}
	if _, err := r.FindTask(s, "t1"); exit(err) != contract.ExitMissing {
		t.Errorf("an id in another letter case is not that task: %v", err)
	}
}

// 6.2: closing, binding and fencing.
func TestRun(t *testing.T) {
	s := in(t, contract.AttemptWorking)
	if err := r.CloseRun(s, false, t0); code(err) != "still_live" {
		t.Errorf("close with a live attempt: %v", err)
	}
	s = in(t, contract.AttemptReported)
	must(t, r.Stop(s, "T1", false, t0))
	s.Tasks["T1"].Status = contract.TaskReview
	if err := r.CloseRun(s, false, t0); code(err) != "in_review" {
		t.Errorf("close with a task in review: %v", err)
	}
	s = in(t, contract.AttemptAccepted)
	s.Tasks["T1"].Accepted.Commit = "c1"
	s.Run.Integration = &contract.Integration{Branch: "whaleshark/r1", Tip: "c1"}
	if err := r.CloseRun(s, false, t0); code(err) != "not_landed" {
		t.Errorf("close with work not landed: %v", err)
	}
	must(t, r.CloseRun(s, true, t0))
	s.Run.ClosedAt, s.Run.Integration.Landed = time.Time{}, "c1"
	must(t, r.CloseRun(s, false, after(time.Hour)))
	if s.Run.ClosedAt != after(time.Hour) {
		t.Errorf("closed at %v", s.Run.ClosedAt)
	}
	closed := snapshot(s)
	for name, err := range map[string]error{
		"close":    r.CloseRun(s, false, t0),
		"takeover": r.Takeover(s, contract.Binding{Pane: "w1:p9"}, t0),
		"pause":    r.Pause(s, "you", t0),
		"task add": r.AddTask(s, contract.Task{ID: "T2", Title: "two", Check: "true"}, nil, t0),
	} {
		if code(err) != "closed" {
			t.Errorf("%s on a closed run: %v", name, err)
		}
	}
	if _, err := r.Need(s, contract.Question{Form: contract.FormTodo, Text: "x"}, t0); code(err) != "closed" || snapshot(s) != closed {
		t.Errorf("need on a closed run: %v", err)
	}

	s = newRun()
	if o := s.Run.Orchestrator; o.Pane != "w1:p1" || o.BoundAt != t0 || s.Run.Limit != 3 || s.Version != contract.StateVersion {
		t.Errorf("new run %+v", s.Run)
	}
	if got := r.NewRun("r2", "", 0, nil, contract.Binding{}, t0).Run.Limit; got != 20 {
		t.Errorf("default limit %d", got)
	}
	must(t, r.Takeover(s, contract.Binding{Pane: "w1:p1"}, after(time.Minute)))
	if s.Run.Gen != 0 || s.Run.Orchestrator.BoundAt != t0 {
		t.Error("a takeover from the bound pane fenced it")
	}
	must(t, r.Takeover(s, contract.Binding{Pane: "w1:p7", Workspace: "w1"}, after(time.Minute)))
	if s.Run.Gen != 1 || s.Run.Orchestrator.Pane != "w1:p7" || s.Run.Orchestrator.BoundAt != after(time.Minute) {
		t.Errorf("after takeover: gen %d, %+v", s.Run.Gen, s.Run.Orchestrator)
	}
	old := contract.Caller{Kind: contract.Orchestrator, Pane: "w1:p1"}
	if err := r.Allowed(s, old, "start", ""); code(err) != "not_bound" || exit(err) != contract.ExitRefused {
		t.Errorf("the old lead agent after a takeover: %v", err)
	}
	must(t, r.Allowed(s, contract.Caller{Kind: contract.Orchestrator, Pane: "w1:p7"}, "start", ""))
	must(t, r.Allowed(s, old, "status", ""))
}

// Section 7, "Who is calling": each kind of caller against the command table.
func TestAllowed(t *testing.T) {
	s := in(t, contract.AttemptWorking)
	lead := contract.Caller{Kind: contract.Orchestrator, Pane: "w1:p1"}
	worker := contract.Caller{Kind: contract.Worker, Pane: "w1:p2", Attempt: "T1.1"}
	person := contract.Caller{Kind: contract.Human, Pane: "w1:p5", Where: contract.WhereTyped}
	stranger := contract.Caller{Kind: contract.Unbound, Pane: "w1:p6"}
	rows := []struct {
		who          contract.Caller
		command, sub string
		want         string
	}{
		{lead, "start", "", ""}, {lead, "accept", "", ""}, {lead, "task", "add", ""}, {lead, "answer", "", ""},
		{lead, "pause", "", "human_only"}, {lead, "resume", "", "human_only"}, {lead, "tell", "lead", "human_only"},
		{lead, "trust", "", "human_only"}, {lead, "report", "", "worker_only"},
		{worker, "report", "", ""}, {worker, "progress", "", ""}, {worker, "ask", "", ""}, {worker, "mail", "", ""},
		{worker, "guide", "", ""}, {worker, "help", "", ""}, {worker, "version", "", ""},
		{worker, "accept", "", "worker"}, {worker, "answer", "", "worker"}, {worker, "task", "add", "worker"},
		{worker, "run", "takeover", "worker"}, {worker, "start", "", "worker"}, {worker, "status", "", "worker"},
		{contract.Caller{Kind: contract.Worker, Attempt: "T9.1"}, "report", "", "not_active"},
		{person, "pause", "", ""}, {person, "accept", "", ""}, {person, "answer", "", ""}, {person, "tell", "lead", ""},
		{person, "report", "", "worker_only"},
		{contract.Caller{Kind: contract.Human, Pane: "w1:p1"}, "answer", "", "not_human"},
		{contract.Caller{Kind: contract.Human, Pane: "w1:p2"}, "accept", "", "not_human"},
		{contract.Caller{Kind: contract.Human}, "answer", "", ""},
		{stranger, "status", "", ""}, {stranger, "run", "new", ""}, {stranger, "run", "takeover", ""}, {stranger, "set", "", ""},
		{stranger, "start", "", "not_bound"}, {stranger, "run", "close", "not_bound"}, {stranger, "answer", "", "not_bound"},
		{contract.Caller{Kind: contract.Orchestrator, Pane: "w1:p9"}, "start", "", "not_bound"},
		{contract.Caller{Kind: contract.Orchestrator, Pane: "w1:p9"}, "show", "", ""},
		{lead, "nonsense", "", "unknown_command"},
	}
	for _, row := range rows {
		err := r.Allowed(s, row.who, row.command, row.sub)
		if code(err) != row.want {
			t.Errorf("%s in %s runs %s %s: %v, want %q", row.who.Kind, row.who.Pane, row.command, row.sub, err, row.want)
		}
		var ref *contract.Refusal
		if errors.As(err, &ref) && row.who.Kind != contract.Human && strings.Contains(strings.Join(ref.Next, " "), "--human") {
			t.Errorf("%s is shown --human", row.who.Kind)
		}
	}
	must(t, r.Allowed(nil, stranger, "init", ""))
	if err := r.Allowed(nil, stranger, "start", ""); code(err) != "not_bound" {
		t.Errorf("with no run: %v", err)
	}
}

// 8o: what is refused while the run is paused, over the whole command table.
func TestPaused(t *testing.T) {
	s := in(t, contract.AttemptWorking)
	must(t, r.Pause(s, "you", t0))
	must(t, r.Pause(s, "again", after(time.Minute)))
	if p := s.Run.Paused; p.At != t0 || p.By != "you" || len(s.Inbox.Events) != 1 || s.Inbox.Events[0].Kind != "paused" {
		t.Fatalf("paused %+v, events %v", p, s.Inbox.Events)
	}
	lead := contract.Caller{Kind: contract.Orchestrator, Pane: "w1:p1"}
	person := contract.Caller{Kind: contract.Human}
	worker := contract.Caller{Kind: contract.Worker, Attempt: "T1.1"}
	refusedForms := map[string]bool{"run close": true, "run rm": true, "team run": true}
	passes := []string{"resume", "answer", "stop", "pause", "wait", "need close"}
	for _, c := range contract.Commands {
		subs := []string{""}
		for _, sub := range c.Subs {
			subs = append(subs, sub.Name)
		}
		if c.Name == "run" || c.Name == "need" || c.Name == "team" {
			subs = append(subs, "close", "rm", "run", "save")
		}
		for _, sub := range subs {
			form := strings.TrimSpace(c.Name + " " + sub)
			for _, who := range []contract.Caller{lead, person} {
				if !c.May(who.Kind, sub) {
					continue
				}
				err := r.Allowed(s, who, c.Name, sub)
				changes := !c.May(contract.Unbound, sub) && !slices.Contains(passes, c.Name) && form != "need close"
				if c.Name == "run" || c.Name == "team" {
					changes = refusedForms[form]
				}
				if c.Name == "trust" {
					changes = false
				}
				if (code(err) == "paused") != changes || (err != nil && code(err) != "paused") {
					t.Errorf("%s by the %s while paused: %v, want refused %v", form, who.Kind, err, changes)
				}
				var ref *contract.Refusal
				if errors.As(err, &ref) && (ref.Exit != contract.ExitRefused || strings.Contains(ref.Next[0], "--human") != (who.Kind == contract.Human)) {
					t.Errorf("%s by the %s: exit %d, next %v", form, who.Kind, ref.Exit, ref.Next)
				}
			}
		}
	}
	for _, c := range []string{"start", "accept", "land", "reject", "tell", "close", "task"} {
		if err := r.Allowed(s, lead, c, ""); code(err) != "paused" {
			t.Errorf("%s while paused: %v", c, err)
		}
	}
	for _, c := range []string{"report", "progress", "ask", "mail"} {
		must(t, r.Allowed(s, worker, c, ""))
	}

	// The rules themselves: nothing starts, no accept begins, a start stops at its next step.
	add(t, s, "T2")
	if _, err := r.Start(s, "T2", false, false, token, agent, true, t0); code(err) != "paused" {
		t.Errorf("start while paused: %v", err)
	}
	must(t, r.Progress(s, "T1.1", 50, "half", t0))
	qid, err := r.Ask(s, "T1.1", "which base?", nil, t0)
	must(t, err)
	must(t, r.Answer(s, qid, "main", orch, 0, t0))
	if _, ok, err := r.Use(s, qid, after(time.Hour)); ok || err != nil {
		t.Errorf("an answer was used while paused: %v", err)
	}
	must(t, r.Undo(s, qid, human, after(time.Hour)))
	must(t, r.Answer(s, qid, "release", orch, 0, after(time.Hour)))
	_, err = report(r.Report(s, "T1.1", token, outDone, "ok", nil, t0))
	must(t, err)
	if err := r.Checking(s, "T1", contract.Checked{}, t0); code(err) != "paused" {
		t.Errorf("a new accept while paused: %v", err)
	}
	must(t, r.Stop(s, "T1", false, t0))

	s = in(t, contract.AttemptChecking)
	must(t, r.Pause(s, "you", t0))
	must(t, r.Checking(s, "T1", contract.Checked{OID: "abc"}, t0))
	if _, err := r.Checked(s, "T1", contract.CheckResult{OK: true}, contract.Accepted{How: howCheck}, t0); err != nil || s.Tasks["T1"].Status != contract.TaskDone {
		t.Errorf("an accept that was running when the pause came: %v", err)
	}

	s = in(t, contract.AttemptStarting)
	must(t, r.Pause(s, "you", t0))
	if err := r.Placed(s, "T1.1", contract.Place{}, t0); code(err) != "paused" {
		t.Errorf("placed while paused: %v", err)
	}
	if err := r.Working(s, "T1.1", t0); code(err) != "paused" {
		t.Errorf("working while paused: %v", err)
	}
	must(t, r.StartFailed(s, "T1.1", "paused", t0))
	if task := s.Tasks["T1"]; task.Status != contract.TaskReady || task.Failures != 0 {
		t.Errorf("a start cut off by the pause counted: %s, %d", task.Status, task.Failures)
	}
	must(t, r.Resume(s, after(time.Minute)))
	if err := r.Resume(s, after(time.Minute)); code(err) != "not_paused" {
		t.Errorf("a second resume: %v", err)
	}
	if e := s.Inbox.Events[len(s.Inbox.Events)-1]; e.Kind != "resumed" || s.Run.Paused != nil {
		t.Errorf("last event %+v", e)
	}
}

func snapshot(s *contract.State) string {
	data, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// report gives a report's two kinds of refusal as one error, for the tests
// that only ask whether it was refused.
func report(already bool, kept *contract.Refusal, err error) (bool, error) {
	if kept != nil {
		return already, kept
	}
	return already, err
}

func TestUncheckedAndShown(t *testing.T) {
	s := in(t, contract.AttemptReported)
	if err := r.Unchecked(s, "T1", t0); code(err) != "not_checking" {
		t.Errorf("unchecked while nothing is checked: %v", err)
	}
	must(t, r.Checking(s, "T1", contract.Checked{OID: "abc"}, t0))
	must(t, r.Unchecked(s, "T1", after(time.Minute)))
	if a := s.Attempts["T1.1"]; a.State != contract.AttemptReported || a.Accept != nil || a.Check != nil || s.Tasks["T1"].Status != contract.TaskReview {
		t.Errorf("after an accept in which no check ran: %+v", a)
	}
	id, err := r.Need(s, contract.Question{Form: contract.FormTodo, Text: "look"}, t0)
	must(t, err)
	must(t, r.Shown(s, id, after(time.Second)))
	must(t, r.Shown(s, id, after(time.Hour)))
	if q := s.Questions[id]; q.ShownAt != after(time.Second) {
		t.Errorf("shown at %v, want the first moment", q.ShownAt)
	}
	if err := r.Shown(s, "n99", t0); exit(err) != contract.ExitMissing {
		t.Errorf("an item that is not there: %v", err)
	}
}
