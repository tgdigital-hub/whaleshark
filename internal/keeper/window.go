package keeper

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/layout"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/theme"
)

// A window gets no more frames than this, however fast panes print.
const frameGap = 16 * time.Millisecond

// window is one person's terminal, as the keeper draws it. w and h are its
// size, under the keeper's lock; the grid of t is its drawing goroutine's.
type window struct {
	link *link
	t    *term.Term
	w, h int
	look layout.Look
	wake chan struct{}
	gone chan struct{}
	bell atomic.Bool
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
		look: layout.Look{Text: in("text"), Dim: in("dim"), Frame: in("frame"), Accent: in("accent"), Plain: !known}}
	w.t = term.New(bytes, l, w.w, w.h, term.ModeOf(env))
	w.t.Sync = known
	k.mu.Lock()
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
		if err == nil && w.bell.Swap(false) {
			_, err = w.link.Write([]byte{'\a'})
		}
		if err != nil {
			w.link.conn.Close()
			return
		}
		time.Sleep(frameGap - time.Since(began))
	}
}

// compose draws the tab that shows into a window's grid: the tab row and
// the dividing lines, each pane's picture in its place, and the cursor of
// the pane with the keys. A window of another size than the panes have gets
// the same picture cut or padded, and words on the tab row that say so.
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
	if tab := k.shown(); tab != nil {
		for _, at := range k.lay.Places(tab) {
			p := k.panes[at.Pane]
			if p == nil {
				continue
			}
			// The cells are the screen's own until its next write, so
			// they are copied under the pane's lock.
			p.mu.Lock()
			pic := p.scr.Picture()
			g.Paint(at.X, at.Y, at.W, at.H, pic)
			if at.Pane == tab.Focus && pic.CurShown && pic.CurX < at.W && pic.CurY < at.H {
				g.CurX, g.CurY, g.CurShown = at.X+pic.CurX, at.Y+pic.CurY, true
			}
			p.mu.Unlock()
		}
	}
	words := ""
	if time.Since(k.noteAt) < noteFor {
		words = k.note
	} else if w.w != k.lay.W || w.h != k.lay.H {
		words = fmt.Sprintf("sized for another window, %d by %d", k.lay.W, k.lay.H)
	}
	if words != "" {
		st := w.look.Accent
		st.Reverse = true
		x := max(g.W-term.Width(words)-2, 0)
		g.Put(x, 0, g.W-x, " "+words+" ", st)
	}
}

// What follows stands in for the input package until it is built: keys and
// pastes go to the pane with the keys in the plain form every program
// reads, and the left button shows a tab, gives a pane the keys or drags a
// dividing line. Nothing is sent in the form a program asked for yet.

// keys are the keys that are no character, as a terminal sends them.
var keys = map[string]string{"enter": "\r", "tab": "\t", "esc": "\x1b", "space": " ", "backspace": "\x7f",
	"up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D", "home": "\x1b[H", "end": "\x1b[F",
	"insert": "\x1b[2~", "delete": "\x1b[3~", "pageup": "\x1b[5~", "pagedown": "\x1b[6~"}

// typed is a key press as bytes for a program; none for a key held with Cmd.
func typed(ev term.Event) string {
	if ev.Rune != 0 {
		return string(ev.Rune)
	}
	key, ctrl := strings.CutPrefix(ev.Key, "ctrl+")
	key, alt := strings.CutPrefix(key, "alt+")
	key, shift := strings.CutPrefix(key, "shift+")
	out := keys[key]
	switch {
	case shift && key == "tab":
		out = "\x1b[Z"
	case ctrl && key == "space":
		out = "\x00"
	case ctrl && len(key) == 1 && key[0] >= '@':
		out = string(rune(key[0] & 0x1f))
	case out == "" && utf8.RuneCountInString(key) == 1:
		out = key
	}
	if alt && out != "" {
		out = "\x1b" + out
	}
	return out
}

// input takes what a window's terminal reported, until its input ends.
func (k *Keeper) input(w *window) {
	var drag layout.Hit
	for ev := range w.t.Events {
		out := ""
		k.mu.Lock()
		switch ev.Kind {
		case term.End:
			k.mu.Unlock()
			return
		case term.KeyPress:
			k.use(w)
			out = typed(ev)
		case term.Paste:
			// No program is given marks yet, so a line break is Enter.
			k.use(w)
			out = strings.NewReplacer("\r\n", "\r", "\n", "\r").Replace(ev.Text)
		case term.Mouse:
			if k.mouse(w, ev, &drag) {
				k.changed()
			}
		}
		var tty contract.Pty
		if p := k.panes[k.focus]; p != nil && out != "" {
			tty = p.tty
		}
		k.mu.Unlock()
		if tty != nil {
			io.WriteString(tty, out)
		}
	}
}

// mouse acts on the engine's own parts and reports whether anything moved.
func (k *Keeper) mouse(w *window, ev term.Event, drag *layout.Hit) bool {
	switch {
	case ev.Action == term.Move && ev.Button == term.Left && drag.Kind == layout.OnLine:
		k.lay.Drag(*drag, ev.X, ev.Y)
		return true
	case ev.Action != term.Press:
		*drag = layout.Hit{}
		return false
	}
	k.use(w)
	wheel := map[term.Button]int{term.WheelUp: -1, term.WheelDown: 1}[ev.Button]
	switch h := k.lay.At(ev.X, ev.Y); {
	case wheel != 0 && ev.Y == 0:
		k.lay.Wheel(wheel)
	case ev.Button != term.Left:
		return false
	case h.Kind == layout.OnTab:
		k.lay.Show(h.Tab)
	case h.Kind == layout.OnMore:
		k.lay.Wheel(h.Step)
	case h.Kind == layout.OnLine:
		*drag = h
	case h.Kind == layout.OnPane:
		k.lay.Focus(h.Pane)
	default:
		return false
	}
	return true
}
