package term

import (
	"fmt"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Colour, Style and Cell are the contract's: one cell from the screen reader
// to the person's terminal.
type (
	Colour = contract.Colour
	Style  = contract.Style
	Cell   = contract.Cell
)

// RGB is the colour 0xRRGGBB.
func RGB(v uint32) Colour { return contract.RGB(v) }

// Mode is how much colour the terminal takes: the full range, the nearest
// of 256, or none at all.
type Mode uint8

const (
	NoColour Mode = iota
	Colour256
	FullColour
)

// ModeOf reads the mode from the environment. The keeper sets COLORTERM to
// truecolor in every pane; NO_COLOR is how a person says they want none.
func ModeOf(env func(string) string) Mode {
	switch t, c := env("TERM"), env("COLORTERM"); {
	case env("NO_COLOR") != "" || t == "" || t == "dumb":
		return NoColour
	case c == "truecolor" || c == "24bit":
		return FullColour
	}
	return Colour256
}

// style appends the sequence that switches the terminal to a style.
func (m Mode) style(b []byte, s Style) []byte {
	b = append(b, "\x1b[0"...)
	for i, on := range [...]bool{s.Bold, s.Dim, s.Italic, s.Underline, s.Reverse, s.Strike} {
		if on {
			b = append(b, ';', "123479"[i])
		}
	}
	b = m.colour(b, 38, s.Fg)
	b = m.colour(b, 48, s.Bg)
	return append(b, 'm')
}

func (m Mode) colour(b []byte, lead int, c Colour) []byte {
	if c == 0 || m == NoColour {
		return b
	}
	// One of the 256 goes by its number, so the person's own palette shows;
	// the first sixteen in the short form every colour terminal knows.
	if n := int(c & 0xff); c>>24 == 2 && n < 8 {
		return fmt.Appendf(b, ";%d", lead-8+n)
	} else if c>>24 == 2 && n < 16 {
		return fmt.Appendf(b, ";%d", lead+44+n)
	} else if c>>24 == 2 {
		return fmt.Appendf(b, ";%d;5;%d", lead, n)
	}
	r, g, bl := int(c>>16&0xff), int(c>>8&0xff), int(c&0xff)
	if m == Colour256 {
		return fmt.Appendf(b, ";%d;5;%d", lead, nearest256(r, g, bl))
	}
	return fmt.Appendf(b, ";%d;2;%d;%d;%d", lead, r, g, bl)
}

// nearest256 picks from the 6x6x6 cube and the 24 greys of the 256 colours.
func nearest256(r, g, b int) int {
	levels := [...]int{0, 95, 135, 175, 215, 255}
	step := func(v int) int {
		if v < 48 {
			return 0
		}
		return max((v-35)/40, 1)
	}
	far := func(x, y, z int) int { return (r-x)*(r-x) + (g-y)*(g-y) + (b-z)*(b-z) }
	cr, cg, cb := step(r), step(g), step(b)
	grey := min(max(((r+g+b)/3-3)/10, 0), 23)
	if v := 8 + 10*grey; far(v, v, v) < far(levels[cr], levels[cg], levels[cb]) {
		return 232 + grey
	}
	return 16 + 36*cr + 6*cg + cb
}
