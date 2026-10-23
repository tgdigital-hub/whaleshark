package runtask_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Ten readonly tasks start with no copy of the code, no setup and no ports,
// in the project's own folder, told they are read only; one with
// --worktree detached gets a copy of the base with no branch and no setup.
// Each is accepted by its result file, with no check.
func TestTenReadonlyTasksStartBare(t *testing.T) {
	scripts := map[string]string{"D.1": "write-result | report done \"read\""}
	var ids []string
	for i := range 10 {
		id := fmt.Sprintf("L%d", i)
		ids, scripts[id+".1"] = append(ids, id), "write-result | report done \"read\""
	}
	h := open(t, scripts)
	h.approve("[setup]\nscript = \"touch set-up\"\nscript_windows = \"type nul > set-up\"\n[limits]\nagents = 20\n")
	h.orch(2, "task", "add", "X", "look", "--brief", "b.md", "--kind", "readonly", "--check", "make test")("no check")
	h.orch(2, "task", "add", "X", "look", "--brief", "b.md", "--kind", "watch")("--limit 2h")
	h.orch(2, "task", "add", "X", "look", "--brief", "b.md", "--check", "none", "--worktree", "detached")("readonly, review and watch")
	for _, id := range ids {
		h.orch(0, "task", "add", id, "look "+id, "--brief", "b.md", "--kind", "readonly")("ready")
	}
	h.orch(0, "task", "add", "D", "look D", "--brief", "b.md", "--kind", "readonly", "--worktree", "detached")("ready")
	h.orch(0, append([]string{"start"}, append(ids, "D")...)...)()
	h.reported(append(ids, "D")...)

	s, dir := h.p.Record()
	for _, id := range ids {
		task, a := s.Tasks[id], s.Attempts[id+".1"]
		if task.Worktree != nil || task.Check != contract.CheckNone || task.Placement != contract.PlaceShared || h.p.Kit.Platform.PathKey(a.Place.Cwd) != h.p.Kit.Platform.PathKey(h.p.Root) {
			t.Fatalf("%s has the copy %+v, the check %q and the folder %s", id, task.Worktree, task.Check, a.Place.Cwd)
		}
	}
	if _, err := os.Stat(filepath.Join(h.p.Root, "set-up")); err == nil {
		t.Error("the setup ran for a task that changes no code")
	}
	dirs, _ := h.p.Kit.Platform.Dirs()
	if taken, _ := contract.ReadSlots(os.ReadFile, dirs.State); len(taken) != 0 {
		t.Errorf("tasks that change no code took the ports %+v", taken)
	}
	prompt := read(t, filepath.Join(contract.AttemptDir(dir, "L0.1"), "prompt.md"))
	if !strings.Contains(prompt, "This task changes no code") || strings.Contains(prompt, "commit everything") {
		t.Errorf("a readonly worker's prompt:\n%s", prompt)
	}
	w := s.Tasks["D"].Worktree
	if w == nil || w.Branch != "" || w.Setup != contract.SetupNone || h.git("-C", w.Path, "rev-parse", "HEAD") != h.git("rev-parse", "main") {
		t.Fatalf("D has the copy %+v", w)
	}
	if _, err := os.Stat(filepath.Join(w.Path, "set-up")); err == nil {
		t.Error("the setup ran in a detached copy")
	}

	h.orch(5, "accept", "L0", "--by-hand", "read it")("by its result file")
	h.orch(0, "accept", "L0")("L0 is done, by its result file")
	h.orch(0, "accept", "D")("D is done")
	if s, _ := h.p.Record(); s.Tasks["L0"].Accepted.How != contract.AcceptResult || h.collected() != "" {
		t.Fatalf("L0 was accepted as %+v; the run's branch holds %q", s.Tasks["L0"].Accepted, h.collected())
	}
	h.orch(0, "close", "D")("removed")
	if h.git("branch", "--format=%(refname:short)") != "main\nwhaleshark/r1" {
		t.Errorf("a detached copy left the branches %q", h.git("branch", "--format=%(refname:short)"))
	}
}

// A review starts once its task has reported, and holds that task's accept
// until it says safe: a result with no verdict is not accepted, a fix-first
// is told to the lead agent with its list, and a later review that says
// safe lets the task in.
func TestAReviewHoldsTheTaskUntilSafe(t *testing.T) {
	h := open(t, map[string]string{"T3.1": work("T3"), "R1.1": "write-result | report done \"read\"", "R2.1": "write-result | report done \"read\""})
	h.orch(2, "task", "add", "R1", "a review", "--brief", "b.md", "--kind", "review")("--reviews T3")
	h.add(map[string]string{"T3": "test -f t3.txt"}, "T3")
	h.orch(0, "task", "add", "R1", "a review", "--name", "review one", "--brief", "b.md", "--kind", "review", "--reviews", "T3")()
	h.reported("T3")
	h.orch(0, "start", "R1")()
	h.reported("R1")
	_, dir := h.p.Record()
	prompt := read(t, filepath.Join(contract.AttemptDir(dir, "R1.1"), "prompt.md"))
	if !strings.Contains(prompt, "You review task T3") || !strings.Contains(prompt, "whaleshark/r1-T3-part-t3") || !strings.Contains(prompt, "verdict: fix-first") {
		t.Fatalf("the reviewer's prompt:\n%s", prompt)
	}
	h.orch(5, "accept", "T3")("waits for the verdict of its review R1")
	h.orch(5, "accept", "R1")("gives no verdict")

	result := func(id, text string) {
		write(t, filepath.Join(contract.AttemptDir(dir, id+".1"), "result.md"), text)
	}
	result("R1", "Read the diff.\n\nverdict: fix-first\n- the limit is off by one\n- no test of an empty list\n")
	h.orch(0, "accept", "R1")("R1 is done", "to be fixed first", "- no test of an empty list")
	s, _ := h.p.Record()
	if e := s.Inbox.Events[len(s.Inbox.Events)-1]; e.Kind != "fix_first" || !strings.Contains(e.Text, "the limit is off by one; no test of an empty list") {
		t.Fatalf("the last event is %+v", e)
	}
	h.orch(5, "accept", "T3")("waits for the verdict of its review R1")

	h.orch(0, "task", "add", "R2", "a review", "--name", "review two", "--brief", "b.md", "--kind", "review", "--reviews", "T3")("ready")
	h.orch(0, "start", "R2")()
	h.reported("R2")
	time.Sleep(time.Second) // R2 is settled after R1, by the record's clock
	result("R2", "verdict: safe\n")
	h.orch(0, "accept", "R2")("T3 is safe")
	h.orch(0, "accept", "T3")("T3 is done, checked")
	if got := h.collected(); got != "T3: the T3 part" {
		t.Fatalf("the run's branch holds %q", got)
	}
}
