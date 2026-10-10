// Package pty is a terminal of its own for each pane: opening it, starting a
// program in it from a list of arguments, its size, its end. It is written
// from the systems' own pages: pty(4) and tty(4) on macOS, pty(7) and
// ioctl_tty(2) on Linux, and Microsoft's "Creating a Pseudoconsole session".
package pty

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Plug puts the starter of a pane's terminal into the kit.
func Plug(k *contract.Kit) { k.Pty = Start }

// maxSide bounds a terminal's width and height, so that no caller can ask
// the system, or a program in the pane, for a picture of absurd size.
const maxSide = 4096

func sized(cols, rows int) error {
	if cols < 1 || rows < 1 || cols > maxSide || rows > maxSide {
		return fmt.Errorf("pty: a size of %d by %d", cols, rows)
	}
	return nil
}

// check refuses what no system can start and finds the program's file: a
// bare name is looked for as a shell would, anything else is taken as it is.
func check(s contract.PtySpec) (path string, err error) {
	if len(s.Argv) == 0 || s.Argv[0] == "" {
		return "", errors.New("pty: no program to start")
	}
	if err = sized(s.Cols, s.Rows); err != nil {
		return "", err
	}
	for _, v := range append(append([]string{s.Dir}, s.Argv...), s.Env...) {
		if strings.ContainsRune(v, 0) {
			return "", errors.New("pty: a zero byte in an argument, the folder or a variable")
		}
	}
	if path = s.Argv[0]; filepath.Base(path) == path {
		path, err = exec.LookPath(path)
	}
	return path, err
}

// ending is how a program's end reaches everyone who waits for it.
type ending struct {
	done chan struct{}
	code int
	err  error
}

func (e *ending) Wait() (int, error) {
	<-e.done
	return e.code, e.err
}
