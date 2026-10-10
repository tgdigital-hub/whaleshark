// Package integrate is the run's integration branch: where accepted work is
// collected, one commit a task, and how it reaches the main branch.
//
// It puts a contract.Integrator into the kit and binds one command, land. It
// writes nothing into a run's record but through the rules: the caller hands
// Begin's answer to Rules.Integrated and the commit of Collect to
// Rules.Checked, and land records the landed tip with Rules.Integrated.
//
// Everything is done with git's own commands, each started with a list of
// arguments. A merge is first made in git's store alone, where a clash
// touches no file; the commit that was checked is collected by its tree, so
// what is kept is exactly what the check ran on; and the branch moves only
// from the commit it was expected at, which git itself compares.
package integrate

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) {
	g := integrator{k}
	k.Integrator = g
	k.Handle("land", g.land)
}

// landed is what land answers with.
type landed struct {
	Run    string `json:"run"`
	Branch string `json:"branch"`
	Tip    string `json:"tip"`
	Onto   string `json:"onto"`
	How    string `json:"how"`
	URL    string `json:"url,omitempty"`
	Log    string `json:"log,omitempty"`
}

// land checks the collected work once more, as a whole, and hands it on: as
// one pull request, or merged into the base branch of the project's own
// checkout. The check, git and the forge are called with no lock held.
func (g integrator) land(c *contract.Call) (any, error) {
	k, pr := c.Kit, c.Flags["pr"] != nil
	switch {
	case len(c.Args) > 0 || pr == (c.Flags["local"] != nil):
		return nil, refuse(contract.ExitUsage, "usage", "land takes one of --pr and --local.", "whaleshark help land")
	case c.Run == "":
		return nil, refuse(contract.ExitMissing, "no_run", "No run is open here.", `whaleshark run new "<objective>"`)
	}
	s, err := k.Store.Read(c.Root, c.Run)
	if err == nil {
		err = k.Rules.Allowed(s, c.Caller, c.Command.Name, "")
	}
	if err != nil {
		return nil, err
	}
	project, err := contract.ReadProjectFile(c.Root)
	if err != nil {
		return nil, err
	}
	in, open, done := s.Run.Integration, []string{}, []string{}
	for _, id := range slices.Sorted(maps.Keys(s.Tasks)) {
		switch t := s.Tasks[id]; {
		case t.Status != contract.TaskDone && t.Status != contract.TaskCancelled:
			open = append(open, id)
		case t.Accepted != nil && t.Accepted.Commit != "":
			done = append(done, id)
		}
	}
	switch {
	case project.Land.Who == contract.ForHuman && c.Caller.Kind != contract.Human:
		return nil, refuse(contract.ExitRefused, "not_yours", "Only the person lands this project's work. Tell them it is ready; they type the command in a tab of their own.")
	case in == nil:
		return nil, refuse(contract.ExitRefused, "needs_git", "Landing needs git: this run collected nothing.")
	case len(open) > 0:
		return nil, refuse(contract.ExitRefused, "tasks_open", "Not every task is done or cancelled: "+strings.Join(open, ", ")+".", "whaleshark status")
	}
	tip, err := tipOf(c.Root, s)
	if err != nil {
		return nil, err
	}
	if len(done) == 0 || tip == in.Landed {
		return nil, refuse(contract.ExitRefused, "nothing_to_land", "Nothing to land: no work was collected since the last landing.")
	}
	// Where to land is what the run wrote down when it began, never a guess.
	if s.Run.Base == nil || s.Run.Base.Ref == "" {
		return nil, refuse(contract.ExitRefused, "no_base", "Run "+s.Run.ID+" began on no branch, so there is none to land on. The work is on "+in.Branch+".")
	}
	onto := s.Run.Base.Ref
	main, err := at(c.Root, onto)
	if err != nil {
		return nil, refuse(contract.ExitRefused, "no_base", "The base "+onto+" of run "+s.Run.ID+" is gone. The work is on "+in.Branch+".")
	}
	fix := func(id, title string) string {
		check := contract.CheckNone
		if project.Land.Check != "" {
			check = project.Land.Check
		}
		return k.Platform.Quote("", []string{"whaleshark", "task", "add", id, title, "--brief", "<file>", "--after", strings.Join(done, ","), "--check", check})
	}
	if !within(c.Root, main, tip) {
		behind, _ := git(c.Root, "rev-list", "--count", tip+".."+main)
		how := "a merge would be clean"
		if _, clash, _ := simulate(c.Root, tip, main); len(clash) > 0 {
			how = "a merge would clash in " + cli.Line(strings.Join(clash, ", "))
		}
		return nil, refuse(contract.ExitRefused, "base_moved", fmt.Sprintf("%s has moved: the collected work lacks %s of its commits, and %s. A worker brings it up to date.", onto, behind, how),
			fix("sync-main", "bring the collected work up to date with "+onto))
	}

	out := landed{Run: s.Run.ID, Branch: in.Branch, Tip: tip, Onto: onto, How: "local"}
	if slices.Contains(project.Held, "land.check") {
		err = k.Store.Change(c.Root, c.Run, func(s *contract.State) error {
			k.Rules.Raise(s, contract.Event{Kind: "untrusted", Text: "[land] check is not approved, so the collected work is landed without it"}, c.Now)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if project.Land.Check != "" {
		dir, err := g.scratch(c.Root, s)
		if err == nil {
			err = g.place(c.Root, s, dir, tip)
		}
		if err != nil {
			return nil, err
		}
		out.Log = filepath.Join(k.Store.Dir(c.Root, c.Run), "land.log")
		ok, tail, err := g.run(project.Land.Check, dir, out.Log, time.Duration(project.Check.TimeoutSeconds)*time.Second)
		if err != nil {
			return nil, err
		}
		if !ok {
			err = k.Store.Change(c.Root, c.Run, func(s *contract.State) error {
				k.Rules.Raise(s, contract.Event{Kind: "land_failed", Text: tail}, c.Now)
				return nil
			})
			if err != nil {
				return nil, err
			}
			r := refuse(contract.ExitFailed, "land_failed", "The check of the collected work failed: "+tail+"\nThe whole log: "+out.Log+"\nNothing has gone anywhere.",
				fix("fix-land", "make the check of the collected work pass"))
			r.Data = out
			return nil, r
		}
	}

	if pr {
		out.How = "pr"
		if out.URL, err = g.request(c.Root, s, out, done); err != nil {
			return nil, err
		}
	} else {
		on, _ := git(c.Root, "symbolic-ref", "--quiet", "--short", "HEAD")
		changed, err := git(c.Root, "status", "--porcelain", "--untracked-files=no")
		switch {
		case err != nil:
			return nil, err
		case on != onto:
			return nil, refuse(contract.ExitRefused, "not_on_base", "The project's own checkout is not on "+onto+", so nothing can be merged into it there.", "git checkout "+onto)
		case changed != "":
			return nil, refuse(contract.ExitRefused, "uncommitted", "The project's own checkout has changes that are not committed. Commit them or put them aside first.")
		}
		// The collected work holds all of the base, so this only moves it forward.
		if _, err := git(c.Root, "merge", "--quiet", "--ff-only", tip); err != nil {
			return nil, err
		}
	}
	err = k.Store.Change(c.Root, c.Run, func(s *contract.State) error {
		return k.Rules.Integrated(s, contract.Integration{Tip: tip, Landed: tip})
	})
	if err != nil {
		return nil, err
	}
	if pr {
		fmt.Fprintf(c.Out, "The collected work of %s is pushed as %s. Pull request: %s\n", s.Run.ID, in.Branch, out.URL)
	} else {
		fmt.Fprintf(c.Out, "The collected work of %s is merged into %s.\n", s.Run.ID, onto)
	}
	return out, nil
}

// request pushes the run's branch to where the base branch comes from and
// opens one pull request for it, or finds the one a landing before opened.
// No text of a person's or an agent's rides on gh's command line: the title
// is ours and the body goes in on its standard input.
func (g integrator) request(root string, s *contract.State, out landed, done []string) (string, error) {
	remote, _ := git(root, "config", "--get", "branch."+out.Onto+".remote")
	if remote == "" {
		remote = "origin"
	}
	if _, err := git(root, "push", "--quiet", remote, ref(out.Branch)+":"+ref(out.Branch)); err != nil {
		return "", err
	}
	if url, err := call(root, "", "gh", "pr", "view", out.Branch, "--json", "url", "--jq", ".url"); err == nil && url != "" {
		return url, nil
	}
	body := s.Run.Objective + "\n"
	for _, id := range done {
		body += "\n- " + id + ": " + s.Tasks[id].Title
	}
	url, err := call(root, body+"\n", "gh", "pr", "create", "--base", out.Onto, "--head", out.Branch,
		"--title", "The collected work of run "+s.Run.ID, "--body-file", "-")
	if err != nil {
		return "", fmt.Errorf("the branch %s is pushed, but no pull request was opened: %w", out.Branch, err)
	}
	return url, nil
}
