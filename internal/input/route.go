package input

import (
	"strconv"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/layout"
	"github.com/tgdigital-hub/whaleshark/internal/screen"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// Kind says what an Act asks of the keeper.
type Kind uint8

const (
	Type   Kind = iota + 1 // write Text to Pane's terminal
	Call                   // do Call, as if it had come over the socket
	Row                    // move the tab row by Step tabs
	Drag                   // move the dividing line Hit to the cell X, Y
	Zoom                   // give Pane its whole tab, or put it back
	Redraw                 // make Pane's program draw again
	Run                    // start Argv as a child, with Pane's id in its variables
	Popup                  // open Argv as an overlay
	Scroll                 // move Pane's view Step lines, back for a negative
	Select                 // the mouse selects in Pane: Action at the window's cell X, Y
	Mark                   // start selecting in Pane with the keys
	Copy                   // put the selection on the clipboard
	tabBy                  // show the tab Step places on
	tabAt                  // show tab number Step
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
		"z": {Kind: Zoom}, "l": {Kind: Redraw}, "v": {Kind: Mark},
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
	held  layout.Hit // where it went down, or was last seen inside its pane
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
	modes := func(pane string) (m screen.Modes) {
		if pane != "" {
			m = v.Modes(pane)
		}
		return m
	}
	switch ev.Kind {
	case term.KeyPress:
		if a, mine := in.shortcut(canon(ev.Key), v, focus); mine {
			return a
		}
		return typed(focus, Key(ev, modes(focus)))
	case term.Paste:
		return typed(focus, Paste(ev.Text, modes(focus)))
	case term.Focus:
		return typed(focus, Focus(ev.Focused, modes(focus)))
	case term.Mouse:
		return in.mouse(ev, v, focus, modes)
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

// mouse routes a mouse report: the tab row and a dividing line are the
// engine's; a pane's program gets what it asked for; and in a pane whose
// program did not ask, the wheel scrolls back and a drag selects.
func (in *In) mouse(ev term.Event, v View, focus string, modes func(string) screen.Modes) []Act {
	h := v.Lay.At(ev.X, ev.Y)
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
			return []Act{{Kind: Drag, Hit: was, X: ev.X, Y: ev.Y}}
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
	case wheel != 0, !on && ev.Button != term.Left:
		return nil
	case h.Kind == layout.OnTab:
		return []Act{{Kind: Call, Call: contract.WireCall{Op: contract.OpTabFocus, Tab: h.Tab}}}
	case h.Kind == layout.OnMore:
		return []Act{{Kind: Row, Step: h.Step}}
	case h.Kind == layout.OnLine:
		in.to, in.held = Drag, h
		return nil
	case !on:
		return nil
	}
	// A press on a pane without the keys moves the keys there and is
	// passed on, so one click acts.
	var acts []Act
	if h.Pane != focus {
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
