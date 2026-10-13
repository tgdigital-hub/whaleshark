package layout

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// Look is what the tab row and the dividing lines are drawn with: five
// colours of the scheme, Need for what waits for the person, and Plain where
// the terminal cannot show the line characters.
type Look struct {
	Text, Dim, Frame, Accent, Need term.Style
	Plain                          bool
}

// What a cell of the window is part of.
const (
	Nothing = iota // outside the window, or the line under the tab row
	OnRow          // the tab row, beside the names
	OnTab          // a tab's name, on either tab line
	OnMore         // the count of names that do not fit; Step says which way they are
	OnLine         // a dividing line, which Drag moves
	OnPane         // a pane; X and Y are the cell inside it
	// OnGroup is a fleet's name on the first of two tab lines; Tab is the
	// tab a click goes to: the one of that fleet that showed last, its
	// leader's the first time.
	OnGroup
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

const (
	// labelMost is the most cells of the tab row one tab's name takes.
	labelMost = 24
	// twoLines is the fewest rows of a window that has a second tab line.
	twoLines = 16
	// Lower multiplies the Step of an OnMore on the second tab line, and
	// Wheel given such a step moves that line.
	Lower = 1 << 10
	// other is the group of the tabs of a workspace that are of no fleet,
	// all but its first: those a person opened by hand.
	other = "\x00"
)

// span is one piece of a tab line: a name, or with a step a count of the
// names that do not fit on that side. A name is a tab's, or on the first of
// two lines a group's, with what waits in it as its mark; tab is the tab a
// click goes to, and on says it shows, or one of the group's does.
type span struct {
	x, w, step int
	text, mark string
	tab        *Tab
	on, group  bool
}

// Head is how many lines the tab row of the workspace in front has.
func (l *Layout) Head() int { return l.lines(l.Front) }

// lines is how many lines the tab row of a workspace has: two while one of
// its tabs is a fleet's, in a window that has the rows for it.
func (l *Layout) lines(space string) int {
	if l.H >= twoLines && slices.ContainsFunc(l.Tabs, func(t *Tab) bool { return t.Space == space && t.Group != "" }) {
		return 2
	}
	return 1
}

// group is the group a tab is of on the tab row: its fleet, none for the
// first tab of its workspace, and other for the rest.
func (l *Layout) group(t *Tab) string {
	first := slices.IndexFunc(l.Tabs, func(o *Tab) bool { return o.Space == t.Space })
	switch {
	case t.Group != "":
		return t.Group
	case first >= 0 && l.Tabs[first] == t:
		return ""
	}
	return other
}

// names is what the tab lines of the workspace in front hold. On one line,
// every tab by its label. On two, the first has the workspace's first tab,
// then each fleet by its name with the count of its tabs but its leader's,
// then the tabs opened by hand as one more group; the second has the tabs of
// the group whose tab shows, a fleet's leader first and called so. While the
// first tab shows, the second line is the group that showed last, and dim
// says so.
func (l *Layout) names() (out [2][]span, dim bool) {
	s, shown := l.Space(l.Front), l.shown()
	if s == nil || shown == nil {
		return out, false
	}
	label := func(t *Tab) string { return cmp.Or(t.Label, t.ID) }
	if l.Head() == 1 {
		for _, t := range l.in(s.ID) {
			out[0] = append(out[0], span{text: label(t), tab: t, on: t == shown})
		}
		return out, false
	}
	at, keys, count, need := l.group(shown), []string{}, map[string]int{}, map[string]int{}
	if dim = at == ""; dim {
		at = s.last
	}
	for _, t := range l.in(s.ID) {
		g := l.group(t)
		if g == "" {
			out[0], keys = append(out[0], span{text: label(t), tab: t, on: t == shown}), append(keys, g)
			continue
		}
		i := slices.Index(keys, g)
		if i < 0 {
			i = len(keys)
			out[0], keys = append(out[0], span{tab: t, on: g == l.group(shown), group: true}), append(keys, g)
		}
		if need[g] += l.Marks[t.ID]; !t.Head {
			count[g]++
		}
		// A click goes to the tab of the group that showed last, else its leader's.
		if was := s.seen[g]; was == t.ID || t.Head && (l.Tab(was) == nil || l.group(l.Tab(was)) != g) {
			out[0][i].tab = t
		}
	}
	if i := slices.Index(keys, other); i >= 0 { // the tabs opened by hand come last
		hand := out[0][i]
		out[0], keys = append(slices.Delete(out[0], i, i+1), hand), append(slices.Delete(keys, i, i+1), other)
	}
	if at == "" || !slices.Contains(keys, at) { // nothing showed yet, or what did is gone
		at = keys[slices.IndexFunc(keys, func(g string) bool { return g != "" })]
	}
	for i, g := range keys {
		if g == "" {
			continue
		}
		name := contract.Label(g)
		if g == other {
			name = "other"
		}
		if out[0][i].text = name + " · " + strconv.Itoa(count[g]); need[g] > 0 {
			out[0][i].mark = "▲" + strconv.Itoa(need[g])
		}
	}
	for _, t := range l.in(s.ID) {
		if n := (span{text: label(t), tab: t, on: t == shown}); l.group(t) != at {
		} else if t.Head {
			n.text = "leader"
			out[1] = slices.Insert(out[1], 0, n)
		} else {
			out[1] = append(out[1], n)
		}
	}
	return out, dim
}

// fit lays names out on one line from the first it shows: three cells
// between them, and before or after them how many more there are.
func fit(names []span, first, x, end int) (out []span) {
	count := func(x, step, n int) span {
		s := "+" + strconv.Itoa(n)
		return span{x: x, w: len(s), step: step, text: s}
	}
	if first > 0 {
		out = append(out, count(x, -1, first))
		x += out[0].w + 3
	}
	for i := first; i < len(names); i++ {
		n := names[i]
		w, rest, after := term.Width(n.text), len(names)-1-i, 0
		if !n.group {
			w = min(w, labelMost)
		}
		if n.mark != "" {
			w += 1 + term.Width(n.mark)
		}
		if rest > 0 {
			after = 3 + count(0, 1, rest).w
		}
		if x+w+after > end && i > first {
			return append(out, count(x, 1, rest+1))
		}
		n.x, n.w = x, max(min(w, end-x-after), 0)
		out = append(out, n)
		x += n.w + 3
	}
	return out
}

// rows lays out the tab lines of the workspace in front, each from the
// first name it shows; the second begins two cells in, after its corner.
func (l *Layout) rows() (out [2][]span, dim bool) {
	names, dim := l.names()
	if s, a := l.Space(l.Front), l.area(); s != nil {
		for i := range l.Head() {
			out[i] = fit(names[i], min(s.first[i], max(len(names[i])-1, 0)), a.X+1+2*i, a.X+a.W)
		}
	}
	return out, dim
}

// reveal moves each tab line until the name that shows is on it.
func (l *Layout) reveal() {
	l.Active = max(min(l.Active, len(l.Tabs)-1), 0)
	s := l.Space(l.Front)
	if s == nil {
		return
	}
	names, _ := l.names()
	for i := range names {
		on := slices.IndexFunc(names[i], func(n span) bool { return n.on })
		for s.first[i] = max(min(s.first[i], on, len(names[i])-1), 0); s.first[i] < on; s.first[i]++ {
			if r, _ := l.rows(); slices.ContainsFunc(r[i], func(n span) bool { return n.on }) {
				break
			}
		}
	}
}

// Wheel moves the first tab line by n names, later ones for n above 0, and
// stops where nothing more is hidden that way; a step times Lower moves the
// second line.
func (l *Layout) Wheel(n int) {
	s, i := l.Space(l.Front), 0
	if s == nil {
		return
	} else if n >= Lower || n <= -Lower {
		i, n = 1, n/Lower
	}
	for ; n < 0 && s.first[i] > 0; n++ {
		s.first[i]--
	}
	for ; n > 0; n-- {
		if r, _ := l.rows(); len(r[i]) == 0 || r[i][len(r[i])-1].step != 1 {
			return
		}
		s.first[i]++
	}
}

// Walk is the layout as the keys walk it, for whoever steps from tab to tab:
// the tabs of the workspace in front and no other; with two tab lines those
// of the second line while a tab of it shows, and while the workspace's
// first tab shows that tab and the one a click on each group goes to. The
// tab that shows is its Tabs[Active], and At answers as the whole does.
func (l *Layout) Walk() *Layout {
	w := *l
	w.whole, w.Tabs, w.Active = l, nil, 0
	names, dim := l.names()
	line := names[0]
	if len(names[1]) > 0 && !dim {
		line = names[1]
	}
	for i, n := range line {
		if w.Tabs = append(w.Tabs, n.tab); n.on {
			w.Active = i
		}
	}
	return &w
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
	if l.whole != nil {
		return l.whole.At(x, y)
	}
	t, head := l.shown(), l.Head()
	if l.Over != "" && l.OverAt.has(x, y) {
		return Hit{Kind: OnPane, Pane: l.Over, X: x - l.OverAt.X, Y: y - l.OverAt.Y}
	}
	if at, line, _, _ := l.side(); at.has(x, y) {
		return Hit{Kind: OnPane, Pane: DockPane, X: x - at.X, Y: y}
	} else if line.has(x, y) {
		return Hit{Kind: OnLine, Pane: DockPane}
	}
	if x < 0 || y < 0 || x >= l.W || y >= l.H || y == head || t == nil {
		return h
	}
	if y < head {
		h.Kind = OnRow
		rows, _ := l.rows()
		for _, s := range rows[y] {
			switch {
			case x < s.x || x >= s.x+s.w:
			case s.step != 0:
				return Hit{Kind: OnMore, Step: s.step * [2]int{1, Lower}[y]}
			case s.group:
				return Hit{Kind: OnGroup, Tab: s.tab.ID}
			default:
				return Hit{Kind: OnTab, Tab: s.tab.ID}
			}
		}
		return h
	}
	if t.Zoom != "" {
		return Hit{Kind: OnPane, Pane: t.Zoom, X: x - l.area().X, Y: y - l.area().Y}
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
// pane that has the keys are in the accent. On the tab row the name that
// shows is in the accent and bold; a second line begins with a corner under
// the first line's start, and is dim while no tab of it shows.
func (l *Layout) Draw(g *term.Grid, k Look) {
	glyphs, corner, marks := lines, "└", strings.NewReplacer()
	if k.Plain {
		glyphs, corner, marks = plain, "+", strings.NewReplacer("·", "-", "▲", "!")
	}
	rows, dim := l.rows()
	for y := range l.Head() {
		if y == 1 {
			g.Put(l.area().X+1, y, 1, corner, k.Frame)
		}
		for i, s := range rows[y] {
			st := k.Text
			if s.step != 0 || dim && y == 1 {
				st = k.Dim
			} else if s.on {
				st = k.Accent
				st.Bold = true
			}
			if i > 0 {
				g.Put(s.x-2, y, 1, glyphs[up|down], k.Frame)
			}
			if s.group {
				s.text, s.mark = marks.Replace(s.text), marks.Replace(s.mark)
			}
			w := s.w
			if m := term.Width(s.mark); m > 0 && w > m {
				w -= m + 1
				g.Put(s.x+w+1, y, m, s.mark, k.Need)
			}
			g.Put(s.x, y, w, s.text, st)
		}
	}
	ends := make([]byte, l.W*l.H)
	mark := func(r Rect, e byte) {
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				ends[y*l.W+x] |= e
			}
		}
	}
	mark(Rect{l.area().X, l.Head(), l.area().W, min(l.H-l.Head(), 1)}, left|right)
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
	// The dock's edge runs the whole height; no line shows under an open rail.
	if dock, edge, _, _ := l.side(); edge.W > 0 {
		for y := range l.H {
			clear(ends[y*l.W+min(dock.X, edge.X) : y*l.W+max(dock.X+dock.W, edge.X+1)])
			ends[y*l.W+edge.X] = up | down
		}
		if l.Dock.Keys {
			keys = Rect{dock.X - 1, -1, dock.W + 2, dock.H + 2}
		}
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
