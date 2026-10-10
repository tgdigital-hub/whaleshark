package input

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/layout"
	"github.com/tgdigital-hub/whaleshark/internal/screen"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

var update = flag.Bool("update", false, "write the files the table is compared with")

// What the program in every pane asked for, in each file of the table.
var tableModes = []struct {
	name string
	m    screen.Modes
}{
	{"plain", screen.Modes{WheelKeys: true}},
	{"cursor-keys", screen.Modes{WheelKeys: true, CursorKeys: true}},
	{"extended-keys", screen.Modes{WheelKeys: true, Keys: 1}},
	{"paste-and-focus", screen.Modes{WheelKeys: true, Paste: true, Focus: true}},
	{"second-screen", screen.Modes{WheelKeys: true, Second: true, CursorKeys: true}},
	{"second-screen-no-wheel", screen.Modes{Second: true}},
	{"mouse-1000", screen.Modes{WheelKeys: true, Mouse: 1000}},
	{"mouse-1000-exact", screen.Modes{WheelKeys: true, Mouse: 1000, MouseSGR: true}},
	{"mouse-1002-exact", screen.Modes{WheelKeys: true, Mouse: 1002, MouseSGR: true}},
	{"mouse-1003", screen.Modes{WheelKeys: true, Mouse: 1003}},
	{"mouse-1003-exact", screen.Modes{WheelKeys: true, Mouse: 1003, MouseSGR: true}},
	{"full-screen-agent", screen.Modes{Second: true, Mouse: 1003, MouseSGR: true, Paste: true, Focus: true, Keys: 1}},
}

// fixture is the layout every case of the table starts from.
func fixture() *layout.Layout {
	lay := &layout.Layout{}
	lay.Add("t1", "one", "p1")
	lay.Fit(80, 24)
	if err := lay.Split("p1", contract.Right, 0.5, "p2"); err != nil {
		panic(err)
	}
	lay.Add("t2", "two", "p3")
	lay.Show("t1")
	lay.Focus("p1")
	return lay
}

// settings holds the shortcuts the action table names, as init writes them.
func settings() contract.Settings {
	s, _ := contract.ReadSettings(func(string) ([]byte, error) { return nil, os.ErrNotExist }, contract.Dirs{})
	for _, a := range contract.Actions {
		if a.Prefix == "" {
			continue
		}
		e := contract.KeyEntry{Key: a.Prefix, Type: "shell", Argv: append([]string{"whaleshark"}, a.Run...)}
		if a.ID == "menu" || a.ID == "catchup" {
			e.Type = "popup"
		}
		s.Shortcuts = append(s.Shortcuts, e)
	}
	return s
}

func all(m screen.Modes) func(string) screen.Modes {
	return func(string) screen.Modes { return m }
}

// read is what term's parser makes of bytes, a lone Escape settled.
func read(b string) []term.Event {
	var p term.Parser
	return append(p.Feed([]byte(b)), p.Flush()...)
}

func show(a Act) string {
	switch a.Kind {
	case Type:
		return fmt.Sprintf("%s <- %q", a.Pane, a.Text)
	case Call:
		b, _ := json.Marshal(a.Call)
		return "call " + string(b)
	case Row:
		return fmt.Sprintf("tab row %+d", a.Step)
	case Drag:
		return fmt.Sprintf("line to %d,%d", a.X, a.Y)
	case Run, Popup:
		return fmt.Sprintf("%s %q for %s", map[Kind]string{Run: "run", Popup: "popup"}[a.Kind], a.Argv, a.Pane)
	case Scroll:
		return fmt.Sprintf("scroll %s %+d", a.Pane, a.Step)
	case Select:
		return fmt.Sprintf("select in %s: %s at %d,%d", a.Pane,
			map[term.Action]string{term.Press: "press", term.Move: "move", term.Release: "release"}[a.Action], a.X, a.Y)
	}
	return map[Kind]string{Zoom: "zoom ", Redraw: "redraw ", Mark: "mark in ", Copy: "copy from "}[a.Kind] + a.Pane
}

func shown(acts []Act) string {
	if len(acts) == 0 {
		return "nothing"
	}
	var s []string
	for _, a := range acts {
		s = append(s, show(a))
	}
	return strings.Join(s, " | ")
}

