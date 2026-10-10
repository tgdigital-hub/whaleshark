package dash

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// apart starts the server with no console and in a group of its own, so
// that it does not end with the terminal `dash start` or `dash url` was
// typed in.
var apart = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
