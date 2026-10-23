package runtask

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
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

// unasked notes a name compared with the run's tasks only. With no engine
// running no tab is open, so no tab can hold the name and there is nothing to say.
func unasked(c *contract.Call, err error) {
	if !errors.Is(err, contract.ErrEngineUnreachable) {
		fmt.Fprintln(c.Err, "The open tabs could not be asked, so the name was compared with this run's tasks only.")
	}
}

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
	for flag, what := range map[string]string{"check": "A check", "model": "A model", "args": "Its arguments"} {
		if v := last(c, flag); f[flag] != nil && (strings.TrimSpace(v) == "" || cli.Plain(v) != v || strings.ContainsAny(v, "\n\t")) {
			return nil, usage(c, "%s must be one line of plain text.", what)
		}
	}
	var changed []string
	for _, flag := range []string{"name", "brief", "check", "owns", "after"} {
		if f[flag] != nil {
			changed = append(changed, flag)
		}
	}
	rename := sub == "edit" && f["name"] != nil
	switch {
	case sub == "add":
		return taskAdd(c, args[0], args[1], data)
	case sub == "edit" && changed == nil:
		return nil, usage(c, "task edit changes one or more of --name, --brief, --check, --owns and --after.")
	case rename && c.Caller.Kind == contract.Leader:
		// Its pane may rename no tab: the record and the tab would say two names.
		return nil, refuse(contract.ExitRefused, contract.RefusedLeader, "A fleet leader renames no task: the orchestrator does, or the person.")
	}
	var picture *contract.Snapshot
	if rename {
		if err = cli.Valid("name", last(c, "name"), ""); err != nil {
			return nil, usage(c, "%v.", err)
		} else if picture, err = k.Terms.Snapshot(context.Background()); err != nil {
			unasked(c, err)
		}
	}
	var t contract.Task
	var ready []string
	apply := func(s *contract.State) error {
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
			if data != nil { // the rule is told of a new brief; its text is written by keep
				edit.Brief = &found.Brief
			}
			was, name := found.Name, last(c, "name")
			tabs := ownTabs(s, found, picture)
			if rename {
				edit.Name, edit.Taken = &name, labels(picture, tabs)
			}
			if ready, err = k.Rules.EditTask(s, found.ID, edit, c.Now); err == nil && data != nil {
				found.MaxMinutes, found.Return, _ = extras(data)
				err = keep(c, found, data)
			}
			// The tabs follow under the lock: one that will not take the
			// name leaves the record as it was.
			for i := 0; err == nil && i < len(tabs) && found.Name != was; i++ {
				if err = k.Terms.TabRename(tabs[i], found.Name); errors.Is(err, contract.ErrNoPane) {
					err = nil // closed since it was seen
				}
			}
			err = inSpace(err, picture, name)
		}
		t = *found
		return err
	}
	// Dropping a fleet's last group gives the fleet up: its run is closed in
	// the same step, which refuses while an agent of the fleet lives.
	fleet := ""
	if s, err := k.Store.Read(c.Root, c.Run); sub == "cancel" && err == nil {
		if g, _ := k.Rules.FindTask(s, args[0]); g != nil && s.Run.Fleets[g.Fleet] != nil && s.Run.Fleets[g.Fleet].EndedAt.IsZero() {
			fleet = s.Run.Fleets[g.Fleet].Run
		}
	}
	if fleet == "" {
		err = change(c, c.Run, sub, apply)
	} else {
		err = k.Store.Both(c.Root, c.Run, fleet, func(up, low *contract.State) error {
			err := k.Rules.Allowed(up, c.Caller, c.Command.Name, sub)
			if err == nil {
				err = apply(up)
			}
			if r := new(*contract.Refusal); err == nil && errors.As(k.Rules.Ended(up, low, true, c.Now), r) && (*r).Code != "open_group" {
				err = *r
			}
			return err
		})
	}
	if err != nil {
		return nil, err
	}
	if low, lerr := k.Store.Read(c.Root, fleet); fleet != "" && lerr == nil && !low.Run.ClosedAt.IsZero() {
		launch.GiveUp(k, c.Root, fleet) // the fleet went with its last group: so do its tabs, copies and ports
	}
	fmt.Fprintf(c.Out, "%s is %s.\n", t.ID, t.Status)
	if sub == "edit" {
		fmt.Fprintf(c.Out, "Changed: %s.\n", strings.Join(changed, ", "))
	}
	if len(ready) > 0 {
		fmt.Fprintf(c.Out, "Ready now: %s.\n", strings.Join(ready, ", "))
	}
	return map[string]any{"id": t.ID, "name": t.Name, "status": t.Status, "ready": ready}, nil
}

