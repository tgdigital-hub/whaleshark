package integrate_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// kinds is the kinds of the events the run has raised.
func (r *run) kinds() string {
	var kinds []string
	for _, e := range r.state().Inbox.Events {
		kinds = append(kinds, e.Kind)
	}
	return strings.Join(kinds, " ")
}

// From "all tasks done" to the base branch: who may, when, and what happens
// to the branch, the record and the project's own checkout at each step.
func TestLandLocal(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n"})
	land := func(who string, exit int, words ...string) string {
		t.Helper()
		return play(t, r.p.Command(who, "land", "--local"), exit, words...)
	}
	main := func() string { return r.git(r.root, "rev-parse", "main") }
	base := main()

	play(t, r.p.Command(scenario.Human, "land"), 2, "--pr", "--local")
	r.task("A", map[string]string{"a.txt": "two\n"})
	land(scenario.Human, 5, "A")
	_, err := r.accept("A", nil)
	must(t, err)
	// The project has not said otherwise, so only the person lands.
	land(scenario.Orch, 5, "Only the person")

	// The base branch moves: a worker brings the collected work up to date.
	r.git(r.root, "checkout", "-q", "-b", "side")
	land(scenario.Human, 5, "not on main")
	r.git(r.root, "checkout", "-q", "main")
	moved := testkit.Commit(t, r.root, "somebody else's", map[string]string{"other.txt": "other\n"})
	land(scenario.Human, 5, "main has moved", "lacks 1 of its commits", "would be clean", "task add sync-main", "--after A")
	dir := r.task("sync-main", nil)
	r.git(dir, "merge", "-q", "--no-edit", "main")
	_, err = r.accept("sync-main", nil)
	must(t, err)
	tip := r.branch()
	// That one commit keeps the base as a second parent: main is in the work now.
	if r.git(r.root, "rev-parse", tip+"^2") != moved {
		t.Fatal("the collected work does not hold main as git sees it")
	}

	// The run's check fails on the collected work: nothing goes anywhere.
	r.approve("[land]\ncheck = \"echo broken && exit 1\"\n")
	out := land(scenario.Human, 1, "failed: broken", "land.log", "task add fix-land")
	_, runDir := r.p.Record()
	if log, _ := os.ReadFile(filepath.Join(runDir, "land.log")); !strings.Contains(string(log), "broken") || main() != moved {
		t.Fatalf("after a failed check main is at %s, the log holds %q\n%s", main(), log, out)
	}
	if s := r.state(); s.Run.Integration.Landed != "" || !strings.Contains(r.kinds(), "land_failed") {
		t.Fatalf("a failed landing left landed %q and the events %s", s.Run.Integration.Landed, r.kinds())
	}

	// It passes: the base branch moves forward to the collected work.
	r.approve("[land]\ncheck = \"exit 0\"\n")
	write(t, filepath.Join(r.root, "a.txt"), "changed by hand\n")
	land(scenario.Human, 5, "not committed")
	r.git(r.root, "checkout", "-q", "a.txt")
	land(scenario.Human, 0, "merged into main")
	if s := r.state(); main() != tip || s.Run.Integration.Landed != tip || s.Run.Integration.Tip != tip {
		t.Fatalf("landed: main %s, tip %s, the record %+v", main(), tip, s.Run.Integration)
	}
	if text, _ := os.ReadFile(filepath.Join(r.root, "a.txt")); string(text) != "two\n" || !has(r.root, "other.txt") || main() == base {
		t.Fatalf("the project's own checkout holds %q", text)
	}
	land(scenario.Human, 5, "Nothing to land")

	// A project may let its lead agent land; that is approved like a command.
	r.task("B", map[string]string{"b.txt": "b\n"})
	_, err = r.accept("B", nil)
	must(t, err)
	r.approve("[land]\nwho = \"orchestrator\"\n")
	land(scenario.Orch, 0, "merged into main")
	if main() != r.branch() {
		t.Fatal("the lead agent's landing did not move main")
	}
}

func TestLandNeedsGitAndWork(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n"})
	play(t, r.p.Command(scenario.Human, "land", "--local"), 5, "Nothing to land")
	// A run that wrote down no base is landed nowhere, whatever is checked out.
	r.task("A", map[string]string{"a.txt": "two\n"})
	_, err := r.accept("A", nil)
	must(t, err)
	r.change(func(s *contract.State) { s.Run.Base = nil })
	play(t, r.p.Command(scenario.Human, "land", "--local"), 5, "no branch", "whaleshark/r1")
	r.change(func(s *contract.State) { s.Run.Integration = nil })
	play(t, r.p.Command(scenario.Human, "land", "--pr"), 5, "needs git")
}

// One pull request: the branch is pushed to where the base comes from and gh
// is handed a title of ours and the body on its standard input. A check
// nobody approved is not run, and the record says so.
func TestLandPullRequest(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n"})
	if r.k.Platform.System() == "windows" {
		t.Skip("the stand-in for gh is a shell script")
	}
	remote, tools := t.TempDir(), t.TempDir()
	testkit.Git(t, remote, "init", "-q", "--bare")
	r.git(r.root, "remote", "add", "origin", remote)
	said := filepath.Join(tools, "said")
	write(t, filepath.Join(tools, "gh"), "#!/bin/sh\nif [ \"$2\" = view ]; then exit 1; fi\necho \"$*\" > '"+said+"'\ncat >> '"+said+"'\necho https://example.test/pull/1\n")
	must(t, os.Chmod(filepath.Join(tools, "gh"), 0o700))
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))

	r.task("A", map[string]string{"a.txt": "two\n"})
	_, err := r.accept("A", nil)
	must(t, err)
	write(t, filepath.Join(r.root, "whaleshark.toml"), "[land]\ncheck = \"exit 1\"\n")
	// Whoever lands is told that the check was left out, not only the lead agent.
	play(t, r.p.Command(scenario.Human, "land", "--pr"), 0, "https://example.test/pull/1", "whaleshark/r1", "is NOT run", "whaleshark trust")

	if pushed := testkit.Git(t, remote, "rev-parse", "whaleshark/r1"); pushed != r.branch() || r.git(r.root, "rev-parse", "main") == pushed {
		t.Fatalf("the remote has the branch at %s", pushed)
	}
	text, _ := os.ReadFile(said)
	for _, want := range []string{"pr create --base main --head whaleshark/r1 --title The collected work of run r1 --body-file -", "a shop", "- A: the A page"} {
		if !strings.Contains(string(text), want) {
			t.Fatalf("gh was not handed %q:\n%s", want, text)
		}
	}
	if s := r.state(); s.Run.Integration.Landed != r.branch() || !strings.Contains(r.kinds(), "untrusted") {
		t.Fatalf("landed %q, events %s", s.Run.Integration.Landed, r.kinds())
	}
}
