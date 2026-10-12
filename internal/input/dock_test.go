package input

import (
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/layout"
	"github.com/tgdigital-hub/whaleshark/internal/screen"
)

// The dock's three keys after the command key, and a click in it: b folds
// it, g gives it the keys, s opens the settings screen; a press in the dock
// goes to its program and moves no keys, where a press in a pane without
// the keys moves them first; the end of a drag of its edge writes the width.
func TestTheDocksKeysAndAClickInIt(t *testing.T) {
	lay := fixture()
	lay.Dock = layout.Dock{On: true, Width: 28} // 14 columns of a window of 80, its edge the fifteenth
	in := In{Keys: New(settings(), "darwin")}
	v := View{Lay: lay, Modes: all(screen.Modes{Mouse: 1000, MouseSGR: true})}
	for _, c := range [][2]string{
		{"\x00b", `call {"op":"dock","pane":"p1","dir":"fold"}`},
		{"\x00g", `call {"op":"dock","pane":"p1","dir":"keys"}`},
		{"\x00s", "settings from p1"},
		{"a", `p1 <- "a"`},
		{"\x1b[<0;4;6M\x1b[<0;4;6m", `dock <- "\x1b[<0;4;6M" | dock <- "\x1b[<0;4;6m"`},
		{"\x1b[<64;4;6M", `dock <- "\x1b[<64;4;6M"`},
		{"\x1b[<0;61;11M\x1b[<0;61;11m", `call {"op":"pane-focus","pane":"p2"} | p2 <- "\x1b[<0;13;9M" | p2 <- "\x1b[<0;13;9m"`},
		{"\x1b[<0;15;10M\x1b[<32;31;10M\x1b[<32;33;11M\x1b[<0;33;11m", "line to 30,9 | line to 32,10 | width"},
		{"\x1b[<0;48;6M\x1b[<32;51;6M\x1b[<0;51;6m", "line to 50,5"},
	} {
		if got := shown(take(&in, v, c[0])); got != c[1] || in.Armed {
			t.Errorf("%q: %s", c[0], got)
		}
	}
	// Two presses on the edge in a row fold the dock, and what follows the
	// second moves nothing; a press on a split's line twice folds nothing.
	if got := shown(take(&in, v, "\x1b[<0;15;10M\x1b[<0;15;10m\x1b[<0;15;10M\x1b[<32;20;10M\x1b[<0;20;10m")); got != `width | call {"op":"dock","dir":"fold"}` {
		t.Errorf("two presses on the dock's edge: %s", got)
	}
	if got := shown(take(&in, v, "\x1b[<0;48;6M\x1b[<0;48;6m\x1b[<0;48;6M\x1b[<0;48;6m")); got != "nothing" {
		t.Errorf("two presses on a split's line: %s", got)
	}
	// With the keys in the dock what is typed and pasted is its program's, a
	// shortcut is still about the pane that had the keys, g gives them back,
	// and a press in any pane takes them there.
	lay.Dock.Keys = true
	for _, c := range [][2]string{
		{"a", `dock <- "a"`},
		{"\x1b[200~hi\x1b[201~", `dock <- "hi"`},
		{"\x00g", `call {"op":"dock","pane":"p1","dir":"back"}`},
		{"\x00w", `call {"op":"pane-close","pane":"p1"}`},
		{"\x00z", "zoom p1"},
		{"\x1b[<0;4;6M\x1b[<0;4;6m", `dock <- "\x1b[<0;4;6M" | dock <- "\x1b[<0;4;6m"`},
		{"\x1b[<0;21;11M\x1b[<0;21;11m", `call {"op":"pane-focus","pane":"p1"} | p1 <- "\x1b[<0;6;9M" | p1 <- "\x1b[<0;6;9m"`},
	} {
		if got := shown(take(&in, v, c[0])); got != c[1] || in.Armed {
			t.Errorf("with the keys in the dock, %q: %s", c[0], got)
		}
	}
	// A pane drawn over the tab takes every key from the dock too.
	v.Over = "p2"
	if got := shown(take(&in, v, "a\x00g")); got != `p2 <- "a" | p2 <- "\x00" | p2 <- "g"` {
		t.Errorf("under a pane over the tab: %s", got)
	}
}
