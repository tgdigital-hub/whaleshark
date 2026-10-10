// Package overlay is the pop-up, the bell, and a pane drawn over a tab.
//
// The keeper holds one Popup: Show takes a notice and answers honestly,
// Draw puts the box into each window's grid while it lasts, and Signal is
// what a window's own terminal is sent with it. Place and Box are the place
// and the frame of a pane drawn over a tab; the pane's life is the keeper's.
// The notice sequences are the ones their terminals' own pages give: iTerm2
// and Ghostty "OSC 9", kitty "OSC 99".
package overlay

import (
	"fmt"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/layout"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// Plug binds this package's part into the kit: it has no command and stands
// behind no interface. The keeper draws with it.
func Plug(*contract.Kit) {}

const (
	// For is how long a pop-up stays.
	For = 5 * time.Second
	// The most cells a pop-up is wide, and the most lines of its body.
	mostW, mostLines = 44, 3
)

// Popup is the notice in the top corner of every attached window. The zero
// value shows nothing.
type Popup struct {
	Title, Body string
	Until       time.Time
}

// Show takes a notice and answers as Notify must: shown only when the
// pop-up is switched on in the person's own settings and a window is
// attached to draw it in. The words are cleaned of anything that could
// steer a terminal.
func (p *Popup) Show(title, body string, on bool, windows int, now time.Time) (reason, delivery string) {
	switch {
	case !on:
		return contract.NotifyOff, contract.NotifyOff
	case windows == 0:
		return contract.NotifyNoWindow, "on"
	}
	*p = Popup{term.Clean(title), term.Clean(body), now.Add(For)}
	return contract.NotifyShown, "on"
}

// Draw puts the box into the top right corner of a window's grid, under
// the tab row, and reports whether the pop-up still lasts.
func (p *Popup) Draw(g *term.Grid, look layout.Look, now time.Time) bool {
	if !now.Before(p.Until) {
		return false
	}
	var lines []string
	for _, word := range strings.Fields(p.Body) {
		if n := len(lines); n > 0 && term.Width(lines[n-1]+" "+word) <= mostW-4 {
			lines[n-1] += " " + word
		} else if n == mostLines {
			lines[n-1] += " …"
			break
		} else {
			lines = append(lines, word)
		}
	}
	w := term.Width(p.Title)
	for _, l := range lines {
		w = max(w, term.Width(l))
	}
	w = min(w+4, mostW, g.W)
	in := Box(g, look, layout.Rect{X: g.W - w, Y: 1, W: w, H: len(lines) + 2}, p.Title)
	for i, l := range lines {
		g.Put(in.X+1, in.Y+i, in.W-2, l, look.Text)
	}
	return true
}

// Signal is what a window's own terminal is sent with a notice: the notice
// sequence of a terminal known to show one on the person's desktop, which
// reaches their own computer over ssh, and the bell when a sound is asked
// for. env reads the variables the window passed when it attached; a
// terminal that names itself in neither gets the bell alone.
func Signal(title, body string, sound bool, env func(string) string) []byte {
	var b []byte
	title, body = term.Clean(title), term.Clean(body)
	switch prog := env("TERM_PROGRAM"); {
	case env("TERM") == "xterm-kitty":
		// Three parts under one name: the title, the body, and done.
		b = fmt.Appendf(b, "\x1b]99;i=ws:d=0:p=title;%s\x1b\\\x1b]99;i=ws:d=0:p=body;%s\x1b\\\x1b]99;i=ws:d=1;\x1b\\", title, body)
	case prog == "iTerm.app", prog == "ghostty":
		b = fmt.Appendf(b, "\x1b]9;%s: %s\x1b\\", title, body)
	}
	if sound {
		b = append(b, '\a')
	}
	return b
}

// Place is where a pane drawn over a tab stands in a window of W by H
// cells, frame included: in the middle, under the tab row. w and h are
// shares of the window, or above 1 the cells of the pane's inside.
func Place(W, H int, w, h float64) layout.Rect {
	side := func(all int, v float64) int {
		if v > 1 {
			return min(max(int(v)+2, 3), all)
		}
		return min(max(int(v*float64(all)), 3), all)
	}
	r := layout.Rect{W: side(W, w), H: side(H-1, h)}
	r.X, r.Y = (W-r.W)/2, 1+(H-1-r.H)/2
	return r
}

// Box draws a frame in the accent with a title in its top line, blanks
// what is in it and returns its inside. The characters are Unicode's box
// drawings, and those of plain text where a terminal cannot show them.
func Box(g *term.Grid, look layout.Look, r layout.Rect, title string) layout.Rect {
	c := []string{"┌", "─", "┐", "│", " ", "│", "└", "─", "┘"}
	if look.Plain {
		c = []string{"+", "-", "+", "|", " ", "|", "+", "-", "+"}
	}
	for y := range max(r.H, 0) {
		k := c[3:]
		if y == 0 {
			k = c
		} else if y == r.H-1 {
			k = c[6:]
		}
		g.Put(r.X, r.Y+y, r.W, k[0]+strings.Repeat(k[1], max(r.W-2, 0))+k[2], look.Accent)
	}
	st := look.Accent
	st.Bold = true
	g.Put(r.X+2, r.Y, r.W-4, title, st)
	return layout.Rect{X: r.X + 1, Y: r.Y + 1, W: max(r.W-2, 0), H: max(r.H-2, 0)}
}
