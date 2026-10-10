package platform

import (
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// The port slots on the real lock and the real replace, in a login whose
// state folder does not exist yet: twenty tasks started at once get twenty
// slots, and each keeps its own when asked again.
func TestSlotsOnTheRealLock(t *testing.T) {
	s, home := New(runtime.GOOS), t.TempDir()
	s.Home, s.Env = home, func(name string) string {
		if name == "APPDATA" || name == "LOCALAPPDATA" {
			return filepath.Join(home, name)
		}
		return ""
	}
	var wg sync.WaitGroup
	got := make([]contract.Slot, 20)
	for i := range got {
		wg.Go(func() {
			var err error
			if got[i], err = contract.TakeSlot(s, contract.DefaultPorts, home, "r1", string(rune('a'+i)), 10); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	seen := map[int]bool{}
	for i, slot := range got {
		again, err := contract.TakeSlot(s, contract.DefaultPorts, home, "r1", string(rune('a'+i)), 10)
		if seen[slot.N] || err != nil || again != slot || slot.N >= 20 {
			t.Fatalf("task %d has slot %+v, asked again %+v, %v", i, slot, again, err)
		}
		seen[slot.N] = true
	}
	freed, err := contract.FreeSlots(s, func(contract.Slot) bool { return true })
	if err != nil || len(freed) != 20 {
		t.Fatalf("giving all back freed %d, %v", len(freed), err)
	}
}
