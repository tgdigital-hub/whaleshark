package runtask_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// A brief's Time box is its task's max_minutes, which the stuck detector
// reads through For, and its Return is kept for the worker's prompt.
func TestBriefTimeBoxAndReturn(t *testing.T) {
	p := scenario.Prepare(t, nil, nil)
	orch := func(exit int, args ...string) func(words ...string) string {
		return func(words ...string) string { return play(t, p.Command(scenario.Orch, args...), exit, words...) }
	}
	orch(0, "run", "new", "a shop")("Run r1 is open")
	write(t, filepath.Join(p.Root, "late.md"), briefText+"## Time box\nsoon\n")
	orch(2, "task", "add", "A", "task A", "--brief", "late.md", "--check", "none")("Time box", "minutes from 1 to 240")
	write(t, filepath.Join(p.Root, "b.md"), briefText+"## Time box\n45 minutes\n## Return\nresult.md: what changed,\nthen how it was checked.\n")
	orch(0, "task", "add", "A", "task A", "--brief", "b.md", "--check", "none")("ready")
	s, _ := p.Record()
	a := s.Tasks["A"]
	if a.MaxMinutes != 45 || a.Return != "result.md: what changed,\nthen how it was checked." {
		t.Fatalf("the task holds %d minutes and the return %q", a.MaxMinutes, a.Return)
	}
	if got := contract.ProjectDefaults().For(a).Limits.MaxMinutes; got != 45 {
		t.Fatalf("the task's limits hold %d minutes", got)
	}
	write(t, filepath.Join(p.Root, "b.md"), briefText+"Time box: 90\n")
	orch(0, "task", "edit", "A", "--brief", "b.md")("Changed: brief")
	if s, _ = p.Record(); s.Tasks["A"].MaxMinutes != 90 || s.Tasks["A"].Return != "" {
		t.Fatalf("after the edit the task holds %d minutes and the return %q", s.Tasks["A"].MaxMinutes, s.Tasks["A"].Return)
	}
}

// With results.mirror_dir approved, an accepted task's brief and result lie
// in its notes folder with DONE, and stay there when the run is closed.
func TestNotesFolderOnAccept(t *testing.T) {
	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	review(f, "T7", "echo all good")
	p := scenario.Prepare(t, f, nil)
	notes := t.TempDir()
	write(t, filepath.Join(p.Root, "whaleshark.toml"), "[results]\nmirror_dir = \""+filepath.ToSlash(notes)+"/{date}-{task}\"\n")
	trust := p.Command(scenario.Human, "trust")
	trust.Stdin = strings.NewReader("yes\n")
	play(t, trust, 0, "+ results.mirror_dir", "Approved.")
	s, dir := p.Record()
	task := s.Tasks["T7"]
	write(t, filepath.Join(dir, filepath.FromSlash(task.Brief)), briefText)
	write(t, filepath.Join(contract.AttemptDir(dir, task.Attempts[len(task.Attempts)-1]), "result.md"), "It sends.\n")
	play(t, p.Command(scenario.Orch, "accept", "T7"), 0, "T7 is done, checked")

	project, _ := contract.ReadProjectFile(p.Root)
	folder := project.MirrorDir(s.Run.ID, task)
	if !strings.HasPrefix(folder, notes) || !strings.HasSuffix(folder, "-T7") {
		t.Fatalf("the notes folder is %s", folder)
	}
	look := func(when string) {
		t.Helper()
		for name, want := range map[string]string{"brief.md": "# Target", "result.md": "It sends.", contract.MirrorDone: "T"} {
			if data, err := os.ReadFile(filepath.Join(folder, name)); err != nil || !strings.Contains(string(data), want) {
				t.Fatalf("%s the notes folder's %s holds %q (%v)", when, name, data, err)
			}
		}
	}
	look("after the accept")
	// The rest of the evening's work is given up, so that the run can be closed.
	err = p.Kit.Store.Change(p.Root, s.Run.ID, func(s *contract.State) error {
		for _, a := range s.Attempts {
			if a.State.Live() || a.State == contract.AttemptReported {
				a.State = contract.AttemptExited
			}
		}
		for _, t := range s.Tasks {
			if t.Status != contract.TaskDone {
				t.Status = contract.TaskCancelled
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	play(t, p.Command(scenario.Orch, "run", "close"), 0)
	play(t, p.Command(scenario.Orch, "run", "rm", s.Run.ID), 0)
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("run rm left the run's folder behind")
	}
	look("after close and rm")
}
