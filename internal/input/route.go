package input

import (
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/layout"
	"github.com/tgdigital-hub/whaleshark/internal/screen"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// Kind says what an Act asks of the keeper.
type Kind uint8

const (
	Type     Kind = iota + 1 // write Text to Pane's terminal
	Call                     // do Call, as if it had come over the socket
	Row                      // move the tab row by Step tabs
	Drag                     // move the dividing line Hit to the cell X, Y
	Zoom                     // give Pane its whole tab, or put it back
	Redraw                   // make Pane's program draw again
	Run                      // start Argv as a child, with Pane's id in its variables
	Popup                    // open Argv as an overlay
	Scroll                   // move Pane's view Step lines, back for a negative
	Select                   // the mouse selects in Pane: Action at the window's cell X, Y
	Mark                     // start selecting in Pane with the keys
	Copy                     // put the selection on the clipboard
	Allow                    // let Pane's program copy what it asked to
	Settings                 // open the settings screen over the tab
	Width                    // a drag of the dock's edge ended: its width is written
	Menu                     // the menu of the tab or the group Hit is asked for, at the window's cell X, Y
	Reset                    // put the dividing line Hit back to how it came
	Lit                      // the line Hit is the lit one, Step says how; a Hit of no kind is no line
	Put                      // put the line Hit at the cell X, Y: the press after a click on it
	Away                     // a press beside the pane drawn over the tab, which reached nothing else
	tabBy                    // show the tab Step places on
	tabAt                    // show tab number Step
)

// Act is one thing the keeper does for an event, in the order given.
type Act struct {
	Kind   Kind
	Pane   string
	Text   string
	Call   contract.WireCall
	Argv   []string
	Hit    layout.Hit
	X, Y   int
	Step   int
	Action term.Action
}

// Twice is the most that lies between the two presses of a double-click.
const Twice = 500 * time.Millisecond

// How a line is lit, a Lit's Step.
const (
	Under = iota // the pointer came onto it with no button down
	Held         // the button went down on it
	Waits        // it was clicked and not dragged: the next press puts it
)

// The wheel moves three lines a notch; a resize by key a twentieth of the split.
const (
	notch = 3
	nudge = 0.05
)

// Keys is the person's command key and what the key after it does: our own
// table, with the shortcuts of the settings over it.
type Keys struct {
	command string
	after   map[string]Act
	windows bool
}

func call(op, dir string, ratio float64) Act {
	return Act{Kind: Call, Call: contract.WireCall{Op: op, Dir: dir, Ratio: ratio}}
}

// New reads the keys from the settings. system is the system the person
// sits at, as the window said it; it decides, where keys.style is auto,
// whether Ctrl+C with a selection copies, which is Windows' rule alone.
func New(s contract.Settings, system string) *Keys {
	k := &Keys{command: canon(s.Keys.Command), windows: s.Keys.Style == "windows" || s.Keys.Style == "auto" && system == "windows"}
	k.after = map[string]Act{
		"t": call(contract.OpTabCreate, "", 0), "w": call(contract.OpPaneClose, "", 0),
		"r": call(contract.OpSplit, contract.Right, 0.5), "d": call(contract.OpSplit, contract.Down, 0.5),
		"n": {Kind: tabBy, Step: 1}, "p": {Kind: tabBy, Step: -1},
		"z": {Kind: Zoom}, "l": {Kind: Redraw}, "v": {Kind: Mark}, "y": {Kind: Allow},
		"b": call(contract.OpDock, contract.DockFold, 0), "g": call(contract.OpDock, contract.DockKeys, 0), "s": {Kind: Settings},
	}
	for _, dir := range []string{contract.Left, contract.Right, contract.Up, contract.Down} {
		k.after[dir] = call(contract.OpPaneFocus, dir, 0)
		k.after["shift+"+dir] = call(contract.OpResize, dir, nudge)
	}
	for n := 1; n <= 9; n++ {
		k.after[strconv.Itoa(n)] = Act{Kind: tabAt, Step: n}
	}
	for _, e := range s.Shortcuts {
		a := Act{Kind: Run, Argv: e.Argv}
		if e.Type == "popup" {
			a.Kind = Popup
		}
		k.after[canon(e.Key)] = a
	}
	return k
}

// View is what routing needs to see, as it is under the keeper's lock: the
// layout, the pane of an overlay that is open (which then takes every key
// and click), whether a selection of ours shows, and what a pane's program
// asked for.
type View struct {
	Lay      *layout.Layout
	Over     string
	Selected bool
	Modes    func(pane string) screen.Modes
}

// In is one window's keys and mouse on their way in. Armed is set between
// the command key and the key after it. A changed settings file is a new Keys.
type In struct {
	Keys  *Keys
	Armed bool
	to    Kind       // whose the held button is: Drag, Select, or Type for a program
	at    time.Time  // when a line was last pressed, until it moves
	held  layout.Hit // where it went down, or was last seen inside its pane
	put   layout.Hit // the line a click left waiting for the press that puts it
	lit   layout.Hit // the line the keeper was last told is lit, and how
	how   int
}

func typed(pane, text string) []Act {
	if pane == "" || text == "" {
		return nil
	}
	return []Act{{Kind: Type, Pane: pane, Text: text}}
}

// Take routes one event of a window's terminal: to the engine when it is a
// shortcut or on the engine's own parts, else to the program it belongs to,
// written in the form that program asked for.
func (in *In) Take(ev term.Event, v View) []Act {
	focus := v.Over
	if n := v.Lay.Active; focus == "" && n >= 0 && n < len(v.Lay.Tabs) {
		focus = v.Lay.Tabs[n].Focus
	}
	// What is typed goes to the dock while it has the keys; a shortcut is
	// still about the pane that had them.
	keys := focus
	if v.Over == "" && v.Lay.Dock.Keys {
		keys = layout.DockPane
	}
	modes := func(pane string) (m screen.Modes) {
		if pane != "" {
			m = v.Modes(pane)
		}
		return m
	}
	switch ev.Kind {
	case term.KeyPress:
		if in.put.Kind != 0 && ev.Key == "esc" {
			in.put = layout.Hit{} // the line waits no longer, and the key is used up
			return in.light(layout.Hit{}, Under)
		}
		if a, mine := in.shortcut(canon(ev.Key), v, focus); mine {
			return a
		}
		return typed(keys, Key(ev, modes(keys)))
	case term.Paste:
		return typed(keys, Paste(ev.Text, modes(keys)))
	case term.Focus:
		return typed(keys, Focus(ev.Focused, modes(keys)))
	case term.Mouse:
		return in.mouse(ev, v, keys, modes)
	}
	return nil
}

// shortcut says what a key does when it is the engine's and not a program's.
func (in *In) shortcut(key string, v View, focus string) ([]Act, bool) {
	switch {
	case v.Over != "":
		return nil, false
	case !in.Armed && key == in.Keys.command:
		in.Armed = true
		return nil, true
	case !in.Armed:
		// Cmd+C and Ctrl+Shift+C copy and never interrupt; Ctrl+C copies
		// by Windows' rule only. With nothing selected each is the program's.
		if v.Selected && (key == "super+c" || key == "ctrl+shift+c" || in.Keys.windows && key == "ctrl+c") {
			return []Act{{Kind: Copy, Pane: focus}}, true
		}
		return nil, false
	}
	in.Armed = false
	a, ok := in.Keys.after[key]
	if key == in.Keys.command {
		// The command key twice is the key itself, for the program.
		return nil, false
	}
	if a.Pane = focus; a.Call.Op != contract.OpTabCreate {
		a.Call.Pane = focus
	}
	if a.Call.Dir == contract.DockKeys && v.Lay.Dock.Keys {
		a.Call.Dir = contract.DockBack // pressed again, the keys go back
	}
	switch tabs := v.Lay.Tabs; {
	case !ok, len(tabs) == 0 && a.Call.Op != contract.OpTabCreate && a.Kind != Run && a.Kind != Popup:
		return nil, true
	case a.Kind == tabBy:
		a.Step = (v.Lay.Active+a.Step+len(tabs))%len(tabs) + 1
		fallthrough
	case a.Kind == tabAt:
		if a.Step > len(tabs) {
			return nil, true
		}
		a = Act{Kind: Call, Call: contract.WireCall{Op: contract.OpTabFocus, Tab: tabs[a.Step-1].ID}}
	}
	return []Act{a}, true
}

// light tells the keeper which line is lit and how, when that changed.
func (in *In) light(h layout.Hit, how int) []Act {
	if h.Kind != layout.OnLine {
		h, how = layout.Hit{}, Under
	}
	if h == in.lit && how == in.how {
		return nil
	}
	in.lit, in.how = h, how
	return []Act{{Kind: Lit, Hit: h, Step: how}}
}

// mouse routes a mouse report to what is under it, and then says which
// line is lit: the one that waits, else the one held, else the one the
// pointer came onto. Only a movement with no button down lights a line
// under the pointer, so a terminal that reports none leaves none lit.
func (in *In) mouse(ev term.Event, v View, focus string, modes func(string) screen.Modes) []Act {
	h := v.Lay.At(ev.X, ev.Y)
	acts := in.point(h, ev, v, focus, modes)
	line, how := layout.Hit{}, Under
	switch {
	case in.put.Kind != 0:
		line, how = in.put, Waits
	case in.to == Drag:
		line, how = in.held, Held
	case ev.Button == term.NoButton && v.Over == "":
		line = h
	}
	return append(acts, in.light(line, how)...)
}

// point routes a mouse report at h: the tab row and a dividing line are
// the engine's; a pane's program gets what it asked for; and in a pane
// whose program did not ask, the wheel scrolls back and a drag selects.
func (in *In) point(h layout.Hit, ev term.Event, v View, focus string, modes func(string) screen.Modes) []Act {
	on := h.Kind == layout.OnPane && (v.Over == "" || h.Pane == v.Over)
	if ev.Action != term.Press {
		to, was := in.to, in.held
		if ev.Button == term.NoButton || ev.Action == term.Release {
			in.to = 0
		}
		switch {
		case ev.Button == term.NoButton && on:
			return typed(h.Pane, Mouse(ev, modes(h.Pane), h.X, h.Y))
		case to == Drag && ev.Action == term.Move:
			in.at = time.Time{}
			return []Act{{Kind: Drag, Hit: was, X: ev.X, Y: ev.Y}}
		case to == Drag && !in.at.IsZero():
			// Pressed and let go with no movement between: the line waits
			// for the press that says where it should go.
			in.put = was
			return nil
		case to == Drag && was.Pane == layout.DockPane:
			return []Act{{Kind: Width}}
		case to == Select:
			return []Act{{Kind: Select, Pane: was.Pane, X: ev.X, Y: ev.Y, Action: ev.Action}}
		case to != Type:
			return nil
		case on && h.Pane == was.Pane:
			// The program is told where inside its pane; a drag that left
			// the pane ends where it was last seen in it.
			in.held, was = h, h
		case ev.Action == term.Move:
			return nil
		}
		return typed(was.Pane, Mouse(ev, modes(was.Pane), was.X, was.Y))
	}
	wheel := map[term.Button]int{term.WheelUp: -1, term.WheelDown: 1}[ev.Button]
	switch {
	case v.Over != "" && !on && wheel == 0:
		// Beside a pane drawn over the tab a press reaches nothing; the
		// keeper closes a placed one by it.
		return []Act{{Kind: Away}}
	case v.Over != "" && !on:
		return nil
	case wheel != 0 && on:
		switch m := modes(h.Pane); {
		case m.Mouse != 0:
			return typed(h.Pane, Mouse(ev, m, h.X, h.Y))
		case m.Second && m.WheelKeys:
			key := map[int]string{-1: "up", 1: "down"}[wheel]
			return typed(h.Pane, strings.Repeat(Key(term.Event{Key: key}, m), notch))
		case m.Second:
			return nil
		}
		return []Act{{Kind: Scroll, Pane: h.Pane, Step: wheel * notch}}
	case wheel != 0 && ev.Y == 0:
		return []Act{{Kind: Row, Step: wheel}}
	case wheel != 0:
		return nil
	case in.put.Kind != 0 && v.Over == "":
		// The press after a click on a line is used up: on that line again
		// within the span of a double-click it puts the line back, else it
		// puts the line where the press is.
		put := in.put
		if in.put = (layout.Hit{}); h == put && time.Since(in.at) < Twice {
			return []Act{{Kind: Reset, Hit: put}}
		}
		return []Act{{Kind: Put, Hit: put, X: ev.X, Y: ev.Y}}
	case ev.Menu() && (h.Kind == layout.OnTab || h.Kind == layout.OnGroup):
		// The menu of this thing, here: whatever press term says asks for one.
		return []Act{{Kind: Menu, Hit: h, X: ev.X, Y: ev.Y}}
	case !on && (ev.Menu() || ev.Button != term.Left):
		return nil
	case h.Kind == layout.OnTab, h.Kind == layout.OnGroup:
		return []Act{{Kind: Call, Call: contract.WireCall{Op: contract.OpTabFocus, Tab: h.Tab}}}
	case h.Kind == layout.OnMore:
		return []Act{{Kind: Row, Step: h.Step}}
	case h.Kind == layout.OnLine:
		in.to, in.held, in.at = Drag, h, time.Now()
		return nil
	case !on:
		return nil
	}
	// A press on a pane without the keys moves the keys there and is
	// passed on, so one click acts. A press in the dock moves no keys.
	var acts []Act
	if h.Pane != focus && h.Pane != layout.DockPane {
		acts = []Act{{Kind: Call, Call: contract.WireCall{Op: contract.OpPaneFocus, Pane: h.Pane}}}
	}
	in.held = h
	if m := modes(h.Pane); m.Mouse != 0 {
		in.to = Type
		return append(acts, typed(h.Pane, Mouse(ev, m, h.X, h.Y))...)
	}
	if in.to = 0; ev.Button == term.Left {
		in.to = Select
		acts = append(acts, Act{Kind: Select, Pane: h.Pane, X: ev.X, Y: ev.Y, Action: term.Press})
	}
	return acts
}
