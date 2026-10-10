// Package team is saved teams: a set of tasks kept as one small file and
// added to a run in one step. A team holds no state and starts nothing: it
// is a template for task add. A team of the person's own is used as it
// stands; one that comes with the project only once trust approved it.
package team

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) { k.Handle("team", handle) }

// team is a team file: a line about it and its tasks, and where it was
// found. Held says that it comes with the project and is not approved.
type team struct {
	Name  string `toml:"-" json:"name"`
	From  string `toml:"-" json:"from"`
	Held  bool   `toml:"-" json:"held,omitempty"`
	About string `toml:"about" json:"about,omitempty"`
	Task  []item `toml:"task" json:"tasks"`
	path  string
}

// item is one task of a team. After names tasks by name or id. Brief is a
// file beside the team's; it and Title may hold {goal}. A task with no id
// gets the first free one when the team is run.
type item struct {
	ID    string   `toml:"id,omitempty" json:"id,omitempty"`
	Name  string   `toml:"name,omitempty" json:"name,omitempty"`
	Title string   `toml:"title" json:"title"`
	Check string   `toml:"check" json:"check"`
	Brief string   `toml:"brief,omitempty" json:"brief,omitempty"`
	After []string `toml:"after,omitempty" json:"after,omitempty"`
	Owns  []string `toml:"owns,omitempty" json:"owns,omitempty"`
	Agent string   `toml:"agent,omitempty" json:"agent,omitempty"`
	Model string   `toml:"model,omitempty" json:"model,omitempty"`
}

const yours, project, goal = "yours", "project", "{goal}"

// refuse is a refusal with the command to run next.
func refuse(exit int, code, next, format string, a ...any) *contract.Refusal {
	return &contract.Refusal{Exit: exit, Code: code, Message: fmt.Sprintf(format, a...), Next: []string{next}}
}

// put writes a file of the login's own, and its folder if there is none.
func put(path string, data []byte) error {
	return cmp.Or(os.MkdirAll(filepath.Dir(path), 0o700), os.WriteFile(path, data, 0o600))
}

// found lists every team there is, the person's own first, and names their
// folder. A project's team is a file that trust lists, found with the very
// pattern trust uses: no other spelling of a file name reaches one.
func found(c *contract.Call) (teams []team, own string, err error) {
	dirs, err := c.Kit.Platform.Dirs()
	own = filepath.Join(dirs.Config, "teams")
	settings, bad := contract.ReadProjectFile(c.Root)
	for _, dir := range []string{own, filepath.Join(c.Root, contract.TeamsDir)} {
		paths, _ := filepath.Glob(filepath.Join(dir, "*.toml"))
		slices.Sort(paths)
		for _, path := range paths {
			base := filepath.Base(path)
			t := team{Name: strings.TrimSuffix(base, ".toml"), From: yours, path: path}
			if dir != own {
				t.From, t.Held = project, bad != nil || slices.Contains(settings.Held, contract.TeamsDir+"/"+base)
			}
			teams = append(teams, t)
		}
	}
	return teams, own, err
}

// read takes the team's file in. A key it does not know is refused: a
// misspelt one would otherwise count for nothing and nobody would be told.
func (t *team) read(c *contract.Call) error {
	data, err := c.Kit.Platform.Peek(t.path)
	md, bad := toml.Decode(string(data), t)
	if err = cmp.Or(err, bad); err == nil && len(md.Undecoded()) > 0 {
		err = fmt.Errorf("it has no key %s", md.Undecoded()[0])
	}
	return err
}

