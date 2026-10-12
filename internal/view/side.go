package view

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// space is a workspace while it is built: every row it could show, each
// fleet open, how many of them wait for the person and the first that does.
type space struct {
	contract.Workspace
	rows       []contract.SideRow
	need, done int // what waits for the person, and what was done today
	first      *contract.SideRow
	job        bool
}

// needs reports whether a look is one that waits for the person.
func needs(l contract.Look) bool { return l != "" && order[l] <= order[contract.LookAtPrompt] }

func byLook(x, y contract.SideRow) int { return cmp.Compare(order[x.Look], order[y.Look]) }

// Side builds the left section's model from every project of the login. It
// reads nothing and keeps nothing; a run's rows are its view's cards, so
// the bar says nothing of an agent that the other screens do not.
func Side(in contract.SideInput) *contract.Side {
	out := &contract.Side{Version: contract.SideVersion, At: in.Now, Warn: contract.Warned(in.Changed), Workspaces: []contract.Workspace{}, Rows: []contract.SideRow{},
		Fresh: contract.Fresh{Checked: in.Checked, Notes: in.Notes}}
	snap := in.Terms
	if snap == nil {
		snap, out.Fresh.Notes = &contract.Snapshot{}, append([]string{"the engine is not running"}, in.Notes...)
	}
	tabs, focus := map[string]string{}, ""
	for _, p := range snap.Panes {
		if tabs[p.ID] = p.Tab; p.Focused && !p.Dock {
			focus = p.Tab
		}
	}
	names, taken := map[string]int{}, map[string]bool{"": true}
	for _, p := range in.Projects {
		names[filepath.Base(p.Root)]++
	}
	var spaces []*space
	for _, p := range in.Projects {
		w := &space{Workspace: contract.Workspace{Root: p.Root, Name: filepath.Base(p.Root), Branch: clean(p.Branch)}, job: len(p.Runs) > 0}
		if names[w.Name] > 1 {
			w.Name = filepath.Base(filepath.Dir(p.Root)) + "/" + w.Name
		}
		w.Name = clean(w.Name)
		runs := map[string]*contract.State{}
		for _, s := range p.Runs {
			if runs[s.Run.ID] = s; s.Run.Orchestrator != nil {
				taken[tabs[s.Run.Orchestrator.Pane]] = true
			}
			for _, a := range s.Attempts {
				taken[tabs[a.Place.Pane]] = true
			}
		}
		for _, s := range p.Runs {
			if s.Run.Parent == nil || runs[s.Run.Parent.Run] == nil {
				w.run(in, s, runs, tabs)
			}
		}
		spaces = append(spaces, w)
	}
	// The tabs the person opened himself: a tab of no agent of ours, in the
	// first project its folder lies in, else among the other tabs.
	spaces = append(spaces, &space{Workspace: contract.Workspace{Name: "other tabs", Other: true}})
	for _, p := range snap.Panes {
		if p.Dock || p.Ours || taken[p.Tab] {
			continue
		}
		taken[p.Tab] = true
		w := spaces[slices.IndexFunc(spaces, func(w *space) bool {
			rel, err := filepath.Rel(w.Root, p.Cwd)
			return w.Other || err == nil && filepath.IsLocal(rel)
		})]
		w.rows = append(w.rows, contract.SideRow{Kind: contract.RowTab, Name: clean(p.Label), Root: w.Root, Tab: p.Tab})
	}
	var front *space
	spaces = slices.DeleteFunc(spaces, func(w *space) bool { return !w.job && len(w.rows) == 0 })
	for _, w := range spaces {
		w.sum(focus)
		if out.NeedYou += w.need; out.First == nil {
			out.First = w.first
		}
		if front == nil || w.Front {
			front = w
		}
	}
	if front == nil {
		return out
	}
	front.Front = true
	for _, w := range spaces {
		out.Workspaces = append(out.Workspaces, w.Workspace)
	}
	if out.Title, out.Done = front.Name, front.done; !front.job && !front.Other {
		out.Hint = "no job yet · start your agent here: " + contract.DefaultTool
		// An agent that sits in the tab in front is started already.
		for _, p := range cmp.Or(in.Terms, &contract.Snapshot{}).Panes {
			if p.Focused && p.Agent != "" {
				out.Hint = "no job yet · tell your agent what you want done"
			}
		}
	}
	// What was typed narrows the rows, and finds one in a folded fleet too;
	// with nothing typed a folded fleet keeps only what waits for the person.
	rows, open := out.Rows, true
	for _, r := range front.rows {
		if r.Kind == contract.RowFleet {
			open = r.Open
		}
		if strings.Contains(strings.ToLower(r.Name), strings.ToLower(in.Find)) && (open || r.Depth == 0 || in.Find != "" || needs(r.Look)) {
			rows = append(rows, r)
		}
	}
	if out.Rows = rows; in.Rows > 0 && len(rows) > in.Rows {
		out.Rows = fit(rows, in.Rows)
		if out.Count = plural(front.Agents, "agent", "agents"); front.need > 0 {
			out.Count += " · " + plural(front.need, "needs you", "need you")
		}
	}
	return out
}

