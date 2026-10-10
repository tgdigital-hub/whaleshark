package keeper

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/input"
	"github.com/tgdigital-hub/whaleshark/internal/layout"
	"github.com/tgdigital-hub/whaleshark/internal/overlay"
	"github.com/tgdigital-hub/whaleshark/internal/pick"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/theme"
)

// A window gets no more frames than this, however fast panes print.
const frameGap = 16 * time.Millisecond

// window is one person's terminal, as the keeper draws it. w and h are its
// size and in its keys on their way in, under the keeper's lock; the grid
// of t is its drawing goroutine's.
type window struct {
	link  *link
	t     *term.Term
	w, h  int
	look  layout.Look
	wake  chan struct{}
	gone  chan struct{}
	in    input.In
	sys   string              // the system the person sits at
	env   func(string) string // what the window said of its terminal
	board pick.Board          // the person's clipboard, as this window reaches it

	mu   sync.Mutex
	tail []byte // what follows the next frame: the bell, a notice, a copy
}

// later sends a window bytes of the keeper's own making after its next
// frame, never in the middle of one. Bells in a row are one.
func (w *window) later(b []byte) {
	w.mu.Lock()
	if string(b) != "\a" || !strings.HasSuffix(string(w.tail), "\a") {
		w.tail = append(w.tail, b...)
	}
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func side(v float64) int { return min(max(int(v), 1), maxSide) }

// attach serves a window until its connection ends: what it sends is typed
// into the pane with the keys, and what the keeper composes is sent to it.
func (k *Keeper) attach(l *link, c contract.WireCall) {
	env := func(name string) string {
		for _, v := range c.Env {
			if n, val, _ := strings.Cut(v, "="); n == name {
				return val
			}
		}
		return ""
	}
	// A terminal that says nothing of itself gets the plain marks, and no
	// request to draw in one go.
	known := env("TERM") != "" && env("TERM") != "dumb"
	colours := theme.Get(theme.None).Colours
	if cfg, err := contract.ReadPerson(k.kit.Platform.Peek, k.dirs.Config); err == nil {
		colours = theme.Get(cfg.UI.Theme).Colours
	}
	in := func(name string) term.Style {
		if v, ok := colours[name]; ok {
			return term.Style{Fg: term.RGB(v)}
		}
		return term.Style{}
	}
	bytes, feed := io.Pipe()
	w := &window{link: l, w: side(c.W), h: side(c.H), wake: make(chan struct{}, 1), gone: make(chan struct{}),
		look: layout.Look{Text: in("text"), Dim: in("dim"), Frame: in("frame"), Accent: in("accent"), Plain: !known},
		sys:  c.System, env: env}
	// Only a window that says it is on the keeper's own computer, or comes
	// through connect, is at a clipboard the keeper can be sure of.
	w.board = pick.Board{System: c.System, Connect: c.Reach == contract.ReachConnect,
		Remote: c.Reach != contract.ReachLocal && c.Reach != contract.ReachConnect}
	w.t = term.New(bytes, l, w.w, w.h, term.ModeOf(env))
	w.t.Sync = known
	k.mu.Lock()
	w.in.Keys = input.New(k.set, w.sys)
	k.wins = append(k.wins, w)
	k.use(w)
	if len(k.lay.Tabs) == 0 {
		// A window on nothing is no use to anybody: it gets a first tab.
		k.do(contract.WireCall{Op: contract.OpTabCreate})
	}
	k.mu.Unlock()
	go w.draw(k)
	go k.input(w)
	for {
		kind, body, err := contract.ReadFrame(l.conn)
		if err != nil {
			break
		}
		var c contract.WireCall
		if kind == contract.FrameBytes {
			feed.Write(body)
		} else if json.Unmarshal(body, &c) == nil && c.Op == contract.OpWindowSize {
			k.mu.Lock()
			w.w, w.h = side(c.W), side(c.H)
			k.last = nil
			k.use(w)
			k.mu.Unlock()
		}
	}
	feed.Close()
	close(w.gone)
	k.mu.Lock()
	k.wins = slices.DeleteFunc(k.wins, func(o *window) bool { return o == w })
	if k.last == w {
		k.last = nil
	}
	k.mu.Unlock()
}

// use makes a window the one used last: the panes take its size.
func (k *Keeper) use(w *window) {
	if k.last != w {
		k.last = w
		k.lay.Fit(w.w, w.h)
		k.changed()
	}
}

// draw sends the window a frame whenever something changed and it has taken
// the frame before. A window that is slow holds up nobody but itself, and
// what it gets next is the picture as it is then.
func (w *window) draw(k *Keeper) {
	for {
		select {
		case <-w.wake:
		case <-w.gone:
			return
		}
		began := time.Now()
		k.compose(w)
		err := w.t.Flush()
		w.mu.Lock()
		tail := w.tail
		w.tail = nil
		w.mu.Unlock()
		if err == nil && len(tail) > 0 {
			_, err = w.link.Write(tail)
		}
		if err != nil {
			w.link.conn.Close()
			return
		}
		time.Sleep(frameGap - time.Since(began))
	}
}

// compose draws the tab that shows into a window's grid: the tab row and
// the dividing lines, each pane as the person looks at it, the name of a
// pane program of ours in the line above it, where no program can write,
// the pane drawn over the tab, the cursor of whichever has the keys, and
// the pop-up. A window of another size than the panes have gets the same
// picture cut or padded, and words on the tab row that say so.
func (k *Keeper) compose(w *window) {
	k.mu.Lock()
	defer k.mu.Unlock()
	g := w.t
	if g.W != w.w || g.H != w.h {
		g.Resize(w.w, w.h)
	} else {
		g.Clear()
	}
	k.lay.Draw(g.Grid, w.look)
	g.CurShown = false
	// The cells are the screen's own until its next write, so they are
	// copied under the pane's lock.
	paint := func(p *pane, at layout.Rect, keys bool) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if x, y, on := p.pick.Paint(g.Grid, at, p.scr, w.look.Accent); on && keys {
			g.CurX, g.CurY, g.CurShown = at.X+x, at.Y+y, true
		}
	}
	if tab := k.shown(); tab != nil {
		for _, at := range k.lay.Places(tab) {
			p := k.panes[at.Pane]
			if p == nil {
				continue
			}
			paint(p, at.Rect, at.Pane == tab.Focus && k.over == nil)
			if p.rec.Ours && at.Y > 0 && at.W > 4 {
				g.Put(at.X+1, at.Y-1, at.W-2, " whaleshark "+p.argv[3]+" ", w.look.Accent)
			}
		}
	}
	if o := k.over; o != nil {
		paint(o, overlay.Box(g.Grid, w.look, overlay.Place(w.w, w.h, k.overW, k.overH), strings.Join(o.argv, " ")), true)
	}
	k.pop.Draw(g.Grid, w.look, time.Now())
	words := map[bool]string{true: " keys "}[w.in.Armed] // between the command key and the key after it
	if w.w != k.lay.W || w.h != k.lay.H {
		words = fmt.Sprintf(" sized for another window, %d by %d ", k.lay.W, k.lay.H)
	}
	if words != "" {
		st := w.look.Accent
		st.Reverse = true
		x := max(g.W-term.Width(words), 0)
		g.Put(x, 0, g.W-x, words, st)
	}
}
