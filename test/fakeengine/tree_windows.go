package fakeengine

import (
	"os/exec"
	"strconv"
)

// apart has nothing to prepare here: the system's own tool finds what an
// agent started by who started it.
func apart(*exec.Cmd) {}

func endTree(cmd *exec.Cmd) {
	if exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run() != nil {
		cmd.Process.Kill()
	}
}

// treeGone: the tool has returned only once it ended them.
func treeGone(int) bool { return true }
