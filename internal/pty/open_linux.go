package pty

import (
	"bytes"
	"os"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// open makes a pseudo-terminal: the outer side, which the keeper holds, and
// the inner side, which the program sees. As pty(7) has it: unlock the inner
// side, ask its number, and open the file of that number.
func open() (outer, inner *os.File, err error) {
	if outer, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0); err != nil {
		return nil, nil, err
	}
	fd, n := int(outer.Fd()), 0 // #nosec G115 -- a file's number
	if err = unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err == nil {
		n, err = unix.IoctlGetInt(fd, unix.TIOCGPTN)
	}
	if err == nil {
		inner, err = os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|syscall.O_NOCTTY, 0)
	}
	if err != nil {
		outer.Close()
	}
	return outer, inner, err
}

// pids are all the processes there are.
func pids() (all []int) {
	dir, err := os.Open("/proc")
	if err != nil {
		return nil
	}
	defer dir.Close()
	names, _ := dir.Readdirnames(-1)
	for _, name := range names {
		if pid, err := strconv.Atoi(name); err == nil {
			all = append(all, pid)
		}
	}
	return all
}

// name is a process's short name.
func name(pid int) (string, error) {
	comm, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	return string(bytes.TrimSpace(comm)), err
}
