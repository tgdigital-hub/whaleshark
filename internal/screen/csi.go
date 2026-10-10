package screen

import (
	"strings"
	"unicode"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// escape obeys ESC and one final byte, with the intermediate byte in s.mid.
func (s *Screen) escape(b byte) {
	s.prev = -1
	switch {
	case s.mid == '(' || s.mid == ')':
		s.lines[s.mid-'('] = b == '0'
	case s.mid != 0:
	case b == '7':
		s.saved[s.alt] = s.cursor
	case b == '8':
		s.cursor = s.saved[s.alt]
	case b == 'D':
		s.hold = false
		s.index()
	case b == 'E':
		s.to(0, s.y)
		s.index()
	case b == 'H':
		s.tabs[s.x] = true
	case b == 'M':
		if s.hold = false; s.y == s.top {
			s.scroll(s.top, s.bot, -1)
		} else if s.y > 0 {
			s.y--
		}
	}
}

// sequence obeys a control sequence whose final byte is b. One with a
// prefix or an intermediate byte is another sequence altogether, whatever
// its final byte: `CSI > 4 ; 2 m` sets a keyboard mode and is no style.
func (s *Screen) sequence(b byte) {
	prev := s.prev
	s.prev = -1
	if s.prefix == '?' && s.mid == 0 && (b == 'h' || b == 'l') {
		for _, n := range s.args[:s.n] {
			s.mode(n, b == 'h')
		}
	}
	if s.prefix != 0 || s.mid != 0 {
		return
	}
	if strings.IndexByte("@JKLMPSTX", b) >= 0 {
		s.hold = false
	}
	n, row := s.arg(0, 1), s.at()-s.x
	switch b {
	case 'A':
		s.up(n)
	case 'B':
		s.down(n)
	case 'C':
		s.to(s.x+n, s.y)
	case 'D':
		s.to(s.x-n, s.y)
	case 'E':
		s.down(n)
		s.x = 0
	case 'F':
		s.up(n)
		s.x = 0
	case 'G', '`':
		s.to(n-1, s.y)
	case 'H', 'f':
		s.place(s.arg(1, 1)-1, n-1)
	case 'd':
		s.place(s.x, n-1)
	case 'J': // 3, which erases the scroll-back, is left alone
		s.settle()
		switch s.arg(0, 0) {
		case 0:
			s.erase(s.at(), len(s.cells))
		case 1:
			s.erase(0, s.at()+1)
		case 2:
			s.erase(0, len(s.cells))
		}
	case 'K':
		switch s.arg(0, 0) {
		case 0:
			s.erase(s.at(), row+s.w)
		case 1:
			s.erase(row, s.at()+1)
		case 2:
			s.erase(row, row+s.w)
		}
	case 'L', 'M':
		if s.y >= s.top && s.y <= s.bot {
			if b == 'L' {
				n = -n
			}
			s.scroll(s.y, s.bot, n)
			s.x = 0
		}
	case '@':
		s.insertCells(n)
	case 'P':
		s.deleteCells(n)
	case 'X':
		s.erase(s.at(), s.at()+min(n, s.w-s.x))
	case 'S':
		s.scroll(s.top, s.bot, n)
	case 'T':
		s.scroll(s.top, s.bot, -n)
	case 'b':
		if prev >= 0 {
			t, wd := s.cells[prev].Text, 1
			if prev+1 < len(s.cells) && s.cells[prev+1].Text == "" {
				wd = 2
			}
			for range min(n, len(s.cells)) {
				s.put(t, wd)
			}
		}
	case 'g':
		if s.arg(0, 0) == 0 {
			s.tabs[s.x] = false
		} else if s.args[0] == 3 {
			clear(s.tabs)
		}
	case 'h', 'l':
		for _, n := range s.args[:s.n] {
			if n == 4 {
				s.insert = b == 'h'
			}
		}
	case 'm':
		s.sgr()
	case 'r':
		if top, bot := n-1, min(s.arg(1, s.h), s.h)-1; top < bot {
			s.top, s.bot = top, bot
			s.place(0, 0)
		}
	case 's':
		s.saved[s.alt] = s.cursor
	case 'u':
		s.cursor = s.saved[s.alt]
	}
}

// mode switches one of the DEC private modes on or off.
func (s *Screen) mode(n int, on bool) {
	switch n {
	case 6:
		s.origin = on
		s.place(0, 0)
	case 7:
		s.wrap, s.hold = on, s.hold && on
	case 25:
		s.shown = on
	case 47:
		s.show(on)
	case 1047:
		if !on && s.alt == 1 {
			s.blank(s.cells)
		}
		s.show(on)
	case 1048, 1049:
		if on {
			s.saved[s.alt] = s.cursor
		}
		if n == 1049 {
			s.show(on)
		}
		if on && n == 1049 {
			s.blank(s.cells)
		} else if !on {
			s.cursor = s.saved[s.alt]
		}
	}
}

// show brings the second screen to the front, or the first.
func (s *Screen) show(second bool) {
	s.settle()
	s.alt = 0
	if second {
		s.alt = 1
	}
	s.cells = s.grids[s.alt]
}

// sgr sets the style from the sequence's numbers.
func (s *Screen) sgr() {
	st := &s.style
	for i := 0; i < s.n; i++ {
		v := max(s.args[i], 0)
		switch {
		case v == 38 || v == 48 || v == 58: // 58 colours an underline: read and dropped
			c, next, ok := s.colour(i)
			if ok && v == 38 {
				st.Fg = c
			} else if ok && v == 48 {
				st.Bg = c
			}
			i = next
			continue
		case v == 0:
			*st = contract.Style{}
		case v == 1:
			st.Bold = true
		case v == 2:
			st.Dim = true
		case v == 3:
			st.Italic = true
		case v == 4, v == 21: // a styled underline is a plain one; 4:0 is none
			st.Underline = !s.sub(i+1) || s.args[i+1] != 0
		case v == 7:
			st.Reverse = true
		case v == 9:
			st.Strike = true
		case v == 22:
			st.Bold, st.Dim = false, false
		case v == 23:
			st.Italic = false
		case v == 24:
			st.Underline = false
		case v == 27:
			st.Reverse = false
		case v == 29:
			st.Strike = false
		case v >= 30 && v <= 37:
			st.Fg = indexed(v - 30)
		case v == 39:
			st.Fg = 0
		case v >= 40 && v <= 47:
			st.Bg = indexed(v - 40)
		case v == 49:
			st.Bg = 0
		case v >= 90 && v <= 97:
			st.Fg = indexed(v - 90 + 8)
		case v >= 100 && v <= 107:
			st.Bg = indexed(v - 100 + 8)
		}
		for s.sub(i + 1) {
			i++
		}
	}
}

// indexed is colour n of the 256.
func indexed(n int) contract.Colour {
	return contract.Indexed(uint8(n)) // #nosec G115 -- every caller passes 0 to 255
}

// sub says whether number i of the sequence followed a colon.
func (s *Screen) sub(i int) bool { return i < s.n && s.colon>>i&1 == 1 }

// colour reads the colour that follows number i of a style: 5 and one of
// the 256, or 2 and red, green and blue. In the colon spelling everything
// joined by colons belongs to it, and a fifth number is the colour space,
// which is passed over. next is the last number that was read.
func (s *Screen) colour(i int) (c contract.Colour, next int, ok bool) {
	end := s.n
	if s.sub(i + 1) {
		for end = i + 1; s.sub(end); end++ {
		}
	}
	a := s.args[min(i+1, end):end]
	for j := range a {
		a[j] = min(max(a[j], 0), 255)
	}
	switch {
	case len(a) >= 2 && a[0] == 5:
		c, ok, next = indexed(a[1]), true, i+2
	case len(a) >= 4 && a[0] == 2:
		if next = i + 4; s.sub(i+1) && len(a) >= 5 {
			a = a[1:]
		}
		c, ok = contract.RGB(uint32(a[1]<<16|a[2]<<8|a[3])), true // #nosec G115 -- each is 0 to 255, set above
	default:
		next = i
	}
	if s.sub(i + 1) {
		next = end - 1
	}
	return c, next, ok
}

// command obeys a finished operating system command. 0, 1 and 2 set the
// title; every other one is read and dropped.
func (s *Screen) command() {
	num, text, _ := strings.Cut(string(s.str), ";")
	if s.bad || num != "0" && num != "1" && num != "2" {
		return
	}
	left := 100
	s.title = strings.Map(func(r rune) rune {
		if left--; left < 0 || !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(text, ""))
}
