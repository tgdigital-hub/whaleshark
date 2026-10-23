package launch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// A project whose own main is far behind the remote's: with fetch_base the
// run begins at the remote's tip, and in pr-per-task mode a task's copy is
// cut from there, while the person's checkout stays where it was. The
// worker is told to push its own branch and where the body of its pull
// request goes.
func TestAStaleBaseAndANewerRemote(t *testing.T) {
	for _, pair := range testkit.GitEnv()[len(os.Environ()):] {
		name, value, _ := strings.Cut(pair, "=")
		t.Setenv(name, value)
	}
	p := scenario.Prepare(t, nil, nil)
	remote, other := t.TempDir(), t.TempDir()
	testkit.Git(t, remote, "init", "-q", "--bare", "-b", "main")
	testkit.Git(t, p.Root, "init", "-q", "-b", "main")
	stale := testkit.Commit(t, p.Root, "start", map[string]string{"b.md": brief, ".gitignore": "whaleshark.toml\n"})
	testkit.Git(t, p.Root, "remote", "add", "origin", remote)
	testkit.Git(t, p.Root, "push", "-q", "origin", "main")
	// Somebody else moves the remote's main on.
	testkit.Git(t, other, "clone", "-q", remote, ".")
	newer := testkit.Commit(t, other, "elsewhere", map[string]string{"c.txt": "new\n"})
	testkit.Git(t, other, "push", "-q", "origin", "main")

	approve(t, p, "[worktrees]\nfetch_base = true\n[land]\nmode = \"pr-per-task\"\n")
	must(t, p.Command(scenario.Orch, "init"), 0)
	must(t, p.Command(scenario.Orch, "run", "new", "a parser"), 0)
	if s, _ := p.Record(); s.Run.Base == nil || s.Run.Base.Ref != "main" || s.Run.Base.OID != newer {
		t.Fatalf("the run began at %+v, not at the remote's main %s", s.Run.Base, newer)
	}
	// The remote moves once more before the start: the copy is cut from that.
	newest := testkit.Commit(t, other, "later", map[string]string{"d.txt": "later\n"})
	testkit.Git(t, other, "push", "-q", "origin", "main")
	must(t, p.Command(scenario.Orch, "task", "add", "T1", "the parser", "--brief", "b.md", "--check", "none"), 0)
	must(t, p.Command(scenario.Orch, "start", "T1"), 0, "working (T1.1)")

	s, run := p.Record()
	tree := s.Tasks["T1"].Worktree
	if tree == nil || testkit.Git(t, tree.Path, "rev-parse", "HEAD") != newest {
		t.Fatalf("the task's copy %+v is not at the remote's tip %s", tree, newest)
	}
	if testkit.Git(t, p.Root, "rev-parse", "main") != stale || testkit.Git(t, p.Root, "symbolic-ref", "--short", "HEAD") != "main" {
		t.Fatal("the person's own checkout was moved")
	}
	prompt, _ := os.ReadFile(filepath.Join(run, "attempts", "T1.1", "prompt.md"))
	for _, want := range []string{"Push your own branch", "PR body:      " + filepath.Join(run, "attempts", "T1.1", contract.PRBody)} {
		if !strings.Contains(string(prompt), want) {
			t.Errorf("the prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(string(prompt), "delete or push a branch") {
		t.Errorf("the prompt still forbids the push:\n%s", prompt)
	}
}