// run adds a run's rows: its lead agent, then its agents in the one order;
// for an orchestrator's run its fleets, the one that needs most first, each
// with its workers under it.
func (w *space) run(in contract.SideInput, s *contract.State, runs map[string]*contract.State, tabs map[string]string) {
	vin := contract.ViewInput{Now: in.Now, Caller: contract.Human, State: s, Terms: in.Terms, Person: in.Person, Swept: contract.Swept{Ran: true}, Watched: true}
	name := "orchestrator" // with Fleet Mode on or off, as its tab
	if s.Run.Mode == contract.ModeFleets {
		vin.Fleets = map[string]*contract.State{}
		for fleet, f := range s.Run.Fleets {
			if low := runs[f.Run]; low != nil {
				vin.Fleets[fleet] = low
			}
		}
	}
	if b := s.Run.Orchestrator; b != nil {
		// A name its tab took from the chat, or was given, is its row's too.
		for _, p := range cmp.Or(in.Terms, &contract.Snapshot{}).Panes {
			if p.ID == b.Pane && p.Label != "" && !strings.EqualFold(p.Label, contract.MainTab) {
				name = clean(p.Label)
			}
		}
		w.rows = append(w.rows, contract.SideRow{Kind: contract.RowLead, Name: name, Root: w.Root, Run: s.Run.ID, Tab: tabs[b.Pane]})
	}
	v := Build(vin)
	var fleets [][]contract.SideRow
	for _, r := range w.agents(v, 0) {
		c := cardOf(v, r.Task)
		if c.Fleet == nil {
			w.rows = append(w.rows, r)
			continue
		}
		fleet := s.Tasks[r.Task].Fleet
		r.Kind, r.Tool, r.Agents, r.Limit, r.Parked, r.Run, r.Item = contract.RowFleet, clean(c.Agent), c.Fleet.Agents, c.Fleet.Limit, c.Fleet.Parked, c.Fleet.Run, ""
		if r.Open = !slices.Contains(in.Folded, s.Run.ID+"/"+fleet); !r.Parked {
			r.Actions = []string{"park", "note-leader"}
		}
		block := []contract.SideRow{r}
		if low := vin.Fleets[fleet]; low != nil {
			lin := vin
			lin.State, lin.Fleets = low, nil
			under := w.agents(Build(lin), 1)
			// A worker whose question climbed to the person waits for him.
			for _, q := range s.Questions {
				if q.Down == nil || q.Down.Run != r.Run || q.State != contract.QuestionOpen || q.For != contract.ForHuman || low.Questions[q.Down.ID] == nil {
					continue
				}
				for i := range under {
					if u := &under[i]; u.Task == low.Questions[q.Down.ID].Task && !needs(u.Look) {
						u.Look, u.Word, u.Actions, u.Item, u.ItemRun = contract.LookNeedsYou, "", append([]string{"answer"}, u.Actions...), q.ID, s.Run.ID
					}
				}
			}
			slices.SortStableFunc(under, byLook)
			if block = append(block, under...); len(under) > 0 && byLook(under[0], r) < 0 {
				block[0].Look = under[0].Look
			}
		}
		fleets = append(fleets, block)
	}
	slices.SortStableFunc(fleets, func(x, y []contract.SideRow) int { return byLook(x[0], y[0]) })
	w.rows = append(w.rows, slices.Concat(fleets...)...)
	// A fleet the person keeps has its row with no work too.
	for _, p := range in.Projects {
		for _, f := range p.Saved {
			if live := s.Run.Fleets[f.Name]; p.Root == w.Root && s.Run.Mode == contract.ModeFleets && (live == nil || !live.EndedAt.IsZero()) {
				w.rows = append(w.rows, contract.SideRow{Kind: contract.RowFleet, Name: clean(f.Name), Word: contract.WordSaved, Root: w.Root, Run: s.Run.ID})
			}
		}
	}
}

