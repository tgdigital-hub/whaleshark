// Package panes draws the left section, the fleet pane, the action pane and
// the settings screen, and places the panes as the layout says. A pane keeps
// nothing and changes nothing: it reads the record, the terminals' picture and a few
// small files, has one view model built from them, and draws that. What a
// click or a key changes, a child command changes: the program itself,
// started with one row of the action table.
package panes

import (
	"cmp"
	"context"
	"os"
	"os/exec"
	"slices"
	"syscall"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) {
	k.Handle("ui", ui)
	k.Handle("set", set)
	k.Panes = Panes
}

// ui opens and closes the two panes beside the conversation, gives the
// action pane the keys, or, as `ui run` and `ui menu`, is the program inside
// a pane until the pane closes or the person leaves it. `ui run offer` is the
// menu at the pointer: for a kind of thing and its id, then its project, its
// run and its task where it has them, a dash for one it has not, and the
// word "row" where it opens on a row of the left section.
func ui(c *contract.Call) (any, error) {
	a := append(c.Args, "", "")[:max(len(c.Args), 2)]
	one := a[0] == fleet || a[0] == actions
	was := a
	if a[0] == "run" && a[1] == offer && len(a) >= 4 && len(a) <= 8 {
		a = a[:2]
	}
	switch {
	case len(a) > 2:
	case a[0] == "run" && slices.Contains([]string{fleet, actions, side, settings, offer}, a[1]) && (a[1] == offer) == (len(was) > 2), a[0] == menu && a[1] == "":
		t, err := term.Open()
		if err != nil {
			return nil, err
		}
		defer t.Close()
		// Only a run that was named is held to: a pane of the lead agent's
		// tab shows whichever run that agent drives, also the next one.
		run := ""
		if c.Flags["run"] != nil {
			run = c.Run
		}
		p := newPane(cmp.Or(a[1], menu), t, c.Kit, c.Root, run, contract.Now)
		p.want = was[2:]
		if p.self, _ = c.Kit.Platform.SelfPath(); p.loop() {
			t.Close()
			return nil, again(p.self)
		}
		return nil, nil
	case a[0] == settings && a[1] == "":
		self, _ := c.Kit.Platform.SelfPath()
		return nil, c.Kit.Terms.Overlay([]string{self, "ui", "run", settings}, 0, 0)
	case a[0] == actions && a[1] == "focus":
		_, ui, _, err := screen(c.Kit)
		snap, _ := c.Kit.Terms.Snapshot(context.Background())
		if pair := pairOf(ui, snap, rootOf(snap, c.Caller.Pane)); err == nil && pair.Actions == "" {
			err = cannot("The action pane is closed: whaleshark ui actions on")
		} else if err == nil {
			err = hand(c.Kit.Terms, pair, actions)
		}
		return nil, err
	case a[0] == "" || a[0] == "close" && a[1] == "":
		return place(c, func(string, bool) bool { return a[0] == "" })
	case one && (a[1] == "" || a[1] == "on" || a[1] == "off" || a[1] == "toggle"):
		return place(c, func(kind string, open bool) bool {
			if kind != a[0] {
				return open
			}
			return a[1] == "on" || a[1] != "off" && !open
		})
	}
	return nil, &contract.Refusal{Exit: contract.ExitUsage, Code: "usage", Message: "Usage: whaleshark " + c.Command.Usage}
}

// again starts the new program file in this one's place: where the system
// can, this program becomes it; elsewhere it is started and waited for.
func again(self string) error {
	args := append([]string{self}, os.Args[1:]...)
	// #nosec G204 G702 -- this program's own file, with its own arguments
	if err := syscall.Exec(self, args, os.Environ()); err == nil {
		return nil
	}
	cmd := exec.Command(self, args[1:]...) // #nosec G204 G702 -- as above
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// Run draws one pane, "fleet" or "actions", on a terminal until its input
// ends or the person leaves. With no run named it shows the run of the lead
// agent in its own tab, and with none there the project's current one. A button starts the program WHALESHARK_BIN names, and with
// none it starts nothing: a pane inside a test never runs the test again.
func Run(kind string, t *term.Term, k *contract.Kit, root, run string) {
	p := newPane(kind, t, k, root, run, contract.Now)
	p.self = os.Getenv(contract.EnvBin)
	p.loop()
}
