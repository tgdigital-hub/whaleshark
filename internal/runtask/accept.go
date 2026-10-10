package runtask

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/launch"
)

// accepted is what accept answers with.
type accepted struct {
	Task   string   `json:"task"`
	How    string   `json:"how"`
	Tail   string   `json:"tail,omitempty"`
	Log    string   `json:"log,omitempty"`
	Commit string   `json:"commit,omitempty"`
	Beside int      `json:"beside,omitempty"` // other agents at work in the folder the check ran in
	Ready  []string `json:"ready,omitempty"`
}

// accept checks tasks and keeps their work: one in its own folder, several
// together on one merged copy. A lock of accept's own is held throughout: an
// accept that died leaves its attempts marked as being checked with the lock
// free, and the next one starts again.
func accept(c *contract.Call) (any, error) {
	k, byHand := c.Kit, c.Flags["by-hand"] != nil
	switch {
	case len(c.Args) == 0 || byHand && len(c.Args) > 1:
		return nil, usage(c, "accept takes one task or several; by hand, one at a time.")
	case c.Run == "":
		return nil, noRun()
	case byHand && strings.TrimSpace(last(c, "by-hand")) == "":
		return nil, usage(c, "--by-hand needs a note: what you ran or read.")
	}
	// A button's check can take minutes: it runs in a tab of its own, which
	// is typed its line, so only ids go there.
	if !byHand && !slices.ContainsFunc(c.Args, func(arg string) bool { return cli.Valid("id", arg, "") != nil }) {
		if moved, err := launch.OwnTab(c, strings.Join(c.Args, " "), c.Args...); moved != nil || err != nil {
			return moved, err
		}
	}
	unlock, free, err := k.Platform.TryLock(filepath.Join(k.Store.Dir(c.Root, c.Run), "accept.lock"))
	if err != nil {
		return nil, err
	}
	if !free {
		return nil, refuse(contract.ExitRefused, "accepting", "Another accept is running in run "+c.Run+". One at a time.")
	}
	defer unlock()
	var out any
	if len(c.Args) > 1 {
		out, err = acceptAll(c)
	} else {
		out, err = acceptOne(c, c.Args[0], byHand)
	}
	if err == nil {
		launch.CloseOwnTab(c)
	}
	return out, err
}

