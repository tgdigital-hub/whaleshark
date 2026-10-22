package keeper

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/layout"
	"github.com/tgdigital-hub/whaleshark/internal/theme"
)

// The pointer as a terminal reports it: a movement with no button down, a
// press and a release of a button (0 the left, 2 the right), a movement
// with the left button down.
func (o *outer) mouse(code int, x, y int, end string) {
	o.types(fmt.Sprintf("\x1b[<%d;%d;%d%s", code, x+1, y+1, end))
}
func (o *outer) move(x, y int)    { o.mouse(35, x, y, "M") }
func (o *outer) press(x, y int)   { o.mouse(0, x, y, "M") }
func (o *outer) pull(x, y int)    { o.mouse(32, x, y, "M") }
func (o *outer) release(x, y int) { o.mouse(0, x, y, "m") }
func (o *outer) right(x, y int)   { o.mouse(2, x, y, "M"); o.mouse(2, x, y, "m") }

// at is where a pane shows.
func (r *rig) at(pane string) (at layout.Rect) {
	r.locked(func() bool { at = r.k.place(r.k.panes[pane]); return true })
	return at
}

// width waits until the settings file holds that width of the dock.
func (r *rig) width(want int) {
	r.t.Helper()
	r.until(fmt.Sprintf("the width %d in the settings file", want), func() bool {
		s, err := contract.ReadSettings(r.kit.Platform.Peek, r.k.dirs)
		return err == nil && s.Side.Width == want
	})
}

// The dock's edge: it is lit while the pointer is on it; held, it is heavy
// and says its size at every cell; let go, it is plain and the width is
// written. Two presses put it back. A click leaves it waiting for the press
// that puts it, which reaches nothing else; Escape ends the waiting.
func TestTheDocksEdgeLitHeldPutAndPutBack(t *testing.T) {
	r := dockRig(t)
	c := r.dial()
	w := r.window(200, 30)
	r.sized(c, layout.DockPane, contract.SideWidth, 30)
	if reply := c.call(contract.WireCall{Op: contract.OpSplit, Pane: "p1", Dir: contract.Down}); reply.Err != "" {
		t.Fatal(reply)
	}
	lit := func(what string, ask func(l *layout.Layout) bool) {
		t.Helper()
		r.until(what, func() bool { return r.locked(func() bool { return ask(&r.k.lay) }) })
	}
	w.move(28, 10)
	lit("the edge under the pointer to be lit", func(l *layout.Layout) bool {
		return l.Lit.Kind == layout.OnLine && l.Lit.Pane == layout.DockPane && !l.Held
	})
	w.move(90, 10)
	lit("no line to be lit beside it", func(l *layout.Layout) bool { return l.Lit.Kind == 0 })

	w.press(28, 10)
	if x, y := w.shows("┃◀▶ 28 columns"); x != 28 || y != 10 {
		t.Errorf("the held edge's label is at %d, %d", x, y)
	}
	w.pull(40, 12)
	if x, y := w.shows("┃◀▶ 40 columns"); x != 40 || y != 12 {
		t.Errorf("the dragged edge's label is at %d, %d", x, y)
	}
	r.sized(c, layout.DockPane, 40, 30)
	w.pull(190, 12)
	w.shows("┃◀▶ 60 columns · the most")
	w.pull(40, 12)
	w.shows("┃◀▶ 40 columns")
	w.release(40, 12)
	r.until("the edge to be plain again", func() bool { return !strings.ContainsAny(w.text(), "┃◀") && w.cell(40, 12) == "│" })
	r.width(40)

	// Two presses within the span of a double-click: back to how it comes.
	w.press(40, 10)
	w.release(40, 10)
	w.press(40, 10)
	w.release(40, 10)
	r.sized(c, layout.DockPane, contract.SideWidth, 30)
	r.width(contract.SideWidth)
	// The window may still show the first press's waiting edge at 40.
	r.until("the window to show the edge plain where it comes", func() bool {
		return !strings.Contains(w.text(), "click where") && w.cell(contract.SideWidth, 10) == "│"
	})
	if r.dockIs(func(d layout.Dock) bool { return d.Fold }) {
		t.Error("two presses on the edge folded the dock")
	}

	// A click, and then a press where it should go.
	keys := r.focused()
	w.press(28, 10)
	w.release(28, 10)
	if x, y := w.shows("┃" + waits); x != 28 || y != 10 {
		t.Errorf("the waiting edge's label is at %d, %d", x, y)
	}
	time.Sleep(20 * time.Millisecond)
	w.press(50, 20)
	w.release(50, 20)
	r.sized(c, layout.DockPane, 50, 30)
	r.width(50)
	r.until("the edge to wait no longer", func() bool { return !strings.Contains(w.text(), "click where") })
	if r.focused() != keys || r.at("p2").Y > 20 {
		t.Errorf("the press that put the edge moved the keys to %s", r.focused())
	}
	// A click and Escape: the edge stays, and the next press is a press.
	w.press(50, 10)
	w.release(50, 10)
	w.shows(waits)
	w.types("\x1b")
	r.until("the edge to wait no longer after Escape", func() bool { return !strings.Contains(w.text(), "click where") })
	w.click(90, 25)
	r.until("the keys in the pane that was pressed", func() bool { return r.focused() == "p2" })
	r.sized(c, layout.DockPane, 50, 30)
	// Every width was written as the person's own: none is noted as a
	// setting an agent changed.
	var ui contract.UIFile
	contract.ReadVersioned(r.kit.Platform.Peek, filepath.Join(r.k.dirs.State, contract.UIFileName), contract.FileVersion, &ui)
	if len(ui.ByAgent) != 0 {
		t.Errorf("a dragged edge is noted as an agent's change: %+v", ui.ByAgent)
	}
	// A window that leaves with the edge held leaves no line lit behind.
	w.press(50, 10)
	lit("the edge to be held", func(l *layout.Layout) bool { return l.Held })
	w.conn.Close()
	lit("no line to be lit when its window has left", func(l *layout.Layout) bool { return l.Lit.Kind == 0 && !l.Held })
}

