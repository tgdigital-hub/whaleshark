package panes

import (
	"context"
	"fmt"
	"math"
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
	// A dividing line is moved by a share of its split: step at one key, and
	// at most the half in one call, which takes a pane down to the tenth
	// that is the least it is left.
	step  = 0.02
	most  = 0.5
	least = 0.1
)

// widen moves the fleet's edge one step and returns the fleet's new width.
func widen(t contract.Terminals, pane string, wider bool) (int, error) {
	if err := t.Resize(pane, map[bool]string{true: contract.Left, false: contract.Right}[wider], step); err != nil {
		return 0, err
	}
	w, _, err := t.Size(pane)
	return w, err
}

// shrink takes the action pane down to the least a pane is left, or
// gives it back the share it opens with. Its dividing line lies towards the
// conversation: above a pane at the bottom, below one at the top.
func shrink(t contract.Terminals, pane string, top, folded bool) error {
	amount := 1 - talkShare - least
	if folded {
		amount = most
	}
	return t.Resize(pane, map[bool]string{true: contract.Down, false: contract.Up}[folded != top], amount)
}

// fit brings an open fleet to a width of n cells, asked from the pane beside
// it: the line between the two moves by a share of both together.
func fit(t contract.Terminals, me, pane string, n int) error {
	if pane == "" || me == "" || me == pane {
		return nil
	}
	w, _, err := t.Size(pane)
	mine, _, _ := t.Size(me)
	if err != nil || w == n || w+mine == 0 {
		return err
	}
	return t.Resize(pane, map[bool]string{true: contract.Left, false: contract.Right}[n > w], min(math.Abs(float64(n-w))/float64(w+mine), most))
}

// talk finds the conversation's pane from ours: one of ours is asked for the
// pane on its far side, which takes the keys, and the conversation is where
// they land.
func talk(t contract.Terminals, ui contract.UIFile) (string, error) {
	for _, way := range [][2]string{{ui.ActionsPane, contract.Up}, {ui.ActionsPane, contract.Down}, {ui.FleetPane, contract.Left}} {
		if way[0] == "" {
			continue
		}
		if got, err := t.PaneFocus(way[0], way[1]); err != nil || got != "" && got != ui.ActionsPane && got != ui.FleetPane {
			return got, err
		}
	}
	return "", nil
}

// hand gives the keys to the conversation, or through it to one of ours.
func hand(t contract.Terminals, ui contract.UIFile, to string) error {
	from, err := talk(t, ui)
	want, ways := ui.FleetPane, []string{contract.Right}
	if to == actions {
		want, ways = ui.ActionsPane, []string{contract.Down, contract.Up}
	}
	for _, dir := range ways {
		if err != nil || from == "" || want == "" || to == "" {
			break
		}
		var got string
		if got, err = t.PaneFocus(from, dir); got == want {
			break
		}
	}
	return err
}

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
// whether it shall be. A pane counts as ours only while the keeper marks it
// as a pane program of ours; nothing else is ever closed.
func place(c *contract.Call, want func(kind string, open bool) bool) (any, error) {
	k, me := c.Kit, c.Caller.Pane
	if me == "" {
		return nil, cannot("The panes open beside the conversation: run this in the lead agent's tab.")
	}
	dirs, ui, file, err := screen(k)
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
	was := ui
	if _, there := panes[me]; !there {
		return nil, cannot("This pane (%s) is not one the terminal engine knows.", me)
	}
	ours := []struct {
		kind string
		pane *string
		open bool
	}{{fleet, &ui.FleetPane, false}, {actions, &ui.ActionsPane, false}}
	for i := range ours {
		o := &ours[i]
		p, there := panes[*o.pane]
		switch o.open = there && *o.pane != "" && p.Ours; {
		case !o.open:
			*o.pane = ""
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
	asker := ""
	if me == *fl.pane || me == *ac.pane {
		// One of the two panes asks, as a key in it does. The panes open
		// beside the conversation; finding it takes the keys there, and they
		// are given back at the end.
		asker = map[bool]string{true: fleet, false: actions}[me == *fl.pane]
		if me, err = talk(k.Terms, ui); err != nil || me == "" {
			return nil, cannot("The conversation's pane was not found beside this one: run this there.")
		}
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
	// action pane, so an action pane that is in its way is opened again:
	// unless it is the one that asks, which would end with this command.
	again := wantFleet && !fl.open && ac.open && asker != actions
	if ac.open && (!wantActions || again) {
		if err := k.Terms.PaneClose(*ac.pane); err != nil {
			return nil, err
		}
		*ac.pane, ac.open = "", false
	}
	if fl.open && !wantFleet {
		if err := k.Terms.PaneClose(*fl.pane); err != nil {
			return nil, err
		}
		*fl.pane, fl.open = "", false
	}
	self, err := k.Platform.SelfPath()
	if err != nil {
		self = "whaleshark"
	}
	split := func(kind, direction string, keep float64, pane *string) error {
		p, err := k.Terms.Split(me, direction, keep)
		if err == nil {
			// The keeper starts the program in the shell's place, marks the
			// pane as ours, closes it with its program and brings it back
			// after a restart.
			*pane = p.ID
			err = k.Terms.Run(p.ID, []string{self, "ui", "run", kind, "--root", c.Root})
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
		err = split(fleet, "right", math.Round(1000*(1-min(max(share, 0.1), 0.9)))/1000, fl.pane)
	}
	if err == nil && wantActions && !ac.open {
		side := map[bool]string{true: contract.Up, false: contract.Down}[person(k, dirs).UI.Actions == "top"]
		err = split(actions, side, talkShare, ac.pane)
	}
	// What was opened is written down even when a later step failed.
	if fmt.Sprint(ui) != fmt.Sprint(was) {
		ui.Version = contract.FileVersion
		if werr := contract.WriteVersioned(k.Platform, file, contract.FileVersion, ui); err == nil {
			err = werr
		}
	}
	if err == nil && asker != "" {
		err = hand(k.Terms, ui, asker)
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
