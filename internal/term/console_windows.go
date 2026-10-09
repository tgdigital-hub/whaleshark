package term

import (
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// waitResize returns when the terminal may have a new size. The console
// sends no notice a raw reader can use, so it is looked at five times a second.
func waitResize() { time.Sleep(200 * time.Millisecond) }

// console makes the console speak in the same sequences as every other
// terminal, in and out; undo puts the output back.
func console(in, out *os.File) (undo func()) {
	var mode, was uint32
	if h := windows.Handle(in.Fd()); windows.GetConsoleMode(h, &mode) == nil {
		windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_INPUT)
	}
	h := windows.Handle(out.Fd())
	if windows.GetConsoleMode(h, &was) != nil {
		return func() {}
	}
	windows.SetConsoleMode(h, was|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING|windows.DISABLE_NEWLINE_AUTO_RETURN)
	return func() { windows.SetConsoleMode(h, was) }
}