// Under 160 columns a drag of the dock's edge past what the window's width
// gives it says so and writes nothing.
func TestADragPastTheWindowsWidthWritesNothing(t *testing.T) {
	r := dockRig(t)
	c := r.dial()
	w := r.window(120, 30)
	r.sized(c, layout.DockPane, contract.SideNames, 30)
	w.press(22, 10)
	w.pull(60, 10)
	w.shows("◀▶ 22 columns · the most at this window size")
	w.release(60, 10)
	r.until("the edge to be plain again", func() bool { return !strings.Contains(w.text(), "◀▶") })
	w.drag(22, 10, 18, 10)
	r.width(18)
	if _, err := contract.ReadSettings(r.kit.Platform.Peek, r.k.dirs); err != nil {
		t.Fatal(err)
	}
	r.sized(c, layout.DockPane, 18, 30)
}

// Every dividing line of a tab: held, it is heavy and says the size at
// every cell; dragged, the panes take the size, which a restart brings
// back; two presses put it back to what the split was opened with. The label
// is the size of the pane that was split off.
func TestALineOfASplitHeldDraggedAndPutBack(t *testing.T) {
	r := newRig(t)
	c := r.dial()
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "first"})
	w := r.window(101, 30)
	c.call(contract.WireCall{Op: contract.OpSplit, Pane: "p1", Dir: contract.Right, Ratio: 0.5})  // p2
	c.call(contract.WireCall{Op: contract.OpSplit, Pane: "p1", Dir: contract.Down, Ratio: 0.5})   // p3
	c.call(contract.WireCall{Op: contract.OpSplit, Pane: "p2", Dir: contract.Down, Ratio: 6})     // p4, six rows
	c.call(contract.WireCall{Op: contract.OpSplit, Pane: "p2", Dir: contract.Right, Ratio: 0.25}) // p5
	sizes := func() (s string) {
		for _, p := range []string{"p1", "p2", "p3", "p4", "p5"} {
			at := r.at(p)
			s += fmt.Sprintf("%s %dx%d ", p, at.W, at.H)
		}
		return s
	}
	first := sizes()
	for _, l := range []struct {
		x, y, tx, ty int
		held, moved  string
	}{
		{r.at("p2").X - 1, 5, r.at("p2").X - 11, 5, "┃◀▶ 50 columns", "┃◀▶ 60 columns"},
		{10, r.at("p3").Y - 1, 10, r.at("p3").Y - 5, " ▲▼ 13 rows ", " ▲▼ 17 rows "},
		{70, r.at("p4").Y - 1, 70, r.at("p4").Y - 4, " ▲▼ 6 rows ", " ▲▼ 9 rows "},
		{r.at("p5").X - 1, 5, r.at("p5").X + 4, 5, "┃◀▶ 37 columns", "┃◀▶ 32 columns"},
	} {
		before := sizes()
		w.press(l.x, l.y)
		w.shows(l.held)
		w.pull(l.tx, l.ty)
		w.shows(l.moved)
		w.release(l.tx, l.ty)
		r.until("the line to be plain again", func() bool { return !strings.ContainsAny(w.text(), "┃━◀▲") })
		moved := sizes()
		if moved == before {
			t.Fatalf("the line at %d, %d did not move: %s", l.x, l.y, moved)
		}
		r.again()
		r.dial()
		w = r.window(101, 30)
		w.shows("first")
		if got := sizes(); got != moved {
			t.Errorf("after a restart the panes are\n%s\nnot\n%s", got, moved)
		}
		w.press(l.tx, l.ty)
		w.release(l.tx, l.ty)
		w.press(l.tx, l.ty)
		w.release(l.tx, l.ty)
		r.until("the line to be back", func() bool { return sizes() == before })
	}
	if got := sizes(); got != first {
		t.Errorf("with every line put back the panes are\n%s\nnot\n%s", got, first)
	}
}

