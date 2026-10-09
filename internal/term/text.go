package term

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// Clean removes what could steer a terminal from text somebody else wrote:
// escape sequences, control characters and the marks that turn the reading
// direction. A tab or a line break becomes a space.
func Clean(s string) string {
	if strings.IndexFunc(s, steers) < 0 && utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == 0x1b:
			n = sequence(s[i:])
		case r == '\t' || r == '\n':
			b.WriteByte(' ')
		case !steers(r):
			b.WriteRune(r)
		}
		i += n
	}
	return b.String()
}

func steers(r rune) bool {
	return unicode.IsControl(r) || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069
}

// sequence is the length of the escape sequence s starts with.
func sequence(s string) int {
	if len(s) < 2 {
		return len(s)
	}
	switch s[1] {
	case '[':
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1
			}
		}
		return len(s)
	case ']', 'P', 'X', '^', '_':
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b {
				return min(i+2, len(s))
			}
		}
		return len(s)
	}
	return 2
}

// Width is how many cells s takes once it is clean.
func Width(s string) int { return uniseg.StringWidth(Clean(s)) }
