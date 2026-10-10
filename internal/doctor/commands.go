// Package doctor checks what the tool needs and what it left behind, one
// line a check, each with its fix. What it checks it asks through the kit:
// the keeper with Terms.Version, worktrees with Placement.Repair, port slots
// with contract.ReadSlots and FreeSlots, what was approved with
// contract.ReadTrust and a project's Held keys. Nothing here changes a run.
// With --server it proves a server row by row, and the server command, the
// one writer of the server's file, is here too.
package doctor

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) {
	k.Handle("doctor", run)
	k.Handle("server", serve)
}

// What a line says of its check. A note is worth knowing and fails nothing;
// a problem makes the command end with 1; fixed is a problem or a leftover
// that --fix has just put right.
const (
	ok      = "ok"
	note    = "note"
	problem = "problem"
	fixed   = "fixed"

	again   = "whaleshark doctor --fix"
	initCmd = "whaleshark init"
)

// Line is one check: how it ended, what was found, and the command that mends it.
type Line struct {
	Mark string `json:"mark"`
	Text string `json:"text"`
	Fix  string `json:"fix,omitempty"`
}

// exam is one run of doctor, and with its lines the command's result.
type exam struct {
	c    *contract.Call
	k    *contract.Kit
	dirs contract.Dirs
	fix  bool
	// runs holds every run of every project of the login, by the key of the
	// project's folder; whole says that all of a project's records were read.
	runs  map[string][]*contract.State
	whole map[string]bool

	Lines    []Line `json:"lines"`
	Problems int    `json:"problems"`
}

func (e *exam) say(mark, fix, format string, a ...any) {
	l := Line{mark, cli.Plain(fmt.Sprintf(format, a...)), fix}
	if e.Lines = append(e.Lines, l); mark == problem {
		e.Problems++
	}
	if fix != "" {
		l.Text += "  Fix: " + fix
	}
	fmt.Fprintf(e.c.Out, "%-8s %s\n", l.Mark, l.Text)
}

// mend is a finding --fix can put right: without it the finding is said
// with its mark, with it the repair runs and done says what became of it.
func (e *exam) mend(mark, text, done string, repair func() error) {
	if !e.fix {
		e.say(mark, again, "%s", text)
	} else if err := repair(); err != nil {
		e.say(problem, "", "%s, and that could not be put right: %v", text, err)
	} else {
		e.say(fixed, "", "%s: %s", text, done)
	}
}

func run(c *contract.Call) (any, error) {
	e := &exam{c: c, k: c.Kit, fix: c.Flags["fix"] != nil, runs: map[string][]*contract.State{}, whole: map[string]bool{}}
	var err error
	if c.Flags["server"] != nil && len(c.Args) == 2 {
		return probe(c)
	} else if len(c.Args) > 0 {
		return nil, &contract.Refusal{Exit: contract.ExitUsage, Code: "usage", Message: "doctor takes no argument.", Next: []string{"whaleshark help doctor"}}
	}
	if e.dirs, err = e.k.Platform.Dirs(); err != nil {
		return nil, &contract.Refusal{Exit: contract.ExitFailed, Code: "no_home", Message: "The login's own folders cannot be found: " + err.Error() + "."}
	}
	e.private("your state folder", e.dirs.State)
	e.private("your cache folder", e.dirs.Cache)
	e.read()
	e.engine()
	e.project()
	e.slots()
	e.leftovers()
	e.nudge()
	e.paste()
	if c.Flags["server"] != nil {
		e.machine()
	}
	if e.Problems > 0 {
		r := &contract.Refusal{Exit: contract.ExitFailed, Code: "problems", Message: fmt.Sprintf("%d of %d checks found a problem.", e.Problems, len(e.Lines)), Data: e}
		for _, l := range e.Lines {
			if l.Mark == problem && l.Fix == again {
				r.Next = []string{again}
			}
		}
		return nil, r
	}
	return e, nil
}

// read loads every run of this project and of each project the login has
// set up: a tab, a port slot and a half-typed answer are the login's, and
// only all the records together say whose each is.
func (e *exam) read() {
	key := e.k.Platform.PathKey
	roots, err := contract.ReadProjects(e.k.Platform.Peek, e.dirs.State)
	if err != nil {
		e.say(problem, "", "the list of your projects cannot be read: %v", err)
	}
	for _, root := range append(roots, e.c.Root) {
		if _, seen := e.whole[key(root)]; seen {
			continue
		}
		ids, err := e.k.Store.Runs(root)
		e.whole[key(root)] = err == nil
		for _, id := range ids {
			s, err := e.k.Store.Read(root, id)
			if err != nil {
				e.whole[key(root)] = false
				if key(root) == key(e.c.Root) {
					e.say(problem, "", "the record of run %s cannot be read, so it is not checked: %v", id, err)
				}
				continue
			}
			e.runs[key(root)] = append(e.runs[key(root)], s)
		}
	}
}

// private holds a file or folder of ours to being the login's alone.
func (e *exam) private(what, path string) {
	info, err := os.Stat(path)
	if err != nil {
		return // not made yet
	}
	p := e.k.Platform
	switch others, err := p.WritableByOthers(path); {
	case errors.Is(err, contract.ErrCannotTell): // said once, of the project's folder
	case err != nil:
		e.say(note, "", "%s is %s; whether it is yours alone could not be told: %v", what, path, err)
	case others || info.Mode().Perm()&0o077 != 0:
		e.mend(problem, fmt.Sprintf("%s, %s, is open to another login", what, path), "it is yours alone now", func() error {
			if err := p.Private(path); err != nil {
				return err
			}
			if still, _ := p.WritableByOthers(path); still {
				return errors.New("it belongs to another login")
			}
			return nil
		})
	default:
		e.say(ok, "", "%s is yours alone: %s", what, path)
	}
}

// count is a number with its noun, for a line that counts.
func count(n int, one string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %ss", n, one)
}

// list names the first few of many.
func list(names []string) string {
	if len(names) > 5 {
		return strings.Join(names[:5], ", ") + fmt.Sprintf(" and %d more", len(names)-5)
	}
	return strings.Join(names, ", ")
}
