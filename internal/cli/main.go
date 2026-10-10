package cli

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Main runs one command line: it parses it, works out the caller, runs the
// handler and prints the outcome, as text or as one envelope.
func Main(k *contract.Kit, argv []string, in io.Reader, out, errw io.Writer) int {
	began := time.Now()
	c := &contract.Call{Kit: k, Now: contract.Now(), Stdin: in, Out: out, Err: errw}
	for _, a := range argv {
		if a == "--" {
			break
		}
		if a == "--json" {
			c.JSON, c.Out = true, io.Discard
		}
	}
	result, err := run(c, argv)
	exit := finish(c, out, result, err)
	logged(c, exit, time.Since(began))
	return exit
}

// logged adds one line to the day's file in the login's log folder (6.1):
// when, who, which run, the command with the names of its flags, how it
// ended and how long it took. Never an argument or a flag's value: those are
// free text. A hook is run every few seconds and is left out, and so are
// help and version, which touch nothing.
func logged(c *contract.Call, exit int, took time.Duration) {
	dirs, err := c.Kit.Platform.Dirs()
	if err != nil || c.Command == nil || slices.Contains([]string{"hook", "help", "version"}, c.Command.Name) {
		return
	}
	line := []string{c.Now.UTC().Format(time.RFC3339), string(c.Caller.Kind), cmp.Or(c.Run, "-"), c.Command.Name}
	// The form of a command is logged only when it is a word of its usage.
	if len(c.Args) > 0 && slices.Contains(strings.FieldsFunc(c.Command.Usage, func(r rune) bool { return r < 'a' || r > 'z' }), c.Args[0]) {
		line = append(line, c.Args[0])
	}
	for _, name := range slices.Sorted(maps.Keys(c.Flags)) {
		line = append(line, "--"+name)
	}
	line = append(line, fmt.Sprintf("exit=%d %dms", exit, took.Milliseconds()))
	dir := filepath.Join(dirs.State, "log")
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	// #nosec G304 -- the login's own log folder and a date
	if f, err := os.OpenFile(filepath.Join(dir, c.Now.UTC().Format(time.DateOnly)+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		fmt.Fprintln(f, strings.Join(line, " "))
		f.Close()
	}
}

func run(c *contract.Call, argv []string) (any, error) {
	if len(argv) == 0 || argv[0] == "--help" || argv[0] == "-h" {
		argv = append([]string{"help"}, argv[min(1, len(argv)):]...)
	}
	cmd := contract.Find(argv[0])
	if cmd == nil {
		return nil, NoSuch("command", argv[0], names(), "whaleshark help")
	}
	p, bad := parse(cmd, argv[1:])
	if bad != nil {
		return nil, bad
	}
	if p.flags["help"] != nil {
		p, cmd = parsed{flags: map[string][]string{}, args: []string{cmd.Name}}, contract.Find("help")
	}
	sub := ""
	if len(p.args) > 0 {
		if to, ok := contract.Aliases[p.args[0]]; ok && strings.Contains(cmd.Usage, cmd.Name+" "+to+" ") {
			p.args[0] = to
		}
		sub = p.args[0]
	}
	if bad := check(cmd, p, ""); bad != nil {
		return nil, bad
	}
	c.Command, c.Flags, c.Args = cmd, p.flags, p.args
	if bad := locate(c, sub); bad != nil {
		return nil, bad
	}
	if bad := check(cmd, p, c.Root); bad != nil {
		return nil, bad
	}
	if bad := permit(c, sub); bad != nil {
		return nil, bad
	}
	h := c.Kit.Handler(cmd.Name)
	if h == nil {
		return nil, &contract.Refusal{Exit: contract.ExitFailed, Code: "not_built", Message: cmd.Name + " is " + contract.ErrNotBuilt.Error() + "."}
	}
	if bad := p.text(c); bad != nil {
		return nil, bad
	}
	return h(c)
}

func names() []string {
	out := make([]string, len(contract.Commands))
	for i, c := range contract.Commands {
		out[i] = c.Name
	}
	return out
}

type envelope struct {
	OK     bool              `json:"ok"`
	Result any               `json:"result,omitempty"`
	Error  *contract.Refusal `json:"error,omitempty"`
}

// finish prints how a command ended and returns its exit code, the same in
// text and in JSON. A next line that carries --human reaches the person only.
func finish(c *contract.Call, out io.Writer, result any, err error) int {
	var r *contract.Refusal
	switch {
	case err == nil && result == nil:
		result = struct{}{}
	case err != nil && !errors.As(err, &r):
		r = &contract.Refusal{Code: "failed", Message: err.Error()}
	}
	e := envelope{OK: r == nil, Result: result}
	exit := contract.ExitOK
	if r != nil {
		shown := *r
		shown.Next = slices.DeleteFunc(slices.Clone(r.Next), func(line string) bool {
			return c.Caller.Kind != contract.Human && strings.Contains(line, "--human")
		})
		e = envelope{Error: &shown}
		if exit = r.Exit; exit == contract.ExitOK {
			exit = contract.ExitFailed
		}
		if !c.JSON {
			io.WriteString(c.Err, Plain(shown.Message)+"\n")
			for _, line := range shown.Next {
				io.WriteString(c.Err, "Next: "+Plain(line)+"\n")
			}
		}
	}
	if c.JSON {
		enc := json.NewEncoder(out)
		enc.SetEscapeHTML(false)
		enc.Encode(e)
	}
	return exit
}
