// Package launch brings workers up and takes them down: start, stop, close,
// pause and resume, and a button's long command moved into a tab of its own.
package launch

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Phase 7 owes here: a start hands Terminals.TabCreate the project's folder
// as the workspace and, for a group's leader, the fleet's name as the
// group; `stop --all`, the person's alone: every agent of the job that is
// at work stopped and, in a job of fleets, every fleet parked.

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) {
	k.Handle("start", start)
	k.Handle("stop", stop)
	k.Handle("close", closeTabs)
	k.Handle("pause", pause)
	k.Handle("resume", resume)
	k.Hold = Hold
}

func refuse(exit int, code, format string, a ...any) *contract.Refusal {
	return &contract.Refusal{Exit: exit, Code: code, Message: fmt.Sprintf(format, a...)}
}

func usage(c *contract.Call, message string) *contract.Refusal {
	r := refuse(contract.ExitUsage, "usage", "%s", message)
	r.Next = []string{"whaleshark help " + c.Command.Name}
	return r
}

// line is a command as this caller may type it: --human is the person's alone.
func line(c *contract.Call, command string) string {
	if c.Caller.Kind == contract.Human {
		command += " --human"
	}
	return "whaleshark " + command
}

// refusal is any error as what a command ends with. Terminals that cannot be
// reached and a record this program may not touch are the surroundings'
// fault, exit 3.
func refusal(err error) *contract.Refusal {
	var r *contract.Refusal
	switch {
	case errors.As(err, &r):
		return r
	case errors.Is(err, contract.ErrEngineUnreachable):
		return refuse(contract.ExitEnv, "unreachable", "%v.", err)
	case errors.Is(err, contract.ErrNewer):
		return refuse(contract.ExitEnv, "newer_state", "The record was %v.", err)
	case errors.Is(err, contract.ErrNoRun):
		r = refuse(contract.ExitMissing, "no_run", "No run is open here.")
		r.Next = []string{`whaleshark run new "<objective>"`}
		return r
	}
	return refuse(contract.ExitFailed, "failed", "%v.", err)
}

// read loads the command's run and refuses a caller the rules refuse.
func read(c *contract.Call) (*contract.State, error) {
	s, err := c.Kit.Store.Read(c.Root, c.Run)
	if err == nil {
		err = c.Kit.Rules.Allowed(s, c.Caller, c.Command.Name, "")
	}
	if err != nil {
		return nil, refusal(err)
	}
	return s, nil
}

// change is one short locked step of a command on its run.
func change(c *contract.Call, fn func(*contract.State) error) error {
	err := c.Kit.Store.Change(c.Root, c.Run, func(s *contract.State) error {
		if err := c.Kit.Rules.Allowed(s, c.Caller, c.Command.Name, ""); err != nil {
			return err
		}
		return fn(s)
	})
	if err != nil {
		return refusal(err)
	}
	return nil
}

// both is change for the command's run and a fleet's run of it, under the
// project's one lock.
func both(c *contract.Call, low string, fn func(up, low *contract.State) error) error {
	err := c.Kit.Store.Both(c.Root, c.Run, low, func(up, low *contract.State) error {
		if err := c.Kit.Rules.Allowed(up, c.Caller, c.Command.Name, ""); err != nil {
			return err
		}
		return fn(up, low)
	})
	if err != nil {
		return refusal(err)
	}
	return nil
}

// tabName is the label of the tab an attempt of t runs in: a group's is its
// fleet's, whose leader sits there.
func tabName(t *contract.Task) string { return cmp.Or(t.Fleet, t.Name) }

// byID orders ids as a person counts them: T2 before T10.
func byID(a, b string) int {
	return cmp.Or(cmp.Compare(len(a), len(b)), strings.Compare(a, b))
}

func paneOf(snap *contract.Snapshot, id string) *contract.Pane {
	if snap == nil || id == "" {
		return nil
	}
	for i := range snap.Panes {
		if snap.Panes[i].ID == id {
			return &snap.Panes[i]
		}
	}
	return nil
}

// shut closes the tab an attempt ran in, if the terminals still show it as
// ours: the same pane in the same tab under the task's name. A tab's id alone
// can be somebody else's tab once the terminals were started afresh.
func shut(k *contract.Kit, snap *contract.Snapshot, name string, p contract.Place) (bool, error) {
	pane := paneOf(snap, p.Pane)
	if !p.OpenedByUs || pane == nil || pane.Tab != p.Tab || pane.Label != name {
		return false, nil
	}
	err := k.Terms.TabClose(p.Tab)
	if errors.Is(err, contract.ErrNoPane) {
		err = nil
	} else if err != nil && strings.Contains(err.Error(), contract.Fence(contract.OpTabClose).Error()) {
		err = errFenced
	}
	return err == nil, err
}

