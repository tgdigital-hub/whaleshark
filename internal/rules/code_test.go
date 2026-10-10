package rules

import (
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

func kinds(s *contract.State, from int) (out []string) {
	for _, e := range s.Inbox.Events[from:] {
		out = append(out, e.Kind)
	}
	return out
}

// Where accepted work is collected is written field by field, an accept
// moves the tip to its commit, and a run with work not landed does not close.
func TestIntegrated(t *testing.T) {
	s := newRun()
	must(t, r.Integrated(s, contract.Integration{Branch: "whaleshark/r1", Tip: "base"}))
	must(t, r.Integrated(s, contract.Integration{Landed: "base"}))
	if in := *s.Run.Integration; in != (contract.Integration{Branch: "whaleshark/r1", Tip: "base", Landed: "base"}) {
		t.Fatalf("the integration reads %+v", in)
	}
	add(t, s, "A")
	s.Tasks["A"].Worktree = &contract.Worktree{Path: "wt"}
	id, err := r.Start(s, "A", false, false, token, agent, false, t0)
	must(t, err)
	must(t, r.Working(s, id, t0))
	if _, _, err := r.Report(s, id, token, contract.ReportDone, "ok", nil, t0); err != nil || !s.Run.Scan.Due {
		t.Fatalf("a report from a worktree left no scan due: %v", err)
	}
	if fresh := r.Found(s, nil, after(time.Second)); fresh != nil || s.Run.Scan.Due || !s.Run.Scan.At.Equal(after(time.Second)) {
		t.Fatalf("a scan with nothing found left %+v", s.Run.Scan)
	}
	if n, err := r.Synced(s, "A", "msg/1.md", t0); n != 1 || err != nil {
		t.Fatalf("the first sync: %d, %v", n, err)
	}
	must(t, r.Checking(s, "A", contract.Checked{OID: "c1", Tip: "base"}, t0))
	_, err = r.Checked(s, "A", contract.CheckResult{OK: true}, contract.Accepted{How: contract.AcceptCheck, Commit: "c2"}, t0)
	must(t, err)
	if s.Run.Integration.Tip != "c2" || !s.Run.Scan.Due || s.Tasks["A"].SyncBrief != "" || s.Tasks["A"].Synced != 1 {
		t.Fatalf("after the accept the tip is %q and the scan due is %v", s.Run.Integration.Tip, s.Run.Scan.Due)
	}
	if got := code(r.CloseRun(s, false, t0)); got != "not_landed" {
		t.Fatalf("closing with work not landed: %q", got)
	}
	must(t, r.Integrated(s, contract.Integration{Landed: "c2"}))
	must(t, r.CloseRun(s, false, t0))
	if got := code(r.Integrated(s, contract.Integration{Tip: "c3"})); got != "closed" {
		t.Fatalf("a closed run took a new tip: %q", got)
	}
}

// Stuck is said once for each episode, and a finding once for as long as it
// is there: two tasks that only meet in a file raise nothing.
func TestStuckAndFound(t *testing.T) {
	s := newRun()
	add(t, s, "A")
	add(t, s, "B")
	if !r.Stuck(s, true, t0) || r.Stuck(s, true, t0) || r.Stuck(s, false, t0) || !r.Stuck(s, true, t0) {
		t.Fatal("stuck must be raised once an episode")
	}
	from := len(s.Inbox.Events)
	clash := contract.Finding{Kind: "overlap", Tasks: []string{"A", "B"}, Files: []string{"a.go"}, How: "both changed a.go"}
	same := contract.Finding{Kind: "same", Tasks: []string{"A", "B"}, Files: []string{"b.go"}}
	scope := contract.Finding{Kind: "scope", Tasks: []string{"B"}, Files: []string{"c.go"}}
	if fresh := r.Found(s, []contract.Finding{clash, same}, t0); len(fresh) != 2 {
		t.Fatalf("the first scan's new findings: %+v", fresh)
	}
	if fresh := r.Found(s, []contract.Finding{clash, same, scope, {Kind: "lands", Tasks: []string{"A"}}}, t0); len(fresh) != 2 {
		t.Fatalf("the second scan's new findings: %+v", fresh)
	}
	if got := kinds(s, from); len(got) != 3 || got[0] != "overlap" || got[1] != "scope" || got[2] != "overlap" {
		t.Fatalf("the scans raised %v", got)
	}
	if e := s.Inbox.Events[from]; e.Task != "B" || e.Text != clash.How {
		t.Fatalf("a clash is the later task's to wait for: %+v", e)
	}
	// A task sent to sync keeps its brief until it is done, and counts.
	if got := code(func() error { _, err := r.Synced(s, "A", "msg/1.md", t0); return err }()); got != "no_worktree" {
		t.Fatalf("a sync of a task in a shared folder: %q", got)
	}
	s.Tasks["A"].Worktree = &contract.Worktree{Path: "wt"}
	r.Synced(s, "A", "msg/1.md", t0)
	if n, err := r.Synced(s, "A", "msg/2.md", t0); n != 2 || err != nil || s.Tasks["A"].SyncBrief != "msg/2.md" {
		t.Fatalf("the second sync: %d, %v, %+v", n, err, s.Tasks["A"])
	}
	// A finding that went away and came back is new again.
	r.Found(s, nil, t0)
	if fresh := r.Found(s, []contract.Finding{clash}, t0); len(fresh) != 1 || len(s.Run.Findings) != 1 {
		t.Fatalf("a finding that came back: %+v", fresh)
	}
	// A change in the project's own folder is no task's: one scope event
	// that names none, and nothing else without a task is an event.
	from = len(s.Inbox.Events)
	own := contract.Finding{Kind: "scope", Files: []string{"d.go"}, How: "changed in the project's own folder"}
	r.Found(s, []contract.Finding{clash, own, {Kind: "same", Files: []string{"e.go"}}}, t0)
	r.Found(s, []contract.Finding{clash, own}, t0)
	if got := kinds(s, from); len(got) != 1 || got[0] != "scope" || s.Inbox.Events[from].Task != "" || s.Inbox.Events[from].Text != own.How {
		t.Fatalf("a change in the project's own folder raised %v", got)
	}
}
