package integrate

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/lock"
)

// pr-per-task (land.mode): each accepted task is pushed as its own branch
// and pull request, watched by the sweep, and merged by itself once green.
// Nothing here moves the run's branch or the person's own checkout. Work that
// changes protected files stays on this machine until the person's yes: its
// PullRequest has Number 0, the head that waits, and nothing on the remote.

// watchEvery is how often a run's pull requests are asked after; a pull
// request that shows no check for noChecks is taken to have none.
const watchEvery, noChecks = 30 * time.Second, 2 * time.Minute

// upstream is the remote a branch comes from: its own setting, else origin.
func upstream(root, branch string) string {
	if remote, _ := git(root, "config", "--get", "branch."+branch+".remote"); remote != "" && remote != "." {
		return remote
	}
	return "origin"
}

// fetch brings one branch from where it comes from and names it there.
func fetch(root, branch string) (string, error) {
	remote := upstream(root, branch)
	far := remote + "/" + branch
	_, err := git(root, "fetch", "--quiet", "--no-tags", "--", remote, "+"+ref(branch)+":refs/remotes/"+far)
	return far, err
}

func (g integrator) Fetch(root string, s *contract.State) string {
	project, err := contract.ReadProjectFile(root)
	if err != nil || !project.Worktrees.FetchBase || s.Run.Parent != nil || s.Run.Base == nil || s.Run.Base.Ref == "" {
		return ""
	}
	far, err := fetch(root, s.Run.Base.Ref)
	if err != nil {
		return ""
	}
	return far
}

// fresh is Prepare in pr-per-task mode: the base as it stands (base, else
// as fetched where worktrees.fetch_base is on) is merged into the task's own
// folder, so that what is checked is what its pull request will hold.
func (g integrator) fresh(root string, s *contract.State, task, base string) (contract.Checked, error) {
	dir := folder(root, s, task)
	if dir == "" || s.Run.Base == nil || s.Run.Base.Ref == "" {
		return contract.Checked{}, refuse(contract.ExitRefused, "needs_worktree", task+" has no copy of the code of its own, or the run began on no branch: in pr-per-task mode a task is its own pull request.")
	}
	if err := clean(dir, task); err != nil {
		return contract.Checked{}, err
	}
	head, err := at(dir, "HEAD")
	if err != nil {
		return contract.Checked{}, err
	}
	if base == "" {
		base = cmp.Or(g.Fetch(root, s), s.Run.Base.Ref)
	}
	tip, err := at(root, base)
	c := contract.Checked{Dir: dir, OID: head}
	if err != nil || within(root, tip, head) {
		return c, err
	}
	tree, clash, err := simulate(root, head, tip)
	if err != nil {
		return contract.Checked{}, err
	}
	if len(clash) > 0 {
		return contract.Checked{}, conflict(task, base, clash)
	}
	if c.OID, err = commit(root, tree, "Merge "+s.Run.Base.Ref+" into "+task, head, tip); err == nil {
		_, err = git(dir, "merge", "--quiet", "--ff-only", c.OID)
	}
	return c, err
}

func (g integrator) Request(root string, s *contract.State, task string, c contract.Checked) (*contract.PullRequest, error) {
	t := s.Tasks[task]
	project, err := contract.ReadProjectFile(root)
	if err != nil {
		return nil, err
	}
	now := contract.Now()
	ok, why, err := g.gate(root, s, t.ID, project, "", c.OID, now)
	switch {
	case err != nil:
		return nil, err
	case why != "":
		return nil, refuse(contract.ExitRefused, contract.RefusedProtected, t.ID+" is not pushed: "+why)
	case !ok:
		return &contract.PullRequest{Head: c.OID, State: contract.PRWatching, OpenedAt: now}, nil
	}
	return g.open(root, s, t, c.OID, project)
}

