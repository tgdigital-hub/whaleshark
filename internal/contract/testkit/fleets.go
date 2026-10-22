package testkit

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// ThreeFleets is one prepared morning with Fleet Mode on, written by hand
// from the design's pictures of the whole window (19.4): the project
// shop-site with an orchestrator and three fleets (billing led by Codex with
// five workers, search by Claude Code with six, help pages by Hermit with
// four), a second project, blog, with a lead agent and two workers, and two
// tabs in no project. Billing's worker "refund flow" asked a question that
// climbed to the person; "invoice pdf" waits to be checked. The person has
// looked at every tab this morning: nothing is unseen.
//
// In is what the side model is built from and Side7 what must come out with
// every fleet unfolded but help pages and no limit of rows. View and Cards
// are what the orchestrator's run is shown as: one card a fleet, and the
// one item with its fleet. Cards word what a fleet's card adds and what it
// is ordered and coloured by; how far a fleet is, its news and the times
// are whatever the builder makes of In, and the pictures' own figures (40,
// 55 and 75 percent) are in no record here. The package that builds the model is tested
// against Side7; the pane that draws it is tested on Side7 alone.
//
// Side7 is the morning as it comes out since phase 7: the workspaces are the
// keeper's (In.Terms.Spaces), each row has the first block of its menu, and
// what a menu's first block does is not among the work's own rows. It is
// worked out from the model of before, which this package keeps for its own
// tests and nobody else is held to.
type ThreeFleets struct {
	In    contract.SideInput
	Side7 *contract.Side
	old   *contract.Side
	View  contract.ViewInput
	Cards []contract.Card
	Item  contract.Item
}

const (
	shop = "/work/shop-site"
	blog = "/work/blog"
)

// Hermit is the tool the fixture wires in beside the two built in: one of
// the person's own, with no hooks and no words of its screen.
var Hermit = contract.Tool{Name: "hermit", Label: "Hermit", Run: []string{"/opt/hermit/bin/hermit", "chat"}, Hooks: contract.HooksNone,
	Suits: "long refactors; slow; runs on this machine, costs nothing", Mine: true}

type fleetSpec struct {
	name, run, tool, group string
	// workers are names; one ending in "?" has asked, in "!" has reported.
	workers []string
}

var fleetSpecs = []fleetSpec{
	{"billing", "r2", "codex", "G1", []string{"refund flow?", "invoice pdf!", "tax rounding", "card retries", "receipt mail"}},
	{"search", "r3", "claude", "G2", []string{"search box", "search results", "filters", "ranking", "typeahead", "synonyms"}},
	{"help pages", "r4", "hermit", "G3", []string{"getting started", "billing help", "search help", "faq"}},
}

// run makes a run with a lead agent in a pane, and adds that pane to snap.
func run(id, mode, pane, tab, label, cwd string, now time.Time, snap *contract.Snapshot) *contract.State {
	s := &contract.State{Version: contract.StateVersion, Tasks: map[string]*contract.Task{}, Attempts: map[string]*contract.Attempt{},
		Questions: map[string]*contract.Question{}}
	s.Run = contract.Run{ID: id, Mode: mode, CreatedAt: now.Add(-2 * time.Hour), Limit: 20,
		Orchestrator: &contract.Binding{Pane: pane, BoundAt: now.Add(-2 * time.Hour)}}
	snap.Panes = append(snap.Panes, contract.Pane{ID: pane, Tab: tab, Label: label, Cwd: cwd, Agent: "claude", Status: contract.StatusWorking})
	return s
}