// errFenced is a tab the keeper's fence does not let this pane close: a
// leader closes the tabs it opened itself, and one that took a fleet over
// did not open the tabs of the workers it found there.
var errFenced = errors.New("the leader before you opened it, and a tab is closed from the pane that opened it: the orchestrator's `whaleshark stop <group>` closes it, or the person")

// DropTemp removes the temp folder a start on a server gave an attempt. The
// record's word counts only for a folder that lies where such folders are
// made, or were before their place got a shorter name, under a name that
// leads nowhere else.
func DropTemp(k *contract.Kit, a *contract.Attempt) {
	dirs, err := k.Platform.Dirs()
	for _, made := range []string{filepath.Join(dirs.Cache, "tmp"), filepath.Join(filepath.Dir(dirs.Cache), "ws")} {
		if err == nil && a.Temp == filepath.Join(made, filepath.Base(a.Temp)) {
			os.RemoveAll(a.Temp)
		}
	}
}

// Leave takes away what a closed run's attempts left with the terminals and
// in the login's temp folder: each tab that is still ours, which frees its
// name for another task, and each temp folder. A keeper that is not running
// holds no tab to close.
func Leave(k *contract.Kit, s *contract.State) {
	snap, _ := k.Terms.Snapshot(context.Background())
	for _, a := range s.Attempts {
		if t := s.Tasks[a.Task]; t != nil && !a.State.Live() {
			shut(k, snap, t.Name, a.Place)
		}
		DropTemp(k, a)
	}
}

// GiveUp takes away what an ended fleet's run gives up, as a removed run
// does: its agents' tabs and temp folders, its copies of the code with the
// task branches proven merged, and its ports. The run's folder and the
// fleet's branch stay.
func GiveUp(k *contract.Kit, root, run string) {
	if low, err := k.Store.Read(root, run); err == nil {
		Leave(k, low)
		k.Placement.Forget(root, low)
	}
	contract.FreeSlots(k.Platform, func(o contract.Slot) bool { return o.Root == k.Platform.PathKey(root) && o.Run == run })
}

type stopped struct {
	Task    string `json:"task"`
	Attempt string `json:"attempt"`
	Tab     string `json:"tab"`              // closed, kept, gone, or why it was left open
	Parked  int    `json:"parked,omitempty"` // the agents of a parked fleet that were stopped with its leader
}

// stop ends a task's attempt and closes its tab. A start that is still
// bringing the attempt up finds it ended at its next step and gives up. A
// group's stop parks its fleet: the leader and every agent of the fleet are
// stopped and their tabs closed, and the fleet's run stays open. A leader
// that holds another open group sits on, and the one group is stopped.
func stop(c *contract.Call) (any, error) {
	if len(c.Args) != 1 {
		return nil, usage(c, "stop takes one task.")
	}
	k, keep := c.Kit, c.Flags["keep-tab"] != nil
	var name string
	var a contract.Attempt
	plain := func(s *contract.State) error {
		t, err := k.Rules.FindTask(s, c.Args[0])
		if err == nil {
			err = k.Rules.Stop(s, t.ID, keep, c.Now)
		}
		if err == nil {
			name, a = tabName(t), *s.Attempts[t.Attempts[len(t.Attempts)-1]]
		}
		return err
	}
	s, err := read(c)
	if err != nil {
		return nil, err
	}
	fleet, parked := map[string]contract.Attempt{}, false // the fleet's agents that go with its leader, by their tabs' names
	t, err := k.Rules.FindTask(s, c.Args[0])
	if err != nil {
		return nil, err
	}
	if f := s.Run.Fleets[t.Fleet]; f != nil && f.EndedAt.IsZero() {
		err = both(c, f.Run, func(up, low *contract.State) error {
			for _, w := range low.Attempts {
				if w.State.Live() {
					fleet[low.Tasks[w.Task].Name] = *w
				}
			}
			_, err := k.Rules.Park(up, low, t.ID, c.Now)
			if parked = err == nil; parked {
				name, a = tabName(t), *up.Attempts[t.Attempts[len(t.Attempts)-1]]
			}
			return err
		})
		var r *contract.Refusal
		if errors.As(err, &r) && (r.Code == "holds_more" || r.Code == "no_live_attempt") {
			keep, fleet = keep || r.Code == "holds_more", nil // the leader sits on with its other group
			err = change(c, plain)
		}
	} else {
		err = change(c, plain)
	}
	if err != nil {
		return nil, err
	}
	r := stopped{Task: a.Task, Attempt: a.ID, Tab: "kept", Parked: len(fleet)}
	if !keep {
		r.Tab = "gone"
		snap, err := c.Kit.Terms.Snapshot(context.Background())
		if err == nil {
			var closed bool
			if closed, err = shut(c.Kit, snap, name, a.Place); closed {
				r.Tab = "closed"
			}
			for name, w := range fleet {
				shut(k, snap, name, w.Place)
				DropTemp(k, &w)
			}
			if parked {
				// And what its ended agents left open, which a leader that
				// took the fleet over could not close itself.
				if low, lerr := k.Store.Read(c.Root, s.Run.Fleets[t.Fleet].Run); lerr == nil {
					Leave(k, low)
				}
			}
		}
		if err != nil {
			r.Tab = "left open: " + err.Error()
		}
		DropTemp(c.Kit, &a)
	}
	fmt.Fprintf(c.Out, "%s stopped (%s); its tab: %s.\n", name, a.ID, r.Tab)
	if parked {
		fmt.Fprintf(c.Out, "The fleet is parked, %d of its agents stopped with its leader: %s brings it back.\n", len(fleet), line(c, "start "+t.ID+" --retry"))
	}
	return r, nil
}