// acceptAll is accept for several tasks (8a): they are merged in the order
// given in the run's own copy of the code, and on that one result run the
// run's check and each task's own where it is another command. When all
// pass, each task is collected as its own commit. When they do not merge or
// a check fails, nothing is collected and each is accepted alone, so that the
// one that breaks the rest is found.
func acceptAll(c *contract.Call) (any, error) {
	k := c.Kit
	project, err := contract.ReadProjectFile(c.Root)
	if err != nil {
		return nil, err
	}
	var ids []string
	var seen *contract.State
	lines := []string{project.Land.Check}
	err = change(c, c.Run, "", func(s *contract.State) error {
		for _, arg := range c.Args {
			t, err := k.Rules.FindTask(s, arg)
			switch {
			case err != nil:
				return err
			case slices.Contains(ids, t.ID):
				continue
			case t.Check == contract.CheckNone:
				return refuse(contract.ExitRefused, "by_hand", t.ID+" has no check, so it is not accepted together with others: accept it alone, by hand.",
					"whaleshark accept "+t.ID+` --by-hand "<what I ran or read>"`)
			case slices.Contains(project.Held, "land.check"):
				return refuse(contract.ExitRefused, "untrusted", "Several tasks are accepted together on the run's check, and nobody has approved this project's ([land] check). The person approves it with whaleshark trust, or accept one at a time.")
			case project.Land.Check == "":
				return refuse(contract.ExitRefused, "no_run_check", "Several tasks are accepted together on the run's check, and this project has none: check under [land] in whaleshark.toml. Accept one at a time.")
			}
			if err := k.Rules.Checking(s, t.ID, contract.Checked{}, c.Now); err != nil {
				return err
			}
			if ids = append(ids, t.ID); !slices.Contains(lines, t.Check) {
				lines = append(lines, t.Check)
			}
		}
		seen = s
		return nil
	})
	if err != nil {
		return nil, err
	}
	step := func(fn func(*contract.State) error) error { return k.Store.Change(c.Root, c.Run, fn) }
	// back returns to waiting every task that was not collected.
	back := func(why error) error {
		return errors.Join(why, step(func(s *contract.State) error {
			for _, id := range ids {
				k.Rules.Unchecked(s, id, c.Now)
			}
			return nil
		}))
	}
	done, why, tail := []accepted{}, "", ""
	all, err := k.Integrator.PrepareAll(c.Root, seen, ids)
	if r := new(*contract.Refusal); errors.As(err, r) && (*r).Code == "conflict" {
		why = (*r).Message
	} else if err != nil {
		return nil, back(err)
	}
	log := filepath.Join(k.Store.Dir(c.Root, c.Run), "accept.log")
	for _, line := range lines {
		if why != "" {
			break
		}
		result, err := check(c, line, all[0].Dir, log)
		if err != nil {
			return nil, back(err)
		}
		if tail = result.Tail; !result.OK {
			why = fmt.Sprintf("on the merged work the check %q failed: %s", line, tail)
		}
	}
	if why != "" {
		if err := back(nil); err != nil {
			return nil, err
		}
		fmt.Fprintf(c.Out, "Together they are not accepted: %s\nNothing was collected. One at a time now.\n", why)
		for _, id := range ids {
			one, failed := acceptOne(c, id, false)
			if failed != nil {
				fmt.Fprintf(c.Out, "%s: %s\n", id, cli.Plain(failed.Error()))
				err = cmp.Or(err, failed)
				continue
			}
			done = append(done, one)
		}
		if r := new(*contract.Refusal); errors.As(err, r) {
			err = refuse((*r).Exit, (*r).Code, fmt.Sprintf("%d of %d tasks were accepted, one at a time.", len(done), len(ids)), "whaleshark status")
		}
		return map[string]any{"accepted": done}, err
	}
	for i, id := range ids {
		if i > 0 {
			all[i].Tip = done[i-1].Commit
		}
		one := accepted{Task: id, How: contract.AcceptCheck, Tail: tail, Log: log}
		if one.Commit, err = k.Integrator.Collect(c.Root, seen, id, all[i]); err != nil {
			return nil, back(err)
		}
		err = step(func(s *contract.State) (err error) {
			one.Ready, err = k.Rules.Checked(s, id, contract.CheckResult{OK: true, At: c.Now, Tail: tail}, contract.Accepted{How: one.How, Commit: one.Commit}, c.Now)
			return err
		})
		if err != nil {
			return nil, back(err)
		}
		done = append(done, one)
		fmt.Fprintf(c.Out, "%s is done, checked.\n", id)
		if len(one.Ready) > 0 {
			fmt.Fprintf(c.Out, "Ready now: %s.\n", strings.Join(one.Ready, ", "))
		}
	}
	return map[string]any{"accepted": done}, nil
}

