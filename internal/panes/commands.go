// Package panes is not built yet. Its owner replaces this file. Until then it
// holds an empty pane, so that the program file is measured with a pane in it.
package panes

import (
	"fmt"
	"os"
	"strings"

	"github.com/rivo/uniseg"
	"golang.org/x/term"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) { k.Handle("empty-pane", emptyPane) }

// emptyPane draws one centred line on the alternate screen and leaves at the
// first key, putting the terminal back as it was.
func emptyPane(*contract.Call) (any, error) {
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	defer term.Restore(fd, old)
	cols, rows, err := term.GetSize(fd)
	if err != nil {
		return nil, err
	}
	line := "FLEET · " + contract.ErrNotBuilt.Error()
	pad := strings.Repeat(" ", max(0, (cols-uniseg.StringWidth(line))/2))
	fmt.Print("\x1b[?1049h\x1b[2J\x1b[", rows/2+1, ";1H", pad, line)
	defer fmt.Print("\x1b[?1049l")
	_, err = os.Stdin.Read(make([]byte, 1))
	return nil, err
}
