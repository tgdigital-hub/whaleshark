package worker_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// morning is the fixture of three fleets as a project on disk, with a token
// for every attempt, after change. second gives the leader of billing a
// second group, G4, beside G1.
func morning(t *testing.T, change func(up *contract.State)) *scenario.Project {
	t.Helper()
	f, p := testkit.Fleets(), scenario.Prepare(t, nil, nil)
	runs := f.In.Projects[0].Runs
	change(runs[0])
	for _, s := range runs {
		for id, a := range s.Attempts {
			token := filepath.Join(p.Kit.Store.Dir(p.Root, s.Run.ID), "attempts", id, "token")
			if err := errors.Join(os.MkdirAll(filepath.Dir(token), 0o700), os.WriteFile(token, []byte("token-of-"+id+"\n"), 0o600)); err != nil {
				t.Fatal(err)
			}
			a.TokenHash = contract.TokenHash("token-of-" + id)
		}
		if err := p.Kit.Store.Create(p.Root, s); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(contract.ListProject(p.Kit.Platform, p.Root), p.Kit.Store.SetCurrent(p.Root, "r1")); err != nil {
		t.Fatal(err)
	}
	p.Double.Load(f.In.Terms)
	return p
}

func second(up *contract.State) {
	one, first := *up.Tasks["G1"], *up.Attempts["G1.1"]
	one.ID, one.Name, one.Attempts = "G4", "billing two", []string{"G4.1"}
	first.ID, first.Task = "G4.1", "G4"
	up.Tasks["G4"], up.Attempts["G4.1"] = &one, &first
	up.Run.Fleets["billing"].Groups = []string{"G1", "G4"}
}

// in runs the real program from a pane as the attempt of a run that the
// pane's variables name, and wants this exit code and these words.
func in(t *testing.T, p *scenario.Project, pane, run, attempt string, exit int, want string, args ...string) {
	t.Helper()
	cmd := p.Command(scenario.Unbound, args...)
	cmd.Env = append(cmd.Env, contract.EnvPane+"="+pane, contract.EnvRoot+"="+p.Root, contract.EnvRun+"="+run, contract.EnvAttempt+"="+attempt)
	out, _ := cmd.CombinedOutput()
	if got := cmd.ProcessState.ExitCode(); got != exit || !strings.Contains(string(out), want) {
		t.Fatalf("%s: %v: exit %d, want %d and %q\n%s", attempt, args, got, exit, want, out)
	}
}

func above(t *testing.T, p *scenario.Project) *contract.State {
	t.Helper()
	s, err := p.Kit.Store.Read(p.Root, "r1")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A leader that holds two groups in its one pane says which it means with
// --task: its tab's variables name the first for ever. Without it nothing
// is guessed, a group that is not its own is not found, and nobody but a
// leader has the flag.
func TestALeadersTask(t *testing.T) {
	p := morning(t, second)
	leader := func(exit int, want string, args ...string) {
		t.Helper()
		in(t, p, "p2", "r1", "G1.1", exit, want, args...)
	}
	leader(5, "with --task: G1, G4", "progress", "40", "half")
	leader(5, "with --task: G1, G4", "report", "done", "all of it")
	leader(5, "with --task: G1, G4", "progress", "40", "half", "--task", "G2")
	leader(0, "", "progress", "40", "half", "--task", "G4")
	if s := above(t, p); s.Attempts["G4.1"].Progress == nil || s.Attempts["G4.1"].Progress.Pct != 40 || s.Attempts["G1.1"].Progress != nil {
		t.Fatalf("progress of G4: %+v, of G1: %+v", s.Attempts["G4.1"].Progress, s.Attempts["G1.1"].Progress)
	}
	leader(1, filepath.Join("G4.1", "result.md"), "report", "done", "the second group", "--task", "G4")
	result := filepath.Join(p.Kit.Store.Dir(p.Root, "r1"), "attempts", "G4.1", "result.md")
	if err := os.WriteFile(result, []byte("done\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	leader(0, "recorded", "report", "done", "the second group", "--task", "G4")
	leader(0, "already recorded", "report", "done", "the second group", "--task", "G4")
	leader(0, "", "progress", "60", "on", "--task", "G1")
	s := above(t, p)
	if s.Tasks["G4"].Status != contract.TaskReview || s.Attempts["G4.1"].Report.Summary != "the second group" ||
		s.Tasks["G1"].Status != contract.TaskRunning || s.Attempts["G1.1"].Report != nil || s.Attempts["G1.1"].Progress.Pct != 60 {
		t.Fatalf("after the report: G4 %s, G1 %s %+v", s.Tasks["G4"].Status, s.Tasks["G1"].Status, s.Attempts["G1.1"])
	}
	// The token is the group's own: with another in its file nothing is taken.
	token := filepath.Join(p.Kit.Store.Dir(p.Root, "r1"), "attempts", "G1.1", "token")
	if err := os.WriteFile(token, []byte("token-of-G4.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	leader(5, "token", "progress", "70", "on", "--task", "G1")
	// A worker of the fleet, and the leader of a fleet with one group.
	in(t, p, "p3", "r2", "T1.1", 5, "--task is a fleet leader's", "progress", "5", "begun", "--task", "T1")
	in(t, p, "p8", "r1", "G2.1", 0, "", "progress", "30", "a third")
	in(t, p, "p8", "r1", "G2.1", 0, "", "progress", "35", "more", "--task", "G2")
	in(t, p, "p8", "r1", "G2.1", 5, "with --task: G2", "progress", "35", "more", "--task", "G1")
	if s := above(t, p); s.Attempts["G2.1"].Progress.Pct != 35 || s.Attempts["G1.1"].Progress.Pct != 60 {
		t.Fatalf("progress of G2: %+v", s.Attempts["G2.1"].Progress)
	}
}

// The group a leader was started for is accepted and it leads one other:
// what it says without --task is about that one.
func TestALeadersOneGroupLeft(t *testing.T) {
	p := morning(t, func(up *contract.State) {
		second(up)
		up.Attempts["G1.1"].State, up.Tasks["G1"].Status = contract.AttemptAccepted, contract.TaskDone
	})
	in(t, p, "p2", "r1", "G1.1", 0, "", "progress", "20", "the second group")
	if s := above(t, p); s.Attempts["G4.1"].Progress == nil || s.Attempts["G1.1"].Progress != nil {
		t.Fatalf("progress of G4: %+v, of G1: %+v", s.Attempts["G4.1"].Progress, s.Attempts["G1.1"].Progress)
	}
}
