//go:build darwin || linux

package platform

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// lockFile opens and locks a lock file. Without wait it gives no file when
// another process holds the lock.
func lockFile(path string, exclusive, wait bool) (*os.File, error) {
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	// #nosec G115 -- where a login has a number it is never below nought
	if uid, _, _ := owner(dir); uid != uint32(os.Geteuid()) {
		return nil, fmt.Errorf("%s: %w", path, contract.ErrNotOurs)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	how := unix.LOCK_SH
	if exclusive {
		how = unix.LOCK_EX
	}
	if !wait {
		how |= unix.LOCK_NB
	}
	for err = unix.EINTR; err == unix.EINTR; {
		err = unix.Flock(int(f.Fd()), how)
	}
	if err != nil {
		f.Close()
		if err == unix.EWOULDBLOCK {
			err = nil
		}
		return nil, err
	}
	return f, nil
}

func readFile(path string) ([]byte, error) { return os.ReadFile(path) }
func moveOver(tmp, final string) error     { return os.Rename(tmp, final) }

func owner(info fs.FileInfo) (uid, gid uint32, ok bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return st.Uid, st.Gid, true
}