// open pushes a head that may leave this machine, and opens its pull request
// or finds the one that is open.
func (g integrator) open(root string, s *contract.State, t *contract.Task, head string, project contract.ProjectFile) (*contract.PullRequest, error) {
	onto, branch := s.Run.Base.Ref, t.Worktree.Branch
	if _, err := git(root, "push", "--quiet", "--", upstream(root, onto), head+":"+ref(branch)); err != nil {
		return nil, err
	}
	pr := &contract.PullRequest{Head: head, State: contract.PRWatching, OpenedAt: contract.Now()}
	out, _ := call(root, "", "gh", "pr", "view", branch, "--json", "number,url,state", "--jq", `"\(.number) \(.url) \(.state)"`)
	if f := strings.Fields(out); len(f) == 3 && f[2] == "OPEN" {
		pr.Number, _ = strconv.Atoi(f[0])
		pr.URL = f[1]
		return pr, nil
	}
	// The worker's own words, from its attempt's folder; else ours.
	heading := strings.TrimSpace(t.Ref + " " + t.ID + ": " + t.Title) // the person's card first
	body := "The work of task " + t.ID + ": " + t.Title + "\n"
	if n := len(t.Attempts); n > 0 {
		// #nosec G304 -- a file in the run's own attempt folder
		if data, err := os.ReadFile(filepath.Join(contract.AttemptDir(g.k.Store.Dir(root, s.Run.ID), t.Attempts[n-1]), contract.PRBody)); err == nil && len(strings.TrimSpace(string(data))) > 0 {
			body = string(data)
		}
	}
	args := []string{"pr", "create", "--base", onto, "--head", branch, "--title", heading, "--body-file", "-"}
	if project.Land.DraftFirst {
		args = append(args, "--draft")
	}
	out, err := call(root, body, "gh", args...)
	if err == nil {
		pr.URL = strings.TrimSpace(out[strings.LastIndexByte(out, '\n')+1:])
		if pr.Number, err = strconv.Atoi(path.Base(pr.URL)); err != nil {
			err = fmt.Errorf("gh answered %q, which names no pull request", pr.URL)
		}
	}
	if err == nil && project.Land.DraftFirst {
		_, err = call(root, "", "gh", "pr", "ready", strconv.Itoa(pr.Number))
	}
	if err != nil {
		return nil, fmt.Errorf("the branch %s is pushed, but its pull request is not open and ready: %w", branch, err)
	}
	return pr, nil
}

func (g integrator) Watch(root, run string, now time.Time) error {
	k, dir := g.k, g.k.Store.Dir(root, run)
	project, err := contract.ReadProjectFile(root)
	if err != nil || project.Land.Mode != contract.LandPR {
		return err
	}
	stamp := filepath.Join(dir, "watch.at")
	// #nosec G304 -- the run's own stamp
	if data, err := os.ReadFile(stamp); err == nil {
		if last, err := time.Parse(time.RFC3339Nano, string(data)); err == nil && now.Sub(last) >= 0 && now.Sub(last) < watchEvery {
			return nil
		}
	}
	// accept's own lock: a task is never accepted and merged at once.
	unlock, free, err := k.Platform.TryLock(filepath.Join(dir, "accept.lock"))
	if err != nil || !free {
		return err
	}
	defer unlock()
	if err := os.WriteFile(stamp, []byte(now.Format(time.RFC3339Nano)), 0o600); err != nil {
		return err
	}
	s, err := k.Store.Read(root, run)
	if err != nil || s.Run.Paused != nil || s.Run.Base == nil {
		return err
	}
	var errs error
	for _, id := range slices.Sorted(maps.Keys(s.Tasks)) {
		t := s.Tasks[id]
		if n := len(t.Attempts); t.PR != nil && t.PR.State == contract.PRWatching && t.Status == contract.TaskReview && n > 0 && s.Attempts[t.Attempts[n-1]].State == contract.AttemptReported {
			errs = errors.Join(errs, g.watch(root, s, t, project, now))
		}
	}
	return errs
}

