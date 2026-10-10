package runtask_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// shop is a project that is a real git repository with a run open, and its
// lead agent. Each fake agent works in the copy of the code start gives it.
type shop struct {
	t *testing.T
	p *scenario.Project
}

func open(t *testing.T, scripts map[string]string) *shop {
	t.Helper()
	p := scenario.Prepare(t, nil, scripts)
	testkit.Git(t, p.Root, "init", "-q", "-b", "main")
	testkit.Git(t, p.Root, "config", "user.name", "a test")
	testkit.Git(t, p.Root, "config", "user.email", "test@localhost")
	write(t, filepath.Join(p.Root, "local.env"), "not for git\n")
	testkit.Commit(t, p.Root, "start", map[string]string{"b.md": briefText, ".gitignore": "local.env\nwhaleshark.toml\n"})
	h := &shop{t, p}
	h.orch(0, "init")()
	h.orch(0, "run", "new", "a shop")("r1")
	return h
}

func (h *shop) orch(exit int, args ...string) func(words ...string) string {
	return func(words ...string) string {
		h.t.Helper()
		return play(h.t, h.p.Command(scenario.Orch, args...), exit, words...)
	}
}

// add adds tasks, each with the check given, and starts them.
func (h *shop) add(check map[string]string, ids ...string) string {
	h.t.Helper()
	for _, id := range ids {
		h.orch(0, "task", "add", id, "the "+id+" part", "--name", "part "+id, "--brief", "b.md", "--check", check[id])()
	}
	return h.orch(0, append([]string{"start"}, ids...)...)()
}

// reported waits until each attempt has reported.
func (h *shop) reported(ids ...string) {
	h.t.Helper()
	for end := time.Now().Add(20 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		s, _ := h.p.Record()
		n := 0
		for _, id := range ids {
			if a := s.Attempts[id+".1"]; a != nil && a.State == contract.AttemptReported {
				n++
			}
		}
		if n == len(ids) {
			return
		}
		if time.Now().After(end) {
			h.t.Fatalf("not every one of %v has reported", ids)
		}
	}
}

func (h *shop) git(args ...string) string { return testkit.Git(h.t, h.p.Root, args...) }

// collected is the titles of the commits on the run's branch, oldest first.
func (h *shop) collected() string {
	return strings.ReplaceAll(h.git("log", "--reverse", "--format=%s", "main..whaleshark/r1"), "\n", "; ")
}

// approve writes the project's settings and approves them, as trust does.
func (h *shop) approve(toml string) {
	h.t.Helper()
	write(h.t, filepath.Join(h.p.Root, "whaleshark.toml"), toml)
	lines, err := contract.TrustLines(h.p.Root)
	if err == nil {
		err = contract.WriteTrust(h.p.Kit.Platform, h.p.Root, lines, contract.Now())
	}
	if err != nil {
		h.t.Fatal(err)
	}
}

func work(id string) string {
	return "edit " + strings.ToLower(id) + ".txt | commit | write-result | report done \"" + id + " is in\""
}

