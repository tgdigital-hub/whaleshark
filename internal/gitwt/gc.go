package gitwt

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// gc removes, in every run of the project, the copies of the code of tasks
// that ended longer ago than --older-than, 7d when not given: done or
// cancelled ones, and each task of a closed run. A task with an agent at work
// is passed by, a copy with work not committed is kept, and a branch until
// its work is proven landed. With --dry-run it only says what would go. It
// is the one call of this package that records: each removal in its run.
func (t *trees) gc(c *contract.Call) (any, error) {
	k, since, dry := c.Kit, append([]string{"7d"}, c.Flags["older-than"]...), c.Flags["dry-run"] != nil
	age, err := ageOf(since[len(since)-1])
	if err != nil || age < 0 {
		return nil, &contract.Refusal{Exit: contract.ExitUsage, Code: "usage", Message: "--older-than takes an age such as 7d or 12h.", Next: []string{"whaleshark help gc"}}
	}
	runs, err := k.Store.Runs(c.Root)
	if err != nil {
		return nil, err
	}
	verb := map[bool]string{true: "would remove", false: "removed"}[dry]
	out := []string{}
	for _, run := range runs {
		s, err := k.Store.Read(c.Root, run)
		if err != nil {
			return out, err
		}
		removed := map[string]*contract.Worktree{}
		for _, id := range slices.Sorted(maps.Keys(s.Tasks)) {
			tk, closed := s.Tasks[id], s.Run.ClosedAt
			ended, w := tk.SettledAt, tk.Worktree
			if ended.IsZero() {
				ended = closed
			}
			over := tk.Status == contract.TaskDone || tk.Status == contract.TaskCancelled || !closed.IsZero()
			busy := slices.ContainsFunc(tk.Attempts, func(id string) bool { return s.Attempts[id] != nil && s.Attempts[id].State.Live() })
			if w == nil || !over || busy || ended.IsZero() || c.Now.Sub(ended) < age {
				continue
			}
			what := id + " of run " + run
			if w.RemovedAt.IsZero() {
				if clean, err := t.Clean(w.Path); err != nil || !clean {
					out = append(out, "kept: the copy of "+what+", which holds work not committed: "+w.Path)
					continue
				}
				if !dry {
					if err := t.take(c.Root, w.Path); err != nil {
						return out, err
					}
					removed[id] = new(*w)
					removed[id].RemovedAt = c.Now
				}
				out = append(out, verb+": the copy of "+what+": "+w.Path)
			}
			tip, err := git(c.Root, "rev-parse", "--verify", "-q", "refs/heads/"+w.Branch)
			switch {
			case err != nil: // gone already
			case w.KeepBranch || !landed(c.Root, s, tk, tip):
				out = append(out, "kept: the branch "+w.Branch+" of "+what+", whose work is not proven landed")
			case dry:
				out = append(out, "would delete: the branch "+w.Branch+" of "+what)
			default:
				if _, err := git(c.Root, "update-ref", "-d", "refs/heads/"+w.Branch, tip); err != nil {
					return out, err
				}
				git(c.Root, "config", "--remove-section", "branch."+w.Branch)
				out = append(out, "deleted: the branch "+w.Branch+" of "+what)
			}
		}
		if len(removed) == 0 {
			continue
		}
		if err := k.Store.Change(c.Root, run, func(s *contract.State) (err error) {
			for id, w := range removed {
				err = cmp.Or(err, k.Rules.SetWorktree(s, id, w, c.Now))
			}
			return err
		}); err != nil {
			return out, err
		}
	}
	if len(out) == 0 {
		fmt.Fprintf(c.Out, "Nothing to remove: no task that ended more than %s ago has a copy or a branch left.\n", since[len(since)-1])
	}
	for _, line := range out {
		fmt.Fprintln(c.Out, line)
	}
	return map[string]any{"lines": out, "dry_run": dry}, nil
}

// ageOf reads an age as 7d, or as Go writes a duration: 12h, 90m.
func ageOf(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		return time.Duration(n) * 24 * time.Hour, err
	}
	return time.ParseDuration(s)
}

// landed says a task's branch holds nothing that did not land: its pull
// request was merged, or its work is proven in the run's base, or in the
// collected work once that was landed. Collected work not landed is no proof.
func landed(root string, s *contract.State, tk *contract.Task, tip string) bool {
	if tk.PR != nil && tk.PR.State == contract.PRMerged {
		return true
	}
	w, at := *tk.Worktree, *s
	if in := s.Run.Integration; in != nil && in.Landed == "" {
		at.Run.Integration = nil
		if w.BaseRef == in.Branch {
			w.BaseRef = ""
		}
	}
	return merged(root, &at, &w, tip, tk.Accepted)
}
