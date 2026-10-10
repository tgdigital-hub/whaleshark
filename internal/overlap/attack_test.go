package overlap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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
