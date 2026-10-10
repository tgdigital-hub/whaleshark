// Package pick is scrolling back, selecting in one pane and copying.
//
// The keeper holds one View for each pane: a wheel or left-button report
// over the pane goes to Mouse, a key for it to Key, and Paint draws it.
// Selecting from one cell to another, a word with two clicks and a line
// with three, and a cursor moved by keys are ideas common to the field; the
// clipboard sequence is XTerm Control Sequences' "Manipulate Selection Data".
package pick

import (
	"fmt"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/layout"
	"github.com/tgdigital-hub/whaleshark/internal/screen"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// Plug binds this package's part into the kit: it has no command and stands
// behind no interface. The keeper makes a View for each pane.
func Plug(*contract.Kit) {}

const (
	// step is how many lines one notch of the wheel moves.
	step = 3
	// again is how soon a second click must follow the first to count with it.
	again = 400 * time.Millisecond
)

// point is a cell of a pane's text: line counts from the oldest line of
// the scroll-back, the rows of the screen following the last of them.
type point struct{ x, line int }

// View is where the person looks in one pane and what is selected there;
// the zero value is the live picture with nothing selected.
type View struct {
	Back  int   // how many lines the pane is scrolled back
	seen  int   // the scroll-back's lines at the last Paint
	a, b  point // the selection's two ends, in either order
	on    bool  // something is selected
	whole bool  // by whole lines
	drag  bool  // the button is down
	keys  bool  // selecting by keys; b is the cursor
	click point // where the button went down last, when, and how often
	last  time.Time
	count int
}

// Did is what a report came to.
type Did struct {
	Used bool   // it was the engine's own, and is not for the pane's program
	Type string // to be typed into the pane's program
	Copy string // to be put on the person's clipboard
}

// Live is the live picture with nothing selected: for a paste, or a click elsewhere.
func (v *View) Live() { *v = View{seen: v.seen} }

// row is the cells of a line: the scroll-back's without its last blanks, or the screen's.
func row(s *screen.Screen, line int) []contract.Cell {
	p, y := s.Picture(), line-s.Lines()
	if y < 0 || y >= p.H {
		return s.Line(line)
	}
	return p.Cells[y*p.W : (y+1)*p.W]
}

// span is the selected cells of a line w cells wide: x0 to x1, or x0 past x1 for none.
func (v *View) span(line, w int) (x0, x1 int) {
	a, b := v.a, v.b
	if b.line < a.line || b.line == a.line && b.x < a.x {
		a, b = b, a
	}
	if !v.on || line < a.line || line > b.line {
		return 0, -1
	}
	x0, x1 = 0, w-1
	if line == a.line && !v.whole {
		x0 = a.x
	}
	if line == b.line && !v.whole {
		x1 = min(b.x, x1)
	}
	return x0, x1
}

// Text is the selected text, line by line, with no blanks at a line's end.
func (v *View) Text(s *screen.Screen) string {
	var out []string
	for line := min(v.a.line, v.b.line); v.on && line <= max(v.a.line, v.b.line); line++ {
		var t strings.Builder
		cells := row(s, line)
		for x0, x1 := v.span(line, len(cells)); x0 <= x1; x0++ {
			t.WriteString(cells[x0].Text)
		}
		out = append(out, strings.TrimRight(t.String(), " "))
	}
	return strings.Join(out, "\n")
}

// Paint draws the pane into its place in a window's grid and nowhere else:
// its picture, scrolled back as far as the person went, the selection in
// the accent, and in the top corner how far back it is. It returns where
// the cursor is inside the place and whether it shows. A view scrolled back
// stays on its lines while the program prints, as long as the scroll-back grows.
func (v *View) Paint(g *term.Grid, at layout.Rect, s *screen.Screen, accent term.Style) (cx, cy int, shown bool) {
	if n := s.Lines(); v.Back > 0 {
		v.Back += n - v.seen
	}
	v.seen = s.Lines()
	if v.Back = min(max(v.Back, 0), v.seen); s.Modes().Second {
		v.Back = 0
	}
	if at.X < 0 || at.Y < 0 {
		return 0, 0, false
	}
	pic := s.View(v.Back)
	g.Paint(at.X, at.Y, at.W, at.H, pic)
	w, h, top := min(at.W, pic.W, g.W-at.X), min(at.H, pic.H, g.H-at.Y), v.seen-v.Back
	accent.Reverse = true
	for y := range max(h, 0) {
		for x0, x1 := v.span(top+y, w); x0 <= x1; x0++ {
			if t := g.At(at.X+x0, at.Y+y).Text; t != "" {
				g.Put(at.X+x0, at.Y+y, w-x0, t, accent)
			}
		}
	}
	words := map[bool]string{true: " select "}[v.keys]
	if v.Back > 0 {
		words += fmt.Sprintf(" %d back ", v.Back)
	}
	if x := max(w-len(words), 0); h > 0 {
		g.Put(at.X+x, at.Y, w-x, words, accent)
	}
	if cx, cy, shown = pic.CurX, pic.CurY, pic.CurShown; v.keys {
		cx, cy, shown = v.b.x, v.b.line-top, true
	}
	return cx, cy, shown && cx >= 0 && cx < w && cy >= 0 && cy < h
}

// Mouse takes a report of the wheel or the left button for the pane at a
// place of the window; a drag goes to the pane it began in, wherever the
// pointer is by then. A program that asked for the mouse gets all of it.
// Otherwise the wheel moves the view into the scroll-back, or on the second
// screen is arrow keys where the program takes them (mode 1007), and the
// button selects: a drag from cell to cell, two clicks a word, three a line.
// A pointer outside the pane counts as the nearest cell inside, and above or
// below it scrolls a line a report. Letting go copies what is selected.
func (v *View) Mouse(ev term.Event, at layout.Rect, s *screen.Screen, now time.Time) (d Did) {
	m, pic := s.Modes(), s.Picture()
	w, h, lines := min(at.W, pic.W), min(at.H, pic.H), s.Lines()
	if m.Mouse != 0 || w < 1 || h < 1 {
		return d
	}
	at.W, at.H, d.Used = w, h, true
	here := func() point {
		p := point{min(max(ev.X-at.X, 0), w-1), lines - v.Back + min(max(ev.Y-at.Y, 0), h-1)}
		// The right half of a wide character is that character.
		if c := row(s, p.line); p.x > 0 && p.x < len(c) && c[p.x].Text == "" {
			p.x--
		}
		return p
	}
	switch dir := map[term.Button]int{term.WheelUp: 1, term.WheelDown: -1}[ev.Button]; {
	case dir != 0 && m.Second:
		if lead := map[bool]string{false: "\x1b[", true: "\x1bO"}[m.CursorKeys]; m.WheelKeys {
			d.Type = strings.Repeat(lead+string("B A"[dir+1]), step)
		}
	case dir != 0:
		v.Back = min(max(v.Back+dir*step, 0), lines)
	case ev.Button != term.Left:
		d.Used = false
	case ev.Action == term.Press:
		p := here()
		if v.count++; p != v.click || now.Sub(v.last) > again {
			v.count = 1
		}
		*v = View{Back: v.Back, seen: v.seen, a: p, b: p, drag: true, click: p, count: v.count, last: now,
			on: v.count%3 != 1, whole: v.count%3 == 0}
		if v.count%3 == 2 {
			v.a.x, v.b.x = word(row(s, p.line), p.x)
		}
	case !v.drag:
	case ev.Action == term.Move:
		if ev.Y < at.Y {
			v.Back = min(v.Back+1, lines)
		} else if ev.Y >= at.Y+at.H {
			v.Back = max(v.Back-1, 0)
		}
		v.b = here()
		v.on = v.on || v.b != v.a
	default:
		v.drag = false
		d.Copy = v.Text(s)
	}
	return d
}

// word is the run of cells around x that are all blank or all not.
func word(c []contract.Cell, x int) (x0, x1 int) {
	same := func(i int) bool {
		return i >= 0 && i < len(c) && x < len(c) && (c[i].Text == " ") == (c[x].Text == " ")
	}
	for x0, x1 = x, x; same(x0 - 1); x0-- {
	}
	for ; same(x1 + 1); x1++ {
	}
	return x0, x1
}

// Select starts selecting by keys, for a pane whose program has the mouse and
// for a person without one: a cursor of ours stands where the program's was.
func (v *View) Select(s *screen.Screen) {
	p := s.Picture()
	*v = View{Back: v.Back, seen: v.seen, keys: true, b: point{p.CurX, s.Lines() - v.Back + p.CurY}}
}

// moves are the keys that move the cursor while selecting by keys, each by
// so many cells and lines; a million is past either end of any line.
var moves = map[string]point{"left": {-1, 0}, "h": {-1, 0}, "right": {1, 0}, "l": {1, 0}, "up": {0, -1}, "k": {0, -1},
	"down": {0, 1}, "j": {0, 1}, "home": {-1e6, 0}, "0": {-1e6, 0}, "end": {1e6, 0}, "$": {1e6, 0}}

// Key takes a key meant for the pane. While selecting by keys every key is
// the engine's: moves and a page up or down move the cursor, v or space
// starts the selection there or drops it, enter or y copies and leaves, esc
// or q leaves. Otherwise the copy key copies what is selected: Cmd+C and
// Ctrl+Shift+C where the person's terminal passes them on, and Ctrl+C where
// ctrlC says the Windows rule holds, which also drops the selection. With
// nothing selected all three are the program's, as is any other key, which
// brings back the live picture with nothing selected.
func (v *View) Key(ev term.Event, s *screen.Screen, ctrlC bool) (d Did) {
	p, lines := s.Picture(), s.Lines()
	switch key := ev.Key; {
	case !v.keys && v.on && (key == "super+c" || key == "ctrl+shift+c"):
		return Did{Used: true, Copy: v.Text(s)}
	case !v.keys && v.on && key == "ctrl+c" && ctrlC, v.keys && (key == "enter" || key == "y"):
		d = Did{Used: true, Copy: v.Text(s)}
		v.Live()
	case !v.keys, key == "esc", key == "q":
		d.Used = v.keys
		v.Live()
	case key == "v", key == "space":
		v.a, v.on = v.b, !v.on
	case key == "pageup":
		v.b.line -= p.H
	case key == "pagedown":
		v.b.line += p.H
	default:
		v.b = point{v.b.x + moves[key].x, v.b.line + moves[key].line}
	}
	if !v.keys {
		return d
	}
	first := map[bool]int{true: lines}[s.Modes().Second] // nothing is above the second screen
	v.b = point{min(max(v.b.x, 0), p.W-1), min(max(v.b.line, first), lines+p.H-1)}
	v.Back = min(max(v.Back, lines-v.b.line), lines-v.b.line+p.H-1) // the view follows the cursor
	return Did{Used: true}
}