// ownTabs are the tabs a task's attempts were given that still carry its
// name: what a rename renames, and whose label is not another's. A group's
// tab is its fleet's, which has no other name.
func ownTabs(s *contract.State, t *contract.Task, picture *contract.Snapshot) (tabs []string) {
	for _, id := range t.Attempts {
		place := s.Attempts[id].Place
		for _, p := range cmp.Or(picture, &contract.Snapshot{}).Panes {
			if place.OpenedByUs && p.ID == place.Pane && p.Tab == place.Tab && p.Label == t.Name && t.Fleet == "" && !slices.Contains(tabs, p.Tab) {
				tabs = append(tabs, p.Tab)
			}
		}
	}
	return tabs
}

// labels are the labels of every open tab but these.
func labels(picture *contract.Snapshot, but []string) (taken []string) {
	for _, p := range cmp.Or(picture, &contract.Snapshot{}).Panes {
		if !slices.Contains(but, p.Tab) {
			taken = append(taken, p.Label)
		}
	}
	return taken
}

// inSpace says of a name a tab has which workspace that tab is of.
func inSpace(err error, picture *contract.Snapshot, name string) error {
	var r *contract.Refusal
	if !errors.As(err, &r) || r.Code != "name_taken" || picture == nil {
		return err
	}
	name = strings.Join(strings.Fields(name), " ")
	for _, p := range picture.Panes {
		for _, sp := range picture.Spaces {
			if strings.EqualFold(p.Label, name) && sp.ID == p.Workspace {
				r.Message = fmt.Sprintf(contract.NameTaken, name, cmp.Or(sp.Name, contract.SpaceName(sp.Root)))
			}
		}
	}
	return r
}

// headings are what a brief must hold, each at the start of a line.
var headings = []string{"Target", "Change", "Constraints", "Ownership", "Acceptance"}

// brief reads the file --brief names and holds it to the five headings. A
// heading may carry the marks of a title before it. A sync brief is the
// tool's own text, which a fleet's leader hands to a worker as it stands.
func brief(c *contract.Call) ([]byte, error) {
	data, err := os.ReadFile(last(c, "brief"))
	if err == nil {
		err = cli.Valid("text", string(data), "")
	}
	if err != nil {
		return nil, usage(c, "--brief: %v.", err)
	} else if strings.HasPrefix(string(data), "----- sync brief") {
		return data, nil
	}
	missing := slices.Clone(headings)
	for line := range strings.Lines(string(data)) {
		line = strings.ToLower(strings.TrimLeft(line, "#* \t"))
		missing = slices.DeleteFunc(missing, func(h string) bool { return strings.HasPrefix(line, strings.ToLower(h)) })
	}
	if len(missing) > 0 {
		return nil, usage(c, "The brief lacks the heading %s. A brief has five: %s.", strings.Join(missing, ", "), strings.Join(headings, ", "))
	}
	if _, _, err := extras(data); err != nil {
		return nil, usage(c, "%v.", err)
	}
	return data, nil
}

// optional are the headings a brief may hold besides the five: its Time box,
// in minutes, and Return, the files and the form the worker writes.
var optional = []string{"Time box", "Return"}

