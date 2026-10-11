package platform

import (
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// The three ways a command line is spelled, and what a POSIX shell takes as it stands.
const (
	Posix      = contract.ShellPosix
	PowerShell = contract.ShellPowerShell
	Cmd        = contract.ShellCmd
	plain      = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-"
)

// everyShell is what cmd, both PowerShells and git's bash all take as it
// stands; between double quotes they agree on everything but the signs in
// notInQuotes, and on a backslash only before a character that is none.
const (
	everyShell  = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_+=:./-"
	notInQuotes = "\"$`%!\n\r\u201c\u201d\u201e"
)

// Quote joins arguments so that the shell hands each on unchanged and reads
// none as its own. With no shell named on Windows, where nobody can know
// which shell an agent's tool types into, it writes the one spelling all
// four read, and only where there is none PowerShell's. Nothing carries a
// line break into cmd, and the PowerShell of Windows itself drops an empty
// argument and the double quotes inside one.
func (s *System) Quote(dialect string, argv []string) string {
	if dialect == "" {
		if dialect = Posix; s.OS == onWindows {
			if line, ok := forEveryShell(argv); ok {
				return line
			}
			dialect = PowerShell
		}
	}
	words := make([]string, len(argv))
	for i, arg := range argv {
		switch dialect {
		case PowerShell:
			// The curly quotes end a string as the straight one does.
			for _, q := range []string{"'", "‘", "’", "‚", "‛"} {
				arg = strings.ReplaceAll(arg, q, q+q)
			}
			words[i] = "'" + arg + "'"
		case Cmd:
			words[i] = cmdWord(arg)
		default:
			if arg != "" && strings.Trim(arg, plain) == "" {
				words[i] = arg
			} else {
				words[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
			}
		}
	}
	line := strings.Join(words, " ")
	if dialect == PowerShell && line != "" {
		return "& " + line // without the call sign a quoted first word is only a string
	}
	return line
}

// forEveryShell spells a line once for every shell of Windows. The program
// comes first and bare, since no two of them quote a command alike: with
// forward slashes, and by its short name where its own has a blank. Every
// other word is bare or between double quotes. Seen on a hosted Windows
// machine: all four read such a line, and one of them not as one side of a
// pipe, where cmd takes the path's first slash for a switch; a backslash,
// single or doubled, is lost on the way into git's bash.
func forEveryShell(argv []string) (string, bool) {
	words := make([]string, len(argv))
	for i, arg := range argv {
		switch bare := strings.Trim(arg, everyShell) == "" && arg != ""; {
		case i == 0:
			if arg = strings.ReplaceAll(arg, `\`, "/"); strings.Trim(arg, everyShell) != "" {
				arg = strings.ReplaceAll(short(argv[0]), `\`, "/")
			}
			// Two slashes begin another machine's folder, which cmd runs nothing from.
			if arg == "" || strings.Trim(arg, everyShell+"~") != "" || strings.HasPrefix(arg, "//") || arg[0] == '~' {
				return "", false
			}
			words[i] = arg
		case bare:
			words[i] = arg
		case strings.ContainsAny(arg, notInQuotes) || strings.HasSuffix(arg, `\`) || strings.Contains(arg, `\\`):
			return "", false
		default:
			words[i] = `"` + arg + `"`
		}
	}
	return strings.Join(words, " "), true
}

// cmdWord quotes for two readers: cmd, which takes everything between double
// quotes as it stands except a percent sign, and the program's own start-up
// code, which splits the line again and reads a double quote only after a
// backslash. cmd takes that quote for the end of its own, so from there on
// every sign of cmd's, the quotes too, gets a caret, which cmd takes away. A
// percent sign before that is put outside the quotes with a caret, where it
// can no longer open a variable's name.
func cmdWord(arg string) string {
	var b strings.Builder
	b.WriteByte('"')
	slashes, out := 0, false
	for _, r := range arg {
		switch {
		case r == '\\':
			slashes++
			b.WriteRune(r)
			continue
		case r == '"':
			b.WriteString(strings.Repeat(`\`, slashes+1))
			if out {
				b.WriteByte('^')
			}
			out = true
		case r == '%' && !out:
			b.WriteString(strings.Repeat(`\`, slashes) + `"^%"`)
			slashes = 0
			continue
		case out && strings.ContainsRune("%^&|<>()!", r):
			b.WriteByte('^')
		}
		slashes = 0
		b.WriteRune(r)
	}
	b.WriteString(strings.Repeat(`\`, slashes))
	if out {
		b.WriteByte('^')
	}
	b.WriteByte('"')
	return b.String()
}