// Several tasks accepted at once (8a): the three refusals, one merged copy
// checked by the run's check and by each task's own, each task its own
// commit, and the fall-back to one at a time that finds the one that breaks.
func TestAcceptSeveral(t *testing.T) {
	scripts := map[string]string{"E.1": "edit broken.txt | commit | write-result | report done \"E is in\""}
	check := map[string]string{"E": "test ! -f broken.txt", "G": "test ! -f f.txt", "N": "none"}
	for _, id := range []string{"A", "B", "C", "D", "F", "G", "H", "I"} {
		scripts[id+".1"] = work(id)
		if check[id] == "" {
			check[id] = "test -f " + strings.ToLower(id) + ".txt"
		}
	}
	h := open(t, scripts)
	// The first copy of the code a run makes says what git does not carry.
	if out := h.add(check, "A", "B"); !strings.Contains(out, "1 ignored files or folders") || !strings.Contains(out, "local.env") || strings.Count(out, "ignored") != 1 {
		t.Fatalf("the first worktrees of a run say what they lack, once:\n%s", out)
	}
	h.add(check, "C", "D", "E", "F", "G", "H", "I")
	h.orch(0, "task", "add", "N", "no check", "--name", "by hand", "--brief", "b.md", "--check", "none")()
	h.reported("A", "B", "C", "D", "E", "F", "G", "H", "I")

	// Each worker's hooks are in its own folder, and git does not see them:
	// nothing of ours is in a worker's commit.
	s, _ := h.p.Record()
	tree := s.Tasks["A"].Worktree
	if tree == nil || tree.Branch != "whaleshark/r1-A-part-a" || s.Tasks["N"].Placement != contract.PlaceWorktree {
		t.Fatalf("A has the copy %+v", tree)
	}
	if _, err := os.Stat(filepath.Join(tree.Path, ".claude", "settings.local.json")); err != nil {
		t.Errorf("the worker's settings file is not in its folder: %v", err)
	}
	if files := h.git("ls-tree", "-r", "--name-only", tree.Branch); files != ".gitignore\na.txt\nb.md" {
		t.Errorf("A's branch holds %q", files)
	}

	// The three refusals. No run check; one nobody approved; a task with none.
	h.orch(5, "accept", "A", "B")("the run's check", "has none")
	write(t, filepath.Join(h.p.Root, "whaleshark.toml"), "[land]\ncheck = \"test ! -f broken.txt\"\n")
	h.orch(5, "accept", "A", "B")("nobody has approved", "whaleshark trust")
	h.approve("[land]\ncheck = \"test ! -f broken.txt\"\n")
	h.orch(5, "accept", "A", "N", "B")("N has no check", "by hand")
	if s, _ := h.p.Record(); s.Attempts["A.1"].State != contract.AttemptReported || h.collected() != "" {
		t.Fatal("a refused batch left something behind")
	}

	// Five of which one breaks the run's check: nothing is collected
	// together, and one at a time four are, the fifth's own check failing.
	out := h.orch(1, "accept", "A", "B", "C", "D", "E")("Together they are not accepted", "broken.txt", "Nothing was collected", "4 of 5")
	if got := h.collected(); got != "A: the A part; B: the B part; C: the C part; D: the D part" {
		t.Fatalf("after five of which one breaks the branch holds %q\n%s", got, out)
	}
	if s, _ := h.p.Record(); s.Tasks["E"].Status != contract.TaskReview || s.Attempts["E.1"].Check == nil || s.Attempts["E.1"].Check.OK {
		t.Fatalf("E is %s", s.Tasks["E"].Status)
	}

	// A task whose own check fails on the merged work is not collected by a
	// batch, though the run's check passes: G's wants F's file absent.
	h.orch(1, "accept", "F", "G")("Together they are not accepted", `the check "test ! -f f.txt" failed`, "1 of 2")
	if s, _ := h.p.Record(); s.Tasks["F"].Status != contract.TaskDone || s.Tasks["G"].Status != contract.TaskReview {
		t.Fatalf("F is %s and G is %s", s.Tasks["F"].Status, s.Tasks["G"].Status)
	}

	// Two that pass together: one commit each, in the order given, and a
	// task named twice is taken once.
	h.orch(0, "accept", "I", "H", "I")("I is done, checked", "H is done, checked")
	if got := h.collected(); !strings.HasSuffix(got, "F: the F part; I: the I part; H: the H part") {
		t.Fatalf("the branch holds %q", got)
	}
	s, _ = h.p.Record()
	if tip := h.git("rev-parse", "whaleshark/r1"); s.Run.Integration.Tip != tip || s.Tasks["H"].Accepted.Commit != tip || h.git("ls-tree", "--name-only", tip) != ".gitignore\na.txt\nb.md\nb.txt\nc.txt\nd.txt\nf.txt\nh.txt\ni.txt" {
		t.Fatalf("the record has the tip %s, the branch %s", s.Run.Integration.Tip, tip)
	}

	// close gives back the copy of a task that is done, and keeps its
	// branch; one that is cancelled keeps its copy until --discard.
	h.orch(0, "close", "--settled")("removed: the copy of the code of A", "its branch whaleshark/r1-A-part-a stays")
	if _, err := os.Stat(tree.Path); err == nil || h.git("rev-parse", "--verify", "-q", tree.Branch) == "" {
		t.Error("A's copy is still there, or its branch is not")
	}
	for _, id := range []string{"G", "E"} {
		h.orch(0, "stop", id)()
		h.orch(0, "task", "cancel", id)()
	}
	h.orch(0, "close", "G")("kept: the copy of the code of G", "close G --discard")
	h.orch(0, "close", "G", "--discard")("removed: the copy of the code of G")
	h.orch(0, "task", "cancel", "N")()

	// run rm takes what is left: E's copy and every branch proven merged.
	// E's work was never collected, so its branch is kept and named; the
	// run's own branch was not landed and stays too.
	h.orch(0, "run", "close", "--abandon")()
	play(t, h.p.Command(scenario.Human, "run", "rm", "r1"), 0, "Kept, not proven merged: whaleshark/r1-E-part-e", "whaleshark/r1-G-part-g", "removed")
	if left := h.git("worktree", "list"); strings.Count(left, "\n") != 0 {
		t.Errorf("run rm left worktrees:\n%s", left)
	}
	if b := h.git("branch", "--format=%(refname:short)"); b != "main\nwhaleshark/r1\nwhaleshark/r1-E-part-e\nwhaleshark/r1-G-part-g" {
		t.Errorf("after run rm the branches are:\n%s", b)
	}
}

