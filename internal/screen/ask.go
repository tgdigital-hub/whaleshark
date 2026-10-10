package screen

import (
	"fmt"
	"time"
)

// What a program is told about the terminal, what it asked of it, and what
// it does that is no drawing. The questions and their answers are in XTerm
// Control Sequences and the VT220 Programmer Reference; the hold is the
// public page "Terminal Spec: Synchronized Output"; the extended keyboard's
// levels are on the page of the kitty keyboard protocol.

// Version is what follows the engine's name in its answer to "what
// terminal are you". The program sets it once, before any screen is made.
var Version = "0"

// holdFor is how long a frame a program began and never finished is kept
// back. The page of the hold gives no figure; a program that dies inside a
// frame must not freeze its pane.
const holdFor = time.Second

const (
	maxEvents = 16
	maxKeys   = 16
)

// Modes is what the program asked of the terminal, for whoever hands it the
// keys, the mouse and pasted text.
type Modes struct {
	CursorKeys bool // the arrow keys in their application form (mode 1)
	Keypad     bool // the keypad in its application form (ESC =)
	Mouse      int  // which mouse events it wants: 0 for none, or 1000, 1002, 1003
	MouseSGR   bool // in the form with exact positions (1006)
	Focus      bool // told when the pane gets and loses the keys (1004)
	Paste      bool // pasted text between its two marks (2004)
	WheelKeys  bool // the wheel as arrow keys on the second screen (1007)
	Keys       int  // the level of the extended keyboard form: 0 or 1
	Second     bool // the second screen is showing
}

// Modes is what the program has asked for so far.
func (s *Screen) Modes() Modes {
	m := s.modes
	m.Second = s.alt == 1
	return m
}

// Kind is what an Event is.
type Kind uint8

const (
	Bell Kind = iota + 1 // the program rang the bell
	Copy                 // it asks for Text to be put on the person's clipboard
)

// Event is something a program did that draws nothing.
type Event struct {
	Kind Kind
	Text string
}

// Events returns what happened since it was last called. At most sixteen
// wait; a bell after a bell is the same bell.
func (s *Screen) Events() []Event {
	e := s.events
	s.events = nil
	return e
}

func (s *Screen) event(k Kind, text string) {
	if n := len(s.events); n < maxEvents && (k != Bell || n == 0 || s.events[n-1].Kind != Bell) {
		s.events = append(s.events, Event{k, text})
	}
}

// Reply returns the answers to the program's questions since it was last
// called, to be written to the program. Each has a fixed shape: numbers,
// and the engine's own name. None holds text a program chose.
func (s *Screen) Reply() []byte {
	b := s.reply
	s.reply = nil
	return b
}

// answer adds one answer. Past the limit of a string, answers are left out.
func (s *Screen) answer(format string, a ...any) {
	if len(s.reply) < maxStr {
		s.reply = fmt.Appendf(s.reply, format, a...)
	}
}

// Held says whether the program is inside a frame it asked to have shown
// in one go (mode 2026). While it is, Picture is the picture from before.
func (s *Screen) Held() bool {
	if !s.since.IsZero() && s.now().Sub(s.since) >= holdFor {
		s.since = time.Time{}
	}
	return !s.since.IsZero()
}

