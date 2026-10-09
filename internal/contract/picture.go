package contract

import "strings"

// Colour is the colour of a cell. The zero value is the terminal's own. RGB
// gives one of the full range; Indexed one of the 256 by its number, so the
// first sixteen stay the colours the person chose for their terminal.
type Colour uint32

// RGB is the colour 0xRRGGBB.
func RGB(v uint32) Colour { return Colour(v&0xffffff | 1<<24) }

// Indexed is colour n of the 256.
func Indexed(n uint8) Colour { return Colour(n) | 2<<24 }

// Style is how a cell is drawn. A styled underline counts as a plain one.
type Style struct {
	Fg, Bg                                        Colour
	Bold, Dim, Italic, Underline, Reverse, Strike bool
}

// Cell is one place on a screen. Text is one character as a person sees it
// and never a control character; it is empty in the right half of a
// character two cells wide, and a space where nothing was ever written.
type Cell struct {
	Text  string
	Style Style
}

// Picture is what one pane shows: H rows of W cells, row after row, and
// where the cursor is. The screen reader fills it; the terminal layer draws it.
type Picture struct {
	W, H       int
	Cells      []Cell
	CurX, CurY int
	CurShown   bool
}

// NewPicture returns a picture of blanks.
func NewPicture(w, h int) *Picture {
	p := &Picture{W: max(w, 0), H: max(h, 0)}
	p.Cells = make([]Cell, p.W*p.H)
	for i := range p.Cells {
		p.Cells[i].Text = " "
	}
	return p
}

// Text is the picture as lines of text, blanks at the end of a line and
// empty lines at the end left out. A cell nothing was written to reads as a
// space: a program may place every word with a cursor move and print none.
func (p *Picture) Text() string {
	rows := make([]string, p.H)
	for y := range rows {
		var b strings.Builder
		for _, c := range p.Cells[y*p.W : (y+1)*p.W] {
			b.WriteString(c.Text)
		}
		rows[y] = strings.TrimRight(b.String(), " ")
	}
	return strings.TrimRight(strings.Join(rows, "\n"), "\n")
}