// closeTabs closes the tabs of attempts that have ended, and with each the
// to-do item about a prompt in it; a settled task gives back its copy of the
// code and its ports. A leader's tab stays while it sits there, and goes
// with its fleet: the last group settled ends the fleet's run, and what the
// run's agents left is taken away as for a run that is removed.
func closeTabs(c *contract.Call) (any, error) {
	if (c.Flags["settled"] != nil) == (len(c.Args) > 0) {
		return nil, usage(c, "close takes tasks, or --settled.")
	}
	s, err := read(c)
	if err != nil {
		return nil, err
	}
	k := c.Kit
	var tasks []*contract.Task
	for _, arg := range c.Args {
		t, err := k.Rules.FindTask(s, arg)
		if err != nil {
			return nil, err
		}
		if n := len(t.Attempts); n > 0 && s.Attempts[t.Attempts[n-1]].State.Live() {
			r := refuse(contract.ExitRefused, "still_live", "%s has a live attempt.", t.ID)
			r.Next = []string{line(c, "stop "+t.ID)}
			return nil, r
		}
		tasks = append(tasks, t)
	}
	if len(c.Args) == 0 {
		for _, t := range s.Tasks {
			tasks = append(tasks, t)
		}
		slices.SortFunc(tasks, func(a, b *contract.Task) int { return byID(a.ID, b.ID) })
	}
	snap, err := k.Terms.Snapshot(context.Background())
	if err != nil {
		return nil, refusal(err)
	}
	closed, items, over := []stopped{}, []string{}, []string{}
	for _, t := range tasks {
		f := s.Run.Fleets[t.Fleet]
		if f != nil && f.EndedAt.IsZero() && !slices.Contains(over, t.Fleet) &&
			both(c, f.Run, func(up, low *contract.State) error { return k.Rules.Ended(up, low, false, c.Now) }) == nil {
			over = append(over, t.Fleet)
			GiveUp(k, c.Root, f.Run)
			fmt.Fprintf(c.Out, "ended: the fleet %s (run %s)\n", t.Fleet, f.Run)
		}
		for _, id := range t.Attempts {
			a := s.Attempts[id]
			if a.State.Live() || f != nil && f.Pane == a.Place.Pane && !slices.Contains(over, t.Fleet) {
				continue
			}
			did, err := shut(k, snap, tabName(t), a.Place)
			if err == errFenced {
				fmt.Fprintf(c.Out, "kept: the tab of %s: %v\n", t.Name, err)
			} else if err != nil {
				return nil, refusal(err)
			}
			DropTemp(k, a)
			if did {
				closed = append(closed, stopped{Task: t.ID, Attempt: id, Tab: "closed"})
				fmt.Fprintf(c.Out, "closed: %s (%s)\n", t.Name, id)
			}
			for _, q := range s.Questions {
				if q.From == contract.FromTool && q.Cause == contract.CausePrompt && q.Attempt == id && q.State == contract.QuestionOpen {
					items = append(items, q.ID)
				}
			}
		}
	}
	if len(closed) == 0 {
		fmt.Fprintln(c.Out, "No tab to close.")
	}
	// A task's own copy of the code goes with its tab once the task is done;
	// that of a failed or cancelled one only with --discard, which loses
	// what was not committed there. The branch always stays.
	discard, removed, done := c.Flags["discard"] != nil, map[string]*contract.Worktree{}, []string{}
	var kept error
	for _, t := range tasks {
		over := t.Status == contract.TaskFailed || t.Status == contract.TaskCancelled
		if t.Status != contract.TaskDone && !over {
			continue
		}
		done = append(done, t.ID)
		if w := t.Worktree; w == nil || !w.RemovedAt.IsZero() || c.Flags["keep-worktree"] != nil {
			continue
		}
		if over && !discard {
			fmt.Fprintf(c.Out, "kept: the copy of the code of %s, which is %s; %s removes it\n", t.ID, t.Status, line(c, "close "+t.ID+" --discard"))
			continue
		}
		w, err := k.Placement.Remove(c.Root, s, t.ID, discard)
		if err != nil {
			fmt.Fprintf(c.Out, "kept: %s\n", refusal(err).Message)
			kept = cmp.Or(kept, err)
			continue
		}
		removed[t.ID] = w
		fmt.Fprintf(c.Out, "removed: the copy of the code of %s; its branch %s stays\n", t.ID, w.Branch)
	}
	if len(items)+len(removed) > 0 {
		err = change(c, func(s *contract.State) error {
			for _, id := range items {
				if err := k.Rules.CloseQuestion(s, id, c.Now); err != nil {
					return err
				}
			}
			for id, w := range removed {
				if err := k.Rules.SetWorktree(s, id, w, c.Now); err != nil {
					return err
				}
			}
			return nil
		})
	}
	if root := k.Platform.PathKey(c.Root); err == nil {
		_, err = contract.FreeSlots(k.Platform, func(o contract.Slot) bool {
			return o.Root == root && o.Run == c.Run && slices.Contains(done, o.Task)
		})
	}
	if err == nil && kept != nil {
		return nil, refusal(kept)
	}
	return map[string]any{"closed": closed, "removed": slices.Sorted(maps.Keys(removed)), "ended": over}, err
}

