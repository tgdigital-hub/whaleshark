package layout

import (
	"slices"
	"strconv"

	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// Look is what the tab row and the dividing lines are drawn with: four
// colours of the scheme, and Plain where the terminal cannot show the line
// characters.
type Look struct {
	Text, Dim, Frame, Accent term.Style
	Plain                    bool
}

// What a cell of the window is part of.
const (
	Nothing = iota // outside the window, or the line under the tab row
	OnRow          // the tab row, beside the tabs
	OnTab          // a tab's name
	OnMore         // the count of tabs that do not fit; Step says which way they are
	OnLine         // a dividing line, which Drag moves
	OnPane         // a pane; X and Y are the cell inside it
)

// Hit is what At found under a cell.
type Hit struct {
	Kind int
	Tab  string
	Pane string
	X, Y int
	Step int
	line *Node
}

// labelMost is the most cells of the tab row one name takes.
const labelMost = 24

// span is one piece of the tab row: a tab's name, or with a step a count of
// the tabs that do not fit on that side.
type span struct {
	x, w, tab, step int
	text            string
}

// row lays the tab row out from the first tab it shows: the names with three
// cells between them, and before or after them how many more there are.
func (l *Layout) row() []span {
	var out []span
	count := func(x, step, n int) span {
		s := "+" + strconv.Itoa(n)
		return span{x, len(s), -1, step, s}
	}
	x := 1
	if l.first > 0 {
		out = append(out, count(x, -1, l.first))
		x += out[0].w + 3
	}
	for i := l.first; i < len(l.Tabs); i++ {
		text := l.Tabs[i].Label
		if text == "" {
			text = l.Tabs[i].ID
		}
		w, rest, after := min(term.Width(text), labelMost), len(l.Tabs)-1-i, 0
		if rest > 0 {
			after = 3 + count(0, 1, rest).w
		}
		if x+w+after > l.W && i > l.first {
			return append(out, count(x, 1, rest+1))
		}
		w = max(min(w, l.W-x-after), 0)
		out = append(out, span{x, w, i, 0, text})
		x += w + 3
	}
	return out
}

// reveal moves the tab row until the tab that shows is on it.
func (l *Layout) reveal() {
	l.Active = max(min(l.Active, len(l.Tabs)-1), 0)
	l.first = min(l.first, l.Active)
	for l.first < l.Active && !slices.ContainsFunc(l.row(), func(s span) bool { return s.tab == l.Active }) {
		l.first++
	}
}

// Wheel moves the tab row by n tabs, later ones for n above 0, and stops
// where nothing more is hidden that way.
func (l *Layout) Wheel(n int) {
	for ; n < 0 && l.first > 0; n++ {
		l.first--
	}
	for ; n > 0; n-- {
		if r := l.row(); len(r) == 0 || r[len(r)-1].step != 1 {
			return
		}
		l.first++
	}
}

// shown is the tab that shows, nil when there is none.
func (l *Layout) shown() *Tab {
	if l.Active < 0 || l.Active >= len(l.Tabs) {
		return nil
	}
	return l.Tabs[l.Active]
}

// At says what is under a cell of the window.
func (l *Layout) At(x, y int) (h Hit) {
	t := l.shown()
	if l.Over != "" && l.OverAt.has(x, y) {
		return Hit{Kind: OnPane, Pane: l.Over, X: x - l.OverAt.X, Y: y - l.OverAt.Y}
	}
	if x < 0 || y < 0 || x >= l.W || y >= l.H || y == 1 || t == nil {
		return h
	}
	if y == 0 {
		h.Kind = OnRow
		for _, s := range l.row() {
			if x >= s.x && x < s.x+s.w && s.step != 0 {
				return Hit{Kind: OnMore, Step: s.step}
			} else if x >= s.x && x < s.x+s.w {
				return Hit{Kind: OnTab, Tab: l.Tabs[s.tab].ID}
			}
		}
		return h
	}
	if t.Zoom != "" {
		return Hit{Kind: OnPane, Pane: t.Zoom, X: x, Y: y - 2}
	}
	walk(t.Root, l.area(), func(n *Node, r Rect) {
		if n.A == nil && r.has(x, y) {
			h = Hit{Kind: OnPane, Pane: n.Pane, X: x - r.X, Y: y - r.Y}
		} else if n.A != nil {
			if _, _, line := n.cut(r); line.has(x, y) {
				h = Hit{Kind: OnLine, line: n}
			}
		}
	})
	return h
}

// The ends a line cell has: its own two, and one more toward each line
// that touches it. The characters are Unicode's box drawings (U+2500 on),
// and the three of plain text where a terminal cannot show those.
const (
	up = 1 << iota
	down
	left
	right
)

var (
	lines = [16]string{up | down: "│", left | right: "─", up | down | left: "┤", up | down | right: "├",
		up | left | right: "┴", down | left | right: "┬", up | down | left | right: "┼"}
	plain = [16]string{up | down: "|", left | right: "-", up | down | left: "+", up | down | right: "+",
		up | left | right: "+", down | left | right: "+", up | down | left | right: "+"}
)

// Draw puts the tab row, the line under it and the dividing lines of the
// tab that shows into a grid; the panes' own pictures go into their places.
// A line is one cell, shared by the panes on both sides, and those round the
// pane that has the keys are in the accent.
func (l *Layout) Draw(g *term.Grid, k Look) {
	glyphs := lines
	if k.Plain {
		glyphs = plain
	}
	for i, s := range l.row() {
		st := k.Text
		if s.step != 0 {
			st = k.Dim
		} else if s.tab == l.Active {
			st = k.Accent
			st.Bold = true
		}
		if i > 0 {
			g.Put(s.x-2, 0, 1, glyphs[up|down], k.Frame)
		}
		g.Put(s.x, 0, s.w, s.text, st)
	}
	ends := make([]byte, l.W*l.H)
	mark := func(r Rect, e byte) {
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				ends[y*l.W+x] |= e
			}
		}
	}
	mark(Rect{0, 1, l.W, min(l.H-1, 1)}, left|right)
	var keys Rect
	if t := l.shown(); t != nil {
		for _, p := range l.Places(t) {
			if p.Pane == t.Focus {
				keys = Rect{p.X - 1, p.Y - 1, p.W + 2, p.H + 2}
			}
		}
		walk(t.Root, l.area(), func(n *Node, r Rect) {
			if n.A == nil || t.Zoom != "" {
				return
			}
			_, _, line := n.cut(r)
			if n.Down {
				mark(line, left|right)
			} else {
				mark(line, up|down)
			}
		})
	}
	at := func(x, y int) bool { return x >= 0 && y >= 0 && x < l.W && y < l.H && ends[y*l.W+x] != 0 }
	for i, e := range ends {
		if e == 0 {
			continue
		}
		x, y := i%l.W, i/l.W
		for j, d := range [4][2]int{{0, -1}, {0, 1}, {-1, 0}, {1, 0}} {
			if at(x+d[0], y+d[1]) {
				e |= 1 << j
			}
		}
		st := k.Frame
		if keys.has(x, y) {
			st = k.Accent
		}
		g.Put(x, y, 1, glyphs[e], st)
	}
}
