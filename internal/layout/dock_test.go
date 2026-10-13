package layout

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// docked is one tab of one pane beside a dock.
func docked(d Dock, w, h int) *Layout {
	l := &Layout{Dock: d}
	l.Add("t1", "Lead", "p1")
	l.Fit(w, h)
	return l
}

// At every width from 20 to 240, for every width a person can set and at
// both edges: the dock is given first and has the width contract.Fit says,
// the tab has the rest less the dividing line and never fewer than fifty
// columns in a window of 72 or more, no region changes its side, nothing is
// shared, and going up and coming down give the same forms.
func TestTheDockAtEveryWidth(t *testing.T) {
	for _, d := range []Dock{{On: true}, {On: true, Width: 16}, {On: true, Width: 28}, {On: true, Width: 60}, {On: true, Width: 28, Right: true}, {On: true, Width: 99, Right: true}} {
		l := docked(d, 0, 0)
		var up, down []int
		check := func(w int) int {
			l.Fit(w, 30)
			places := l.Places(l.Tabs[0])
			if len(places) != 2 || places[0].Pane != DockPane || places[1].Pane != "p1" {
				t.Fatalf("%+v at %d: the places are %+v", d, w, places)
			}
			dock, tab := places[0].Rect, places[1].Rect
			want := contract.Fit(w, contract.Layout{Side: d.Width}).Side
			edge := dock.W
			if d.Right {
				edge = tab.W
			}
			switch {
			case dock.W != want || dock.Y != 0 || dock.H != 30 || tab.W != w-want-1 || tab.Y != 2 || tab.H != 28:
				t.Errorf("%+v at %d: the dock is %+v and the tab %+v, want a dock of %d", d, w, dock, tab, want)
			case d.Right && (tab.X != 0 || dock.X != w-want), !d.Right && (dock.X != 0 || tab.X != want+1):
				t.Errorf("%+v at %d: a region changed its side: the dock %+v, the tab %+v", d, w, dock, tab)
			case w >= contract.SideRailFrom && tab.W < contract.TalkMin, dock.meet(tab) != 0:
				t.Errorf("%+v at %d: the tab has %d columns", d, w, tab.W)
			}
			if h := l.At(dock.X+dock.W-1, 7); h.Kind != OnPane || h.Pane != DockPane || h.X != dock.W-1 || h.Y != 7 {
				t.Errorf("%+v at %d: a cell of the dock is %+v", d, w, h)
			}
			if h := l.At(edge, 0); h.Kind != OnLine || h.Pane != DockPane {
				t.Errorf("%+v at %d: the dock's edge is %+v", d, w, h)
			}
			if h := l.At(tab.X+tab.W-1, 2); h.Kind != OnPane || h.Pane != "p1" || h.X != tab.W-1 || h.Y != 0 {
				t.Errorf("%+v at %d: a cell of the tab is %+v", d, w, h)
			}
			rows, _ := l.rows()
			for _, s := range rows[0] {
				if s.x <= tab.X || s.x+s.w > tab.X+tab.W {
					t.Errorf("%+v at %d: the tab row has %+v outside the tab's columns", d, w, s)
				}
			}
			return dock.W
		}
		for w := 20; w <= 240; w++ {
			up = append(up, check(w))
		}
		for w := 240; w >= 20; w-- {
			down = append([]int{check(w)}, down...)
		}
		for i := range up {
			if up[i] != down[i] {
				t.Errorf("%+v at %d: %d going up and %d coming down", d, 20+i, up[i], down[i])
			}
		}
	}
	// A window with no room at all, and one with no size yet.
	for _, w := range []int{0, 1, 2, 5} {
		l := docked(Dock{On: true, Width: 28, Open: true}, w, 3)
		for _, p := range l.Places(l.Tabs[0]) {
			if p.X < 0 || p.W < 0 || p.X+p.W > w {
				t.Errorf("at %d columns %s is at %+v", w, p.Pane, p.Rect)
			}
		}
		l.Draw(newGrid(l), look)
	}
}