// private obeys a sequence with a prefix or an intermediate byte: the
// private modes, the questions of that form, the extended keyboard's
// levels and the soft reset. Every other one is read and dropped.
func (s *Screen) private(b byte) {
	n := max(s.args[0], 0)
	switch [3]byte{s.prefix, s.mid, b} {
	case [3]byte{'?', 0, 'h'}, [3]byte{'?', 0, 'l'}:
		for _, n := range s.args[:s.n] {
			s.mode(n, b == 'h')
		}
	case [3]byte{'?', '$', 'p'}:
		s.answer("\x1b[?%d;%d$y", n, s.report(n))
	case [3]byte{'>', 0, 'c'}:
		if n == 0 {
			s.answer("\x1b[>1;10;0c")
		}
	case [3]byte{'>', 0, 'q'}:
		if n == 0 {
			s.answer("\x1bP>|whaleshark %s\x1b\\", Version)
		}
	case [3]byte{0, '!', 'p'}:
		s.reset(false)
	case [3]byte{'?', 0, 'u'}:
		s.answer("\x1b[?%du", s.modes.Keys)
	case [3]byte{'>', 0, 'u'}: // enter a level, remembering the one before
		if len(s.keys) == maxKeys {
			s.keys = s.keys[:copy(s.keys, s.keys[1:])]
		}
		s.keys, s.modes.Keys = append(s.keys, s.modes.Keys), n&1
	case [3]byte{'<', 0, 'u'}: // leave so many levels; past the first, none is left
		for range s.arg(0, 1) {
			if s.modes.Keys = 0; len(s.keys) == 0 {
				break
			}
			s.keys, s.modes.Keys = s.keys[:len(s.keys)-1], s.keys[len(s.keys)-1]
		}
	case [3]byte{'=', 0, 'u'}: // set, add to or take from the level in force
		switch s.arg(1, 1) {
		case 1:
			s.modes.Keys = n & 1
		case 2:
			s.modes.Keys |= n & 1
		case 3:
			s.modes.Keys &^= n
		}
	}
}

// report is the answer to "is private mode n on": 1 for on, 2 for off, and
// 0 for a mode this reader does not have.
func (s *Screen) report(n int) int {
	on := false
	switch p := s.flag(n); {
	case p != nil:
		on = *p
	case n == 47 || n == 1047 || n == 1049:
		on = s.alt == 1
	case n == 1000 || n == 1002 || n == 1003:
		on = s.modes.Mouse == n
	case n == 2026:
		on = s.Held()
	default:
		return 0
	}
	if on {
		return 1
	}
	return 2
}

// reset puts back what a program may have changed: the style, the modes,
// the region, the saved cursors, the hold. A full reset (ESC c) empties both
// screens and goes home too; a soft one (CSI ! p) leaves what is drawn and
// where the cursor stands. The title and the scroll-back stay.
func (s *Screen) reset(full bool) {
	s.cursor, s.saved = cursor{x: s.x, y: s.y}, [2]cursor{}
	s.top, s.bot, s.wrap, s.insert, s.shown = 0, s.h-1, true, false, true
	s.modes, s.keys, s.since, s.prev = Modes{WheelKeys: true}, s.keys[:0], time.Time{}, -1
	if full {
		s.show(false)
		for _, g := range s.grids {
			s.blank(g)
		}
		s.x, s.y = 0, 0
		clear(s.tabs)
		stops(s.tabs, 8)
	}
}

// rgb is a colour as a program is told it: four digits a part, as xterm answers.
func rgb(c uint32) string {
	return fmt.Sprintf("rgb:%04x/%04x/%04x", c>>16&0xff*0x101, c>>8&0xff*0x101, c&0xff*0x101)
}

// palette is colour n of the 256 where nobody has chosen another: the
// first sixteen by the three bits of their number, then the cube of six
// steps a part and the twenty-four greys, as the xterm document lays the
// numbers out. The person's own first sixteen are not known here.
func palette(n int) uint32 {
	switch {
	case n < 16:
		lit, dark := uint32(0xcd), uint32(0)
		if n >= 8 {
			lit, dark = 0xff, 0x55
		}
		var c uint32
		for bit := range 3 {
			v := dark
			if n>>bit&1 == 1 {
				v = lit
			}
			c |= v << (16 - 8*bit)
		}
		return c
	case n < 232:
		step := func(i int) uint32 {
			if i == 0 {
				return 0
			}
			return uint32(55 + 40*i) // #nosec G115 -- at most 255
		}
		n -= 16
		return step(n/36)<<16 | step(n/6%6)<<8 | step(n%6)
	}
	return uint32(8+10*(n-232)) * 0x010101 // #nosec G115 -- at most 238
}
