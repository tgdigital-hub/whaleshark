package panes

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

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

// fit brings an open fleet to a width of n cells, or with tall a pane of
// ours above or under the conversation to a height of n rows, asked from the
// pane beside it: the line between the two moves by a share of both
// together. above says a pane that is fitted in its height lies at the top.
func fit(t contract.Terminals, me, pane string, n int, tall, above bool) error {
	if pane == "" || me == "" || me == pane || n <= 0 {
		return nil
	}
	w, h, err := t.Size(pane)
	mine, mineH, _ := t.Size(me)
	more, less := contract.Left, contract.Right
	if tall {
		w, mine, more, less = h, mineH, contract.Up, contract.Down
	}
	if above {
		more, less = less, more
	}
	if err != nil || w == n || w+mine == 0 {
		return err
	}
	return t.Resize(pane, map[bool]string{true: more, false: less}[n > w], min(math.Abs(float64(n-w))/float64(w+mine), most))
}

// beside is the conversation beside a pane of ours: the pane of its tab that
// is not ours and was opened first. It is found in the picture, and no key
// moves for it.
func beside(snap *contract.Snapshot, ours string) (talks string) {
	tab := ""
	for _, q := range snap.Panes {
		if q.ID == ours {
			tab = q.Tab
		}
	}
	for _, q := range snap.Panes {
		if q.Tab == tab && tab != "" && !q.Ours && (talks == "" || len(q.ID) < len(talks) || len(q.ID) == len(talks) && q.ID < talks) {
			talks = q.ID
		}
	}
	return talks
}

// rootOf is the folder of the workspace a pane is in: empty for the tabs of
// no project, and for a pane the picture does not have.
func rootOf(snap *contract.Snapshot, pane string) string {
	for _, q := range cmp.Or(snap, &contract.Snapshot{}).Panes {
		for _, s := range snap.Spaces {
			if q.ID == pane && s.ID == q.Workspace {
				return s.Root
			}
		}
	}
	return ""
}

// pairOf is the two panes of ours of the workspace of a folder. A keeper of
// before phase 7 keeps no workspaces: the one pair of an older file is then
// the pair of every tab.
func pairOf(ui contract.UIFile, snap *contract.Snapshot, root string) contract.Pair {
	if snap != nil && len(snap.Spaces) == 0 && ui.Panes == nil {
		return contract.Pair{Fleet: ui.FleetPane, Actions: ui.ActionsPane}
	}
	return ui.PairOf(root, snap)
}

// pair is the two panes of ours in the workspace this pane is in.
func (p *pane) pair() contract.Pair { return pairOf(p.ui, p.picture, rootOf(p.picture, p.me)) }

// rested is a pane beside the conversation whose size has rested for half a
// second. A person who dragged its edge moved the line between it and the
// conversation, so the two together measure what they did: then the fleet's
// width, or its height at the bottom, or the action pane's height is
// written, as its keys write it. A window that changed changes what the two
// measure together, a size this pane asked for itself is no drag, and a
// pane that only changed along its other side was not dragged: then
// nothing is written. An action pane put back to the quarter it comes as
// is written as 0. With store false the two are only measured. A strip
// has nothing to remember, nor has the fleet in a window so narrow that Fit
// decides its width.
func (p *pane) rested(store bool) {
	if p.kind != fleet && p.kind != actions || p.picture == nil || p.me == "" || p.bg == nil {
		return
	}
	tab, talks, dock := "", "", ""
	for _, q := range p.picture.Panes {
		if q.ID == p.me {
			tab = q.Tab
		}
	}
	for _, q := range p.picture.Panes {
		if q.Tab == tab && !q.Ours && !q.Dock {
			talks = q.ID
		} else if q.Dock {
			dock = q.ID
		}
	}
	key, mine, had, wide := "fleet.width", p.t.W, p.ui.FleetWidth, true
	switch {
	case p.kind == actions:
		key, mine, had, wide = "actions.height", p.t.H, p.ui.ActionsHeight, false
	case p.cfg.Panes.Fleet == contract.Bottom:
		key, mine, had, wide = "fleet.height", p.t.H, p.ui.FleetHeight, false
	}
	store = store && p.t.H > stripRows && !p.folded && time.Now().After(p.resized)
	p.helper(func() {
		tw, th, err := p.k.Terms.Size(talks)
		dw, _, _ := p.k.Terms.Size(dock)
		p.tell(news{do: func() {
			// Beside the conversation the left section counts too: its edge
			// dragged gives and takes from the conversation alone, and tells
			// this pane nothing.
			sum := mine + map[bool]int{true: tw + dw, false: th}[wide]
			same, moved, now := sum == p.both, mine != p.mine, mine
			if p.kind == actions && mine == sum-int(talkShare*float64(sum)+0.5) {
				now = 0 // the quarter it comes as, at any size of the window
			}
			if p.both, p.mine = sum, mine; err == nil && store && same && moved && now != had && (!wide || sum+2 >= contract.SideNamesFrom) {
				p.child(p.cmd("set", key, strconv.Itoa(now)), "", p.quiet)
			}
		}})
	})
}

