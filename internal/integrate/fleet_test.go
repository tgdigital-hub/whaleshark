package integrate_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
)

// fleet gives the run a fleet as a start of its first group does: a run of
// its own that begins at the collected work, with its branch written into
// the orchestrator's record. It returns a folder that stands on that branch,
// where the fleet's accepted tasks are committed.
func (r *run) fleet(name, id string) string {
	r.t.Helper()
	up := r.state()
	low := r.k.Rules.NewRun(id, name, 0, &contract.GitRef{Ref: up.Run.Integration.Branch, OID: up.Run.Integration.Tip}, contract.Binding{}, contract.Now())
	low.Run.Parent = &contract.Twin{Run: "r1", Fleet: name}
	_, in, err := r.k.Integrator.Begin(r.root, low)
	must(r.t, err)
	must(r.t, r.k.Rules.Integrated(low, *in))
	must(r.t, r.k.Store.Create(r.root, low))
	r.change(func(s *contract.State) {
		if s.Run.Mode = contract.ModeFleets; s.Run.Fleets == nil {
			s.Run.Fleets = map[string]*contract.Fleet{}
		}
		s.Run.Fleets[name] = &contract.Fleet{Run: id, Branch: in.Branch}
	})
	dir := filepath.Join(r.work, id)
	r.git(r.root, "worktree", "add", "-q", dir, in.Branch)
	return dir
}

// group leaves a group of a fleet waiting to be checked.
func (r *run) group(id, fleet string) {
	r.change(func(s *contract.State) {
		s.Tasks[id] = &contract.Task{ID: id, Name: "group " + id, Title: "the " + id + " group", Status: contract.TaskReview, Check: "exit 0",
			Fleet: fleet, Placement: contract.PlaceFleet, Attempts: []string{id + ".1"}}
		s.Attempts[id+".1"] = &contract.Attempt{ID: id + ".1", Task: id, N: 1, State: contract.AttemptReported, Report: &contract.Report{Outcome: contract.ReportDone}}
	})
}