// A folded dock is a rail at every width. A rail drawn open has its set
// width and lies over the tab, which keeps its place; a dock that is no
// rail is not changed by it.
func TestTheRailFoldedAndOpen(t *testing.T) {
	for _, w := range []int{60, 120, 200} {
		l := docked(Dock{On: true, Width: 30, Fold: true}, w, 20)
		tab := l.Places(l.Tabs[0])[1].Rect
		if dock := l.Places(nil)[0]; dock.W != contract.SideRail || tab.X != contract.SideRail+1 || tab.W != w-contract.SideRail-1 {
			t.Errorf("folded at %d: the dock %+v, the tab %+v", w, dock, tab)
		}
		l.Dock.Open = true
		if dock := l.Places(nil)[0]; dock.W != 30 || l.Places(l.Tabs[0])[1].Rect != tab {
			t.Errorf("open at %d: the dock %+v, the tab %+v", w, dock, l.Places(l.Tabs[0])[1])
		}
		if under, edge, beside := l.At(20, 10), l.At(30, 10), l.At(31, 10); under.Pane != DockPane || under.Kind != OnPane || edge.Kind != OnLine || beside.Pane != "p1" || beside.X != 31-tab.X {
			t.Errorf("open at %d: under it %+v, its edge %+v, beside it %+v", w, under, edge, beside)
		}
	}
	l := docked(Dock{On: true, Width: 30, Open: true}, 200, 20)
	if dock := l.Places(nil)[0]; dock.W != 30 || l.Places(l.Tabs[0])[1].X != 31 {
		t.Errorf("a dock that is no rail, open: %+v", dock)
	}
	if l.Beside("p1", contract.Left) != "" || l.Beside("p1", contract.Right) != "" {
		t.Error("the dock is beside a pane as another pane would be")
	}
	l.Zoom("p1")
	if h := l.At(40, 5); h.Pane != "p1" || h.X != 9 || h.Y != 3 {
		t.Errorf("a cell of the zoomed pane beside the dock: %+v", h)
	}
}

// A drag of the dock's edge sets the width, at either edge: never under
// contract.SideMin, never over contract.SideMax, never past what leaves the
// tab contract.TalkMin columns; it unfolds a folded dock; and in a window
// whose width gives the dock one width whatever is set, it sets none.
func TestADragOfTheDocksEdge(t *testing.T) {
	for _, c := range []struct {
		w, x, want int
		right      bool
	}{{200, 40, 40, false}, {200, 2, 16, false}, {200, 199, 60, false}, {180, 170, 60, false}, {100, 70, 28, false}, {60, 30, 28, false},
		{200, 159, 40, true}, {200, 199, 16, true}, {200, 0, 60, true}, {100, 10, 28, true}} {
		l := docked(Dock{On: true, Width: 28, Right: c.right, Fold: true}, c.w, 20)
		edge := contract.SideRail
		if c.right {
			edge = c.w - contract.SideRail - 1
		}
		if l.Drag(l.At(edge, 9), c.x, 3); l.Dock.Width != c.want || l.Dock.Fold {
			t.Errorf("%+v: the width is %d, folded %v", c, l.Dock.Width, l.Dock.Fold)
		}
	}
	l := docked(Dock{On: true, Width: 28}, 200, 20)
	if l.Drag(Hit{Kind: OnLine}, 50, 3); l.Dock.Width != 28 {
		t.Error("a line of no split moved the dock's edge")
	}
}

func newGrid(l *Layout) *term.Grid { return term.NewGrid(l.W, l.H) }

// The dock's edge runs the whole height and joins the line under the tab
// row; with the keys in the dock it is in the accent; an open rail's edge
// is drawn where the open rail ends, and no line of the tab under it. (The
// tab's name under it is the keeper's to cover with the dock's picture.)
func TestTheDocksEdgeIsDrawn(t *testing.T) {
	l := docked(Dock{On: true, Width: 16}, 200, 5)
	for name, want := range map[string]string{
		"left":  "    │ Lead\n    ├──────────────\n    │\n    │\n    │\n",
		"right": " Lead   │\n────────┤\n        │\n        │\n        │\n",
		"keys":  "    ┃ Lead\n    ┣──────────────\n    ┃\n    ┃\n    ┃\n",
		"open":  "      Lead      │\n                ├──\n                │\n                │\n                │\n",
	} {
		l.Dock.Right, l.Dock.Keys, l.Dock.Fold, l.Dock.Open = name == "right", name == "keys", name == "open", name == "open"
		l.Fit(19, 5)
		if name == "right" {
			l.Fit(9+contract.SideRail, 5)
		} else if name == "open" {
			l.Fit(60, 5)
		}
		g := newGrid(l)
		l.Draw(g, look)
		var got strings.Builder
		for y := range g.H {
			var row strings.Builder
			for x := range min(g.W, 19) {
				c := g.At(x, y)
				if c.Style == look.Accent && name == "keys" {
					c.Text = heavy.Replace(c.Text)
				}
				row.WriteString(c.Text)
			}
			got.WriteString(strings.TrimRight(row.String(), " ") + "\n")
		}
		if got.String() != want {
			t.Errorf("%s:\n%s", name, got.String())
		}
	}
}

// No pane of a tab can have the dock's id: a layout that names one is not
// sound, and a layout carries nothing of the dock into a file.
func TestNoTabHoldsTheDock(t *testing.T) {
	l := docked(Dock{On: true, Width: 28, Fold: true}, 120, 30)
	if !l.Sound() {
		t.Fatal("a layout beside a dock is not sound")
	}
	if text, _ := json.Marshal(l); strings.Contains(strings.ToLower(string(text)), "dock") || strings.Contains(string(text), "Fold") {
		t.Errorf("the dock is in what a layout file holds: %s", text)
	}
	l.Split("p1", contract.Right, 0.5, DockPane)
	if l.Sound() {
		t.Error("a tab with a pane of the dock's id is taken for sound")
	}
}