// agents is a view's cards as rows, in the view's order.
func (w *space) agents(v *contract.View, depth int) (rows []contract.SideRow) {
	for _, sec := range v.Sections {
		for _, c := range sec.Cards {
			rows = append(rows, contract.SideRow{Kind: contract.RowAgent, Depth: depth, Look: c.Look, Word: clean(c.Word), Name: clean(c.Name), Root: w.Root, Run: v.Run,
				Task: c.Task, Tab: c.Tab, Item: c.Item, Unseen: c.Unseen, Actions: slices.DeleteFunc(slices.Clone(c.Actions), func(a string) bool { return a == "go" })})
			if needs(c.Look) {
				w.need++
			}
		}
	}
	if depth > 0 || v.Mode == "" {
		w.done += v.Counts.DoneToday
	}
	return rows
}

// sum fills in what a workspace's rows add up to: the row in front, the
// agents, the worst look, and the first row that waits for the person,
// which under a fleet that waits is the worker it waits for.
func (w *space) sum(focus string) {
	for i := range w.rows {
		r := &w.rows[i]
		r.Front = r.Tab != "" && r.Tab == focus
		w.Front = w.Front || r.Front
		if r.Kind == contract.RowLead || r.Tab != "" {
			w.Agents++
		}
		if r.Look != "" && (w.Look == "" || order[r.Look] < order[w.Look]) {
			w.Look = r.Look
		}
		if needs(r.Look) && r.Kind != contract.RowTab && (w.first == nil || r.Depth == 1 && w.first == &w.rows[i-1]) {
			w.first = r
		}
	}
}

// fit lets rows give way where there are more than the panel has. The lead
// agent, a fleet's line, a tab, what waits for the person and the row in
// front always show. A group, which is a fleet's workers or the agents of a
// run without fleets, then shows as many more as fit, in order, and one
// line for the rest; where not even that line fits for every group, the
// fleets that are not in front fold to their own line.
func fit(rows []contract.SideRow, limit int) (out []contract.SideRow) {
	must := func(r contract.SideRow) bool { return r.Kind != contract.RowAgent || needs(r.Look) || r.Front }
	// Each row's group, named by its first row, and how many rows of a
	// group may still give way. Such a group keeps one line for its rest.
	gid, spare := make([]int, len(rows)), map[int]int{}
	room, front, g := limit, -1, -1
	for i, r := range rows {
		if gid[i] = -1; must(r) {
			room--
		}
		if r.Kind != contract.RowAgent {
			g = -1
			continue
		}
		if g < 0 {
			g = i
		}
		if gid[i] = g; !must(r) {
			spare[g]++
		}
		if r.Front || r.Depth == 0 || g > 0 && rows[g-1].Front {
			front = g
		}
	}
	fold := room < len(spare)
	if room -= len(spare); fold {
		room += len(spare) - 1
	}
	hidden, kept, top := 0, false, 0
	for i, r := range rows {
		g := gid[i]
		switch {
		case must(r):
			if r.Kind != contract.RowAgent {
				top = len(out)
			}
			out = append(out, r)
		case hidden == 0 && (!fold || g == front) && (room > 0 || spare[g] == 1 && room == 0):
			room--
			out = append(out, r)
		default:
			hidden++
		}
		if !must(r) {
			spare[g]--
			kept = true
		}
		if g < 0 || i+1 < len(rows) && gid[i+1] == g {
			continue
		}
		switch { // the group ends here
		case hidden > 0 && fold && g != front:
			out[top].Open = false
		case hidden > 0:
			out = append(out, contract.SideRow{Kind: contract.RowMore, Depth: r.Depth, Name: fmt.Sprint(hidden, " more"), More: hidden, Root: r.Root, Run: r.Run})
		case kept:
			room++
		}
		hidden, kept = 0, false
	}
	return out
}

// Steps is which of the five first steps are ticked, a bit each from the
// first: those ui.json holds, and those the left section's input shows to
// have happened (an agent runs in a tab; a job exists; a worker has its row;
// the person answered, or a job is ten minutes old). The fifth is the
// pane's to tick. show is false once they were hidden or all are ticked.
func Steps(in contract.SideInput, ui contract.UIFile) (ticked int, show bool) {
	ticked = ui.Steps
	for _, p := range cmp.Or(in.Terms, &contract.Snapshot{}).Panes {
		if p.Agent != "" {
			ticked |= 1
		}
	}
	for _, p := range in.Projects {
		for _, s := range p.Runs {
			if ticked |= 3; len(s.Attempts) > 0 {
				ticked |= 4
			}
			if in.Now.Sub(s.Run.CreatedAt) >= contract.AwayAfter {
				ticked |= 8
			}
			for _, q := range s.Questions {
				if by := q.AnsweredBy; by != nil && by.Caller == contract.Human {
					ticked |= 8
				}
			}
		}
	}
	return ticked, ui.FirstSteps != contract.Off && ticked != 1<<len(contract.FirstSteps)-1
}