// watch looks once at one task's pull request and takes the one step due.
func (g integrator) watch(root string, s *contract.State, t *contract.Task, project contract.ProjectFile, now time.Time) error {
	if t.PR.Number == 0 {
		return g.release(root, s, t, project, now)
	}
	pr, n := *t.PR, strconv.Itoa(t.PR.Number)
	var checks []struct{ Name, Bucket string }
	out, err := call(root, "", "gh", "pr", "checks", n, "--json", "name,bucket")
	if json.Unmarshal([]byte(out), &checks) != nil {
		// gh tells of no checks as a failure; anything else it cannot tell is asked again.
		if err == nil || !strings.Contains(err.Error(), "no checks") || now.Sub(pr.OpenedAt) < noChecks {
			return err
		}
	}
	var failed []string
	for _, c := range checks {
		switch c.Bucket {
		case "fail", "cancel":
			failed = append(failed, c.Name)
		case "pass", "skipping":
		default:
			return nil // still running
		}
	}
	if len(failed) > 0 {
		return g.mark(root, s, t.ID, contract.PRFailed, contract.Event{Kind: "ci_failed", Task: t.ID,
			Text: "the checks of " + pr.URL + " failed: " + strings.Join(failed, ", "), Data: map[string]any{"checks": failed, "url": pr.URL}}, now)
	}
	// Green. Where the base moved under it in the task's own files, it is
	// brought up to date and watched again, or the lead agent is asked.
	onto, base := s.Run.Base.Ref, s.Run.Base.Ref
	if far, err := fetch(root, onto); err == nil {
		base = far
		if files := touched(root, pr.Head, far); len(files) > 0 {
			why := "it moved under " + pr.URL + " in " + strings.Join(files, ", ")
			if project.Land.Refresh == "auto" {
				c, err := g.fresh(root, s, t.ID, far)
				var again *contract.PullRequest
				if err == nil {
					again, err = g.Request(root, s, t.ID, c)
				}
				if err == nil {
					return g.k.Store.Change(root, s.Run.ID, func(s *contract.State) error {
						s.Tasks[t.ID].PR = again
						return nil
					})
				}
				why += "; bringing it up to date failed: " + err.Error()
			}
			return g.mark(root, s, t.ID, contract.PRAsked, contract.Event{Kind: "base_moved", Task: t.ID,
				Text: onto + ": " + why + ". Accept the task again to bring it up to date, or send it back to sync.", Data: map[string]any{"files": files, "url": pr.URL}}, now)
		}
	}
	// A review that has not said safe holds the merge; the next watch looks again.
	if contract.ReviewHold(s, t.ID) != "" {
		return nil
	}
	if ok, err := g.cleared(root, s, t, project, base, now); err != nil || !ok {
		return err
	}
	return g.merge(root, s, t, project, now)
}

// release looks once at work held back for its protected files, and pushes it
// once the person's yes covers it. Work the folder has moved on from, as when
// the task was sent back meanwhile, is not what is to go: it is told of instead.
func (g integrator) release(root string, s *contract.State, t *contract.Task, project contract.ProjectFile, now time.Time) error {
	if ok, err := g.cleared(root, s, t, project, "", now); err != nil || !ok {
		return err
	}
	if here, err := at(folder(root, s, t.ID), "HEAD"); err == nil && here != t.PR.Head {
		return g.mark(root, s, t.ID, contract.PRAsked, contract.Event{Kind: "land_failed", Task: t.ID,
			Text: "The work of " + t.ID + " is not pushed: its folder is at " + here + ", and what was checked and approved is " + t.PR.Head + ". Accept the task again."}, now)
	}
	pr, err := g.open(root, s, t, t.PR.Head, project)
	if err != nil {
		return err
	}
	return g.k.Store.Change(root, s.Run.ID, func(s *contract.State) error {
		s.Tasks[t.ID].PR = pr
		return nil
	})
}

// cleared is gate at a task being watched: one the person said no to, or that
// cannot be asked about, stops being watched and is told of, until it is
// accepted again.
func (g integrator) cleared(root string, s *contract.State, t *contract.Task, project contract.ProjectFile, base string, now time.Time) (bool, error) {
	ok, why, err := g.gate(root, s, t.ID, project, base, t.PR.Head, now)
	if err != nil || why == "" {
		return ok, err
	}
	what, data := "The work of "+t.ID+" is not pushed: ", map[string]any{}
	if t.PR.Number != 0 {
		what, data = t.PR.URL+" is not merged: ", map[string]any{"url": t.PR.URL}
	}
	return false, g.mark(root, s, t.ID, contract.PRAsked, contract.Event{Kind: "land_failed", Task: t.ID, Text: what + why, Data: data}, now)
}