// menus reads a keeper's events and gives each `menu` among them.
func (r *rig) menus() func() contract.TermEvent {
	watch := r.dial()
	watch.send(contract.WireCall{Op: contract.OpEvents})
	watch.reply()
	return func() contract.TermEvent {
		r.t.Helper()
		watch.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		for {
			if e := watch.reply().Event; e != nil && e.Kind == contract.EvMenu {
				return *e
			}
		}
	}
}

// The menu of a tab is asked for by a right press on its name and by the
// command key then a full stop: one event with the tab and the window's
// cell, and nothing else happens. On a fleet's name it is the leader's tab;
// the tabs opened by hand have none together.
func TestTheMenuOfATabIsAskedFor(t *testing.T) {
	r := newRig(t)
	c := r.dial()
	open := func(from, group, label string) contract.Pane {
		t.Helper()
		reply := c.call(contract.WireCall{Op: contract.OpTabCreate, Group: group, Label: label, From: from})
		if reply.Pane == nil {
			t.Fatalf("tab-create %s: %+v", label, reply)
		}
		return *reply.Pane
	}
	open("", "", "first")
	notes := open("", "", "notes")
	w := r.window(100, 30)
	next := r.menus()
	x, y := w.shows("notes")
	w.right(x+2, y)
	if e := next(); e.Pane.Tab != notes.Tab || e.X != x+2 || e.Y != y || e.Pane.ID != "" {
		t.Errorf("a right press on a tab's name told %+v", e)
	}
	if r.focused() != "p1" {
		t.Errorf("a right press on a tab's name gave the keys to %s", r.focused())
	}
	// The key: the tab that shows, at its name.
	w.click(x+2, y)
	r.until("the second tab to show", func() bool { return r.focused() == notes.ID })
	w.types(cmd + ".")
	if e := next(); e.Pane.Tab != notes.Tab || e.X != x || e.Y != y {
		t.Errorf("the command key then a full stop told %+v, the name is at %d, %d", e, x, y)
	}
	// With a fleet: its name on the first line is its leader's tab, whichever
	// of its tabs a click there would show; a name on the second line is its own.
	lead := open("p1", "front desk", "front desk")
	one := open(lead.ID, "", "sign-up page")
	gx, _ := w.shows("front desk · 1")
	w.click(gx+1, 0)
	r.until("the leader's tab to show", func() bool { return r.focused() == lead.ID })
	ox, oy := w.shows("sign-up page")
	w.click(ox+1, oy)
	r.until("the worker's tab to show", func() bool { return r.focused() == one.ID })
	w.right(gx+1, 0)
	if e := next(); e.Pane.Tab != lead.Tab || e.X != gx+1 || e.Y != 0 {
		t.Errorf("a right press on a fleet's name told %+v, its leader's tab is %s", e, lead.Tab)
	}
	hx, _ := w.shows("other · 1")
	w.right(hx+1, 0)
	w.right(ox+1, oy)
	if e := next(); e.Pane.Tab != one.Tab || e.Y != 1 {
		t.Errorf("after a right press on the tabs opened by hand, one on a worker's name told %+v", e)
	}
	w.types(cmd + ".")
	if e := next(); e.Pane.Tab != one.Tab || e.X != ox || e.Y != 1 {
		t.Errorf("the key with a fleet's tab in front told %+v, the name is at %d, 1", e, ox)
	}
	if r.focused() != one.ID {
		t.Errorf("asking for menus moved the keys to %s", r.focused())
	}
}

