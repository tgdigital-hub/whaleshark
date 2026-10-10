package pty

import (
	"bytes"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// open makes a pseudo-terminal: the outer side, which the keeper holds, and
// the inner side, which the program sees. The three control calls of pty(4)
// grant the inner side to this login, unlock it and give its name.
func open() (outer, inner *os.File, err error) {
	if outer, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0); err != nil {
		return nil, nil, err
	}
	var path [128]byte
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, outer.Fd(), unix.TIOCPTYGRANT, 0)
	if errno == 0 {
		_, _, errno = unix.Syscall(unix.SYS_IOCTL, outer.Fd(), unix.TIOCPTYUNLK, 0)
	}
	if errno == 0 {
		// #nosec G103 -- the system writes the name here, at most these 128 bytes
		_, _, errno = unix.Syscall(unix.SYS_IOCTL, outer.Fd(), unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&path)))
	}
	if errno != 0 {
		outer.Close()
		return nil, nil, errno
	}
	end := max(bytes.IndexByte(path[:], 0), 0)
	if inner, err = os.OpenFile(string(path[:end]), os.O_RDWR|syscall.O_NOCTTY, 0); err != nil {
		outer.Close()
	}
	return outer, inner, err
}

// pids are all the processes there are.
func pids() (all []int) {
	procs, _ := unix.SysctlKinfoProcSlice("kern.proc.all")
	for i := range procs {
		all = append(all, int(procs[i].Proc.P_pid))
	}
	return all
}

// name is a process's short name.
func name(pid int) (string, error) {
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	comm := proc.Proc.P_comm[:]
	if proc.Proc.P_pid == 0 || comm[0] == 0 {
		return "", os.ErrProcessDone
	}
	return string(comm[:bytes.IndexByte(comm, 0)]), nil
}