func handle(c *contract.Call) (any, error) {
	const help = "whaleshark help team"
	teams, own, err := found(c)
	switch {
	case err != nil:
		return nil, err
	case len(c.Args) == 1 && c.Args[0] == "list":
		for i := range teams {
			t, state := &teams[i], map[bool]string{true: ", not approved"}[teams[i].Held]
			if err := t.read(c); err != nil {
				state = ", cannot be read: " + err.Error()
			}
			fmt.Fprintln(c.Out, cli.Plain(fmt.Sprintf("%-20s %d tasks, %s%s  %s", t.Name, len(t.Task), t.From, state, t.About)))
		}
		return teams, nil
	case len(c.Args) != 2:
		return nil, refuse(contract.ExitUsage, "usage", help, "Usage: whaleshark %s", c.Command.Usage)
	}
	sub, name := c.Args[0], strings.Join(strings.Fields(strings.ToLower(c.Args[1])), "-")
	at := slices.IndexFunc(teams, func(t team) bool { return strings.EqualFold(t.Name, name) })
	switch err := cli.Valid("slug", name, ""); {
	case !slices.Contains([]string{"save", "show", "run", "rm"}, sub):
		return nil, cli.NoSuch("form of team", sub, []string{"save", "list", "show", "run", "rm"}, help)
	case err != nil:
		return nil, refuse(contract.ExitUsage, "usage", help, "A team's name is letters, digits and dashes: %v.", err)
	case sub == "save" && at >= 0 && teams[at].From == yours:
		return nil, refuse(contract.ExitRefused, "team_exists", "whaleshark team rm "+name, "You have a team %s already.", name)
	case sub == "save":
		return save(c, own, name)
	case at < 0:
		return nil, refuse(contract.ExitMissing, "no_team", "whaleshark team list", "There is no team %s.", name)
	}
	t, where := &teams[at], contract.TeamsDir+"/"+filepath.Base(teams[at].path)
	switch {
	case sub == "rm" && t.From != yours:
		return nil, refuse(contract.ExitRefused, "project_team", "whaleshark team list", "The team %s comes with the project: it is the file %s of the repository.", name, where)
	case sub == "rm":
		err := cmp.Or(os.Remove(t.path), os.RemoveAll(filepath.Join(own, name))) // the folder holds the briefs save wrote
		if err == nil {
			fmt.Fprintf(c.Out, "The team %s is removed.\n", name)
		}
		return nil, err
	case sub == "run" && t.Held:
		return nil, refuse(contract.ExitRefused, "untrusted", "whaleshark trust --human", "The team %s comes with the project (%s) and its checks are commands. Nobody has approved the project as it stands now, so it is not used. Only the person can approve it: whaleshark trust.", name, where)
	}
	if err := t.read(c); err != nil {
		return nil, refuse(contract.ExitRefused, "bad_team", help, "The team %s cannot be read: %v.", name, err)
	}
	if sub == "run" {
		return run(c, t)
	}
	fmt.Fprintln(c.Out, cli.Plain(fmt.Sprintf("%s (%s) %s", t.Name, t.From, t.About)))
	for _, it := range t.Task {
		fmt.Fprintln(c.Out, cli.Plain(fmt.Sprintf("  %s %q: %s; check: %s; after: %s", it.ID, it.Name, it.Title, it.Check, strings.Join(it.After, ", "))))
	}
	return t, nil
}

// save writes the run's tasks that were not cancelled as a team of the
// person's own, each brief in a folder beside the file.
func save(c *contract.Call, own, name string) (any, error) {
	s, err := c.Kit.Store.Read(c.Root, c.Run)
	if err != nil {
		return nil, refuse(contract.ExitMissing, "no_run", `whaleshark run new "<objective>"`, "No run is open here to save the tasks of.")
	}
	t := team{Name: name, From: yours, About: s.Run.Objective}
	cancelled := func(id string) bool { return s.Tasks[id].Status == contract.TaskCancelled }
	for _, id := range slices.DeleteFunc(slices.Sorted(maps.Keys(s.Tasks)), cancelled) {
		task := s.Tasks[id]
		it := item{ID: id, Name: task.Name, Title: task.Title, Check: task.Check, Owns: task.Owns, Agent: task.Agent, Model: task.Model, After: slices.DeleteFunc(slices.Clone(task.After), cancelled)}
		if task.Brief != "" {
			data, err := c.Kit.Platform.Peek(filepath.Join(c.Kit.Store.Dir(c.Root, c.Run), filepath.FromSlash(task.Brief)))
			it.Brief = name + "/" + id + ".md"
			if err = cmp.Or(err, put(filepath.Join(own, name, id+".md"), data)); err != nil {
				return nil, err
			}
		}
		t.Task = append(t.Task, it)
	}
	data, err := toml.Marshal(t)
	path := filepath.Join(own, name+".toml")
	if err = cmp.Or(err, put(path+".new", data), c.Kit.Platform.Replace(path+".new", path)); err == nil {
		fmt.Fprintf(c.Out, "The team %s is saved: %d tasks.\n", name, len(t.Task))
	}
	return t, err
}

