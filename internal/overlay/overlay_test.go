package overlay

import (
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/layout"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

var t0 = time.Unix(1_700_000_000, 0)

func rows(g *term.Grid) string {
	var out []string
	for y := range g.H {
		out = append(out, g.Row(y))
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// "Shown" is said only when a window is there to show it and the pop-up is
// switched on; the notice of a call that was not shown is not kept.
func TestThePopUpAnswersHonestly(t *testing.T) {
	var p Popup
	g := term.NewGrid(40, 6)
	for _, c := range []struct {
		on               bool
		windows          int
		reason, delivery string
	}{{false, 2, contract.NotifyOff, contract.NotifyOff}, {false, 0, contract.NotifyOff, contract.NotifyOff},
		{true, 0, contract.NotifyNoWindow, "on"}, {true, 1, contract.NotifyShown, "on"}} {
		reason, delivery := p.Show("needs you", "a question waits", c.on, c.windows, t0)
		if reason != c.reason || delivery != c.delivery {
			t.Errorf("switched on %v with %d windows: %q, %q", c.on, c.windows, reason, delivery)
		}
		if drawn := p.Draw(g, layout.Look{}, t0); drawn != (reason == contract.NotifyShown) {
			t.Errorf("switched on %v with %d windows: drawn is %v", c.on, c.windows, drawn)
		}
	}
}

func TestThePopUpIsABoxInTheTopCorner(t *testing.T) {
	var p Popup
	p.Show("needs you", "the sign-up page asks which of the two plans it should build first, and waits for an answer before it goes on with anything else at all", true, 1, t0)
	g := term.NewGrid(60, 8)
	g.Put(0, 0, 60, strings.Repeat("tab ", 15), term.Style{})
	accent := term.Style{Fg: term.RGB(0x3fd8f0)}
	if !p.Draw(g, layout.Look{Accent: accent}, t0.Add(For-time.Millisecond)) {
		t.Fatal("the pop-up went before its time")
	}
	want := strings.TrimRight(strings.Repeat("tab ", 15), " ") + `
                  ┌─needs you──────────────────────────────┐
                  │ the sign-up page asks which of the two │
                  │ plans it should build first, and waits │
                  │ for an answer before it goes on with … │
                  └────────────────────────────────────────┘`
	if got := rows(g); got != want {
		t.Fatalf("the pop-up is drawn\n%s\nwant\n%s", got, want)
	}
	if c := g.At(20, 1); c.Style.Fg != accent.Fg || !c.Style.Bold {
		t.Fatalf("the title is drawn %+v", c)
	}
	if p.Draw(term.NewGrid(60, 8), layout.Look{}, t0.Add(For)) {
		t.Fatal("the pop-up stayed past its time")
	}
	// A terminal without the line characters, and a window too small for it.
	p.Show("hi", "there", true, 1, t0)
	g = term.NewGrid(7, 3)
	p.Draw(g, layout.Look{Plain: true}, t0)
	if got := rows(g); got != "\n+-hi--+\n| the |" {
		t.Fatalf("in a small plain window the pop-up is %q", got)
	}
	for w := range 6 {
		for h := range 4 {
			p.Draw(term.NewGrid(w, h), layout.Look{}, t0)
		}
	}
}

// Nothing of a notice's words can steer the person's terminal, in the box
// or in the sequence.
func TestWhatAWindowsTerminalIsSent(t *testing.T) {
	env := func(vars ...string) func(string) string {
		return func(name string) string {
			for i := 0; i < len(vars); i += 2 {
				if vars[i] == name {
					return vars[i+1]
				}
			}
			return ""
		}
	}
	const title, body = "needs you\x1b]52;c;eA==\a", "a question\x1b\\ waits\r\n\u009d"
	for _, c := range []struct {
		name  string
		env   func(string) string
		sound bool
		want  string
	}{
		{"a bare ssh", env(), true, "\a"},
		{"a bare ssh without sound", env("TERM", "xterm-256color"), false, ""},
		{"the system's own terminal on a Mac", env("TERM_PROGRAM", "Apple_Terminal"), true, "\a"},
		{"iTerm2", env("TERM_PROGRAM", "iTerm.app"), false, "\x1b]9;needs you: a question waits \x1b\\"},
		{"Ghostty", env("TERM_PROGRAM", "ghostty", "TERM", "xterm-ghostty"), true, "\x1b]9;needs you: a question waits \x1b\\\a"},
		{"kitty", env("TERM", "xterm-kitty"), true, "\x1b]99;i=ws:d=0:p=title;needs you\x1b\\\x1b]99;i=ws:d=0:p=body;a question waits \x1b\\\x1b]99;i=ws:d=1;\x1b\\\a"},
	} {
		if got := string(Signal(title, body, c.sound, c.env)); got != c.want {
			t.Errorf("%s is sent %q, want %q", c.name, got, c.want)
		}
	}
	var p Popup
	p.Show(title, body, true, 1, t0)
	g := term.NewGrid(40, 5)
	p.Draw(g, layout.Look{}, t0)
	if got := rows(g); strings.ContainsAny(got, "\x1b\a\r") || !strings.Contains(got, "─needs you─") || !strings.Contains(got, "│ a question waits │") {
		t.Fatalf("the box holds %q", got)
	}
}

// A pane drawn over a tab is inside the window and under the tab row,
// whatever is asked for, and its frame draws nowhere else.
func TestAPaneOverATab(t *testing.T) {
	for _, c := range []struct {
		W, H int
		w, h float64
		want layout.Rect
	}{{100, 31, 0.5, 0.5, layout.Rect{X: 25, Y: 8, W: 50, H: 15}}, {100, 31, 60, 10, layout.Rect{X: 19, Y: 10, W: 62, H: 12}},
		{100, 31, 1, 1, layout.Rect{X: 0, Y: 1, W: 100, H: 30}}, {100, 31, 500, 500, layout.Rect{X: 0, Y: 1, W: 100, H: 30}},
		{100, 31, 0, -3, layout.Rect{X: 48, Y: 14, W: 3, H: 3}}} {
		if got := Place(c.W, c.H, c.w, c.h); got != c.want {
			t.Errorf("%v by %v in a window of %d by %d is at %+v, want %+v", c.w, c.h, c.W, c.H, got, c.want)
		}
	}
	r := rand.New(rand.NewSource(1))
	for range 2000 {
		W, H := 1+r.Intn(200), 2+r.Intn(80)
		at := Place(W, H, r.Float64()*float64(r.Intn(300)), r.Float64()*float64(r.Intn(100)))
		if at.X < 0 || at.Y < 1 || at.X+at.W > W || at.Y+at.H > H || at.W < 1 || at.H < 1 {
			t.Fatalf("in a window of %d by %d the pane is at %+v", W, H, at)
		}
		g := term.NewGrid(W, H)
		for y := range H {
			g.Put(0, y, W, strings.Repeat("x", W), term.Style{})
		}
		in := Box(g, layout.Look{}, at, "list")
		for y := range H {
			for x := range W {
				frame := x >= at.X && x < at.X+at.W && y >= at.Y && y < at.Y+at.H
				inside := x >= in.X && x < in.X+in.W && y >= in.Y && y < in.Y+in.H
				if c := g.At(x, y).Text; !frame && c != "x" || inside && c != " " || frame && c == "x" {
					t.Fatalf("in a window of %d by %d, frame %+v, inside %+v: the cell %d,%d is %q", W, H, at, in, x, y, c)
				}
			}
		}
	}
}
