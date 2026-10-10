// Package soak is the long test of the engine: twenty panes of noisy
// programs under the real keeper, with a window attached that is typed in,
// resized and switched between tabs, for as long as WHALESHARK_SOAK says.
// The design asks for an hour:
//
//	WHALESHARK_SOAK=1h go test ./test/soak -count=1 -timeout 90m -v
//
// Without the variable the test is skipped, so that an ordinary run stays
// fast. WHALESHARK_SOAK_PROFILE names a file for the keeper's processor
// profile, which says where its time went; the figures of a run that is
// profiled are a little worse than those of one that is not.
//
// The test's own program plays three parts, told apart by a variable: the
// test, the keeper in a process of its own, and the program in each pane.
package soak

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/platform"
)

const (
	envSoak    = "WHALESHARK_SOAK"
	envRole    = "WHALESHARK_SOAK_ROLE"
	envProfile = "WHALESHARK_SOAK_PROFILE"
)

func TestMain(m *testing.M) {
	switch os.Getenv(envRole) {
	case "keeper":
		keep()
	case "agent":
		agent()
	default:
		os.Exit(m.Run())
	}
}

// tally is a count of bytes with their checksum: what an agent printed, or
// what the keeper took from the agent's terminal.
type tally struct {
	N   uint64
	Sum uint32
}

// report is what the keeper's process says of itself, five times a second
// and once more when the keeper has stopped. Heap and Sys are Go's own
// figures: the heap in use, and everything it has from the system. The
// two Base figures are from before the keeper started; Left is every
// goroutine still there at the end, with where it waits.
type report struct {
	Heap, Sys                  uint64
	Goroutines, BaseGoroutines int
	Files, BaseFiles           int   // open files; -1 where the system does not list them
	Saves, LineSaves, Wrote    int64 // writes of the layout file, of the files beside it, and the bytes of both
	Looks, LookNanos           int64 // a watcher asking which program is in front
	Read                       map[string]tally
	Left                       string
	Ended                      bool
}

// login is a pretended login whose folders are all in dir.
func login(dir string) *platform.System {
	sys := platform.New(runtime.GOOS)
	sys.Home = dir
	sys.Env = func(name string) string {
		return map[string]string{"APPDATA": filepath.Join(dir, "roaming"), "LOCALAPPDATA": filepath.Join(dir, "local")}[name]
	}
	return sys
}

// reportPath is where the keeper's process writes its report.
func reportPath(dir string) string { return filepath.Join(dir, "keeper.json") }
