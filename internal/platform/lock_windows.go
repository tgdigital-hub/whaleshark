package platform

import (
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

// lockFile is as on the other systems. The locked byte lies far past the
// end, so that what the file says can still be read while it is held.
func lockFile(path string, exclusive, wait bool) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	var how uint32
	if exclusive {
		how |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	if !wait {
		how |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	err = windows.LockFileEx(windows.Handle(f.Fd()), how, 0, 1, 0, &windows.Overlapped{OffsetHigh: 1})
	if err != nil {
		f.Close()
		if err == windows.ERROR_LOCK_VIOLATION {
			err = nil
		}
		return nil, err
	}
	return f, nil
}

// owner cannot tell: who may write a file on Windows is not read yet.
func owner(fs.FileInfo) (uid, gid uint32, ok bool) { return 0, 0, false }