// work adds a task with one live attempt in a tab of its own.
func work(s *contract.State, id, name, tool, pane, cwd string, now time.Time, snap *contract.Snapshot) *contract.Attempt {
	state := contract.AttemptWorking
	switch {
	case strings.HasSuffix(name, "?"):
		state = contract.AttemptAsked
	case strings.HasSuffix(name, "!"):
		state = contract.AttemptReported
	}
	name = strings.TrimRight(name, "?!")
	t := &contract.Task{ID: id, Name: name, Title: name, Brief: "briefs/" + id + ".md", Check: "make test", Placement: contract.PlaceShared,
		Agent: tool, Status: contract.TaskRunning, Attempts: []string{id + ".1"}, CreatedAt: now.Add(-time.Hour), SeenAt: now}
	if state == contract.AttemptReported {
		t.Status = contract.TaskReview
	}
	a := &contract.Attempt{ID: id + ".1", Task: id, N: 1, State: state, StateSince: now.Add(-10 * time.Minute), StartedAt: now.Add(-time.Hour),
		Agent: contract.Agent{Kind: tool, Name: "a-" + strings.ToLower(s.Run.ID+"-"+id)},
		Place: contract.Place{Tab: "t-" + pane, Pane: pane, Cwd: cwd, OpenedByUs: true},
		Seen:  contract.AgentSeen{Status: contract.StatusWorking, At: now, Liveness: contract.Live, LastWorking: now}}
	s.Tasks[id], s.Attempts[a.ID] = t, a
	snap.Panes = append(snap.Panes, contract.Pane{ID: pane, Tab: a.Place.Tab, Label: name, Cwd: cwd, Agent: tool, Name: a.Agent.Name, Status: contract.StatusWorking})
	return a
}

