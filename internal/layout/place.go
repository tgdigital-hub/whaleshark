package layout

import (
	"cmp"
	"fmt"
	"math"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Rect is a place in the window, in cells.
type Rect struct{ X, Y, W, H int }

func (r Rect) has(x, y int) bool { return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H }

// meet is how many cells two places share.
func (r Rect) meet(o Rect) int {
	return max(min(r.X+r.W, o.X+o.W)-max(r.X, o.X), 0) * max(min(r.Y+r.H, o.Y+o.H)-max(r.Y, o.Y), 0)
}

// Place is where a pane is shown. A pane's inside is its whole place.
type Place struct {
	Pane string
	Rect
}

// side is the dock and its dividing line as they show, and the columns left
// to every tab: from x, w of them. The dock's width is contract.Fit's for
// this window: the set one, a narrower one, or a rail, as a folded dock is.
func (l *Layout) side() (at, line Rect, x, w int) {
	if !l.Dock.On {
		return at, line, 0, l.W
	}
	kept := contract.Fit(l.W, contract.Layout{Side: l.Dock.Width}).Side
	if l.Dock.Fold {
		kept = contract.SideRail
	}
	shown := kept
	if l.Dock.Open && kept == contract.SideRail {
		shown = min(max(l.Dock.Width, contract.SideMin), contract.SideMax)
	}
	kept, shown = max(min(kept, l.W-1), 0), max(min(shown, l.W-1), 0)
	at, line = Rect{0, 0, shown, l.H}, Rect{shown, 0, min(l.W, 1), l.H}
	if w = max(l.W-kept-1, 0); l.Dock.Right {
		line.X = l.W - shown - 1
		at.X = line.X + 1
		return at, line, 0, w
	}
	return at, line, l.W - w, w
}

// area is the window under the tab row and its line, beside the dock, for
// the workspace in front; areaOf is a workspace's own, whose tab row may
// have another count of lines.
func (l *Layout) area() Rect { return l.areaOf(l.Front) }

func (l *Layout) areaOf(space string) Rect {
	_, _, x, w := l.side()
	y := l.lines(space) + 1
	return Rect{x, y, w, max(l.H-y, 0)}
}

// least is the smallest a node can be, down or across.
func (n *Node) least(down bool) int {
	switch {
	case n.A != nil && n.Down == down:
		return n.A.least(down) + 1 + n.B.least(down)
	case n.A != nil:
		return max(n.A.least(down), n.B.least(down))
	case down:
		return LeastH
	}
	return LeastW
}

// sizes divides the cells of a split between its two sides, less one for the
// line. A share is rounded to cells; a fixed side gets its cells; either gives
// way so that both sides keep their smallest size. Where even that does not
// fit, each side gets its part of what there is, and none is ever negative.
func (n *Node) sizes(all int) (a, b int) {
	free := all - 1
	if free < 0 {
		return 0, 0
	}
	la, lb := n.A.least(n.Down), n.B.least(n.Down)
	if la+lb > free {
		a = free * la / (la + lb)
		return a, free - a
	}
	a = n.hold(n.want(free), free)
	return a, free - a
}

// want is the size the split asks for its first side out of free cells.
func (n *Node) want(free int) int {
	switch {
	case n.Cells > 0 && n.Last:
		return free - n.Cells
	case n.Cells > 0:
		return n.Cells
	}
	return int(n.Share*float64(free) + 0.5)
}

// hold keeps a size for the first side inside what both sides need.
func (n *Node) hold(a, free int) int {
	return min(max(a, n.A.least(n.Down)), free-n.B.least(n.Down))
}

// cut is the places of a split's two sides and of the line between them.
func (n *Node) cut(r Rect) (a, b, line Rect) {
	a, b, line = r, r, r
	if n.Down {
		a.H, b.H = n.sizes(r.H)
		line.Y, line.H = r.Y+a.H, min(r.H, 1)
		b.Y = line.Y + line.H
	} else {
		a.W, b.W = n.sizes(r.W)
		line.X, line.W = r.X+a.W, min(r.W, 1)
		b.X = line.X + line.W
	}
	return a, b, line
}

// walk calls fn for n and everything under it, each with its place.
func walk(n *Node, r Rect, fn func(*Node, Rect)) {
	fn(n, r)
	if n.A != nil {
		a, b, _ := n.cut(r)
		walk(n.A, a, fn)
		walk(n.B, b, fn)
	}
}

// place is the place a node of a tab has when nothing is zoomed.
func (l *Layout) place(t *Tab, of *Node) (at Rect) {
	walk(t.Root, l.areaOf(t.Space), func(n *Node, r Rect) {
		if n == of {
			at = r
		}
	})
	return at
}

// Places is where each pane of a tab shows at the window's size: the dock
// first, beside every tab and beside none, then every pane or the zoomed one
// alone. No two places share a cell but under an open rail, none has a
// negative side, and one with no cells is a pane the window has no room for.
func (l *Layout) Places(t *Tab) (out []Place) {
	if at, _, _, _ := l.side(); l.Dock.On {
		out = append(out, Place{DockPane, at})
	}
	if t == nil {
		return out
	} else if t.Zoom != "" {
		return append(out, Place{t.Zoom, l.areaOf(t.Space)})
	}
	walk(t.Root, l.areaOf(t.Space), func(n *Node, r Rect) {
		if n.A == nil {
			out = append(out, Place{n.Pane, r})
		}
	})
	return out
}

// Beside is the pane across pane's dividing line in a direction, the one
// that shares most of that line; empty when the window's edge is there.
func (l *Layout) Beside(pane, direction string) string {
	t := l.Of(pane)
	if t == nil {
		return ""
	}
	if l.W == 0 || l.H == 0 {
		// Before the first window nothing has a place; the panes lie as
		// they would in any window with room for them all.
		roomy := Layout{Tabs: l.Tabs, Active: l.Active, Front: l.Front, W: 1 << 12, H: 1 << 12}
		return roomy.Beside(pane, direction)
	}
	places := l.Places(t)
	var probe Rect
	for _, p := range places {
		if p.Pane == pane {
			probe = p.Rect
		}
	}
	switch direction {
	case contract.Right:
		probe.X, probe.W = probe.X+probe.W+1, 1
	case contract.Left:
		probe.X, probe.W = probe.X-2, 1
	case contract.Down:
		probe.Y, probe.H = probe.Y+probe.H+1, 1
	case contract.Up:
		probe.Y, probe.H = probe.Y-2, 1
	default:
		return ""
	}
	found, most := "", 0
	for _, p := range places {
		if m := p.meet(probe); m > most && p.Pane != DockPane {
			found, most = p.Pane, m
		}
	}
	return found
}

// move puts a split's line a cells from its start, held to the smallest
// sizes, and keeps the result as the split keeps its size: cells or a share.
func (n *Node) move(r Rect, a int) {
	free := r.W - 1
	if n.Down {
		free = r.H - 1
	}
	if n.A.least(n.Down)+n.B.least(n.Down) > free {
		return
	}
	switch a = n.hold(a, free); {
	case n.Cells > 0 && n.Last:
		n.Cells = free - a
	case n.Cells > 0:
		n.Cells = a
	default:
		n.Share = float64(a) / float64(free)
	}
}

// Resize moves a pane's dividing line in a direction by a fraction of its
// split: the line on that side of the pane, or, at the window's edge, the
// one on its other side.
func (l *Layout) Resize(pane, direction string, amount float64) error {
	t := l.Of(pane)
	if t == nil {
		return contract.ErrNoPane
	}
	down := direction == contract.Down || direction == contract.Up
	back := direction == contract.Left || direction == contract.Up
	if !down && !back && direction != contract.Right {
		return ErrDirection
	}
	var near, far *Node
	var nr, fr Rect
	walk(t.Root, l.areaOf(t.Space), func(n *Node, r Rect) {
		if n.A == nil || n.Down != down {
			return
		}
		inA, _ := n.A.leaf(pane, nil)
		inB, _ := n.B.leaf(pane, nil)
		if inA != nil && !back || inB != nil && back {
			near, nr = n, r
		} else if inA != nil || inB != nil {
			far, fr = n, r
		}
	})
	if near == nil {
		near, nr = far, fr
	}
	if near == nil {
		return ErrNoLine
	}
	a, _, line := near.cut(nr)
	size, free := a.W, nr.W-line.W
	if down {
		size, free = a.H, nr.H-line.H
	}
	if math.IsNaN(amount) {
		amount = 0
	}
	by := int(math.Round(max(min(amount, 1), -1) * float64(free)))
	if back {
		by = -by
	}
	near.move(nr, size+by)
	return nil
}

// Drag moves the dividing line a press landed on to the cell the pointer is
// on now. The hit is one of the layout as it is: once a pane has opened or
// closed, At is asked again. The dock's edge sets its width, unfolded, within
// the contract's limits, never past what leaves a tab contract.TalkMin, and
// held to what this window's width gives the dock: where that is no other
// width than it shows already, the set width stays as it is.
func (l *Layout) Drag(h Hit, x, y int) {
	if h.Kind == OnLine && h.Pane == DockPane {
		if l.Dock.Right {
			x = l.W - 1 - x
		}
		if to := l.fit(x); to != l.fit(l.Dock.Width) {
			l.Dock.Width = to
		}
		l.Dock.Fold = false
		return
	}
	if h.line == nil || !l.Has(h) {
		return
	}
	walk(l.Tabs[l.Active].Root, l.area(), func(n *Node, r Rect) {
		if n == h.line && n.Down {
			n.move(r, y-r.Y)
		} else if n == h.line {
			n.move(r, x-r.X)
		}
	})
}

// fit is the width the dock shows in this window when it is set to w.
func (l *Layout) fit(w int) int {
	return contract.Fit(l.W, contract.Layout{Side: max(min(w, contract.SideMax, l.W-1-contract.TalkMin), contract.SideMin)}).Side
}

// Reset puts a dividing line back to how it came: the dock's edge to the
// contract's width, a split's line to what the split was opened with, a
// half where an older file does not say.
func (l *Layout) Reset(h Hit) {
	switch n := h.line; {
	case h.Kind == OnLine && h.Pane == DockPane:
		l.Dock.Width, l.Dock.Fold = contract.SideWidth, false
	case n == nil || !l.Has(h):
	case n.Cells > 0:
		n.Cells = cmp.Or(int(n.Made), n.Cells)
	default:
		n.Share = min(cmp.Or(n.Made, 0.5), 1)
	}
}

// Has says the layout still has the line a hit was on: a split's line is
// gone with a pane of its split, though a neighbour stands in its place.
func (l *Layout) Has(h Hit) (has bool) {
	if t := l.shown(); h.line != nil && t != nil {
		walk(t.Root, Rect{}, func(o *Node, _ Rect) { has = has || o == h.line && o.A != nil && o.A == h.side })
		return has
	}
	return h.line == nil
}

// held is the place of the line that is lit, none where the layout has that
// line no more; for a split's line also the split and its place.
func (l *Layout) held() (line Rect, n *Node, in Rect) {
	t := l.shown()
	switch {
	case l.Lit.Kind != OnLine:
	case l.Lit.Pane == DockPane:
		_, line, _, _ = l.side()
	case t != nil && t.Zoom == "":
		walk(t.Root, l.area(), func(o *Node, r Rect) {
			if o == l.Lit.line && o.A == l.Lit.side {
				_, _, line = o.cut(r)
				n, in = o, r
			}
		})
	}
	return line, n, in
}

// said is the label of the held line: the size it gives, of the dock, or of
// the side of a split that was split off, which is the fleet pane's width,
// the action pane's height and the new pane of a split a person made; and
// the limit the line stands at.
func (l *Layout) said() string {
	line, n, in := l.held()
	if l.Said != "" || line.W == 0 {
		return l.Said
	}
	dock, _, _, _ := l.side()
	size, lo, hi, unit, most := dock.W, l.fit(0), l.fit(contract.SideMax), "◀▶ %d columns", " · the most"
	if n == nil && lo == hi {
		return "fixed at this window size"
	} else if n == nil && hi < contract.SideMax {
		most += " at this window size"
	} else if n != nil {
		a, b, _ := n.cut(in)
		free := a.W + b.W
		if size = a.W; n.Down {
			free, size, unit = a.H+b.H, a.H, "▲▼ %d rows"
		}
		if lo, hi = n.A.least(n.Down), free-n.B.least(n.Down); n.Last {
			size, lo, hi = free-size, free-hi, free-lo
		}
	}
	text := fmt.Sprintf(unit, size)
	if lo < hi && size <= lo {
		return text + " · the least"
	} else if lo < hi && size >= hi {
		return text + most
	}
	return text
}
