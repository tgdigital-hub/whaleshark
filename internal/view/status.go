package view

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"golang.org/x/term"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/theme"
)

// snapshotWait is how long status waits for the terminals' picture before it shows
// the record without one.
const snapshotWait = 2 * time.Second

// status runs one sweep, then prints the team with what waits for the person
// on top. With --sweep --quiet it only sweeps: that is the child a pane and
// the page start every few seconds.
func status(c *contract.Call) (any, error) {
	has := func(flag string) bool { return c.Flags[flag] != nil }
	if len(c.Args) > 0 {
		return nil, usage("status takes no argument.")
	}
	quiet := has("quiet")
	none := func() (any, error) {
		if has("everywhere") && !quiet {
			in := input(c, nil, contract.Swept{}, nil)
			return everywhere(c, in, jobs(c, in, true)), nil
		}
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
	others := jobs(c, in, has("everywhere"))
	for _, j := range others {
		if j.View.Counts.Waiting > 0 {
			in.Elsewhere++
		}
	}
	v := Build(in)
	if has("everywhere") {
		return everywhere(c, in, append([]job{{c.Root, v, ""}}, others...)), nil
	}
	o := options(c)
	o.Items = has("items")
	Text(c.Out, v, o)
	return v, nil
}

// job is one open run of the login, as --everywhere lists it: its project
// folder, its view and the line that shows it in full.
type job struct {
	Root string         `json:"root"`
	View *contract.View `json:"view"`
	Next string         `json:"next"`
}

// jobs builds the view of the login's other open runs, from the list of
// folders init keeps: every run of each folder, or, for the count that every
// status prints, only the one each folder's commands address.
func jobs(c *contract.Call, in contract.ViewInput, every bool) []job {
	var out []job
	dirs, err := c.Kit.Platform.Dirs()
	if err != nil {
		return nil
	}
	key := c.Kit.Platform.PathKey
	roots, _ := contract.ReadProjects(c.Kit.Platform.Peek, dirs.State)
	for _, root := range roots {
		runs, _ := c.Kit.Reader().Runs(root)
		if current, _ := c.Kit.Reader().Current(root); !every {
			runs = []string{current}
		}
		for _, run := range runs {
			if key(root) == key(c.Root) && run == c.Run {
				continue
			}
			if s, err := c.Kit.Reader().Read(root, run); err == nil && s.Run.ClosedAt.IsZero() {
				in.State, in.Ctx, in.Notes, in.All, in.Elsewhere = s, nil, nil, false, 0
				out = append(out, job{root, c.Kit.View(in), ""})
			}
		}
	}
	return out
}

// everywhere prints every open run of the login, those that need the person
// first, each with the line that shows it.
func everywhere(c *contract.Call, in contract.ViewInput, all []job) []job {
	slices.SortStableFunc(all, func(x, y job) int { return min(y.View.Counts.Waiting, 1) - min(x.View.Counts.Waiting, 1) })
	p := newPrinter(&contract.View{At: c.Now, Fresh: contract.Fresh{Checked: in.Checked}}, options(c))
	rows, need := make([]row, len(all)), 0
	for i := range all {
		j, n := &all[i], all[i].View.Counts
		j.Next = c.Kit.Platform.Quote("", []string{"whaleshark", "status", "--root", j.Root, "--run", j.View.Run})
		look, word := contract.LookQueued, plural(n.Agents, "agent", "agents")
		if n.Agents > 0 {
			look = contract.LookBuilding
		}
		if n.Waiting > 0 {
			look, word = contract.LookNeedsYou, fmt.Sprint(n.Waiting, " waiting")
			need++
		}
		rows[i] = p.measure(row{mark: p.mark(look), id: j.View.Run, name: filepath.Base(j.Root), word: word, text: fmt.Sprintf("%q", clean(j.View.Objective)),
			next: []string{j.Next}, colour: contract.Looks[order[look]].Colour})
	}
	p.line("JOBS   ", plural(len(all), "open run", "open runs")+" · "+plural(need, "needs you", "need you"), c.Now.Format("2 Jan 15:04"))
	p.block("", rows)
	p.fresh(c.Out)
	return all
}

// options is how this command's text is printed: as wide as the terminal,
// with the marks it can show, and in the person's colours where the text
// goes to a terminal that has not asked for none.
func options(c *contract.Call) Options {
	o := Options{Width: width(), PlainMarks: contract.PlainMarks(os.Getenv)}
	if f, ok := c.Out.(*os.File); ok && term.IsTerminal(int(f.Fd())) && os.Getenv("TERM") != "dumb" {
		o.Colours = map[string]uint32{}
		if dirs, err := c.Kit.Platform.Dirs(); err == nil && os.Getenv("NO_COLOR") == "" {
			person, _ := contract.ReadPerson(c.Kit.Platform.Peek, dirs.Config)
			maps.Copy(o.Colours, theme.Get(person.UI.Theme).Colours)
		}
	}
	return o
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

// input gathers what the view is built from besides the run: the terminals'
// picture, each agent's context figure, the person's screen file and the
// project's limits. Whatever cannot be had is left out, and the view says so.
// snap is the picture a sweep has just taken; with none, one is asked for.
func input(c *contract.Call, s *contract.State, swept contract.Swept, snap *contract.Snapshot) contract.ViewInput {
	in := contract.ViewInput{Now: c.Now, Caller: c.Caller.Kind, State: s, Limits: contract.ProjectDefaults(), Swept: swept, Watched: true}
	if s != nil {
		in.Checked = s.Run.Terms.SeenAt
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
	if s != nil {
		in.Ctx = contract.ReadCtx(c.Kit.Platform.Peek, dirs.State, s)
	}
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
