package mail_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// morning is the fixture of three fleets as a project on disk, with a token
// for every attempt. The leader of billing holds a second group, G4, beside
// G1, and was last seen resting.
func morning(t *testing.T) *scenario.Project {
	t.Helper()
	f, p := testkit.Fleets(), scenario.Prepare(t, nil, nil)
	runs := f.In.Projects[0].Runs
	up := runs[0]
	one, first := *up.Tasks["G1"], *up.Attempts["G1.1"]
	one.ID, one.Name, one.Attempts = "G4", "billing two", []string{"G4.1"}
	first.ID, first.Task, first.Seen.Status = "G4.1", "G4", contract.StatusIdle
	up.Tasks["G4"], up.Attempts["G4.1"] = &one, &first
	up.Run.Fleets["billing"].Groups = []string{"G1", "G4"}
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
	p.Double.Load(&contract.Snapshot{Panes: append(f.In.Terms.Panes, contract.Pane{ID: p.Own, Tab: "w1:t0", Label: "you", Cwd: p.Root})})
	return p
}

// in is one real command from a pane of the morning, as the attempt its
// variables name or, with none, as whoever the pane is; it wants this exit
// code and these words.
func in(t *testing.T, p *scenario.Project, pane, run, attempt string, exit int, want string, args ...string) {
	t.Helper()
	cmd := p.Command(scenario.Unbound, args...)
	if cmd.Env = append(cmd.Env, contract.EnvPane+"="+pane); attempt != "" {
		cmd.Env = append(cmd.Env, contract.EnvRoot+"="+p.Root, contract.EnvRun+"="+run, contract.EnvAttempt+"="+attempt)
	}
	out, _ := cmd.CombinedOutput()
	if got := cmd.ProcessState.ExitCode(); got != exit || !strings.Contains(string(out), want) {
		t.Fatalf("%s: %v: exit %d, want %d and %q\n%s", pane, args, got, exit, want, out)
	}
}

// humans is the person's doing as a run's lead agent is told it: what, and
// in which fleet.
func humans(t *testing.T, p *scenario.Project, run string) (out []string) {
	t.Helper()
	s, err := p.Kit.Store.Read(p.Root, run)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range s.Inbox.Events {
		if e.Kind == "human" {
			what, _ := e.Data["what"].(string)
			fleet, _ := e.Data["fleet"].(string)
			out = append(out, strings.TrimSpace(what+" "+fleet))
		}
	}
	return out
}

// A message to a group is its leader's, whichever of its groups it is for:
// the leader reads them all with the one command of its one tab, in the
// order they were sent. A message into a fleet's run reaches a worker
// there, from its leader and from the person, and from nobody above; and
// what the person says past the orchestrator the orchestrator is told.
func TestTellAGroupAndIntoAFleet(t *testing.T) {
	p := morning(t)
	orch := func(exit int, want string, args ...string) { t.Helper(); in(t, p, "p1", "", "", exit, want, args...) }
	leader := func(exit int, want string, args ...string) {
		t.Helper()
		in(t, p, "p2", "r1", "G1.1", exit, want, args...)
	}

	orch(0, "Stored as message 1 for G4. Pointer typed.", "tell", "G4", "the second group first")
	orch(0, "Stored as message 2 for G1. Pointer deferred: until it is idle.", "tell", "billing", "then the first")
	if got := pointers(p); len(got) != 1 || !strings.HasPrefix(got[0], "p2") || !strings.HasSuffix(got[0], string(contract.PointMail)) {
		t.Fatalf("typed into the leader's tab: %q", got)
	}
	leader(0, "[1] the second group first\n[2] then the first\n", "mail")
	leader(0, "No new messages.", "mail")
	// A group sent back: its reason is in the same mail.
	if err := p.Kit.Store.Change(p.Root, "r1", func(s *contract.State) error {
		s.Attempts["G4.1"].State, s.Tasks["G4"].Status = contract.AttemptReported, contract.TaskReview
		s.Attempts["G4.1"].Seen.Liveness = contract.Live
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	orch(0, "G4 is back with G4.1 as message 3.", "reject", "G4", "the refunds are not tested")
	leader(0, "[3] ", "mail")

	// Into the fleet's run: the leader, the person, and not the orchestrator.
	leader(0, "Stored as message 1 for T3.", "tell", "T3", "round half up")
	orch(5, "", "tell", "T3", "round half down", "--run", "r2")
	human := p.Command(scenario.Human, "tell", "tax rounding", "ask me before you touch the tables", "--run", "r2")
	if out, err := human.CombinedOutput(); err != nil || !strings.Contains(string(out), "Stored as message 2 for T3.") {
		t.Fatalf("the person's message into the fleet: %v\n%s", err, out)
	}
	in(t, p, "p5", "r2", "T3.1", 0, "[1] round half up\n[2] ask me before you touch the tables\n", "mail")
	note := p.Command(scenario.Human, "tell", "lead", "leave the tax tables to me", "--run", "r2")
	if out, err := note.CombinedOutput(); err != nil {
		t.Fatalf("the person's note to the leader: %v\n%s", err, out)
	}
	if up, low := humans(t, p, "r1"), humans(t, p, "r2"); !slices.Equal(up, []string{"tell billing", "tell lead billing"}) ||
		!slices.Equal(low, []string{"tell billing", "note"}) {
		t.Fatalf("the person's doing past the orchestrator: above %q, in the fleet %q", up, low)
	}
}
