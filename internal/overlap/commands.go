// Package overlap finds the files two tasks both change before they collide.
//
// It puts a contract.Overlap into the kit and binds two commands, overlap and
// sync. A scan touches no branch, index or file of the project and writes
// nothing into the record: its findings go to Rules.Found, which keeps them
// on the run, knows the new ones and raises their events once.
//
// The method is this project's own experiment built again: every pair of
// tasks is merged in git's store alone, all pairs in one process, with the
// objects that makes kept in a scratch folder of the login's private cache.
package overlap

import (
	"cmp"
	"context"
	"errors"
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
	o := scanner{k}
	k.Overlap = o
	k.Handle("overlap", o.overlap)
	k.Handle("sync", o.sync)
}

func refuse(exit int, code, message string, next ...string) *contract.Refusal {
	return &contract.Refusal{Exit: exit, Code: code, Message: message, Next: next}
}

// begin is the start of both commands: the run's record, for a caller who
// may run the command on it now.
func begin(c *contract.Call) (*contract.State, error) {
	if c.Run == "" {
		return nil, refuse(contract.ExitMissing, "no_run", "No run is open here.", `whaleshark run new "<objective>"`)
	}
	s, err := c.Kit.Store.Read(c.Root, c.Run)
	if err == nil {
		err = c.Kit.Rules.Allowed(s, c.Caller, c.Command.Name, "")
	}
	return s, err
}

// scanned is what overlap answers with. New counts the findings the run did
// not have before.
type scanned struct {
	Run      string             `json:"run"`
	Findings []contract.Finding `json:"findings"`
	New      int                `json:"new"`
}

// overlap scans now and shows what there is; with tasks named, what is
// theirs. Whoever a run is bound to has the findings written down as the
// run's own; a caller from outside only reads.
func (o scanner) overlap(c *contract.Call) (any, error) {
	s, err := begin(c)
	if err != nil {
		return nil, err
	}
	k, out := c.Kit, &scanned{Run: s.Run.ID, Findings: []contract.Finding{}}
	found, failed := o.Scan(context.Background(), c.Root, s, c.Args, false)
	if failed == nil && c.Caller.Kind != contract.Unbound && s.Run.ClosedAt.IsZero() {
		failed = k.Store.Change(c.Root, c.Run, func(s *contract.State) error {
			out.New = len(k.Rules.Found(s, found, c.Now))
			return nil
		})
	}
	var named, sync []string
	for _, name := range c.Args {
		if t, err := k.Rules.FindTask(s, name); err == nil {
			named = append(named, t.ID)
		}
	}
	clash := false
	for _, f := range found {
		if len(named) > 0 && !slices.ContainsFunc(f.Tasks, func(id string) bool { return slices.Contains(named, id) }) {
			continue
		}
		out.Findings = append(out.Findings, f)
		files, who := strings.Join(f.Files, ", "), strings.Join(f.Tasks, " and ")
		switch f.Kind {
		case "overlap":
			clash = true
			sync = append(sync, "whaleshark sync "+f.Tasks[len(f.Tasks)-1])
			fmt.Fprintf(c.Out, "%s would conflict (%s): %s\n", who, cli.Plain(f.How), cli.Plain(files))
		case "lands":
			sync = append(sync, "whaleshark sync "+f.Tasks[0])
			fmt.Fprintf(c.Out, "%s no longer lands on the collected work (%s): %s\n", who, cli.Plain(f.How), cli.Plain(files))
		case "same":
			fmt.Fprintf(c.Out, "%s both change %s (%s)\n", who, cli.Plain(files), cli.Plain(f.How))
		default:
			fmt.Fprintf(c.Out, "%s changed %s: %s\n", cmp.Or(who, "Somebody"), f.How, cli.Plain(files))
		}
	}
	slices.Sort(sync)
	next := slices.Compact(sync)
	r := refuse(contract.ExitNoLand, "no_land", "Work no longer lands on what is collected: bring it up to date before it is accepted.", next...)
	switch {
	case failed != nil:
		return out, failed
	case clash:
		r = refuse(contract.ExitClash, "clash", "Work of two tasks would conflict: accept the first of each pair, then bring the second up to date.", next...)
	case len(next) == 0 && len(out.Findings) == 0:
		fmt.Fprintln(c.Out, "Nothing is shared, and everything lands.")
		fallthrough
	case len(next) == 0:
		return out, nil
	}
	r.Data = out
	return nil, r
}

