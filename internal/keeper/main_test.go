package keeper

import (
	"os"
	"sync"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/pick"
)

// No test of the keeper may reach the clipboard of whoever runs it: the
// program that would write it is replaced before anything starts, and what
// it was given is kept for the test to look at.
var (
	boardMu sync.Mutex
	board   []string
)

func TestMain(m *testing.M) {
	// The keeper starts its own program file in the dock, and under a test
	// that file is this one: it is then the stand-in for the left section.
	if len(os.Args) == 4 && os.Args[1] == "ui" && os.Args[2] == "run" && os.Args[3] == "side" {
		standIn()
	}
	pick.Run = func(argv []string, in []byte) error {
		boardMu.Lock()
		defer boardMu.Unlock()
		board = append(board, argv[0]+": "+string(in))
		return nil
	}
	os.Exit(m.Run())
}
