package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Repo makes a real git repository in a temporary folder, with the files
// given committed on the branch main, and returns its folder. The packages
// that work with git test against this and against no stand-in: a worktree,
// a merge and a branch are git's own. Nothing of the machine's or the
// login's git settings reaches it, and nothing it does reaches them.
func Repo(t testing.TB, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	// A temporary folder is a link on some systems; git prints the real path.
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	Git(t, root, "init", "-q", "-b", "main")
	Commit(t, root, "start", files)
	return root
}

// Commit writes the files into a repository or a worktree of one, an empty
// text removing the file, commits all of it and returns the commit's id.
func Commit(t testing.TB, dir, message string, files map[string]string) string {
	t.Helper()
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if text == "" {
			os.Remove(path)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	Git(t, dir, "add", "-A")
	Git(t, dir, "commit", "-q", "--allow-empty", "-m", message)
	return Git(t, dir, "rev-parse", "HEAD")
}

// GitEnv is the surroundings every git of a test runs in: no settings but
// the repository's own, a made-up author, no prompt and no hook of anybody's.
func GitEnv() []string {
	return append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=a test", "GIT_AUTHOR_EMAIL=test@localhost", "GIT_COMMITTER_NAME=a test", "GIT_COMMITTER_EMAIL=test@localhost",
		"GIT_AUTHOR_DATE=2026-10-08T21:14:00Z", "GIT_COMMITTER_DATE=2026-10-08T21:14:00Z")
}

// Git runs git in a folder and returns what it printed, without the last
// line break; a git that fails fails the test with all it said.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	// #nosec G204 -- git, with the words a test gives, in the test's own temporary repository
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = dir, GitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimRight(string(out), "\n")
}
