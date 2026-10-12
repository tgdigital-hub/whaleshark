package testkit

import (
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Guards is one prepared afternoon of a job with every guard switched on,
// written by hand from the pictures of phase 6: the job "the October list"
// with two fleets, billing (Codex) and search (Claude Code). What it holds,
// so that a package can be tested on it without the others:
//
//   - the plan was approved from the pane at 09:02 for the two groups there
//     were; a third group, G3, was added since and its start was refused:
//     the plan's item n1 is open and shows G3 alone;
//   - billing's group was collected at 12:30 and changed a migration: the
//     person approved the fingerprint f1, a later collect made it f2, and
//     the item n2 is open again;
//   - 33 of 40 agent-hours are spent (Spent of the three runs at Now): the
//     four fifths were told, nothing is held;
//   - the orchestrator asked for the spending guard to be switched off:
//     the item n3 waits for the person's click;
//   - billing's worker "sign-up page" is stuck since 12:10 by two signs, its
//     leader was told then and the orchestrator ten minutes later;
//   - search's worker "cart filter" works on, with nothing wrong.
//
// Person is the settings it was made under, Up the job's record and Low the
// fleets' by name.
type Guards struct {
	Now    time.Time
	Person contract.PersonConfig
	Up     *contract.State
	Low    map[string]*contract.State
}

// Guarded builds the afternoon afresh: a test may change what it gets.
func Guarded() Guards {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 12, h, m, 0, 0, time.UTC) }
	tool := contract.Origin{Caller: contract.Human, Where: contract.WherePane}
	person := contract.PersonDefaults()
	person.Fleets.Mode, person.Plan.First, person.Protect.Files, person.Spend.Guard = contract.On, true, true, true

	run := func(id, objective string) *contract.State {
		return &contract.State{Version: contract.StateVersion, Tasks: map[string]*contract.Task{}, Attempts: map[string]*contract.Attempt{},
			Questions: map[string]*contract.Question{}, Inbox: contract.Inbox{Events: []contract.Event{}},
			Run: contract.Run{ID: id, Objective: objective, CreatedAt: at(9, 0), Orchestrator: &contract.Binding{Pane: "w1:p1", Workspace: "w1", BoundAt: at(9, 0)}}}
	}
	agent := func(s *contract.State, task, name, kind string, state contract.AttemptState, from time.Time, hours float64) *contract.Attempt {
		a := &contract.Attempt{ID: task + ".1", Task: task, N: 1, State: state, StateSince: from, StartedAt: from, Agent: contract.Agent{Kind: kind, Name: kind},
			Place: contract.Place{Tab: name, Pane: "w1:" + s.Run.ID + task}, Seen: contract.AgentSeen{Status: "working", Liveness: contract.Live, At: at(13, 19)}}
		if state == contract.AttemptAccepted {
			a.EndedAt = from.Add(time.Duration(hours * float64(time.Hour)))
		}
		s.Attempts[a.ID] = a
		s.Tasks[task] = &contract.Task{ID: task, Name: name, Title: name, Check: contract.CheckNone, Placement: contract.PlaceWorktree, Status: contract.TaskRunning, Attempts: []string{a.ID}, CreatedAt: at(9, 0)}
		return a
	}

	up := run("r7", "the October list")
	up.Run.Mode = contract.ModeFleets
	up.Run.Fleets = map[string]*contract.Fleet{
		"billing": {Run: "r8", Tool: "codex", Pane: "w1:r7G1", Tab: "billing", Since: at(9, 5), Groups: []string{"G1"}},
		"search":  {Run: "r9", Tool: "claude", Pane: "w1:r7G2", Tab: "search", Since: at(9, 5), Groups: []string{"G2"}},
	}
	for id, fleet := range map[string]string{"G1": "billing", "G2": "search"} {
		a := agent(up, id, fleet, up.Run.Fleets[fleet].Tool, contract.AttemptWorking, at(9, 5), 0)
		a.Leads = up.Run.Fleets[fleet].Run
		up.Tasks[id].Placement, up.Tasks[id].Fleet = contract.PlaceFleet, fleet
	}
	up.Tasks["G3"] = &contract.Task{ID: "G3", Name: "help pages", Title: "help pages", Check: contract.CheckNone, Placement: contract.PlaceFleet,
		Fleet: "help pages", Agent: "claude", Status: contract.TaskReady, CreatedAt: at(13, 0)}
	item := func(id, form, cause, text string, since time.Time) *contract.Question {
		q := &contract.Question{ID: id, Kind: contract.KindNeed, Form: form, For: contract.ForHuman, From: contract.FromTool, Cause: cause,
			Text: text, State: contract.QuestionOpen, CreatedAt: since}
		up.Questions[id] = q
		return q
	}
	up.Run.Plan = &contract.Approval{Approved: []string{"G1", "G2"}, At: at(9, 2), By: &tool, Item: "n1"}
	item("n1", contract.FormSignoff, contract.CausePlan, "Plan for \"the October list\" · 1 more thing for help pages", at(13, 1)).Tasks = []string{"G3"}
	up.Run.Protect = &contract.Approval{Print: "f1", Seen: "f2", Files: []string{"db/migrations/0042_refunds.sql", ".env.example"}, At: at(12, 40), By: &tool, Item: "n2"}
	item("n2", contract.FormSignoff, contract.CauseProtect, "Protected files changed in this job", at(13, 5)).Task = "G1"
	up.Run.Spend = contract.Spend{Warned: true}
	item("n3", contract.FormSignoff, contract.CauseSetting, "Spending guard: "+must(contract.FindKey("spend.guard")).Help, at(13, 10)).Set =
		&contract.SetAsk{Key: "spend.guard", From: contract.On, To: contract.Off}
	up.Inbox.Events = append(up.Inbox.Events,
		contract.Event{Seq: 1, At: at(12, 30), Kind: "protected", Task: "G1", Text: "G1 changed protected files: db/migrations/0042_refunds.sql"},
		contract.Event{Seq: 2, At: at(12, 20), Kind: "stuck", Task: "G1", Text: "billing › sign-up page looks stuck",
			Data: map[string]any{"why": "stale,check", "level": contract.ToldOrchestrator, "fleet": "billing", "task": "T7"}},
		contract.Event{Seq: 3, At: at(13, 15), Kind: "budget", Text: "33 of 40 agent-hours used"})
	up.Counters = contract.Counters{Seq: 3, Need: 3}

	billing, search := run("r8", "billing"), run("r9", "search")
	billing.Run.Parent, search.Run.Parent = &contract.Twin{Run: "r7", Fleet: "billing"}, &contract.Twin{Run: "r7", Fleet: "search"}
	billing.Run.Orchestrator.Pane, search.Run.Orchestrator.Pane = "w1:r7G1", "w1:r7G2"
	// 33 agent-hours at 13:20: two leaders open since 09:05 (8h30m); of
	// billing the refund flow for 3h25m and the invoice pdf for 3h05m, both
	// accepted, and the sign-up page since 09:10 (4h10m); of search three
	// open since 09:15, 09:20 and 09:30 (11h55m) less an hour one of them
	// rested, and one that ended after 2h55m.
	stuck := agent(billing, "T7", "sign-up page", "codex", contract.AttemptWorking, at(9, 10), 0)
	stuck.Progress = &contract.Progress{Pct: 50, Note: "wiring the form", At: at(11, 8)}
	stuck.Seen.StaleFlagged, stuck.Seen.StuckSince, stuck.Seen.StuckWhy = true, at(12, 10), "stale,check"
	stuck.Seen.StuckTold, stuck.Seen.StuckAt = contract.ToldOrchestrator, at(12, 20)
	billing.Tasks["T7"].CheckFails = contract.StuckTimes
	done := agent(billing, "T3", "refund flow", "codex", contract.AttemptAccepted, at(9, 5), 3+25.0/60)
	billing.Tasks["T3"].Status, billing.Tasks["T3"].Accepted = contract.TaskDone, &contract.Accepted{How: contract.AcceptCheck, At: done.EndedAt}
	pdf := agent(billing, "T6", "invoice pdf", "codex", contract.AttemptAccepted, at(9, 10), 3+5.0/60)
	billing.Tasks["T6"].Status, billing.Tasks["T6"].Accepted = contract.TaskDone, &contract.Accepted{How: contract.AcceptCheck, At: pdf.EndedAt}
	billing.Inbox.Events = append(billing.Inbox.Events, contract.Event{Seq: 1, At: at(12, 10), Kind: "stuck", Task: "T7", Attempt: "T7.1",
		Text: "sign-up page looks stuck: " + contract.StuckWhy(stuck.Seen.StuckWhy)})
	billing.Counters.Seq = 1
	agent(search, "T1", "cart filter", "claude", contract.AttemptWorking, at(9, 15), 0).Progress = &contract.Progress{Pct: 70, Note: "filters done, sorting next", At: at(13, 12)}
	agent(search, "T2", "result page", "claude", contract.AttemptWorking, at(9, 20), 0).Rested = time.Hour
	agent(search, "T4", "typo search", "claude", contract.AttemptWorking, at(9, 30), 0)
	ended := agent(search, "T5", "index job", "claude", contract.AttemptAccepted, at(9, 0), 2+55.0/60)
	search.Tasks["T5"].Status, search.Tasks["T5"].Accepted = contract.TaskDone, &contract.Accepted{How: contract.AcceptCheck, At: ended.EndedAt}
	return Guards{Now: at(13, 20), Person: person, Up: up, Low: map[string]*contract.State{"billing": billing, "search": search}}
}

func must(k *contract.Key, _ []string) *contract.Key { return k }