// report done refuses work that is not committed, and saves nothing of the
// report; a setup that nobody approved is said once and not run, and one
// that fails refuses the start and leaves no copy behind.
func TestCommitFirstAndSetup(t *testing.T) {
	h := open(t, map[string]string{"A.1": "edit a.txt | write-result | hang", "B.1": "hang"})
	write(t, filepath.Join(h.p.Root, "whaleshark.toml"), "[setup]\nscript = \"exit 1\"\nscript_windows = \"exit 1\"\n")
	check := map[string]string{"A": "exit 0", "B": "exit 0", "C": "exit 0"}
	h.add(check, "A", "B")
	s, _ := h.p.Record()
	dir, n := s.Tasks["A"].Worktree.Path, 0
	for _, e := range s.Inbox.Events {
		if e.Kind == "untrusted" {
			n++
		}
	}
	if n != 1 || s.Tasks["A"].Worktree.Setup != contract.SetupUntrusted {
		t.Fatalf("a setup nobody approved raised %d events; A's copy says %q", n, s.Tasks["A"].Worktree.Setup)
	}
	for end := time.Now().Add(20 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(dir, "a.txt")); err == nil {
			break
		} else if time.Now().After(end) {
			t.Fatal("the agent never wrote its file")
		}
	}
	as := func(exit int, words ...string) {
		t.Helper()
		play(t, h.p.Command("A.1", "report", "done", "A is in"), exit, words...)
	}
	as(1, "Commit first")
	if s, _ := h.p.Record(); s.Attempts["A.1"].State != contract.AttemptWorking || s.Attempts["A.1"].Report != nil {
		t.Fatal("a refused report was saved")
	}
	testkit.Commit(t, dir, "the work of A", nil)
	as(0, "recorded")

	h.approve("[setup]\nscript = \"exit 1\"\nscript_windows = \"exit 1\"\n")
	h.orch(0, "task", "add", "C", "the C part", "--name", "part C", "--brief", "b.md", "--check", "exit 0")()
	h.orch(1, "start", "C")("The setup of the copy of the code for C failed", "r1-C.setup.log")
	if s, _ := h.p.Record(); s.Tasks["C"].Worktree != nil || s.Tasks["C"].Status != contract.TaskReady || strings.Contains(h.git("worktree", "list"), "r1-C") {
		t.Fatalf("a start refused for its setup left C as %s with %+v", s.Tasks["C"].Status, s.Tasks["C"].Worktree)
	}
}