// synced is what sync answers with. Seq is the message's number; without
// one no agent was alive to be told, and Text is the brief.
type synced struct {
	Task    string             `json:"task"`
	Base    string             `json:"base"`
	Behind  int                `json:"behind"`
	Clashes []contract.Finding `json:"clashes,omitempty"`
	Times   int                `json:"times"`
	Brief   string             `json:"brief"`
	Seq     int                `json:"seq,omitempty"`
	Text    string             `json:"text,omitempty"`
}

// sync sends a task's worker the brief for bringing its copy up to date with
// the collected work: how far behind it is, and what would clash and how.
// The brief is kept under the run's msg folder for a later attempt's prompt;
// with no agent alive it is printed instead of sent. The worker does the
// merging, in its own folder: the tool never does.
func (o scanner) sync(c *contract.Call) (any, error) {
	if len(c.Args) != 1 {
		return nil, refuse(contract.ExitUsage, "usage", "Usage: whaleshark "+c.Command.Usage, "whaleshark help sync")
	}
	s, err := begin(c)
	if err != nil {
		return nil, err
	}
	k := c.Kit
	t, err := k.Rules.FindTask(s, c.Args[0])
	if err == nil {
		// Asked of the copy just read, so a refusal comes before any git.
		_, err = k.Rules.Synced(s, t.ID, "", c.Now)
	}
	if err != nil {
		return nil, err
	}
	g, err := open(context.Background(), k, c.Root, s)
	if err != nil {
		return nil, err
	}
	tip, head := g.tip, g.at[t.Worktree.Branch]
	if head == "" {
		return nil, fmt.Errorf("the branch %s of %s is gone", t.Worktree.Branch, t.ID)
	}
	count, err := g.git(c.Root, "", "rev-list", "--count", head+".."+tip)
	if err == nil {
		err = g.merge([][2]string{{tip, head}})
		g.save(nil)
	}
	if err != nil {
		return nil, err
	}
	out := &synced{Task: t.ID, Base: s.Run.Integration.Branch, Brief: "msg/sync-" + t.ID + ".md"}
	if out.Behind, _ = strconv.Atoi(count); out.Behind == 0 {
		return nil, refuse(contract.ExitRefused, "up_to_date", t.ID+" already holds all the collected work.")
	}
	m := g.seen.Pairs[tip+" "+head]
	for _, how := range slices.Sorted(maps.Keys(m.Clashes)) {
		out.Clashes = append(out.Clashes, contract.Finding{Kind: "lands", Tasks: []string{t.ID}, Files: m.Clashes[how], How: how})
	}
	text := k.SyncText(contract.SyncBrief{Task: t.ID, Base: out.Base, Behind: out.Behind, Clashes: out.Clashes})
	path := filepath.Join(k.Store.Dir(c.Root, c.Run), filepath.FromSlash(out.Brief))
	err = k.Store.Change(c.Root, c.Run, func(s *contract.State) error {
		err := k.Rules.Allowed(s, c.Caller, c.Command.Name, "")
		if err == nil {
			out.Times, err = k.Rules.Synced(s, t.ID, out.Brief, c.Now)
		}
		if err == nil {
			err = cmp.Or(os.MkdirAll(filepath.Dir(path), 0o700), os.WriteFile(path, []byte(text), 0o600))
		}
		if err != nil {
			return err
		}
		line, _, _ := strings.Cut(text, "\n")
		var gone *contract.Refusal
		if out.Seq, err = k.Rules.Tell(s, t.ID, line, path, c.Now); errors.As(err, &gone) && gone.Code == "no_live_attempt" {
			out.Text, err = text, nil
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if out.Text == "" {
		fmt.Fprintf(c.Out, "Sent to %s as message %d: %d commits behind %s, %d kinds of clash. The brief: %s\n", t.ID, out.Seq, out.Behind, out.Base, len(out.Clashes), path)
	} else {
		fmt.Fprintf(c.Out, "%s\nNo agent is alive in %s, so nothing was sent. The brief is kept for its next start: %s\n", strings.TrimRight(cli.Plain(text), "\n"), t.ID, path)
	}
	if out.Times > 1 {
		fmt.Fprintf(c.Out, "%s has been sent to sync %d times: accept it next, before any task that touches its files.\n", t.ID, out.Times)
	}
	return out, nil
}
