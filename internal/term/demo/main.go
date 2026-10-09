// The demo draws a grid of a scheme's sixteen colours and says what was
// clicked, typed or pasted. Tab takes the next scheme; ctrl+c leaves.
//
//	go run ./internal/term/demo
package main

import (
	"fmt"
	"os"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/theme"
)

const across, wide = 4, 12 // cells in a row of the grid, and columns in a cell

func main() {
	t, err := term.Open()
	if err != nil {
		fmt.Fprintln(os.Stderr, "demo:", err)
		os.Exit(1)
	}
	defer t.Close()
	run(t)
}

func run(t *term.Term) {
	var line term.Line
	scheme, said, px, py := theme.Schemes[0], "click a cell, type or paste", -1, -1
	for {
		draw(t, scheme, &line, said, px, py)
		switch ev := <-t.Events; {
		case ev.Kind == term.End || ev.Key == "ctrl+c":
			return
		case ev.Kind == term.Resize:
			t.Resize(ev.W, ev.H)
		case ev.Kind == term.Focus:
			said = fmt.Sprintf("has the keys: %v", ev.Focused)
		case ev.Kind == term.Mouse && ev.Action == term.Move:
			px, py = ev.X, ev.Y
		case ev.Kind == term.Mouse && ev.Action == term.Press:
			button := [...]string{"", "clicked", "middle button on", "wheel up on", "wheel down on"}[ev.Button]
			said = fmt.Sprintf("%s %s at column %d, row %d", button, nameAt(ev.X, ev.Y), ev.X, ev.Y)
			if ev.Y == t.H-2 {
				line.Click(ev.X - 2)
			}
		case ev.Kind == term.Paste:
			line.Key(ev)
			said = fmt.Sprintf("pasted %d cells of text", term.Width(ev.Text))
		case ev.Key == "tab":
			scheme = theme.Get(theme.Next(scheme.Name))
		case ev.Kind == term.KeyPress && !line.Key(ev):
			said = "key " + ev.Key
		}
	}
}

// nameAt is the colour whose cell of the grid is at that place.
func nameAt(x, y int) string {
	if i := (y-1)/2*across + x/wide; y > 0 && x < across*wide && i < len(contract.Colours) {
		return contract.Colours[i]
	}
	return "nothing"
}

func draw(t *term.Term, scheme theme.Scheme, line *term.Line, said string, px, py int) {
	t.Clear()
	colour := func(name string) term.Style {
		if c, ok := scheme.Colours[name]; ok {
			return term.Style{Fg: term.RGB(c)}
		}
		return term.Style{}
	}
	mode := [...]string{"no colour", "256 colours", "full colour"}[t.Mode]
	t.Put(0, 0, t.W, fmt.Sprintf("%s · %dx%d · %s · tab: next scheme", scheme.Name, t.W, t.H, mode), colour("dim"))
	for i, name := range contract.Colours {
		x, y, st := i%across*wide, 1+i/across*2, colour(name)
		st.Reverse = name == nameAt(px, py)
		t.Put(x, y, wide-1, fmt.Sprintf(" %-*s", wide, name), st)
		t.Put(x, y+1, wide-1, " ████▓▓░░ ", st)
	}
	t.Put(0, t.H-2, 2, "> ", colour("accent"))
	line.Draw(t.Grid, 2, t.H-2, t.W-2, colour("text"), true)
	t.Put(0, t.H-1, t.W, said, colour("needs"))
	t.Flush()
}
