// Package contract holds everything the other packages share: the shapes on
// disk, the view model, the interfaces between packages, and the command and
// action tables. Only a contract package edits it.
package contract

import "time"

// StateVersion is raised by every change to the shape of state.json.
const StateVersion = 1

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
	Herdr        HerdrSeen    `json:"herdr"`
	Lead         LeadSeen     `json:"lead,omitzero"`
	Findings     []Finding    `json:"findings,omitempty"`
}

// LeadSeen is since when the lead agent's own pane has shown "done" with
// nobody looking, and since when it has rested without waiting while events
// were unread. Each raises its item for the person a minute later.
type LeadSeen struct {
	DoneSince   time.Time `json:"done_since,omitzero"`
	SilentSince time.Time `json:"silent_since,omitzero"`
}

// Binding names the herdr pane that drives a run.
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

type HerdrSeen struct {
	SeenAt time.Time `json:"seen_at,omitzero"`
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
	Agent        string     `json:"agent,omitempty"`
	Model        string     `json:"model,omitempty"`
	Status       TaskStatus `json:"status"`
	Failures     int        `json:"failures"`
	Attempts     []string   `json:"attempts,omitempty"`
	Decisions    []Decision `json:"decisions,omitempty"`
	Worktree     *Worktree  `json:"worktree"`
	Accepted     *Accepted  `json:"accepted"`
	SeenAt       time.Time  `json:"seen_at,omitzero"`
	CreatedAt    time.Time  `json:"created_at"`
	SettledAt    time.Time  `json:"settled_at,omitzero"`
}

// The three placements; a folder given with --cwd is PlaceCwd followed by it.
const (
	PlaceWorktree = "worktree"
	PlaceShared   = "shared"
	PlaceCwd      = "cwd:"
	CheckNone     = "none"
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
	Herdr      AgentSeen    `json:"herdr"`
	Progress   *Progress    `json:"progress"`
	Gated      bool         `json:"gated"`
	Check      *CheckResult `json:"check"`
	Report     *Report      `json:"report"`
	Accept     *Accepting   `json:"accept"`
	Exit       *Exit        `json:"exit"`
	Mail       []Mail       `json:"mail,omitempty"`
	StartedAt  time.Time    `json:"started_at"`
	EndedAt    time.Time    `json:"ended_at,omitzero"`
}

// Agent is the program in the attempt's pane. Name is its herdr name and
// Session its own conversation id as herdr reports it.
type Agent struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Model   string `json:"model,omitempty"`
	Session string `json:"session,omitempty"`
}

// Place is where the attempt runs. Tab and Pane survive a restart of herdr;
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

// AgentSeen is the agent state as herdr last showed it: a cache, never the truth.
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
}

type Progress struct {
	Pct  int       `json:"pct"`
	Note string    `json:"note"`
	At   time.Time `json:"at"`
}

// CheckResult is how the last check of an attempt ended, with the last line of its log.
type CheckResult struct {
	OK   bool      `json:"ok"`
	At   time.Time `json:"at"`
	Tail string    `json:"tail"`
}

// Report is the worker's own word: Outcome is ReportDone or ReportFailed.
type Report struct {
	Outcome  string         `json:"outcome"`
	Summary  string         `json:"summary"`
	At       time.Time      `json:"at"`
	Evidence []EvidenceFile `json:"evidence,omitempty"`
}

type EvidenceFile struct {
	Name string    `json:"name"`
	Size int64     `json:"size"`
	At   time.Time `json:"at"`
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
	"untrusted",
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

// Worktree is a task's own copy of the code. Setup is one of none, pending,
// ok, failed, untrusted.
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
