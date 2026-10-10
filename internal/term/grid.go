package term

import (
	"strings"

	"github.com/rivo/uniseg"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

var blank = Cell{Text: " "}

// Grid is what a program draws into: H rows of W cells.
type Grid struct {
	W, H  int
	cells []Cell
}

func NewGrid(w, h int) *Grid {
	g := &Grid{}
	g.Resize(w, h)
	return g
}

// Resize gives the grid a new size and empties it.
func (g *Grid) Resize(w, h int) {
	g.W, g.H = max(w, 0), max(h, 0)
	g.cells = make([]Cell, g.W*g.H)
	g.Clear()
}

func (g *Grid) Clear() {
	for i := range g.cells {
		g.cells[i] = blank
	}
}

// At returns the cell at column x of row y.
func (g *Grid) At(x, y int) Cell { return g.cells[y*g.W+x] }

// Put draws s from column x of row y in at most w cells, cut at the last
// character that fits, and returns the cells it took. The text is cleaned
// first, so nothing drawn can carry a command for the terminal.
func (g *Grid) Put(x, y, w int, s string, st Style) int {
	if x < 0 || y < 0 || y >= g.H {
		return 0
	}
	start, end := x, min(x+w, g.W)
	s = Clean(s)
	for state := -1; s != ""; {
		var c string
		var cw int
		c, s, cw, state = uniseg.FirstGraphemeClusterInString(s, state)
		if cw == 0 {
			continue
		}
		if x+cw > end {
			break
		}
		g.set(x, y, Cell{Text: c, Style: st})
		if cw == 2 {
			g.set(x+1, y, Cell{Style: st})
		}
		x += cw
	}
	return x - start
}

// Paint copies a pane's picture to column x of row y, in at most w columns
// and h rows: a picture that is larger is cut, and around one that is
// smaller the grid stays as it was. A cell's text is cleaned as Put's is.
func (g *Grid) Paint(x, y, w, h int, p *contract.Picture) {
	if x < 0 || y < 0 {
		return
	}
	w, h = min(w, p.W, g.W-x), min(h, p.H, g.H-y)
	for row := range h {
		for col := range w {
			i := row*p.W + col
			c := p.Cells[i]
			if t := c.Text; len(t) != 1 || t[0] < 0x20 || t[0] == 0x7f {
				// Only the right half of a wide character may be empty.
				if c.Text = Clean(t); c.Text == "" && (t != "" || col == 0) {
					c.Text = " "
				}
			}
			// A wide character the cut would halve is a blank.
			if col == w-1 && col+1 < p.W && p.Cells[i+1].Text == "" {
				c.Text = " "
			}
			g.set(x+col, y+row, c)
		}
	}
}

// set writes one cell, and blanks the other half of a wide character that
// the write cuts in two.
func (g *Grid) set(x, y int, c Cell) {
	i := y*g.W + x
	if g.cells[i].Text == "" && x > 0 {
		g.cells[i-1].Text = " "
	}
	if x+1 < g.W && g.cells[i+1].Text == "" {
		g.cells[i+1].Text = " "
	}
	g.cells[i] = c
}

// Row is the text of row y without the spaces at its end.
func (g *Grid) Row(y int) string {
	var b strings.Builder
	for _, c := range g.cells[y*g.W : (y+1)*g.W] {
		b.WriteString(c.Text)
	}
	return strings.TrimRight(b.String(), " ")
}

// Find returns the first cell of the first place the text stands.
func (g *Grid) Find(text string) (x, y int, ok bool) {
	for y := range g.H {
		row := g.Row(y)
		if i := strings.Index(row, text); i >= 0 {
			return uniseg.StringWidth(row[:i]), y, true
		}
	}
	return 0, 0, false
}