// extras reads a brief's Time box and Return. A heading is a line of its
// own, as a title or with a colon and the text after it.
func extras(data []byte) (minutes int, ret string, err error) {
	at, found := "", map[string][]string{}
	for line := range strings.Lines(string(data)) {
		name, rest, _ := strings.Cut(strings.TrimLeft(line, "#* \t"), ":")
		name = strings.ToLower(strings.TrimRight(name, "*# \t\r\n"))
		if slices.ContainsFunc(append(slices.Clone(headings), optional...), func(h string) bool { return strings.ToLower(h) == name }) {
			at, line = name, strings.TrimLeft(rest, "* \t")
		}
		found[at] = append(found[at], line)
	}
	box, held := found["time box"]
	if word := strings.Fields(strings.Join(box, " ") + " none"); held {
		if minutes, err = strconv.Atoi(word[0]); err != nil || minutes < 1 || minutes > contract.WatchMinutesMost {
			return 0, "", fmt.Errorf("the brief's Time box is a number of minutes from 1 to %d, as 45", contract.WatchMinutesMost)
		}
	}
	return minutes, strings.TrimSpace(strings.Join(found["return"], "")), nil
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

// ungranted are the options a task's own arguments may not hold, each with
// what gives it instead: the lead agent writes those arguments, and what
// widens a worker's permissions or replaces its settings is the person's to
// approve. --add-dir is refused for a folder outside the project.
var ungranted = map[string]string{
	"--dangerously-skip-permissions": "agent.args", "--allow-dangerously-skip-permissions": "agent.args",
	"--permission-mode": "agent.args", "--permission-prompt-tool": "agent.args",
	"--allowedTools": "agent.args", "--allowed-tools": "agent.args", "--disallowedTools": "agent.args", "--disallowed-tools": "agent.args",
	"--dangerously-bypass-approvals-and-sandbox": "agent.args", "--yolo": "agent.args", "--full-auto": "agent.args",
	"--sandbox": "agent.args", "--ask-for-approval": "agent.args", "--approval-mode": "agent.args",
	"--settings": "agent.settings", "--setting-sources": "agent.settings", "--model": "--model", "--effort": "--effort",
}

// granted says whether a task's own arguments hold none of the ungranted.
func granted(root string, args []string) error {
	for i, arg := range args {
		name, value, joined := strings.Cut(arg, "=")
		if by := ungranted[name]; by == "agent.args" || by == "agent.settings" {
			return fmt.Errorf("%s is not for a task's arguments: it is set in whaleshark.toml as %s, which the person approves", name, by)
		} else if by != "" {
			return fmt.Errorf("%s is not for a task's arguments: give the task %s", name, by)
		}
		if name != "--add-dir" {
			continue
		}
		if !joined && i+1 < len(args) {
			value = args[i+1]
		}
		rel, err := filepath.Rel(root, filepath.Join(root, value))
		if filepath.IsAbs(value) {
			rel, err = filepath.Rel(root, value)
		}
		if value == "" || err != nil || !filepath.IsLocal(rel) && rel != "." {
			return fmt.Errorf("--add-dir is for a folder inside the project; one outside it is set in whaleshark.toml as agent.args, which the person approves")
		}
	}
	return nil
}

func taskAdd(c *contract.Call, id, title string, data []byte) (any, error) {
	k, f := c.Kit, c.Flags
	t := contract.Task{ID: id, Title: title, Name: last(c, "name"), Check: last(c, "check"), Placement: contract.PlaceWorktree, Fleet: last(c, "fleet"),
		Agent: last(c, "agent"), Model: last(c, "model"), BrowserCheck: f["browser-check"] != nil, Owns: contract.List(f["owns"]...)}
	t.Effort, t.Args = last(c, "effort"), strings.Fields(last(c, "args"))
	t.MaxMinutes, t.Return, _ = extras(data)
	err := cmp.Or(cli.Valid("id", id, ""), cli.Valid("title", title, ""), granted(c.Root, t.Args))
	kind := kinded(c, &t)
	switch {
	case err != nil:
		return nil, usage(c, "%v.", err)
	case t.Effort != "" && !slices.Contains(strings.Split(contract.EffortValues, "|"), t.Effort):
		return nil, usage(c, "--effort is one of low, medium, high and max.")
	case data == nil:
		return nil, usage(c, "A task needs its brief: --brief FILE.")
	case kind != nil:
		return nil, kind
	case f["check"] == nil && t.Code():
		return nil, usage(c, "A task needs a check: --check with a command, or the word none.")
	case joinSaved(c, &t):
	case len(f["shared"])+len(f["cwd"])+len(f["fleet"])+len(f["worktree"]) > 1:
		return nil, usage(c, "Only one of --shared, --cwd, --fleet and --worktree can be given.")
	case f["worktree"] != nil:
		t.Placement = contract.PlaceDetached
	case !t.Code() && f["cwd"] == nil:
		t.Placement = contract.PlaceShared // the project's own folder, read only
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
		unasked(c, err)
	}
	var meets []string
	var parent *contract.Twin
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
		t, meets, parent = *added, meeting(s, added), s.Run.Parent
		return keep(c, added, data)
	})
	// A name can be held by the tab of a run that is closed: removing that
	// run is what frees it.
	var r *contract.Refusal
	if errors.As(err, &r) && r.Code == "name_taken" {
		runs, _ := k.Store.Runs(c.Root)
		for _, id := range runs {
			old, err := k.Store.Read(c.Root, id)
			if err != nil || old.Run.ClosedAt.IsZero() {
				continue
			}
			for _, other := range old.Tasks {
				if strings.EqualFold(other.Name, strings.Join(strings.Fields(cmp.Or(t.Name, title)), " ")) && slices.Contains(taken, other.Name) {
					r.Message += fmt.Sprintf(" A tab of run %s, which is closed, still carries it.", id)
					r.Next = []string{"whaleshark run rm " + id}
				}
			}
		}
	}
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(c.Out, "%s %q is %s.\n", t.ID, cli.Plain(t.Name), t.Status)
	out := map[string]any{"id": t.ID, "name": t.Name, "status": t.Status}
	if len(meets) > 0 {
		out["meets"] = meets
		fmt.Fprintf(c.Out, "%s may change files that %s may change too, by their --owns. Let one wait for the other (--after), or expect them to overlap.\n", t.ID, strings.Join(meets, ", "))
	}
	if across := abroad(c, parent, t.Owns); len(across) > 0 {
		out["meets_fleets"] = across
		fmt.Fprintf(c.Out, "%s may change files that other fleets of this job may change too: %s. No fleet owns folders: expect an overlap, and a sync from the orchestrator.\n", t.ID, strings.Join(across, ", "))
	}
	return out, nil
}

