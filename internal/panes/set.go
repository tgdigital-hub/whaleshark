package panes

import (
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
// there; here are the ways of its own: a time for do not disturb, an open
// pane that moves or is fitted, and a pane's own marks: folded, here, seen,
// shut <run>/<fleet> and draft.<item> for a half-typed answer.
func set(c *contract.Call) (any, error) {
	k := c.Kit
	bad := func(format string, a ...any) (any, error) {
		return nil, &contract.Refusal{Exit: contract.ExitUsage, Code: "usage", Message: fmt.Sprintf(format, a...), Next: []string{"whaleshark help set"}}
	}
	if len(c.Args) == 0 {
		return bad("Usage: whaleshark %s", c.Command.Usage)
	}
	key, value := c.Args[0], strings.Join(c.Args[1:], " ")
	dirs, ui, file, err := screen(k)
	if err != nil {
		return nil, err
	}
	draft, isDraft := strings.CutPrefix(key, "draft.")
	var onOff *bool
	switch {
	case key == "folded":
		onOff = &ui.Folded
	case key == "dnd" && strings.HasPrefix(value, "until"):
		at, err := time.ParseInLocation("15:04", strings.TrimSpace(value[len("until"):]), c.Now.Location())
		if err != nil {
			return bad("Do not disturb takes a time of day: whaleshark set dnd until 9:00")
		}
		y, m, d := c.Now.Date()
		ui.DND, ui.DNDUntil = true, time.Date(y, m, d, at.Hour(), at.Minute(), 0, 0, c.Now.Location())
		if !ui.DNDUntil.After(c.Now) {
			ui.DNDUntil = ui.DNDUntil.AddDate(0, 0, 1)
		}
	case key == "dnd":
		onOff, ui.DNDUntil = &ui.DND, time.Time{}
	case key == "fleet.width":
		n, err := strconv.Atoi(value)
		if err != nil || n < 10 || n > 500 {
			return bad("The fleet's width is a number of columns.")
		}
		if ui.FleetWidth = n; fit(k.Terms, c.Caller.Pane, ui.FleetPane, n) != nil {
			fmt.Fprintln(c.Out, "the open fleet keeps its width; the next one opens at this one")
		}
	case key == "here":
		ui.LastHere = c.Now
	case key == "shut":
		// A fleet of the left section is folded, or one that was is opened.
		if at := slices.Index(ui.Shut, value); at >= 0 {
			ui.Shut = slices.Delete(ui.Shut, at, at+1)
		} else {
			ui.Shut = append(ui.Shut, value)
		}
	case key == "seen":
		// The settings screen showed a section again: its warnings end.
		ui.Changed = slices.DeleteFunc(ui.Changed, func(name string) bool {
			row, _ := contract.FindKey(name)
			return row == nil || row.Section == value
		})
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
		if value, err = contract.Set(c, key, value); err == nil && (strings.HasPrefix(key, "panes.") || key == actions) {
			err = k.Panes(k, c.Caller.Pane)
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
	ui.Version = contract.FileVersion
	if err = contract.WriteVersioned(k.Platform, file, contract.FileVersion, ui); err != nil {
		return nil, err
	}
	if key != "here" && key != "seen" && key != "folded" && key != "shut" && !isDraft {
		fmt.Fprintln(c.Out, key+": "+value)
	}
	return map[string]string{"key": key, "value": value}, nil
}
