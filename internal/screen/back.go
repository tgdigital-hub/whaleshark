package screen

import (
	"encoding/binary"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// The scroll-back and a new size. A line that left the top of the first
// screen is kept packed: a letter of the first 128 is its own byte, any
// other character one byte more than its text, a change of style two to
// ten bytes, and the blanks at a line's end nothing. That is about a byte
// a cell for what programs print.

// maxBack bounds the bytes of one screen's scroll-back, whatever the lines hold.
const maxBack = 8 << 20

const (
	styled = 1    // a style follows: its switches, then each colour it has
	long   = 0x80 // a text of 1 to 64 bytes follows; with wide, in two cells
	wide   = 0x40
	hasFg  = 0x40
	hasBg  = 0x80
)

// Scrollback sets how many lines are kept; 0 keeps none.
func (s *Screen) Scrollback(lines int) {
	for s.keep = max(lines, 0); s.Lines() > s.keep; {
		s.drop()
	}
}

// Lines is how many lines of scroll-back there are.
func (s *Screen) Lines() int { return len(s.back) - s.first }

// Line is line i of the scroll-back, the oldest being 0, as the cells it
// had without the blanks at its end. It keeps the width it was printed at.
func (s *Screen) Line(i int) []contract.Cell {
	if i < 0 || i >= s.Lines() {
		return nil
	}
	return unpack(s.back[s.first+i])
}

// View is the picture moved back lines up into the scroll-back, for a
// person scrolling: the lines above the screen, then as much of the screen
// as still fits. The cells are the caller's, unless back is 0.
func (s *Screen) View(back int) *contract.Picture {
	p := s.Picture()
	if back = min(back, s.Lines()); back <= 0 {
		return p
	}
	v := contract.NewPicture(p.W, p.H)
	v.CurX, v.CurY, v.CurShown = p.CurX, p.CurY+back, p.CurShown && p.CurY+back < p.H
	n := min(back, p.H)
	for y := range n {
		row := unpack(s.back[len(s.back)-back+y])
		if copy(v.Cells[y*p.W:(y+1)*p.W], row); len(row) > p.W && row[p.W].Text == "" {
			v.Cells[(y+1)*p.W-1].Text = " "
		}
	}
	copy(v.Cells[n*p.W:], p.Cells)
	return v
}

// push adds a row to the scroll-back, and lets the oldest lines go when
// there are too many of them.
func (s *Screen) push(row []contract.Cell) {
	if s.keep == 0 {
		return
	}
	var b []byte
	if s.Lines() == s.keep {
		b = s.drop()
	}
	n := len(row)
	var st contract.Style
	for n > 0 && row[n-1].Text == " " && row[n-1].Style == st {
		n--
	}
	for i, c := range row[:n] {
		if c.Text == "" {
			continue
		}
		if c.Style != st {
			st = c.Style
			b = append(b, styled, 0)
			f := &b[len(b)-1]
			for j, on := range [...]bool{st.Bold, st.Dim, st.Italic, st.Underline, st.Reverse, st.Strike} {
				if on {
					*f |= 1 << j
				}
			}
			if st.Fg != 0 {
				*f |= hasFg
				b = binary.LittleEndian.AppendUint32(b, uint32(st.Fg))
				f = &b[len(b)-5]
			}
			if st.Bg != 0 {
				*f |= hasBg
				b = binary.LittleEndian.AppendUint32(b, uint32(st.Bg))
			}
		}
		two := i+1 < len(row) && row[i+1].Text == ""
		if len(c.Text) == 1 && !two {
			b = append(b, c.Text[0])
			continue
		}
		head := long | byte(len(c.Text)-1) // #nosec G115 -- a cell's text is 1 to 64 bytes
		if two {
			head |= wide
		}
		b = append(append(b, head), c.Text...)
	}
	s.back, s.packed = append(s.back, b), s.packed+len(b)
	for s.packed > maxBack {
		s.drop()
	}
}

// drop lets the oldest line go and returns its room for the next.
func (s *Screen) drop() []byte {
	b := s.back[s.first]
	s.back[s.first] = nil
	s.first++
	s.packed -= len(b)
	if s.first > len(s.back)/2 {
		s.back = s.back[:copy(s.back, s.back[s.first:])]
		s.first = 0
	}
	return b[:0]
}

// unpack is a packed line as cells.
func unpack(b []byte) []contract.Cell {
	var st contract.Style
	row := make([]contract.Cell, 0, len(b))
	for i := 0; i < len(b); i++ {
		switch h := b[i]; {
		case h == styled:
			f := b[i+1]
			i++
			st = contract.Style{Bold: f&1 != 0, Dim: f&2 != 0, Italic: f&4 != 0, Underline: f&8 != 0, Reverse: f&16 != 0, Strike: f&32 != 0}
			if f&hasFg != 0 {
				st.Fg = contract.Colour(binary.LittleEndian.Uint32(b[i+1:]))
				i += 4
			}
			if f&hasBg != 0 {
				st.Bg = contract.Colour(binary.LittleEndian.Uint32(b[i+1:]))
				i += 4
			}
		case h < long:
			row = append(row, contract.Cell{Text: ascii[h-0x20 : h-0x1f], Style: st})
		default:
			n := int(h&0x3f) + 1
			row = append(row, contract.Cell{Text: string(b[i+1 : i+1+n]), Style: st})
			if i += n; h&wide != 0 {
				row = append(row, contract.Cell{Style: st})
			}
		}
	}
	return row
}

// stops sets a tab stop at every eighth column from x on.
func stops(tabs []bool, x int) {
	for ; x < len(tabs); x += 8 {
		tabs[x] = true
	}
}

// Resize gives the screen a new size. Both screens are cut or padded at
// the right and at the bottom; if the cursor of the first would be cut off,
// rows leave its top for the scroll-back instead, so that the line being
// typed stays. Lines already in the scroll-back keep their width.
func (s *Screen) Resize(w, h int) {
	if w, h = min(max(w, 1), maxSide), min(max(h, 1), maxSide); w == s.w && h == s.h {
		return
	}
	s.settle()
	first := &s.cursor // the first screen's cursor
	if s.alt == 1 {
		first = &s.saved[0]
	}
	up := max(first.y-h+1, 0)
	for g, old := range s.grids {
		skip := 0
		if g == 0 {
			for skip < up {
				s.push(old[skip*s.w : (skip+1)*s.w])
				skip++
			}
		}
		cells := contract.NewPicture(w, h).Cells
		for y := 0; y < h && y+skip < s.h; y++ {
			row := old[(y+skip)*s.w : (y+skip+1)*s.w]
			if copy(cells[y*w:(y+1)*w], row); w < s.w && row[w].Text == "" {
				cells[(y+1)*w-1].Text = " "
			}
		}
		s.grids[g] = cells
	}
	if s.saved[0].y = max(s.saved[0].y-up, 0); s.alt == 0 {
		s.y -= up
	}
	for _, c := range []*cursor{&s.cursor, &s.saved[0], &s.saved[1]} {
		c.x, c.y, c.hold = min(c.x, w-1), min(max(c.y, 0), h-1), false
	}
	if s.bot >= s.h-1 || s.bot >= h {
		s.bot = h - 1
	}
	if s.top >= s.bot {
		s.top = 0
	}
	tabs := make([]bool, w)
	stops(tabs, max(copy(tabs, s.tabs)+7, 8)/8*8)
	s.w, s.h, s.tabs, s.cells, s.prev, s.since = w, h, tabs, s.grids[s.alt], -1, time.Time{}
}
