package overlap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A project folder with a colon in its name: git reads the project's objects
// through a list that a colon divides, so the folder is named there quoted.
// Unquoted, the scan found no object, and the part after the colon was read
// as a folder of its own.
func TestAProjectFolderWithAColonInItsName(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("no folder's name holds a colon there")
	}
	f := start(t, map[string]string{"a.txt": alpha()})
	root := filepath.Join(f.work, `a:b "c"`)
	f.git(f.work, "clone", "-q", f.root, root)
	f.git(root, "branch", "whaleshark/r1", "main")
	g, err := open(context.Background(), f.k, root, f.state())
	must(t, err)
	if _, err := g.git(root, "", "cat-file", "-e", g.tip); err != nil {
		t.Fatalf("the scan does not find the project's objects: %v", err)
	}
	must(t, g.merge([][2]string{{g.tip, g.tip}}))
}

// A task's folder with links in it, one in a circle and one out of the
// project: the scan, the deep one included, names each link and follows none.
func TestAScanFollowsNoLink(t *testing.T) {
	f := start(t, map[string]string{"a.txt": alpha()})
	dir, outside := f.task("A", map[string]string{"a.txt": alpha(3)}, "a.txt"), t.TempDir()
	must(t, os.WriteFile(filepath.Join(outside, "secret"), []byte("not the project's\n"), 0o600))
	if os.Symlink(".", filepath.Join(dir, "loop")) != nil || os.Symlink(outside, filepath.Join(dir, "out")) != nil {
		t.Skip("this login cannot make links")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	found, err := f.o.Scan(ctx, f.root, f.state(), nil, true)
	must(t, err)
	same(t, "the scan of a folder with links", lines(found), "scope A (outside what it owns) loop out")
}