// run adds the team's tasks to the run, with the goal filled in, and starts
// nothing. One task refused and none is added.
func run(c *contract.Call, t *team) (any, error) {
	k, dir, needs := c.Kit, filepath.Dir(t.path), false
	fill := func(text string) string {
		needs = needs || strings.Contains(text, goal)
		return strings.ReplaceAll(text, goal, strings.Join(c.Flags["goal"], " "))
	}
	tasks, briefs := make([]contract.Task, len(t.Task)), make([]string, len(t.Task))
	for i, it := range t.Task {
		data, err := []byte(nil), error(nil)
		if it.Brief != "" {
			if err = cli.Valid("path", it.Brief, dir); err == nil {
				data, err = k.Platform.Peek(filepath.Join(dir, filepath.FromSlash(it.Brief)))
			}
		}
		briefs[i] = fill(string(data))
		tasks[i] = contract.Task{ID: it.ID, Name: it.Name, Title: fill(it.Title), Check: it.Check, Owns: it.Owns, Placement: contract.PlaceShared, Agent: it.Agent, Model: it.Model}
		if needs && c.Flags["goal"] == nil {
			return nil, refuse(contract.ExitUsage, "goal_needed", "whaleshark team run "+t.Name+` --goal "<text>"`, "The team %s is run with a goal.", t.Name)
		}
		if err = cmp.Or(err, cli.Valid("text", briefs[i], ""), valid(c.Root, tasks[i])); err != nil {
			return nil, refuse(contract.ExitRefused, "bad_team", "whaleshark team show "+t.Name, "The team %s cannot be used, for its task %d: %v.", t.Name, i+1, err)
		}
	}
	// Every open tab's label is a name already taken.
	var taken []string
	if picture, err := k.Terms.Snapshot(context.Background()); err == nil {
		for _, p := range picture.Panes {
			taken = append(taken, p.Label)
		}
	}
	out := make([]map[string]any, len(tasks))
	err := k.Store.Change(c.Root, c.Run, func(s *contract.State) error {
		if err := k.Rules.Allowed(s, c.Caller, c.Command.Name, "run"); err != nil {
			return err
		}
		for i := range tasks {
			task := &tasks[i]
			for n := 1; task.ID == ""; n++ {
				id := "T" + strconv.Itoa(n)
				if _, err := k.Rules.FindTask(s, id); err != nil && !slices.ContainsFunc(t.Task, func(it item) bool { return it.ID == id }) {
					task.ID = id
				}
			}
			if err := k.Rules.AddTask(s, *task, taken, c.Now); err != nil {
				return err
			}
		}
		// What a task waits for is set once all are there: any order in the file will do.
		for i, task := range tasks {
			var after []string
			for _, name := range t.Task[i].After {
				other, err := k.Rules.FindTask(s, name)
				if err != nil {
					return err
				}
				after = append(after, other.ID)
			}
			if _, err := k.Rules.EditTask(s, task.ID, contract.TaskEdit{After: &after}, c.Now); err != nil {
				return err
			}
			added := s.Tasks[task.ID]
			if briefs[i] != "" {
				added.Brief = "briefs/" + task.ID + ".md"
				if err := put(filepath.Join(k.Store.Dir(c.Root, c.Run), filepath.FromSlash(added.Brief)), []byte(briefs[i])); err != nil {
					return err
				}
			}
			out[i] = map[string]any{"id": added.ID, "name": added.Name, "status": added.Status}
			fmt.Fprintf(c.Out, "%s %q is %s.\n", added.ID, cli.Plain(added.Name), added.Status)
		}
		return nil
	})
	return map[string]any{"team": t.Name, "tasks": out}, err
}

// valid holds a task of a team file to what task add holds its flags to.
// What may be left out passes; a task with no name is named by its title.
func valid(root string, t contract.Task) error {
	err := cmp.Or(cli.Valid("title", t.Title, ""), cli.Valid("name", cmp.Or(t.Name, t.Title), ""), cli.Valid("id", cmp.Or(t.ID, "T1"), ""),
		cli.Valid("agent", cmp.Or(t.Agent, "any"), ""), cli.Valid("model", cmp.Or(t.Model, "any"), ""))
	if strings.TrimSpace(t.Check) == "" || cli.Plain(t.Check) != t.Check || strings.ContainsAny(t.Check, "\n\t") {
		err = cmp.Or(err, fmt.Errorf("a check is a command on one line, or the word %s", contract.CheckNone))
	}
	for _, pattern := range t.Owns {
		err = cmp.Or(err, cli.Valid("path", pattern, root))
	}
	return err
}
