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

// A repository that brings a whole record along, a run with a task whose
// check is a command of its author's, is not used: no command changes or
// runs anything of it. In a project that is set up, init and doctor say so.
func TestARecordThatComesWithARepositoryIsNotUsed(t *testing.T) {
	p := scenario.Prepare(t, nil, nil)
	testkit.Git(t, p.Root, "init", "-q", "-b", "main")
	testkit.Commit(t, p.Root, "start", map[string]string{"b.md": briefText})
	orch := func(exit int, args ...string) string { return play(t, p.Command(scenario.Orch, args...), exit) }
	// Its author made the run at home and committed the folder with it.
	orch(0, "run", "new", "planted")
	orch(0, "task", "add", "T1", "looks harmless", "--brief", "b.md", "--check", "echo ran>ran.txt")
	testkit.Git(t, p.Root, "add", "-f", contract.ProjectDir)
	testkit.Git(t, p.Root, "commit", "-q", "-m", "a record comes along")

	for _, args := range [][]string{{"task", "add", "T2", "more", "--brief", "b.md", "--check", "none"}, {"start", "T1"}, {"accept", "T1"}, {"run", "takeover"}} {
		if out := orch(contract.ExitEnv, args...); !strings.Contains(out, "brings a .whaleshark folder along") {
			t.Fatalf("%v on a record that came with the repository:\n%s", args, out)
		}
	}
	if out := orch(contract.ExitEnv, "init"); !strings.Contains(out, "git rm -r --cached .whaleshark") {
		t.Fatalf("init on a record that came with the repository:\n%s", out)
	}
	// Taken out of git, the folder is the login's to set up.
	testkit.Git(t, p.Root, "rm", "-r", "-q", "--cached", contract.ProjectDir)
	testkit.Git(t, p.Root, "commit", "-q", "-m", "out again")
	orch(0, "init")
	orch(0, "task", "add", "T2", "more", "--brief", "b.md", "--check", "none")
	// Tracked again in a project that is set up, init and doctor still say so.
	testkit.Git(t, p.Root, "add", "-f", contract.ProjectDir)
	if out := orch(contract.ExitEnv, "init"); !strings.Contains(out, "git tracks") {
		t.Fatalf("init in a project whose record git tracks:\n%s", out)
	}
	if out := orch(contract.ExitFailed, "doctor"); !strings.Contains(out, "somebody else's word") {
		t.Fatalf("doctor in a project whose record git tracks:\n%s", out)
	}
}
