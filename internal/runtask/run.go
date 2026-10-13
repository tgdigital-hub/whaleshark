package runtask

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/launch"
)

func run(c *contract.Call) (any, error) {
	if len(c.Args) > 0 && c.Args[0] == "fleet" {
		return runFleet(c, c.Args[1:])
	}
	sub, args, err := form(c, map[string]int{"fleet": 0, "new": 1, "list": 0, "show": 0, "use": 1, "close": 0, "rm": 1, "takeover": 0})
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
	// An orchestrator's run takes its fleets' runs with it, each closed or
	// removed as a run is, once the command is known to be let in: no fleet
	// goes under a job that stays.
	var fleets []string
	if s, lost := k.Store.Read(c.Root, id); lost == nil && k.Rules.Allowed(s, c.Caller, c.Command.Name, sub) == nil {
		for _, f := range s.Run.Fleets {
			fleets = append(fleets, f.Run)
		}
	}
	// ended closes the run and its fleets' runs, by the rule that closes any
	// run: asked of what was read first, the run's own record last, then
	// done. For a removal a run that is closed already is passed over.
	ended := func(abandon, removal bool) {
		shut := func(s *contract.State) error { return k.Rules.CloseRun(s, abandon, c.Now) }
		for _, write := range []bool{false, true} {
			for _, run := range append(fleets, id) {
				low, lost := k.Store.Read(c.Root, run)
				switch {
				case err != nil || (run != id || removal) && (lost != nil || !low.Run.ClosedAt.IsZero()):
				case !write && len(fleets) > 0:
					err = shut(low)
				case !write:
				case run == id:
					err = change(c, id, sub, shut)
				default:
					err = k.Store.Change(c.Root, run, shut)
				}
			}
		}
	}
	switch sub {
	case "close":
		said = "closed"
		ended(c.Flags["abandon"] != nil, false)
		// The tabs of its ended agents go with the job, as at a removal; the
		// copies of the code stay until then.
		for _, run := range append(fleets, id) {
			if s, lost := k.Store.Read(c.Root, run); err == nil && lost == nil {
				launch.Leave(k, s)
			}
		}
	case "rm":
		said = "removed"
		ended(false, true)
		for _, id := range append(fleets, id) {
			// A fleet's run was let in with the run above it.
			if err == nil && id == args[0] {
				err = change(c, id, sub, func(*contract.State) error { return nil })
			}
			// What the run left in git goes first: its worktrees and the task
			// branches proven merged, which is asked of the collected branch, then
			// that branch if it was landed, then its port slots.
			var s *contract.State
			if err == nil {
				s, err = k.Store.Read(c.Root, id)
			}
			var kept []string
			if err == nil {
				launch.Leave(k, s)
				kept, err = k.Placement.Forget(c.Root, s)
			}
			unlanded := ""
			if err == nil {
				unlanded, err = k.Integrator.Drop(c.Root, s)
			}
			if err == nil {
				root := k.Platform.PathKey(c.Root)
				_, err = contract.FreeSlots(k.Platform, func(o contract.Slot) bool { return o.Root == root && o.Run == id })
			}
			if cur, _ := k.Store.Current(c.Root); err == nil && cur == id {
				err = k.Store.SetCurrent(c.Root, "") // the number is given again, and the person's pointer must not follow it
			}
			if err == nil {
				err = k.Store.Remove(c.Root, id)
			}
			for _, branch := range kept {
				fmt.Fprintf(c.Out, "Kept, not proven merged: %s\n", cli.Plain(branch))
			}
			if unlanded != "" {
				fmt.Fprintf(c.Out, "Kept, it holds accepted work that was not landed: %s\n", cli.Plain(unlanded))
			}
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
	// A closed run binds no tab and goes nowhere: the open ones are named instead.
	var r *contract.Refusal
	if errors.As(err, &r) && r.Code == "closed" && (sub == "takeover" || sub == "use") {
		for _, other := range runs {
			if s, err := k.Store.Read(c.Root, other); err == nil && s.Run.ClosedAt.IsZero() {
				r.Next = append(r.Next, "whaleshark run "+map[string]string{"use": "use ", "takeover": "takeover --run "}[sub]+other)
			}
		}
		if len(r.Next) == 0 {
			r.Next = []string{`whaleshark run new "<objective>"`}
		}
	}
	if err != nil {
		return nil, err
	}
	if (sub == "takeover" || sub == "use") && c.Caller.Kind != contract.Human {
		k.Panes(k, pane)
		mainTab(k, pane)
	}
	fmt.Fprintf(c.Out, "Run %s is %s.\n", id, said)
	// What a closed run leaves (its tabs with their names, its copies of
	// the code, its branches) stays until the run is removed.
	if s, err := k.Store.Read(c.Root, id); sub == "close" && err == nil && len(s.Attempts) > 0 {
		fmt.Fprintf(c.Out, "Its tabs, copies of the code and branches stay until it is removed.\nNext: whaleshark run rm %s\n", id)
	}
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
	// Fleet Mode is the person's setting of this moment, for as long as the run
	// lives. Its attempts are groups, of which a leader can hold several: the
	// agents under them are held by the person's three limits, not by the run's.
	limit, mode := project.Limits.Agents, ""
	if dirs, err := k.Platform.Dirs(); err == nil {
		if person, _ := contract.ReadPerson(k.Platform.Read, dirs.Config); person.Fleets.Mode == contract.On {
			limit, mode = 1000, contract.ModeFleets
		}
	}
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
	// Where the project has git, accepted work is collected on a branch of
	// the run's own, which begins at the base: land needs both written down.
	// A number whose branch a removed run left with work on it is passed over.
	var s *contract.State
	var in *contract.Integration
	for taken, named := new(*contract.Refusal), base; ; n++ {
		s = k.Rules.NewRun("r"+strconv.Itoa(n), objective, limit, named, by, c.Now)
		if base, in, err = k.Integrator.Begin(c.Root, s); !errors.As(err, taken) || (*taken).Code != "branch_exists" {
			break
		}
	}
	if err == nil && in != nil {
		s.Run.Base = base
		err = k.Rules.Integrated(s, *in)
	}
	if err != nil {
		return nil, err
	}
	s.Run.Mode = mode
	if err = k.Store.Create(c.Root, s); err == nil && !park {
		err = k.Store.SetCurrent(c.Root, s.Run.ID)
	}
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(c.Out, "Run %s is open.\n", s.Run.ID)
	if by.Pane == "" {
		fmt.Fprintln(c.Out, "No tab drives it yet: the lead agent binds it with `whaleshark run takeover`.")
	} else {
		k.Panes(k, by.Pane)
		mainTab(k, by.Pane)
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

// mainTab names the tab a run's lead agent sits in, where nobody has named
// it: Orchestrator, with Fleet Mode on or off. A tab that has a name keeps it.
func mainTab(k *contract.Kit, pane string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snap, err := k.Terms.Snapshot(ctx)
	if err != nil {
		return
	}
	for _, p := range snap.Panes {
		if p.ID == pane && p.Label == "" {
			k.Terms.TabRename(p.Tab, contract.MainTab)
		}
	}
}

// runFleet keeps the fleets a person saved for this project: with a name
// and what it works on it saves one, or says anew what it works on; with
// --forget it is saved no longer, and ends with its work as any fleet;
// with nothing it lists them. A saved fleet has no leader and no run until
// a group is placed in it and started, and it stays when that work is done.
func runFleet(c *contract.Call, args []string) (any, error) {
	k := c.Kit
	saved, err := contract.ReadFleets(k.Platform.Read, c.Root)
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		shown, _ := contract.FleetsShown(k.Platform.Peek, c.Root, c.Caller.Kind)
		for _, f := range shown {
			fmt.Fprintln(c.Out, strings.TrimSuffix(f.Name+" · saved · "+f.Purpose, " · "))
		}
		if len(saved) == 0 {
			fmt.Fprintln(c.Out, `No fleet is saved here. whaleshark run fleet "<name>" "<what it works on>" saves one.`)
		}
		return saved, nil
	}
	name, ok := contract.FleetName(args[0])
	purpose := ""
	if len(args) > 1 {
		purpose = cli.Line(args[1])
	}
	at := slices.IndexFunc(saved, func(f contract.SavedFleet) bool { return strings.EqualFold(f.Name, name) })
	forget := c.Flags["forget"] != nil
	switch {
	case !ok || len(args) > 2 || len([]rune(purpose)) > 200:
		return nil, usage(c, "A fleet is named in at most three words and 24 characters, with no dash in front, and what it works on is one line of at most 200 characters.")
	case forget && at < 0, !forget && at < 0 && purpose == "":
		r := refuse(contract.ExitMissing, "no_such_fleet", fmt.Sprintf("No fleet %q is saved here.", name), `whaleshark run fleet "`+name+`" "<what it works on>"`)
		return nil, r
	case forget:
		name = saved[at].Name
		saved = slices.Delete(saved, at, at+1)
		fmt.Fprintf(c.Out, "The fleet %q is saved no longer. Work it has goes on, and it ends with that work.\n", name)
	case at >= 0:
		if name = saved[at].Name; purpose != "" {
			saved[at].Purpose = purpose
		}
		said := saved[at].Purpose
		if purpose == "" && c.Caller.Kind != contract.Human {
			said = "" // an agent that names a fleet is not told what the file says of it: the list decides that
		}
		fmt.Fprintln(c.Out, strings.TrimSuffix(fmt.Sprintf("The fleet %q is saved · %s", name, said), " · "))
	default:
		saved = append(saved, contract.SavedFleet{Name: name, Purpose: purpose, Since: c.Now})
		fmt.Fprintf(c.Out, "The fleet %q is saved · %s\nIt stays when its work is done. Work goes into it with: whaleshark task add <id> \"<title>\" --fleet %q ...\n", name, purpose, name)
		if dirs, err := k.Platform.Dirs(); err == nil {
			if person, _ := contract.ReadPerson(k.Platform.Peek, dirs.Config); person.Fleets.Mode != contract.On {
				fmt.Fprintln(c.Out, "Fleet Mode is off: a fleet takes work in a job begun with it on (whaleshark set fleets.mode on).")
			}
		}
	}
	// The project's folder of ours, where this is the first thing kept in
	// it, is made as the store makes it: the login's own, and in its list.
	if dir := filepath.Join(c.Root, contract.ProjectDir); os.MkdirAll(dir, 0o700) == nil {
		if err := contract.ListProject(k.Platform, c.Root); err != nil {
			return nil, err
		}
	}
	return saved, contract.WriteFleets(k.Platform, c.Root, saved)
}
