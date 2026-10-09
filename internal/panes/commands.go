// Package panes draws the fleet pane and the action pane. A pane keeps
// nothing and changes nothing: it reads the record, herdr's picture and a few
// small files, has one view model built from them, and draws that.
package panes

import (
	"fmt"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) { k.Handle("ui", ui) }

// ui runs a pane program in the pane it was started in, until the pane
// closes or the person leaves it.
func ui(c *contract.Call) (any, error) {
	if len(c.Args) != 2 || c.Args[0] != "run" || c.Args[1] != fleet && c.Args[1] != actions {
		return nil, fmt.Errorf("ui: only `ui run fleet` and `ui run actions` exist so far: the rest is %w", contract.ErrNotBuilt)
	}
	t, err := term.Open()
	if err != nil {
		return nil, err
	}
	defer t.Close()
	Run(c.Args[1], t, c.Kit, c.Root, c.Run)
	return nil, nil
}

// Run draws one pane, "fleet" or "actions", on a terminal until its input
// ends or the person leaves. With no run named it shows the project's
// current one.
func Run(kind string, t *term.Term, k *contract.Kit, root, run string) {
	newPane(kind, t, k, root, run, contract.Now).loop()
}
