// Package contract holds everything the other packages share: the shapes on
// disk, the view model, the interfaces between packages, and the command and
// action tables. Only a contract package edits it.
package contract

import "time"

// StateVersion is raised by every change to the shape of state.json. 2 is
// phase 5: a run's mode, parent and fleets, a task's fleet, an attempt's
// leads, a question's two twins. Each is empty in a file of version 1, which
// is read as it stands and written back as 2 by the first change, under the
// lock, with the older file kept beside it. 3 is phase 6: a run's plan,
// protected files and spending, an attempt's rest under a hold and what the
// stuck detector keeps, a progress note said again, a task's failed checks,
// a question's tasks and setting. Each is empty in an older file.
const StateVersion = 3

// State is state.json, the one authoritative record of a run.
type State struct {
	Version   int                  `json:"version"`
	Run       Run                  `json:"run"`
	Tasks     map[string]*Task     `json:"tasks"`
	Attempts  map[string]*Attempt  `json:"attempts"`
	Questions map[string]*Question `json:"questions"`
	Inbox     Inbox                `json:"inbox"`
	Counters  Counters             `json:"counters"`
}

type Counters struct {
	Seq      int `json:"seq"`
	Question int `json:"question"`
	Need     int `json:"need"`
	Delivery int `json:"delivery"`
}

type Run struct {
	ID           string       `json:"id"`
	Objective    string       `json:"objective"`
	CreatedAt    time.Time    `json:"created_at"`
	ClosedAt     time.Time    `json:"closed_at,omitzero"`
	Orchestrator *Binding     `json:"orchestrator"`
	Gen          int          `json:"gen"`
	Limit        int          `json:"limit"`
	Paused       *Pause       `json:"paused"`
	Base         *GitRef      `json:"base"`
	Integration  *Integration `json:"integration"`
	Terms        TermsSeen    `json:"terminals"`
	Lead         LeadSeen     `json:"lead,omitzero"`
	Findings     []Finding    `json:"findings,omitempty"`
	Scan         ScanSeen     `json:"scan,omitzero"`
	// StuckFlagged: stuck was raised for this episode, as quiet_flagged is
	// for an attempt.
	StuckFlagged bool `json:"stuck_flagged,omitempty"`
	// Programs is every version of the program that has swept this run: one
	// more while the run is open is an upgrade, and raises "upgraded" once.
	Programs []string `json:"programs,omitempty"`
	// Mode is empty, or ModeFleets on an orchestrator's run: written by run
	// new from the person's setting of that moment and never changed. Fleets
	// are that run's fleets by name. Parent is set on a fleet's own run.
	Mode   string            `json:"mode,omitempty"`
	Fleets map[string]*Fleet `json:"fleets,omitempty"`
	Parent *Twin             `json:"parent,omitempty"`
	// Plan, Protect and Spend are the three guards of a job as its record
	// knows them (guard.go); each is empty while its setting is off.
	Plan    *Approval `json:"plan,omitempty"`
	Protect *Approval `json:"protect,omitempty"`
	Spend   Spend     `json:"spend,omitzero"`
}

// ModeFleets is the mode of a run whose tasks are groups handed to fleets.
const ModeFleets = "fleets"

// Fleet is one fleet of an orchestrator's run: the run that is the fleet,
// its leader's tool and model, the leader's pane and tab while one lives,
// the branch its work is collected on, and the groups handed to it. A fleet
// whose leader has ended while its run is open is parked.
type Fleet struct {
	Run     string    `json:"run"`
	Tool    string    `json:"tool"`
	Model   string    `json:"model,omitempty"`
	Pane    string    `json:"pane,omitempty"`
	Tab     string    `json:"tab,omitempty"`
	Branch  string    `json:"branch,omitempty"`
	Since   time.Time `json:"since"`
	Groups  []string  `json:"groups,omitempty"`
	EndedAt time.Time `json:"ended_at,omitzero"`
	// Used is the agent-hours of this fleet's runs that have ended, written
	// when a run ends and kept when the fleet is led again in a new one.
	Used time.Duration `json:"used,omitempty"`
	// Spend is the spending guard on this fleet alone.
	Spend Spend `json:"spend,omitzero"`
}

// Twin names the other half of something that lies in two runs of one
// project: on a fleet's run, the orchestrator's run and the fleet's name
// there; on a question, the run and the id of the question it became.
type Twin struct {
	Run   string `json:"run"`
	ID    string `json:"id,omitempty"`
	Fleet string `json:"fleet,omitempty"`
}

