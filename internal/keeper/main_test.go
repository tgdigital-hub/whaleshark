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
	pick.Run = func(argv []string, in []byte) error {
		boardMu.Lock()
		defer boardMu.Unlock()
		board = append(board, argv[0]+": "+string(in))
		return nil
	}
	os.Exit(m.Run())
}
