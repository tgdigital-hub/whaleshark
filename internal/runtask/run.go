package runtask

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/launch"
)

func run(c *contract.Call) (any, error) {
	sub, args, err := form(c, map[string]int{"new": 1, "list": 0, "show": 0, "use": 1, "close": 0, "rm": 1, "takeover": 0})
	if err != nil {
		return nil, err
	}
	k, id, said := c.Kit, c.Run, ""
	runs, err := k.Store.Runs(c.Root)
	switch {
	case err != nil:
		return nil, err
	case sub == "new":
		return runNew(c, args[0], runs)
	case sub == "list":
		return runList(c, runs)
	case sub == "show":
		return runList(c, []string{id})
	case len(args) == 1:
		if id = args[0]; !slices.Contains(runs, id) {
			return nil, refuse(contract.ExitMissing, "no_run", fmt.Sprintf("There is no run %.20q here.", id), "whaleshark run list")
		}
	}
	// A run is never bound to the person's own tab: a bound pane is an
	// agent's, and nothing in it is done as the person afterwards.
	pane := c.Caller.Pane
	bind := func(s *contract.State) error {
		if pane == "" || c.Caller.Kind == contract.Human {
			return refuse(contract.ExitRefused, "no_pane", "A run is bound to the tab this is typed in: run it in the lead agent's tab.")
		}
		return k.Rules.Takeover(s, contract.Binding{Pane: pane}, c.Now)
	}
	switch sub {
	case "close":
		said = "closed"
		err = change(c, id, sub, func(s *contract.State) error { return k.Rules.CloseRun(s, c.Flags["abandon"] != nil, c.Now) })
	case "rm":
		// A run still open is closed first, by the rule that closes any run.
		said = "removed"
		err = change(c, id, sub, func(s *contract.State) error {
			if !s.Run.ClosedAt.IsZero() {
				return nil
			}
			return k.Rules.CloseRun(s, false, c.Now)
		})
		// What the run left in git goes first: its worktrees and the task
		// branches proven merged, which is asked of the collected branch, then
		// that branch if it was landed, then its port slots.
		var s *contract.State
		if err == nil {
			s, err = k.Store.Read(c.Root, id)
		}
		var kept []string
		if err == nil {
			for _, a := range s.Attempts {
				launch.DropTemp(k, a)
			}
			kept, err = k.Placement.Forget(c.Root, s)
		}
		if err == nil {
			err = k.Integrator.Drop(c.Root, s)
		}
		if err == nil {
			root := k.Platform.PathKey(c.Root)
			_, err = contract.FreeSlots(k.Platform, func(o contract.Slot) bool { return o.Root == root && o.Run == id })
		}
		if err == nil {
			err = k.Store.Remove(c.Root, id)
		}
		for _, branch := range kept {
			fmt.Fprintf(c.Out, "Kept, not proven merged: %s\n", cli.Plain(branch))
		}
	case "takeover":
		said = "bound to this tab"
		err = change(c, id, sub, bind)
	case "use":
		// The person's switch moves the pointer their commands follow. An
		// agent's moves its own pane only, which takes the run over and lets
		// go of every other: a pane's command goes to the newest run it drives.
		if said = "where your commands go now"; c.Caller.Kind == contract.Human {
			err = k.Store.SetCurrent(c.Root, id)
			break
		}
		said = "what this tab drives now"
		err = change(c, id, sub, bind)
		for _, other := range runs {
			if other == id || err != nil {
				continue
			}
			err = k.Store.Change(c.Root, other, func(s *contract.State) error {
				if o := s.Run.Orchestrator; o == nil || o.Pane != pane || !s.Run.ClosedAt.IsZero() {
					return nil
				}
				return k.Rules.Takeover(s, contract.Binding{}, c.Now)
			})
		}
	}
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(c.Out, "Run %s is %s.\n", id, said)
	return map[string]string{"run": id}, nil
}