// ScanSeen is the overlap scan as the record knows it: Due since a report
// or an accept moved something, At when findings were last written down.
type ScanSeen struct {
	Due bool      `json:"due,omitempty"`
	At  time.Time `json:"at,omitzero"`
}

// LeadSeen is since when the lead agent has rested after work with nobody
// looking, and since when it has rested without waiting while events were
// unread. Each raises its item for the person a minute later. Unseen says
// that it has worked since it was last in front while it rested.
type LeadSeen struct {
	Unseen      bool      `json:"unseen,omitempty"`
	DoneSince   time.Time `json:"done_since,omitzero"`
	SilentSince time.Time `json:"silent_since,omitzero"`
}

// Binding names the pane that drives a run.
type Binding struct {
	Pane      string    `json:"pane"`
	Workspace string    `json:"workspace"`
	BoundAt   time.Time `json:"bound_at"`
}

type Pause struct {
	At time.Time `json:"at"`
	By string    `json:"by"`
}

type GitRef struct {
	Ref string `json:"ref"`
	OID string `json:"oid"`
}

// Integration is where accepted work is collected. Landed is the tip that was
// last landed, empty until then.
type Integration struct {
	Branch string `json:"branch"`
	Tip    string `json:"tip"`
	Landed string `json:"landed"`
}

// TermsSeen is when a sweep last had the terminals' picture, and which start
// of the keeper that picture was of: another Instance is a restart.
type TermsSeen struct {
	SeenAt   time.Time `json:"seen_at,omitzero"`
	Instance string    `json:"instance,omitempty"`
}

type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskReady     TaskStatus = "ready"
	TaskRunning   TaskStatus = "running"
	TaskReview    TaskStatus = "review"
	TaskDone      TaskStatus = "done"
	TaskFailed    TaskStatus = "failed"
	TaskCancelled TaskStatus = "cancelled"
)

// MaxFailures is the number of failed or exited attempts after which a task
// stays failed until it is reset.
const MaxFailures = 3

type Task struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Title        string     `json:"title"`
	Brief        string     `json:"brief"`
	After        []string   `json:"after,omitempty"`
	Owns         []string   `json:"owns,omitempty"`
	Check        string     `json:"check"`
	BrowserCheck bool       `json:"browser_check,omitempty"`
	Placement    string     `json:"placement"`
	Fleet        string     `json:"fleet,omitempty"` // a group's fleet: Placement is then PlaceFleet
	Agent        string     `json:"agent,omitempty"`
	Model        string     `json:"model,omitempty"`
	Status       TaskStatus `json:"status"`
	Failures     int        `json:"failures"`
	Attempts     []string   `json:"attempts,omitempty"`
	Decisions    []Decision `json:"decisions,omitempty"`
	Worktree     *Worktree  `json:"worktree"`
	// Synced counts the times the task was sent to bring its copy up to
	// date; SyncBrief is the last brief it was sent, a file under the run's
	// msg folder, kept for the prompt of a later attempt until it is done.
	Synced    int       `json:"synced,omitempty"`
	SyncBrief string    `json:"sync_brief,omitempty"`
	Accepted  *Accepted `json:"accepted"`
	// PR is the task's own pull request in pr-per-task mode, from the
	// accept that opened it.
	PR *PullRequest `json:"pr,omitempty"`
	// CheckFails counts the checks of this task that failed running; a pass
	// sets it to nought.
	CheckFails int       `json:"check_fails,omitempty"`
	SeenAt     time.Time `json:"seen_at,omitzero"`
	CreatedAt  time.Time `json:"created_at"`
	SettledAt  time.Time `json:"settled_at,omitzero"`
}

// The placements; a folder given with --cwd is PlaceCwd followed by it. A
// task placed in a fleet is a group: it has no folder of its own, and its
// work is the fleet's branch.
const (
	PlaceWorktree = "worktree"
	PlaceShared   = "shared"
	PlaceCwd      = "cwd:"
	PlaceFleet    = "fleet"
	CheckNone     = "none"
)

// PullRequest is a task's own pull request. State is PRWatching while its
// checks are watched, PRFailed after ci_failed until the task is accepted
// again, PRAsked after base_moved, and PRMerged at the end; Head is the
// commit that was pushed.
type PullRequest struct {
	Number   int       `json:"number"`
	URL      string    `json:"url"`
	Head     string    `json:"head"`
	State    string    `json:"state"`
	OpenedAt time.Time `json:"opened_at"`
}

const (
	PRWatching = "watching"
	PRFailed   = "failed"
	PRAsked    = "asked"
	PRMerged   = "merged"
)

