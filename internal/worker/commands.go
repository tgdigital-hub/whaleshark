// Package worker is the two commands a worker tells its work with: report
// and progress (7.3). Who is calling is the cli package's answer; what is
// checked here is the token in the attempt's own file and, for a finished
// task, its result file.
package worker

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) {
	k.Handle("report", report)
	k.Handle("progress", progress)
}

// pausedLine is what a worker reads whose agent no gate will hold.
const pausedLine = "paused: stop now and wait to be told"

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
	return filepath.Join(c.Kit.Store.Dir(c.Root, c.Run), "attempts", c.Caller.Attempt, name)
}

// tokenHash is the record's form of the token in the attempt's file, and
// nothing where there is no such file: the rules refuse that as a wrong one.
func tokenHash(c *contract.Call) (string, error) {
	data, err := c.Kit.Platform.Read(file(c, "token"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return contract.TokenHash(string(data)), err
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
		fmt.Fprintln(c.Out, pausedLine)
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
	result := file(c, "result.md")
	// #nosec G703 -- the attempt's own result file: its id passed the validator and the folder is the store's
	_, missing := os.Stat(result)
	var kept *contract.Refusal
	var already bool
	out, err := change(c, func(s *contract.State, a *contract.Attempt) error {
		var err error
		already, kept, err = c.Kit.Rules.Report(s, a.ID, hash, c.Args[0], c.Args[1], evidence, c.Now)
		if err == nil && kept == nil && !already && done && missing != nil {
			// Returned as an error, so nothing of the report is saved: the
			// worker can mend this and report again.
			return &contract.Refusal{Exit: contract.ExitFailed, Code: "no_result",
				Message: "Write the result file first, then report again: " + result}
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
		if hash == "" || hash != a.TokenHash {
			return &contract.Refusal{Exit: contract.ExitRefused, Code: "bad_token",
				Message: "The token does not match " + a.ID + ". Stop."}
		}
		return c.Kit.Rules.Progress(s, a.ID, pct, c.Args[1], c.Now)
	})
	if err != nil {
		return nil, err
	}
	return out.print(c, "")
}
