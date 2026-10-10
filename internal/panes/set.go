package panes

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/theme"
)

// screen reads ui.json for a command that may write it back.
func screen(k *contract.Kit) (dirs contract.Dirs, ui contract.UIFile, file string, err error) {
	if dirs, err = k.Platform.Dirs(); err == nil {
		file = filepath.Join(dirs.State, "ui.json")
		err = contract.ReadVersioned(k.Platform.Read, file, contract.FileVersion, &ui)
	}
	return dirs, ui, file, err
}

// set changes one of the person's own settings: what they chose in
// config.toml, what the screen remembers in ui.json. A pane keeps its own
// marks the same way: folded, here, and draft.<item> for a half-typed answer.
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
	cfg := person(k, dirs)
	draft, isDraft := strings.CutPrefix(key, "draft.")
	var names []string
	for _, s := range theme.Schemes {
		names = append(names, s.Name)
	}
	var onOff *bool
	switch {
	case key == "theme" && slices.Contains(names, value):
		cfg.UI.Theme = value
	case key == "theme":
		return bad("No colour scheme is called %q: there are %s.", value, strings.Join(names, ", "))
	case key == "actions" && (value == "top" || value == "bottom"):
		cfg.UI.Actions = value
	case key == "actions":
		return bad("The action pane lies at the top or at the bottom.")
	case key == "nudge.sound":
		onOff = &cfg.Nudge.Sound
	case key == "nudge.popup":
		onOff = &cfg.Nudge.Popup
	case key == "nudge.phone":
		onOff = &cfg.Nudge.Phone
	case key == "mute":
		onOff = &ui.Mute
	case key == "folded":
		onOff = &ui.Folded
	case key == "dnd" && strings.HasPrefix(value, "until "):
		at, err := time.ParseInLocation("15:04", value[len("until "):], c.Now.Location())
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
	case isDraft && value == "":
		delete(ui.Drafts, draft)
	case isDraft:
		if ui.Drafts == nil {
			ui.Drafts = map[string]string{}
		}
		ui.Drafts[draft] = value
	case slices.Contains(contract.SettingKeys, key):
		return nil, &contract.Refusal{Exit: contract.ExitFailed, Code: "not_built", Message: key + " is a setting of the engine: " + contract.ErrNotBuilt.Error() + "."}
	default:
		return bad("There is no setting %q. The settings: %s.", key, c.Command.Help[strings.Index(c.Command.Help, ":")+2:])
	}
	if onOff != nil && value != "on" && value != "off" {
		return bad("%s is on or off.", key)
	} else if onOff != nil {
		*onOff = value == "on"
	}
	if key == "theme" || key == "actions" || strings.HasPrefix(key, "nudge.") {
		var b bytes.Buffer
		if err = toml.NewEncoder(&b).Encode(cfg); err == nil {
			err = replace(k, filepath.Join(dirs.Config, "config.toml"), b.Bytes())
		}
		if key == "actions" && err == nil && ui.ActionsPane != "" {
			fmt.Fprintln(c.Out, "the action pane moves when it is next opened: whaleshark ui actions off, then on")
		}
	} else {
		ui.Version = contract.FileVersion
		err = contract.WriteVersioned(k.Platform, file, contract.FileVersion, ui)
	}
	if err != nil {
		return nil, err
	}
	if key != "here" && key != "folded" && !isDraft {
		fmt.Fprintln(c.Out, key+": "+value)
	}
	return map[string]string{"key": key, "value": value}, nil
}

// replace puts a finished file, private to the login, in the place of path.
func replace(k *contract.Kit, path string, data []byte) error {
	err := os.MkdirAll(filepath.Dir(path), 0o700)
	if err == nil {
		err = os.WriteFile(path+".new", data, 0o600)
	}
	if err == nil {
		err = k.Platform.Replace(path+".new", path)
	}
	return err
}