// kinded checks a task's kind and what comes with it, and fills them in. A
// task that changes no code has no check, owns no files and is no group.
func kinded(c *contract.Call, t *contract.Task) error {
	f := c.Flags
	t.Kind, t.Reviews = last(c, "kind"), last(c, "reviews")
	if t.Kind == contract.WorkCode {
		t.Kind = ""
	}
	limit, err := time.ParseDuration(last(c, "limit"))
	switch {
	case t.Kind != "" && !slices.Contains(strings.Split(contract.WorkKinds, "|"), t.Kind):
		return usage(c, "--kind is one of code, readonly, review and watch.")
	case (t.Kind == contract.WorkReview) != (t.Reviews != ""):
		return usage(c, "A review names the task it is of, and only a review does: --kind review --reviews T3.")
	case (t.Kind == contract.WorkWatch) != (f["limit"] != nil):
		return usage(c, "A watch has a time box, and only a watch: --kind watch --limit 2h.")
	case f["limit"] != nil && (err != nil || limit < time.Minute):
		return usage(c, "--limit is a time of a minute or more, as 2h or 90m.")
	case f["worktree"] != nil && last(c, "worktree") != contract.PlaceDetached:
		return usage(c, "--worktree takes the word detached.")
	case t.Code() && f["worktree"] != nil:
		return usage(c, "A task that changes code has its own copy already: --worktree detached is for readonly, review and watch.")
	case t.Code():
		return nil
	case f["check"] != nil && last(c, "check") != contract.CheckNone, len(t.Owns) > 0, t.Fleet != "":
		return usage(c, "A %s task changes no code: it has no check, owns no files and is no fleet's group.", t.Kind)
	}
	t.Check, t.LimitMinutes = contract.CheckNone, int(limit/time.Minute)
	return nil
}

// abroad is meeting across fleets (20.10), for a task a leader adds: each as
// "fleet/id". A hint, never a refusal: a run that cannot be read is passed by.
func abroad(c *contract.Call, mine *contract.Twin, owns []string) (found []string) {
	if mine == nil || len(owns) == 0 {
		return nil
	}
	job, _ := c.Kit.Store.Read(c.Root, mine.Run)
	fleets := cmp.Or(job, &contract.State{}).Run.Fleets
	for _, name := range slices.Sorted(maps.Keys(fleets)) {
		f := fleets[name]
		if other, err := c.Kit.Store.Read(c.Root, f.Run); name != mine.Fleet && f.EndedAt.IsZero() && err == nil {
			for _, id := range meeting(other, &contract.Task{Owns: owns}) {
				found = append(found, name+"/"+id)
			}
		}
	}
	return found
}

// meeting names the tasks still open whose --owns meets t's. Two patterns
// meet when the fixed beginning of one, up to its first wildcard, begins the
// other's, and neither task waits for the other.
func meeting(s *contract.State, t *contract.Task) (ids []string) {
	fixed := func(p string) string {
		if i := strings.IndexAny(p, "*?["); i >= 0 {
			return p[:i]
		}
		return strings.TrimSuffix(p, "/") + "/"
	}
	for _, id := range slices.Sorted(maps.Keys(s.Tasks)) {
		o := s.Tasks[id]
		if id == t.ID || !o.SettledAt.IsZero() || slices.Contains(t.After, id) || slices.Contains(o.After, t.ID) {
			continue
		}
		if slices.ContainsFunc(t.Owns, func(p string) bool {
			return slices.ContainsFunc(o.Owns, func(q string) bool {
				p, q := fixed(p), fixed(q)
				return strings.HasPrefix(p, q) || strings.HasPrefix(q, p)
			})
		}) {
			ids = append(ids, id)
		}
	}
	return ids
}

// joinSaved gives a group the name of the saved fleet it is placed in, in
// the spelling the person saved it under. It never decides the switch it is
// a case of.
func joinSaved(c *contract.Call, t *contract.Task) bool {
	saved, _ := contract.ReadFleets(c.Kit.Platform.Peek, c.Root)
	for _, f := range saved {
		if name, _ := contract.FleetName(t.Fleet); strings.EqualFold(f.Name, name) {
			t.Fleet = f.Name
		}
	}
	return false
}
