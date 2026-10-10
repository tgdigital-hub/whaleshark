package runtask

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/guide"
)

const (
	installedFile = "installed.json"
	excludeLine   = ".whaleshark/"

	kindHook, kindBlock, kindKeys = "hook", "block", "keys"
)

// setting is one run of init: what it says, what still needs the person, and
// the two records of what init wrote, the project's own and the login's.
type setting struct {
	c           *contract.Call
	in          *bufio.Reader
	state       string // the login's state folder
	mine, login contract.Installed
	Did         []string `json:"did"`
	Problems    []string `json:"problems,omitempty"`
}

func (i *setting) say(format string, a ...any) {
	i.Did = append(i.Did, cli.Plain(fmt.Sprintf(format, a...)))
	fmt.Fprintln(i.c.Out, i.Did[len(i.Did)-1])
}

// ask puts a question to a person at a terminal and returns the line typed.
func (i *setting) ask(question string) string {
	fmt.Fprint(i.c.Err, question)
	line, _ := i.in.ReadString('\n')
	return strings.TrimSpace(line)
}

// record reads a record, or with write replaces it; an empty one is deleted.
func (i *setting) record(path string, in *contract.Installed, write bool) error {
	p := i.c.Kit.Platform
	switch {
	case !write:
		return contract.ReadVersioned(p.Read, path, contract.FileVersion, in)
	case len(in.Entries) > 0:
		in.Version = contract.FileVersion
		return contract.WriteVersioned(p, path, contract.FileVersion, in)
	}
	if err := os.Remove(path); !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func at(entries []contract.InstalledEntry, kind, file string) int {
	return slices.IndexFunc(entries, func(e contract.InstalledEntry) bool { return e.Kind == kind && e.File == file })
}

func setUp(c *contract.Call) (any, error) {
	f := c.Flags
	switch {
	case len(c.Args) > 0:
		return nil, usage(c, "init takes no argument.")
	case f["all"] != nil && f["remove"] == nil:
		return nil, usage(c, "--all goes with --remove.")
	case f["print-block"] != nil:
		_, err := io.WriteString(c.Out, guide.Block)
		return map[string]string{"block": guide.Block}, err
	}
	dirs, err := c.Kit.Platform.Dirs()
	if err != nil {
		return nil, refuse(contract.ExitEnv, "no_home", "The login's own folders cannot be found: "+err.Error()+".")
	}
	i := &setting{c: c, in: bufio.NewReader(c.Stdin), state: dirs.State}
	login := filepath.Join(dirs.State, installedFile)
	if err = i.record(login, &i.login, false); err == nil {
		if f["remove"] != nil {
			err = i.remove()
		} else {
			err = i.project()
		}
	}
	if err == nil {
		err = i.record(login, &i.login, true)
	}
	switch {
	case errors.Is(err, contract.ErrNewer):
		return nil, refuse(contract.ExitEnv, "newer_state", "A record of what init wrote is from a newer whaleshark, and this one will not change it.")
	case err != nil:
		return nil, err
	case len(i.Problems) > 0:
		r := refuse(contract.ExitEnv, "not_ready", "The project is set up, and this still needs you:\n  "+strings.Join(i.Problems, "\n  "))
		r.Data = i
		return nil, r
	}
	return i, nil
}

// git runs git in a project folder and returns the line it prints.
func git(root string, args ...string) (string, error) {
	// #nosec G204 G702 -- always the program git, with a list of arguments that holds nothing a person typed
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

// project sets the project up, checks what it needs, and writes what was asked for.
func (i *setting) project() error {
	c, k, f := i.c, i.c.Kit, i.c.Flags
	if k.Platform.Synced(c.Root) {
		if f["allow-synced"] == nil {
			return refuse(contract.ExitEnv, "synced", c.Root+" is in a folder that is synced to other machines, which makes copies of the record. Move the project, or pass --allow-synced.")
		}
		i.say("warning: this folder is synced to other machines; a second machine must never write here")
	}
	switch shared, err := k.Platform.WritableByOthers(c.Root); {
	case errors.Is(err, contract.ErrCannotTell):
		i.say("this system cannot tell who else may write to the folder; going on")
	case err != nil:
		return err
	case shared:
		return refuse(contract.ExitEnv, "shared_folder", "Another login can write to "+c.Root+": it belongs to somebody else, or anyone may write it, or its group may and has another member.")
	}
	ours := k.Store.Dir(c.Root, "")
	err := os.MkdirAll(ours, 0o700)
	if err == nil {
		err = k.Platform.Private(ours)
	}
	if err != nil {
		return refuse(contract.ExitEnv, "state_unwritable", "The project's folder of ours cannot be made: "+err.Error()+".")
	}
	i.say("%s is here, readable by you only", ours)
	roots, err := contract.ReadProjects(k.Platform.Read, i.state)
	if err == nil && !slices.ContainsFunc(roots, func(r string) bool { return k.Platform.PathKey(r) == k.Platform.PathKey(c.Root) }) {
		err = contract.WriteProjects(k.Platform, i.state, append(roots, c.Root))
	}
	if err == nil {
		err = i.exclude()
	}
	if err == nil {
		err = i.record(filepath.Join(ours, installedFile), &i.mine, false)
	}
	if err != nil {
		return err
	}

	// The engine is this same program: nothing else has to be installed,
	// and `open` starts the keeper when none runs.
	version, down := k.Terms.Version()
	reachable := down == nil
	switch {
	case !reachable:
		i.say("the engine is not running: `whaleshark open` starts it and shows your tabs")
	case version != cli.Version:
		i.say("the engine is running as version %s, and this is %s: `whaleshark engine stop`, then `whaleshark open`, starts this one", version, cli.Version)
	default:
		i.say("the engine is running")
	}
	if line, err := git(c.Root, "--version"); err != nil {
		i.Problems = append(i.Problems, "git is missing")
	} else {
		i.say("%s", line)
	}
	if unlock, free, err := k.Platform.TryLock(filepath.Join(ours, "lock")); err != nil {
		i.Problems = append(i.Problems, "the lock does not work here: "+err.Error())
	} else if i.say("the lock works"); free {
		unlock()
	}

	person := c.Caller.Kind == contract.Human && c.Caller.Where == contract.WhereTyped
	kinds := contract.List(f["agent"]...)
	if f["agent"] == nil && person {
		var known []string
		for _, row := range guide.Files {
			known = append(known, row.Kind)
		}
		answer := i.ask("Which agent tools do you use here? (" + strings.Join(known, ", ") + "; several with commas, none with Enter) ")
		kinds = strings.FieldsFunc(answer, func(r rune) bool { return r == ',' || r == ' ' })
	}
	for _, kind := range kinds {
		if err = cli.Valid("agent", kind, ""); err != nil {
			return usage(c, "%v.", err)
		}
		if err = i.agent(kind); err != nil {
			return err
		}
	}
	if f["keys"] != nil && err == nil {
		err = i.keys(person, reachable)
	}
	if err != nil {
		return err
	}
	return i.record(filepath.Join(ours, installedFile), &i.mine, true)
}

// exclude keeps our folder out of git, through the repository's own file of
// local exclusions; a project that is no repository has nothing to exclude.
func (i *setting) exclude() error {
	path, err := git(i.c.Root, "rev-parse", "--git-path", "info/exclude")
	if err != nil || path == "" {
		return nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(i.c.Root, path)
	}
	data, _ := os.ReadFile(path)
	if slices.Contains(strings.Fields(string(data)), excludeLine) {
		return nil
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// #nosec G306 -- git's own file, with the permissions git gives it
	return os.WriteFile(path, append(data, excludeLine+"\n"...), 0o644)
}

// use records once that this project uses something init wrote, and counts
// it in the login's record when it is the whole login's.
func (i *setting) use(e contract.InstalledEntry) {
	if at(i.mine.Entries, e.Kind, e.File) >= 0 {
		return
	}
	i.mine.Entries = append(i.mine.Entries, contract.InstalledEntry{Kind: e.Kind, File: e.File, Agent: e.Agent})
	if n := at(i.login.Entries, e.Kind, e.File); n >= 0 {
		i.login.Entries[n].Uses++
	} else if e.Kind != kindHook {
		e.Uses = 1
		i.login.Entries = append(i.login.Entries, e)
	}
}

// agent writes the rules block into an agent tool's own instruction file and
// has its hooks set in this project. A block is recorded only when it was
// written here, or by init for another project: any other is the person's.
func (i *setting) agent(kind string) error {
	k := i.c.Kit
	home, _ := os.UserHomeDir()
	if path, ok := guide.File(kind, home, os.Getenv); !ok {
		i.say("%s has no instruction file we know: start your lead agent with this as its first message: %s", kind, guide.FirstMessage)
	} else {
		before, created, err := guide.Write(k.Platform, path)
		switch {
		case err != nil:
			return err
		case before == guide.Edited, before == guide.Ours && at(i.login.Entries, kindBlock, path) < 0:
			i.say("%s holds a rules block that init did not write, or that was changed since; it is left alone", path)
		default:
			i.use(contract.InstalledEntry{Kind: kindBlock, File: path, Agent: kind, Created: created})
			i.say("the rules for the lead agent are in %s", path)
		}
	}
	setup, err := k.AgentSettings.Ensure(kind, i.c.Root, "", true)
	if err == nil && setup.File != "" {
		i.use(contract.InstalledEntry{Kind: kindHook, File: setup.File, Agent: kind})
		i.say("the status line and the gate for %s are set in %s", kind, setup.File)
	}
	return err
}

// shortcuts is the action table's keys that work from anywhere. The action list and the catch-up open over
// the screen, the rest run unseen. A value an action leaves open is "toggle".
func shortcuts(bin string) (keys []contract.KeyEntry, labels []string) {
	for _, a := range contract.Actions {
		if a.Prefix == "" {
			continue
		}
		e := contract.KeyEntry{Key: a.Prefix, Type: "shell", Argv: []string{bin}}
		if a.ID == "menu" || a.ID == "catchup" {
			e.Type = "popup"
		}
		for _, word := range a.Run {
			e.Argv = append(e.Argv, strings.ReplaceAll(word, "{value}", "toggle"))
		}
		keys, labels = append(keys, e), append(labels, a.Label)
	}
	return keys, labels
}

// keys shows the shortcuts and writes them into the terminals' settings on
// the person's yes, and on nobody else's.
func (i *setting) keys(person, reachable bool) error {
	bin, err := i.c.Kit.Platform.SelfPath()
	if err != nil {
		return err
	}
	entries, labels := shortcuts(bin)
	i.say("The %d shortcuts that work from any tab, each after the command key (Ctrl+Space unless you changed it):", len(entries))
	for n, e := range entries {
		i.say("  %-8s %s", e.Key, labels[n])
	}
	switch {
	case !person:
		i.say("They are written when the person runs `whaleshark init --keys` in a terminal of their own and says yes.")
	case !reachable:
		i.say("They are not written while the engine is not running: run `whaleshark open` first.")
	case !strings.EqualFold(i.ask("Write them into your WhaleShark settings? (yes/no) "), "yes"):
		i.say("Nothing was written.")
	default:
		if err = i.c.Kit.Terms.SetKeys(entries); err == nil {
			i.use(contract.InstalledEntry{Kind: kindKeys})
			i.say("The shortcuts are written.")
		}
	}
	return err
}

// undo takes one thing init wrote for the whole login out again.
func (i *setting) undo(e contract.InstalledEntry) error {
	if e.Kind == kindKeys {
		i.say("the shortcuts are out of the settings")
		return i.c.Kit.Terms.SetKeys(nil)
	}
	before, err := guide.Remove(i.c.Kit.Platform, e.File, e.Created)
	if before == guide.Ours {
		i.say("the rules block is out of %s", e.File)
	} else if err == nil {
		i.say("the rules block in %s is no longer what init wrote; it is left alone", e.File)
	}
	return err
}

// remove takes this project out, or with --all every project: its hooks, and
// of the login's things what no other project still uses. Runs are work, and stay.
func (i *setting) remove() error {
	c, k, all := i.c, i.c.Kit, i.c.Flags["all"] != nil
	listed, err := contract.ReadProjects(k.Platform.Read, i.state)
	if err != nil {
		return err
	}
	roots, what := []string{c.Root}, "This project is"
	if all {
		roots, what = append(roots, listed...), "Every project is"
	}
	for _, root := range roots {
		path, mine := filepath.Join(k.Store.Dir(root, ""), installedFile), contract.Installed{}
		if err = i.record(path, &mine, false); err != nil {
			return err
		}
		for _, e := range mine.Entries {
			n := at(i.login.Entries, e.Kind, e.File)
			switch {
			case e.Kind == kindHook:
				err = k.AgentSettings.Remove(e.Agent, root)
				i.say("the hooks for %s are out of %s", e.Agent, e.File)
			case n < 0:
			case i.login.Entries[n].Uses > 1 && !all:
				i.login.Entries[n].Uses--
				i.say("%s is still used by %d other projects; it stays", cmp.Or(e.File, "the shortcuts"), i.login.Entries[n].Uses)
			default:
				err = i.undo(i.login.Entries[n])
				i.login.Entries = slices.Delete(i.login.Entries, n, n+1)
			}
			if err != nil {
				return err
			}
		}
		if err = i.record(path, &contract.Installed{}, true); err != nil {
			return err
		}
		listed = slices.DeleteFunc(listed, func(r string) bool { return k.Platform.PathKey(r) == k.Platform.PathKey(root) })
	}
	for ; all && len(i.login.Entries) > 0; i.login.Entries = i.login.Entries[1:] {
		if err = i.undo(i.login.Entries[0]); err != nil {
			return err
		}
	}
	i.say("%s out of your list. Runs and their folders are untouched.", what)
	return contract.WriteProjects(k.Platform, i.state, listed)
}
