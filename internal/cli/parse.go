package cli

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// parsed is a command line taken apart. open names a flag that was given
// without its value, which --file then supplies.
type parsed struct {
	flags map[string][]string
	args  []string
	open  string
}

func usage(cmd *contract.Command, format string, a ...any) *contract.Refusal {
	return &contract.Refusal{Exit: contract.ExitUsage, Code: "usage",
		Message: fmt.Sprintf(format, a...), Next: []string{"whaleshark help " + cmd.Name}}
}

// flagsOf lists a command's flags and then the ones every command takes.
func flagsOf(cmd *contract.Command) []contract.Flag {
	return append(slices.Clone(cmd.Flags), contract.CommonFlags...)
}

// spell writes flags as they are typed. --human is left out: it is printed
// for the person only.
func spell(flags []contract.Flag, human bool) []string {
	var out []string
	for _, f := range flags {
		if f.Name == "human" && !human {
			continue
		}
		out = append(out, strings.TrimSpace("--"+f.Name+" "+f.Value))
	}
	return out
}

// parse splits what follows the command into flags and the rest. Only a word
// that starts with two dashes is a flag; after a bare "--" nothing is.
func parse(cmd *contract.Command, argv []string) (parsed, *contract.Refusal) {
	p := parsed{flags: map[string][]string{}}
	known := flagsOf(cmd)
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			p.args = append(p.args, argv[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "--") {
			p.args = append(p.args, a)
			continue
		}
		name, value, given := strings.Cut(a[2:], "=")
		at := slices.IndexFunc(known, func(f contract.Flag) bool { return f.Name == name })
		switch {
		case at < 0:
			return p, usage(cmd, "%s has no flag --%s. Its flags: %s.", cmd.Name, name, strings.Join(spell(known, false), ", "))
		case known[at].Value == "" && given:
			return p, usage(cmd, "--%s takes no value.", name)
		case known[at].Value == "" || given:
		case i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "--"):
			i++
			value = argv[i]
		case p.open != "" || name == "file":
			return p, usage(cmd, "--%s needs a value.", name)
		default:
			p.open = name
		}
		p.flags[name] = append(p.flags[name], value)
	}
	if p.open != "" && p.flags["file"] == nil {
		return p, usage(cmd, "--%s needs a value.", p.open)
	}
	return p, nil
}

// kinds maps what a flag says it takes to the rule its value must pass. What
// is not listed is free text, which is stored and never run.
var kinds = map[string]string{
	"ID": "id", "QID": "id", "T": "task", "A,B": "task", "KIND": "agent", "KIND,...": "agent",
	"N": "number", "SEC": "number", "PATTERN,...": "path", "DIR": "path",
}

// check passes every flag value through the validator. A path is checked
// for its form while root is empty, and against the project once it is known.
func check(cmd *contract.Command, p parsed, root string) *contract.Refusal {
	for _, f := range cmd.Flags {
		kind := kinds[f.Value]
		if kind == "" || root != "" && kind != "path" {
			continue
		}
		for _, v := range p.flags[f.Name] {
			if f.Name == p.open {
				continue
			}
			parts := []string{v}
			if strings.Contains(f.Value, ",") {
				parts = strings.Split(v, ",")
			}
			for _, part := range parts {
				if err := Valid(kind, part, root); err != nil {
					r := usage(cmd, "--%s: %v.", f.Name, err)
					r.Code = "invalid_value"
					return r
				}
			}
		}
	}
	return nil
}

// maxText is the most free text one command takes from a file.
const maxText = 1 << 20

// text puts the free text of --file, or of standard input for "-", where it
// belongs: as the value of the flag left open, else as the last argument.
func (p parsed) text(c *contract.Call) *contract.Refusal {
	read := func(path string) (string, *contract.Refusal) {
		in := c.Stdin
		if path != "-" {
			f, err := os.Open(path)
			if err != nil {
				return "", usage(c.Command, "--file: %v.", err)
			}
			defer f.Close()
			in = f
		}
		data, err := io.ReadAll(io.LimitReader(in, maxText+1))
		if err == nil && len(data) > maxText {
			err = fmt.Errorf("more than %d bytes", maxText)
		}
		if err == nil {
			err = Valid("text", string(data), "")
		}
		if err != nil {
			return "", usage(c.Command, "the text given as a file: %v.", err)
		}
		return strings.TrimRight(string(data), "\r\n"), nil
	}
	if at := slices.Index(c.Args, "-"); at >= 0 {
		t, bad := read("-")
		if bad != nil {
			return bad
		}
		c.Args[at] = t
	}
	if f := c.Flags["file"]; f != nil {
		t, bad := read(f[len(f)-1])
		if bad != nil {
			return bad
		}
		if of := c.Flags[p.open]; p.open != "" {
			of[len(of)-1] = t
		} else {
			c.Args = append(c.Args, t)
		}
	}
	return nil
}

// NoSuch is the usage error for a word that is none of the known ones. It
// offers the nearest known word, and nothing at all when that word destroys
// something: a slip of the hand is not turned into a loss.
func NoSuch(what, word string, known []string, next string) *contract.Refusal {
	r := &contract.Refusal{Exit: contract.ExitUsage, Code: "usage", Next: []string{next},
		Message: fmt.Sprintf("There is no %s %q.", what, word)}
	best, least := "", 3
	for _, k := range known {
		if d := distance(word, k); d < len(k) && (d < least || d == least && slices.Contains(contract.Destructive, k)) {
			best, least = k, d
		}
	}
	if slices.Contains(contract.Destructive, best) {
		best = ""
	}
	if best != "" {
		r.Message += fmt.Sprintf(" Did you mean %q?", best)
		r.Data = map[string]string{"did_you_mean": best}
	}
	return r
}

// distance counts the letters to add, drop or change to turn a into b.
func distance(a, b string) int {
	row := make([]int, len(b)+1)
	for j := range row {
		row[j] = j
	}
	for i := 1; i <= len(a); i++ {
		diag := row[0]
		row[0] = i
		for j := 1; j <= len(b); j++ {
			cost := diag
			if a[i-1] != b[j-1] {
				cost = 1 + min(diag, row[j], row[j-1])
			}
			diag, row[j] = row[j], cost
		}
	}
	return row[len(b)]
}
