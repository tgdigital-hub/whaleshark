package integrate_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The ways round a protected path that the list itself must hold: a file
// moved out of it, a link put where a folder of the list would begin, so
// that what is added elsewhere lies under the protected path on disk, and a
// path that differs from the list by its letters' case alone, which on a
// Mac or on Windows is the same folder. With a pattern that begins with two
// stars in the list, every link is one to look at.
func TestAProtectedPathIsNotWalkedRound(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n", "deploy/old.sh": "old\n"})
	list := []string{"deploy/**", ".env*", ".github/workflows/**", "db/migrations/**", "**/secrets/**"}
	print := func() []string {
		t.Helper()
		_, files, err := r.k.Integrator.Protected(r.root, r.state(), list)
		must(t, err)
		return files
	}
	dir := r.task("A", nil)
	r.git(dir, "mv", "deploy/old.sh", "moved.sh")
	r.git(dir, "commit", "-q", "-m", "moved out")
	_, err := r.accept("A", nil)
	must(t, err)
	if got := print(); !slices.Equal(got, []string{"deploy/old.sh"}) {
		t.Errorf("a protected file moved away: %v", got)
	}
	dir = r.task("B", map[string]string{"gh/workflows/ci.yml": "on: push\n", "store/migrations/1.sql": "drop\n"})
	must(t, os.Symlink("gh", filepath.Join(dir, ".github")))
	must(t, os.Symlink("store", filepath.Join(dir, "db")))
	must(t, os.Symlink("a.txt", filepath.Join(dir, "plain-link")))
	r.git(dir, "add", ".github", "db", "plain-link")
	r.git(dir, "commit", "-q", "-m", "two links")
	_, err = r.accept("B", nil)
	must(t, err)
	if got := print(); !slices.Equal(got, []string{".github", "db", "deploy/old.sh", "plain-link"}) {
		t.Errorf("a link where a protected folder begins: %v", got)
	}
	dir = r.task("C", nil)
	blob := r.git(dir, "hash-object", "-w", "a.txt")
	for _, name := range []string{"Deploy/run.sh", ".ENV", "DB2/Secrets/key.pem"} {
		r.git(dir, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+name)
	}
	r.git(dir, "commit", "-q", "-m", "other letters")
	r.git(dir, "reset", "-q", "--hard")
	_, err = r.accept("C", nil)
	must(t, err)
	if got := print(); !slices.Equal(got, []string{".ENV", ".github", "DB2/Secrets/key.pem", "Deploy/run.sh", "db", "deploy/old.sh", "plain-link"}) {
		t.Errorf("a path that differs by its letters' case: %v", got)
	}
}
