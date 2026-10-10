// Package runtask is the commands that set a project up and plan its work:
// init, run, task, accept and trust.
package runtask

import (
	"fmt"
	"slices"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) {
	k.Handle("init", setUp)
	k.Handle("run", run)
	k.Handle("task", task)
	k.Handle("accept", accept)
	k.Handle("trust", trust)
}

// refuse is a refusal with the commands to run next.
func refuse(exit int, code, message string, next ...string) *contract.Refusal {
	return &contract.Refusal{Exit: exit, Code: code, Message: message, Next: next}
}

func usage(c *contract.Call, format string, a ...any) *contract.Refusal {
	return refuse(contract.ExitUsage, "usage", fmt.Sprintf(format, a...), "whaleshark help "+c.Command.Name)
}

func noRun() *contract.Refusal {
	return refuse(contract.ExitMissing, "no_run", "No run is open here.", `whaleshark run new "<objective>"`)
}

// form takes the form of a command off its arguments and holds what is left
// to the number that form takes.
func form(c *contract.Call, takes map[string]int) (sub string, args []string, err error) {
	known := make([]string, 0, len(takes))
	for name := range takes {
		known = append(known, name)
	}
	slices.Sort(known)
	if len(c.Args) == 0 {
		return "", nil, usage(c, "%s needs one of: %s.", c.Command.Name, strings.Join(known, ", "))
	}
	sub, args = c.Args[0], c.Args[1:]
	n, ok := takes[sub]
	switch {
	case !ok:
		return "", nil, cli.NoSuch("form of "+c.Command.Name, sub, known, "whaleshark help "+c.Command.Name)
	case len(args) != n:
		return "", nil, usage(c, "%s %s takes %d more, not %d.", c.Command.Name, sub, n, len(args))
	}
	return sub, args, nil
}

// last is the value a flag was last given, or nothing.
func last(c *contract.Call, flag string) string {
	if v := c.Flags[flag]; len(v) > 0 {
		return v[len(v)-1]
	}
	return ""
}

// change applies fn to a run under the store's lock, once the rules have
// said that this caller may run this command on it now. What the store or a
// rule answers is handed back as it is.
func change(c *contract.Call, run, sub string, fn func(*contract.State) error) error {
	if run == "" {
		return noRun()
	}
	return c.Kit.Store.Change(c.Root, run, func(s *contract.State) error {
		if err := c.Kit.Rules.Allowed(s, c.Caller, c.Command.Name, sub); err != nil {
			return err
		}
		return fn(s)
	})
}
