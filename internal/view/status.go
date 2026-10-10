package view

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/term"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// snapshotWait is how long status waits for herdr's picture before it shows
// the record without one.
const snapshotWait = 2 * time.Second

func notBuilt(flag, command string) *contract.Refusal {
	return &contract.Refusal{Exit: contract.ExitFailed, Code: "not_built", Message: command + " --" + flag + " is " + contract.ErrNotBuilt.Error() + "."}
}

// status runs one sweep, then prints the team with what waits for the person
// on top. With --sweep --quiet it only sweeps: that is the child a pane and
// the page start every few seconds.
func status(c *contract.Call) (any, error) {
	has := func(flag string) bool { return c.Flags[flag] != nil }
	switch {
	case len(c.Args) > 0:
		return nil, usage("status takes no argument.")
	case has("brief"):
		return nil, notBuilt("brief", "status")
	case has("everywhere"):
		return nil, notBuilt("everywhere", "status")
	}
	quiet := has("quiet")
	none := func() (any, error) {
		if !quiet {
			fmt.Fprintln(c.Out, noRun)
		}
		return nil, nil
	}
	if c.Run == "" {
		return none()
	}
	swept, snap, err := c.Kit.Sweeper.Sweep(c.Root, c.Run, c.Now)
	if quiet {
		return swept, err
	}
	s, err := load(c)
	if errors.Is(err, contract.ErrNoRun) {
		return none()
	}
	if err != nil {
		return nil, err
	}
	in := input(c, s, swept, snap)
	in.All = has("all")
	v := Build(in)
	Text(c.Out, v, Options{Width: width(), PlainMarks: contract.PlainMarks(os.Getenv), Items: has("items")})
	return v, nil
}

const noRun = "No run is open in this project.\n> whaleshark run new \"<objective>\""

// load reads the run a command addresses: ErrNoRun when there is none.
func load(c *contract.Call) (*contract.State, error) {
	if c.Run == "" {
		return nil, contract.ErrNoRun
	}
	s, err := c.Kit.Reader().Read(c.Root, c.Run)
	if err != nil && !errors.Is(err, contract.ErrNoRun) {
		err = unreadable("record", err)
	}
	return s, err
}

func unreadable(what string, err error) *contract.Refusal {
	return &contract.Refusal{Exit: contract.ExitEnv, Code: "state_unreadable", Message: "The " + what + " cannot be read: " + err.Error() + "."}
}

// missing is how show and jump end when what was named is not there.
func missing(code, format string, args ...any) *contract.Refusal {
	return &contract.Refusal{Exit: contract.ExitMissing, Code: code, Message: fmt.Sprintf(format, args...), Next: []string{"whaleshark status"}}
}

func usage(message string) *contract.Refusal {
	return &contract.Refusal{Exit: contract.ExitUsage, Code: "usage", Message: message}
}

// input gathers what the view is built from besides the run: herdr's
// picture, each agent's context figure, the person's screen file and the
// project's limits. Whatever cannot be had is left out, and the view says so.
// snap is the picture a sweep has just taken; with none, one is asked for.
func input(c *contract.Call, s *contract.State, swept contract.Swept, snap *contract.Snapshot) contract.ViewInput {
	in := contract.ViewInput{
		Now: c.Now, Caller: c.Caller.Kind, State: s, Limits: contract.ProjectDefaults(),
		Checked: s.Run.Terms.SeenAt, Swept: swept, Watched: true,
	}
	if p, err := contract.ReadProjectFile(c.Root); err == nil {
		in.Limits = p
	}
	if snap == nil {
		ctx, cancel := context.WithTimeout(context.Background(), snapshotWait)
		defer cancel()
		snap, _ = c.Kit.Terms.Snapshot(ctx)
	}
	if snap != nil {
		in.Terms, in.Checked = snap, c.Now
	}
	dirs, err := c.Kit.Platform.Dirs()
	if err != nil {
		return in
	}
	contract.ReadVersioned(c.Kit.Platform.Peek, filepath.Join(dirs.State, contract.UIFileName), contract.FileVersion, &in.UI)
	in.Ctx = contract.ReadCtx(c.Kit.Platform.Peek, dirs.State, s)
	return in
}

// width is how many columns the text may take: COLUMNS, the terminal's own
// width, or 100 where neither says.
func width() int {
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		return n
	}
	if n, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && n > 0 {
		return n
	}
	return 100
}
