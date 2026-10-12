package contract

import "time"

// Rules is every legal change to a run, as pure functions on the record:
// no file, no terminals, no clock. Each either changes s and returns nil, or
// changes nothing and returns a *Refusal, so a function handed to
// Store.Change returns that error and nothing is written. Report is the one
// that can refuse and still have something to save, and says so in its own
// result. Only the rules package implements it.
type Rules interface {
	// 6.2 Run.
	NewRun(id, objective string, limit int, base *GitRef, by Binding, now time.Time) *State
	CloseRun(s *State, abandon bool, now time.Time) error
	// Integrated writes down where accepted work is collected: a field of
	// in that is not empty takes the place of the record's. Checked moves
	// the tip itself, to the commit it is given.
	Integrated(s *State, in Integration) error
	// Stuck keeps the mark of one stuck episode: with on it raises stuck
	// unless the mark is set, and reports whether it did; without, it
	// clears the mark. What stuck means is the wait's to say.
	Stuck(s *State, on bool, now time.Time) (raised bool)
	// Found takes a scan's findings as the run's own and raises one event
	// for each that the run did not have: overlap for "overlap" and
	// "lands", scope for "scope", nothing for "same". It ends a scan that
	// was due.
	Found(s *State, findings []Finding, now time.Time) (fresh []Finding)
	// Takeover binds the run to another pane and raises gen, fencing the old one.
	Takeover(s *State, by Binding, now time.Time) error
	Pause(s *State, by string, now time.Time) error
	Resume(s *State, now time.Time) error
	// Allowed refuses a command the caller may not run on this run now: the
	// wrong kind of caller, a pane the run is not bound to, or a paused run.
	Allowed(s *State, c Caller, command, sub string) error

	// 6.3 Task. taken holds the names on every open tab of the login.
	AddTask(s *State, t Task, taken []string, now time.Time) error
	EditTask(s *State, id string, edit TaskEdit, now time.Time) (ready []string, err error)
	// Looked stamps a task as seen by the person: show and jump call it.
	Looked(s *State, task string, now time.Time) error
	CancelTask(s *State, id string, now time.Time) error
	ResetTask(s *State, id string, now time.Time) error
	// FindTask resolves an id or a name.
	FindTask(s *State, idOrName string) (*Task, error)

	// 6.4 Attempt.
	// Start writes a new attempt as starting; paneHoldsAgent is what a fresh
	// snapshot says of the previous attempt's pane.
	Start(s *State, task string, retry, paneHoldsAgent bool, tokenHash string, a Agent, gated bool, now time.Time) (attempt string, err error)
	// Placed records the tab the attempt got; Working and StartFailed end the start.
	Placed(s *State, attempt string, p Place, now time.Time) error
	Working(s *State, attempt string, now time.Time) error
	StartFailed(s *State, attempt, why string, now time.Time) error
	// Token refuses a worker's command whose token is not the attempt's:
	// progress, ask and mail call it; Report checks for itself and keeps the refusal.
	Token(s *State, attempt, tokenHash string) error
	Progress(s *State, attempt string, pct int, note string, now time.Time) error
	// Report records a worker's report. kept is a refusal that was written
	// into the record as a rejected event: the caller lets Store.Change save
	// and then ends with it. With err nothing changed.
	Report(s *State, attempt, tokenHash, outcome, summary string, evidence []EvidenceFile, now time.Time) (already bool, kept *Refusal, err error)
	// Checking and Checked are the two locked steps of accept; a failed check
	// is stored on the attempt and raises check_failed.
	Checking(s *State, task string, c Checked, now time.Time) error
	Checked(s *State, task string, result CheckResult, how Accepted, now time.Time) (ready []string, err error)
	// Unchecked takes an attempt back from checking to reported when no
	// check ran: a conflict, a folder that moved, a check that could not start.
	Unchecked(s *State, task string, now time.Time) error
	Reject(s *State, task, why string, agent Liveness, now time.Time) error
	Stop(s *State, task string, keepTab bool, now time.Time) error
	// Seen applies what one sweep saw of an attempt: the cached block, the
	// flags, the two-step exit and the events each raises once per episode.
	Seen(s *State, attempt string, pane *Pane, startLockHeld bool, limits ProjectFile, now time.Time) (changed bool)
	// LeadSeen applies the sweep's two rows about the lead agent's own pane.
	LeadSeen(s *State, pane *Pane, waiting bool, now time.Time) (changed bool)

	// 6.5 Inbox.
	Raise(s *State, e Event, now time.Time) int
	// Batch hands out the outstanding batch again, or a new one when an
	// unacknowledged event is of a wanted kind.
	Batch(s *State, kinds []string, now time.Time) (b *Batch, events []Event, replayed bool, err error)
	Ack(s *State, delivery string) ([]Event, error)

	// 6.6 Questions and items.
	Ask(s *State, attempt, text string, options []string, now time.Time) (id string, err error)
	Need(s *State, q Question, now time.Time) (id string, err error)
	Escalate(s *State, id string, now time.Time) error
	Answer(s *State, id, text string, by Origin, settle time.Duration, now time.Time) error
	Undo(s *State, id string, by Origin, now time.Time) error
	// Use hands a settled answer to whoever asked and makes it final. ok is
	// false while the item is open, settling, or the run is paused.
	Use(s *State, id string, now time.Time) (answer string, ok bool, err error)
	CloseQuestion(s *State, id string, now time.Time) error
	// Shown stamps the first moment an item was put in front of the person;
	// whoever nudges calls it, since only it knows Do not disturb.
	Shown(s *State, id string, now time.Time) error

	// 6.8 Messages down to an attempt.
	Tell(s *State, task, text, body string, now time.Time) (seq int, err error)
	ReadMail(s *State, attempt string, now time.Time) ([]Mail, error)
	Pointed(s *State, attempt string, seq int, now time.Time) error

	// 6.7 Worktree: nil removes the record.
	SetWorktree(s *State, task string, w *Worktree, now time.Time) error
	// Synced counts one more sync of a task that has a worktree and keeps
	// its brief; sync then sends the brief with Tell. A task synced twice
	// is the one to accept next. From phase 6 a group is counted too, with
	// no brief: its copy is its fleet's branch.
	Synced(s *State, task, brief string, now time.Time) (times int, err error)

	// 19.1 Fleets. up is an orchestrator's run and low one of its fleets'
	// runs; a caller changes the two with Store.Both. A group is a task of
	// up whose Fleet is set: AddTask places it (in a run of ModeFleets only,
	// and there every task is one), Start, Report, Reject, Stop and the
	// rest move it as they move any task.
	//
	// Room refuses a start the person's limits leave no room for. fleets is
	// every fleet's run of up, by the fleet's name. With an empty worker it
	// is asked for a group of up placed in fleet: a living leader takes it
	// and nothing is counted; else a new leader is one more of
	// limits.Leaders (LimitLeaders) and of limits.Total (LimitTotal). With a
	// worker it is asked for that task of the fleet's own run: one more of
	// limits.Agents, the leader not counted (LimitFleet), and of the total.
	// cap is how many agents a server allows the login in all, 0 for no
	// server: it takes the place of limits.Total where it is lower.
	Room(up *State, fleets map[string]*State, fleet, worker string, limits PersonConfig, cap int) error
	// Lead makes a group's attempt in up the leader of low: it writes the
	// attempt's Leads, low's Parent and up's entry of the fleet with by as
	// the leader's pane, and binds low to that pane as Takeover does. For a
	// fleet that has a living leader by is empty: the attempt takes that
	// leader's place. A fleet whose leader had ended is taken over the same
	// way. For a new leader it is called after Placed, which gives the tab.
	Lead(up, low *State, attempt string, by Binding, branch string, now time.Time) error
	// Park stops a fleet whose leader holds no other open group: every live
	// attempt of low is stopped and its task ready again, as Stop does one,
	// and the fleet's entry loses its pane. tabs are theirs to close.
	Park(up, low *State, group string, now time.Time) (tabs []string, err error)
	// Ended closes a fleet's run with its last group settled, as CloseRun
	// does, and stamps the fleet's entry in up.
	Ended(up, low *State, abandon bool, now time.Time) error
	// Twin hands a question or an item of low up one level: it is then for
	// the orchestrator, and a twin of it is made in up (Up here, Down
	// there), holding the group if the first held its task, urgent if the
	// first was. escalate in a fleet calls it, and need from a leader.
	Twin(up, low *State, id string, now time.Time) (twin string, err error)
	// Down copies the settled answer of a twin in up to its other half in
	// low, answered by "orchestrator" or by the person's own origin, and
	// uses the twin; done is false where there was nothing to copy. answer
	// on a twin calls it, and the sweep of either run for a pair a kill left apart.
	Down(up, low *State, twin string, now time.Time) (done bool, err error)

	// Phase 6 (20.1 to 20.4, 20.18). The items below are the tool's own
	// (FromTool, for the human) and of a cause PersonOnly names: Answer
	// refuses them to every caller but the person, and --relayed too, with
	// RefusedPersonOnly. Use, once such an answer has settled, also writes
	// what it decides: a plan's tasks into Run.Plan.Approved; the
	// fingerprint seen into Run.Protect.Print; for AnswerMore a fifth of
	// the limit into Spend.More and for AnswerNoLimit Spend.Off, each
	// ending the hold as Resume ends a pause (and adding the time rested to
	// Attempt.Rested, which Resume does too); a setting's change it only
	// hands back, for the caller to make with Set as the person. An answer
	// that sends back, or leaves, writes nothing.
	//
	// Planned is asked by start in a job (a run with no parent) while
	// plan.first is on, with the tasks it would start. All approved: nil.
	// With by, the person's own start: they are approved by that, nil.
	// Else nothing may start: it opens the plan's item (a signoff,
	// CausePlan, Tasks the ones that lack approval, text the plan as
	// Kit.PlanText words it) or brings the open one up to date, and returns
	// the refusal RefusedPlanFirst as kept: Store.Change saves, then the
	// caller ends with it.
	Planned(s *State, tasks []string, text string, by *Origin, now time.Time) (kept *Refusal)
	// Protect is told by accept in a job, after a collect, what
	// Integrator.Protected found: seen is empty when no protected file
	// differs from the base. A fingerprint that is new and not the approved
	// one opens the item (a signoff, CauseProtect, holding nothing) or
	// brings it up to date, raises one protected event for the task and
	// reports asked; the approved one, or none, closes an open item.
	Protect(s *State, task, seen string, files []string, now time.Time) (asked bool)
	// Landable refuses a landing (RefusedProtected) whose protected files,
	// worked out again from the tip, are not the ones approved.
	Landable(s *State, seen string, files []string) error
	// Spend is the sweep's look at a job (fleet empty) or at one fleet of
	// it, on the job's record: spent as Spent gives it, limit as
	// PersonConfig.SpendLimit. Crossing four fifths it raises budget once;
	// at the limit, once, it marks the Spend held, pauses the job's own run
	// by HeldBy (a fleet's run is Hold's), raises budget and the urgent
	// choice of CauseBudget. It answers SpendWarned, SpendHeld or nothing.
	Spend(s *State, fleet string, spent time.Duration, limit int, now time.Time) (did string)
	// Hold brings a fleet's run in line with its job's record: paused by
	// HeldBy while the job or that fleet is held, and resumed when neither
	// is. The caller then has Kit.Hold do the same with the run's hold file
	// and its agents.
	Hold(up, low *State, now time.Time) (changed bool)
	// Going judges one live attempt after Seen, while stuck.watch is on:
	// the signs, the verdict and its level. exact is Tool.Exact of its
	// tool; ctx is its context figure, not Known for a tool that tells
	// none. It writes only when a verdict or a level changes: the stuck
	// fields of AgentSeen, a stuck event for this run's lead agent at
	// ToldLeader, and in a job the person's to-do of CauseStuck one climb
	// later, withdrawn when the verdict clears. In a fleet's run the levels
	// above the leader lie in the job's run: up says that Passed is owed.
	// Tell, Stop, Reject, Synced, ResetTask and Start of the task count as
	// dealing with it and start the climb's clock again.
	Going(s *State, attempt string, exact bool, ctx CtxFile, limits ProjectFile, climb time.Duration, now time.Time) (changed, up bool)
	// Passed writes into the job's run what a fleet's attempt has reached:
	// a stuck event naming the fleet at ToldOrchestrator, the person's
	// to-do at ToldPerson, and its withdrawal once the verdict cleared.
	Passed(up, low *State, attempt string, now time.Time) error
	// SetAsked takes an agent's change of a setting that holds agents or
	// calls the person: it opens the item (a signoff, CauseSetting, Set the
	// change, text the setting's own description) or replaces the value in
	// the one that is open for that key. A key the person left as it was in
	// this run is refused, and not asked again.
	SetAsked(s *State, ask SetAsk, text string, now time.Time) (id string, err error)
}

// TaskEdit is what task edit may replace; a nil field is left as it is.
type TaskEdit struct {
	Brief, Check *string
	Owns, After  *[]string
}