type Decision struct {
	Question string    `json:"question"`
	Answer   string    `json:"answer"`
	At       time.Time `json:"at"`
}

// How a task was accepted, and the two outcomes of a worker's report.
const (
	AcceptCheck  = "check"
	AcceptByHand = "by-hand"
	ReportDone   = "done"
	ReportFailed = "failed"
)

// Lead is the word that addresses the lead agent where a task is named; no
// task may have it as its id or its name.
const Lead = "lead"

// Accepted says how a task became done: How is AcceptCheck or AcceptByHand.
type Accepted struct {
	How    string    `json:"how"`
	Note   string    `json:"note,omitempty"`
	Commit string    `json:"commit,omitempty"`
	At     time.Time `json:"at"`
}

type AttemptState string

const (
	AttemptStarting    AttemptState = "starting"
	AttemptWorking     AttemptState = "working"
	AttemptAsked       AttemptState = "asked"
	AttemptReported    AttemptState = "reported"
	AttemptChecking    AttemptState = "checking"
	AttemptAccepted    AttemptState = "accepted"
	AttemptFailed      AttemptState = "failed"
	AttemptExited      AttemptState = "exited"
	AttemptStopped     AttemptState = "stopped"
	AttemptStartFailed AttemptState = "start_failed"
)

// Live reports whether an attempt in this state still occupies an agent.
func (s AttemptState) Live() bool {
	switch s {
	case AttemptStarting, AttemptWorking, AttemptAsked, AttemptReported, AttemptChecking:
		return true
	}
	return false
}

type Attempt struct {
	ID         string       `json:"id"`
	Task       string       `json:"task"`
	N          int          `json:"n"`
	RetryOf    string       `json:"retry_of,omitempty"`
	State      AttemptState `json:"state"`
	StateSince time.Time    `json:"state_since"`
	Round      int          `json:"round"`
	TokenHash  string       `json:"token_hash"`
	Agent      Agent        `json:"agent"`
	Place      Place        `json:"place"`
	Seen       AgentSeen    `json:"seen"`
	Progress   *Progress    `json:"progress"`
	Gated      bool         `json:"gated"`
	Check      *CheckResult `json:"check"`
	Report     *Report      `json:"report"`
	Accept     *Accepting   `json:"accept"`
	Exit       *Exit        `json:"exit"`
	Mail       []Mail       `json:"mail,omitempty"`
	StartedAt  time.Time    `json:"started_at"`
	BackAt     time.Time    `json:"back_at,omitzero"` // when its result was last sent back
	EndedAt    time.Time    `json:"ended_at,omitzero"`
	Temp       string       `json:"temp,omitempty"` // on a server, the temp folder of its own that start made
	// Leads is the fleet's run this attempt is the leader of. Several live
	// attempts of one orchestrator's run can share a place and a Leads: one
	// leader, several groups.
	Leads string `json:"leads,omitempty"`
	// Rested is how long it stood under holds that have ended: Spent leaves
	// it out. Whoever ends a hold adds to it, in one place (Rules.Hold).
	Rested time.Duration `json:"rested,omitzero"`
}

// Agent is the program in the attempt's pane. Name is what the keeper knows it by and
// Session its own conversation id as the keeper reports it.
type Agent struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Model   string `json:"model,omitempty"`
	Session string `json:"session,omitempty"`
}

// Place is where the attempt runs. Tab and Pane survive a restart of the keeper;
// Terminal changes at every restart.
type Place struct {
	Tab        string `json:"tab"`
	Pane       string `json:"pane"`
	Terminal   string `json:"terminal"`
	Workspace  string `json:"workspace"`
	Cwd        string `json:"cwd"`
	OpenedByUs bool   `json:"opened_by_us"`
}

type Liveness string

const (
	Live         Liveness = "live"
	Unverifiable Liveness = "unverifiable"
	Gone         Liveness = "exited"
)

