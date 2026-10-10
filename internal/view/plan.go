package view

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const loopLine = "the tasks wait for each other in a circle: listed in the order they were added"

// graph prints the plan: every task under what it waits for, what is for the
// person on top, and what holds the next step. --ready keeps the tasks that
// can start now.
func graph(c *contract.Call) (any, error) {
	if len(c.Args) > 0 {
		return nil, usage("graph takes no argument.")
	}
	s, _, v, err := lookOr(c)
	if s == nil {
		return nil, err
	}
	pl := plan(v, s)
	if c.Flags["ready"] != nil {
		pl.Rows = slices.DeleteFunc(pl.Rows, func(r contract.PlanRow) bool { return s.Tasks[r.Card.Task].Status != contract.TaskReady })
	}
	planText(c.Out, v, pl, options(c))
	return pl, nil
}

// natural orders ids as a person counts them: T2 before T10.
func natural(x, y string) int {
	return cmp.Or(cmp.Compare(len(x), len(y)), strings.Compare(x, y))
}

// plan builds the plan from a view with every card in it and its record.
func plan(v *contract.View, s *contract.State) *contract.Plan {
	tasks := slices.SortedFunc(maps.Values(s.Tasks), func(x, y *contract.Task) int { return natural(x.ID, y.ID) })
	// A task is placed once everything it waits for is: pass after pass,
	// until a pass places nothing, which is a circle.
	pl, placed, sorted := &contract.Plan{}, map[string]bool{}, make([]*contract.Task, 0, len(tasks))
	for n := -1; len(sorted) < len(tasks) && n != len(sorted); {
		n = len(sorted)
		for _, t := range tasks {
			if !placed[t.ID] && !slices.ContainsFunc(t.After, func(id string) bool { return s.Tasks[id] != nil && !placed[id] }) {
				placed[t.ID], sorted = true, append(sorted, t)
			}
		}
	}
	if pl.Loop = len(sorted) < len(tasks); pl.Loop {
		slices.SortStableFunc(tasks, func(x, y *contract.Task) int { return x.CreatedAt.Compare(y.CreatedAt) })
		sorted = tasks
	}

	// The longest chain of tasks left that ends at each task; of two as long,
	// the one that begins higher in the one order.
	longer := func(x, y []string) bool {
		return len(x) > len(y) || len(x) == len(y) && len(x) > 0 && order[cardOf(v, x[0]).Look] < order[cardOf(v, y[0]).Look]
	}
	chains, ready, waits := map[string][]string{}, []string(nil), []string(nil)
	for _, t := range sorted {
		c := cardOf(v, t.ID)
		row := contract.PlanRow{Card: *c, After: t.After, Waits: len(unmet(s, t))}
		if c.Look == contract.LookNeedsYou && c.Try == 0 {
			row.HeldBy = c.Item
		}
		pl.Rows = append(pl.Rows, row)
		switch {
		case t.Status == contract.TaskReady && row.HeldBy == "":
			ready = append(ready, t.ID)
		case t.Status == contract.TaskPending && waits == nil:
			waits = unmet(s, t)
		}
		if pl.Loop || t.Status == contract.TaskDone || t.Status == contract.TaskCancelled {
			continue
		}
		var best []string
		for _, id := range t.After {
			if longer(chains[id], best) {
				best = chains[id]
			}
		}
		if chains[t.ID] = append(slices.Clone(best), t.ID); longer(chains[t.ID], pl.Chain) {
			pl.Chain = chains[t.ID]
		}
	}
	switch {
	case len(ready) > 0:
		pl.Holding = "Can start now: " + some(ready) + "."
	case len(waits) == 1:
		pl.Holding = "Nothing new can start until " + waits[0] + " is done."
	case len(waits) > 1:
		pl.Holding = "Nothing new can start until " + some(waits) + " are done."
	}
	return pl
}

// some names up to three ids and counts the rest.
func some(ids []string) string {
	if len(ids) > 3 {
		return strings.Join(ids[:3], ", ") + fmt.Sprint(" and ", len(ids)-3, " more")
	}
	return strings.Join(ids, ", ")
}

// planText prints a plan under the items of its view. Each row has the
// state's mark and word; a task something for the person is about shows
// that item in its place.
func planText(w io.Writer, v *contract.View, pl *contract.Plan, o Options) {
	p := newPrinter(v, o)
	var items, rows []row
	for _, it := range v.Items {
		r := p.item(it)
		if !it.Holds && it.Look == contract.LookQueued {
			r.trail = "(holds nothing yet)"
		}
		items = append(items, r)
	}
	count := map[contract.Look]int{}
	for _, pr := range pl.Rows {
		c := pr.Card
		count[c.Look]++
		r := p.card(c)
		r.name, r.age, r.text, r.trail, r.next = c.Name, "", "", "", nil
		switch {
		case pr.HeldBy != "":
			r.word = "held by " + clean(pr.HeldBy)
		case c.Item != "":
			r.word = p.mark(contract.LookNeedsYou) + " " + clean(c.Item)
		}
		if after := pr.After; len(after) > 3 {
			r.text = "after " + clean(after[0]) + " ... " + clean(after[len(after)-1])
		} else if len(after) > 0 {
			r.text = "after " + clean(strings.Join(after, ", "))
		}
		if pr.Waits > 0 && len(pr.After) > 1 {
			r.text += fmt.Sprint("  (waits on ", pr.Waits, ")")
		}
		if c.Clash != nil {
			r.trail = clash(c.Clash)
		}
		rows = append(rows, p.measure(r))
	}

	p.alerts()
	n, done, queued := len(pl.Rows), count[contract.LookDone], count[contract.LookQueued]
	parts := []string{"run " + clean(v.Run), plural(n, "task", "tasks"), fmt.Sprint(done, " done"), fmt.Sprint(n-done-queued, " open"), fmt.Sprint(queued, " queued")}
	if v.Counts.Waiting > 0 {
		parts = append(parts, plural(v.Counts.Waiting, "thing for you", "things for you"))
	}
	p.line("PLAN   ", strings.Join(parts, " · "), v.At.Format("2 Jan 15:04"))
	if pl.Loop {
		p.line("", p.paint("blocked", loopLine), "")
	}
	if len(items) > 0 {
		p.block("FOR YOU", items)
	}
	p.block("", rows)
	p.out.WriteByte('\n')
	if len(pl.Chain) > 1 {
		p.line("", "Longest chain left: "+clean(strings.Join(pl.Chain, " > "))+".", "")
	}
	if pl.Holding != "" {
		p.line("", clean(pl.Holding), "")
	}
	p.fresh(w)
}