// OwnTab moves a button's long command into a tab of its own (decision 57):
// a pane or the page waits only milliseconds for its child, and such a
// command runs for minutes and needs a tab's surroundings. argv is the
// command's own line and holds ids and switches, nothing else. moved is nil
// for every other caller, who runs the command where it is.
func OwnTab(c *contract.Call, label string, argv ...string) (moved any, err error) {
	w := c.Caller.Where
	if c.Caller.Kind != contract.Human || w != contract.WherePane && w != contract.WherePage && w != contract.WherePhone || os.Getenv(contract.EnvOwnTab) != "" {
		return nil, nil
	}
	k := c.Kit
	self, err := k.Platform.SelfPath()
	if err != nil {
		return nil, err
	}
	label = c.Command.Name + " " + label
	pane, err := k.Terms.TabCreate("", "", c.Root, label, []string{contract.EnvOwnTab + "=" + w})
	if err == nil {
		argv = append([]string{self, c.Command.Name}, append(argv, "--human", "--root", c.Root, "--run", c.Run)...)
		err = k.Terms.Run(pane.ID, argv)
	}
	if err != nil {
		return nil, refusal(err)
	}
	fmt.Fprintf(c.Out, "It runs in a tab of its own: %s.\n", label)
	return map[string]string{"tab": pane.Tab, "label": label}, nil
}

// CloseOwnTab closes the tab OwnTab opened for this command, once it has
// ended well; one that failed keeps its tab, with what it printed.
func CloseOwnTab(c *contract.Call) {
	if os.Getenv(contract.EnvOwnTab) == "" {
		return
	}
	snap, err := c.Kit.Terms.Snapshot(context.Background())
	if p := paneOf(snap, c.Caller.Pane); err == nil && p != nil && strings.HasPrefix(p.Label, c.Command.Name+" ") {
		c.Kit.Terms.TabClose(p.Tab)
	}
}