// AgentSeen is the agent state as the terminals last showed it: a cache, never the truth.
type AgentSeen struct {
	Status       string    `json:"status"`
	At           time.Time `json:"at,omitzero"`
	Liveness     Liveness  `json:"liveness"`
	LastWorking  time.Time `json:"last_working,omitzero"`
	BlockedSince time.Time `json:"blocked_since,omitzero"`
	GoneSince    time.Time `json:"gone_since,omitzero"`
	QuietFlagged bool      `json:"quiet_flagged,omitempty"`
	StaleFlagged bool      `json:"stale_flagged,omitempty"`
	// OvertimeFlagged and StillFlagged: the overtime event was raised for
	// this attempt, and still_running for this pause.
	OvertimeFlagged bool `json:"overtime_flagged,omitempty"`
	StillFlagged    bool `json:"still_flagged,omitempty"`
	// What the stuck detector keeps (guard.go): since when the verdict
	// holds and by which signs (their names with commas between: the block
	// stays comparable), how far up it was told (0 to 3) and when
	// last, the context figure last seen and how often it fell by CtxDrop
	// with no new progress note between.
	StuckSince time.Time `json:"stuck_since,omitzero"`
	StuckWhy   string    `json:"stuck_why,omitempty"`
	StuckTold  int       `json:"stuck_told,omitempty"`
	StuckAt    time.Time `json:"stuck_at,omitzero"`
	Ctx        int       `json:"ctx,omitempty"`
	Emptied    int       `json:"emptied,omitempty"`
}

type Progress struct {
	Pct  int       `json:"pct"`
	Note string    `json:"note"`
	At   time.Time `json:"at"`
	// Same counts the notes that repeated the last one that said anything
	// new, figure and words: 2 is the same note three times.
	Same int `json:"same,omitempty"`
}

// CheckResult is how the last check of an attempt ended, with the last line
// of its log. Took is how long the command ran, in whole seconds.
type CheckResult struct {
	OK   bool          `json:"ok"`
	At   time.Time     `json:"at"`
	Tail string        `json:"tail"`
	Took time.Duration `json:"took,omitzero"`
}

// Report is the worker's own word: Outcome is ReportDone or ReportFailed.
type Report struct {
	Outcome  string         `json:"outcome"`
	Summary  string         `json:"summary"`
	At       time.Time      `json:"at"`
	Evidence []EvidenceFile `json:"evidence,omitempty"`
}

// EvidenceFile is one file of a browser check. Kind is "png", "jpeg" or
// "webp", judged by the file's first bytes, and empty for anything else,
// which a page offers as a download and never shows.
type EvidenceFile struct {
	Name string    `json:"name"`
	Size int64     `json:"size"`
	At   time.Time `json:"at"`
	Kind string    `json:"kind,omitempty"`
}

// Accepting is set while an accept runs.
type Accepting struct {
	StartedAt   time.Time `json:"started_at"`
	CheckedOID  string    `json:"checked_oid,omitempty"`
	ExpectedTip string    `json:"expected_tip,omitempty"`
}

// Exit causes.
const (
	ExitReported    = "reported"
	ExitStopped     = "stopped"
	ExitClosedByUs  = "closed-by-us"
	ExitExited      = "exited"
	ExitStartFailed = "start-failed"
)

type Exit struct {
	Cause string    `json:"cause"`
	At    time.Time `json:"at"`
}

// Mail is a message from the orchestrator to an attempt. Body is the path of
// a file under msg/ when the message is longer than one line.
type Mail struct {
	Seq      int       `json:"seq"`
	At       time.Time `json:"at"`
	Text     string    `json:"text"`
	Body     string    `json:"body,omitempty"`
	ReadAt   time.Time `json:"read_at,omitzero"`
	NudgedAt time.Time `json:"nudged_at,omitzero"`
}

type Inbox struct {
	Events      []Event `json:"events"`
	Acked       int     `json:"acked"`
	Outstanding *Batch  `json:"outstanding"`
	// Say is the person's notes that wait to be typed into the lead agent's tab.
	Say []Note `json:"say,omitempty"`
}

// Batch is a delivery handed out by wait and not yet acknowledged.
type Batch struct {
	ID   string `json:"id"`
	From int    `json:"from"`
	To   int    `json:"to"`
}

// MaxBatch is the most events one wait hands out.
const MaxBatch = 50

