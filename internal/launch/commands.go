// Package launch brings workers up and takes them down: start, stop, close,
// pause and resume, and a button's long command moved into a tab of its own.
package launch

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) {
	k.Handle("start", start)
	k.Handle("stop", stop)
	k.Handle("close", closeTabs)
	k.Handle("pause", pause)
	k.Handle("resume", resume)
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
	}
	return err == nil, err
}

type stopped struct {
	Task    string `json:"task"`
	Attempt string `json:"attempt"`
	Tab     string `json:"tab"` // closed, kept, gone, or why it was left open
}

// stop ends a task's attempt and closes its tab. A start that is still
// bringing the attempt up finds it ended at its next step and gives up.
func stop(c *contract.Call) (any, error) {
	if len(c.Args) != 1 {
		return nil, usage(c, "stop takes one task.")
	}
	keep := c.Flags["keep-tab"] != nil
	var name string
	var a contract.Attempt
	err := change(c, func(s *contract.State) error {
		t, err := c.Kit.Rules.FindTask(s, c.Args[0])
		if err == nil {
			err = c.Kit.Rules.Stop(s, t.ID, keep, c.Now)
		}
		if err == nil {
			name, a = t.Name, *s.Attempts[t.Attempts[len(t.Attempts)-1]]
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	r := stopped{Task: a.Task, Attempt: a.ID, Tab: "kept"}
	if !keep {
		r.Tab = "gone"
		snap, err := c.Kit.Terms.Snapshot(context.Background())
		if err == nil {
			var closed bool
			if closed, err = shut(c.Kit, snap, name, a.Place); closed {
				r.Tab = "closed"
			}
		}
		if err != nil {
			r.Tab = "left open: " + err.Error()
		}
	}
	fmt.Fprintf(c.Out, "%s stopped (%s); its tab: %s.\n", name, a.ID, r.Tab)
	return r, nil
}

// closeTabs closes the tabs of attempts that have ended, and with each the
// to-do item about a prompt in it.
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
	closed, items := []stopped{}, []string{}
	for _, t := range tasks {
		for _, id := range t.Attempts {
			a := s.Attempts[id]
			if a.State.Live() {
				continue
			}
			did, err := shut(k, snap, t.Name, a.Place)
			if err != nil {
				return nil, refusal(err)
			}
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
	if len(items) > 0 {
		err = change(c, func(s *contract.State) error {
			for _, id := range items {
				if err := k.Rules.CloseQuestion(s, id, c.Now); err != nil {
					return err
				}
			}
			return nil
		})
	}
	return map[string]any{"closed": closed}, err
}

// ownTabEnv marks a tab that was opened for one command of ours, which
// closes it when it ends well.
const ownTabEnv = "WHALESHARK_OWN_TAB"

// OwnTab moves a button's long command into a tab of its own (decision 57):
// a pane or the page waits only milliseconds for its child, and such a
// command runs for minutes and needs a tab's surroundings. argv is the
// command's own line and holds ids and switches, nothing else. moved is nil
// for every other caller, who runs the command where it is.
func OwnTab(c *contract.Call, label string, argv ...string) (moved any, err error) {
	if w := c.Caller.Where; c.Caller.Kind != contract.Human || w != contract.WherePane && w != contract.WherePage && w != contract.WherePhone {
		return nil, nil
	}
	k := c.Kit
	self, err := k.Platform.SelfPath()
	if err != nil {
		return nil, err
	}
	label = c.Command.Name + " " + label
	pane, err := k.Terms.TabCreate(c.Root, label, []string{ownTabEnv + "=1"})
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
	if os.Getenv(ownTabEnv) == "" {
		return
	}
	snap, err := c.Kit.Terms.Snapshot(context.Background())
	if p := paneOf(snap, c.Caller.Pane); err == nil && p != nil && strings.HasPrefix(p.Label, c.Command.Name+" ") {
		c.Kit.Terms.TabClose(p.Tab)
	}
}
