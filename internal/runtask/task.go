package runtask

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

func task(c *contract.Call) (any, error) {
	sub, args, err := form(c, map[string]int{"add": 2, "edit": 1, "cancel": 1, "reset": 1})
	if err != nil {
		return nil, err
	}
	k, f := c.Kit, c.Flags
	var data []byte
	if f["brief"] != nil {
		if data, err = brief(c); err != nil {
			return nil, err
		}
	}
	for flag, what := range map[string]string{"check": "A check", "model": "A model"} {
		if v := last(c, flag); f[flag] != nil && (strings.TrimSpace(v) == "" || cli.Plain(v) != v || strings.ContainsAny(v, "\n\t")) {
			return nil, usage(c, "%s must be one line of plain text.", what)
		}
	}
	var changed []string
	for _, flag := range []string{"brief", "check", "owns", "after"} {
		if f[flag] != nil {
			changed = append(changed, flag)
		}
	}
	switch {
	case sub == "add":
		return taskAdd(c, args[0], args[1], data)
	case sub == "edit" && changed == nil:
		return nil, usage(c, "task edit changes one or more of --brief, --check, --owns and --after.")
	}
	var t contract.Task
	var ready []string
	err = change(c, c.Run, sub, func(s *contract.State) error {
		found, err := k.Rules.FindTask(s, args[0])
		if err != nil {
			return err
		}
		switch sub {
		case "cancel":
			err = k.Rules.CancelTask(s, found.ID, c.Now)
		case "reset":
			err = k.Rules.ResetTask(s, found.ID, c.Now)
		case "edit":
			var edit contract.TaskEdit
			check, owns := last(c, "check"), contract.List(f["owns"]...)
			if f["check"] != nil {
				edit.Check = &check
			}
			if slices.Equal(owns, none) {
				owns = []string{}
			}
			if f["owns"] != nil {
				edit.Owns = &owns
			}
			if f["after"] != nil {
				after, err := ids(c, s)
				if err != nil {
					return err
				}
				edit.After = &after
			}
			if ready, err = k.Rules.EditTask(s, found.ID, edit, c.Now); err == nil && data != nil {
				err = keep(c, found, data)
			}
		}
		t = *found
		return err
	})
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(c.Out, "%s is %s.\n", t.ID, t.Status)
	if sub == "edit" {
		fmt.Fprintf(c.Out, "Changed: %s.\n", strings.Join(changed, ", "))
	}
	if len(ready) > 0 {
		fmt.Fprintf(c.Out, "Ready now: %s.\n", strings.Join(ready, ", "))
	}
	return map[string]any{"id": t.ID, "status": t.Status, "ready": ready}, nil
}

// headings are what a brief must hold, each at the start of a line.
var headings = []string{"Target", "Change", "Constraints", "Ownership", "Acceptance"}

// brief reads the file --brief names and holds it to the five headings. A
// heading may carry the marks of a title before it.
func brief(c *contract.Call) ([]byte, error) {
	data, err := os.ReadFile(last(c, "brief"))
	if err == nil {
		err = cli.Valid("text", string(data), "")
	}
	if err != nil {
		return nil, usage(c, "--brief: %v.", err)
	}
	missing := slices.Clone(headings)
	for line := range strings.Lines(string(data)) {
		line = strings.ToLower(strings.TrimLeft(line, "#* \t"))
		missing = slices.DeleteFunc(missing, func(h string) bool { return strings.HasPrefix(line, strings.ToLower(h)) })
	}
	if len(missing) > 0 {
		return nil, usage(c, "The brief lacks the heading %s. A brief has five: %s.", strings.Join(missing, ", "), strings.Join(headings, ", "))
	}
	return data, nil
}

// keep writes a brief into the run's folder, private to the login, under the
// store's lock once the rule has agreed: a refused task replaces no brief.
func keep(c *contract.Call, t *contract.Task, data []byte) error {
	if t.Brief == "" {
		t.Brief = "briefs/" + t.ID + ".md"
	}
	path := filepath.Join(c.Kit.Store.Dir(c.Root, c.Run), filepath.FromSlash(t.Brief))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// none is the word that empties a list: an empty value is no value at all.
var none = []string{"none"}

// ids turns what --after was given, ids or names, into ids.
func ids(c *contract.Call, s *contract.State) ([]string, error) {
	out, given := []string{}, contract.List(c.Flags["after"]...)
	if slices.Equal(given, none) {
		return out, nil
	}
	for _, name := range given {
		t, err := c.Kit.Rules.FindTask(s, name)
		if err != nil {
			return nil, err
		}
		out = append(out, t.ID)
	}
	return out, nil
}

func taskAdd(c *contract.Call, id, title string, data []byte) (any, error) {
	k, f := c.Kit, c.Flags
	t := contract.Task{ID: id, Title: title, Name: last(c, "name"), Check: last(c, "check"), Placement: contract.PlaceWorktree,
		Agent: last(c, "agent"), Model: last(c, "model"), BrowserCheck: f["browser-check"] != nil, Owns: contract.List(f["owns"]...)}
	err := cmp.Or(cli.Valid("id", id, ""), cli.Valid("title", title, ""))
	switch {
	case err != nil:
		return nil, usage(c, "%v.", err)
	case data == nil:
		return nil, usage(c, "A task needs its brief: --brief FILE.")
	case f["check"] == nil:
		return nil, usage(c, "A task needs a check: --check with a command, or the word none.")
	case f["shared"] != nil && f["cwd"] != nil:
		return nil, usage(c, "--shared and --cwd cannot both be given.")
	case f["cwd"] != nil:
		t.Placement = contract.PlaceCwd + last(c, "cwd")
	case f["shared"] != nil:
		t.Placement = contract.PlaceShared
	}
	// Every open tab's label is a name already taken.
	var taken []string
	if picture, err := k.Terms.Snapshot(context.Background()); err == nil {
		for _, p := range picture.Panes {
			taken = append(taken, p.Label)
		}
	} else {
		fmt.Fprintln(c.Err, "The open tabs could not be asked, so the name was compared with this run's tasks only.")
	}
	err = change(c, c.Run, "add", func(s *contract.State) error {
		if t.After, err = ids(c, s); err != nil {
			return err
		}
		if err := k.Rules.AddTask(s, t, taken, c.Now); err != nil {
			return err
		}
		added := s.Tasks[id]
		if err := cli.Valid("name", added.Name, ""); err != nil {
			return usage(c, "%v. Give a name with --name.", err)
		}
		t = *added
		return keep(c, added, data)
	})
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(c.Out, "%s %q is %s.\n", t.ID, cli.Plain(t.Name), t.Status)
	return map[string]any{"id": t.ID, "name": t.Name, "status": t.Status}, nil
}