func runNew(c *contract.Call, objective string, runs []string) (any, error) {
	k, park := c.Kit, c.Flags["park"] != nil
	switch {
	case cli.Valid("text", objective, "") != nil || strings.TrimSpace(objective) == "":
		return nil, usage(c, "A run needs its objective, in plain text.")
	case c.Flags["base"] != nil && cli.Valid("title", last(c, "base"), "") != nil:
		return nil, usage(c, "--base names a branch, a tag or a commit.")
	}
	if c.Run != "" && !park {
		if s, err := k.Store.Read(c.Root, c.Run); err == nil && s.Run.ClosedAt.IsZero() {
			return nil, refuse(contract.ExitRefused, "run_open", "Run "+c.Run+" is still open. Close it first, or leave it open with --park.",
				"whaleshark run close", `whaleshark run new "<objective>" --park`)
		}
	}
	n := 1
	for _, id := range runs {
		if m, err := strconv.Atoi(strings.TrimPrefix(id, "r")); err == nil && m >= n {
			n = m + 1
		}
	}
	project, err := contract.ReadProjectFile(c.Root)
	if err != nil {
		return nil, err
	}
	limit := project.Limits.Agents
	if v := last(c, "limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	by := contract.Binding{}
	if c.Caller.Kind != contract.Human {
		by.Pane = c.Caller.Pane
	}
	var base *contract.GitRef
	if name := last(c, "base"); name != "" {
		base = &contract.GitRef{Ref: name}
	}
	s := k.Rules.NewRun("r"+strconv.Itoa(n), objective, limit, base, by, c.Now)
	// Where the project has git, accepted work is collected on a branch of
	// the run's own, which begins at the base: land needs both written down.
	base, in, err := k.Integrator.Begin(c.Root, s)
	if err == nil && in != nil {
		s.Run.Base = base
		err = k.Rules.Integrated(s, *in)
	}
	if err != nil {
		return nil, err
	}
	if err = k.Store.Create(c.Root, s); err == nil && !park {
		err = k.Store.SetCurrent(c.Root, s.Run.ID)
	}
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(c.Out, "Run %s is open.\n", s.Run.ID)
	if by.Pane == "" {
		fmt.Fprintln(c.Out, "No tab drives it yet: the lead agent binds it with `whaleshark run takeover`.")
	}
	return map[string]string{"run": s.Run.ID, "pane": by.Pane}, nil
}

// runLine is one run as run list and run show print it.
type runLine struct {
	ID        string `json:"id"`
	Objective string `json:"objective"`
	State     string `json:"state"`
	Pane      string `json:"pane"`
	Limit     int    `json:"limit"`
	Tasks     int    `json:"tasks"`
	Done      int    `json:"done"`
	Live      int    `json:"live"`
}

// runList prints runs with their counts, and how many are kept: every
// command from a pane reads each of them once to learn whose pane it is.
func runList(c *contract.Call, runs []string) (any, error) {
	lines := []runLine{}
	for _, id := range runs {
		if id == "" {
			return nil, noRun()
		}
		s, err := c.Kit.Store.Read(c.Root, id)
		if err != nil {
			return nil, err
		}
		l := runLine{ID: id, Objective: s.Run.Objective, State: "open", Pane: "none", Limit: s.Run.Limit, Tasks: len(s.Tasks)}
		if !s.Run.ClosedAt.IsZero() {
			l.State = "closed"
		} else if s.Run.Paused != nil {
			l.State = "paused"
		}
		if o := s.Run.Orchestrator; o != nil && o.Pane != "" {
			l.Pane = o.Pane
		}
		for _, t := range s.Tasks {
			if t.Status == contract.TaskDone {
				l.Done++
			}
		}
		for _, a := range s.Attempts {
			if a.State.Live() {
				l.Live++
			}
		}
		fmt.Fprintf(c.Out, "%-4s %-6s %d tasks, %d done, %d of %d agents at work, pane %s  %s\n",
			id, l.State, l.Tasks, l.Done, l.Live, l.Limit, l.Pane, cli.Plain(l.Objective))
		lines = append(lines, l)
	}
	if c.Args[0] == "list" {
		fmt.Fprintf(c.Out, "%d kept. Each costs every command from a pane one read; remove a finished one with `whaleshark run rm <id>`.\n", len(lines))
	}
	return map[string]any{"runs": lines, "kept": len(lines)}, nil
}
