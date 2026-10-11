//go:build !windows

package fakeengine

import (
	"os/exec"
	"syscall"
)

// apart starts an agent in a process group of its own, so that endTree
// reaches every command the agent has started and not yet seen end.
func apart(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func endTree(cmd *exec.Cmd) { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }

// treeGone reports whether nothing of an agent's group is left.
func treeGone(pid int) bool { return syscall.Kill(-pid, 0) != nil }
