// Package termtest is a pretended terminal for tests: a program draws on it
// through the terminal layer exactly as on a real one, and a test types and
// clicks into it and reads what it shows.
package termtest

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// Screen is the pretended terminal. Term is what the program under test is
// handed in the place of term.Open.
type Screen struct {
	Term *term.Term

	in    *io.PipeWriter
	mu    sync.Mutex
	shown *term.Grid
	x, y  int
	sent  strings.Builder
}

func New(w, h int) *Screen {
	r, in := io.Pipe()
	s := &Screen{in: in, shown: term.NewGrid(w, h)}
	s.Term = term.New(r, s, w, h, term.FullColour)
	return s
}

// Write is the terminal's side: it obeys the two sequences the layer moves
// and clears with, and shows every text where the cursor stands. Colours
// are not kept; a test reads those from Term's own grid.
func (s *Screen) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent.Write(p)
	for b := string(p); b != ""; {
		if b[0] != 0x1b {
			n := strings.IndexByte(b+"\x1b", 0x1b)
			s.x += s.shown.Put(s.x, s.y, s.shown.W, b[:n], term.Style{})
			b = b[n:]
			continue
		}
		end := 2
		for end < len(b) && (b[end] < 0x40 || b[end] > 0x7e) {
			end++
		}
		if end >= len(b) {
			break
		}
		switch b[end] {
		case 'H':
			fmt.Sscanf(b[2:end], "%d;%d", &s.y, &s.x)
			s.x, s.y = s.x-1, s.y-1
		case 'J':
			s.shown.Clear()
		}
		b = b[end+1:]
	}
	return len(p), nil
}

// Sent is every byte the program has written so far.
func (s *Screen) Sent() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sent.String()
}

// Row is what row y shows.
func (s *Screen) Row(y int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shown.Row(y)
}

// Find returns the first cell of the first place the text is shown.
func (s *Screen) Find(text string) (x, y int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shown.Find(text)
}

// Wait gives the program up to two seconds to show the text.
func (s *Screen) Wait(text string) bool {
	for range 1000 {
		if _, _, ok := s.Find(text); ok {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

// Type sends bytes exactly as a terminal would.
func (s *Screen) Type(raw string) { io.WriteString(s.in, raw) }

// Key presses one key, named as in term.Event. After a bare Escape it
// waits, as a person does, so the next key is not taken for part of it.
func (s *Screen) Key(name string) {
	s.Type(Bytes(name))
	if name == "esc" {
		time.Sleep(3 * term.EscapeWait)
	}
}

func (s *Screen) Paste(text string) { s.Type("\x1b[200~" + text + "\x1b[201~") }

// Click presses and lets go of the left button on a cell.
func (s *Screen) Click(x, y int) {
	s.Type(fmt.Sprintf("\x1b[<0;%d;%dM\x1b[<0;%[1]d;%[2]dm", x+1, y+1))
}

// ClickText clicks the first cell of the text, if it is shown.
func (s *Screen) ClickText(text string) bool {
	x, y, ok := s.Find(text)
	if ok {
		s.Click(x, y)
	}
	return ok
}

// Resize gives the terminal a new size and tells the program.
func (s *Screen) Resize(w, h int) {
	s.mu.Lock()
	s.shown.Resize(w, h)
	s.mu.Unlock()
	s.Term.Post(term.Event{Kind: term.Resize, W: w, H: h})
}

// Close ends the input, as a closed pane does.
func (s *Screen) Close() { s.in.Close() }

var keys = map[string]string{"enter": "\r", "esc": "\x1b", "tab": "\t", "space": " ", "backspace": "\x7f",
	"up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D", "home": "\x1b[H", "end": "\x1b[F",
	"delete": "\x1b[3~", "pageup": "\x1b[5~", "pagedown": "\x1b[6~", "shift+tab": "\x1b[Z"}

// Bytes is what a terminal sends for a key.
func Bytes(key string) string {
	if b, ok := keys[key]; ok {
		return b
	}
	if rest, ok := strings.CutPrefix(key, "ctrl+"); ok && len(rest) == 1 {
		return string(rest[0] &^ 0x60)
	}
	if rest, ok := strings.CutPrefix(key, "alt+"); ok {
		return "\x1b" + Bytes(rest)
	}
	return key
}
