//go:build unix

package dash

import "syscall"

// apart starts the server in a session of its own, so that it does not end
// with the terminal `dash start` or `dash url` was typed in.
var apart = &syscall.SysProcAttr{Setsid: true}
