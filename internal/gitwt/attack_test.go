package gitwt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// A folder named in .worktreeinclude is copied into each new worktree. Where
// the branch the worktree is cut from holds a link below that folder, put
// there by an earlier task, nothing is written through it: neither a file
// over the link's target nor files into a folder it leads to.
func TestNothingIsCopiedThroughALinkGitPutThere(t *testing.T) {
	d := project(t, map[string]string{".gitignore": "cfg/\n", ".worktreeinclude": "cfg\n"})
	outside := t.TempDir()
	d.write("cfg/secret", "the project's own\n")
	d.write("cfg/sub/inner", "the project's own\n")
	if os.WriteFile(filepath.Join(outside, "target"), []byte("untouched\n"), 0o600) != nil || os.Mkdir(filepath.Join(outside, "dir"), 0o700) != nil {
		t.Fatal("no place outside")
	}
	// The run's collected work holds the links, as a task's commit left them.
	dir, _ := d.place("A", "plants")
	if os.Symlink(filepath.Join(outside, "target"), filepath.Join(dir, "cfg", "secret2")) != nil {
		t.Skip("this login cannot make links")
	}
	for name, to := range map[string]string{"secret": "target", "sub": "dir"} {
		os.RemoveAll(filepath.Join(dir, "cfg", name))
		if err := os.Symlink(filepath.Join(outside, to), filepath.Join(dir, "cfg", name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := git(dir, "add", "-f", "--", "cfg"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(dir, "-c", "user.name=t", "-c", "user.email=t@localhost", "commit", "-q", "-m", "links"); err != nil {
		t.Fatal(err)
	}
	tip, _ := git(dir, "rev-parse", "HEAD")
	d.git("branch", "collected", tip)
	d.s.Run.Integration = &contract.Integration{Branch: "collected", Tip: tip}

	d.place("B", "is given the copies")
	if got, _ := os.ReadFile(filepath.Join(outside, "target")); string(got) != "untouched\n" {
		t.Errorf("a file outside the project was written through a link: %q", got)
	}
	if exists(filepath.Join(outside, "dir", "inner")) {
		t.Error("a file was copied into a folder outside the project through a link")
	}
}
