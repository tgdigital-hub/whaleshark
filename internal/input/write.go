package input

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tgdigital-hub/whaleshark/internal/screen"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// The writer: a key, a mouse report, pasted text and a focus report as the
// bytes the program in a pane reads, in the form that program asked for. It
// is the reverse of term's parser. Written from the xterm control-sequences
// document (the function keys of a PC keyboard, mouse tracking, bracketed
// paste, focus reports), the VT100 user guide (the cursor key mode) and the
// page of the keyboard protocol kitty wrote down, and from nothing else.

// The keys held with a key, as both key forms count them: one more than
// their sum follows the key in a report.
const (
	shift = 1 << iota
	alt
	ctrl
	super
)

var (
	held = [...]string{"shift+", "alt+", "ctrl+", "super+"}
	// The keys that are no character: the letter that ends the report of
	// one, the number before "~" of another, and the four that are a control
	// character, which is also their number in the extended form.
	letters = map[string]byte{"up": 'A', "down": 'B', "right": 'C', "left": 'D', "home": 'H', "end": 'F',
		"f1": 'P', "f2": 'Q', "f3": 'R', "f4": 'S'}
	tildes = map[string]int{"insert": 2, "delete": 3, "pageup": 5, "pagedown": 6,
		"f5": 15, "f6": 17, "f7": 18, "f8": 19, "f9": 20, "f10": 21, "f11": 23, "f12": 24}
	codes = map[string]rune{"enter": '\r', "tab": '\t', "esc": 0x1b, "space": ' ', "backspace": 0x7f}
)

// split takes a key's name apart: the keys held, in whatever order they are
// named, and the key. A capital letter is its small one with shift.
func split(name string) (with int, key string) {
	for again := true; again; {
		again = false
		for i, h := range held {
			if rest, ok := strings.CutPrefix(name, h); ok && rest != "" {
				name, with, again = rest, with|1<<i, true
			}
		}
	}
	if r, n := utf8.DecodeRuneInString(name); n == len(name) && unicode.IsUpper(r) {
		return with | shift, string(unicode.ToLower(r))
	}
	return with, name
}

// canon is a key's name in one spelling, so that "shift+a" in the settings
// is the "A" a terminal reports.
func canon(name string) string {
	with, key := split(name)
	for i, h := range held {
		if with&(1<<i) != 0 {
			key = h + key
		}
	}
	return key
}

// Key is a key press as the bytes a program reads; none for a key that has
// no form the program understands, which is every key held with Cmd for a
// program that did not ask for the extended form.
func Key(ev term.Event, m screen.Modes) string {
	if ev.Rune != 0 {
		return string(ev.Rune)
	}
	with, key := split(ev.Key)
	mod := ""
	if with != 0 {
		mod = ";" + strconv.Itoa(with+1)
	}
	if l, ok := letters[key]; ok {
		switch {
		case with != 0 && l == 'R' && m.Keys > 0:
			// F3 held with a key would read as a cursor report.
			return "\x1b[13" + mod + "~"
		case with != 0:
			return "\x1b[1" + mod + string(l)
		case m.CursorKeys || l >= 'P':
			return "\x1bO" + string(l)
		}
		return "\x1b[" + string(l)
	}
	if n, ok := tildes[key]; ok {
		return "\x1b[" + strconv.Itoa(n) + mod + "~"
	}
	code, named := codes[key]
	if r, n := utf8.DecodeRuneInString(key); !named && (n == 0 || n != len(key) || r == utf8.RuneError) {
		return ""
	} else if !named {
		code = r
	}
	// The first level of the extended form: Escape, a key held with alt,
	// ctrl or Cmd, and Enter, Tab or Backspace held with anything are a
	// report of their own; what types a character stays that character.
	if m.Keys > 0 && (with&^shift != 0 || key == "esc" || with != 0 && named && key != "space") {
		return "\x1b[" + strconv.Itoa(int(code)) + mod + "u"
	}
	if with&super != 0 {
		return ""
	}
	out := string(code)
	switch {
	case key == "tab" && with&shift != 0:
		out = "\x1b[Z"
	case with&ctrl != 0 && code == '?':
		out = "\x7f"
	case with&ctrl != 0 && (code == ' ' || code >= '@' && code <= '~'):
		out = string(code & 0x1f)
	case with&shift != 0:
		out = string(unicode.ToUpper(code))
	}
	if with&alt != 0 {
		out = "\x1b" + out
	}
	return out
}

// Mouse is a mouse report for the program in a pane, x and y counted from
// the pane's own top left; none when the program did not ask for that kind
// of event, or asked for the older form and the cell is past what it can say.
func Mouse(ev term.Event, m screen.Modes, x, y int) string {
	code := map[term.Button]int{term.Left: 0, term.Middle: 1, term.NoButton: 3, term.WheelUp: 64, term.WheelDown: 65}[ev.Button]
	switch {
	case m.Mouse == 0, x < 0, y < 0:
		return ""
	case ev.Action == term.Move && (m.Mouse < 1002 || ev.Button == term.NoButton && m.Mouse < 1003):
		return ""
	case ev.Action == term.Move:
		code += 32
	}
	for i, on := range [...]bool{ev.Shift, ev.Alt, ev.Ctrl} {
		if on {
			code += 4 << i
		}
	}
	if m.MouseSGR {
		end := "M"
		if ev.Action == term.Release {
			end = "m"
		}
		return "\x1b[<" + strconv.Itoa(code) + ";" + strconv.Itoa(x+1) + ";" + strconv.Itoa(y+1) + end
	}
	// The older form: one byte a number, and a release names no button.
	if ev.Action == term.Release {
		code |= 3
	}
	if x > 222 || y > 222 {
		return ""
	}
	return "\x1b[M" + string([]byte{byte(32 + code), byte(33 + x), byte(33 + y)}) // #nosec G115 -- a code is under 160 and a cell at most 222, checked above
}

// Paste is pasted text for a program: between the two marks when it asked
// for them, else with each line break as Enter. Control characters other
// than the tab and the line break are taken out first, the well-known
// defence against pasted text that pretends the paste is over or presses keys.
func Paste(text string, m screen.Modes) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r' {
			return -1
		}
		return r
	}, text)
	switch {
	case text == "":
		return ""
	case m.Paste:
		return "\x1b[200~" + text + "\x1b[201~"
	}
	return strings.NewReplacer("\r\n", "\r", "\n", "\r").Replace(text)
}

// Focus tells a program that its pane got the keys or lost them, if it
// asked to be told.
func Focus(on bool, m screen.Modes) string {
	switch {
	case !m.Focus:
		return ""
	case on:
		return "\x1b[I"
	}
	return "\x1b[O"
}
