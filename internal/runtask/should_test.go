package runtask_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// A task's card is a short id of the person's, kept with the task; with
// [tasks] close_on_accept an accepted task gives up its tab and its copy of
// the code at once, and its branch stays.
func TestACardAndCloseOnAccept(t *testing.T) {
	h := open(t, map[string]string{"A.1": work("A"), "B.1": work("B")})
	h.orch(2, "task", "add", "A", "the A part", "--brief", "b.md", "--check", "none", "--ref", "a card with spaces")("--ref is a card's id")
	h.orch(0, "task", "add", "A", "the A part", "--name", "part A", "--brief", "b.md", "--check", "test -f a.txt", "--ref", "C-42")("ready")
	if s, _ := h.p.Record(); s.Tasks["A"].Ref != "C-42" {
		t.Fatalf("the card is kept as %q", s.Tasks["A"].Ref)
	}
	h.approve("[tasks]\nclose_on_accept = true\n")
	h.orch(0, "start", "A")()
	h.reported("A")
	h.orch(0, "accept", "A")("A is done, checked", "closed: part A", "removed: the copy of the code of A")
	if left := h.git("worktree", "list"); strings.Contains(left, "r1-A") || h.git("branch", "--list", "whaleshark/r1-A-part-a") == "" {
		t.Fatalf("the copy of the code stayed, or the branch went:\n%s", left)
	}

	// Without it, the tab and the copy stay until close.
	h.approve("")
	h.add(map[string]string{"B": "test -f b.txt"}, "B")
	h.reported("B")
	if out := h.orch(0, "accept", "B")("B is done, checked"); strings.Contains(out, "closed:") {
		t.Fatalf("a tab was closed with the setting off:\n%s", out)
	}
}

// [check] parallel holds a check back while as many run as it allows, over
// every project of the login, and lets it run once one has ended.
func TestChecksAtOnce(t *testing.T) {
	h := open(t, map[string]string{"A.1": work("A")})
	h.approve("[check]\nparallel = 1\n")
	h.add(map[string]string{"A": "test -f a.txt"}, "A")
	h.reported("A")
	dirs, err := h.p.Kit.Platform.Dirs()
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dirs.State, "checks", ".keep"), "")
	unlock, free, err := h.p.Kit.Platform.TryLock(filepath.Join(dirs.State, "checks", "0.lock"))
	if err != nil || !free {
		t.Fatalf("the one place could not be taken: %v", err)
	}
	cmd := h.p.Command(scenario.Orch, "accept", "A")
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	if s, _ := h.p.Record(); s.Tasks["A"].Status == contract.TaskDone {
		t.Fatal("the check ran while its one place was taken")
	}
	unlock()
	if err := cmd.Wait(); err != nil || !strings.Contains(out.String(), "waits for a free place") || !strings.Contains(out.String(), "A is done, checked") {
		t.Fatalf("accept after the place came free: %v\n%s", err, out.String())
	}
}
