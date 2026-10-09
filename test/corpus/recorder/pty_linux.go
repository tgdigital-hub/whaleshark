package main

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// openPTY makes a pseudo-terminal: the side a terminal program holds, and
// the side a program inside it sees (pty(7)).
func openPTY() (outer, inner *os.File, err error) {
	outer, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	if err = unix.IoctlSetPointerInt(int(outer.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		return nil, nil, err
	}
	n, err := unix.IoctlGetInt(int(outer.Fd()), unix.TIOCGPTN)
	if err != nil {
		return nil, nil, err
	}
	inner, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	return outer, inner, err
}
