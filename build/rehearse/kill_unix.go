//go:build !windows

package rehearse

import "syscall"

// killProcess ends the process at once; the rehearsal is for Linux and Mac.
func killProcess(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }
