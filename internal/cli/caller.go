package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/term"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

var onTerminal = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

func refuse(code, format string, a ...any) *contract.Refusal {
	return &contract.Refusal{Exit: contract.ExitRefused, Code: code, Message: fmt.Sprintf(format, a...)}
}

func unusable(err error) *contract.Refusal {
	r := &contract.Refusal{Exit: contract.ExitEnv, Code: "state_unreadable", Message: "The record cannot be read: " + err.Error() + "."}
	if errors.Is(err, contract.ErrNewer) {
		r.Code = "newer_state"
	}
	return r
}

// rootOf finds the project folder: --root, the variable of a worker's tab,
// the nearest folder upwards that holds our own, else the folder we are in.
func rootOf(c *contract.Call) (string, error) {
	if f := c.Flags["root"]; f != nil {
		return filepath.Abs(f[len(f)-1])
	}
	if root := os.Getenv(contract.EnvRoot); root != "" {
		if !filepath.IsAbs(root) {
			return "", fmt.Errorf("%s is not a full path", contract.EnvRoot)
		}
		return filepath.Clean(root), nil
	}
	cwd, err := os.Getwd()
	for dir := cwd; err == nil; dir = filepath.Dir(dir) {
		if ours := c.Kit.Reader().Dir(dir, ""); ours != "" {
			if _, err := os.Stat(ours); err == nil {
				return dir, nil
			}
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	return cwd, err
}

// Brought lists what git tracks in the project's folder of ours. That folder
// is the login's own record and no part of a repository: files of it that
// come along with one are somebody else's word about runs, the commands
// that check them and what init wrote.
func Brought(root string) []string {
	// #nosec G204 -- always the program git, with a list of arguments that holds nothing a person typed
	out, _ := exec.Command("git", "-C", root, "ls-files", "-z", "--", contract.ProjectDir).Output()
	return strings.FieldsFunc(string(out), func(r rune) bool { return r == 0 })
}

// setUp reports whether init was run in this folder by this login.
func setUp(c *contract.Call, root string) bool {
	p := c.Kit.Platform
	dirs, _ := p.Dirs()
	roots, _ := contract.ReadProjects(p.Peek, dirs.State)
	return slices.ContainsFunc(roots, func(r string) bool { return p.PathKey(r) == p.PathKey(root) })
}

// locate works out the project, the run and the caller, the same way for
// every command, reading the record and nothing else. sub is the form of the
// command, which decides who may be told about a record that cannot be read.
func locate(c *contract.Call, sub string) *contract.Refusal {
	pane, key := contract.PaneOf(os.Getenv)
	attempt, run := os.Getenv(contract.EnvAttempt), os.Getenv(contract.EnvRun)
	if f := c.Flags["run"]; f != nil {
		run = f[len(f)-1]
	}
	for _, v := range [][2]string{{"pane", pane}, {"pane", key}, {"attempt", attempt}, {"id", run}} {
		if v[1] == "" {
			continue
		}
		if err := Valid(v[0], v[1], ""); err != nil {
			return &contract.Refusal{Exit: contract.ExitUsage, Code: "invalid_value", Message: err.Error() + "."}
		}
	}
	root, err := rootOf(c)
	if err != nil {
		return unusable(err)
	}
	c.Root = root

	// A problem with the record stops every command but those open to all.
	var problem *contract.Refusal
	store := c.Kit.Reader()
	runs, err := store.Runs(root)
	if err != nil {
		problem = unusable(err)
	}
	if _, err := os.Stat(store.Dir(root, "")); err == nil && !setUp(c, root) && len(Brought(root)) > 0 {
		problem = &contract.Refusal{Exit: contract.ExitEnv, Code: "brought_record", Next: []string{"git rm -r --cached " + contract.ProjectDir},
			Message: "This repository brings a " + contract.ProjectDir + " folder along, and you never ran init here: its runs, checks and settings are somebody else's word and are not used. Take it out of git, delete what you did not make, then run whaleshark init."}
	}
	var leads []string // the open runs that record this pane as their lead agent's
	its := ""          // the open run with a live attempt in this pane
	for _, id := range runs {
		if pane == "" {
			break
		}
		s, err := store.Read(root, id)
		if err != nil {
			problem = unusable(err)
			continue
		}
		if !s.Run.ClosedAt.IsZero() {
			continue
		}
		if o := s.Run.Orchestrator; o != nil && o.Pane == pane {
			leads = append(leads, id)
		}
		for name, a := range s.Attempts {
			if attempt == "" && a.State.Live() && a.Place.Pane == pane {
				attempt, its = name, id
			}
		}
	}

	switch {
	case run != "":
		if c.Run = run; !slices.Contains(runs, run) {
			c.Run = "" // a word somebody typed and no run: nothing takes it for one, the log least of all
			problem = &contract.Refusal{Exit: contract.ExitMissing, Code: "no_run",
				Message: fmt.Sprintf("There is no run %s here.", run), Next: []string{"whaleshark run list"}}
		}
	case its != "":
		c.Run = its
	case len(leads) > 0:
		c.Run = leads[len(leads)-1]
	default:
		if c.Run, err = store.Current(root); err != nil {
			problem = unusable(err)
		}
	}

	// Who is the human: a worker never; the page's mark only on a child that
	// has no pane, which is how the page's server starts them; --human from a
	// pane the record does not know as an agent's, or from the command of a
	// shortcut, which has no pane of its own and the keeper's mark instead;
	// and a person at a plain terminal. Nothing else, so a variable an agent
	// inherited or a flag it added where it has no pane gains it nothing.
	human, from := c.Flags["human"] != nil, os.Getenv(contract.EnvFrom)
	if own := os.Getenv(contract.EnvOwnTab); pane != "" && own != "" {
		// A button's long command, moved into a tab of its own, is still the
		// button's: the tab says where it was pressed.
		from = own
	}
	who := contract.Caller{Kind: contract.Unbound, Pane: pane}
	switch {
	case attempt != "":
		who.Kind, who.Attempt = contract.Worker, attempt
	case pane == "" && (from == contract.WherePage || from == contract.WherePhone):
		who.Kind, who.Where = contract.Human, from
	case human && len(leads) > 0:
	case human && pane != "" && (from == contract.WherePane || from == contract.WherePage || from == contract.WherePhone):
		who.Kind, who.Where = contract.Human, from
	case human && (pane != "" || key != ""), pane == "" && onTerminal():
		who.Kind, who.Where = contract.Human, contract.WhereTyped
	case slices.Contains(leads, c.Run):
		who.Kind = contract.Orchestrator
	}
	c.Caller = who
	if human && who.Kind != contract.Human {
		if pane == "" {
			return refuse("not_human", "--human counts only in a tab of your own, at a terminal or from a shortcut.")
		}
		return refuse("not_human", "--human is not for an agent's tab.")
	}
	if problem != nil && !(c.Command.May(contract.Worker, sub) && c.Command.May(contract.Unbound, sub)) {
		return problem
	}
	return nil
}

// permit refuses a command, a form of it or a flag that the table does not
// mark for this caller.
func permit(c *contract.Call, sub string) *contract.Refusal {
	cmd, kind := c.Command, c.Caller.Kind
	if !cmd.May(kind, sub) {
		switch {
		case kind == contract.Worker:
			r := refuse("not_for_worker", "This tab is a worker's, and %s is not a worker's command.", cmd.Name)
			r.Next = []string{"whaleshark guide worker"}
			return r
		case !cmd.May(contract.Human, sub):
			return refuse("worker_only", "%s is a worker's command: it runs in the worker's own tab.", cmd.Name)
		case kind == contract.Unbound && cmd.May(contract.Orchestrator, sub):
			r := refuse("not_bound", "Only the tab a run is bound to may change it.")
			if r.Next = []string{"whaleshark run takeover"}; c.Run == "" {
				r.Message, r.Next = "No run is open here.", []string{`whaleshark run new "<objective>"`}
			}
			return r
		}
		return refuse("human_only", "%s is the person's to run, not an agent's.", cmd.Name)
	}
	for _, f := range cmd.Flags {
		if f.Who != 0 && c.Flags[f.Name] != nil && !(&contract.Command{Who: f.Who}).May(kind, "") {
			return refuse("human_only", "--%s is the person's to give, not an agent's.", f.Name)
		}
	}
	return nil
}
