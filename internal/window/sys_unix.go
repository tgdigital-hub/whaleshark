//go:build unix

package window

import "syscall"

// apart starts the keeper in a session of its own, so that it does not end
// with this terminal.
var apart = &syscall.SysProcAttr{Setsid: true}