// gate is the look of 20.2 at a task's head, the one a landing takes: before
// it is pushed and again before it is merged, it is held to the person's yes
// for exactly those files, from where it left the base as it stands (with no
// base named, as fetched now). While the yes lacks the person is asked, once,
// and ok stays false. The record has one item for protected files, so another
// task waits for the first one's answer, and after a yes until that one is
// merged, since a second yes would take the first one's place. Why says what
// stops it for good: a no, or nobody to ask.
func (g integrator) gate(root string, s *contract.State, task string, project contract.ProjectFile, base, head string, now time.Time) (ok bool, why string, err error) {
	k := g.k
	dirs, err := k.Platform.Dirs()
	if err != nil {
		return false, "", err
	}
	person, err := contract.ReadPerson(k.Platform.Peek, dirs.Config)
	if err != nil {
		return false, "the settings file does not read, so it cannot be told whether protected files are on (" + cli.Line(err.Error()) + "). Mend it, then accept the task again.", nil
	}
	patterns := contract.Protected(person, project)
	for i, p := range patterns {
		patterns[i] = strings.ToLower(p)
	}
	seen, files := "", []string(nil)
	if len(patterns) > 0 {
		if base == "" {
			base = s.Run.Base.Ref
			if far, err := fetch(root, base); err == nil {
				base = far
			}
		}
		from, err := at(root, base)
		if err != nil {
			from = s.Run.Base.OID
		}
		if seen, files, err = changed(root, from, head, patterns); err != nil {
			return false, "", err
		}
	}
	err = k.Store.Change(root, s.Run.ID, func(s *contract.State) error {
		var q *contract.Question
		p := s.Run.Protect
		if p != nil {
			q = s.Questions[p.Item]
		}
		waits := q != nil && (q.State == contract.QuestionOpen || q.State == contract.QuestionAnswered)
		ours := waits && q.Task == task
		switch {
		case k.Rules.Landable(s, seen, files) == nil:
			if ok = true; ours {
				k.Rules.Protect(s, task, seen, files, now) // it was put back, or is the one approved: the item closes
			}
			return nil
		case s.Run.Parent != nil:
			why = "it changes protected files (" + strings.Join(files, ", ") + "), and only a job with no parent can ask the person about them."
			return nil
		case q != nil && !waits && p.Seen == seen && q.State == contract.QuestionUsed:
			why = "the person did not approve its protected files (" + strings.Join(files, ", ") + "). Send the task back, or cancel it."
			return nil
		case q != nil && q.Task != task && (waits || q.State == contract.QuestionUsed && q.Answer == contract.AnswerApprove):
			if other := s.Tasks[q.Task]; other != nil && other.PR != nil && other.PR.State == contract.PRWatching && other.Status == contract.TaskReview {
				return nil
			}
			if waits {
				q.State = contract.QuestionClosed // of a pull request nobody watches any more
			}
		}
		k.Rules.Protect(s, task, seen, files, now)
		return nil
	})
	return ok, why, err
}

// touched is the files that both the base, since the two parted, and the
// pull request changed: none where the base did not move.
func touched(root, head, base string) []string {
	if within(root, base, head) {
		return nil
	}
	from, err := git(root, "merge-base", head, base)
	if err != nil {
		return nil
	}
	ours, _ := git(root, "diff", "--name-only", "--no-renames", from, head)
	theirs, _ := git(root, "diff", "--name-only", "--no-renames", from, base)
	var both []string
	for _, f := range strings.Split(ours, "\n") {
		if f != "" && slices.Contains(strings.Split(theirs, "\n"), f) {
			both = append(both, f)
		}
	}
	return both
}

