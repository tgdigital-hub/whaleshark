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

// ui opens and closes the two panes beside the conversation, or, as `ui run`,
// is the program inside one of them until the pane closes or the person
// leaves it.
func ui(c *contract.Call) (any, error) {
	a := append(c.Args, "", "")[:max(len(c.Args), 2)]
	one := a[0] == fleet || a[0] == actions
	switch {
	case len(a) > 2 || a[0] == "menu":
	case a[0] == "run" && (a[1] == fleet || a[1] == actions):
		t, err := term.Open()
		if err != nil {
			return nil, err
		}
		defer t.Close()
		Run(a[1], t, c.Kit, c.Root, c.Run)
		return nil, nil
	case a[0] == "" || a[0] == "close" && a[1] == "":
		return place(c, func(string, bool) bool { return a[0] == "" })
	case one && (a[1] == "" || a[1] == "on" || a[1] == "off"):
		return place(c, func(kind string, open bool) bool {
			if kind != a[0] {
				return open
			}
			return a[1] == "on" || a[1] == "" && !open
		})
	}
	if a[0] == "menu" {
		return nil, fmt.Errorf("ui menu: %w", contract.ErrNotBuilt)
	}
	return nil, &contract.Refusal{Exit: contract.ExitUsage, Code: "usage", Message: "Usage: whaleshark " + c.Command.Usage}
}

// Run draws one pane, "fleet" or "actions", on a terminal until its input
// ends or the person leaves. With no run named it shows the project's
// current one.
func Run(kind string, t *term.Term, k *contract.Kit, root, run string) {
	newPane(kind, t, k, root, run, contract.Now).loop()
}
