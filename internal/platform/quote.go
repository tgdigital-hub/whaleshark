package platform

import "strings"

// The three ways a command line is spelled, and what a POSIX shell takes as it stands.
const (
	Posix      = "posix"
	PowerShell = "powershell"
	Cmd        = "cmd"
	plain      = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-"
)

func (s *System) Quote(argv []string) string {
	if s.OS == onWindows {
		return Quote(PowerShell, argv)
	}
	return Quote(Posix, argv)
}

// Quote joins arguments so that the shell hands each on unchanged and reads
// none as its own. Nothing carries a line break into cmd, and the PowerShell
// of Windows itself drops an empty argument and the double quotes inside one.
func Quote(dialect string, argv []string) string {
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

// cmdWord quotes for two readers: cmd, which takes everything between double
// quotes as it stands except a percent sign, and the program's own start-up
// code, which splits the line again. A percent sign is put outside the quotes
// with a caret, where it can no longer open a variable's name.
func cmdWord(arg string) string {
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, r := range arg {
		switch r {
		case '\\':
			slashes++
		case '"':
			// Written twice, so that cmd still counts itself inside the quotes.
			b.WriteString(strings.Repeat(`\`, slashes) + `"`)
			slashes = 0
		case '%':
			b.WriteString(strings.Repeat(`\`, slashes) + `"^%"`)
			slashes = 0
			continue
		default:
			slashes = 0
		}
		b.WriteRune(r)
	}
	b.WriteString(strings.Repeat(`\`, slashes) + `"`)
	return b.String()
}
