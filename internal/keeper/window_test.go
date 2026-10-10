package keeper

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/screen"
)

// outer is a person's terminal at the other end of a window's connection:
// the screen reader stands in for it, and shows what a person would see.
type outer struct {
	*client
	r     *rig
	mu    sync.Mutex
	scr   *screen.Screen
	bells int
}

// attach makes the connection a window of that size; nothing is read yet.
func (r *rig) attach(w, h int) *outer {
	r.t.Helper()
	o := &outer{client: r.dial(), r: r, scr: screen.New(w, h)}
	o.send(contract.WireCall{Op: contract.OpAttach, W: float64(w), H: float64(h), System: "darwin",
		Env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	return o
}

// read takes what the keeper sends, as a terminal does, until the end.
func (o *outer) read() {
	for {
		kind, body, err := contract.ReadFrame(o.conn)
		if err != nil {
			return
		}
		if kind == contract.FrameBytes {
			o.mu.Lock()
			o.scr.Write(body)
			o.bells += bytes.Count(body, []byte{'\a'})
			o.mu.Unlock()
		}
	}
}

func (r *rig) window(w, h int) *outer {
	r.t.Helper()
	o := r.attach(w, h)
	go o.read()
	return o
}

func (o *outer) text() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.scr.Picture().Text()
}

// shows waits until the terminal shows a text, and returns where.
func (o *outer) shows(text string) (x, y int) {
	o.t.Helper()
	o.r.until("the window to show "+text, func() bool {
		for row, line := range strings.Split(o.text(), "\n") {
			if i := strings.Index(line, text); i >= 0 {
				x, y = len([]rune(line[:i])), row
				return true
			}
		}
		return false
	})
	return x, y
}

// types sends bytes as the person's terminal would.
func (o *outer) types(s string) {
	o.t.Helper()
	if err := contract.WriteFrame(o.conn, contract.FrameBytes, []byte(s)); err != nil {
		o.t.Fatal(err)
	}
}

func (o *outer) click(x, y int) {
	o.types(fmt.Sprintf("\x1b[<0;%d;%dM\x1b[<0;%d;%dm", x+1, y+1, x+1, y+1))
}

// got waits until a pane's program has been typed exactly that.
func (r *rig) got(pane, want string) {
	r.t.Helper()
	f := r.program(pane)
	for end := time.Now().Add(10 * time.Second); f.got() != want; time.Sleep(time.Millisecond) {
		if time.Now().After(end) {
			r.t.Fatalf("%s was typed %q, not %q", pane, f.got(), want)
		}
	}
}

func (r *rig) focused() (pane string) {
	r.locked(func() bool { pane = r.k.focus; return true })
	return pane
}

// A window is served: it shows the tab row, the dividing lines and each
// pane's picture with the cursor of the pane that has the keys; what is
// typed and pasted in it reaches that pane's program; a click shows a tab or
// gives a pane the keys; the panes follow its size.
func TestAWindowIsServed(t *testing.T) {
	r := newRig(t)
	c := r.dial()
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "one", Cwd: "/work"})
	w := r.window(100, 30)
	if _, y := w.shows("one"); y != 0 {
		t.Errorf("the tab's name is on row %d", y)
	}
	if _, y := w.shows("size 100x28"); y != 2 {
		t.Errorf("the pane's first line is on row %d:\n%s", y, w.text())
	}
	w.types("hi")
	r.got("p1", "hi")
	x, y := w.shows("hi")
	w.r.until("the cursor behind what was typed", func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		p := w.scr.Picture()
		return p.CurShown && p.CurX == x+2 && p.CurY == y
	})
	// Keys in both forms a terminal sends them in; Cmd+C is not typed.
	w.types("\r\x1b[A\x03\x1b[99;5u\x1b[99;9u\x1b[13;2u\x1bx\x1b[Z")
	r.got("p1", "hi\r\x1b[A\x03\x03\r\x1bx\x1b[Z")
	// A paste: its line breaks are Enter.
	w.types("\x1b[200~a\nb\r\nc\x1b[201~")
	r.got("p1", "hi\r\x1b[A\x03\x03\r\x1bx\x1b[Za\rb\rc")

	// A second pane beside the first, and a click on it.
	c.call(contract.WireCall{Op: contract.OpSplit, Pane: "p1", Dir: contract.Right, Ratio: 0.5})
	w.shows("│")
	x, y = w.shows("pane=p2")
	w.click(x, y)
	r.until("the keys in the pane clicked on", func() bool { return r.focused() == "p2" })
	w.types("there")
	r.got("p2", "there")

	// A second tab, shown by a click on its name.
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "two", Cwd: "/work"})
	x, y = w.shows("two")
	w.click(x, y)
	r.until("the keys in the tab clicked on", func() bool { return r.focused() == "p3" })
	w.shows("pane=p3")
	if strings.Contains(w.text(), "pane=p2") {
		t.Errorf("the tab that does not show is drawn:\n%s", w.text())
	}

	// The window changes its size, and the panes follow.
	w.send(contract.WireCall{Op: contract.OpWindowSize, W: 80, H: 24})
	w.mu.Lock()
	w.scr = screen.New(80, 24)
	w.mu.Unlock()
	w.shows("size 80x22")
	if reply := c.call(contract.WireCall{Op: contract.OpSize, Pane: "p3"}); reply.W != 80 || reply.H != 22 {
		t.Errorf("the pane is %d by %d in a window of 80 by 24", reply.W, reply.H)
	}

	// A second window of another size: the panes take the size of the
	// window used last, which is the new one, and the other gets the same
	// picture cut or padded with words that say so. Typing in the first
	// makes it the one used last again.
	small := r.window(50, 15)
	w.shows("sized for another window, 50 by 15")
	w.types("x")
	small.shows("sized for another window, 80 by 24")
	w.shows("size 80x22")
	if strings.Contains(w.text(), "another window") {
		t.Errorf("the window used last says:\n%s", w.text())
	}

	// A notice, with the bell, in every window; and honestly none without one.
	if reply := c.call(contract.WireCall{Op: contract.OpNotify, Title: "T3", Text: "needs \x1b[31myou", Sound: true}); reply.Text != contract.NotifyShown {
		t.Errorf("a notice with two windows: %+v", reply)
	}
	w.shows("T3: needs you")
	small.shows("T3: needs you")
	r.until("the bell", func() bool { w.mu.Lock(); defer w.mu.Unlock(); return w.bells == 1 })
	small.conn.Close()
	w.conn.Close()
	r.until("the windows to be gone", func() bool { return r.locked(func() bool { return len(r.k.wins) == 0 && r.k.last == nil }) })
	if reply := c.call(contract.WireCall{Op: contract.OpNotify, Title: "T3", Text: "needs you"}); reply.Text != contract.NotifyNoWindow {
		t.Errorf("a notice with no window: %+v", reply)
	}
}

// A window that takes nothing holds up neither the panes nor the calls nor
// another window, and when it reads again it ends on the picture as it is.
func TestAWindowThatTakesNothingHoldsNobodyUp(t *testing.T) {
	r := newRig(t)
	c := r.dial()
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "one"})
	slow, quick := r.attach(120, 40), r.window(120, 40)
	r.until("both windows", func() bool { return r.locked(func() bool { return len(r.k.wins) == 2 }) })
	f := r.program("p1")
	began := time.Now()
	for i := range 3000 {
		f.print(fmt.Sprintf("\x1b[3%dmline %d of the noise a busy agent makes while nobody reads this window\r\n", i%8, i))
		if i%100 == 0 {
			c.call(contract.WireCall{Op: contract.OpScreen, Pane: "p1"})
		}
	}
	f.print("the last line")
	r.settle()
	if took := time.Since(began); took > 10*time.Second {
		t.Errorf("the pane took %v beside a window that takes nothing", took)
	}
	quick.shows("the last line")
	go slow.read()
	slow.shows("the last line")
	if a, b := slow.text(), quick.text(); a != b {
		t.Errorf("the window that was slow shows\n%s\nand the other\n%s", a, b)
	}
}