// merge merges a green pull request under the built-in merge lock, the one
// `whaleshark lock` shows, one at a time, and marks its task done once gh
// says it is merged. A pull request whose turn it is not yet is in the
// lock's line and is looked at again at the next watch. The task is marked
// as being checked first, as accept does, so that nothing is merged that
// the record would then refuse. What is merged is the head that was checked:
// one that moved since is not merged, and gh holds GitHub to the same head.
func (g integrator) merge(root string, s *contract.State, t *contract.Task, project contract.ProjectFile, now time.Time) error {
	k, run, n := g.k, s.Run.ID, strconv.Itoa(t.PR.Number)
	holder := contract.HolderOf(contract.Orchestrator, run, t.ID)
	if _, err := lock.Take(k.Platform, root, lock.Want{Name: "merge", Holder: holder, Task: t.ID, Run: run, Note: t.PR.URL}, now); err != nil {
		if r := new(*contract.Refusal); errors.As(err, r) && (*r).Code == lock.RefusedHeld {
			return nil
		}
		return err
	}
	defer lock.Release(k.Platform, root, "merge", holder, run, false, now)
	head, err := call(root, "", "gh", "pr", "view", n, "--json", "headRefOid", "--jq", ".headRefOid")
	if err != nil {
		return err
	}
	if head != t.PR.Head {
		return g.mark(root, s, t.ID, contract.PRAsked, contract.Event{Kind: "land_failed", Task: t.ID,
			Text: t.PR.URL + " is not merged: its head is " + head + ", and what was checked is " + t.PR.Head + ". Send the task back, so that what is merged has been checked.", Data: map[string]any{"url": t.PR.URL}}, now)
	}
	if err := k.Store.Change(root, run, func(s *contract.State) error { return k.Rules.Checking(s, t.ID, contract.Checked{}, now) }); err != nil {
		return err
	}
	back := func(err error) error {
		return errors.Join(err, k.Store.Change(root, run, func(s *contract.State) error { return k.Rules.Unchecked(s, t.ID, now) }))
	}
	if _, err := call(root, "", "gh", "pr", "merge", n, "--"+project.Land.MergeMethod, "--match-head-commit", t.PR.Head); err != nil {
		return back(err)
	}
	out, err := call(root, "", "gh", "pr", "view", n, "--json", "state,mergeCommit", "--jq", `"\(.state) \(.mergeCommit.oid)"`)
	if f := strings.Fields(out); err != nil || len(f) != 2 || f[0] != "MERGED" {
		return back(errors.Join(err, fmt.Errorf("pull request %s is not merged: %s", n, out)))
	}
	sha := strings.Fields(out)[1]
	// The branch goes from the remote alone: gh's own --delete-branch would
	// delete and switch branches in a checkout here too.
	git(root, "push", "--quiet", "--delete", "--", upstream(root, s.Run.Base.Ref), ref(t.Worktree.Branch))
	err = k.Store.Change(root, run, func(s *contract.State) error {
		how, tip := contract.AcceptCheck, ""
		if t.Check == contract.CheckNone {
			how = contract.AcceptByHand
		}
		if s.Run.Integration != nil {
			tip = s.Run.Integration.Tip
		}
		_, err := k.Rules.Checked(s, t.ID, contract.CheckResult{OK: true, At: now, Tail: "merged as " + sha}, contract.Accepted{How: how, Note: t.PR.URL, Commit: sha}, now)
		if err == nil && tip != "" {
			err = k.Rules.Integrated(s, contract.Integration{Tip: tip}) // the run's branch stays where it was
		}
		if err == nil {
			s.Tasks[t.ID].PR.State = contract.PRMerged
			k.Rules.Raise(s, contract.Event{Kind: "merged", Task: t.ID, Text: t.PR.URL + " is merged as " + sha, Data: map[string]any{"sha": sha, "url": t.PR.URL}}, now)
		}
		return err
	})
	if err == nil && len(t.Attempts) > 0 { // a notes folder that cannot be written stops no merge
		contract.Mirror(project, k.Store.Dir(root, run), run, t, t.Attempts[len(t.Attempts)-1], contract.MirrorDone, now)
	}
	return err
}

// mark writes a pull request's new state with the event that says so.
func (g integrator) mark(root string, s *contract.State, task, state string, e contract.Event, now time.Time) error {
	return g.k.Store.Change(root, s.Run.ID, func(s *contract.State) error {
		if t := s.Tasks[task]; t != nil && t.PR != nil {
			t.PR.State = state
		}
		g.k.Rules.Raise(s, e, now)
		return nil
	})
}