// acceptOne checks one task and keeps its work. The check, the merge and the
// collecting run between short locked steps, with the attempt marked as being
// checked.
func acceptOne(c *contract.Call, task string, byHand bool) (out accepted, err error) {
	k, dir := c.Kit, c.Kit.Store.Dir(c.Root, c.Run)
	var t contract.Task
	var seen *contract.State
	err = change(c, c.Run, "", func(s *contract.State) error {
		found, err := k.Rules.FindTask(s, task)
		if err == nil {
			err = k.Rules.Checking(s, found.ID, contract.Checked{}, c.Now)
		}
		if err != nil {
			return err
		}
		if none := found.Check == contract.CheckNone; none != byHand {
			r := refuse(contract.ExitRefused, "by_hand", found.ID+" has a check, so it is accepted by that check and not by hand.", "whaleshark accept "+found.ID)
			if none {
				r.Message = found.ID + " has no check, so it is accepted by hand: say what you ran or read."
				r.Next[0] += ` --by-hand "<what I ran or read>"`
			}
			return r
		}
		t, seen = *found, s
		return nil
	})
	if err != nil {
		return out, err
	}
	// The later steps belong to a command that was let in: they are not asked again.
	step := func(fn func(*contract.State) error) error { return k.Store.Change(c.Root, c.Run, fn) }
	back := func(why error) (accepted, error) {
		err := step(func(s *contract.State) error { return k.Rules.Unchecked(s, t.ID, c.Now) })
		return out, errors.Join(why, err)
	}

	checked, err := k.Integrator.Prepare(c.Root, seen, t.ID)
	if err != nil {
		return back(err)
	}
	if checked != (contract.Checked{}) {
		if err := step(func(s *contract.State) error { return k.Rules.Checking(s, t.ID, checked, c.Now) }); err != nil {
			return out, err
		}
	}
	out = accepted{Task: t.ID, How: contract.AcceptByHand}
	result := contract.CheckResult{OK: true}
	if !byHand {
		where := checked.Dir
		if where == "" {
			if where, _, err = k.Placement.Place(c.Root, seen, t.ID); err != nil {
				return back(err)
			}
		}
		if !filepath.IsAbs(where) {
			where = filepath.Join(c.Root, where)
		}
		attempt := t.Attempts[len(t.Attempts)-1]
		for id, a := range seen.Attempts {
			if id != attempt && a.State.Live() && k.Platform.PathKey(a.Place.Cwd) == k.Platform.PathKey(where) {
				out.Beside++
			}
		}
		out.How, out.Log = contract.AcceptCheck, filepath.Join(contract.AttemptDir(dir, attempt), "check.log")
		if result, err = check(c, t.Check, where, out.Log); err != nil {
			return back(err)
		}
		out.Tail = result.Tail
	}
	if result.OK {
		if out.Commit, err = k.Integrator.Collect(c.Root, seen, t.ID, checked); err != nil {
			return back(err)
		}
	}
	err = step(func(s *contract.State) (err error) {
		how := contract.Accepted{How: out.How, Note: last(c, "by-hand"), Commit: out.Commit}
		out.Ready, err = k.Rules.Checked(s, t.ID, result, how, c.Now)
		return err
	})
	if err != nil {
		return out, err
	}
	if !result.OK {
		r := refuse(contract.ExitFailed, "check_failed", fmt.Sprintf("The check of %s failed: %s\nThe whole log: %s", t.ID, out.Tail, out.Log),
			"whaleshark reject "+t.ID+` "<why>"`)
		r.Data = out
		return out, r
	}
	fmt.Fprintf(c.Out, "%s is done%s.\n", t.ID, map[bool]string{true: " (by hand)", false: ", checked"}[byHand])
	if out.Beside > 0 {
		fmt.Fprintf(c.Out, "Checked in a folder where %d other agents are working.\n", out.Beside)
	}
	if len(out.Ready) > 0 {
		fmt.Fprintf(c.Out, "Ready now: %s.\n", strings.Join(out.Ready, ", "))
	}
	return out, nil
}

// check runs a task's check in its folder with all it prints written to the
// log, and reports how it ended. An error means the check never ran.
func check(c *contract.Call, line, dir, log string) (contract.CheckResult, error) {
	project, err := contract.ReadProjectFile(c.Root)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(log), 0o700)
	}
	if err != nil {
		return contract.CheckResult{}, err
	}
	file, err := os.OpenFile(log, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return contract.CheckResult{}, err
	}
	defer file.Close()
	limit := time.Duration(project.Check.TimeoutSeconds) * time.Second
	cmd := c.Kit.Platform.Shell(line)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, file, file
	if err := cmd.Start(); err != nil {
		return contract.CheckResult{}, fmt.Errorf("the check could not be started: %w", err)
	}
	// Over its time the check is ended with everything it started.
	over := time.AfterFunc(limit, func() { c.Kit.Platform.EndTree(cmd) })
	if err = cmd.Wait(); !over.Stop() {
		fmt.Fprintf(file, "\nwhaleshark: the check was stopped after %v\n", limit)
	}
	return contract.CheckResult{OK: err == nil, At: c.Now, Tail: tail(log)}, nil
}

// maxTail is the most of a log's end that is read and kept, in bytes.
const maxTail = 400

// tail is the last line of a log that says anything.
func tail(log string) string {
	data, _ := os.ReadFile(log)
	text := strings.TrimSpace(cli.Plain(strings.ToValidUTF8(string(data[max(0, len(data)-maxTail):]), "")))
	if text == "" {
		return "it printed nothing"
	}
	return strings.TrimSpace(text[strings.LastIndexByte(text, '\n')+1:])
}
