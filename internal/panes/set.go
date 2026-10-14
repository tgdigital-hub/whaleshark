package panes

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// screen reads ui.json for a command that may write it back.
func screen(k *contract.Kit) (dirs contract.Dirs, ui contract.UIFile, file string, err error) {
	if dirs, err = k.Platform.Dirs(); err == nil {
		file = filepath.Join(dirs.State, contract.UIFileName)
		err = contract.ReadVersioned(k.Platform.Read, file, contract.FileVersion, &ui)
	}
	return dirs, ui, file, err
}

// set changes a setting. Most are a row of the contract's table and are set
// there; here are the ways of its own: a profile saved or loaded, a time for
// do not disturb, an open pane that moves or is fitted, and a pane's own
// marks: folded, here, seen, steps, shut <run>/<fleet> and draft.<item> for
// a half-typed answer. They are the person's: any other caller's change is
// the table's to decide, or an agent could set by a way of ours what the
// table has it ask for.
func set(c *contract.Call) (any, error) {
	k := c.Kit
	bad := func(format string, a ...any) (any, error) {
		return nil, &contract.Refusal{Exit: contract.ExitUsage, Code: "usage", Message: fmt.Sprintf(format, a...), Next: []string{"whaleshark help set"}}
	}
	human := c.Caller.Kind == contract.Human
	if file := c.Flags["save"]; human && file != nil {
		return save(c, file[len(file)-1])
	} else if file = c.Flags["load"]; human && file != nil {
		return load(c, file[len(file)-1])
	} else if len(c.Args) == 0 {
		return bad("Usage: whaleshark %s", c.Command.Usage)
	}
	key, value := c.Args[0], strings.Join(c.Args[1:], " ")
	own := key
	if !human && key == "dnd" && strings.HasPrefix(value, "until") {
		// The table's writer knows do not disturb as on or off: asked for
		// with a time, the person's yes would switch it off.
		return nil, &contract.Refusal{Exit: contract.ExitRefused, Code: "human_only", Message: "Do not disturb until a time is the person's to set. You can ask for it without one: whaleshark set dnd on"}
	} else if !human {
		own = ""
	}
	unlock := placing(k)
	defer func() { unlock() }()
	dirs, ui, file, err := screen(k)
	if err != nil {
		return nil, err
	}
	draft, isDraft := strings.CutPrefix(own, "draft.")
	var onOff *bool
	switch {
	case own == "folded":
		onOff = &ui.Folded
	case own == "dnd" && strings.HasPrefix(value, "until"):
		at, err := time.ParseInLocation("15:04", strings.TrimSpace(value[len("until"):]), c.Now.Location())
		if err != nil {
			return bad("Do not disturb takes a time of day: whaleshark set dnd until 9:00")
		}
		y, m, d := c.Now.Date()
		ui.DND, ui.DNDUntil = true, time.Date(y, m, d, at.Hour(), at.Minute(), 0, 0, c.Now.Location())
		if !ui.DNDUntil.After(c.Now) {
			ui.DNDUntil = ui.DNDUntil.AddDate(0, 0, 1)
		}
	case own == "dnd":
		onOff, ui.DNDUntil = &ui.DND, time.Time{}
	case own == "fleet.width":
		n, err := strconv.Atoi(value)
		if err != nil || n < 10 || n > 500 {
			return bad("The fleet's width is a number of columns.")
		}
		snap, _ := k.Terms.Snapshot(context.Background())
		if ui.FleetWidth = n; fit(k.Terms, c.Caller.Pane, pairOf(ui, snap, rootOf(snap, c.Caller.Pane)).Fleet, n, false, person(k, dirs).Panes.Fleet == contract.Left) != nil {
			fmt.Fprintln(c.Out, "the open fleet keeps its width; the next one opens at this one")
		}
	case own == "here":
		ui.LastHere = c.Now
	case own == "shut":
		// A fleet of the left section is folded, or one that was is opened.
		if at := slices.Index(ui.Shut, value); at >= 0 {
			ui.Shut = slices.Delete(ui.Shut, at, at+1)
		} else {
			ui.Shut = append(ui.Shut, value)
		}
	case own == "seen":
		// The settings screen showed a section again: its warnings end, of
		// what runs a program and of what an agent changed.
		gone := func(name string) bool {
			row, _ := contract.FindKey(name)
			return row == nil || row.Section == value
		}
		ui.Changed = slices.DeleteFunc(ui.Changed, gone)
		ui.ByAgent = slices.DeleteFunc(ui.ByAgent, func(a contract.SetAsk) bool { return gone(a.Key) })
	case own == "ui.first_steps" && value == contract.On:
		ui.FirstSteps, ui.Steps = value, 0 // switched on again, they begin again
	case own == "steps":
		// The first steps that have happened, a bit each: never taken back.
		n, _ := strconv.Atoi(value)
		ui.Steps |= n
	case isDraft && value == "":
		delete(ui.Drafts, draft)
	case isDraft:
		if ui.Drafts == nil {
			ui.Drafts = map[string]string{}
		}
		ui.Drafts[draft] = value
	default:
		// Every other key of the contract's table is set in its own file. A
		// key that places a pane of ours moves the open panes at once.
		// The table writes ui.json under the lock itself, and what is open
		// is read again: the left section may have placed the panes since.
		unlock()
		value, err = contract.Set(c, key, value)
		unlock = placing(k)
		_, ui, _, _ = screen(k)
		snap, serr := k.Terms.Snapshot(context.Background())
		unlock()
		unlock = func() {}
		pair := pairOf(ui, snap, rootOf(snap, c.Caller.Pane))
		if err == nil && serr == nil && (strings.HasPrefix(key, "panes.") || key == actions) {
			// They move within the tab they are open in, in the workspace the
			// setting was changed from; with none open, beside whoever changed it.
			at := c.Caller.Pane
			for _, p := range snap.Panes {
				if p.Ours && (p.ID == pair.Fleet || p.ID == pair.Actions) {
					at = p.ID
				}
			}
			err = k.Panes(k, at)
		} else if n, _ := strconv.Atoi(value); err == nil && serr == nil && key == "actions.height" && n > 0 {
			// A figure typed for a line moves the line at once, as a drag
			// does; the keys stay where they are, in whichever workspace.
			if fit(k.Terms, beside(snap, pair.Actions), pair.Actions, n, true, person(k, dirs).Panes.Actions == contract.Top) != nil {
				fmt.Fprintln(c.Out, "the open action pane keeps its height; the next one opens at this one")
			}
		}
		if err != nil {
			return nil, err
		}
		if f := person(k, dirs).Fleets; strings.HasPrefix(key, "fleets.") && f.Total != contract.Unlimited && (f.Total < f.Agents || f.Agents == contract.Unlimited) {
			fmt.Fprintf(c.Out, "one fleet could use all %d: the limit of all agents is under a fleet's\n", f.Total)
		}
		fmt.Fprintln(c.Out, key+": "+value)
		return map[string]string{"key": key, "value": value}, nil
	}
	if onOff != nil && value != "on" && value != "off" && value != "toggle" {
		return bad("%s is on or off.", key)
	} else if onOff != nil {
		// toggle is what a shortcut says: it cannot know which way it is.
		*onOff = value == "on" || value == "toggle" && !*onOff
		value = map[bool]string{true: "on", false: "off"}[*onOff]
	}
	// Do not disturb from a phone is flagged as the table's holding keys are.
	if c.Caller.Where == contract.WherePhone && key == "dnd" && !slices.Contains(ui.Changed, key) {
		ui.Changed = append(ui.Changed, key)
	}
	ui.Version = contract.FileVersion
	if err = contract.WriteVersioned(k.Platform, file, contract.FileVersion, ui); err != nil {
		return nil, err
	}
	if key != "here" && key != "seen" && key != "steps" && key != "folded" && key != "shut" && !isDraft {
		fmt.Fprintln(c.Out, key+": "+value)
	}
	return map[string]string{"key": key, "value": value}, nil
}
