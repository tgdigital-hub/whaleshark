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

func notBuilt(flag string) *contract.Refusal {
	return &contract.Refusal{Exit: contract.ExitFailed, Code: "not_built", Message: "status --" + flag + " is " + contract.ErrNotBuilt.Error() + "."}
}

// status runs one sweep, then prints the team with what waits for the person
// on top. With --sweep --quiet it only sweeps: that is the child a pane and
// the page start every few seconds.
func status(c *contract.Call) (any, error) {
	has := func(flag string) bool { return c.Flags[flag] != nil }
	switch {
	case len(c.Args) > 0:
		return nil, &contract.Refusal{Exit: contract.ExitUsage, Code: "usage", Message: "status takes no argument.", Next: []string{"whaleshark help status"}}
	case has("brief"):
		return nil, notBuilt("brief")
	case has("everywhere"):
		return nil, notBuilt("everywhere")
	}
	quiet := has("quiet")
	none := func() (any, error) {
		if !quiet {
			fmt.Fprintln(c.Out, "No run is open in this project.\n> whaleshark run new \"<objective>\"")
		}
		return nil, nil
	}
	if c.Run == "" {
		return none()
	}
	swept, err := c.Kit.Sweeper.Sweep(c.Root, c.Run, c.Now)
	if quiet {
		return swept, err
	}
	s, err := c.Kit.Reader().Read(c.Root, c.Run)
	if errors.Is(err, contract.ErrNoRun) {
		return none()
	}
	if err != nil {
		return nil, &contract.Refusal{Exit: contract.ExitEnv, Code: "state_unreadable", Message: "The record cannot be read: " + err.Error() + "."}
	}
	in := input(c, s, swept)
	in.All = has("all")
	v := Build(in)
	Text(c.Out, v, Options{Width: width(), PlainMarks: contract.PlainMarks(os.Getenv), Items: has("items")})
	return v, nil
}

// input gathers what the view is built from besides the run: herdr's
// picture, each agent's context figure, the person's screen file and the
// project's limits. Whatever cannot be had is left out, and the view says so.
func input(c *contract.Call, s *contract.State, swept contract.Swept) contract.ViewInput {
	in := contract.ViewInput{
		Now: c.Now, Caller: c.Caller.Kind, State: s, Limits: contract.ProjectDefaults(),
		Checked: s.Run.Herdr.SeenAt, Swept: swept, Watched: true,
	}
	if p, err := contract.ReadProjectFile(c.Root); err == nil {
		in.Limits = p
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapshotWait)
	defer cancel()
	if snap, err := c.Kit.Herdr.Snapshot(ctx); err == nil && snap != nil {
		in.Herdr, in.Checked = snap, c.Now
	}
	dirs, err := c.Kit.Platform.Dirs()
	if err != nil {
		return in
	}
	contract.ReadVersioned(filepath.Join(dirs.State, "ui.json"), contract.FileVersion, &in.UI)
	in.Ctx = contract.ReadCtx(dirs.State, s)
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