// Fleets builds the fixture. Every call gives a fresh one.
func Fleets() *ThreeFleets {
	now := time.Date(2026, 10, 12, 10, 20, 0, 0, time.UTC)
	snap := &contract.Snapshot{At: now, Instance: "k1"}
	person := contract.PersonDefaults()
	person.Fleets.Mode = contract.On
	tools := []contract.Tool{}
	for _, name := range []string{"claude", "codex"} {
		t, _ := contract.FindTool(contract.BuiltIn(), name)
		tools = append(tools, t)
	}
	tools = append(tools, Hermit)

	up := run("r1", contract.ModeFleets, "p1", "t-p1", "Orchestrator", shop, now, snap)
	up.Run.Fleets = map[string]*contract.Fleet{}
	runs := []*contract.State{up}
	fleets := map[string]*contract.State{}
	// Four first steps are ticked by what the morning holds; the fifth is the pane's.
	side := &contract.Side{Version: contract.SideVersion, At: now, NeedYou: 1, Title: "shop-site", Fresh: contract.Fresh{Checked: now}, Steps: 15, StepsShown: true}
	side.Rows = []contract.SideRow{{Kind: contract.RowLead, Name: "orchestrator", Root: shop, Run: "r1", Tab: "t-p1"}}
	var cards []contract.Card
	pane := 1
	for _, f := range fleetSpecs {
		pane++
		lead := fmt.Sprintf("p%d", pane)
		// The leader: the attempt of its group above, the lead agent of its run.
		a := work(up, f.group, f.name, f.tool, lead, shop, now, snap)
		a.Leads = f.run
		up.Tasks[f.group].Placement, up.Tasks[f.group].Fleet = contract.PlaceFleet, f.name
		up.Run.Fleets[f.name] = &contract.Fleet{Run: f.run, Tool: f.tool, Pane: lead, Tab: a.Place.Tab, Branch: "whaleshark/" + f.run,
			Since: now.Add(-time.Hour), Groups: []string{f.group}}
		low := &contract.State{Version: contract.StateVersion, Tasks: map[string]*contract.Task{}, Attempts: map[string]*contract.Attempt{},
			Questions: map[string]*contract.Question{}}
		low.Run = contract.Run{ID: f.run, CreatedAt: now.Add(-time.Hour), Limit: person.Fleets.Agents,
			Orchestrator: &contract.Binding{Pane: lead, BoundAt: now.Add(-time.Hour)}, Parent: &contract.Twin{Run: "r1", Fleet: f.name}}
		open := f.name != "help pages"
		row := contract.SideRow{Kind: contract.RowFleet, Look: contract.LookBuilding, Name: f.name, Tool: f.tool, Agents: len(f.workers),
			Limit: person.Fleets.Agents, Open: open, Root: shop, Run: f.run, Task: f.group, Tab: a.Place.Tab, Actions: []string{"park", "note-leader"}}
		card := contract.Card{Task: f.group, Name: f.name, Look: contract.LookBuilding, Colour: "building", Agent: f.tool, Tab: a.Place.Tab, Try: 1,
			Fleet: &contract.FleetCard{Run: f.run, Agents: len(f.workers), Limit: person.Fleets.Agents, Groups: 1, Of: len(f.workers), Led: true}}
		var under []contract.SideRow
		for i, w := range f.workers {
			pane++
			id := fmt.Sprintf("T%d", i+1)
			wa := work(low, id, w, f.tool, fmt.Sprintf("p%d", pane), shop, now, snap)
			r := contract.SideRow{Kind: contract.RowAgent, Depth: 1, Look: contract.LookBuilding, Name: low.Tasks[id].Name,
				Root: shop, Run: f.run, Task: id, Tab: wa.Place.Tab, Actions: []string{"stop", "note"}}
			switch wa.State {
			case contract.AttemptAsked:
				// Its question climbed: the answer goes to the twin in the run above.
				r.Look, r.Actions, r.Item, r.ItemRun = contract.LookNeedsYou, []string{"answer", "stop", "note"}, "q1", "r1"
			case contract.AttemptReported:
				r.Look, r.Actions = contract.LookToCheck, []string{"accept", "send-back", "stop", "note"}
				card.Fleet.ToCheck++
			}
			under = append(under, r)
		}
		if f.name == "billing" {
			// The worker asked its leader, the leader passed it up, the
			// orchestrator passed it on: the twin above is the person's.
			low.Questions["q1"] = &contract.Question{ID: "q1", Kind: contract.KindAsk, Form: contract.FormChoice, For: contract.ForOrchestrator,
				From: "T1.1", Task: "T1", Attempt: "T1.1", Holds: true, Text: "Refund to the card, or to store credit?", Options: []string{"Card", "Credit"},
				State: contract.QuestionOpen, CreatedAt: now.Add(-3 * time.Minute), Up: &contract.Twin{Run: "r1", ID: "q1"}}
			up.Questions["q1"] = &contract.Question{ID: "q1", Kind: contract.KindAsk, Form: contract.FormChoice, For: contract.ForHuman,
				From: a.ID, Task: f.group, Attempt: a.ID, Holds: true, Text: "Refund to the card, or to store credit?", Options: []string{"Card", "Credit"},
				State: contract.QuestionOpen, CreatedAt: now.Add(-2 * time.Minute), Down: &contract.Twin{Run: f.run, ID: "q1"}}
			row.Look, card.Look, card.Colour, card.Item = contract.LookNeedsYou, contract.LookNeedsYou, "needs", "q1"
			side.First = &under[0]
		}
		side.Rows = append(side.Rows, row)
		if open {
			side.Rows = append(side.Rows, under...)
		}
		cards = append(cards, card)
		fleets[f.name] = low
		runs = append(runs, low)
	}
	// The row the keys are on in the picture, and the tab in front.
	for i := range side.Rows {
		side.Rows[i].Front = side.Rows[i].Name == "search box"
	}
	for i := range snap.Panes {
		snap.Panes[i].Focused = snap.Panes[i].Label == "search box"
	}

	other := run("r1", "", "b1", "t-b1", "Lead", blog, now, snap)
	work(other, "T1", "spring post", "claude", "b2", blog, now, snap)
	work(other, "T2", "old links", "claude", "b3", blog, now, snap)
	snap.Panes = append(snap.Panes, contract.Pane{ID: "x1", Tab: "t-x1", Label: "notes", Cwd: "/srv/notes", Status: contract.StatusUnknown},
		contract.Pane{ID: "x2", Tab: "t-x2", Label: "logs", Cwd: "/var/log", Status: contract.StatusUnknown})
	side.Workspaces = []contract.Workspace{
		{Root: shop, Name: "shop-site", Branch: "main", Look: contract.LookNeedsYou, Agents: 19, Front: true},
		{Root: blog, Name: "blog", Branch: "drafts", Look: contract.LookBuilding, Agents: 3},
		{Name: "other tabs", Agents: 2, Other: true},
	}
	// The keeper's workspaces: every tab in the one of its project, the two
	// tabs of no project in the last, and shop-site, where the keys are, in front.
	snap.Spaces = []contract.Space{{ID: "s1", Root: shop, Name: "shop-site", Front: true}, {ID: "s2", Root: blog, Name: "blog", Tab: "t-b1"},
		{ID: "s3", Name: contract.OtherTabs, Tab: "t-x1"}}
	for i := range snap.Panes {
		p := &snap.Panes[i]
		if p.Workspace = map[string]string{shop: "s1", blog: "s2"}[p.Cwd]; p.Workspace == "" {
			p.Workspace = "s3"
		}
		if p.Focused {
			snap.Spaces[0].Tab = p.Tab
		}
	}
	in := contract.SideInput{Now: now, Terms: snap, Tools: tools, Person: person, Folded: []string{"r1/help pages"}, Checked: now,
		Projects: []contract.SideProject{{Root: shop, Branch: "main", Runs: runs}, {Root: blog, Branch: "drafts", Runs: []*contract.State{other}}}}
	view := contract.ViewInput{Now: now, Caller: contract.Human, State: up, Terms: snap, Limits: contract.ProjectDefaults(), Checked: now,
		Swept: contract.Swept{Ran: true, At: now}, Watched: true, Fleets: fleets, Person: person, Tools: tools}
	item := contract.Item{ID: "q1", Form: contract.FormChoice, Look: contract.LookNeedsYou, Task: "G1", Name: "refund flow", Fleet: "billing",
		Source: "passed up by the orchestrator", Since: now.Add(-2 * time.Minute), Text: "Refund to the card, or to store credit?", Holds: true}
	return &ThreeFleets{In: in, old: side, Side7: seven(side, snap, map[string]int{"s1": 1}), View: view, Cards: cards, Item: item}
}

