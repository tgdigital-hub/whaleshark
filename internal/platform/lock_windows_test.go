package platform

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// openShared opens a file to read, with leave for anyone to replace it.
func openShared(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

// TestMoveUnderAnOpenFile: the one-step replace goes on under a reader that
// gave leave, the name holds the new file at once, and the reader keeps the
// old one.
func TestMoveUnderAnOpenFile(t *testing.T) {
	dir := t.TempDir()
	tmp, final := filepath.Join(dir, "state.json.new"), filepath.Join(dir, "state.json")
	os.WriteFile(final, []byte("old"), 0o600)
	os.WriteFile(tmp, []byte("new"), 0o600)
	held, err := openShared(final)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := moveUnder(tmp, final); err != nil {
		t.Fatalf("the one-step replace under an open file: %v", err)
	}
	if now, err := readFile(final); err != nil || string(now) != "new" {
		t.Errorf("the name holds %q, %v", now, err)
	}
	if old, err := io.ReadAll(held); err != nil || string(old) != "old" {
		t.Errorf("the reader that was there first has %q, %v", old, err)
	}
	if _, err := os.Stat(tmp); err == nil {
		t.Error("the temporary file is still there")
	}
}

// hammer replaces one file many times with move while two readers open it
// as fast as they can, each open made once. It says what the readers met.
func hammer(t *testing.T, move func(tmp, final string) error) (missing, opens int64, longest time.Duration, refused map[syscall.Errno]int) {
	dir := t.TempDir()
	tmp, final := filepath.Join(dir, "count.new"), filepath.Join(dir, "count")
	os.WriteFile(final, []byte("start"), 0o600)
	var stop atomic.Bool
	var readers sync.WaitGroup
	var mu sync.Mutex
	var gone, all atomic.Int64
	refused = map[syscall.Errno]int{}
	for range 2 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			var since time.Time
			for !stop.Load() {
				f, err := openShared(final)
				all.Add(1)
				if err == nil {
					f.Close()
					if !since.IsZero() {
						mu.Lock()
						longest = max(longest, time.Since(since))
						mu.Unlock()
						since = time.Time{}
					}
					continue
				}
				var code syscall.Errno
				errors.As(err, &code)
				if code == windows.ERROR_FILE_NOT_FOUND {
					gone.Add(1)
					if since.IsZero() {
						since = time.Now()
					}
					continue
				}
				mu.Lock()
				refused[code]++
				mu.Unlock()
			}
		}()
	}
	failed := map[string]int{}
	for i := range 3000 {
		if err := os.WriteFile(tmp, []byte(fmt.Sprint(i)), 0o600); err != nil {
			failed["write: "+err.Error()]++
			continue
		}
		if err := move(tmp, final); err != nil {
			failed[err.Error()]++
		}
	}
	stop.Store(true)
	readers.Wait()
	if len(failed) > 0 {
		t.Errorf("replaces that failed: %v", failed)
	}
	return gone.Load(), all.Load(), longest, refused
}

// TestMoveUnderNeverHidesTheName: under the one-step replace a reader that
// gives leave never finds the name without a file and is never refused.
func TestMoveUnderNeverHidesTheName(t *testing.T) {
	missing, opens, longest, refused := hammer(t, moveUnder)
	if missing > 0 || len(refused) > 0 {
		t.Errorf("%d of %d opens found no file, for %v at the longest; refused: %v", missing, opens, longest, refused)
	}
}

// TestPlainRenameUnderReaders says what the plain call does under the same
// readers, which is why Replace does not start with it.
func TestPlainRenameUnderReaders(t *testing.T) {
	missing, opens, longest, refused := hammer(t, os.Rename)
	t.Errorf("the plain call: %d of %d opens found no file, for %v at the longest; refused: %v", missing, opens, longest, refused)
}
