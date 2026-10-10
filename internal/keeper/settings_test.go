package keeper

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// The settings file names programs the keeper starts. One that another
// login can write is not used, and the keeper's status says so.
func TestASettingsFileAnotherLoginCanWriteIsNotUsed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("who can write a file is not asked on Windows")
	}
	r := newRig(t)
	path := contract.SettingsPath(r.k.dirs)
	// Written until the keeper has it: a change in the keeper's first
	// instants, before it watches the file, is noticed by nobody.
	write := func(text string, mode os.FileMode, shell string) {
		t.Helper()
		r.until("the shell to be "+shell, func() bool {
			os.Chmod(path, mode) // who can write it changes first: only what is written is noticed
			if err := errors.Join(os.MkdirAll(filepath.Dir(path), 0o700), os.WriteFile(path, []byte(text), 0o600)); err != nil {
				t.Fatal(err)
			}
			time.Sleep(20 * time.Millisecond)
			return r.locked(func() bool { return strings.Join(r.k.shell(r.k.set), " ") == shell })
		})
	}
	write("shell = [\"my-shell\"]\n", 0o600, "my-shell")
	write("shell = [\"planted\", \"by another\"]\n", 0o666, "the-shell") // #nosec G302 -- the fault under test
	if reply := r.dial().call(contract.WireCall{Op: contract.OpStatus}); !strings.Contains(reply.Text, "is not used") {
		t.Errorf("the status says %q", reply.Text)
	}
	write("shell = [\"my-shell\"]\n", 0o600, "my-shell")
}
