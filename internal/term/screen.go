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

// Enter is what a pane asks of the terminal: the second screen, no cursor,
// the mouse with movement and exact positions, focus reports, and pastes
// marked as such. Leave gives all of it back.
const (
	Enter = "\x1b[?1049h\x1b[?25l\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1006h\x1b[?1004h\x1b[?2004h"
	Leave = "\x1b[?2004l\x1b[?1004l\x1b[?1006l\x1b[?1003l\x1b[?1002l\x1b[?1000l\x1b[0m\x1b[?25h\x1b[?1049l"
)

// Term is one terminal: the grid a program draws into, and its events.
type Term struct {
	*Grid
	Events <-chan Event
	Mode   Mode
	// While CurShown is set, a Flush leaves the terminal's own cursor
	// showing at column CurX of row CurY: the cursor of the pane with the keys.
	CurX, CurY int
	CurShown   bool
	// Sync wraps each Flush in the marks that make a terminal draw it in
	// one go (mode 2026). A terminal that does not know them ignores them.
	Sync bool

	events  chan Event
	out     io.Writer
	mu      sync.Mutex
	shown   []Cell // what the terminal shows now
	shownW  int
	curX    int // where the terminal's cursor was left, and whether it shows
	curY    int
	curOn   bool
	buf     []byte
	closed  bool
	restore func()
}

// New starts a pane on any reader and writer, which need not be a terminal.
func New(in io.Reader, out io.Writer, w, h int, mode Mode) *Term {
	events := make(chan Event, 64)
	t := &Term{Grid: NewGrid(w, h), Events: events, Mode: mode, events: events, out: out, restore: func() {}}
	io.WriteString(out, Enter)
	go Read(in, events, EscapeWait)
	return t
}

// Raw puts the terminal the program runs in into raw mode, and returns its
// size and what puts it back as it was.
func Raw() (w, h int, restore func(), err error) {
	in := int(os.Stdin.Fd())
	old, err := xterm.MakeRaw(in)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("needs a terminal: %w", err)
	}
	undo := console(os.Stdin, os.Stdout)
	restore = func() {
		undo()
		xterm.Restore(in, old)
	}
	if w, h, err = xterm.GetSize(int(os.Stdout.Fd())); err != nil {
		restore()
		return 0, 0, nil, fmt.Errorf("cannot read its size: %w", err)
	}
	return w, h, restore, nil
}

// Watch calls changed with each new size of that terminal, for ever.
func Watch(w, h int, changed func(w, h int)) {
	for {
		waitResize()
		if nw, nh, err := xterm.GetSize(int(os.Stdout.Fd())); err == nil && (nw != w || nh != h) {
			w, h = nw, nh
			changed(w, h)
		}
	}
}

// Open starts a pane on the terminal the program runs in. The terminal is
// put back by Close, and also when the program is told to end or loses its
// terminal. A program that is killed outright cannot put anything back.
func Open() (*Term, error) {
	w, h, restore, err := Raw()
	if err != nil {
		return nil, fmt.Errorf("the pane %w", err)
	}
	t := New(os.Stdin, os.Stdout, w, h, ModeOf(os.Getenv))
	t.restore = restore
	ended := make(chan os.Signal, 1)
	signal.Notify(ended, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		s := <-ended
		t.Close()
		os.Exit(128 + int(s.(syscall.Signal)))
	}()
	go Watch(w, h, func(w, h int) { t.Post(Event{Kind: Resize, W: w, H: h}) })
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
		io.WriteString(t.out, Leave)
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
	if t.Sync {
		b = append(b, "\x1b[?2026h"...)
	}
	if t.curOn { // not seen wandering while the cells are drawn
		b = append(b, "\x1b[?25l"...)
	}
	head := len(b)
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
	drew := len(b) > head
	if drew {
		b = append(b, "\x1b[0m"...)
	}
	show := t.CurShown && t.CurX >= 0 && t.CurX < t.W && t.CurY >= 0 && t.CurY < t.H
	if t.buf = b; !drew && show == t.curOn && (!show || t.CurX == t.curX && t.CurY == t.curY) {
		return nil
	}
	if show {
		b = fmt.Appendf(b, "\x1b[%d;%dH\x1b[?25h", t.CurY+1, t.CurX+1)
	}
	if t.Sync {
		b = append(b, "\x1b[?2026l"...)
	}
	t.curX, t.curY, t.curOn = t.CurX, t.CurY, show
	_, err := t.out.Write(b)
	return err
}
