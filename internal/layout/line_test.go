package layout

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// drawn is what the layout itself puts into a window: the tab row, the
// lines and the label of a held one, as text; lit names the cells that are
// in the lit line's own colour, which is not the accent of the keys' frame.
func drawn(l *Layout, k Look) (text string, lit func(x, y int) bool) {
	g := term.NewGrid(l.W, l.H)
	l.Draw(g, k)
	l.Say(g, k)
	var b strings.Builder
	for y := range g.H {
		var row strings.Builder
		for x := range g.W {
			row.WriteString(g.At(x, y).Text)
		}
		b.WriteString(strings.TrimRight(row.String(), " ") + "\n")
	}
	return b.String(), func(x, y int) bool { return g.At(x, y).Style == k.Lit }
}

// screen is the design's own: four tabs beside the dock, the fleet's pane
// at the right in cells, and with rows the action pane under the conversation.
func screen(w, h, fleet, actions int) *Layout {
	l := &Layout{Dock: Dock{On: true, Width: contract.SideWidth}}
	l.Add("t1", "Orchestrator", "p1")
	l.Add("t2", "sign-up page", "q2")
	l.Add("t3", "login page", "q3")
	l.Add("t4", "help pages", "q4")
	l.Fit(w, h)
	l.Split("p1", contract.Right, float64(fleet), "p2")
	if actions > 0 {
		l.Split("p1", contract.Down, float64(actions), "p3")
	}
	return l
}