// A pane asked for at a cell stands there, its program's own box with
// nothing drawn round it, moved to stay inside the window. A press beside it
// closes it and reaches nothing else; so do another size of the window,
// another workspace in front and the end of the dock's program. A pane over
// the tab that was asked for with no place stays through all of that.
func TestAPlacedPaneOverTheTab(t *testing.T) {
	r := dockRig(t)
	c := r.dial()
	w := r.window(200, 30)
	r.sized(c, layout.DockPane, contract.SideWidth, 30)
	c.call(contract.WireCall{Op: contract.OpSplit, Pane: "p1", Dir: contract.Right}) // p2
	over := func() (id string) {
		r.locked(func() bool {
			if r.k.over != nil {
				id = r.k.over.rec.ID
			}
			return true
		})
		return id
	}
	gone := func(why string) {
		t.Helper()
		r.until("the placed pane to close "+why, func() bool { return over() == "" && !strings.Contains(w.text(), "the-menu") })
	}
	place := func(pane string, x, y, wantX, wantY int) string {
		t.Helper()
		call := contract.WireCall{Op: contract.OpOverlay, Dir: contract.OverAt, Argv: []string{"the-menu"}, W: 24, H: 12, Pane: pane, X: x, Y: y, From: layout.DockPane}
		if reply := c.call(call); reply.Err != "" {
			t.Fatalf("a pane placed at %d, %d of %q: %+v", x, y, pane, reply)
		}
		r.sized(c, over(), 24, 12)
		if gx, gy := w.shows("the-menu"); gx != wantX || gy != wantY {
			t.Errorf("a pane placed at %d, %d of %q shows at %d, %d, not %d, %d", x, y, pane, gx, gy, wantX, wantY)
		}
		return over()
	}
	// At a cell of the dock's pane, which is the window's there; nothing
	// of the keeper's is drawn round it.
	place(layout.DockPane, 5, 8, 5, 8)
	if strings.ContainsAny(w.cell(4, 8)+w.cell(5, 7), "┌│─") {
		t.Errorf("a frame is drawn round a placed pane:\n%s", w.text())
	}
	if reply := c.call(contract.WireCall{Op: contract.OpOverlay, Dir: contract.OverAt, Argv: []string{"another"}, From: layout.DockPane}); reply.Err == "" {
		t.Error("a second pane was placed over the first")
	}
	// A press beside it: closed, and the pane under the pointer has neither
	// a report nor the keys. The next press there is a press.
	in := r.at("p2")
	w.click(in.X+5, in.Y+5)
	gone("at a press beside it")
	if r.focused() != "p1" || r.program("p2").got() != "" {
		t.Errorf("the press beside a placed pane reached %s, typed %q", r.focused(), r.program("p2").got())
	}
	w.click(in.X+5, in.Y+5)
	r.until("the keys in the pane pressed next", func() bool { return r.focused() == "p2" })
	// A press inside it is the program's own affair and closes nothing.
	id := place("p1", 3, 2, r.at("p1").X+3, r.at("p1").Y+2)
	w.click(r.at("p1").X+5, r.at("p1").Y+3)
	// At the window's corner it is moved left and up; on the tab row, down.
	w.click(1, 1)
	gone("at a press in the dock")
	if id == "" {
		t.Fatal("no pane was placed")
	}
	place("", 198, 29, 176, 18)
	w.click(100, 3)
	gone("at a press beside it")
	place("", 60, 0, 60, 1)
	// Another size of the window.
	w.send(contract.WireCall{Op: contract.OpWindowSize, W: 180, H: 30})
	gone("at another size of the window")
	w.send(contract.WireCall{Op: contract.OpWindowSize, W: 200, H: 30})
	r.until("the window at its size again", func() bool { return r.locked(func() bool { return r.k.lay.W == 200 }) })
	// The dock's program ended.
	place("", 60, 5, 60, 5)
	r.program(layout.DockPane).Close()
	gone("with the dock's program")
	// Another workspace in front.
	place("", 60, 5, 60, 5)
	if reply := c.call(contract.WireCall{Op: contract.OpSpaceOpen, Space: t.TempDir(), From: layout.DockPane}); reply.Err != "" {
		t.Fatal(reply)
	}
	gone("with another workspace in front")
	// A pane with no place stays: beside it a press does nothing at all.
	if reply := c.call(contract.WireCall{Op: contract.OpOverlay, Argv: []string{"a-screen"}, W: 30, H: 5, From: layout.DockPane}); reply.Err != "" {
		t.Fatal(reply)
	}
	w.shows("a-screen")
	screen := over()
	w.click(190, 28)
	w.send(contract.WireCall{Op: contract.OpWindowSize, W: 180, H: 30})
	r.until("the window's new size", func() bool { return r.locked(func() bool { return r.k.lay.W == 180 }) })
	if over() != screen || screen == "" {
		t.Error("a pane over the tab with no place was closed by a press beside it or a new size")
	}
}

