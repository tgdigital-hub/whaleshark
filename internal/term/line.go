package term

import (
	"strings"

	"github.com/rivo/uniseg"
)

// Line is the one line a person types on. It counts in characters as a
// person sees them, so a wide or a combined character is one step.
type Line struct {
	chars []string
	at    int // the cursor stands before chars[at]
	first int // the first character drawn, when the line is longer than its place
}

func (l *Line) String() string { return strings.Join(l.chars, "") }

// Set replaces the text and puts the cursor at its end.
func (l *Line) Set(s string) {
	l.chars, l.at, l.first = nil, 0, 0
	l.Insert(s)
}

// Insert puts text in at the cursor. A line break in it becomes a space,
// so pasted text can never send the line.
func (l *Line) Insert(s string) {
	rest := append([]string(nil), l.chars[l.at:]...)
	s = Clean(strings.Join(l.chars[:l.at], "") + s)
	l.chars = l.chars[:0]
	for state := -1; s != ""; {
		var c string
		var w int
		if c, s, w, state = uniseg.FirstGraphemeClusterInString(s, state); w > 0 {
			l.chars = append(l.chars, c)
		}
	}
	l.at = len(l.chars)
	l.chars = append(l.chars, rest...)
}

// Key lets the line take an event and reports whether it was the line's.
// Enter, Escape and the keys it does not know are left to the program.
func (l *Line) Key(ev Event) bool {
	if ev.Kind == Paste {
		l.Insert(ev.Text)
		return true
	}
	if ev.Kind != KeyPress {
		return false
	}
	switch ev.Key {
	case "left":
		l.at = max(l.at-1, 0)
	case "right":
		l.at = min(l.at+1, len(l.chars))
	case "home", "ctrl+a":
		l.at = 0
	case "end", "ctrl+e":
		l.at = len(l.chars)
	case "backspace":
		l.cut(l.at-1, l.at)
	case "delete":
		l.cut(l.at, l.at+1)
	case "ctrl+w", "alt+backspace":
		i := l.at
		for i > 0 && l.chars[i-1] == " " {
			i--
		}
		for i > 0 && l.chars[i-1] != " " {
			i--
		}
		l.cut(i, l.at)
	case "ctrl+u":
		l.cut(0, l.at)
	case "ctrl+k":
		l.cut(l.at, len(l.chars))
	default:
		if ev.Rune == 0 {
			return false
		}
		l.Insert(string(ev.Rune))
	}
	return true
}

func (l *Line) cut(from, to int) {
	if from = max(from, 0); from < min(to, len(l.chars)) {
		l.chars = append(l.chars[:from], l.chars[to:]...)
		l.at = from
	}
}

// Draw shows the line in w cells from column x of row y, moved along so the
// cursor is in view. With cursor set, the cell the cursor is on is reversed.
func (l *Line) Draw(g *Grid, x, y, w int, st Style, cursor bool) {
	l.first = min(l.first, l.at)
	for uniseg.StringWidth(strings.Join(l.chars[l.first:l.at], "")) >= w && l.first < l.at {
		l.first++
	}
	for i, col := l.first, x; i <= len(l.chars); i++ {
		c, s := " ", st
		if i < len(l.chars) {
			c = l.chars[i]
		}
		if i == l.at && cursor {
			s.Reverse = !s.Reverse
		}
		n := g.Put(col, y, x+w-col, c, s)
		if n == 0 {
			break
		}
		col += n
	}
}

// Click puts the cursor on the character drawn col cells into the line.
func (l *Line) Click(col int) {
	for l.at = l.first; l.at < len(l.chars); l.at++ {
		if col -= uniseg.StringWidth(l.chars[l.at]); col < 0 {
			break
		}
	}
}
