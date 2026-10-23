// Package worker is the two commands a worker tells its work with: report
// and progress (7.3). Who is calling is the cli package's answer; what is
// checked here is the token in the attempt's own file and, for a finished
// task, its result file. A fleet leader tells the work of a group with them.
package worker

import (
	"cmp"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) {
	k.Handle("report", report)
	k.Handle("progress", progress)
}

// said is what both commands answer.
type said struct {
	Already bool `json:"already,omitempty"`
	Paused  bool `json:"paused,omitempty"`
}

func usage(c *contract.Call) error {
	return &contract.Refusal{Exit: contract.ExitUsage, Code: "usage",
		Message: "Usage: whaleshark " + c.Command.Usage, Next: []string{"whaleshark help " + c.Command.Name}}
}

// file names one of the attempt's own files.
func file(c *contract.Call, name string) string {
	return filepath.Join(contract.AttemptDir(c.Kit.Store.Dir(c.Root, c.Run), c.Caller.Attempt), name)
}

// tokenHash is the record's form of the token in the attempt's own file. A
// leader's call is first turned to the attempt of the group it is about: the
// one it leads, or of those it leads the one --task names (19.1).
func tokenHash(c *contract.Call) (string, error) {
	if s, err := c.Kit.Store.Read(c.Root, c.Run); err == nil && s.Attempts[c.Caller.Attempt] != nil && s.Attempts[c.Caller.Attempt].Leads != "" {
		task, held := strings.Join(c.Flags["task"], ""), map[string]string{}
		for id, a := range s.Attempts {
			if a.Leads == s.Attempts[c.Caller.Attempt].Leads && a.State.Live() {
				held[a.Task] = id
			}
		}
		for only := range held {
			if task == "" && len(held) == 1 {
				task = only
			}
		}
		if held[task] == "" && (task != "" || len(held) > 1) {
			return "", &contract.Refusal{Exit: contract.ExitRefused, Code: "which_group",
				Message: "Say which of your groups this is about, with --task: " + strings.Join(slices.Sorted(maps.Keys(held)), ", ") + "."}
		}
		c.Caller.Attempt = cmp.Or(held[task], c.Caller.Attempt)
	}
	return contract.ReadToken(c.Kit.Platform.Read, c.Kit.Store.Dir(c.Root, c.Run), c.Caller.Attempt)
}

// change applies fn to the caller's attempt under the lock. It answers
// whether the run is paused and nothing but the worker itself can stop its
// agent: a pause refuses none of a worker's commands (6.4).
func change(c *contract.Call, fn func(s *contract.State, a *contract.Attempt) error) (out said, err error) {
	err = c.Kit.Store.Change(c.Root, c.Run, func(s *contract.State) error {
		if err := c.Kit.Rules.Allowed(s, c.Caller, c.Command.Name, ""); err != nil {
			return err
		}
		a := s.Attempts[c.Caller.Attempt]
		out.Paused = s.Run.Paused != nil && !a.Gated
		return fn(s, a)
	})
	return out, err
}

func (o said) print(c *contract.Call, word string) (any, error) {
	if word != "" {
		fmt.Fprintln(c.Out, word)
	}
	if o.Paused {
		fmt.Fprintln(c.Out, contract.PausedLine)
	}
	return o, nil
}

func report(c *contract.Call) (any, error) {
	if len(c.Args) != 2 || c.Args[0] != contract.ReportDone && c.Args[0] != contract.ReportFailed || cli.Valid("text", c.Args[1], "") != nil {
		return nil, usage(c)
	}
	done := c.Args[0] == contract.ReportDone
	hash, err := tokenHash(c)
	if err != nil {
		return nil, err
	}
	var evidence []contract.EvidenceFile
	if done {
		if evidence, err = c.Kit.Evidence.List(c.Root, c.Run, c.Caller.Attempt); err != nil {
			return nil, err
		}
	}
	// In a copy of the code of its own, what is not committed is neither
	// checked nor collected: asked before the change, never inside it.
	clean := true
	if s, err := c.Kit.Store.Read(c.Root, c.Run); err == nil && done {
		if a := s.Attempts[c.Caller.Attempt]; a != nil && s.Tasks[a.Task].Worktree != nil {
			if clean, err = c.Kit.Placement.Clean(s.Tasks[a.Task].Worktree.Path); err != nil {
				return nil, err
			}
		}
	}
	result := file(c, "result.md")
	// #nosec G703 -- the attempt's own result file: its id passed the validator and the folder is the store's
	_, missing := os.Stat(result)
	var kept *contract.Refusal
	var already bool
	var t contract.Task
	out, err := change(c, func(s *contract.State, a *contract.Attempt) error {
		var err error
		already, kept, err = c.Kit.Rules.Report(s, a.ID, hash, c.Args[0], c.Args[1], evidence, c.Now)
		t = *s.Tasks[a.Task]
		// Each returned as an error, so nothing of the report is saved: the
		// worker can mend this and report again.
		switch {
		case err != nil || kept != nil || already || !done:
		case missing != nil:
			err = &contract.Refusal{Exit: contract.ExitFailed, Code: "no_result",
				Message: "Write the result file first, then report again: " + result}
		case !clean:
			err = &contract.Refusal{Exit: contract.ExitFailed, Code: "uncommitted",
				Message: "Commit first, then report again: your folder holds work that is not committed."}
		}
		return err
	})
	switch {
	case err != nil:
		return nil, err
	case kept != nil:
		return nil, kept
	case already:
		out.Already = true
		return out.print(c, "already recorded")
	}
	// The notes folder outside the project is a copy: one that cannot be
	// written is said, and the report stands.
	if project, err := contract.ReadProjectFile(c.Root); err == nil {
		marker := map[bool]string{true: contract.MirrorReady}[done]
		if err := contract.Mirror(project, c.Kit.Store.Dir(c.Root, c.Run), c.Run, &t, c.Caller.Attempt, marker, c.Now); err != nil {
			fmt.Fprintf(c.Err, "The notes folder was not written: %s.\n", cli.Line(err.Error()))
		}
	}
	return out.print(c, "recorded")
}

func progress(c *contract.Call) (any, error) {
	if len(c.Args) != 2 || cli.Valid("number", c.Args[0], "") != nil || cli.Valid("text", c.Args[1], "") != nil {
		return nil, usage(c)
	}
	pct, _ := strconv.Atoi(c.Args[0])
	hash, err := tokenHash(c)
	if err != nil {
		return nil, err
	}
	out, err := change(c, func(s *contract.State, a *contract.Attempt) error {
		if err := c.Kit.Rules.Token(s, a.ID, hash); err != nil {
			return err
		}
		return c.Kit.Rules.Progress(s, a.ID, pct, c.Args[1], c.Now)
	})
	if err != nil {
		return nil, err
	}
	return out.print(c, "")
}