// cases are the lines of the table that are a case.
func cases(t *testing.T) []string {
	data, err := os.ReadFile(filepath.Join("testdata", "events.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" && line[0] != '#' {
			out = append(out, line)
		}
	}
	return out
}

func unquote(t *testing.T, line string) string {
	s, err := strconv.Unquote(`"` + line + `"`)
	if err != nil {
		t.Fatalf("%s: %v", line, err)
	}
	return s
}

// The row's check: more than two hundred key and mouse events, each routed
// with the panes' programs in each mode, compared with a file a mode.
func TestTheTableInEachMode(t *testing.T) {
	lines := cases(t)
	if len(lines) < 200 {
		t.Fatalf("the table has %d cases, and two hundred are asked for", len(lines))
	}
	keys := New(settings(), "darwin")
	for _, mode := range tableModes {
		var b strings.Builder
		for _, line := range lines {
			in, lay := In{Keys: keys}, fixture()
			var acts []Act
			for _, ev := range read(unquote(t, line)) {
				acts = append(acts, in.Take(ev, View{Lay: lay, Modes: all(mode.m)})...)
			}
			fmt.Fprintf(&b, "%s => %s\n", line, shown(acts))
		}
		file := filepath.Join("testdata", "table-"+mode.name+".txt")
		if *update {
			if err := os.WriteFile(file, []byte(b.String()), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		got, was := strings.Split(b.String(), "\n"), strings.Split(strings.ReplaceAll(string(want), "\r\n", "\n"), "\n")
		for i := range got {
			if i >= len(was) || got[i] != was[i] {
				t.Errorf("%s, case %d:\n got %s\nwant %s", mode.name, i+1, got[i], strings.Join(was[min(i, len(was)-1):min(i+1, len(was))], ""))
				break
			}
		}
	}
}

// every key the parser can name: each key of both forms with each sum of
// the keys held.
func everyKey() (evs []term.Event) {
	for sum := 1; sum <= 16; sum++ {
		for _, code := range []int{9, 13, 27, 32, 127, '0', '1', '/', '?', '@', '[', '\\', ']', '^', '_', '`', '-', '=', ';', 0xe9, 0x4e16} {
			evs = append(evs, read(fmt.Sprintf("\x1b[%d;%du", code, sum))...)
		}
		for code := 'a'; code <= 'z'; code++ {
			evs = append(evs, read(fmt.Sprintf("\x1b[%d;%du", code, sum))...)
		}
		for _, end := range "ABCDHFPQRS" {
			evs = append(evs, read(fmt.Sprintf("\x1b[1;%d%c", sum, end))...)
		}
		for _, n := range []int{2, 3, 5, 6, 15, 17, 18, 19, 20, 21, 23, 24} {
			evs = append(evs, read(fmt.Sprintf("\x1b[%d;%d~", n, sum))...)
		}
	}
	return evs
}

// What the writer writes, term's parser reads back. In the extended form
// that is the same key, always. In the old form some keys have no bytes of
// their own, so the bytes written must be a key that is written the same way
// again: nothing is made up on the way.
func TestWhatIsWrittenIsReadBack(t *testing.T) {
	evs := everyKey()
	if len(evs) != 16*69 {
		t.Fatalf("%d keys, not every one", len(evs))
	}
	for _, ev := range evs {
		// Shift with Space types a space, and is written as one.
		out := Key(ev, screen.Modes{Keys: 1})
		if back := read(out); (ev.Key != "shift+space" || out != " ") &&
			(len(back) != 1 || canon(back[0].Key) != canon(ev.Key) || back[0].Rune != ev.Rune) {
			t.Errorf("extended: %q written %q, read back %+v", ev.Key, out, back)
		}
		with, key := split(ev.Key)
		for _, m := range []screen.Modes{{}, {CursorKeys: true}} {
			out := Key(ev, m)
			switch {
			case out == "" && (with&super == 0 || ev.Rune != 0):
				t.Errorf("%q is not written at all", ev.Key)
			case out == "":
			case with&alt != 0 && (key == "esc" || key == "[" || with&shift != 0 && (key == "o" || key == "tab")):
				// Escape before Escape, "[" or "O" is the start of another
				// report: the old form cannot say these four.
			case with&ctrl != 0 && key == "h":
				// The parser calls the character 8 Backspace, which is 127 here.
			default:
				if back := read(out); len(back) != 1 || Key(back[0], m) != out {
					t.Errorf("old form: %q written %q, read back %+v", ev.Key, out, back)
				}
			}
		}
	}
	exact := screen.Modes{Mouse: 1003, MouseSGR: true, Paste: true, Focus: true}
	for _, ev := range []term.Event{
		{Button: term.Left, Action: term.Press}, {Button: term.Left, Action: term.Release}, {Button: term.Left, Action: term.Move},
		{Button: term.Middle, Action: term.Press}, {Button: term.Middle, Action: term.Release}, {Button: term.Middle, Action: term.Move},
		{Button: term.NoButton, Action: term.Move}, {Button: term.WheelUp, Action: term.Press}, {Button: term.WheelDown, Action: term.Press},
	} {
		for held := 0; held < 8; held++ {
			ev.Kind, ev.Shift, ev.Alt, ev.Ctrl = term.Mouse, held&1 != 0, held&2 != 0, held&4 != 0
			for _, at := range [][2]int{{0, 0}, {7, 3}, {222, 222}, {999, 999}} {
				want := ev
				want.X, want.Y = at[0], at[1]
				if back := read(Mouse(ev, exact, at[0], at[1])); len(back) != 1 || back[0] != want {
					t.Errorf("mouse %+v read back %+v", want, back)
				}
				// The older form says the same in three bytes, up to cell 222.
				old := Mouse(ev, screen.Modes{Mouse: 1003}, at[0], at[1])
				code := 0
				if len(old) == 6 {
					code = int(old[3]) - 32
				}
				switch far := at[0] > 222; {
				case far && old != "", !far && (len(old) != 6 || old[:3] != "\x1b[M" || int(old[4])-33 != at[0] || int(old[5])-33 != at[1]):
					t.Errorf("mouse %+v in the older form: %q", want, old)
				case !far && (code&4 != 0) != ev.Shift, !far && (code&8 != 0) != ev.Alt, !far && (code&16 != 0) != ev.Ctrl,
					!far && (code&32 != 0) != (ev.Action == term.Move), !far && ev.Action == term.Release && code&3 != 3:
					t.Errorf("mouse %+v in the older form: code %d", want, code)
				}
			}
		}
	}
	for _, text := range []string{"hello", "one\ntwo\r\nthree", "\ttab, é and 世", " "} {
		if back := read(Paste(text, exact)); len(back) != 1 || back[0].Kind != term.Paste || back[0].Text != text {
			t.Errorf("paste %q read back %+v", text, back)
		}
	}
	for _, on := range []bool{true, false} {
		if back := read(Focus(on, exact)); len(back) != 1 || back[0].Kind != term.Focus || back[0].Focused != on {
			t.Errorf("focus %v read back %+v", on, back)
		}
	}
}

// Pasted text can end no paste and press no key: whatever is pasted, the
// program reads one paste, or with no marks only characters and Enter.
func TestAPasteCannotBreakOut(t *testing.T) {
	esc := "\x1b"
	for _, text := range []string{esc + "[201~rm -rf x\r", "a" + esc + "[201~" + esc + "[200~b", "\x03\x04\x1a", "x\u009b201~y", esc + esc + "[201~"} {
		back := read(Paste(text, screen.Modes{Paste: true}))
		if len(back) > 1 || len(back) == 1 && (back[0].Kind != term.Paste || strings.ContainsAny(back[0].Text, "\x1b\x03\x04\x1a\u009b")) {
			t.Errorf("%q between marks reads as %+v", text, back)
		}
		for _, ev := range read(Paste(text, screen.Modes{})) {
			if ev.Rune == 0 && ev.Key != "enter" {
				t.Errorf("%q with no marks presses %q", text, ev.Key)
			}
		}
	}
	if got := Paste("a\r\nb\nc", screen.Modes{}); got != "a\rb\rc" {
		t.Errorf("line breaks with no marks: %q", got)
	}
}

func take(in *In, v View, bytes string) (acts []Act) {
	for _, ev := range read(bytes) {
		acts = append(acts, in.Take(ev, v)...)
	}
	return acts
}

// The copy keys: Cmd+C and Ctrl+Shift+C copy what is selected and never
// interrupt; Ctrl+C copies by Windows' rule alone; with nothing selected
// each is the program's key.
func TestTheCopyKeys(t *testing.T) {
	const cmdC, ctrlShiftC, ctrlC = "\x1b[99;9u", "\x1b[99;6u", "\x03"
	for _, c := range []struct {
		style, system, key string
		selected           bool
		keys               int
		want               string
	}{
		{"auto", "darwin", cmdC, true, 0, "copy from p1"},
		{"auto", "darwin", ctrlC, true, 0, `p1 <- "\x03"`},
		{"auto", "darwin", cmdC, false, 0, "nothing"},
		{"auto", "darwin", cmdC, false, 1, `p1 <- "\x1b[99;9u"`},
		{"auto", "windows", ctrlC, true, 0, "copy from p1"},
		{"auto", "windows", ctrlC, false, 0, `p1 <- "\x03"`},
		{"auto", "linux", ctrlShiftC, true, 0, "copy from p1"},
		{"auto", "linux", ctrlC, true, 0, `p1 <- "\x03"`},
		{"auto", "linux", ctrlShiftC, false, 1, `p1 <- "\x1b[99;6u"`},
		// Through a bare ssh nobody knows the system: Ctrl+C interrupts.
		{"auto", "", ctrlC, true, 0, `p1 <- "\x03"`},
		{"auto", "", cmdC, true, 0, "copy from p1"},
		{"windows", "darwin", ctrlC, true, 0, "copy from p1"},
		{"mac", "windows", ctrlC, true, 0, `p1 <- "\x03"`},
		{"linux", "windows", ctrlC, true, 0, `p1 <- "\x03"`},
	} {
		s := settings()
		s.Keys.Style = c.style
		in := In{Keys: New(s, c.system)}
		v := View{Lay: fixture(), Selected: c.selected, Modes: all(screen.Modes{Keys: c.keys})}
		if got := shown(take(&in, v, c.key)); got != c.want {
			t.Errorf("%+v: %s", c, got)
		}
	}
}

// The person's own settings: another command key, and a shortcut of theirs
// in the place of one of ours.
func TestTheSettingsDecide(t *testing.T) {
	s := settings()
	s.Keys.Command = "ctrl+b"
	s.Shortcuts = append(s.Shortcuts, contract.KeyEntry{Key: "t", Type: "shell", Argv: []string{"date"}},
		contract.KeyEntry{Key: "shift+f5", Type: "popup", Argv: []string{"top"}})
	in, v := In{Keys: New(s, "linux")}, View{Lay: fixture(), Modes: all(screen.Modes{})}
	for bytes, want := range map[string]string{
		"\x02t":           `run ["date"] for p1`,
		"\x02\x1b[15;2~":  `popup ["top"] for p1`,
		"\x02w":           `call {"op":"pane-close","pane":"p1"}`,
		"\x00t":           `p1 <- "\x00" | p1 <- "t"`,
		"\x02\x02":        `p1 <- "\x02"`,
		"\x02\x1b[98;5ux": `p1 <- "\x02" | p1 <- "x"`,
	} {
		if got := shown(take(&in, v, bytes)); got != want || in.Armed {
			t.Errorf("%q: %s (armed %v)", bytes, got, in.Armed)
		}
	}
	if take(&in, v, "\x02"); !in.Armed {
		t.Error("the command key did not arm")
	}
}

// An overlay takes every key, the command key too, and every click; a
// click beside it does nothing.
func TestAnOverlayTakesEverything(t *testing.T) {
	lay := fixture()
	in := In{Keys: New(settings(), "darwin")}
	v := View{Lay: lay, Over: "p2", Selected: true, Modes: all(screen.Modes{Mouse: 1000, MouseSGR: true, Keys: 1})}
	for bytes, want := range map[string]string{
		"\x00t":                            `p2 <- "\x1b[32;5u" | p2 <- "t"`,
		"\x1b[99;9u":                       `p2 <- "\x1b[99;9u"`,
		"\x1b[200~hi\x1b[201~":             `p2 <- "hi"`,
		"\x1b[<0;51;11M\x1b[<0;51;11m":     `p2 <- "\x1b[<0;10;9M" | p2 <- "\x1b[<0;10;9m"`,
		"\x1b[<0;6;6M\x1b[<0;6;6m":         "nothing",
		"\x1b[<0;9;1M":                     "nothing",
		"\x1b[<64;6;1M\x1b[<65;6;6M":       "nothing",
		"\x1b[<0;41;11M\x1b[<32;31;11M":    "nothing",
		"\x1b[<64;51;11M":                  `p2 <- "\x1b[<64;10;9M"`,
		"\x1b[<0;51;11M\x1b[<0;6;6m":       `p2 <- "\x1b[<0;10;9M" | p2 <- "\x1b[<0;10;9m"`,
		"\x1b[<35;6;6M\x1b[<35;51;11M\x00": `p2 <- "\x1b[32;5u"`,
	} {
		if got := shown(take(&in, v, bytes)); got != want || in.Armed {
			t.Errorf("%q: %s", bytes, got)
		}
	}
}

// With no tab at all a key belongs to nobody, and a new tab can be asked for.
func TestWithNoTab(t *testing.T) {
	in, v := In{Keys: New(settings(), "darwin")}, View{Lay: &layout.Layout{W: 80, H: 24}, Modes: all(screen.Modes{})}
	for bytes, want := range map[string]string{
		"a\r\x1b[A":                "nothing",
		"\x00w":                    "nothing",
		"\x00n":                    "nothing",
		"\x001":                    "nothing",
		"\x00z":                    "nothing",
		"\x00t":                    `call {"op":"tab-create"}`,
		"\x00m":                    `run ["whaleshark" "set" "mute" "{value}"] for `,
		"\x1b[<0;6;6M\x1b[<0;6;6m": "nothing",
		"\x1b[<64;6;1M":            "tab row -1",
		"\x1b[200~hi\x1b[201~":     "nothing",
	} {
		if got := shown(take(&in, v, bytes)); got != want {
			t.Errorf("%q: %s", bytes, got)
		}
	}
}

// A pane zoomed to its tab gets every click in the tab, and the tab keys
// go round the tabs.
func TestZoomAndTheTabKeys(t *testing.T) {
	lay := fixture()
	lay.Zoom("p2")
	in, v := In{Keys: New(settings(), "darwin")}, View{Lay: lay, Modes: all(screen.Modes{Mouse: 1000, MouseSGR: true})}
	if got := shown(take(&in, v, "\x1b[<0;6;6M")); got != `p2 <- "\x1b[<0;6;4M"` {
		t.Error(got)
	}
	lay.Show("t2")
	for bytes, want := range map[string]string{"\x00n": "t1", "\x00p": "t1", "\x001": "t1", "\x002": "t2"} {
		if got := shown(take(&in, v, bytes)); got != `call {"op":"tab-focus","tab":"`+want+`"}` {
			t.Errorf("%q: %s", bytes, got)
		}
	}
}

// Whatever a terminal sends, routing fails on nothing, types only into a
// pane that is there, and leaves no button held that was let go.
func TestRandomInput(t *testing.T) {
	pieces := append(cases(t), `\x1b`, `[`, `<`, `;`, `M`, `m`, `u`, `~`, `0`, `9`, `\x00`, `\xff`)
	seed := rand.Uint64()
	r := rand.New(rand.NewPCG(seed, 1))
	lay := fixture()
	in := In{Keys: New(settings(), "windows")}
	for n := 0; n < 20000; n++ {
		m := tableModes[r.IntN(len(tableModes))].m
		v := View{Lay: lay, Selected: r.IntN(2) == 0, Modes: all(m)}
		var bytes string
		for i := r.IntN(4); i >= 0; i-- {
			bytes += unquote(t, pieces[r.IntN(len(pieces))])
		}
		for _, ev := range read(bytes) {
			for _, a := range in.Take(ev, v) {
				switch a.Kind {
				case Type:
					if !slices.Contains([]string{"p1", "p2", "p3"}, a.Pane) || a.Text == "" {
						t.Fatalf("seed %d: %q typed %q into %q", seed, bytes, a.Text, a.Pane)
					}
				case Drag:
					lay.Drag(a.Hit, a.X, a.Y)
				case Call:
					if a.Call.Op == contract.OpTabFocus {
						lay.Show(a.Call.Tab)
					} else if a.Call.Op == contract.OpPaneFocus && a.Call.Dir == "" {
						lay.Focus(a.Call.Pane)
					}
				}
			}
			if ev.Kind == term.Mouse && ev.Action == term.Release && in.to != 0 {
				t.Fatalf("seed %d: %q left a button held", seed, bytes)
			}
		}
	}
}

func FuzzTake(f *testing.F) {
	f.Add([]byte("\x00t\x1b[<0;6;6M\x1b[<32;46;6M\x1b[<0;46;6m\x1b[13;2u"), uint8(11))
	keys := New(settings(), "windows")
	f.Fuzz(func(t *testing.T, b []byte, mode uint8) {
		in, lay := In{Keys: keys}, fixture()
		v := View{Lay: lay, Selected: mode&128 != 0, Modes: all(tableModes[int(mode&127)%len(tableModes)].m)}
		for _, ev := range read(string(b)) {
			for _, a := range in.Take(ev, v) {
				if a.Kind == Drag {
					lay.Drag(a.Hit, a.X, a.Y)
				}
			}
		}
	})
}
