package windows

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// gitSays gives every git this test starts, the program's and the agents'
// too, one setting over whatever the machine has.
func gitSays(t *testing.T, key, value string) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", key)
	t.Setenv("GIT_CONFIG_VALUE_0", value)
}

// TestTheOverlapScenariosWithWindowsLineEndings plays the two flows of two
// tasks on one file with git set as its Windows installer sets it: every
// text file checked out with the line endings of Windows. The scan must
// still see one clash and no more, and the merge, the sync and the landing
// must go as they do elsewhere.
func TestTheOverlapScenariosWithWindowsLineEndings(t *testing.T) {
	for _, name := range []string{"8f-one-file.scn", "8f-reported.scn"} {
		t.Run(name, func(t *testing.T) {
			gitSays(t, "core.autocrlf", "true")
			p := scenario.Run(t, filepath.Join("..", "..", "scenario", "phase2", name), nil)
			landed, err := os.ReadFile(filepath.Join(p.Root, "search.txt"))
			if err != nil || !bytes.Contains(landed, []byte("A.1\r\n")) || !bytes.Contains(landed, []byte("B.")) {
				t.Errorf("the landed file should hold both tasks' lines with Windows' line endings: %v\n%q", err, landed)
			}
		})
	}
}

// deep is a file's path inside a project, longer than the 260 characters
// Windows gives a path by default.
var deep = strings.Repeat("a-folder-with-a-long-name/", 12) + "search.txt"

// TestAProjectWithALongPath takes a task through its whole life in a
// project one of whose files lies deeper than 260 characters: its own copy
// of the code, a commit by its worker, the scan, the merge and the landing.
// git is told core.longpaths, which on Windows it needs for such a file;
// TestGitAsItComesAndALongPath says what it does without.
func TestAProjectWithALongPath(t *testing.T) {
	gitSays(t, "core.longpaths", "true")
	p := scenario.Prepare(t, nil, map[string]string{"A.1": `edit ` + deep + ` | commit | write-result | report done "the deep file is changed"`})
	file := filepath.Join(p.Root, filepath.FromSlash(deep))
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{file: "the search page\n",
		filepath.Join(p.Root, "brief.md"): "Target: the search page\nChange: one line\nConstraints: none\nOwnership: the deep file\nAcceptance: the line is there\n"} {
		if err := os.WriteFile(name, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("FACT the file's whole path is %d characters long", len(file))
	testkit.Git(t, p.Root, "init", "-q", "-b", "main")
	testkit.Git(t, p.Root, "config", "user.name", "a test")
	testkit.Git(t, p.Root, "config", "user.email", "test@localhost")
	testkit.Commit(t, p.Root, "start", nil)
	do := func(who string, args ...string) {
		t.Helper()
		if out, err := p.Command(who, args...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	do(scenario.Orch, "init")
	do(scenario.Orch, "run", "new", "a long path")
	do(scenario.Orch, "task", "add", "A", "Change the deep file", "--name", "deep file", "--owns", deep, "--check", "git ls-files --error-unmatch "+deep, "--brief", "brief.md")
	do(scenario.Orch, "start", "--ready")
	if !soon(20*time.Second, func() bool { s, _ := p.Record(); return s.Attempts["A.1"].State == contract.AttemptReported }) {
		s, _ := p.Record()
		t.Fatalf("the worker did not report: A.1 is %s", s.Attempts["A.1"].State)
	}
	do(scenario.Orch, "wait", "--timeout", "10")
	do(scenario.Orch, "overlap")
	do(scenario.Orch, "accept", "A")
	do(scenario.Orch, "close", "--settled")
	do(scenario.Human, "land", "--local")
	do(scenario.Orch, "run", "close")
	if landed, err := os.ReadFile(file); err != nil || !strings.Contains(string(landed), "a line by A.1") {
		t.Errorf("the worker's line did not land in the deep file: %v\n%q", err, landed)
	}
}

// TestGitAsItComesAndALongPath asks git, with no setting of anybody's, to
// take a file deeper than 260 characters into a repository and to check it
// out into a second copy, as a task's own copy of the code is made. On
// Windows git refuses the file until core.longpaths is set; elsewhere it
// takes it.
func TestGitAsItComesAndALongPath(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, filepath.FromSlash(deep))
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("the search page\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	said := ""
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "start"},
		{"worktree", "add", "-q", filepath.Join(t.TempDir(), "copy")}} {
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = root, testkit.GitEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			said = fmt.Sprintf("git %s: %s", args[0], bytes.TrimSpace(out))
			break
		}
	}
	t.Logf("FACT a path of %d characters, git as it comes: %q", len(file), said)
	if refused := said != ""; refused != (system().Platform.System() == "windows") {
		t.Errorf("git as it comes, a path of %d characters: refused=%v %s", len(file), refused, said)
	}
}
