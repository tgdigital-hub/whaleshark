package contract

import "time"

// Rules is every legal change to a run, as pure functions on the record:
// no file, no herdr, no clock. Each either changes s and returns nil, or
// changes nothing and returns a *Refusal. Only the rules package implements it.
type Rules interface {
	// 6.2 Run.
	NewRun(id, objective string, limit int, base *GitRef, by Binding, now time.Time) *State
	CloseRun(s *State, abandon bool, now time.Time) error
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
	Progress(s *State, attempt string, pct int, note string, now time.Time) error
	// Report records a worker's report; a refused one is kept as a rejected event.
	Report(s *State, attempt, tokenHash, outcome, summary string, evidence []EvidenceFile, now time.Time) (already bool, err error)
	// Checking and Checked are the two locked steps of accept; a failed check
	// is stored on the attempt and raises check_failed.
	Checking(s *State, task string, c Checked, now time.Time) error
	Checked(s *State, task string, result CheckResult, how Accepted, now time.Time) (ready []string, err error)
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

	// 6.8 Messages down to an attempt.
	Tell(s *State, task, text, body string, now time.Time) (seq int, err error)
	ReadMail(s *State, attempt string, now time.Time) ([]Mail, error)
	Pointed(s *State, attempt string, seq int, now time.Time) error

	// 6.7 Worktree: nil removes the record.
	SetWorktree(s *State, task string, w *Worktree, now time.Time) error
}

// TaskEdit is what task edit may replace; a nil field is left as it is.
type TaskEdit struct {
	Brief, Check *string
	Owns, After  *[]string
}
