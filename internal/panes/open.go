package panes

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const (
	// The fleet takes fleetShare of the width, or fleetLeast cells where that
	// is more, until the person has left it at another, and the conversation
	// keeps talkShare of the height above the action pane. Under tightBelow
	// cells the fleet takes a third at most, and under noneBelow no pane is
	// opened at all.
	fleetShare = 0.3
	fleetLeast = 34
	talkShare  = 0.75
	tightBelow = 90
	noneBelow  = 60
)

// Opened is what `ui` answers: the pane of each of ours that is open now.
type Opened struct {
	Fleet   string `json:"fleet,omitempty"`
	Actions string `json:"actions,omitempty"`
}

func cannot(format string, a ...any) *contract.Refusal {
	return &contract.Refusal{Exit: contract.ExitEnv, Code: "no_panes", Message: fmt.Sprintf(format, a...)}
}

// place opens and closes our two panes beside the pane the command was run
// in, and in no other tab. want says, for one of ours and whether it is open,
// whether it shall be. A pane counts as ours only while it is the terminal
// that was opened for it; nothing else is ever closed.
func place(c *contract.Call, want func(kind string, open bool) bool) (any, error) {
	k, me := c.Kit, c.Caller.Pane
	if me == "" {
		return nil, cannot("The panes open beside the conversation: run this in the lead agent's tab.")
	}
	dirs, err := k.Platform.Dirs()
	if err != nil {
		return nil, err
	}
	snap, err := k.Terms.Snapshot(context.Background())
	if err != nil {
		return nil, cannot("The panes cannot be opened: %v.", err)
	}
	panes := map[string]contract.Pane{}
	for _, p := range snap.Panes {
		panes[p.ID] = p
	}
	file := filepath.Join(dirs.State, "ui.json")
	var ui contract.UIFile
	if err := contract.ReadVersioned(k.Platform.Read, file, contract.FileVersion, &ui); err != nil {
		return nil, err
	}
	was := ui
	if _, there := panes[me]; !there {
		return nil, cannot("This pane (%s) is not one the terminal engine knows.", me)
	}
	ours := []struct {
		kind       string
		pane, term *string
		open       bool
	}{{fleet, &ui.FleetPane, &ui.FleetTerminal, false}, {actions, &ui.ActionsPane, &ui.ActionsTerminal, false}}
	for i := range ours {
		o := &ours[i]
		p, there := panes[*o.pane]
		switch o.open = there && *o.pane != "" && p.Terminal == *o.term; {
		case !o.open:
			*o.pane, *o.term = "", ""
		case p.Tab != panes[me].Tab:
			return nil, cannot("The panes are open in another tab (%s): run `whaleshark ui close` there first.", p.Label)
		case o.kind == fleet:
			if w, _, err := k.Terms.Size(p.ID); err == nil && w > 0 {
				ui.FleetWidth = w
			}
		}
	}
	fl, ac := &ours[0], &ours[1]
	wantFleet, wantActions := want(fleet, fl.open), want(actions, ac.open)
	if (wantFleet && !fl.open || wantActions && !ac.open) && (me == *fl.pane || me == *ac.pane) {
		return nil, cannot("This is one of the two panes: run this in the conversation's pane.")
	}
	// The conversation's pane is as wide as the tab while the fleet is closed.
	w := 0
	if wantFleet && !fl.open {
		w, _, _ = k.Terms.Size(me)
	}
	if w > 0 && w < noneBelow {
		return nil, cannot("This terminal is %d columns wide, too narrow for panes: `whaleshark status` shows the same.", w)
	}
	// The fleet keeps the full height only when it is split off before the
	// action pane, so an action pane that is in its way is opened again.
	again := wantFleet && !fl.open && ac.open
	if ac.open && (!wantActions || again) {
		if err := k.Terms.PaneClose(*ac.pane); err != nil {
			return nil, err
		}
		*ac.pane, *ac.term, ac.open = "", "", false
	}
	if fl.open && !wantFleet {
		if err := k.Terms.PaneClose(*fl.pane); err != nil {
			return nil, err
		}
		*fl.pane, *fl.term, fl.open = "", "", false
	}
	self, err := k.Platform.SelfPath()
	if err != nil {
		self = "whaleshark"
	}
	split := func(kind, direction string, keep float64, pane, term *string) error {
		p, err := k.Terms.Split(me, direction, keep)
		if err == nil {
			*pane, *term = p.ID, p.Terminal
			// exec, so that the pane closes with its program.
			err = k.Terms.Run(p.ID, []string{"exec", self, "ui", "run", kind, "--root", c.Root})
		}
		return err
	}
	if wantFleet && !fl.open {
		share := fleetShare
		if ui.FleetWidth > 0 && w > 0 {
			share = float64(ui.FleetWidth) / float64(w)
		} else if w > 0 {
			share = max(share, fleetLeast/float64(w))
		}
		if w > 0 && w < tightBelow {
			share = min(share, 1.0/3)
		}
		err = split(fleet, "right", math.Round(1000*(1-min(max(share, 0.1), 0.9)))/1000, fl.pane, fl.term)
	}
	if err == nil && wantActions && !ac.open {
		err = split(actions, "down", talkShare, ac.pane, ac.term)
	}
	// What was opened is written down even when a later step failed.
	if fmt.Sprint(ui) != fmt.Sprint(was) {
		ui.Version = contract.FileVersion
		if werr := contract.WriteVersioned(k.Platform, file, contract.FileVersion, ui); err == nil {
			err = werr
		}
	}
	if err != nil {
		return nil, err
	}
	var said []string
	for _, o := range ours {
		state := "closed"
		if *o.pane != "" {
			state = "open (" + *o.pane + ")"
		}
		said = append(said, o.kind+" "+state)
	}
	fmt.Fprintln(c.Out, strings.Join(said, " · "))
	return Opened{ui.FleetPane, ui.ActionsPane}, nil
}
