package rules

import (
	"slices"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// A watch inside its time box is long and idle by its nature: no note for
// hours, at rest for hours, and the run's own limit of an hour passed, and
// nothing is raised and nobody calls it stuck. Past its box it is judged as
// any worker is.
func TestAWatchInsideItsTimeBox(t *testing.T) {
	s, a := calm(t)
	s.Tasks["T1"].Kind, s.Tasks["T1"].LimitMinutes = contract.WorkWatch, 6*60
	a.Seen.LastWorking, a.Progress.At = late.Add(-3*time.Hour), late.Add(-3*time.Hour)
	from := len(s.Inbox.Events)
	r.Seen(s, "T1.1", pane(contract.StatusIdle), false, spans(), late)
	r.Seen(s, "T1.1", pane(contract.StatusWorking), false, spans(), late.Add(time.Minute))
	r.Seen(s, "T1.1", pane(contract.StatusIdle), false, spans(), late.Add(2*time.Minute))
	if changed, up := r.Going(s, "T1.1", true, noCtx, spans(), climb, late.Add(2*time.Minute)); up || changed || !a.Seen.StuckSince.IsZero() {
		t.Fatalf("a watch inside its box is judged stuck: %+v", a.Seen)
	}
	if got := kinds(s, from); len(got) > 0 {
		t.Fatalf("a watch inside its box raised %v", got)
	}
	past := a.StartedAt.Add(6*time.Hour + time.Minute)
	r.Seen(s, "T1.1", pane(contract.StatusIdle), false, spans(), past)
	if got := kinds(s, from); !slices.Contains(got, "overtime") {
		t.Fatalf("a watch past its box raised %v", got)
	}
	if r.Going(s, "T1.1", true, noCtx, spans(), climb, past.Add(4*time.Hour)); a.Seen.StuckSince.IsZero() {
		t.Fatal("a watch long past its box is not judged at all")
	}
}

// A review waits for its task's report, holds that task until its verdict
// is safe, and tells the lead of a fix-first; the last review done decides.
func TestAReviewHoldsItsTask(t *testing.T) {
	s := newRun()
	add(t, s, "T3")
	review := func(id string) {
		t.Helper()
		must(t, r.AddTask(s, contract.Task{ID: id, Title: "review " + id, Check: contract.CheckNone, Kind: contract.WorkReview, Reviews: "T3"}, nil, t0))
	}
	if err := r.AddTask(s, contract.Task{ID: "R0", Title: "review R0", Check: contract.CheckNone, Kind: contract.WorkReview, Reviews: "T9"}, nil, t0); code(err) != "no_such_task" {
		t.Fatalf("a review of no task: %v", err)
	}
	review("R1")
	if s.Tasks["R1"].Status != contract.TaskPending || contract.ReviewHold(s, "T3") != "R1" {
		t.Fatalf("before T3 reports R1 is %s and T3 is held by %q", s.Tasks["R1"].Status, contract.ReviewHold(s, "T3"))
	}
	work := func(id string) {
		t.Helper()
		_, err := r.Start(s, id, false, false, token, agent, true, t0)
		must(t, err)
		n := s.Tasks[id].Attempts[len(s.Tasks[id].Attempts)-1]
		must(t, r.Placed(s, n, contract.Place{Tab: "w1:t2", Pane: "w1:p2"}, t0))
		must(t, r.Working(s, n, t0))
		_, refused, err := r.Report(s, n, token, contract.ReportDone, "read", nil, t0)
		must(t, err)
		if refused != nil {
			t.Fatal(refused)
		}
	}
	work("T3")
	if s.Tasks["R1"].Status != contract.TaskReady {
		t.Fatalf("after T3 reported R1 is %s", s.Tasks["R1"].Status)
	}
	verdict := func(id, word, fix string) {
		t.Helper()
		work(id)
		must(t, r.Checking(s, id, contract.Checked{}, t0))
		if _, err := r.Checked(s, id, contract.CheckResult{OK: true}, contract.Accepted{How: contract.AcceptByHand}, t0); code(err) != "by_result" {
			t.Fatalf("a review accepted by hand: %v", err)
		}
		_, err := r.Checked(s, id, contract.CheckResult{OK: true}, contract.Accepted{How: contract.AcceptResult, Verdict: word, Fix: fix}, t0)
		must(t, err)
	}
	from := len(s.Inbox.Events)
	verdict("R1", contract.VerdictFixFirst, "the limit is off by one")
	if got := kinds(s, from); !slices.Contains(got, "fix_first") || contract.ReviewHold(s, "T3") != "R1" {
		t.Fatalf("a fix-first raised %v and T3 is held by %q", got, contract.ReviewHold(s, "T3"))
	}
	review("R2")
	verdict("R2", contract.VerdictSafe, "")
	if by := contract.ReviewHold(s, "T3"); by != "" {
		t.Fatalf("after a safe review T3 is still held by %s", by)
	}
	review("R3")
	must(t, r.CancelTask(s, "R3", t0))
	if by := contract.ReviewHold(s, "T3"); by != "" {
		t.Fatalf("a cancelled review holds T3: %s", by)
	}
}