// The program in a placed pane may ask for another size of its box, and
// nobody else may: the corner stays, the box is moved to stay inside the
// window, and outside the box every cell of the window is the tab's own,
// what it was before the pane stood there, at every size the box had.
func TestAPlacedPaneTakesAnotherSize(t *testing.T) {
	r := dockRig(t)
	c := r.dial()
	w := r.window(200, 30)
	r.sized(c, layout.DockPane, contract.SideWidth, 30)
	r.program("p1").print(strings.Repeat("the conversation ", 400))
	r.settle()
	w.shows("the conversation")
	box := func() (at layout.Rect, id string) {
		r.locked(func() bool {
			if r.k.over != nil {
				at, id = r.k.overAt(), r.k.over.rec.ID
			}
			return true
		})
		return at, id
	}
	bare := w.text()
	stands := func(what string, want layout.Rect) {
		t.Helper()
		at, id := box()
		if at != want {
			t.Fatalf("%s: the box stands at %+v, not %+v", what, at, want)
		}
		r.sized(c, id, want.W, want.H)
		r.program(id).print("\x1b[2J\x1b[H" + strings.Repeat("#", want.W*want.H))
		r.until(what+": the box drawn and every cell beside it the tab's own", func() bool {
			text := w.text()
			return strings.Count(text, "#") == want.W*want.H && cellsOf(text, want) == cellsOf(bare, want)
		})
	}
	ask := func(from string, w, h float64) string {
		return c.call(contract.WireCall{Op: contract.OpOverlay, Dir: contract.OverAt, W: w, H: h, From: from}).Err
	}
	if reply := c.call(contract.WireCall{Op: contract.OpOverlay, Dir: contract.OverAt, Argv: []string{"the-menu"}, W: 24, H: 4, X: 60, Y: 5, From: layout.DockPane}); reply.Err != "" {
		t.Fatal(reply)
	}
	stands("as it opened", layout.Rect{X: 60, Y: 5, W: 24, H: 4})
	_, id := box()
	// Nobody but the program in the box: the dock and a pane of the tab are refused.
	if ask(layout.DockPane, 36, 8) == "" || ask("p1", 36, 8) == "" || ask("", 36, 8) == "" {
		t.Error("another pane gave the placed pane another size")
	}
	if ask(id, 36, 8) != "" {
		t.Fatal("the program in the box was refused another size")
	}
	stands("larger", layout.Rect{X: 60, Y: 5, W: 36, H: 8})
	if ask(id, 30, 4) != "" {
		t.Fatal("the program in the box was refused a smaller size")
	}
	stands("smaller again", layout.Rect{X: 60, Y: 5, W: 30, H: 4})
	// Too large for where it stands: moved left and up, never off the window.
	if ask(id, 180, 27) != "" {
		t.Fatal("refused")
	}
	stands("moved to stay inside", layout.Rect{X: 20, Y: 3, W: 180, H: 27})
	w.click(5, 1)
	r.until("the box closed and the window as it was", func() bool { _, id := box(); return id == "" && w.text() == bare })
	// A pane with no place has its size from whoever opened it.
	if reply := c.call(contract.WireCall{Op: contract.OpOverlay, Argv: []string{"a-screen"}, W: 30, H: 5, From: layout.DockPane}); reply.Err != "" {
		t.Fatal(reply)
	}
	if _, id := box(); ask(id, 36, 8) == "" {
		t.Error("a pane with no place was given another size")
	}
}

