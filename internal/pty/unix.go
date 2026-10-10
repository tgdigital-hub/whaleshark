//go:build darwin || linux

package pty

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// grace is how long the programs of a closing terminal have to end by
// themselves, as they do when a person closes a window.
const grace = time.Second

// terminal is a pseudo-terminal with a program in a session of its own. The
// session's number is the program's process id; it is 0 once the session
// has been seen empty, because the system may then give the number away.
type terminal struct {
	ending
	outer *os.File
	sid   atomic.Int64
	close sync.Once
}

// Start opens a terminal and starts the program in it, with the terminal as
// the controlling terminal of a new session and as its three standard files.
func Start(s contract.PtySpec) (contract.Pty, error) {
	path, err := check(s)
	if err != nil {
		return nil, err
	}
	outer, inner, err := open()
	if err != nil {
		return nil, err
	}
	defer inner.Close()
	t := &terminal{ending: ending{done: make(chan struct{})}, outer: outer}
	// A nil Env would hand on this process's own variables.
	cmd := &exec.Cmd{Path: path, Args: s.Argv, Dir: s.Dir, Env: append([]string{}, s.Env...),
		Stdin: inner, Stdout: inner, Stderr: inner,
		SysProcAttr: &syscall.SysProcAttr{Setsid: true, Setctty: true}}
	if err = t.Resize(s.Cols, s.Rows); err == nil {
		err = cmd.Start()
	}
	if err != nil {
		outer.Close()
		return nil, err
	}
	t.sid.Store(int64(cmd.Process.Pid))
	go func() {
		err := cmd.Wait()
		if cmd.ProcessState != nil {
			t.code, err = cmd.ProcessState.ExitCode(), nil
		}
		t.err = err
		if len(t.members()) == 0 {
			t.sid.Store(0)
		}
		close(t.done)
	}()
	return t, nil
}

func (t *terminal) Read(p []byte) (int, error) {
	n, err := t.outer.Read(p)
	if errors.Is(err, syscall.EIO) {
		err = io.EOF // how Linux says that nothing holds the inner side any more
	}
	return n, err
}

func (t *terminal) Write(p []byte) (int, error) { return t.outer.Write(p) }

// control runs one control call on the terminal, unless it is closed.
func (t *terminal) control(call func(fd int) error) error {
	conn, err := t.outer.SyscallConn()
	if err != nil {
		return err
	}
	if e := conn.Control(func(fd uintptr) { err = call(int(fd)) }); e != nil {
		return e
	}
	return err
}

// Resize sets the size; the system tells the program in front.
func (t *terminal) Resize(cols, rows int) error {
	if err := sized(cols, rows); err != nil {
		return err
	}
	size := &unix.Winsize{Col: uint16(cols), Row: uint16(rows)} // #nosec G115 -- sized holds both under maxSide
	return t.control(func(fd int) error { return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, size) })
}

// Front is the name of the leader of the terminal's foreground process group.
func (t *terminal) Front() (string, error) {
	var group int
	err := t.control(func(fd int) (err error) {
		group, err = unix.IoctlGetInt(fd, unix.TIOCGPGRP)
		return err
	})
	if err != nil {
		return "", err
	}
	return name(group)
}

// members are the processes of the terminal's session.
func (t *terminal) members() (in []int) {
	sid := int(t.sid.Load())
	if sid == 0 {
		return nil
	}
	for _, pid := range pids() {
		if s, _ := unix.Getsid(pid); s == sid {
			in = append(in, pid)
		}
	}
	return in
}

// Close hangs up on every process of the session, as a closing window does,
// and kills what is still there after the grace.
func (t *terminal) Close() error {
	t.close.Do(func() {
		for start, first := time.Now(), true; time.Since(start) < 2*grace; first = false {
			left := t.members()
			if len(left) == 0 {
				break
			}
			for _, pid := range left {
				if first {
					// A stopped program takes the hang-up only once it is continued.
					_ = syscall.Kill(pid, syscall.SIGHUP)
					_ = syscall.Kill(pid, syscall.SIGCONT)
				} else if time.Since(start) > grace {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			}
			time.Sleep(2 * time.Millisecond)
		}
		t.outer.Close()
	})
	return nil
}
