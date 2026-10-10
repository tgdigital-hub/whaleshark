package window

import (
	"syscall"

	"golang.org/x/sys/windows"
)

const system = "windows"

// apart starts the keeper with no console and in a group of its own, so
// that it does not end with this terminal.
var apart = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
