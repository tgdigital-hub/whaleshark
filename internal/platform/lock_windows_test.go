package platform

import (
	"io"
	"os"
	"path/filepath"
	"testing"

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