func joined(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

// The dock's edge held, at 160 columns, as the design draws it: heavy over
// the whole height, the size beside it at the pointer's row, and every other
// line as it was.
func TestTheHeldEdgeOfTheDock(t *testing.T) {
	l := screen(160, 14, 46, 0)
	l.Drag(l.At(28, 5), 31, 5)
	l.Lit, l.Held = l.At(31, 9), true
	l.Lit.X, l.Lit.Y = 31, 9
	pad, side := strings.Repeat(" ", 31), strings.Repeat(" ", 81)
	want := []string{pad + "┃ Orchestrator │ sign-up page │ login page │ help pages",
		pad + "┃" + strings.Repeat("─", 81) + "┬" + strings.Repeat("─", 46)}
	for y := 2; y < 14; y++ {
		if y == 9 {
			want = append(want, pad+"┃◀▶ 31 columns"+side[13:]+"│")
		} else {
			want = append(want, pad+"┃"+side+"│")
		}
	}
	got, lit := drawn(l, look)
	if got != joined(want...) {
		t.Errorf("the held edge:\n%s", got)
	}
	if !lit(31, 0) || !lit(31, 13) || !lit(32, 9) {
		t.Error("the held edge and its label are not in the accent")
	}
}

// The action pane's top edge held, at 120 columns, as the design draws it:
// heavy from the dock's edge to the fleet's, with the pane's rows on it at
// the pointer.
func TestTheHeldEdgeOfTheActionPane(t *testing.T) {
	l := screen(120, 24, 36, 9)
	l.Lit, l.Held = l.At(55, 14), true
	l.Lit.X, l.Lit.Y = 55, 14
	if l.Lit.Kind != OnLine {
		t.Fatalf("no line at the action pane's top edge: %+v", l.Lit)
	}
	pad := strings.Repeat(" ", 22)
	want := []string{pad + "│ Orchestrator │ sign-up page │ login page │ help pages",
		pad + "├" + strings.Repeat("─", 60) + "┬" + strings.Repeat("─", 36)}
	for y := 2; y < 24; y++ {
		if y == 14 {
			want = append(want, pad+"┣"+strings.Repeat("━", 27)+" ▲▼ 9 rows "+strings.Repeat("━", 22)+"┫")
		} else {
			want = append(want, pad+"│"+strings.Repeat(" ", 60)+"│")
		}
	}
	if got, _ := drawn(l, look); got != joined(want...) {
		t.Errorf("the held edge:\n%s", got)
	}
	// In a terminal without the line characters.
	plain := look
	plain.Plain = true
	got, _ := drawn(l, plain)
	if line := strings.Split(got, "\n")[14]; line != pad+"#"+strings.Repeat("#", 27)+" ^v 9 rows "+strings.Repeat("#", 22)+"#" {
		t.Errorf("the held edge in plain text: %q", line)
	}
}

// A line under the pointer is in the accent and as thin as it was, with no
// label; a line the layout has no more is not drawn; and the words the
// keeper sets stand in place of the size.
func TestALineUnderThePointer(t *testing.T) {
	l := screen(160, 14, 46, 0)
	l.Split("p2", contract.Down, 0.5, "p5")
	l.Focus("p1")
	before, was := drawn(l, look)
	l.Lit = l.At(130, 8)
	got, lit := drawn(l, look)
	if l.Lit.Kind != OnLine || got != before || was(130, 8) || !lit(130, 8) || !lit(159, 8) {
		t.Errorf("a line under the pointer is drawn otherwise, or is not in the accent:\n%s", got)
	}
	l.Lit, l.Held, l.Said = l.At(113, 5), true, "click where it should go · esc"
	l.Lit.Y = 4
	if got, _ := drawn(l, look); !strings.Contains(strings.Split(got, "\n")[4], "┃click where it should go · esc") {
		t.Errorf("the words of a line that waits:\n%s", got)
	}
	l.Said = ""
	l.Close("p5")
	l.Close("p2")
	if got, _ := drawn(l, look); strings.ContainsAny(got, "┃━◀▶") {
		t.Errorf("a line that is gone is drawn held:\n%s", got)
	}
	// At the right edge the label stands left of the line.
	l = screen(160, 14, 46, 0)
	l.Dock.Right, l.Dock.Width = true, contract.SideMin
	l.Lit, l.Held = l.At(143, 3), true
	l.Lit.Y = 3
	if got, _ := drawn(l, look); !strings.HasSuffix(strings.Split(got, "\n")[3], "◀▶ 16 columns · the least┃") {
		t.Errorf("the label of an edge at the right:\n%s", got)
	}
}

// The label says the limit a line stands at: the least and the most of the
// dock and of a split, the most a window's width gives, and that a width is
// fixed there.
func TestTheLabelSaysTheLimit(t *testing.T) {
	for _, c := range []struct {
		w, x int
		want string
	}{{200, 40, "◀▶ 40 columns"}, {200, 2, "◀▶ 16 columns · the least"}, {200, 150, "◀▶ 60 columns · the most"},
		{120, 60, "◀▶ 22 columns · the most at this window size"}, {120, 18, "◀▶ 18 columns"},
		{110, 60, "◀▶ 22 columns · the most at this window size"}, {80, 30, "fixed at this window size"}} {
		l := docked(Dock{On: true, Width: contract.SideWidth}, c.w, 20)
		_, edge, _, _ := l.side()
		l.Lit, l.Held = l.At(edge.X, 5), true
		if l.Drag(l.Lit, c.x, 5); l.said() != c.want {
			t.Errorf("%d columns, dragged to %d: %q, not %q", c.w, c.x, l.said(), c.want)
		}
	}
	var l *Layout
	for _, c := range []struct {
		x, y, tx, ty int
		want         string
	}{{113, 5, 100, 5, "◀▶ 59 columns"}, {113, 5, 0, 5, "◀▶ 122 columns · the most"}, {113, 5, 159, 5, "◀▶ 8 columns · the least"},
		{60, 20, 60, 10, "▲▼ 19 rows"}, {60, 20, 60, 29, "▲▼ 2 rows · the least"}, {60, 20, 60, 0, "▲▼ 25 rows · the most"}} {
		l = screen(160, 30, 46, 9)
		l.Lit, l.Held = l.At(c.x, c.y), true
		if l.Drag(l.Lit, c.tx, c.ty); l.said() != c.want {
			t.Errorf("the line at %d, %d dragged to %d, %d: %q, not %q", c.x, c.y, c.tx, c.ty, l.said(), c.want)
		}
	}
	// A split by shares says the side that was split off, as one by cells
	// does: the new pane on the right or below, or on the left or above.
	l = docked(Dock{}, 101, 20)
	l.Split("p1", contract.Right, 0.3, "p2")
	if l.Lit, l.Held = l.At(30, 5), true; l.said() != "◀▶ 70 columns" {
		t.Errorf("a split by shares, the new pane on the right: %q", l.said())
	}
	l = docked(Dock{}, 101, 20)
	l.Split("p1", contract.Up, 0.75, "p2")
	at := Hit{}
	for y := range l.H {
		if h := l.At(30, y); h.Kind == OnLine {
			at = h
		}
	}
	if l.Lit, l.Held = at, true; l.said() != "▲▼ 4 rows" {
		t.Errorf("a split by shares, the new pane above with a quarter: %q", l.said())
	}
}

// Under 160 columns a drag of the dock's edge is held to what the window's
// width gives the dock: past that nothing is set, and a narrower dock is.
func TestADragIsHeldToTheWindowsWidth(t *testing.T) {
	for _, c := range []struct{ w, was, x, want int }{{120, 28, 60, 28}, {120, 28, 22, 28}, {120, 28, 18, 18},
		{120, 18, 60, 22}, {80, 28, 40, 28}, {60, 28, 30, 28}, {160, 28, 40, 40}} {
		l := docked(Dock{On: true, Width: c.was}, c.w, 20)
		_, edge, _, _ := l.side()
		if l.Drag(l.At(edge.X, 5), c.x, 5); l.Dock.Width != c.want {
			t.Errorf("%d columns, set to %d, dragged to %d: %d, not %d", c.w, c.was, c.x, l.Dock.Width, c.want)
		}
	}
}

// Reset puts a line back to how it came: the dock's edge to the contract's
// width, a split to the share or the cells it was opened with, which a
// layout file keeps; a split of a file that does not say goes to a half.
func TestALinePutBack(t *testing.T) {
	l := docked(Dock{On: true, Width: 44, Fold: true}, 200, 30)
	if l.Reset(Hit{Kind: OnLine, Pane: DockPane}); l.Dock.Width != contract.SideWidth || l.Dock.Fold {
		t.Errorf("the dock's edge put back: %+v", l.Dock)
	}
	l = docked(Dock{}, 101, 30)
	l.Split("p1", contract.Right, 0.3, "p2")
	l.Split("p1", contract.Down, 7, "p3")
	l.Split("p2", contract.Left, 0.25, "p4")
	text, _ := json.Marshal(l)
	l = &Layout{}
	if err := json.Unmarshal(text, l); err != nil {
		t.Fatal(err)
	}
	l.Fit(101, 30)
	before := picture(l, look)
	for _, at := range [][4]int{{30, 5, 60, 5}, {10, 22, 10, 9}, {83, 5, 90, 5}} {
		h := l.At(at[0], at[1])
		if h.Kind != OnLine {
			t.Fatalf("no line at %v:\n%s", at, before)
		}
		l.Drag(h, at[2], at[3])
		moved := picture(l, look)
		if moved == before {
			t.Fatalf("the line at %v did not move", at)
		}
		if l.Reset(l.At(at[2], at[3])); picture(l, look) != before {
			t.Errorf("the line at %v put back:\n%s", at, picture(l, look))
		}
	}
	root := l.Tabs[0].Root
	root.Made, root.Share = 0, 0.8
	if l.Reset(Hit{Kind: OnLine, line: root, side: root.A}); root.Share != 0.5 {
		t.Errorf("a split of an older file put back: %v", root.Share)
	}
	held := root.A
	if held.Made, held.Cells = 0, 11; held.A == nil {
		t.Fatal("the first side is no split")
	}
	if l.Reset(Hit{Kind: OnLine, line: held, side: held.A}); held.Cells != 11 {
		t.Errorf("a split held to its cells in an older file put back: %d cells", held.Cells)
	}
	l.Reset(Hit{Kind: OnLine})
	l.Reset(Hit{Kind: OnLine, line: root.A.A})
	if !l.Sound() {
		t.Error("a line of no split put back left the layout unsound")
	}
}

// A line whose split went with one of its panes is no line any more, though
// the neighbour's split now stands in its place in the tree: it is not lit,
// not dragged and not put back, and the line that is there is a line.
func TestALineThatWentWithItsSplit(t *testing.T) {
	l := docked(Dock{}, 101, 30)
	l.Split("p1", contract.Right, 0.5, "p2")
	l.Split("p2", contract.Down, 0.3, "p3")
	h := Hit{}
	for x := range l.W {
		if at := l.At(x, 20); at.Kind == OnLine {
			h = at
		}
	}
	if !l.Has(h) || h.Kind != OnLine || !l.Has(Hit{}) || !l.Has(Hit{Kind: OnLine, Pane: DockPane}) {
		t.Fatalf("the line between the two sides: %+v", h)
	}
	l.Lit, l.Held = h, true
	if line, _, _ := l.held(); line.H == 0 {
		t.Fatal("the held line has no place")
	}
	l.Close("p1")
	before := picture(l, look)
	if l.Has(h) {
		t.Error("the layout says it has the line of a split that is gone")
	}
	l.Drag(h, 50, 20)
	l.Reset(h)
	if line, _, _ := l.held(); picture(l, look) != before || line.W != 0 || line.H != 0 || l.said() != "" {
		t.Errorf("the line that is gone was moved or is lit at %+v:\n%s", line, picture(l, look))
	}
	now := Hit{}
	for y := 2; y < l.H; y++ {
		if at := l.At(50, y); at.Kind == OnLine {
			now = at
		}
	}
	if l.Drag(now, 50, 20); !l.Has(now) || picture(l, look) == before {
		t.Errorf("the line that is there does not move: %+v", now)
	}
}