// A group is collected as one merge of its fleet's branch: the fleet's own
// commits are kept, the merge carries the group's trailer, and the check runs
// on the fleet's work together with everything collected before it.
func TestAGroupIsCollectedAsAMerge(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n", "cart.js": "1\n2\n3\n"})
	billing := r.fleet("billing", "r2")
	testkit.Commit(t, billing, "T1: the invoice", map[string]string{"invoice.txt": "i\n"})
	testkit.Commit(t, billing, "T2: the refund", map[string]string{"refund.txt": "r\n"})
	r.group("G1", "billing")
	// Something else was collected meanwhile, so the tip is past where the fleet began.
	r.task("A", map[string]string{"a.txt": "two\n"})
	other, err := r.accept("A", nil)
	must(t, err)

	diff, err := r.k.Integrator.Diff(r.root, r.state(), "G1", true)
	if err != nil || !strings.Contains(diff, "invoice.txt") || strings.Contains(diff, "a.txt") {
		t.Fatalf("the diff of a group that waits is its fleet's work: %q, %v", diff, err)
	}
	seen := ""
	made, err := r.accept("G1", func(dir string) bool {
		seen = dir
		return has(dir, "invoice.txt") && has(dir, "refund.txt") && strings.Contains(r.git(dir, "show", "HEAD:a.txt"), "two")
	})
	must(t, err)
	if seen == "" || seen == billing || seen == r.root {
		t.Fatalf("the check of a group ran in %q, not in the run's own copy", seen)
	}
	head := r.git(billing, "rev-parse", "HEAD")
	if got := r.git(r.root, "rev-list", "--parents", "-1", made); got != made+" "+other+" "+head {
		t.Fatalf("the group's commit is no merge of the collected work and the fleet's branch: %s", got)
	}
	if got := r.git(r.root, "log", "--format=%s", other+".."+made); got != "G1: the G1 group\nT2: the refund\nT1: the invoice" {
		t.Fatalf("the fleet's commits were not kept:\n%s", got)
	}
	if got := r.git(r.root, "log", "-1", "--format=%(trailers:key=Whaleshark-Task,valueonly,separator=)", made); got != "r1/G1" || r.branch() != made {
		t.Fatalf("the merge carries %q and the branch is at %s", got, r.branch())
	}
	if stat, err := r.k.Integrator.Diff(r.root, r.state(), "G1", true); err != nil || !strings.Contains(stat, "refund.txt") || strings.Contains(stat, "a.txt") {
		t.Fatalf("the diff of a collected group: %q, %v", stat, err)
	}

	// A second group of the fleet is collected from its branch as it then stands.
	testkit.Commit(t, billing, "T3: the receipt", map[string]string{"receipt.txt": "r\n"})
	r.group("G2", "billing")
	second, err := r.accept("G2", func(dir string) bool { return has(dir, "receipt.txt") })
	must(t, err)
	if got := r.git(r.root, "log", "--format=%s", made+".."+second); got != "G2: the G2 group\nT3: the receipt" {
		t.Fatalf("the second group brought:\n%s", got)
	}

	// An accept cut off after it moved the branch: the next finds its merge.
	testkit.Commit(t, billing, "T4: the tax", map[string]string{"tax.txt": "t\n"})
	r.group("G3", "billing")
	c, err := r.k.Integrator.Prepare(r.root, r.state(), "G3")
	must(t, err)
	cut, err := r.k.Integrator.Collect(r.root, r.state(), "G3", c)
	must(t, err)
	if again, err := r.accept("G3", nil); err != nil || again != cut || r.branch() != cut {
		t.Fatalf("after a cut-off accept the group was collected as %s, %v; the first was %s", again, err, cut)
	}

	// Another fleet changed a line that billing changed too: nothing moves,
	// and the refusal names the files and the cure.
	search := r.fleet("search", "r3")
	testkit.Commit(t, billing, "T5: the cart", map[string]string{"cart.js": "1\nbilling\n3\n"})
	testkit.Commit(t, search, "T1: the cart", map[string]string{"cart.js": "1\nsearch\n3\n"})
	r.group("G4", "billing")
	r.group("G5", "search")
	_, err = r.accept("G4", nil)
	must(t, err)
	tip := r.branch()
	_, err = r.accept("G5", nil)
	var refusal *contract.Refusal
	if code(err) != "conflict" || r.branch() != tip {
		t.Fatalf("a group that clashes: %v, branch at %s", err, r.branch())
	} else if refusal = err.(*contract.Refusal); !strings.Contains(refusal.Message, "cart.js") || refusal.Next[0] != "whaleshark sync G5" {
		t.Fatalf("the refusal is %+v", refusal)
	}
	// A worker of the fleet merges the collected work into the fleet's branch
	// and settles the clash; then the group is collected.
	r.git(search, "merge", "-q", "-X", "ours", "-m", "T2: brought up to date", tip)
	if _, err = r.accept("G5", func(dir string) bool { return strings.Contains(r.git(dir, "show", "HEAD:cart.js"), "search") }); err != nil {
		t.Fatal(err)
	}

	// A fleet's branch goes with its run once all of it is collected above,
	// and stays, and is named, while it holds work that is not.
	end := func(id string) (string, error) {
		must(t, r.k.Store.Change(r.root, id, func(s *contract.State) error { s.Run.ClosedAt = contract.Now(); return nil }))
		low, err := r.k.Store.Read(r.root, id)
		must(t, err)
		return r.k.Integrator.Drop(r.root, low)
	}
	r.git(r.root, "worktree", "remove", "--force", search)
	if kept, err := end("r3"); err != nil || kept != "" || r.git(r.root, "branch", "--list", "whaleshark/r3") != "" {
		t.Fatalf("the branch of a fleet whose work is collected: kept %q, %v", kept, err)
	}
	testkit.Commit(t, billing, "T6: not collected", map[string]string{"late.txt": "l\n"})
	r.git(r.root, "worktree", "remove", "--force", billing)
	if kept, err := end("r2"); err != nil || kept != "whaleshark/r2" {
		t.Fatalf("the branch of a fleet with work that was not collected: kept %q, %v", kept, err)
	}
}