// talk finds the conversation's pane from ours: one of ours is asked for the
// pane on its far side, which takes the keys, and the conversation is where
// they land.
func talk(t contract.Terminals, ui contract.Pair) (string, error) {
	for _, way := range [][2]string{{ui.Actions, contract.Up}, {ui.Actions, contract.Down}, {ui.Fleet, contract.Left}} {
		if way[0] == "" {
			continue
		}
		if got, err := t.PaneFocus(way[0], way[1]); err != nil || got != "" && got != ui.Actions && got != ui.Fleet {
			return got, err
		}
	}
	return "", nil
}

// hand gives the keys to the conversation, or through it to one of ours.
func hand(t contract.Terminals, ui contract.Pair, to string) error {
	from, err := talk(t, ui)
	want, ways := ui.Fleet, []string{contract.Right}
	if to == actions {
		want, ways = ui.Actions, []string{contract.Down, contract.Up}
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

// refit lays the two panes of every workspace out again for a window that
// took another width, in the tab they stand in, and opens nothing where
// neither is open: the file names them, and a pane it names that is gone
// stands for nothing.
func refit(k *contract.Kit) error {
	_, ui, _, err := screen(k)
	snap, serr := k.Terms.Snapshot(context.Background())
	if err != nil || serr != nil {
		return err
	}
	for _, s := range append(slices.Clone(snap.Spaces), contract.Space{})[:max(len(snap.Spaces), 1)] {
		pair := pairOf(ui, snap, s.Root)
		for _, ours := range []string{pair.Fleet, pair.Actions} {
			if ours == "" {
				continue
			}
			_, err = place(&contract.Call{Kit: k, Caller: contract.Caller{Pane: ours}, Out: io.Discard}, nil)
			if r := new(contract.Refusal); !errors.As(err, &r) || r.Code != "no_panes" {
				break
			}
			err = nil
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// place opens and closes our two panes beside the pane the command was run
// in: the pair of that pane's workspace, and no other workspace's. One that
// is open in another tab of it is closed there first. want says, for
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
	if _, there := panes[me]; !there || me == "" {
		return nil, cannot("The panes open beside the conversation: run this in the lead agent's tab.")
	}
	// An older file's one pair becomes the pair of the workspace it is in.
	root, all := rootOf(snap, me), map[string]contract.Pair{}
	for _, s := range append(slices.Clone(snap.Spaces), contract.Space{Root: root}) {
		if pr := pairOf(ui, snap, s.Root); pr != (contract.Pair{}) {
			all[s.Root] = pr
		}
	}
	pair := all[root]
	was := fmt.Sprint(ui)
	ui.Panes, ui.FleetPane, ui.ActionsPane = all, "", ""
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
	}{{fleet, &pair.Fleet, false}, {actions, &pair.Actions, false}}
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
		if me, err = talk(k.Terms, pair); err != nil || me == "" {
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
		// its height in rows, and as a strip its one line and its age. It is
		// opened at what it comes as and then brought to what is remembered,
		// so that its line is put back to how it comes.
		lay.FleetWide = 0
		switch comes := contract.Fit(cols, lay); {
		case form.FleetForm == contract.FormStrip:
			err = split(fleet, contract.Down, stripRows, fl.pane)
		case lay.Fleet == contract.Bottom:
			if err = split(fleet, contract.Down, contract.FleetHeight, fl.pane); err == nil {
				err = fit(k.Terms, me, *fl.pane, ui.FleetHeight, true, false)
			}
		default:
			if err = split(fleet, lay.Fleet, float64(comes.Fleet), fl.pane); err == nil && comes.Fleet != form.Fleet {
				err = fit(k.Terms, me, *fl.pane, form.Fleet, false, lay.Fleet == contract.Left)
			}
		}
	}
	if err == nil && wantActions && !ac.open {
		side := map[bool]string{true: contract.Up, false: contract.Down}[lay.Actions == contract.Top]
		// A pane opened anew has its whole share, whatever the last one was
		// left at, or where Fit says so is its strip until an item holds work up.
		if ui.Folded = form.Actions == contract.FormStrip; ui.Folded {
			err = split(actions, side, stripRows, ac.pane)
		} else if err = split(actions, side, talkShare, ac.pane); err == nil {
			err = fit(k.Terms, me, *ac.pane, ui.ActionsHeight, true, lay.Actions == contract.Top)
		}
	}
	// What was opened is written down even when a later step failed.
	if all[root] = pair; pair == (contract.Pair{}) {
		delete(all, root)
	}
	if fmt.Sprint(ui) != was {
		ui.Version = contract.FileVersion
		if werr := contract.WriteVersioned(k.Platform, file, contract.FileVersion, ui); err == nil {
			err = werr
		}
	}
	if err == nil && asker != "" {
		err = hand(k.Terms, pair, asker)
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
	return Opened(pair), nil
}
