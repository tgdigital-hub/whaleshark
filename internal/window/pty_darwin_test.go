package window

import (
	"bytes"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const getTermios = unix.TIOCGETA

// openPTY makes a pseudo-terminal: the side a terminal program holds, and
// the side a program inside it sees.
func openPTY() (outer, inner *os.File, err error) {
	outer, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	name := make([]byte, 128)
	for _, call := range []struct{ req, arg uintptr }{
		{unix.TIOCPTYGRANT, 0}, {unix.TIOCPTYUNLK, 0}, {unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0]))},
	} {
		if _, _, errno := unix.Syscall(unix.SYS_IOCTL, outer.Fd(), call.req, call.arg); errno != 0 {
			return nil, nil, errno
		}
	}
	inner, err = os.OpenFile(string(name[:bytes.IndexByte(name, 0)]), os.O_RDWR|syscall.O_NOCTTY, 0)
	return outer, inner, err
}
