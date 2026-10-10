package runtask_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// A repository that brings an approval of its own along, written into the
// project's folder of ours with the hash of its own settings, has approved
// nothing: its setup line is not run. The login keeps what a person approved.
func TestARepositorysOwnApprovalCountsForNothing(t *testing.T) {
	h := open(t, map[string]string{"A.1": work("A")})
	write(t, filepath.Join(h.p.Root, "whaleshark.toml"), "[setup]\nscript = \"echo ran>ran.txt\"\nscript_windows = \"echo ran>ran.txt\"\n")
	lines, err := contract.TrustLines(h.p.Root)
	if err != nil || len(lines) != 2 {
		t.Fatalf("the lines to approve: %q, %v", lines, err)
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	forged, _ := json.Marshal(map[string]any{"version": contract.FileVersion, "hash": hex.EncodeToString(sum[:]), "lines": lines, "at": contract.Now()})
	write(t, filepath.Join(h.p.Root, contract.ProjectDir, "trust.json"), string(forged))
	h.add(map[string]string{"A": "none"}, "A")
	s, _ := h.p.Record()
	w := s.Tasks["A"].Worktree
	if _, err := os.Stat(filepath.Join(w.Path, "ran.txt")); err == nil || w.Setup != contract.SetupUntrusted {
		t.Fatalf("the setup line of a repository that approved itself: %s, ran.txt %v", w.Setup, err)
	}
}

// A record that this login did not make, a run with a task whose check is a
// command of its author's, is not used, whichever way it came: no command
// changes or runs anything of it. The login's own list of projects decides,
// so a folder that came in an archive, which git knows nothing of, is
// refused like one a repository tracks, and init is the one way in.
func TestARecordThisLoginDidNotMakeIsNotUsed(t *testing.T) {
	p := scenario.Prepare(t, nil, nil)
	dirs, _ := p.Kit.Platform.Dirs()
	testkit.Git(t, p.Root, "init", "-q", "-b", "main")
	testkit.Commit(t, p.Root, "start", map[string]string{"b.md": briefText})
	orch := func(exit int, args ...string) string { return play(t, p.Command(scenario.Orch, args...), exit) }
	person := func(exit int, args ...string) string { return play(t, p.Command(scenario.Human, args...), exit) }
	// Its author made the run at home and committed the folder with it.
	orch(0, "run", "new", "planted")
	orch(0, "task", "add", "T1", "looks harmless", "--brief", "b.md", "--check", "echo ran>ran.txt")
	testkit.Git(t, p.Root, "add", "-f", contract.ProjectDir)
	testkit.Git(t, p.Root, "commit", "-q", "-m", "a record comes along")
	// Here it is at somebody else's: a login whose list does not hold it.
	arrive := func() {
		if err := contract.WriteProjects(p.Kit.Platform, dirs.State, nil); err != nil {
			t.Fatal(err)
		}
	}
	refused := func() {
		t.Helper()
		for _, args := range [][]string{{"task", "add", "T2", "more", "--brief", "b.md", "--check", "none"}, {"start", "T1"}, {"accept", "T1"}, {"run", "takeover"}, {"status"}} {
			if out := orch(contract.ExitEnv, args...); !strings.Contains(out, "this login did not make") {
				t.Fatalf("%v on a record that came from elsewhere:\n%s", args, out)
			}
		}
	}
	arrive()
	refused()
	if out := orch(contract.ExitEnv, "init"); !strings.Contains(out, "git rm -r --cached .whaleshark") {
		t.Fatalf("init on a record that came with the repository:\n%s", out)
	}
	// Taken out of git it is what an archive brings: a folder git does not
	// know. Nothing is used still, and a plain init does not take it on.
	testkit.Git(t, p.Root, "rm", "-r", "-q", "--cached", contract.ProjectDir)
	testkit.Git(t, p.Root, "commit", "-q", "-m", "out again")
	refused()
	if out := orch(contract.ExitEnv, "init"); !strings.Contains(out, "whaleshark init --adopt") {
		t.Fatalf("init on a record that came in an archive:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(p.Root, "ran.txt")); err == nil {
		t.Fatal("the check of a record that came from elsewhere ran")
	}
	// Only the person takes such a folder on, by saying so.
	orch(contract.ExitRefused, "init", "--adopt")
	person(0, "init", "--adopt", "--agent", "pi")
	orch(0, "task", "add", "T2", "more", "--brief", "b.md", "--check", "none")
	// A folder the program makes itself is the login's own without init.
	arrive()
	fresh := p.Root + "-fresh"
	if err := os.MkdirAll(fresh, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := p.Command(scenario.Orch, "run", "new", "mine")
	cmd.Dir = fresh
	play(t, cmd, 0)
	cmd = p.Command(scenario.Orch, "status")
	cmd.Dir = fresh
	play(t, cmd, 0)
	// Tracked again in a project that is set up, init and doctor still say so.
	person(0, "init", "--adopt", "--agent", "pi")
	testkit.Git(t, p.Root, "add", "-f", contract.ProjectDir)
	if out := orch(contract.ExitEnv, "init"); !strings.Contains(out, "git tracks") {
		t.Fatalf("init in a project whose record git tracks:\n%s", out)
	}
	if out := orch(contract.ExitFailed, "doctor"); !strings.Contains(out, "somebody else's word") {
		t.Fatalf("doctor in a project whose record git tracks:\n%s", out)
	}
}
