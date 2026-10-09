//go:build unix

package term

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

var resized = sync.OnceValue(func() chan os.Signal {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGWINCH)
	return c
})

// waitResize returns when the terminal may have a new size.
func waitResize() { <-resized() }

// console has nothing to prepare here; undo does nothing.
func console(in, out *os.File) (undo func()) { return func() {} }
