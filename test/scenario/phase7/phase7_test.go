// Package phase7 plays what the trial of phase 7 saw real agents do, with
// scripted ones: names changed by the person and by the lead agent, the
// names that are refused, and a working agent stopped by the person. The
// suite of every scenario plays the file on the engine's double and on the
// real keeper and holds the two logs to each other; what its own lines
// cannot say is held here.
package phase7

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func TestMain(m *testing.M) { scenario.Main(m) }

// A working agent that the person stops keeps its work: the file it wrote
// and committed is still in its own copy of the code, under its old branch,
// when the lead agent has started the task again.
func TestAStoppedAgentsWorkStaysInItsCopy(t *testing.T) {
	p := scenario.Run(t, "names-and-stops.scn", nil)
	copies, err := filepath.Glob(filepath.Join(p.Root, ".whaleshark", "worktrees", "*", "ship.txt"))
	if err != nil || len(copies) == 0 {
		t.Fatalf("no copy of the code holds the stopped agent's file: %v %v", copies, err)
	}
	if data, err := os.ReadFile(copies[0]); err != nil || len(data) == 0 {
		t.Errorf("the stopped agent's file is %q: %v", data, err)
	}
}