type Event struct {
	Seq     int            `json:"seq"`
	At      time.Time      `json:"at"`
	Kind    string         `json:"kind"`
	Task    string         `json:"task,omitempty"`
	Attempt string         `json:"attempt,omitempty"`
	Text    string         `json:"text,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

// EventKinds is every kind an inbox event can have.
var EventKinds = []string{
	"done", "failed", "question", "exited", "start_failed", "blocked", "quiet",
	"stale", "overtime", "check_failed", "stuck", "overlap", "scope",
	"base_moved", "setup_failed", "land_failed", "rejected", "human",
	"answered", "paused", "resumed", "still_running", "upgraded", "together",
	"untrusted", "protected", "budget", "merged", "ci_failed",
}

// Question kinds, forms, addressees and states.
const (
	KindAsk  = "ask"
	KindNeed = "need"

	FormQuestion = "question"
	FormChoice   = "choice"
	FormSignoff  = "signoff"
	FormTodo     = "todo"

	ForOrchestrator = "orchestrator"
	ForHuman        = "human"

	FromLead = "lead"
	FromTool = "tool"

	QuestionOpen     = "open"
	QuestionAnswered = "answered"
	QuestionUsed     = "used"
	QuestionClosed   = "closed"
)

// The causes of an item the tool raises itself; it clears when its cause does.
const (
	CausePrompt     = "prompt"
	CauseLeadUnread = "lead_unread"
	CauseLeadSilent = "lead_silent"
	CauseTogether   = "together"
	CauseTrust      = "trust"
	CausePair       = "pair"
	// Phase 6. The first four and CauseSetting are answered by the person
	// and nobody else (PersonOnly); a stuck item is a to-do like any other.
	CausePlan    = "plan"
	CauseProtect = "protect"
	CauseBudget  = "budget"
	CauseSetting = "setting"
	CauseStuck   = "stuck"
)

// Question is a worker's question to the lead agent, or an item for the human.
// From is FromLead, FromTool or the asking attempt's id.
type Question struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Form       string    `json:"form"`
	For        string    `json:"for"`
	From       string    `json:"from"`
	Cause      string    `json:"cause,omitempty"`
	Task       string    `json:"task,omitempty"`
	Attempt    string    `json:"attempt,omitempty"`
	Holds      bool      `json:"holds,omitempty"`
	Urgent     bool      `json:"urgent,omitempty"`
	Text       string    `json:"text"`
	Options    []string  `json:"options,omitempty"`
	State      string    `json:"state"`
	CreatedAt  time.Time `json:"created_at"`
	ShownAt    time.Time `json:"shown_at,omitzero"`
	Answer     string    `json:"answer,omitempty"`
	AnsweredAt time.Time `json:"answered_at,omitzero"`
	AnsweredBy *Origin   `json:"answered_by"`
	SettlesAt  time.Time `json:"settles_at,omitzero"`
	UsedAt     time.Time `json:"used_at,omitzero"`
	Earlier    []Undone  `json:"earlier,omitempty"`
	// Up, on a fleet's question, is the question it became in the
	// orchestrator's run; Down, on that one, is the way back.
	Up   *Twin `json:"up,omitempty"`
	Down *Twin `json:"down,omitempty"`
	// Tasks is what a plan's item showed, by id: an approval covers these
	// and no task added later. Set is the change an agent asked for, on an
	// item of CauseSetting.
	Tasks []string `json:"tasks,omitempty"`
	Set   *SetAsk  `json:"set,omitempty"`
	// Fleet, on a to-do of CauseStuck in an orchestrator's run, is the fleet
	// whose worker it is about: Attempt is then an attempt of that fleet's
	// run and of no other, and Task the fleet's group.
	Fleet string `json:"fleet,omitempty"`
}

// Origin is the kind of caller that did something and where the command says
// it came from.
type Origin struct {
	Caller CallerKind `json:"caller"`
	Where  string     `json:"where,omitempty"`
}

// Where a human action came from.
const (
	WherePane    = "pane"
	WherePage    = "page"
	WherePhone   = "phone"
	WhereTyped   = "typed"
	WhereRelayed = "relayed"
)

// Undone is an answer that was taken back; nothing a person did is dropped.
type Undone struct {
	Answer   string    `json:"answer"`
	At       time.Time `json:"at"`
	By       *Origin   `json:"by"`
	UndoneAt time.Time `json:"undone_at"`
}

// The fixed beginnings of the answers the forms accept.
const (
	AnswerOther    = "other:"
	AnswerApprove  = "approve"
	AnswerSendBack = "send back:"
	AnswerDone     = "done"
	AnswerCannot   = "cannot:"
)

// How the setup of a worktree went; pending is never written.
const SetupNone, SetupOK, SetupFailed, SetupUntrusted = "none", "ok", "failed", "untrusted"

// Worktree is a task's own copy of the code. Setup is one of the words above.
type Worktree struct {
	Path       string    `json:"path"`
	Branch     string    `json:"branch"`
	BaseRef    string    `json:"base_ref"`
	BaseOID    string    `json:"base_oid"`
	Slot       int       `json:"slot"`
	Setup      string    `json:"setup"`
	KeepBranch bool      `json:"keep_branch,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	RemovedAt  time.Time `json:"removed_at,omitzero"`
}
