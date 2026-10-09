package term

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	xterm "golang.org/x/term"
)

// What a pane asks of the terminal: the second screen, no cursor, the mouse
// with movement and exact positions, focus reports, and pastes marked as such.
const (
	enter = "\x1b[?1049h\x1b[?25l\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1006h\x1b[?1004h\x1b[?2004h"
	leave = "\x1b[?2004l\x1b[?1004l\x1b[?1006l\x1b[?1003l\x1b[?1002l\x1b[?1000l\x1b[0m\x1b[?25h\x1b[?1049l"
)

// Term is one terminal: the grid a program draws into, and its events.
type Term struct {
	*Grid
	Events <-chan Event
	Mode   Mode

	events  chan Event
	out     io.Writer
	mu      sync.Mutex
	shown   []Cell // what the terminal shows now
	shownW  int
	buf     []byte
	closed  bool
	restore func()
}

// New starts a pane on any reader and writer, which need not be a terminal.
func New(in io.Reader, out io.Writer, w, h int, mode Mode) *Term {
	events := make(chan Event, 64)
	t := &Term{Grid: NewGrid(w, h), Events: events, Mode: mode, events: events, out: out, restore: func() {}}
	io.WriteString(out, enter)
	go Read(in, events, EscapeWait)
	return t
}

// Open starts a pane on the terminal the program runs in. The terminal is
// put back by Close, and also when the program is told to end or loses its
// terminal. A program that is killed outright cannot put anything back.
func Open() (*Term, error) {
	in, fd := int(os.Stdin.Fd()), int(os.Stdout.Fd())
	old, err := xterm.MakeRaw(in)
	if err != nil {
		return nil, fmt.Errorf("the pane needs a terminal: %w", err)
	}
	undo := console(os.Stdin, os.Stdout)
	w, h, err := xterm.GetSize(fd)
	if err != nil {
		undo()
		xterm.Restore(in, old)
		return nil, fmt.Errorf("the pane cannot read its size: %w", err)
	}
	t := New(os.Stdin, os.Stdout, w, h, ModeOf(os.Getenv))
	t.restore = func() {
		undo()
		xterm.Restore(in, old)
	}
	ended := make(chan os.Signal, 1)
	signal.Notify(ended, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		s := <-ended
		t.Close()
		os.Exit(128 + int(s.(syscall.Signal)))
	}()
	go func() {
		for {
			waitResize()
			if nw, nh, err := xterm.GetSize(fd); err == nil && (nw != w || nh != h) {
				w, h = nw, nh
				t.Post(Event{Kind: Resize, W: w, H: h})
			}
		}
	}()
	return t, nil
}

// Post puts an event in the queue from outside the terminal.
func (t *Term) Post(ev Event) { t.events <- ev }

// Close puts the terminal back as it was. Calling it again does nothing.
func (t *Term) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		t.closed = true
		io.WriteString(t.out, leave)
		t.restore()
	}
}

// Flush sends the cells that differ from what the terminal shows, and
// nothing else. After a Resize everything is sent again.
func (t *Term) Flush() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	b := t.buf[:0]
	if len(t.shown) != len(t.cells) || t.shownW != t.W {
		t.shown, t.shownW = make([]Cell, len(t.cells)), t.W
		for i := range t.shown {
			t.shown[i] = blank
		}
		b = append(b, "\x1b[0m\x1b[2J"...)
	}
	at, style, styled := -1, Style{}, false
	for i, c := range t.cells {
		if c == t.shown[i] {
			continue
		}
		t.shown[i] = c
		if c.Text == "" {
			continue
		}
		if i != at || i%t.W == 0 {
			b = fmt.Appendf(b, "\x1b[%d;%dH", i/t.W+1, i%t.W+1)
		}
		if !styled || c.Style != style {
			b, style, styled = t.Mode.style(b, c.Style), c.Style, true
		}
		b = append(b, c.Text...)
		// Where the cursor stands after a wide or joined character is
		// the terminal's own opinion, so the next cell is addressed.
		if at = i + 1; len(c.Text) > 3 || at < len(t.cells) && t.cells[at].Text == "" {
			at = -1
		}
	}
	if t.buf = b; len(b) == 0 {
		return nil
	}
	_, err := t.out.Write(append(b, "\x1b[0m"...))
	return err
}
