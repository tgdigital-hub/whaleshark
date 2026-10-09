package term_test

import (
	"fmt"
	"io"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/term/termtest"
)

func TestOnlyWhatChangedIsSent(t *testing.T) {
	s := termtest.New(40, 10)
	g := s.Term
	for y := range g.H {
		g.Put(0, y, g.W, fmt.Sprintf("row %d ◐ building 40%%", y), term.Style{Bold: y == 0})
	}
	g.Flush()
	full := len(s.Sent())
	g.Flush()
	if n := len(s.Sent()); n != full {
		t.Fatalf("nothing changed and %d bytes were sent", n-full)
	}
	g.Put(17, 4, 2, "41", term.Style{})
	g.Flush()
	if got := s.Sent()[full:]; got != "\x1b[5;19H\x1b[0m1\x1b[0m" {
		t.Fatalf("one changed cell sent %q", got)
	}
	if s.Row(4) != "row 4 ◐ building 41%" {
		t.Fatalf("the screen shows %q", s.Row(4))
	}
}

// Whatever is drawn over whatever, the terminal shows what the grid holds.
func TestTheScreenShowsTheGrid(t *testing.T) {
	words := []string{"a", "idle 12m", "漢字", "👍🏽", "é", "▲ needs you", "███░░", "[ Yes ]", "ｗｉｄｅ", " "}
	rng := rand.New(rand.NewSource(1))
	s := termtest.New(37, 9)
	g := s.Term
	for round := range 400 {
		switch {
		case round%97 == 96:
			w, h := 20+rng.Intn(30), 5+rng.Intn(8)
			s.Resize(w, h)
			g.Resize(w, h)
		case round%50 == 49:
			g.Clear()
		}
		for range 1 + rng.Intn(6) {
			g.Put(rng.Intn(g.W), rng.Intn(g.H), 1+rng.Intn(g.W), words[rng.Intn(len(words))], term.Style{Reverse: rng.Intn(2) == 0})
		}
		g.Flush()
		for y := range g.H {
			if got, want := s.Row(y), g.Row(y); got != want {
				t.Fatalf("round %d, row %d: the screen shows %q, the grid holds %q", round, y, got, want)
			}
			for x := range g.W {
				if g.At(x, y).Text == "" && (x == 0 || term.Width(g.At(x-1, y).Text) != 2) {
					t.Fatalf("round %d: half a wide character at %d,%d", round, x, y)
				}
			}
		}
	}
}

func TestPutCutsAndCounts(t *testing.T) {
	g := term.NewGrid(10, 2)
	if n := g.Put(0, 0, 5, "漢字漢字", term.Style{}); n != 4 || g.Row(0) != "漢字" {
		t.Errorf("cut inside a wide character: %d cells, %q", n, g.Row(0))
	}
	if n := g.Put(7, 1, 99, "abcdef", term.Style{}); n != 3 || g.Row(1) != "       abc" {
		t.Errorf("cut at the edge: %d cells, %q", n, g.Row(1))
	}
	if g.Put(0, 5, 9, "x", term.Style{})+g.Put(-1, 0, 9, "x", term.Style{}) != 0 {
		t.Errorf("drawing outside the grid took cells")
	}
	if x, y, ok := g.Find("abc"); !ok || x != 7 || y != 1 {
		t.Errorf("Find: %d,%d %v", x, y, ok)
	}
	if x, _, _ := g.Find("字"); x != 2 {
		t.Errorf("Find counts cells, not characters: %d", x)
	}
	if term.Width("漢a\x1b[31mé") != 4 {
		t.Errorf("Width: %d", term.Width("漢a\x1b[31mé"))
	}
}

// What an agent wrote cannot recolour, retitle or clear the screen.
func TestHostileTextIsDrawnHarmless(t *testing.T) {
	const hostile = "\x1b[31mred\x1b]0;a title\x07 ok\x1b[2J\x1b[?1049l\r\n[ Yes ]\u009b1m\u202e\x1bP+q\x1b\\."
	if got := term.Clean(hostile); got != "red ok [ Yes ]1m." {
		t.Errorf("Clean gives %q", got)
	}
	s := termtest.New(40, 3)
	s.Term.Put(0, 1, 40, hostile, term.Style{})
	s.Term.Flush()
	if s.Row(1) != "red ok [ Yes ]1m." || strings.Contains(s.Sent(), "a title") || strings.Contains(s.Sent(), "[31m") {
		t.Errorf("row %q, sent %q", s.Row(1), s.Sent())
	}
}

