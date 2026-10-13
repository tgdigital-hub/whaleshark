package panes

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const (
	// The conversation keeps talkShare of the height above an open action
	// pane; a pane that is its strip has stripRows.
	talkShare = 0.75
	stripRows = 2
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

// Panes is the kit's Panes: it opens, closes and moves our two panes as the
// person's layout says, beside pane or, with none named, in the tab in
// front. A keeper out of reach, or no tab to open them in, is no error.
func Panes(k *contract.Kit, pane string) error {
	_, err := place(&contract.Call{Kit: k, Caller: contract.Caller{Pane: pane}, Out: io.Discard}, nil)
	if r := new(contract.Refusal); errors.As(err, &r) && r.Code == "no_panes" {
		return nil
	}
	return err
}

// refit lays the two panes out again for a window that took another width,
// in the tab they stand in, and opens nothing where neither is open: the
// file names them, and a pane it names that is gone stands for nothing.
func refit(k *contract.Kit) error {
	_, ui, _, err := screen(k)
	for _, ours := range []string{ui.FleetPane, ui.ActionsPane} {
		if err != nil || ours == "" {
			continue
		}
		_, err = place(&contract.Call{Kit: k, Caller: contract.Caller{Pane: ours}, Out: io.Discard}, nil)
		if r := new(contract.Refusal); !errors.As(err, &r) || r.Code != "no_panes" {
			return err
		}
		err = nil
	}
	return err
}

// place opens and closes our two panes beside the pane the command was run
// in; one that is open in another tab is closed there first. want says, for
// one of ours and whether it is open, whether it shall be; with none it is
// the layout that says, and every pane of ours is opened anew in its place.
// The form of each is contract.Fit's for the window. A pane counts as ours
// only while the keeper marks it as a pane program of ours; nothing else is
// ever closed.
func place(c *contract.Call, want func(kind string, open bool) bool) (any, error) {
	k, me, anew := c.Kit, c.Caller.Pane, want == nil
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
		if panes[p.ID] = p; anew && me == "" && p.Focused && !p.Dock {
			me = p.ID
		}
	}
	// The layout is about the conversation: a screen of ours that asks, as
	// the settings screen does, stands for the pane of its tab that is not ours.
	for _, p := range snap.Panes {
		if anew && panes[me].Ours && p.Tab == panes[me].Tab && !p.Ours {
			me = p.ID
		}
	}
	was := ui
	if _, there := panes[me]; !there || me == "" {
		return nil, cannot("The panes open beside the conversation: run this in the lead agent's tab.")
	}
	cfg := person(k, dirs)
	lay := contract.Layout{FleetWide: ui.FleetWidth, Fleet: cfg.Panes.Fleet, Actions: cfg.Panes.Actions}
	if anew {
		want = func(kind string, _ bool) bool {
			return map[string]string{fleet: lay.Fleet, actions: lay.Actions}[kind] != contract.Off
		}
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
		case anew || p.Tab != panes[me].Tab:
			// The panes belong beside the conversation this is run in, so a
			// pane of ours elsewhere is closed, and opened again below if wanted.
			if err := k.Terms.PaneClose(p.ID); err != nil {
				return nil, err
			}
			*o.pane, o.open = "", false
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
	// The window is the conversation, which is as wide as the tab while the
	// fleet is closed, a line and the left section in its dock.
	w, _, _ := k.Terms.Size(me)
	cols := w + contract.SideRail + 1
	for _, p := range snap.Panes {
		if dock, _, err := k.Terms.Size(p.ID); p.Dock && err == nil {
			cols = w + dock + 1
		}
	}
	engine, _ := contract.ReadSettings(k.Platform.Peek, dirs)
	if lay.Side = engine.Side.Width; lay.Fleet == contract.Off {
		lay.Fleet = contract.Right // asked for by name, it opens where it comes
	}
	form := contract.Fit(cols, lay)
	if wantFleet && !fl.open && form.FleetForm == contract.FormOff && !anew {
		return nil, cannot("This terminal is %d columns wide, too narrow for the fleet: `whaleshark status` shows the same.", w)
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
			argv := []string{self, "ui", "run", kind}
			if *pane = p.ID; c.Root != "" {
				argv = append(argv, "--root", c.Root)
			}
			err = k.Terms.Run(p.ID, argv)
		}
		return err
	}
	if wantFleet && !fl.open && form.FleetForm != contract.FormOff {
		// Beside the conversation it has Fit's width in cells, at the bottom
		// its height in rows, and as a strip its one line and its age.
		switch {
		case form.FleetForm == contract.FormStrip:
			err = split(fleet, contract.Down, stripRows, fl.pane)
		case lay.Fleet == contract.Bottom:
			err = split(fleet, contract.Down, float64(cmp.Or(ui.FleetHeight, contract.FleetHeight)), fl.pane)
		default:
			err = split(fleet, lay.Fleet, float64(form.Fleet), fl.pane)
		}
	}
	if err == nil && wantActions && !ac.open {
		side := map[bool]string{true: contract.Up, false: contract.Down}[lay.Actions == contract.Top]
		// A pane opened anew has its whole share, whatever the last one was
		// left at, or where Fit says so is its strip until an item holds work up.
		if ui.Folded = form.Actions == contract.FormStrip; ui.Folded {
			err = split(actions, side, stripRows, ac.pane)
		} else {
			err = split(actions, side, talkShare, ac.pane)
		}
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
