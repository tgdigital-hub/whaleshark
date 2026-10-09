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

// Build makes the view model a pane draws. Until the view package is joined
// to it, a pane shows its own lines and says that the rest is not built.
var Build = func(in contract.ViewInput) contract.View {
	return contract.View{Version: contract.ViewVersion, At: in.Now, Caller: in.Caller,
		Alerts: []string{"the view model is " + contract.ErrNotBuilt.Error()}, Fresh: contract.Fresh{Checked: in.Checked}}
}

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
	newPane(c.Args[1], t, c.Kit, c.Root, c.Run, contract.Now).loop()
	return nil, nil
}
