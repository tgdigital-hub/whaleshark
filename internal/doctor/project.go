package doctor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/guide"
)

const (
	installedFile = contract.InstalledFile
	manyRuns      = 20
	// watchedFor is how long after a sweep a run still counts as watched:
	// three of the five seconds everybody's sweeps are kept apart by.
	watchedFor = 15 * time.Second
)

// git runs git in the project folder and returns the line it prints.
func (e *exam) git(args ...string) (string, error) {
	// #nosec G204 -- always the program git, with a list of arguments that holds nothing a person typed
	out, err := exec.Command("git", append([]string{"-C", e.c.Root}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

// project checks the folder the command was run in: what init checks and
// wrote, what the project's settings wait for, and what its runs left.
func (e *exam) project() {
	k, root := e.k, e.c.Root
	ours := k.Store.Dir(root, "")
	if _, err := os.Stat(ours); err != nil {
		e.say(note, initCmd, "%s is not set up as a project", root)
		return
	}
	if k.Platform.Synced(root) {
		e.say(note, "", "%s is in a folder that is synced to other machines: a second machine must never write here", root)
	}
	switch shared, err := k.Platform.WritableByOthers(root); {
	case errors.Is(err, contract.ErrCannotTell):
		e.say(note, "", "this system cannot tell who else may write to %s", root)
	case err != nil:
		e.say(problem, "", "%s cannot be looked at: %v", root, err)
	case shared:
		e.say(problem, "", "another login can write to %s: it belongs to somebody else, or anyone may write it, or its group may", root)
	default:
		e.say(ok, "", "nobody else can write to %s", root)
	}
	e.private("the project's folder of ours", ours)
	if unlock, free, err := k.Platform.TryLock(filepath.Join(ours, "lock")); err != nil {
		e.say(problem, "", "the lock does not work here: %v", err)
	} else if e.say(ok, "", "the lock works"); free {
		unlock()
	}
	e.code()
	e.agents(ours)
	e.approved()
	e.left(ours)
}

// oldGit reports whether git, by the line it prints of itself, is older
// than 2.38, the first that merges without a working folder.
func oldGit(line string) bool {
	var major, minor int
	fmt.Sscanf(line, "git version %d.%d", &major, &minor)
	return major < 2 || major == 2 && minor < 38
}

// code checks git and the repository: the worktrees, accepting several
// tasks at once and the proof that a branch is merged all rest on them.
func (e *exam) code() {
	line, err := e.git("--version")
	if err != nil {
		e.say(problem, "", "git is missing")
		return
	}
	if oldGit(line) {
		e.say(problem, "", "%s is older than 2.38: several tasks cannot be accepted at once and no branch is proven merged", line)
	} else {
		e.say(ok, "", "%s", line)
	}
	if _, err := e.git("rev-parse", "--git-dir"); err != nil {
		e.say(note, "", "the project is no git repository: every task works in the project's own folder")
		return
	}
	if _, err := e.git("rev-parse", "--verify", "-q", "HEAD"); err != nil {
		e.say(problem, "git commit --allow-empty -m start", "the repository has no commit yet, so no task can get a copy of the code")
	}
	if _, err := e.git("check-ignore", "-q", contract.ProjectDir); err != nil {
		e.say(problem, initCmd, "git does not leave %s alone", contract.ProjectDir)
	}
	if came := cli.Brought(e.c.Root); len(came) > 0 {
		e.say(problem, "git rm -r --cached "+contract.ProjectDir, "git tracks %s under %s, which is your login's own record and no part of a repository: what comes along in it is somebody else's word", count(len(came), "file"), contract.ProjectDir)
	}
}

// agents says whether what init wrote for each agent tool is still in
// place and is what this version writes: the rules block in the tool's own
// instruction file, the status line and the gate in the project's settings.
func (e *exam) agents(ours string) {
	k := e.k
	var mine contract.Installed
	if err := contract.ReadVersioned(k.Platform.Peek, filepath.Join(ours, installedFile), contract.FileVersion, &mine); err != nil {
		e.say(problem, "", "the record of what init wrote here cannot be read: %v", err)
		return
	}
	if len(mine.Entries) == 0 {
		e.say(note, initCmd+" --agent claude", "no agent tool is set up for this project: no rules for the lead agent, no status line, no gate")
	}
	for _, entry := range mine.Entries {
		redo := initCmd + " --agent " + entry.Agent
		switch entry.Kind {
		case "block":
			switch state, err := guide.Look(entry.File); {
			case err != nil:
				e.say(problem, "", "%s cannot be read: %v", entry.File, err)
			case state == guide.Ours:
				e.say(ok, "", "the rules for the lead agent are in %s, as this version writes them", entry.File)
			case state == guide.Edited:
				e.say(note, "", "the rules block in %s was changed since init wrote it, or is another version's; it is left alone", entry.File)
			default:
				e.say(problem, redo, "the rules for the lead agent are gone from %s", entry.File)
			}
		case "hook":
			scratch, err := k.Platform.PrivateTemp()
			if err != nil {
				e.say(problem, "", "no scratch folder could be made in your cache: %v", err)
				continue
			}
			setup, err := k.AgentSettings.Ensure(entry.Agent, e.c.Root, scratch, false)
			os.RemoveAll(scratch)
			switch {
			case err != nil:
				e.say(problem, redo, "the settings of %s cannot be read: %v", entry.Agent, err)
			case len(setup.Args) > 0:
				e.say(problem, redo, "the status line and the gate for %s are not in %s as this version writes them", entry.Agent, entry.File)
			default:
				e.say(ok, "", "the status line and the gate for %s are set in %s", entry.Agent, entry.File)
			}
		}
	}
}

// approved names the settings of the project that can run something or
// reach outside it and are not used, because nobody approved them as they stand.
func (e *exam) approved() {
	p, err := contract.ReadProjectFile(e.c.Root)
	if err != nil {
		e.say(problem, "", "whaleshark.toml cannot be read: %v", err)
	} else if len(p.Held) > 0 {
		since := "nobody approved them yet"
		if t, _ := contract.ReadTrust(e.k.Platform.Peek, e.c.Root); !t.At.IsZero() {
			since = "they changed since the approval of " + t.At.Local().Format(time.DateOnly)
		}
		e.say(note, "whaleshark trust", "%s of this project are not used, because %s: %s", count(len(p.Held), "setting"), since, list(p.Held))
	}
}

// left is what this project's runs left behind: runs removed halfway, more
// runs than a pane reads quickly, removals of worktrees that were cut off,
// worktrees no run records, and whether anybody watches each open run.
func (e *exam) left(ours string) {
	k, root := e.k, e.c.Root
	if gone, _ := filepath.Glob(filepath.Join(ours, "runs", ".gone-*")); len(gone) > 0 {
		e.mend(problem, "the removal of a run was cut off and left "+count(len(gone), "folder"), "deleted", func() (err error) {
			for _, dir := range gone {
				err = errors.Join(err, os.RemoveAll(dir))
			}
			return err
		})
	}
	runs := e.runs[k.Platform.PathKey(root)]
	if len(runs) > manyRuns {
		var closed []string
		for _, s := range runs {
			if !s.Run.ClosedAt.IsZero() {
				closed = append(closed, s.Run.ID)
			}
		}
		e.say(note, "whaleshark run rm <id>", "%d runs are kept here, and each costs every command from a pane one read; closed: %s", len(runs), list(closed))
	}
	if !e.whole[k.Platform.PathKey(root)] {
		e.say(note, "", "the copies of the code are not checked while a run's record cannot be read")
	} else if found, err := k.Placement.Repair(root, runs, false); err != nil {
		e.say(problem, "", "the copies of the code could not be checked: %v", err)
	} else if len(found) > 0 {
		var after []string
		if e.fix {
			if _, err = k.Placement.Repair(root, runs, true); err == nil {
				after, err = k.Placement.Repair(root, runs, false)
			}
		}
		for _, line := range found {
			switch text := strings.TrimSuffix(line, "."); {
			case !e.fix:
				e.say(problem, again, "%s", text)
			case err != nil:
				e.say(problem, "", "%s, and that could not be put right: %v", text, err)
			case slices.Contains(after, line):
				e.say(problem, "", "%s: it is left as it is", text)
			default:
				e.say(fixed, "", "%s: put right", text)
			}
		}
	} else {
		e.say(ok, "", "every copy of the code belongs to a task, and no removal was cut off")
	}
	current, _ := k.Store.Current(root)
	for _, s := range runs {
		if !s.Run.ClosedAt.IsZero() {
			// A closed run keeps what it made until it is removed.
			n := 0
			for _, t := range s.Tasks {
				if w := t.Worktree; w != nil && w.RemovedAt.IsZero() {
					n++
				}
			}
			if n > 0 {
				e.say(note, "whaleshark run rm "+s.Run.ID, "run %s is closed and still holds the copies of the code of %s, with their branches and tabs: removing the run takes away what is proven merged and names the rest", s.Run.ID, count(n, "task"))
			}
			continue
		}
		dir, live := k.Store.Dir(root, s.Run.ID), 0
		for _, a := range s.Attempts {
			if a.State.Live() {
				live++
			}
		}
		var swept struct{ At time.Time }
		contract.ReadVersioned(k.Platform.Peek, filepath.Join(dir, contract.SweepFile), contract.FileVersion, &swept)
		unlock, free, err := k.Platform.TryLock(filepath.Join(dir, contract.WaitLock))
		if free {
			unlock()
		}
		switch {
		case err == nil && !free:
			e.say(ok, "", "run %s is watched: its lead agent waits", s.Run.ID)
		case e.c.Now.Sub(swept.At) < watchedFor:
			e.say(ok, "", "run %s is watched: it was last looked at %s ago", s.Run.ID, e.c.Now.Sub(swept.At).Round(time.Second))
		case live > 0:
			e.say(note, "whaleshark ui", "nobody is watching run %s (%s): nothing is raised until a pane is open or the lead agent waits", s.Run.ID, count(live, "live attempt"))
		case s.Run.ID == current:
			e.say(note, "", "nobody is watching run %s, and nothing runs in it", s.Run.ID)
		}
	}
}