// cellsOf is every cell of a window's text of 200 by 30 that is outside a box.
func cellsOf(text string, but layout.Rect) string {
	lines := append(strings.Split(text, "\n"), make([]string, 30)...)
	var b strings.Builder
	for y := range 30 {
		row := append([]rune(lines[y]), []rune(strings.Repeat(" ", 200))...)
		for x := range 200 {
			if x < but.X || x >= but.X+but.W || y < but.Y || y >= but.Y+but.H {
				b.WriteRune(row[x])
			}
		}
	}
	return b.String()
}

// A program drawn over the tab is the person's own, whatever tab is in
// front: over a worker's tab it has none of that tab's variables, by which
// a command knows a worker, and it is told which pane the person was in.
func TestAPaneOverAWorkersTabIsNotThatWorker(t *testing.T) {
	r := dockRig(t)
	c := r.dial()
	r.window(200, 30)
	// The window comes on a line of its own: a menu placed before its size
	// is known is closed when it comes, as at any other size of the window.
	r.until("the window's size", func() bool { return r.locked(func() bool { return r.k.lay.W == 200 }) })
	made := c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "a worker", Env: []string{contract.EnvAttempt + "=T1.1", contract.EnvTask + "=T1"}})
	if made.Err != "" || made.Pane == nil {
		t.Fatal(made)
	}
	c.call(contract.WireCall{Op: contract.OpTabFocus, Tab: made.Pane.Tab})
	has := func(env []string, name string) string {
		for _, v := range env {
			if n, val, _ := strings.Cut(v, "="); n == name {
				return val
			}
		}
		return ""
	}
	if has(r.program(made.Pane.ID).spec.Env, contract.EnvAttempt) != "T1.1" {
		t.Fatal("the worker's own pane has not its tab's variables")
	}
	for _, call := range []contract.WireCall{
		{Op: contract.OpOverlay, Dir: contract.OverAt, Argv: []string{"the-menu"}, W: 24, H: 4, X: 60, Y: 5, From: layout.DockPane},
		{Op: contract.OpOverlay, Argv: []string{"a-screen"}, From: made.Pane.ID},
	} {
		if reply := c.call(call); reply.Err != "" {
			t.Fatal(reply)
		}
		var over string
		r.locked(func() bool {
			if r.k.over != nil {
				over = r.k.over.rec.ID
			}
			return true
		})
		if over == "" {
			t.Fatalf("%v is not over the tab once opened", call.Argv)
		}
		env := r.program(over).spec.Env
		if has(env, contract.EnvAttempt) != "" || has(env, contract.EnvTask) != "" || has(env, contract.EnvPane) != over || has(env, contract.EnvActivePane) != made.Pane.ID {
			t.Errorf("%v over a worker's tab is started with attempt %q, task %q, pane %q, the person's pane %q", call.Argv,
				has(env, contract.EnvAttempt), has(env, contract.EnvTask), has(env, contract.EnvPane), has(env, contract.EnvActivePane))
		}
		c.call(contract.WireCall{Op: contract.OpPaneClose, Pane: over})
	}
}

// The keeper's own parts follow the colour scheme the person chooses while
// a window is open: the tab row and the lines change with the panes, and
// are not left in the scheme the window was opened in.
func TestAWindowFollowsTheColourScheme(t *testing.T) {
	r := newRig(t)
	c := r.dial()
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "first"})
	w := r.window(100, 20)
	x, y := w.shows("first")
	fg := func() contract.Colour {
		w.mu.Lock()
		defer w.mu.Unlock()
		p := w.scr.Picture()
		return p.Cells[y*p.W+x].Style.Fg
	}
	was := fg()
	if _, err := contract.Set(&contract.Call{Kit: r.kit, Caller: contract.Caller{Kind: contract.Human}}, "theme", "paper"); err != nil {
		t.Fatal(err)
	}
	want := contract.RGB(theme.Get("paper").Colours["accent"])
	r.until("the tab's name in the new scheme's accent", func() bool { return fg() == want })
	if was == want {
		t.Error("the window was in that scheme before it was chosen")
	}
}
