package screen

import (
	"slices"
	"unicode/utf8"

	"github.com/rivo/uniseg"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// The bounds of a screen and of one cell's text. The longest character a
// person sees that is in use today, a family of four with skin tones, is
// under 64 bytes; marks piled on a letter past that are left out.
const (
	maxSide = 1000
	maxCell = 64
)

// cursor is what "save the cursor" saves: the place, the style, the wrap
// held at the last column, origin mode and the character sets.
type cursor struct {
	x, y   int
	style  contract.Style
	hold   bool
	origin bool
	lines  [2]bool // the line-drawing set is in G0, in G1
	shift  int     // which of the two is in use
}

// Screen reads what one program prints into a picture. It is not safe for
// use from two goroutines at once.
type Screen struct {
	reader
	cursor
	w, h     int
	grids    [2][]contract.Cell // the first screen and the second
	alt      int                // which is shown
	cells    []contract.Cell    // grids[alt]
	base     int                // the row of cells that is the top row of the screen: see scroll
	saved    [2]cursor
	top, bot int // the scrolling region, both rows inside it
	tabs     []bool
	wrap     bool
	insert   bool
	shown    bool
	prev     int    // the cell of the character printed last while a mark can still join it, else -1
	part     []byte // the start of a character whose end has not arrived
	title    string
}

// New returns a blank screen of w columns and h rows.
func New(w, h int) *Screen {
	w, h = min(max(w, 1), maxSide), min(max(h, 1), maxSide)
	s := &Screen{w: w, h: h, bot: h - 1, wrap: true, shown: true, prev: -1, tabs: make([]bool, w)}
	for i := range s.grids {
		s.grids[i] = contract.NewPicture(w, h).Cells
	}
	s.cells = s.grids[0]
	for x := 8; x < w; x += 8 {
		s.tabs[x] = true
	}
	return s
}

// Picture is what the screen shows now. Its cells are the screen's own:
// they change with the next Write.
func (s *Screen) Picture() *contract.Picture {
	s.settle()
	return &contract.Picture{W: s.w, H: s.h, Cells: s.cells, CurX: s.x, CurY: s.y, CurShown: s.shown}
}

// Title is the title the program set, with nothing in it but visible characters.
func (s *Screen) Title() string { return s.title }

// Write reads what the program printed. It takes everything and never fails.
func (s *Screen) Write(p []byte) (int, error) {
	for i := 0; i < len(p); {
		j := i
		for s.state == ground && j < len(p) && p[j] >= 0x20 && p[j] != 0x7f {
			j++
		}
		if j > i {
			s.print(p[i:j])
			i = j
			continue
		}
		if len(s.part) > 0 && s.state == ground {
			for range s.part {
				s.char(utf8.RuneError)
			}
			s.part = nil
		}
		s.feed(p[i])
		i++
	}
	return len(p), nil
}

// print draws a run of text, keeping back a character cut in two by a read.
func (s *Screen) print(p []byte) {
	if len(s.part) > 0 {
		p = append(s.part, p...)
		s.part = nil
	}
	for len(p) > 0 {
		r, n := rune(p[0]), 1
		if r >= 0x80 {
			if !utf8.FullRune(p) {
				s.part = append(s.part, p...)
				return
			}
			r, n = utf8.DecodeRune(p)
		}
		s.char(r)
		p = p[n:]
	}
}

const ascii = " !\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~"

// lines is the VT100's special graphics set for the codes 0x60 to 0x7e
// (VT100 User Guide, table 3-9).
var lines = [...]string{"◆", "▒", "␉", "␌", "␍", "␊", "°", "±", "␤", "␋", "┘", "┐", "┌", "└", "┼", "⎺",
	"⎻", "─", "⎼", "⎽", "├", "┤", "┴", "┬", "│", "≤", "≥", "π", "≠", "£", "·"}

// char draws one code point: joined to the character before it when the
// two are one character to a person, else in a cell or two of its own.
func (s *Screen) char(r rune) {
	var t string
	switch {
	case r >= 0xa0:
		t = string(r)
	case r >= 0x80: // a control character of the upper set is never drawn
		return
	case r >= 0x60 && s.lines[s.shift]:
		t = lines[r-0x60]
	default:
		t = ascii[r-0x20 : r-0x1f]
	}
	// A plain letter after a plain letter joins nothing; no other pair is
	// decided here.
	wd := 1
	if r >= 0x80 || s.prev >= 0 && len(s.cells[s.prev].Text) > 1 {
		if s.prev >= 0 && s.join(t) {
			return
		}
		wd = uniseg.StringWidth(t)
	}
	if wd > 0 && wd <= s.w {
		s.put(t, wd)
	}
}

// join adds t to the character printed last if the two make one, and says
// whether they did. A character that becomes wide by it takes the next cell.
func (s *Screen) join(t string) bool {
	c := &s.cells[s.prev]
	whole := c.Text + t
	if first, _, _, _ := uniseg.FirstGraphemeClusterInString(whole, -1); len(first) != len(whole) {
		return false
	}
	if len(whole) > maxCell {
		return true
	}
	c.Text = whole
	if x := s.prev % s.w; x < s.w-1 && s.cells[s.prev+1].Text != "" && uniseg.StringWidth(whole) == 2 {
		s.cut(s.prev + 2)
		s.cells[s.prev+1] = contract.Cell{Style: c.Style}
		if s.x == x+1 && s.at()/s.w == s.prev/s.w {
			s.step(1)
		}
	}
	return true
}

// put writes a character of wd cells at the cursor and moves past it. At
// the last column the cursor stays and the wrap is held until the next
// character, which is what every terminal since the VT100 does.
func (s *Screen) put(t string, wd int) {
	if s.hold || wd == 2 && s.x == s.w-1 && s.wrap {
		s.x, s.hold = 0, false
		s.index()
	}
	s.x = min(s.x, s.w-wd)
	if s.insert {
		s.insertCells(wd)
	}
	i := s.at()
	s.cut(i)
	s.cut(i + wd)
	s.cells[i] = contract.Cell{Text: t, Style: s.style}
	if wd == 2 {
		s.cells[i+1] = contract.Cell{Style: s.style}
	}
	s.prev = i
	s.step(wd)
}

// step moves the cursor n cells on after a character.
func (s *Screen) step(n int) {
	if s.x+n >= s.w {
		s.x, s.hold = s.w-1, s.wrap
	} else {
		s.x += n
	}
}

// cut makes cell i a place where a row may be cut: a wide character lying
// across it becomes two blanks, so that no half is ever left alone.
func (s *Screen) cut(i int) {
	if i%s.w != 0 && i < len(s.cells) && s.cells[i].Text == "" {
		s.cells[i-1].Text, s.cells[i].Text = " ", " "
	}
}

// blank empties cells. They take the background in force, as xterm's do.
func (s *Screen) blank(cells []contract.Cell) {
	for i := range cells {
		cells[i] = contract.Cell{Text: " ", Style: contract.Style{Bg: s.style.Bg}}
	}
}

// erase empties the cells from a up to b, counted from the top left.
func (s *Screen) erase(a, b int) {
	s.cut(a)
	s.cut(b)
	s.blank(s.cells[a:b])
}

// at is the cursor's cell.
func (s *Screen) at() int { return (s.y+s.base)%s.h*s.w + s.x }

// settle puts the rows of cells in the order of the screen's rows. Three
// reversals turn a list round in place, a well-known way.
func (s *Screen) settle() {
	if k := s.base * s.w; k > 0 {
		slices.Reverse(s.cells[:k])
		slices.Reverse(s.cells[k:])
		slices.Reverse(s.cells)
		if s.base = 0; s.prev >= 0 {
			s.prev = (s.prev - k + len(s.cells)) % len(s.cells)
		}
	}
}

// insertCells moves the rest of the cursor's row n cells to the right.
func (s *Screen) insertCells(n int) {
	n = min(n, s.w-s.x)
	i, end := s.at(), s.at()-s.x+s.w
	s.cut(i)
	s.cut(end - n)
	copy(s.cells[i+n:end], s.cells[i:])
	s.blank(s.cells[i : i+n])
}

// deleteCells takes n cells out of the cursor's row.
func (s *Screen) deleteCells(n int) {
	n = min(n, s.w-s.x)
	i, end := s.at(), s.at()-s.x+s.w
	s.cut(i)
	s.cut(i + n)
	copy(s.cells[i:end], s.cells[i+n:end])
	s.blank(s.cells[end-n : end])
}

// scroll moves the rows top to bot up by n, or down by -n; what leaves is
// lost. The whole screen moving up a row is what a pane does all day, so
// it moves no cell: the top of the screen becomes the next row of cells,
// and the row that left is the new bottom.
func (s *Screen) scroll(top, bot, n int) {
	if n == 1 && top == 0 && bot == s.h-1 {
		s.blank(s.cells[s.base*s.w : (s.base+1)*s.w])
		s.base = (s.base + 1) % s.h
		return
	}
	s.settle()
	a, b := top*s.w, (bot+1)*s.w
	k := min(max(n, -n), bot-top+1) * s.w
	if n > 0 {
		copy(s.cells[a:b], s.cells[a+k:b])
		s.blank(s.cells[b-k : b])
	} else {
		copy(s.cells[a+k:b], s.cells[a:b])
		s.blank(s.cells[a : a+k])
	}
}

// to moves the cursor, kept inside the screen.
func (s *Screen) to(x, y int) {
	s.x, s.y, s.hold = min(max(x, 0), s.w-1), min(max(y, 0), s.h-1), false
}

// place moves the cursor to a row counted as the program counts it: from the
// top of the scrolling region, and kept inside it, in origin mode.
func (s *Screen) place(x, y int) {
	if s.origin {
		y = min(y+s.top, s.bot)
	}
	s.to(x, y)
}

// up and down stop at the edge of the scrolling region when they start inside it.
func (s *Screen) up(n int) {
	stop := 0
	if s.y >= s.top {
		stop = s.top
	}
	s.to(s.x, max(s.y-n, stop))
}

func (s *Screen) down(n int) {
	stop := s.h - 1
	if s.y <= s.bot {
		stop = s.bot
	}
	s.to(s.x, min(s.y+n, stop))
}

// index is a line feed: down a row, and at the bottom of the region the
// region scrolls up.
func (s *Screen) index() {
	if s.y == s.bot {
		s.scroll(s.top, s.bot, 1)
	} else if s.y < s.h-1 {
		s.y++
	}
}

// control obeys one control character.
func (s *Screen) control(b byte) {
	s.prev = -1
	switch b {
	case '\b':
		s.to(s.x-1, s.y)
	case '\t':
		for s.x < s.w-1 {
			if s.x++; s.tabs[s.x] {
				break
			}
		}
	case '\n', '\v', '\f':
		s.hold = false
		s.index()
	case '\r':
		s.to(0, s.y)
	case 0x0e, 0x0f:
		s.shift = int(0x0f - b)
	}
}
