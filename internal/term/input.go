package term

import (
	"bytes"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kind says what an Event is.
type Kind uint8

const (
	KeyPress Kind = iota + 1
	Mouse
	Paste
	Focus
	Resize
	End // the input ended; the program leaves
)

type Button uint8

const (
	NoButton Button = iota // the pointer moved with no button held
	Left
	Middle
	WheelUp
	WheelDown
)

type Action uint8

const (
	Press Action = iota + 1
	Release
	Move
)

// Event is one thing the terminal reported.
type Event struct {
	Kind Kind
	// Key names a key press: the character itself ("a", "S", "/"), or
	// "enter", "esc", "tab", "space", "backspace", "delete", "up", "down",
	// "left", "right", "home", "end", "pageup", "pagedown", "insert", "f1"
	// to "f12", with "ctrl+", "alt+" and "shift+" in front, in that order,
	// and "super+" (Cmd on a Mac) before those where the terminal sends keys
	// in the extended form.
	// Rune is the character the key types, 0 when it types none.
	Key  string
	Rune rune
	// A mouse report: X and Y count cells from 0 at the top left.
	Button           Button
	Action           Action
	X, Y             int
	Shift, Alt, Ctrl bool
	Text             string // what was pasted, as it came
	Focused          bool
	W, H             int // the new size
}

var (
	pasteStart = []byte("\x1b[200~")
	pasteEnd   = []byte("\x1b[201~")
	letterKeys = map[byte]string{'A': "up", 'B': "down", 'C': "right", 'D': "left", 'H': "home", 'F': "end",
		'P': "f1", 'Q': "f2", 'R': "f3", 'S': "f4", 'Z': "shift+tab"}
	tildeKeys = map[int]string{1: "home", 2: "insert", 3: "delete", 4: "end", 5: "pageup", 6: "pagedown", 7: "home", 8: "end",
		11: "f1", 12: "f2", 13: "f3", 14: "f4", 15: "f5", 17: "f6", 18: "f7", 19: "f8", 20: "f9", 21: "f10", 23: "f11", 24: "f12"}
)

// Parser turns the bytes a terminal sends into events. A report cut in two
// by a read is kept until the rest arrives.
type Parser struct{ buf []byte }

// Feed takes bytes as they arrive and returns the events that are complete.
func (p *Parser) Feed(b []byte) []Event {
	p.buf = append(p.buf, b...)
	return p.take(false)
}

// Pending reports that bytes are kept which a timer must settle: a lone
// Escape, or the start of a report. A paste is waited for without a timer.
func (p *Parser) Pending() bool {
	return len(p.buf) > 0 && !bytes.HasPrefix(p.buf, pasteStart)
}

// Flush is called when nothing more came in time: a kept Escape is the
// Escape key, and a report that never ended is dropped.
func (p *Parser) Flush() []Event { return p.take(true) }

func (p *Parser) take(last bool) (evs []Event) {
	b := p.buf
	for len(b) > 0 {
		ev, n := next(b, last)
		if n == 0 {
			break
		}
		if ev.Kind != 0 {
			evs = append(evs, ev)
		}
		b = b[n:]
	}
	p.buf = append(p.buf[:0], b...)
	return evs
}

// next reads one event from the front of b and says how many bytes it took;
// 0 means wait for more, and an event of no kind is skipped.
func next(b []byte, last bool) (Event, int) {
	switch c := b[0]; {
	case c == 0x1b:
		return escape(b, last)
	case c < 0x20 || c == 0x7f:
		return Event{Kind: KeyPress, Key: controlKey(c)}, 1
	case !utf8.FullRune(b) && !last:
		return Event{}, 0
	}
	r, n := utf8.DecodeRune(b)
	if r == utf8.RuneError && n == 1 {
		return Event{}, 1
	}
	key := string(r)
	if r == ' ' {
		key = "space"
	}
	return Event{Kind: KeyPress, Key: key, Rune: r}, n
}

func controlKey(c byte) string {
	switch c {
	case '\r':
		return "enter"
	case '\t':
		return "tab"
	case 0x7f, 0x08:
		return "backspace"
	case 0:
		return "ctrl+space"
	}
	return "ctrl+" + strings.ToLower(string(rune(c+0x40)))
}

func escape(b []byte, last bool) (Event, int) {
	if len(b) == 1 || b[1] == 0x1b {
		if len(b) == 1 && !last {
			return Event{}, 0
		}
		return Event{Kind: KeyPress, Key: "esc"}, 1
	}
	switch b[1] {
	case '[':
		return csi(b, last)
	case 'O':
		if len(b) == 2 && !last {
			return Event{}, 0
		}
		if len(b) > 2 && letterKeys[b[2]] != "" {
			return Event{Kind: KeyPress, Key: letterKeys[b[2]]}, 3
		}
	}
	ev, n := next(b[1:], last)
	if n == 0 {
		return Event{}, 0
	}
	if ev.Kind == KeyPress {
		ev.Key, ev.Rune = "alt+"+ev.Key, 0
	}
	return ev, n + 1
}

// csi reads a report that starts with Escape and "[".
func csi(b []byte, last bool) (Event, int) {
	i := 2
	for i < len(b) && b[i] >= 0x20 && b[i] < 0x40 {
		i++
	}
	if i == len(b) && !last {
		return Event{}, 0
	}
	if i == len(b) || b[i] < 0x40 || b[i] > 0x7e {
		return Event{}, i
	}
	args, end, n := string(b[2:i]), b[i], i+1
	switch {
	case strings.HasPrefix(args, "<") && (end == 'M' || end == 'm'):
		return mouse(args[1:], end == 'm'), n
	case args == "200" && end == '~':
		stop := bytes.Index(b[n:], pasteEnd)
		if stop < 0 {
			return Event{}, 0
		}
		return Event{Kind: Paste, Text: string(b[n : n+stop])}, n + stop + len(pasteEnd)
	case args == "" && (end == 'I' || end == 'O'):
		return Event{Kind: Focus, Focused: end == 'I'}, n
	}
	if end == 'u' {
		return extended(args), n
	}
	v := numbers(args, ";")
	key := letterKeys[end]
	if end == '~' {
		key = tildeKeys[v[0]]
	}
	if key == "" {
		return Event{}, n
	}
	if len(v) > 1 {
		key = held((max(v[1], 1)-1)&7) + key
	}
	return Event{Kind: KeyPress, Key: key}, n
}

// held names the keys held with a key, from the sum of shift 1, alt 2,
// ctrl 4 and super 8.
func held(sum int) (names string) {
	for i, name := range [...]string{"shift+", "alt+", "ctrl+", "super+"} {
		if sum&(1<<i) != 0 {
			names = name + names
		}
	}
	return names
}

// The keys of the extended form that are no character: the four that are
// control characters otherwise, and the keypad, numbered from keypad.
var (
	codeKeys   = map[rune]string{9: "tab", 13: "enter", 27: "esc", 32: "space", 127: "backspace"}
	keypadKeys = strings.Fields("0 1 2 3 4 5 6 7 8 9 . / * - + enter = , left right up down pageup pagedown home end insert delete")
)

const keypad = 57399

// extended reads a key in the extended form, the keyboard protocol that
// kitty wrote down and several terminals speak: the key's number and after
// a colon the character it gives with shift, then one more than the sum of
// the keys held and after a colon 3 for a release, which is no event here.
// It is how Escape arrives unmistakably, and shift+enter and Cmd+C at all.
func extended(args string) Event {
	f := strings.Split(args+";", ";")
	code, with := numbers(f[0], ":"), numbers(f[1], ":")
	if len(with) > 1 && with[1] == 3 || code[0] < 0 || code[0] > unicode.MaxRune {
		return Event{}
	}
	r, sum := rune(code[0]), (max(with[0], 1)-1)&15 // #nosec G115 -- a rune's number, by the line above
	key := codeKeys[r]
	if i := int(r - keypad); key == "" && i >= 0 && i < len(keypadKeys) {
		key = keypadKeys[i]
	}
	switch {
	case len(key) == 1 && sum == 0:
		return Event{Kind: KeyPress, Key: key, Rune: rune(key[0])}
	case key != "":
	case unicode.IsControl(r) || r >= 0xe000 && r <= 0xf8ff || !utf8.ValidRune(r):
		return Event{}
	default:
		// Shift with a character is the other character, as it is when
		// the terminal sends the character itself; with ctrl or super
		// held it stays a key that is held.
		if sum&1 != 0 && sum&12 == 0 {
			if r, sum = unicode.ToUpper(r), sum&^1; len(code) > 1 && code[1] > 0 && code[1] <= unicode.MaxRune {
				r = rune(code[1]) // #nosec G115 -- a rune's number, by the line above
			}
		}
		if key = string(r); sum == 0 {
			return Event{Kind: KeyPress, Key: key, Rune: r}
		}
	}
	ev := Event{Kind: KeyPress, Key: held(sum) + key}
	if key == "space" && sum == 0 {
		ev.Rune = ' '
	}
	return ev
}

func numbers(s, sep string) []int {
	var v []int
	for _, f := range strings.Split(s, sep) {
		n, _ := strconv.Atoi(f)
		v = append(v, n)
	}
	return v
}

// mouse reads an "SGR" mouse report. Nothing of ours is on the right
// button, so its reports are dropped.
func mouse(args string, up bool) Event {
	v := numbers(args, ";")
	if len(v) != 3 {
		return Event{}
	}
	code, b := v[0], v[0]&3
	ev := Event{Kind: Mouse, Action: Press, X: v[1] - 1, Y: v[2] - 1, Shift: code&4 != 0, Alt: code&8 != 0, Ctrl: code&16 != 0}
	switch {
	case code&128 != 0, code&64 != 0 && b > 1, code&64 == 0 && b == 2:
		return Event{}
	case code&64 != 0:
		ev.Button = WheelUp + Button(b)
		return ev
	case b < 2:
		ev.Button = Left + Button(b)
	}
	if code&32 != 0 {
		ev.Action = Move
	} else if up {
		ev.Action = Release
	}
	return ev
}
