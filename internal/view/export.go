package view

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// entry is one line of an exported log: who, and what they are or did.
type entry struct {
	At   time.Time `json:"at,omitzero"`
	Who  string    `json:"who"`
	What string    `json:"what"`
}

// jobLog is one job as log --export writes it, its fleets' runs with it. It
// holds summaries, questions and answers: never a brief, a message to an
// agent or a check's output.
type jobLog struct {
	Project   string    `json:"project"`
	Run       string    `json:"run"`
	Objective string    `json:"objective"`
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
	Exported  time.Time `json:"exported"`
	Who       []entry   `json:"who"`
	Happened  []entry   `json:"happened"`
	Approved  []entry   `json:"approved"`
	Relayed   int       `json:"relayed"`
	Hours     float64   `json:"agent_hours"`
	Agents    int       `json:"agents"`
	Longest   string    `json:"longest,omitempty"`
}

// did is the word of a message's kind where the kind itself reads badly.
var did = map[string]string{"brief": "started", "message": "wrote to", "done": "reported done", "failed": "reported failed", "answer": "answered"}

// logOf gathers one job's log from its record, its fleets' and their
// histories: nil for a run that cannot be read, or for a fleet's own run
// where every job is asked for.
func logOf(c *contract.Call, id string, jobs bool) *jobLog {
	r := c.Kit.Reader()
	s, err := r.Read(c.Root, id)
	if err != nil || jobs && s.Run.Parent != nil {
		return nil
	}
	l := &jobLog{Project: clean(filepath.Base(c.Root)), Run: id, Objective: clean(s.Run.Objective), From: s.Run.CreatedAt, Exported: c.Now, Who: []entry{{Who: "orchestrator", What: "your own agent"}}}
	var spent, longest time.Duration
	for _, fleet := range append([]string{""}, slices.Sorted(maps.Keys(s.Run.Fleets))...) {
		low, lead, pre := s, "orchestrator", ""
		if fleet != "" {
			if low, err = r.Read(c.Root, s.Run.Fleets[fleet].Run); err != nil {
				continue
			}
			lead, pre = clean(fleet), clean(fleet)+"/"
		}
		for _, id := range slices.Sorted(maps.Keys(low.Attempts)) {
			a, name, role := low.Attempts[id], pre+id, "worker"
			if t := low.Tasks[a.Task]; a.Leads != "" && t != nil {
				name, role = clean(t.Fleet), "fleet leader"
			}
			l.Who = append(l.Who, entry{Who: name, What: fmt.Sprintf("%s · %s · tab %q", role, clean(a.Agent.Kind), clean(a.Place.Tab))})
			one := contract.Spent(c.Now, &contract.State{Run: low.Run, Attempts: map[string]*contract.Attempt{id: a}})
			if spent, l.Agents = spent+one, l.Agents+1; one > longest {
				longest, l.Longest = one, name+", "+age(one)
			}
		}
		asked := map[time.Time]bool{} // what the lead agent put to the person
		for _, q := range low.Questions {
			asked[q.CreatedAt] = q.From == contract.FromLead
		}
		past, _ := r.History(c.Root, low.Run.ID)
		for _, m := range said(low, append(past, low.Inbox.Events...), time.Time{}) {
			task := strings.TrimSuffix(pre+clean(m.Task), pre)
			e := entry{At: m.At, Who: "whaleshark", What: strings.TrimSpace(cmp.Or(did[m.Kind], m.Kind) + " " + task)}
			mine := m.By != nil && (m.By.Caller == contract.Human || m.By.Where == contract.WhereRelayed)
			switch {
			case m.Dim:
				continue
			case mine && m.By.Where == contract.WhereRelayed:
				e.Who, l.Relayed = lead+", in your name", l.Relayed+1
			case mine:
				e.Who = "you (" + cmp.Or(m.By.Where, contract.WhereTyped) + ")"
			case m.Dir == "<":
				e.Who, e.What = task, cmp.Or(did[m.Kind], m.Kind)
			case m.Dir == ">" || asked[m.At]:
				e.Who = lead
			}
			if m.Text != "" && m.Kind != "message" && !strings.HasPrefix(m.Kind, "check") && (!mine || m.Kind == "answer") {
				e.What += fmt.Sprintf(": %q", clean(m.Text))
			}
			if m.Struck {
				e.What += " (taken back)"
			}
			if l.Happened = append(l.Happened, e); mine {
				l.Approved = append(l.Approved, e)
			}
		}
	}
	slices.SortStableFunc(l.Happened, func(x, y entry) int { return x.At.Compare(y.At) })
	slices.SortStableFunc(l.Approved, func(x, y entry) int { return x.At.Compare(y.At) })
	if l.To, l.Hours = l.From, spent.Hours(); len(l.Happened) > 0 {
		l.To = l.Happened[len(l.Happened)-1].At
	}
	return l
}

