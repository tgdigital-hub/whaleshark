package testkit

import (
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// ThreeSpaces is one prepared afternoon with three workspaces and Fleet Mode
// off, written by hand from the design's frames of the left section (21.3):
// tomsleeps in front, with its orchestrator and three workers ("sign-up
// page" asked the person, "login page" builds and has the keys, "help
// pages" has been idle for twelve minutes); TestRunFoldersAndPointers, one
// tab and no job; ops-portal behind, where its orchestrator and four
// workers go on ("invoices" asked the person, "audit" waits to be checked).
// Two more projects of the login have no workspace: old-site with a job
// open and four stopped tasks, and notes with none.
//
// In is what the side model is built from, for a section of thirty rows
// with the heavier line where it comes, and Side what must come out. The
// package that builds the model is tested against Side; the pane that
// draws it, and the program of a menu, on Side alone. The frames' own
// count for tomsleeps is 3, where the built count has the lead agent in it
// as ops-portal's 5 has: the fixture counts as built, 4.
type ThreeSpaces struct {
	In   contract.SideInput
	Side *contract.Side
}

const (
	toms, runs3, ops = "/work/tomsleeps", "/work/TestRunFoldersAndPointers", "/work/ops-portal"
	oldSite, notes   = "/work/old-site", "/work/notes"
)

// asks puts the question of an attempt in front of the person.
func asks(s *contract.State, task, text string, now time.Time) {
	s.Questions["q1"] = &contract.Question{ID: "q1", Kind: contract.KindAsk, Form: contract.FormChoice, For: contract.ForHuman,
		From: task + ".1", Task: task, Attempt: task + ".1", Holds: true, Text: text, Options: []string{"Yes", "No"},
		State: contract.QuestionOpen, CreatedAt: now.Add(-3 * time.Minute)}
}

// Spaces builds the fixture. Every call gives a fresh one.
func Spaces() *ThreeSpaces {
	now := time.Date(2026, 10, 13, 15, 5, 0, 0, time.UTC)
	snap := &contract.Snapshot{At: now, Instance: "k1"}
	person := contract.PersonDefaults()
	claude, _ := contract.FindTool(contract.BuiltIn(), "claude")

	front := run("r1", "", "p1", "t-p1", contract.MainTab, toms, now, snap)
	work(front, "T1", "sign-up page?", "claude", "p2", toms, now, snap)
	work(front, "T2", "login page", "claude", "p3", toms, now, snap)
	idle := work(front, "T3", "help pages", "claude", "p4", toms, now, snap)
	idle.Seen.Status, idle.Seen.LastWorking = contract.StatusIdle, now.Add(-12*time.Minute)
	asks(front, "T1", "Must the old sign-up link keep working?", now)
	snap.Panes = append(snap.Panes, contract.Pane{ID: "p5", Tab: "t-p5", Label: contract.MainTab, Cwd: runs3, Status: contract.StatusUnknown})
	behind := run("r1", "", "p6", "t-p6", contract.MainTab, ops, now, snap)
	work(behind, "T1", "invoices?", "claude", "p7", ops, now, snap)
	work(behind, "T2", "audit!", "claude", "p8", ops, now, snap)
	work(behind, "T3", "api keys", "claude", "p9", ops, now, snap)
	work(behind, "T4", "exports", "claude", "p10", ops, now, snap)
	asks(behind, "T1", "Does a credit note get a number of its own?", now)
	old := run("r1", "", "", "", "", oldSite, now, snap)
	old.Run.Orchestrator, snap.Panes = nil, snap.Panes[:len(snap.Panes)-1] // its tabs were closed with its workspace
	for _, id := range []string{"T1", "T2", "T3", "T4"} {
		a := work(old, id, "page "+id, "claude", "", oldSite, now, snap)
		a.State, a.Place = contract.AttemptStopped, contract.Place{}
		old.Tasks[id].Status = contract.TaskFailed
		snap.Panes = snap.Panes[:len(snap.Panes)-1]
	}
	space := map[string]string{toms: "s1", runs3: "s2", ops: "s3"}
	for i := range snap.Panes {
		p := &snap.Panes[i]
		p.Workspace, p.Focused = space[p.Cwd], p.Label == "login page"
		if p.Label == "help pages" {
			p.Status = contract.StatusIdle
		}
	}
	snap.Spaces = []contract.Space{
		{ID: "s1", Root: toms, Name: "tomsleeps", Tab: "t-p3", Front: true},
		{ID: "s2", Root: runs3, Name: "TestRunFoldersAndPointers", Tab: "t-p5"},
		{ID: "s3", Root: ops, Name: "ops-portal", Tab: "t-p6"},
	}
	in := contract.SideInput{Now: now, Terms: snap, Tools: []contract.Tool{claude}, Person: person, Checked: now, Height: 30,
		Projects: []contract.SideProject{{Root: toms, Branch: "main", Runs: []*contract.State{front}}, {Root: runs3, Branch: "main"},
			{Root: ops, Branch: "main", Runs: []*contract.State{behind}}, {Root: oldSite, Branch: "main", Runs: []*contract.State{old}}, {Root: notes}}}
	// What must come out. A workspace's row: its count is its agents with
	// the lead agent, as built, Need what waits for the person there, Tab
	// the tab it shows. A row's menu: Offer, then under the line what its
	// work offers; the row that asked offers its answer first.
	offer := contract.OfferRows(contract.OfferSpace, false, false, true)
	agent := func(look contract.Look, name, task, tab string, actions ...string) contract.SideRow {
		return contract.SideRow{Kind: contract.RowAgent, Look: look, Name: name, Root: toms, Run: "r1", Task: task, Tab: tab,
			Offer: contract.OfferRows(contract.OfferAgent, true, false, true), Actions: actions}
	}
	asked := agent(contract.LookNeedsYou, "sign-up page", "T1", "t-p2", "answer", "note")
	asked.Item = "q1"
	keys := agent(contract.LookBuilding, "login page", "T2", "t-p3", "note")
	keys.Front = true
	rests := agent(contract.LookIdle, "help pages", "T3", "t-p4", "note")
	rests.Word = "idle 12m" // for how long, as the frame of the design has it
	side := &contract.Side{Version: contract.SideVersion, At: now, NeedYou: 2, First: &asked, Title: "tomsleeps",
		Fresh: contract.Fresh{Checked: now}, Steps: 15, StepsShown: true,
		Workspaces: []contract.Workspace{
			{ID: "s1", Root: toms, Name: "tomsleeps", Branch: "main", Look: contract.LookNeedsYou, Agents: 4, Front: true, Need: 1, Tab: "t-p3", Tabs: 4, Offer: offer},
			{ID: "s2", Root: runs3, Name: "TestRunFoldersAndPointers", Branch: "main", Agents: 1, Tab: "t-p5", Tabs: 1, Offer: offer},
			{ID: "s3", Root: ops, Name: "ops-portal", Branch: "main", Look: contract.LookNeedsYou, Agents: 5, Need: 1, Tab: "t-p6", Tabs: 5, Offer: offer},
		},
		Rows: []contract.SideRow{
			{Kind: contract.RowLead, Name: "orchestrator", Root: toms, Run: "r1", Tab: "t-p1",
				Offer: contract.OfferRows(contract.OfferTab, false, false, true), Actions: []string{"note-lead"}},
			asked, keys, rests,
		},
		// Thirty rows, the line half-way down: row 15, as the frame has it.
		Split: contract.SplitRow(in.Height, in.Split, contract.SideHead, contract.SideFoot),
		Marks: map[string]int{"t-p2": 1, "t-p7": 1},
		Closed: []contract.Workspace{
			{Root: oldSite, Name: "old-site", Branch: "main", Word: "job open · 4 stopped"},
			{Root: notes, Name: "notes"},
		},
	}
	return &ThreeSpaces{In: in, Side: side}
}
