package pick

import (
	"bytes"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/layout"
	"github.com/tgdigital-hub/whaleshark/internal/screen"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

var rounds = flag.Int("rounds", 300, "how many random windows the property test tries")

var t0 = time.Unix(1_700_000_000, 0)

// filled is a screen of w by h that a program printed n lines into, each
// "<name><line number> " over and over to the full width.
func filled(w, h, n int, name string) *screen.Screen {
	s := screen.New(w, h)
	for i := range n {
		line := strings.Repeat(fmt.Sprintf("%s%d ", name, i), w)[:w-1]
		if i > 0 {
			line = "\r\n" + line
		}
		io.WriteString(s, line)
	}
	return s
}

func mouse(b term.Button, a term.Action, x, y int) term.Event {
	return term.Event{Kind: term.Mouse, Button: b, Action: a, X: x, Y: y}
}

func key(name string) term.Event { return term.Event{Kind: term.KeyPress, Key: name} }

// drag presses the left button at one cell of the window, moves through the
// others and lets go at the last; it returns what was copied.
func drag(v *View, at layout.Rect, s *screen.Screen, cells ...[2]int) string {
	v.Mouse(mouse(term.Left, term.Press, cells[0][0], cells[0][1]), at, s, t0)
	for _, c := range cells[1:] {
		v.Mouse(mouse(term.Left, term.Move, c[0], c[1]), at, s, t0)
	}
	last := cells[len(cells)-1]
	return v.Mouse(mouse(term.Left, term.Release, last[0], last[1]), at, s, t0).Copy
}

// The row's check: whatever a person does with the mouse or the keys in
// one pane, in whatever layout, nothing is drawn outside that pane's place
// that was not there before, and nothing is copied that the pane does not hold.
func TestASelectionNeverLeavesItsPane(t *testing.T) {
	seed := time.Now().UnixNano()
	for round := range *rounds {
		r := rand.New(rand.NewSource(seed + int64(round)))
		fail := func(format string, a ...any) {
			t.Helper()
			t.Fatalf("seed %d round %d: %s", seed, round, fmt.Sprintf(format, a...))
		}
		W, H := 30+r.Intn(120), 10+r.Intn(40)
		lay := &layout.Layout{}
		tab := lay.Add("t1", "", "a")
		lay.Fit(W, H)
		for i := 1; i < 2+r.Intn(5); i++ {
			panes := tab.Panes()
			dir := []string{"right", "down", "left", "up"}[r.Intn(4)]
			lay.Split(panes[r.Intn(len(panes))], dir, 0.2+0.6*r.Float64(), string(rune('a'+i)))
		}
		places := lay.Places(tab)
		screens, views := map[string]*screen.Screen{}, map[string]*View{}
		for _, at := range places {
			screens[at.Pane] = filled(max(at.W, 1), max(at.H, 1), r.Intn(3*H), at.Pane)
			views[at.Pane] = &View{}
		}
		draw := func() *term.Grid {
			g := term.NewGrid(W, H)
			for _, at := range places {
				views[at.Pane].Paint(g, at.Rect, screens[at.Pane], term.Style{})
			}
			return g
		}
		before := draw()
		at := places[r.Intn(len(places))]
		if at.W < 1 || at.H < 1 {
			continue
		}
		v, s := views[at.Pane], screens[at.Pane]
		anywhere := func() (int, int) { return r.Intn(W+10) - 5, r.Intn(H+10) - 5 }
		check := func(what string, d Did) {
			t.Helper()
			after := draw()
			for y := range H {
				for x := range W {
					in := x >= at.X && x < at.X+at.W && y >= at.Y && y < at.Y+at.H
					if !in && after.At(x, y) != before.At(x, y) {
						fail("%s: the cell %d,%d outside pane %s at %+v changed from %+v to %+v", what, x, y, at.Pane, at.Rect, before.At(x, y), after.At(x, y))
					}
				}
			}
			if i := strings.IndexFunc(d.Copy, func(c rune) bool {
				return !strings.ContainsRune(at.Pane+" \n0123456789", c)
			}); i >= 0 {
				fail("%s: copied %q, which pane %s does not hold", what, d.Copy, at.Pane)
			}
			pic := s.Picture()
			for _, p := range []point{v.a, v.b} {
				if p.x < 0 || p.x >= pic.W || p.line < 0 || p.line >= s.Lines()+pic.H {
					fail("%s: an end of the selection is at %+v, outside a pane of %d by %d with %d lines above", what, p, pic.W, pic.H, s.Lines())
				}
			}
		}
		for range 3 {
			for range 1 + r.Intn(3) {
				x, y := at.X+r.Intn(at.W), at.Y+r.Intn(at.H)
				check("press", v.Mouse(mouse(term.Left, term.Press, x, y), at.Rect, s, t0))
			}
			for range r.Intn(30) {
				x, y := anywhere()
				b := []term.Button{term.Left, term.Left, term.Left, term.WheelUp, term.WheelDown}[r.Intn(5)]
				a := term.Move
				if b != term.Left {
					a = term.Press
				}
				check("move", v.Mouse(mouse(b, a, x, y), at.Rect, s, t0))
			}
			x, y := anywhere()
			check("release", v.Mouse(mouse(term.Left, term.Release, x, y), at.Rect, s, t0))
			v.Select(s)
			names := strings.Fields("left right up down h j k l pageup pagedown home end 0 $ v space")
			for range r.Intn(40) {
				check("key", v.Key(key(names[r.Intn(len(names))]), s, false))
			}
			check("copy key", v.Key(key("enter"), s, false))
		}
	}
}

func TestTheWheelAndTheScrollBackView(t *testing.T) {
	at := layout.Rect{X: 4, Y: 2, W: 20, H: 5}
	s, v, g := filled(20, 5, 30, "n"), &View{}, term.NewGrid(30, 10)
	wheel := func(b term.Button) Did { return v.Mouse(mouse(b, term.Press, 6, 3), at, s, t0) }
	row := func(y int) string {
		v.Paint(g, at, s, term.Style{})
		return strings.TrimRight(g.Row(y)[at.X:], " ")
	}
	if got := row(2); !strings.HasPrefix(got, "n25 ") {
		t.Fatalf("the live picture starts %q", got)
	}
	if d := wheel(term.WheelUp); !d.Used || d.Type != "" || v.Back != 3 {
		t.Fatalf("a wheel up gave %+v and %d lines back", d, v.Back)
	}
	if got := row(2); !strings.HasPrefix(got, "n22 ") || !strings.HasSuffix(got, " 3 back") {
		t.Fatalf("three lines back, with the mark in the corner, is %q", got)
	}
	if got := row(6); !strings.HasPrefix(got, "n26 ") {
		t.Fatalf("the last row three lines back is %q", got)
	}
	// What the program prints meanwhile does not move the lines read.
	io.WriteString(s, "\r\nnew\r\nnewer")
	if got := row(2); !strings.HasPrefix(got, "n22 ") || !strings.HasSuffix(got, " 5 back") {
		t.Fatalf("after two more lines the view is %q", got)
	}
	for range 20 {
		wheel(term.WheelUp)
	}
	if got := row(2); v.Back != s.Lines() || !strings.HasPrefix(got, "n0 ") {
		t.Fatalf("the wheel goes back to the oldest line and no further: %d of %d, %q", v.Back, s.Lines(), got)
	}
	for range 20 {
		wheel(term.WheelDown)
	}
	if got := row(6); v.Back != 0 || got != "newer" {
		t.Fatalf("the wheel at the bottom is the live picture: %d back, %q", v.Back, got)
	}
	wheel(term.WheelUp)
	if d := v.Key(key("x"), s, false); d.Used || v.Back != 0 {
		t.Fatalf("any key is the program's and brings the live picture back: %+v, %d back", d, v.Back)
	}

	// On the second screen the wheel is arrow keys, in the form asked for.
	io.WriteString(s, "\x1b[?1049h")
	if d := wheel(term.WheelUp); !d.Used || d.Type != strings.Repeat("\x1b[A", 3) || v.Back != 0 {
		t.Fatalf("the wheel on the second screen gave %q", d.Type)
	}
	io.WriteString(s, "\x1b[?1h")
	if d := wheel(term.WheelDown); d.Type != strings.Repeat("\x1bOB", 3) {
		t.Fatalf("with the cursor keys in their other form the wheel gave %q", d.Type)
	}
	io.WriteString(s, "\x1b[?1007l")
	if d := wheel(term.WheelDown); !d.Used || d.Type != "" {
		t.Fatalf("a program that takes no arrows for the wheel got %q", d.Type)
	}
	// A program that asked for the mouse gets the wheel and the button.
	io.WriteString(s, "\x1b[?1049l\x1b[?1000h")
	if d := wheel(term.WheelUp); d.Used || v.Back != 0 {
		t.Fatalf("a program with the mouse lost the wheel: %+v", d)
	}
	if got := drag(v, at, s, [2]int{5, 3}, [2]int{9, 4}); got != "" || v.on {
		t.Fatalf("a program with the mouse lost a drag: %q", got)
	}
}

func TestSelectingWithTheMouse(t *testing.T) {
	at := layout.Rect{X: 10, Y: 3, W: 16, H: 4}
	s, v := screen.New(16, 4), &View{}
	io.WriteString(s, "old line one\r\nold line two\r\nalpha beta\r\n  gamma/d.go x\r\n界界 wide\r\nlast")
	if got := drag(v, at, s, [2]int{16, 3}, [2]int{12, 4}); got != "beta\n  g" {
		t.Fatalf("a drag copied %q", got)
	}
	// Backwards, and from outside the pane on both sides: the nearest cell.
	if got := drag(v, at, s, [2]int{40, 4}, [2]int{0, 3}); got != "alpha beta\n  gamma/d.go x" {
		t.Fatalf("a drag from past the right edge to past the left one copied %q", got)
	}
	g := term.NewGrid(40, 10)
	v.Paint(g, at, s, term.Style{Fg: term.RGB(0x3fd8f0)})
	for y := range g.H {
		for x := range g.W {
			in := x >= at.X && x < at.X+at.W && (y == 3 || y == 4)
			if c := g.At(x, y); c.Style.Reverse != in || in && c.Style.Fg != term.RGB(0x3fd8f0) {
				t.Fatalf("the cell %d,%d is drawn %+v", x, y, c)
			}
		}
	}
	if d := v.Mouse(mouse(term.Left, term.Press, 20, 5), at, s, t0); !d.Used || v.on {
		t.Fatalf("a click drops the selection: %+v", d)
	}
	// The right half of a wide character is the character.
	if got := drag(v, at, s, [2]int{13, 5}, [2]int{15, 5}); got != "界 w" {
		t.Fatalf("a drag from the middle of a wide character copied %q", got)
	}
	click := func(x, y int, after time.Duration) string {
		v.Mouse(mouse(term.Left, term.Press, x, y), at, s, t0.Add(after))
		return v.Mouse(mouse(term.Left, term.Release, x, y), at, s, t0.Add(after)).Copy
	}
	if a, b, c := click(14, 4, time.Hour), click(14, 4, time.Hour+again/2), click(14, 4, time.Hour+again); a != "" || b != "gamma/d.go" || c != "  gamma/d.go x" {
		t.Fatalf("one, two and three clicks copied %q, %q, %q", a, b, c)
	}
	if got := click(14, 4, 2*time.Hour); got != "" {
		t.Fatalf("a click long after is one click, and copied %q", got)
	}
	// Dragging past the top scrolls back a line with each report.
	if got := drag(v, at, s, [2]int{12, 3}, [2]int{12, 1}, [2]int{10, 0}); got != "old line one\nold line two\nalp" || v.Back != 2 {
		t.Fatalf("a drag past the top copied %q, %d back", got, v.Back)
	}
	if got := drag(v, at, s, [2]int{10, 3}, [2]int{10, 9}, [2]int{13, 9}, [2]int{13, 9}); !strings.HasSuffix(got, "\nlast") || v.Back != 0 {
		t.Fatalf("a drag past the bottom copied %q, %d back", got, v.Back)
	}
	v.Live()
	if v.on || v.Text(s) != "" {
		t.Fatal("Live left a selection")
	}
}

func TestTheCopyKeyAndSelectingByKeys(t *testing.T) {
	at := layout.Rect{W: 12, H: 3}
	s, v := screen.New(12, 3), &View{}
	io.WriteString(s, "zero\r\none two\r\nthree\r\nfour")
	for _, k := range []string{"super+c", "ctrl+shift+c", "ctrl+c"} {
		if d := v.Key(key(k), s, true); d.Used {
			t.Fatalf("%s with nothing selected is the program's, and was taken", k)
		}
	}
	sel := func() { drag(v, at, s, [2]int{0, 0}, [2]int{2, 0}) }
	for _, k := range []string{"super+c", "ctrl+shift+c"} {
		sel()
		if d := v.Key(key(k), s, false); !d.Used || d.Copy != "one" || !v.on {
			t.Fatalf("%s gave %+v", k, d)
		}
	}
	if d := v.Key(key("ctrl+c"), s, false); d.Used || d.Copy != "" || v.on {
		t.Fatalf("Ctrl+C on a Mac interrupts, and drops the selection like any key: %+v", d)
	}
	sel()
	if d := v.Key(key("ctrl+c"), s, true); !d.Used || d.Copy != "one" || v.on {
		t.Fatalf("Ctrl+C by the Windows rule copies and drops the selection: %+v", d)
	}
	for _, c := range []struct {
		b     Board
		style string
		want  bool
	}{{Board{System: "windows"}, "auto", true}, {Board{System: "darwin"}, "auto", false}, {Board{System: "linux"}, "auto", false},
		{Board{System: "windows", Remote: true}, "auto", false}, {Board{System: "darwin"}, "windows", true},
		{Board{System: "windows"}, "mac", false}, {Board{System: "windows"}, "linux", false}} {
		if got := c.b.CtrlC(c.style); got != c.want {
			t.Errorf("%+v with keys.style %s: Ctrl+C copies is %v", c.b, c.style, got)
		}
	}

	// By keys: the cursor starts where the program's is, on "four".
	g := term.NewGrid(12, 3)
	v.Select(s)
	if x, y, on := v.Paint(g, at, s, term.Style{}); x != 4 || y != 2 || !on || !strings.HasSuffix(g.Row(0), " select") {
		t.Fatalf("the cursor is at %d,%d shown %v under %q", x, y, on, g.Row(0))
	}
	press := func(keys string) (d Did) {
		for _, k := range strings.Fields(keys) {
			if d = v.Key(key(k), s, false); !d.Used {
				t.Fatalf("%s went to the program while selecting", k)
			}
		}
		return d
	}
	if d := press("up up up 0 v l l down $ y"); d.Copy != "zero\none two" || v.keys || v.Back != 0 {
		t.Fatalf("selecting by keys into the scroll-back copied %q", d.Copy)
	}
	v.Select(s)
	if press("k k k"); v.Back != 1 {
		t.Fatalf("the view did not follow the cursor: %d back", v.Back)
	}
	if d := press("space right esc"); d.Copy != "" || v.keys || v.on || v.Back != 0 {
		t.Fatalf("esc left with %+v", d)
	}
	// On the second screen there is nothing above the first row.
	io.WriteString(s, "\x1b[?1049h\x1b[Hfull screen")
	v.Select(s)
	if d := press("pageup home v end enter"); d.Copy != "full screen" {
		t.Fatalf("on the second screen %q was copied", d.Copy)
	}
}

// outer is a pretended terminal the person sits at: it shows what a window
// is sent and, where its own setting allows, obeys the clipboard sequence.
type outer struct {
	allows    bool
	clipboard string
	shown     bytes.Buffer
}

func (o *outer) Write(p []byte) (int, error) {
	head, rest, found := bytes.Cut(p, []byte("\x1b]52;c;"))
	o.shown.Write(head)
	if body, tail, ok := bytes.Cut(rest, []byte("\a")); found && ok {
		if text, err := base64.StdEncoding.DecodeString(string(body)); err == nil && o.allows {
			o.clipboard = string(text)
		}
		o.shown.Write(tail)
	}
	return len(p), nil
}

// The row's check: each of the three routes, from a drag to the clipboard
// of a pretended computer and through a pretended outer terminal.
func TestEachClipboardRoute(t *testing.T) {
	at := layout.Rect{W: 30, H: 4}
	s, v := screen.New(30, 4), &View{}
	io.WriteString(s, "café 界 one\r\ntwo")
	text := drag(v, at, s, [2]int{0, 0}, [2]int{2, 1})
	if text != "café 界 one\ntwo" {
		t.Fatalf("the drag copied %q", text)
	}
	system, ran := "", [][]string(nil)
	fails := map[string]bool{}
	was := Run
	defer func() { Run = was }()
	Run = func(argv []string, in []byte) error {
		if ran = append(ran, argv); fails[argv[0]] {
			return os.ErrNotExist
		}
		system = string(in)
		return nil
	}
	try := func(b Board, term *outer) (mark string) {
		system, ran = "", nil
		mark, seq := b.Copy(text)
		term.Write(append(append([]byte("before"), seq...), "after"...))
		if got := term.shown.String(); got != "beforeafter" {
			t.Fatalf("%+v: the terminal shows %q", b, got)
		}
		return mark
	}

	// 1. The window is on the person's own computer: the system's program.
	for sys, prog := range map[string]string{"darwin": "pbcopy", "linux": "wl-copy", "windows": "clip.exe"} {
		term := &outer{allows: true}
		if mark := try(Board{System: sys}, term); mark != Copied || len(ran) != 1 || ran[0][0] != prog || term.clipboard != "" {
			t.Fatalf("on %s: %q, ran %v, the terminal was sent %q", sys, mark, ran, term.clipboard)
		}
		// Windows' own program reads the two-byte form, behind its mark.
		if want := map[bool]string{true: "\xff\xfec\x00a\x00f\x00\xe9\x00 \x00L\x75"}[sys == "windows"]; want == "" && system != text || !strings.HasPrefix(system, want) {
			t.Fatalf("on %s the program was given %q", sys, system)
		}
	}
	fails["wl-copy"] = true
	if mark := try(Board{System: "linux"}, &outer{}); mark != Copied || len(ran) != 2 || ran[1][0] != "xclip" || system != text {
		t.Fatalf("on Linux without the first program: %q, ran %v", mark, ran)
	}
	// With no clipboard program at all the terminal is asked, and nothing
	// is called copied.
	fails["xclip"] = true
	term := &outer{allows: true}
	if mark := try(Board{System: "linux"}, term); mark != Sent || term.clipboard != text {
		t.Fatalf("on Linux with no program: %q, the terminal has %q", mark, term.clipboard)
	}

	// 2. Through `connect`: it lifts the sequence out of the stream on the
	// person's computer, whatever their terminal would have done with it.
	lift := &outer{allows: true}
	if mark := try(Board{System: "darwin", Connect: true}, lift); mark != Copied || len(ran) != 0 || lift.clipboard != text {
		t.Fatalf("through connect: %q, ran %v, lifted %q", mark, ran, lift.clipboard)
	}

	// 3. A bare ssh: the outer terminal is asked, and obeys or does not.
	for _, allows := range []bool{true, false} {
		term := &outer{allows: allows}
		mark := try(Board{System: "linux", Remote: true}, term)
		if want := map[bool]string{true: text}[allows]; mark != Sent || len(ran) != 0 || term.clipboard != want {
			t.Fatalf("over ssh, a terminal that allows it %v: %q, ran %v, clipboard %q", allows, mark, ran, term.clipboard)
		}
	}
	if mark, seq := (Board{System: "darwin"}).Copy(""); mark != "" || seq != nil || len(ran) != 0 {
		t.Fatalf("nothing selected was copied: %q %q", mark, seq)
	}
}

// The clipboard program is a real program here: this test's own, started
// again, which writes what it is given to a file.
func TestAProgramIsGivenTheText(t *testing.T) {
	if out := os.Getenv("PICK_TEST_OUT"); out != "" {
		in, _ := io.ReadAll(os.Stdin)
		os.WriteFile(out, in, 0o600)
		os.Exit(0)
	}
	out := t.TempDir() + "/clipboard"
	t.Setenv("PICK_TEST_OUT", out)
	if err := Run([]string{os.Args[0], "-test.run=TestAProgramIsGivenTheText"}, []byte("to the program")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(out); string(got) != "to the program" {
		t.Fatalf("the program was given %q", got)
	}
	if Run([]string{t.TempDir() + "/no-such-program"}, nil) == nil {
		t.Fatal("a program that is not there ran")
	}
}

func TestAProgramsOwnWishToCopy(t *testing.T) {
	for _, c := range []struct {
		setting, text string
		now           bool
		ask           string
	}{{"on", "a\nb", true, ""}, {"off", "a\nb", false, ""}, {"ask", "a\nb", false, "sign-up page wants to copy 2 lines: allow?"},
		{"", "a", false, "sign-up page wants to copy 1 lines: allow?"}, {"on", "", false, ""}, {"ask", "", false, ""}} {
		if now, ask := Wish(c.setting, "sign-up page", c.text); now != c.now || ask != c.ask {
			t.Errorf("%q with %q: %v, %q", c.setting, c.text, now, ask)
		}
	}
}