// text prints a job's log in its four parts, times in the computer's own.
func (l *jobLog) text(out *bytes.Buffer, loc *time.Location) {
	day, clock := "2 Jan 2006", "15:04"
	from, to := l.From.In(loc), l.To.In(loc)
	if from.Format(day) != to.Format(day) {
		clock = "2 Jan 15:04"
	}
	fmt.Fprintf(out, "WhaleShark log · project %s · job %s %q · %s, %s to %s\n", l.Project, l.Run, l.Objective, from.Format(day), from.Format(clock), to.Format(clock))
	fmt.Fprintf(out, "Exported %s. This file holds what your agents wrote: read it before you send it on.\n\nWHO\n", l.Exported.In(loc).Format(day+" 15:04"))
	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	for _, e := range l.Who {
		fmt.Fprintf(w, "  %s\t%s\n", e.Who, e.What)
	}
	fmt.Fprintln(w, "WHAT HAPPENED")
	for _, e := range l.Happened {
		fmt.Fprintf(w, "  %s  %s\t%s\n", e.At.In(loc).Format(clock), e.Who, e.What)
	}
	w.Flush()
	approved := make([]string, len(l.Approved))
	for i, e := range l.Approved {
		approved[i] = e.At.In(loc).Format(clock) + " " + e.What
	}
	fmt.Fprintf(out, "WHAT YOU APPROVED\n  %s        (%d: %d by you, %d passed on in your name)\n", strings.Join(approved, " · "), len(approved), len(approved)-l.Relayed, l.Relayed)
	fmt.Fprintf(out, "WHAT IT USED\n  %s agent-hours · %s · longest: %s\n", hours(time.Duration(l.Hours*float64(time.Hour))), plural(l.Agents, "agent", "agents"), l.Longest)
}

// export is log --export: the job's log, or with --all that of every job
// whose record is still there, as one plain file or with --json one
// document. The file is the argument, "-" for the screen, or a new one in
// the login's own state folder: the login's alone, and never one replaced.
func export(c *contract.Call) (any, error) {
	runs, name := []string{c.Run}, c.Run
	if c.Flags["all"] != nil {
		runs, _ = c.Kit.Reader().Runs(c.Root)
		name = "all"
	}
	var logs []*jobLog
	var body bytes.Buffer
	for _, id := range runs {
		if l := logOf(c, id, len(runs) > 1); l != nil {
			logs = append(logs, l)
			l.text(&body, c.Now.Location())
		}
	}
	if len(c.Args) > 1 || c.Flags["task"] != nil {
		return nil, usage("log --export takes one file at most, and writes the whole job: --task does not narrow it.")
	}
	if len(logs) == 0 {
		return nil, missing("no_run", "There is no job here whose log could be exported.")
	}
	path, kind, result := strings.Join(c.Args, ""), ".txt", map[string]any{"jobs": logs}
	if c.JSON {
		body.Reset()
		kind = ".json"
		json.NewEncoder(&body).Encode(result)
	}
	switch {
	case path == "-":
		_, err := c.Out.Write(body.Bytes())
		return result, err
	case path == "":
		dirs, err := c.Kit.Platform.Dirs()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(dirs.State, "export", fmt.Sprintf("%s-%s-%s%s", filepath.Base(c.Root), name, c.Now.Format("2006-01-02-150405"), kind))
		os.MkdirAll(filepath.Dir(path), 0o700)
		c.Kit.Platform.Private(filepath.Dir(path))
	}
	// A new file only: nothing is replaced, and no link at that name is followed.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		_, err = f.Write(body.Bytes())
		err = cmp.Or(err, f.Close(), c.Kit.Platform.Private(path))
	}
	if err != nil {
		return nil, &contract.Refusal{Exit: contract.ExitRefused, Code: "not_written", Message: "The log was not written, and nothing was replaced: " + clean(err.Error()) + "."}
	}
	fmt.Fprintln(c.Out, path)
	result["path"] = path
	return result, nil
}