// seven is a side model of before phase 7 as it must come out since, for
// the keeper's picture it was built from: need is what waits for the person
// in each workspace, by its id.
func seven(old *contract.Side, snap *contract.Snapshot, need map[string]int) *contract.Side {
	side := *old
	side.Workspaces, side.Rows, side.Marks = nil, nil, map[string]int{}
	for i, w := range old.Workspaces {
		sp := snap.Spaces[i]
		w.ID, w.Tab, w.Need, w.Offer = sp.ID, sp.Tab, need[sp.ID], contract.OfferRows(contract.OfferSpace, false, w.Other, true)
		tabs := map[string]bool{}
		for _, p := range snap.Panes {
			if p.Workspace == sp.ID {
				tabs[p.Tab] = true
			}
		}
		w.Tabs = len(tabs)
		side.Workspaces = append(side.Workspaces, w)
	}
	for _, r := range old.Rows {
		switch r.Kind {
		case contract.RowLead:
			r.Offer, r.Actions = contract.OfferRows(contract.OfferTab, false, false, true), []string{"note-lead"}
		case contract.RowFleet:
			r.Offer, r.Actions = contract.OfferRows(contract.OfferFleet, !r.Parked, false, true), nil
		case contract.RowAgent:
			r.Offer = contract.OfferRows(contract.OfferAgent, true, false, true)
			r.Actions = slices.DeleteFunc(slices.Clone(r.Actions), func(id string) bool { return id == "stop" })
			if r.Item != "" {
				side.Marks[r.Tab]++
			}
		case contract.RowTab:
			r.Offer = contract.OfferRows(contract.OfferTab, false, false, true)
		}
		if old.First != nil && r.Name == old.First.Name {
			first := r
			side.First = &first
		}
		side.Rows = append(side.Rows, r)
	}
	return &side
}
