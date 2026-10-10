package gitwt

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// A folder named in .worktreeinclude is copied into each new worktree. Where
// the branch the worktree is cut from holds a link below that folder, put
// there by an earlier task, nothing is written through it: neither a file
// over the link's target nor files into a folder it leads to. Git makes a
// link of it or, as it does on Windows unless told otherwise, a plain file
// with the link's text: the worktree is made either way.
func TestNothingIsCopiedThroughALinkGitPutThere(t *testing.T) {
	for _, links := range []string{"true", "false"} {
		t.Run("core.symlinks="+links, func(t *testing.T) {
			d := project(t, map[string]string{".gitignore": "cfg/\n", ".worktreeinclude": "cfg\n"})
			outside := t.TempDir()
			d.write("cfg/secret", "the project's own\n")
			d.write("cfg/sub/inner", "the project's own\n")
			d.write("cfg/plain", "the project's own\n")
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

			d.git("config", "core.symlinks", links)
			next, _ := d.place("B", "is given the copies")
			if got, _ := os.ReadFile(filepath.Join(outside, "target")); string(got) != "untouched\n" {
				t.Errorf("a file outside the project was written through a link: %q", got)
			}
			if exists(filepath.Join(outside, "dir", "inner")) {
				t.Error("a file was copied into a folder outside the project through a link")
			}
			// What lies beside the links is copied as ever.
			if got, _ := os.ReadFile(filepath.Join(next, "cfg", "plain")); string(got) != "the project's own\n" {
				t.Errorf("the file beside the links was copied as %q", got)
			}
			if info, err := os.Lstat(filepath.Join(next, "cfg", "sub")); err != nil || info.IsDir() {
				t.Errorf("what git put where a folder would go did not stand: %v", err)
			}
		})
	}
}

// The copy itself writes through no link of any kind that lies where a
// folder or a file would go, whoever put it there. On Windows a junction
// leads out as a link does, any login can make one, and it is not reported
// as a link.
func TestTheCopyWritesThroughNoLink(t *testing.T) {
	kinds := map[string]func(target, link string) error{"link": os.Symlink}
	if runtime.GOOS == "windows" {
		kinds["junction"] = func(target, link string) error {
			return exec.Command("cmd", "/c", "mklink", "/J", link, target).Run()
		}
	}
	for kind, link := range kinds {
		t.Run(kind, func(t *testing.T) {
			from, to, outside := t.TempDir(), t.TempDir(), t.TempDir()
			for _, name := range []string{"sub/inner", "sub/deep/inner", "file", "plain"} {
				path := filepath.Join(from, filepath.FromSlash(name))
				if os.MkdirAll(filepath.Dir(path), 0o700) != nil || os.WriteFile(path, []byte("the project's own\n"), 0o600) != nil {
					t.Fatal("nothing to copy")
				}
			}
			if os.WriteFile(filepath.Join(outside, "target"), []byte("untouched\n"), 0o600) != nil || os.Mkdir(filepath.Join(outside, "dir"), 0o700) != nil {
				t.Fatal("no place outside")
			}
			if err := link(filepath.Join(outside, "dir"), filepath.Join(to, "sub")); err != nil {
				if kind == "link" {
					t.Skip("this login cannot make links")
				}
				t.Fatal(err)
			}
			// A junction leads to a folder only.
			if kind == "link" {
				if err := link(filepath.Join(outside, "target"), filepath.Join(to, "file")); err != nil {
					t.Fatal(err)
				}
			}
			if err := copyTree(from, to); err != nil {
				t.Fatal(err)
			}
			if left, _ := os.ReadDir(filepath.Join(outside, "dir")); len(left) != 0 {
				t.Errorf("%d files or folders were copied through the %s into a folder outside", len(left), kind)
			}
			if got, _ := os.ReadFile(filepath.Join(outside, "target")); string(got) != "untouched\n" {
				t.Errorf("a file outside was written through the %s: %q", kind, got)
			}
			if got, _ := os.ReadFile(filepath.Join(to, "plain")); string(got) != "the project's own\n" {
				t.Errorf("the file beside the %s was copied as %q", kind, got)
			}
			// The whole place of the copy a link: nothing is copied at all.
			if err := copyTree(from, filepath.Join(to, "sub")); err != nil {
				t.Fatal(err)
			}
			if left, _ := os.ReadDir(filepath.Join(outside, "dir")); len(left) != 0 {
				t.Errorf("%d files or folders were copied into a folder outside that the %s is", len(left), kind)
			}
		})
	}
}
