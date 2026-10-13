package view

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// space is a workspace while it is built: every row it could show, each
// fleet open, how many of them wait for the person and the first that does;
// whether a job is shown there, and whether one that nobody is bound to.
type space struct {
	contract.Workspace
	rows       []contract.SideRow
	need, done int // what waits for the person, and what was done today
	first      *contract.SideRow
	job, loose bool
	saved      []contract.SavedFleet
}

// needs reports whether a look is one that waits for the person.
func needs(l contract.Look) bool { return l != "" && order[l] <= order[contract.LookAtPrompt] }

func byLook(x, y contract.SideRow) int { return cmp.Compare(order[x.Look], order[y.Look]) }

// answer puts "answer" first among a row's actions.
func answer(actions []string) []string {
	return append([]string{"answer"}, slices.DeleteFunc(actions, func(a string) bool { return a == "answer" })...)
}

// Side builds the left section's model from the keeper's workspaces and
// every project of the login. It reads nothing, asks no folder and keeps
// nothing; a run's rows are its view's cards, so the bar says nothing of an
// agent that the other screens do not.
func Side(in contract.SideInput) *contract.Side {
	out := &contract.Side{Version: contract.SideVersion, At: in.Now, Warn: contract.Warned(in.Changed), Workspaces: []contract.Workspace{}, Rows: []contract.SideRow{},
		Fresh: contract.Fresh{Checked: in.Checked, Notes: in.Notes}, NewVersion: clean(in.NewVersion)}
	out.Steps, out.StepsShown = steps(in)
	if by := contract.AgentChanged(in.UI.ByAgent); by != "" {
		out.Warn = strings.TrimPrefix(out.Warn+" · "+clean(by), " · ")
	}
	if foot := contract.SideFoot + min(len(out.Warn), 1) + min(len(out.NewVersion), 1); in.Height > 0 {
		out.Split = contract.SplitRow(in.Height, in.Split, contract.SideHead, foot)
	}
	snap := in.Terms
	if snap == nil {
		snap, out.Fresh.Notes = &contract.Snapshot{}, append([]string{"the engine is not running"}, in.Notes...)
	}
	// Each pane's tab and each tab's workspace, as the keeper says them.
	tabs, of, count, focus := map[string]string{}, map[string]string{}, map[string]int{}, ""
	for _, p := range snap.Panes {
		if tabs[p.ID] = p.Tab; p.Focused && !p.Dock {
			focus = p.Tab
		}
		if _, seen := of[p.Tab]; !seen && !p.Dock {
			of[p.Tab] = p.Workspace
			count[p.Workspace]++
		}
	}
	// The workspaces in the keeper's order, the tabs of no project last;
	// at finds one by its id and by its folder. A workspace ends with its
	// last tab: one the picture has no tab of is gone.
	var spaces []*space
	var front *space
	at := map[string]*space{}
	for _, sp := range snap.Spaces {
		if count[sp.ID] == 0 {
			continue
		}
		w := &space{Workspace: contract.Workspace{ID: sp.ID, Root: sp.Root, Name: cmp.Or(clean(sp.Name), contract.SpaceName(sp.Root)), Other: sp.Root == "",
			Tab: sp.Tab, Tabs: count[sp.ID], Gone: sp.Root != "" && slices.Contains(in.Gone, sp.Root)}}
		w.Offer = contract.OfferRows(contract.OfferSpace, false, w.Other, true)
		if spaces, at[sp.ID] = append(spaces, w), w; sp.Root != "" && at[sp.Root] == nil {
			at[sp.Root] = w
		}
		if front == nil || sp.Front && !front.Front {
			front, w.Front = w, sp.Front
		}
	}
	slices.SortStableFunc(spaces, func(x, y *space) int { return min(len(y.Root), 1) - min(len(x.Root), 1) })
	taken := map[string]bool{"": true}
	for _, p := range in.Projects {
		home, runs, stopped, shown := at[p.Root], map[string]*contract.State{}, 0, false
		for _, s := range p.Runs {
			if runs[s.Run.ID] = s; s.Run.Orchestrator != nil {
				taken[tabs[s.Run.Orchestrator.Pane]] = true
			}
			for _, a := range s.Attempts {
				taken[tabs[a.Place.Pane]] = true
			}
			for _, t := range s.Tasks {
				if n := len(t.Attempts); n > 0 && s.Attempts[t.Attempts[n-1]] != nil && s.Attempts[t.Attempts[n-1]].State == contract.AttemptStopped {
					stopped++
				}
			}
		}
		// A run is shown where its lead agent's tab is, else in the
		// workspace of its project's folder, else nowhere: only then, and
		// with no workspace of its own, is its project one that can be opened.
		for _, s := range p.Runs {
			if s.Run.Parent != nil && runs[s.Run.Parent.Run] != nil {
				continue
			}
			w, lead := home, ""
			if b := s.Run.Orchestrator; b != nil {
				lead = tabs[b.Pane]
				w = cmp.Or(at[of[lead]], home)
			}
			if w != nil {
				w.run(in, p, s, runs, tabs)
				w.job, w.loose, shown = true, w.loose || lead == "", true
			}
		}
		if home != nil {
			home.Branch, home.saved = clean(p.Branch), p.Saved
			continue
		} else if shown {
			continue // its job is on the screen already, in another workspace
		}
		c := contract.Workspace{Root: p.Root, Name: contract.SpaceName(p.Root), Branch: clean(p.Branch)}
		if len(p.Runs) > 0 {
			if c.Word = "job open"; stopped > 0 {
				c.Word += fmt.Sprint(" · ", stopped, " stopped")
			}
		}
		out.Closed = append(out.Closed, c)
	}
	slices.SortStableFunc(out.Closed, func(x, y contract.Workspace) int { return min(len(y.Word), 1) - min(len(x.Word), 1) })
	// The tabs the person opened himself: a tab of no agent of ours.
	for _, p := range snap.Panes {
		if w := at[p.Workspace]; !p.Dock && !p.Ours && !taken[p.Tab] && w != nil {
			taken[p.Tab] = true
			w.rows = append(w.rows, contract.SideRow{Kind: contract.RowTab, Name: clean(p.Label), Root: w.Root, Tab: p.Tab, Offer: contract.OfferRows(contract.OfferTab, false, false, true)})
		}
	}
	if front == nil {
		return out
	}
	// The tab in front is the one the keeper says the workspace shows.
	if front.Front, out.Marks = true, map[string]int{}; front.Tab != "" || of[focus] != front.ID {
		focus = front.Tab
	}
	for _, w := range spaces {
		// With no job open, the fleets the person keeps there stand all the same.
		for _, f := range w.saved {
			if !w.job && in.Person.Fleets.Mode == contract.On {
				w.rows = append(w.rows, contract.SideRow{Kind: contract.RowFleet, Name: clean(f.Name), Word: contract.WordSaved, Root: w.Root})
			}
		}
		w.sum(focus, out.Marks)
		if out.NeedYou += w.need; out.First == nil {
			out.First = w.first
		}
	}
	// Where the upper panel has fewer rows than workspaces, the last of them
	// says how many more there are, and the one in front keeps its row.
	if fits := (out.Split - contract.SideHead) / 2; in.Height > 0 && len(spaces) > fits {
		shown := max(fits-1, 1)
		if i := slices.Index(spaces, front); i >= shown {
			spaces = slices.Insert(slices.Delete(spaces, i, i+1), shown-1, front)
		}
		out.More = len(spaces) - shown
	}
	for _, w := range spaces {
		out.Workspaces = append(out.Workspaces, w.Workspace)
	}
	switch out.Title, out.Done = front.Name, front.done; {
	case front.Gone:
		out.Job = contract.NoFolder
	case front.loose:
		out.Job = contract.JobOpenHere
	case !front.job && !front.Other:
		out.Hint = "no job yet · start your agent here: " + contract.DefaultTool
		// An agent that sits in the tab in front is started already.
		for _, p := range snap.Panes {
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

// run adds a run of the project p: its lead agent, then its agents in the
// one order; for an orchestrator's run its fleets, the one that needs most
// first, each with its workers under it. A row says the project's folder,
// which is not the workspace's where the lead agent's tab is of another.
func (w *space) run(in contract.SideInput, p contract.SideProject, s *contract.State, runs map[string]*contract.State, tabs map[string]string) {
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
		// Its menu is its tab's: the row is the record's word for who leads.
		w.rows = append(w.rows, contract.SideRow{Kind: contract.RowLead, Name: name, Root: p.Root, Run: s.Run.ID, Tab: tabs[b.Pane],
			Offer: contract.OfferRows(contract.OfferTab, false, false, tabs[b.Pane] != ""), Actions: []string{"note-lead"}})
	}
	v := Build(vin)
	var fleets [][]contract.SideRow
	for _, r := range w.agents(v, 0, p.Root) {
		c := cardOf(v, r.Task)
		if c.Fleet == nil {
			w.rows = append(w.rows, r)
			continue
		}
		fleet := s.Tasks[r.Task].Fleet
		r.Kind, r.Tool, r.Agents, r.Limit, r.Parked, r.Run, r.Item = contract.RowFleet, clean(c.Agent), c.Fleet.Agents, c.Fleet.Limit, c.Fleet.Parked, c.Fleet.Run, ""
		r.Open, r.Offer, r.Actions = !slices.Contains(in.Folded, s.Run.ID+"/"+fleet), contract.OfferRows(contract.OfferFleet, !r.Parked, false, true), nil
		block := []contract.SideRow{r}
		if low := vin.Fleets[fleet]; low != nil {
			lin := vin
			lin.State, lin.Fleets = low, nil
			under := w.agents(Build(lin), 1, p.Root)
			// A worker whose question climbed to the person waits for him.
			for _, q := range s.Questions {
				if q.Down == nil || q.Down.Run != r.Run || q.State != contract.QuestionOpen || q.For != contract.ForHuman || low.Questions[q.Down.ID] == nil {
					continue
				}
				for i := range under {
					if u := &under[i]; u.Task == low.Questions[q.Down.ID].Task && !needs(u.Look) {
						u.Look, u.Word, u.Actions, u.Item, u.ItemRun = contract.LookNeedsYou, "", answer(u.Actions), q.ID, s.Run.ID
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
	for _, f := range p.Saved {
		if live := s.Run.Fleets[f.Name]; s.Run.Mode == contract.ModeFleets && (live == nil || !live.EndedAt.IsZero()) {
			w.rows = append(w.rows, contract.SideRow{Kind: contract.RowFleet, Name: clean(f.Name), Word: contract.WordSaved, Root: p.Root, Run: s.Run.ID})
		}
	}
}

// agents is a view's cards as rows, in the view's order. A row's menu
// begins with what its kind offers, by whether its agent is at work, which
// is when its card may be stopped; what that block does is not said again
// among the work's own, and a row with an item offers its answer first.
func (w *space) agents(v *contract.View, depth int, root string) (rows []contract.SideRow) {
	for _, sec := range v.Sections {
		for _, c := range sec.Cards {
			r := contract.SideRow{Kind: contract.RowAgent, Depth: depth, Look: c.Look, Word: clean(c.Word), Name: clean(c.Name), Root: root, Run: v.Run,
				Task: c.Task, Tab: c.Tab, Item: c.Item, Unseen: c.Unseen, Offer: contract.OfferRows(contract.OfferAgent, slices.Contains(c.Actions, "stop"), false, c.Tab != ""),
				Actions: slices.DeleteFunc(slices.Clone(c.Actions), func(a string) bool { return a == "go" || a == "stop" || a == "close" })}
			if r.Item != "" {
				r.Actions = answer(r.Actions)
			}
			rows = append(rows, r)
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
// which under a fleet that waits is the worker it waits for. marks counts
// what waits by tab: with the agent's own, and with a fleet's leader's only
// where no worker of it is the one that waits.
func (w *space) sum(focus string, marks map[string]int) {
	w.Need = w.need
	for i := range w.rows {
		r := &w.rows[i]
		r.Front = r.Tab != "" && r.Tab == focus
		under := i+1 < len(w.rows) && w.rows[i+1].Depth == 1 && needs(w.rows[i+1].Look)
		if needs(r.Look) && r.Tab != "" && (r.Kind == contract.RowAgent || r.Kind == contract.RowFleet && !under) {
			marks[r.Tab]++
		}
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

// steps is which of the five first steps are ticked, a bit each from the
// first: those ui.json holds, and those the left section's input shows to
// have happened (an agent runs in a tab; a job exists; a worker has its row;
// the person answered, or a job is ten minutes old). The fifth is the
// pane's to tick. show is false once they were hidden or all are ticked.
func steps(in contract.SideInput) (ticked int, show bool) {
	ui := in.UI
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
