package screen

// The reader of sequences: it cuts the bytes into text, control characters
// and sequences with their numbers. A sequence may arrive in any number of
// pieces; one that never ends costs a fixed amount of memory.

// The limits. 32 numbers is our own figure: a style with both colours in
// full is ten.
const (
	maxArgs = 32
	maxStr  = 64 << 10
	maxNum  = 1<<16 - 1
)

const (
	ground  = iota
	esc     // after ESC
	escMid  // after ESC and an intermediate byte
	csi     // after ESC [
	osc     // in the body of an operating system command
	swallow // in a string nothing here reads: device control, application commands
)

type reader struct {
	state  uint8
	prefix byte // '<', '=', '>' or '?' straight after CSI, else 0
	mid    byte // the intermediate byte, else 0
	bad    bool // malformed or over a limit: read to its end and dropped
	n      int  // how many of args are in use
	args   [maxArgs]int
	colon  uint32 // bit i: args[i] followed a colon, not a semicolon
	str    []byte
}

// arg is number i of the sequence, or def where it was left out or is zero.
func (r *reader) arg(i, def int) int {
	if i < r.n && r.args[i] > 0 {
		return r.args[i]
	}
	return def
}

// feed takes one byte that is not plain text: a control character, or any
// byte while a sequence is open.
func (s *Screen) feed(b byte) {
	switch {
	case b == 0x1b:
		if s.state == osc {
			s.command()
		}
		s.state, s.prefix, s.mid, s.bad = esc, 0, 0, false
		return
	case b == 0x18 || b == 0x1a:
		s.state = ground
		return
	}
	switch s.state {
	case ground:
		s.control(b)
	case esc, escMid:
		switch {
		case b < 0x20:
			s.control(b)
		case b < 0x30:
			s.bad = s.bad || s.mid != 0
			s.mid, s.state = b, escMid
		case b >= 0x7f:
		case s.state == escMid || !s.opener(b):
			s.state = ground
			if !s.bad {
				s.escape(b)
			}
		}
	case csi:
		switch {
		case b < 0x20:
			s.control(b)
		case b < 0x30:
			s.bad = s.bad || s.mid != 0
			s.mid = b
		case b < 0x3c:
			s.number(b)
		case b < 0x40:
			s.bad = s.bad || s.prefix != 0 || s.mid != 0 || s.n > 1 || s.args[0] >= 0
			s.prefix = b
		case b < 0x7f:
			s.state = ground
			if s.n = min(s.n, maxArgs); !s.bad {
				s.sequence(b)
			}
		}
	case osc:
		switch {
		case b == 0x07:
			s.state = ground
			s.command()
		case b >= 0x20 && len(s.str) < maxStr:
			s.str = append(s.str, b)
		case b >= 0x20:
			s.bad = true
		}
	}
}

// opener starts what follows ESC when b opens something longer than itself.
func (s *Screen) opener(b byte) bool {
	switch b {
	case '[':
		s.state, s.n, s.args[0], s.colon = csi, 1, -1, 0
	case ']':
		s.state, s.str = osc, s.str[:0]
	case 'P', 'X', '^', '_':
		s.state = swallow
	default:
		return false
	}
	return true
}

// number takes a digit or a separator of a sequence's numbers. Numbers past
// the limit are left out; a number stops growing at maxNum.
func (s *Screen) number(b byte) {
	switch {
	case s.mid != 0:
		s.bad = true
	case b >= ':' && s.n >= maxArgs:
		s.n = maxArgs + 1
	case b >= ':':
		if b == ':' {
			s.colon |= 1 << s.n
		}
		s.args[s.n] = -1
		s.n++
	case s.n <= maxArgs:
		s.args[s.n-1] = min(max(s.args[s.n-1], 0)*10+int(b-'0'), maxNum)
	}
}