func FuzzClean(f *testing.F) {
	f.Add("plain")
	f.Add("\x1b[31mred\x1b]0;t\x07\x1b")
	f.Add("a\xffb\xc2\x9b\u2066")
	f.Fuzz(func(t *testing.T, s string) {
		g := term.NewGrid(30, 1)
		g.Put(0, 0, 30, s, term.Style{})
		for _, r := range term.Clean(s) + g.Row(0) {
			if r < 0x20 || r >= 0x7f && r < 0xa0 || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
				t.Fatalf("%q keeps U+%04X", s, r)
			}
		}
		if term.Clean(term.Clean(s)) != term.Clean(s) {
			t.Fatalf("%q is not clean after one pass", s)
		}
	})
}

// A full pane redrawn from nothing, every cell new and in a style of its
// own, in under 20 ms. A pane is never this large or this varied.
func TestFullRedrawUnder20ms(t *testing.T) {
	r, _ := io.Pipe()
	tm := term.New(r, io.Discard, 200, 60, term.FullColour)
	best := time.Hour
	for round := range 20 {
		start := time.Now()
		tm.Clear()
		for y := range tm.H {
			for x := 0; x < tm.W; x += 4 {
				tm.Put(x, y, 4, "█▓░●◐漢"[(x/4+y+round)%5*3:][:3]+"a"+string(rune('0'+round%10)),
					term.Style{Fg: term.RGB(uint32(x*y*7 + round)), Bg: term.RGB(uint32(x + y<<8)), Bold: x%8 == 0})
			}
		}
		if err := tm.Flush(); err != nil {
			t.Fatal(err)
		}
		best = min(best, time.Since(start))
	}
	t.Logf("a full redraw of 200x60 cells: %v", best)
	if best > 20*time.Millisecond && !slow {
		t.Fatalf("a full redraw took %v, want under 20 ms", best)
	}
}

func TestResizeCloseAndTheEndOfInput(t *testing.T) {
	s := termtest.New(20, 4)
	if !strings.Contains(s.Sent(), "\x1b[?1049h") || !strings.Contains(s.Sent(), "\x1b[?1006h") || !strings.Contains(s.Sent(), "\x1b[?2004h") {
		t.Fatalf("the pane did not ask for its screen, the mouse and marked pastes: %q", s.Sent())
	}
	s.Term.Put(0, 3, 20, "last line", term.Style{})
	s.Term.Flush()
	s.Resize(30, 6)
	if ev := <-s.Term.Events; ev.Kind != term.Resize || ev.W != 30 || ev.H != 6 {
		t.Fatalf("resize event: %+v", ev)
	}
	s.Term.Resize(30, 6)
	s.Term.Put(0, 5, 30, "last line", term.Style{})
	s.Term.Flush()
	if s.Row(5) != "last line" || s.Row(3) != "" {
		t.Fatalf("after the resize: %q / %q", s.Row(3), s.Row(5))
	}
	s.Key("y")
	s.Paste("two\nlines")
	s.Click(3, 5)
	s.Close()
	var got []string
	for ev := range s.Term.Events {
		got = append(got, fmt.Sprintf("%d%s%s %d %d", ev.Kind, ev.Key, ev.Text, ev.X, ev.Y))
		if ev.Kind == term.End {
			break
		}
	}
	want := []string{"1y 0 0", "3two\nlines 0 0", "2 3 5", "2 3 5", "6 0 0"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("events %q, want %q", got, want)
	}
	s.Term.Close()
	s.Term.Close()
	if !strings.HasSuffix(s.Sent(), "\x1b[?1003l\x1b[?1002l\x1b[?1000l\x1b[0m\x1b[?25h\x1b[?1049l") {
		t.Fatalf("the terminal was not put back: %q", s.Sent())
	}
	sent := len(s.Sent())
	s.Term.Put(0, 0, 5, "late", term.Style{})
	s.Term.Flush()
	if len(s.Sent()) != sent {
		t.Fatalf("something was drawn after the close")
	}
}

func TestHarnessKeys(t *testing.T) {
	for name, want := range map[string]string{"ctrl+c": "\x03", "alt+x": "\x1bx", "enter": "\r", "S": "S", "é": "é", "alt+backspace": "\x1b\x7f"} {
		if got := termtest.Bytes(name); got != want {
			t.Errorf("%s is %q, want %q", name, got, want)
		}
	}
	s := termtest.New(10, 2)
	for _, name := range []string{"esc", "y", "up", "ctrl+w", "alt+backspace", "shift+tab", "space"} {
		s.Key(name)
		if ev := <-s.Term.Events; ev.Key != name {
			t.Errorf("%s arrived as %+v", name, ev)
		}
	}
	s.Term.Put(0, 1, 10, "[ Yes ]", term.Style{})
	s.Term.Flush()
	if !s.Wait("Yes") || !s.ClickText("Yes") || s.ClickText("No") {
		t.Fatalf("waiting for and clicking a text")
	}
	if ev := <-s.Term.Events; ev.Kind != term.Mouse || ev.X != 2 || ev.Y != 1 {
		t.Errorf("click on the text: %+v", ev)
	}
}
