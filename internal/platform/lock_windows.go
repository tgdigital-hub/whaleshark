package platform

import (
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"syscall"

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

// readFile opens a file so that it can be replaced or removed while it is
// read, which the standard library's own open does not allow.
func readFile(path string) ([]byte, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err == nil {
		var h windows.Handle
		h, err = windows.CreateFile(name, windows.GENERIC_READ,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err == nil {
			f := os.NewFile(uintptr(h), path)
			defer f.Close()
			return io.ReadAll(f)
		}
	}
	return nil, &fs.PathError{Op: "open", Path: path, Err: err}
}

// moveOver renames tmp over final. The plain call is refused while anyone
// has final open; then the newer call is tried, which puts the new file
// under the name while those who opened the old one with leave to, as Read
// does, keep the old one. Whoever opened it without that leave is waited for.
func moveOver(tmp, final string) error {
	err := os.Rename(tmp, final)
	var code syscall.Errno
	if errors.As(err, &code) && slices.Contains(heldOpen, code) && moveUnder(tmp, final) == nil {
		return nil
	}
	return err
}

func moveUnder(tmp, final string) error {
	from, err := windows.UTF16PtrFromString(tmp)
	to, err2 := windows.UTF16FromString(final)
	if err != nil || err2 != nil {
		return errors.Join(err, err2)
	}
	h, err := windows.CreateFile(from, windows.DELETE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	// What the call reads: how to rename, a folder handle we leave empty,
	// the length of the new name in bytes, and the name with its end mark.
	word := strconv.IntSize / 8
	rec := make([]byte, 2*word+4+2*len(to))
	binary.LittleEndian.PutUint32(rec, windows.FILE_RENAME_REPLACE_IF_EXISTS|windows.FILE_RENAME_POSIX_SEMANTICS)
	binary.LittleEndian.PutUint32(rec[2*word:], uint32(2*(len(to)-1)))
	for i, unit := range to {
		binary.LittleEndian.PutUint16(rec[2*word+4+2*i:], unit)
	}
	return windows.SetFileInformationByHandle(h, windows.FileRenameInfoEx, &rec[0], uint32(len(rec)))
}

// owner cannot tell: who may write a file on Windows is not read yet.
func owner(fs.FileInfo) (uid, gid uint32, ok bool) { return 0, 0, false }
