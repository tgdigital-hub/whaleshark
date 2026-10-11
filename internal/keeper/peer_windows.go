package keeper

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// peer cannot ask on Windows: the system has no such question for a socket
// file, and the folder's permissions are the whole defence there.
func peer(uintptr) (int, error) { return os.Getuid(), nil }

// leaveConsole lets go of a console this process has to itself: the window
// Windows opens for a keeper it starts at log-on, which would otherwise
// stay on the screen, and end the keeper with every pane when it is closed.
// A keeper typed into a terminal shares that terminal's console with the
// shell and keeps it; one started by the window has none.
func leaveConsole() {
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	var sharing [2]uint32
	// #nosec G103 -- the system writes at most the two numbers it is given room for
	if n, _, _ := kernel.NewProc("GetConsoleProcessList").Call(uintptr(unsafe.Pointer(&sharing[0])), 2); n == 1 {
		_, _, _ = kernel.NewProc("FreeConsole").Call()
	}
}
