package runtask

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
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

// accept checks one task and keeps its work. The check, the merge and the
// collecting run between short locked steps, with the attempt marked as being
// checked and a lock of accept's own held: an accept that died leaves that
// mark with the lock free, and the next one starts again.
func accept(c *contract.Call) (any, error) {
	k, byHand := c.Kit, c.Flags["by-hand"] != nil
	switch {
	case len(c.Args) != 1:
		return nil, usage(c, "accept takes one task; several at once come with phase 2.")
	case c.Run == "":
		return nil, noRun()
	case byHand && strings.TrimSpace(last(c, "by-hand")) == "":
		return nil, usage(c, "--by-hand needs a note: what you ran or read.")
	}
	dir := k.Store.Dir(c.Root, c.Run)
	unlock, free, err := k.Platform.TryLock(filepath.Join(dir, "accept.lock"))
	if err != nil {
		return nil, err
	}
	if !free {
		return nil, refuse(contract.ExitRefused, "accepting", "Another accept is running in run "+c.Run+". One at a time.")
	}
	defer unlock()

	var t contract.Task
	var seen *contract.State
	err = change(c, c.Run, "", func(s *contract.State) error {
		found, err := k.Rules.FindTask(s, c.Args[0])
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
		return nil, err
	}
	// The later steps belong to a command that was let in: they are not asked again.
	step := func(fn func(*contract.State) error) error { return k.Store.Change(c.Root, c.Run, fn) }
	back := func(why error) (any, error) {
		err := step(func(s *contract.State) error { return k.Rules.Unchecked(s, t.ID, c.Now) })
		return nil, errors.Join(why, err)
	}

	checked, err := k.Integrator.Prepare(c.Root, seen, t.ID)
	if err != nil {
		return back(err)
	}
	if checked != (contract.Checked{}) {
		if err := step(func(s *contract.State) error { return k.Rules.Checking(s, t.ID, checked, c.Now) }); err != nil {
			return nil, err
		}
	}
	out := accepted{Task: t.ID, How: contract.AcceptByHand}
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
		out.How, out.Log = contract.AcceptCheck, filepath.Join(dir, "attempts", attempt, "check.log")
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
		return nil, err
	}
	if !result.OK {
		r := refuse(contract.ExitFailed, "check_failed", fmt.Sprintf("The check of %s failed: %s\nThe whole log: %s", t.ID, out.Tail, out.Log),
			"whaleshark reject "+t.ID+` "<why>"`)
		r.Data = out
		return nil, r
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

// shell is how a check's line is run: by sh where the system has one, else by
// the command processor Windows names.
func shell(line string) []string {
	if sh, err := exec.LookPath("sh"); err == nil {
		return []string{sh, "-c", line}
	}
	return []string{os.Getenv("ComSpec"), "/C", line}
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
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	argv := shell(line)
	// #nosec G204 G702 -- the check is the shell line the lead agent gave the task, which is its to give
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, file, file
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		fmt.Fprintf(file, "\nwhaleshark: the check was stopped after %v\n", limit)
	case err != nil && !errors.As(err, &exit):
		return contract.CheckResult{}, fmt.Errorf("the check could not be started: %w", err)
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
