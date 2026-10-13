package input

import (
	"strconv"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/layout"
	"github.com/tgdigital-hub/whaleshark/internal/screen"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// A right press on a tab's name asks for that tab's menu at the cell of the
// press, whatever is held with it, and shows no tab; on a fleet's name it
// asks for the menu of the tab a click there goes to, where the left button
// shows that tab. Beside the tabs, on a count, on a line and with the
// middle button it asks for nothing, and in a pane it is the program's.
func TestARightPressAsksForTheMenu(t *testing.T) {
	in, v := In{Keys: New(settings(), "darwin")}, View{Lay: fixture(), Modes: all(screen.Modes{Mouse: 1000, MouseSGR: true})}
	for _, c := range [][2]string{
		{"\x1b[<2;2;1M\x1b[<2;2;1m", "menu of t1 at 1,0"},
		{"\x1b[<2;9;1M\x1b[<2;9;1m", "menu of t2 at 8,0"},
		{"\x1b[<6;10;1M", "menu of t2 at 9,0"},
		{"\x1b[<18;10;1M", "menu of t2 at 9,0"},
		{"\x1b[<2;31;1M\x1b[<2;31;1m", "nothing"},
		{"\x1b[<2;11;2M", "nothing"},
		{"\x1b[<2;41;11M\x1b[<34;31;11M\x1b[<2;31;11m", "nothing"},
		{"\x1b[<1;9;1M\x1b[<1;9;1m", "nothing"},
		{"\x1b[<2;6;6M\x1b[<2;6;6m", `p1 <- "\x1b[<2;6;4M" | p1 <- "\x1b[<2;6;4m"`},
	} {
		if got := shown(take(&in, v, c[0])); got != c[1] {
			t.Errorf("%q: %s", c[0], got)
		}
	}
	// The command key then a full stop asks for the menu of the tab that
	// shows, and names no cell: the keeper puts it under the tab's name.
	if a := take(&in, v, "\x00."); len(a) != 1 || a[0].Kind != Menu || a[0].Hit != (layout.Hit{Kind: layout.OnTab, Tab: "t1"}) || a[0].X != 0 || in.Armed {
		t.Errorf("the key for the menu: %+v", a)
	}
	if a := take(&In{Keys: in.Keys}, View{Lay: &layout.Layout{W: 80, H: 24}, Modes: v.Modes}, "\x00."); len(a) != 0 {
		t.Errorf("the key for the menu with no tab: %+v", a)
	}
	press := func(b term.Button) term.Event {
		return term.Event{Kind: term.Mouse, Button: b, Action: term.Press, X: 30, Y: 0}
	}
	for _, c := range []struct {
		kind int
		b    term.Button
		want string
	}{
		{layout.OnGroup, term.Right, "menu of t2 at 30,0"},
		{layout.OnGroup, term.Left, `call {"op":"tab-focus","tab":"t2"}`},
		{layout.OnGroup, term.Middle, "nothing"},
		{layout.OnMore, term.Right, "nothing"},
		{layout.OnMore, term.Left, "tab row +1"},
		{layout.OnRow, term.Right, "nothing"},
	} {
		h := layout.Hit{Kind: c.kind, Tab: "t2", Step: 1}
		if got := shown(in.point(h, press(c.b), v, "p1", v.Modes)); got != c.want {
			t.Errorf("button %d on a cell of kind %d: %s", c.b, c.kind, got)
		}
	}
	// The act names the tab and the cell, and nothing else of the press.
	a := take(&in, v, "\x1b[<2;9;1M")
	if len(a) != 1 || a[0].Kind != Menu || a[0].Hit != v.Lay.At(8, 0) || a[0].Hit.Kind != layout.OnTab || a[0].X != 8 || a[0].Y != 0 {
		t.Errorf("the menu's act: %+v", a)
	}
}

// A line that is pressed and let go where it went down waits: the next
// press on it within the span of a double-click puts it back, any other
// press puts it where the press is and reaches nothing else, and Escape
// ends the wait and is no key of a program's. The dock's edge is a line
// like any other and folds nothing.
func TestALineClickedAndPressedAgain(t *testing.T) {
	const click, edge = "\x1b[<0;41;11M\x1b[<0;41;11m", "\x1b[<0;15;10M\x1b[<0;15;10m"
	start := func(dock bool) (*In, View) {
		lay := fixture()
		if dock {
			lay.Dock = layout.Dock{On: true, Width: 28}
		}
		return &In{Keys: New(settings(), "darwin")}, View{Lay: lay, Modes: all(screen.Modes{Mouse: 1000, MouseSGR: true})}
	}
	for _, c := range []struct {
		name, bytes, want string
		late, dock        bool
	}{
		{"twice", click + click, "line lit, held | line lit, waits | line back | no line lit", false, false},
		{"twice, too late", click + click, "line lit, held | line lit, waits | line put at 40,10 | no line lit", true, false},
		{"then elsewhere", click + "\x1b[<0;61;11M\x1b[<0;61;11m", "line lit, held | line lit, waits | line put at 60,10 | no line lit", false, false},
		{"then on a tab", click + "\x1b[<0;9;1M", "line lit, held | line lit, waits | line put at 8,0 | no line lit", false, false},
		{"then the right button on a tab", click + "\x1b[<2;9;1M", "line lit, held | line lit, waits | line put at 8,0 | no line lit", false, false},
		{"then another line", edge + click, "dock's edge lit, held | dock's edge lit, waits | dock's edge put at 40,10 | no line lit", false, true},
		{"then Escape", click + "\x1b", "line lit, held | line lit, waits | no line lit", false, false},
		{"then a key", click + "a", `line lit, held | line lit, waits | p1 <- "a"`, false, false},
		{"the wheel leaves it waiting", click + "\x1b[<65;6;1M", "line lit, held | line lit, waits | tab row +1", false, false},
		{"the dock's edge twice", edge + edge, "dock's edge lit, held | dock's edge lit, waits | dock's edge back | no line lit", false, true},
		{"the dock's edge twice, and dragged on", edge + "\x1b[<0;15;10M\x1b[<32;20;10M\x1b[<0;20;10m",
			"dock's edge lit, held | dock's edge lit, waits | dock's edge back | no line lit", false, true},
		{"a drag does not wait", "\x1b[<0;41;11M\x1b[<32;44;11M\x1b[<0;44;11m\x1b[<0;61;11M",
			`line lit, held | line to 43,10 | no line lit | call {"op":"pane-focus","pane":"p2"} | p2 <- "\x1b[<0;20;9M"`, false, false},
		{"a drag back to where it began does not wait", "\x1b[<0;41;11M\x1b[<32;44;11M\x1b[<32;41;11M\x1b[<0;41;11m\x1b[<0;41;11M",
			"line lit, held | line to 43,10 | line to 40,10 | no line lit | line lit, held", false, false},
	} {
		in, v := start(c.dock)
		var acts []Act
		for _, ev := range read(c.bytes) {
			if c.late && ev.Action == term.Press && in.put.Kind != 0 {
				in.at = in.at.Add(-Twice)
			}
			acts = append(acts, in.Take(ev, v)...)
		}
		if got := shown(acts); got != c.want {
			t.Errorf("%s: %s", c.name, got)
		}
		if in.put.Kind != 0 != (c.name == "the wheel leaves it waiting" || c.name == "then a key") {
			t.Errorf("%s: the line waits still: %v", c.name, in.put.Kind != 0)
		}
	}
	// What is put and put back is the line that was clicked: the keeper
	// moves it by the act alone.
	in, v := start(false)
	for _, a := range take(in, v, click+"\x1b[<0;61;11M") {
		if a.Kind == Put {
			v.Lay.Drag(a.Hit, a.X, a.Y)
		}
	}
	if v.Lay.At(60, 10).Kind != layout.OnLine || v.Lay.At(40, 10).Kind != layout.OnPane {
		t.Errorf("after the put the line is not at the press: %+v, %+v", v.Lay.At(60, 10), v.Lay.At(40, 10))
	}
	if a := take(in, v, "\x1b[<0;61;11M\x1b[<0;61;11m\x1b[<0;61;11M"); len(a) != 4 || a[2].Kind != Reset || a[2].Hit != v.Lay.At(60, 10) {
		t.Errorf("the line put back is not the one clicked: %+v", a)
	}
	if Twice.Milliseconds() != 500 {
		t.Errorf("a double-click is two presses within %v", Twice)
	}
}

// The line the pointer comes onto with no button down is lit and the keeper
// is told once, again when the pointer leaves it or comes onto another; a
// press holds it; a movement with a button down, a press and a release
// light nothing under the pointer, so a terminal that reports no movement
// leaves no line lit behind it; and under a pane drawn over the tab no
// line is lit at all.
func TestTheLineUnderThePointer(t *testing.T) {
	lay := fixture()
	lay.Dock = layout.Dock{On: true, Width: 28}
	in, v := In{Keys: New(settings(), "darwin")}, View{Lay: lay, Modes: all(screen.Modes{})}
	for _, c := range [][2]string{
		{"\x1b[<35;48;6M", "line lit, under the pointer"},
		{"\x1b[<35;48;7M\x1b[<35;48;20M", "nothing"},
		{"\x1b[<35;15;7M", "dock's edge lit, under the pointer"},
		{"\x1b[<35;15;8M", "nothing"},
		{"\x1b[<35;30;8M", "no line lit"},
		{"\x1b[<35;31;8M\x1b[<35;4;1M", "nothing"},
		{"\x1b[<35;48;6M\x1b[<0;48;6M", "line lit, under the pointer | line lit, held"},
		{"\x1b[<32;51;6M\x1b[<32;52;6M", "line to 50,5 | line to 51,5"},
		{"\x1b[<0;52;6m", "no line lit"},
		{"\x1b[<0;15;7M\x1b[<32;17;7M\x1b[<0;17;7m", "dock's edge lit, held | line to 16,6 | width | no line lit"},
		{"\x1b[<0;30;8M\x1b[<32;48;6M\x1b[<0;48;6m", "select in p1: press at 29,7 | select in p1: move at 47,5 | select in p1: release at 47,5"},
		{"\x1b[<35;15;7M\x1b[<64;15;7M", "dock's edge lit, under the pointer | no line lit"},
	} {
		if got := shown(take(&in, v, c[0])); got != c[1] {
			t.Errorf("%q: %s", c[0], got)
		}
	}
	take(&in, v, "\x1b[<35;15;7M")
	v.Over = "p2"
	if got := shown(take(&in, v, "\x1b[<35;15;8M\x1b[<35;48;6M")); got != "no line lit" {
		t.Errorf("under a pane over the tab: %s", got)
	}
	// A Lit names the line and how; no line is a Hit of no kind.
	v.Over = ""
	a := take(&in, v, "\x1b[<35;15;7M\x1b[<35;30;7M")
	if len(a) != 2 || a[0].Kind != Lit || a[0].Hit != (layout.Hit{Kind: layout.OnLine, Pane: layout.DockPane}) || a[0].Step != Under ||
		a[1].Kind != Lit || a[1].Hit != (layout.Hit{}) || a[1].Step != Under {
		t.Errorf("the acts: %+v", a)
	}
	if a := take(&in, v, "\x1b[<0;15;7M\x1b[<0;15;7m"); len(a) != 2 || a[0].Step != Held || a[1].Step != Waits || a[1].Hit != a[0].Hit {
		t.Errorf("held and waiting: %+v", a)
	}
}

// A press beside a pane drawn over the tab is one act and nothing else: no
// menu, no line held or put, no tab shown, no keys moved.
func TestAPressBesideThePaneOverTheTab(t *testing.T) {
	in := In{Keys: New(settings(), "darwin")}
	v := View{Lay: fixture(), Modes: all(screen.Modes{Mouse: 1000, MouseSGR: true})}
	take(&in, v, "\x1b[<0;41;11M\x1b[<0;41;11m") // a line waits when the pane opens
	v.Over = "p2"
	for _, bytes := range []string{"\x1b[<2;9;1M", "\x1b[<0;9;1M", "\x1b[<0;41;11M", "\x1b[<0;6;6M", "\x1b[<1;6;6M", "\x1b[<2;6;6M", "\x1b[<0;31;2M"} {
		a := take(&in, v, bytes+"\x1b[<0;6;6m")
		if len(a) != 1 || a[0].Kind != Away || a[0].Call.Op != "" || a[0].Pane != "" || in.to != 0 {
			t.Errorf("%q beside it: %s", bytes, shown(a))
		}
	}
	if got := shown(take(&in, v, "\x1b[<0;51;11M")); got != `p2 <- "\x1b[<0;10;9M"` || in.put.Kind == 0 {
		t.Errorf("a press in it: %s", got)
	}
}

// With two tab lines the wheel over a line moves that line, the command key
// then n and p walk the line the tab that shows is on, and a digit is a tab
// of the second line, also while the workspace's first tab shows and that
// line is dim.
func TestTwoTabLinesUnderTheKeysAndTheWheel(t *testing.T) {
	lay := &layout.Layout{}
	for i, c := range [][2]string{{"Orchestrator", ""}, {"billing", "billing"}, {"invoices", "billing"}, {"search", "search"}, {"index", "search"}} {
		id := string(rune('1' + i))
		tab := lay.Add("t"+id, c[0], "p"+id)
		tab.Group, tab.Head = c[1], c[0] == c[1]
	}
	lay.Fit(80, 24)
	lay.Show("t5")
	lay.Show("t1")
	in, v := In{Keys: New(settings(), "darwin")}, View{Lay: lay, Modes: all(screen.Modes{})}
	focus := func(tab string) string { return `call {"op":"tab-focus","tab":"` + tab + `"}` }
	for _, c := range [][2]string{
		{"\x1b[<65;30;1M", "tab row +1"}, {"\x1b[<64;30;2M", "tab row -" + strconv.Itoa(layout.Lower)}, {"\x1b[<65;30;3M", "nothing"},
		{"\x002", focus("t5")}, {"\x003", "nothing"}, {"\x00n", focus("t2")}, {"\x00p", focus("t5")},
	} {
		if got := shown(take(&in, v, c[0])); got != c[1] {
			t.Errorf("%q with the first tab in front: %s, want %s", c[0], got, c[1])
		}
	}
	lay.Show("t3")
	for _, c := range [][2]string{{"\x001", focus("t2")}, {"\x00n", focus("t2")}, {"\x00p", focus("t2")}} {
		if got := shown(take(&in, v, c[0])); got != c[1] {
			t.Errorf("%q with a fleet's tab in front: %s, want %s", c[0], got, c[1])
		}
	}
}
