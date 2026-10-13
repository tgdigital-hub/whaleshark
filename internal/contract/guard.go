package contract

import (
	"cmp"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Phase 6: what the guards, the stuck detector and an agent's `set` share.
// Each guard is a setting of the person's own file, read when it is used
// and never from a project's; with every one as it comes nothing here
// changes a run but the stuck detector, which tells and stops nothing.

// Approval is the person's yes to something exact, on a run with no parent.
// As Run.Plan: Approved is the tasks that were shown and approved, and Item
// the plan's open item. As Run.Protect: Seen is the fingerprint of the
// protected files as last collected and Files their paths, Print the
// fingerprint the person approved, and Item the open item. A landing needs
// Print to be the fingerprint of the tip.
type Approval struct {
	Approved []string  `json:"approved,omitempty"`
	Print    string    `json:"print,omitempty"`
	Seen     string    `json:"seen,omitempty"`
	Files    []string  `json:"files,omitempty"`
	Item     string    `json:"item,omitempty"`
	At       time.Time `json:"at,omitzero"`
	By       *Origin   `json:"by,omitempty"`
}

// Unplanned is the tasks of a start that the person has not approved yet.
func Unplanned(s *State, tasks []string) (lack []string) {
	for _, id := range tasks {
		if s.Run.Plan == nil || !slices.Contains(s.Run.Plan.Approved, id) {
			lack = append(lack, id)
		}
	}
	return lack
}

// Spend is the spending guard on a job, and on one fleet of it (a Fleet's
// own, kept in the job's record): the agent-hours the person granted beyond
// the setting, whether they lifted the limit, and whether four fifths and
// the limit were crossed. A held job's run is paused by HeldBy; a fleet's
// run is brought in line with its job's record by Rules.Hold.
type Spend struct {
	More   int  `json:"more,omitempty"`
	Off    bool `json:"off,omitempty"`
	Warned bool `json:"warned,omitempty"`
	Held   bool `json:"held,omitempty"`
}

// HeldBy is who a guard's pause is by, and HoldFile the file in a run's
// folder that the gate of every agent of that run looks for: there while
// the record says held, and written again by the sweep if it was removed.
const (
	HeldBy   = "the spending guard"
	HoldFile = "hold"
)

// HoldPath is a run's hold file, from what a tab's own variables say
// (EnvRoot, EnvRun): the gate asks for it without reading the record.
func HoldPath(root, run string) string {
	return filepath.Join(root, ProjectDir, "runs", run, HoldFile)
}

// Spent is the time agents were open in these runs: for each attempt its
// end, or now, or the moment its run was paused, less its start, less what
// it rested under earlier holds. An agent-hour is one hour of it; a leader
// counts, the person's own orchestrator is no attempt and does not. It is
// counted by pane, as the limits count agents: a leader that holds two
// groups at once is one agent in one tab, and its hours are counted once.
func Spent(now time.Time, runs ...*State) (spent time.Duration) {
	type span struct{ from, to time.Time }
	panes := map[string][]span{}
	for _, s := range runs {
		for id, a := range s.Attempts {
			end := now
			if !a.EndedAt.IsZero() {
				end = a.EndedAt
			} else if !a.State.Live() {
				end = a.StateSince
			}
			if p := s.Run.Paused; p != nil && p.At.Before(end) {
				end = p.At
			}
			if end = end.Add(-a.Rested); !a.StartedAt.IsZero() && end.After(a.StartedAt) {
				// An attempt with no pane on record is an agent of its own.
				pane := cmp.Or(a.Place.Pane, s.Run.ID+"/"+id)
				panes[pane] = append(panes[pane], span{a.StartedAt, end})
			}
		}
	}
	for _, spans := range panes {
		slices.SortFunc(spans, func(x, y span) int { return x.from.Compare(y.from) })
		var till time.Time
		for _, sp := range spans {
			if sp.from.Before(till) {
				sp.from = till
			}
			if sp.to.After(sp.from) {
				spent, till = spent+sp.to.Sub(sp.from), sp.to
			}
		}
	}
	return spent
}

// JobSpent is what a job has spent and what each fleet of it has: fleets is
// each fleet's run by the fleet's name. A run of a fleet that has ended
// counts by what the job's record carries of it (Fleet.Used), so a fleet
// that is given work again, in a new run, forgets nothing.
func JobSpent(now time.Time, s *State, fleets map[string]*State) (job time.Duration, each map[string]time.Duration) {
	runs, each := []*State{s}, map[string]time.Duration{}
	for name, f := range s.Run.Fleets {
		low := cmp.Or(fleets[name], &State{})
		if f.EndedAt.IsZero() {
			runs, each[name] = append(runs, low), f.Used+Spent(now, low)
			job += f.Used
		} else {
			each[name] = cmp.Or(f.Used, Spent(now, low)) // none kept: a record from before Used was
			job += each[name]
		}
	}
	return job + Spent(now, runs...), each
}

// HourWord is the unit after a figure of agent-hours: "1 agent-hour",
// "0.5 agent-hours", "2 agent-hours".
func HourWord(figure string) string {
	if figure == "1" {
		return "agent-hour"
	}
	return "agent-hours"
}

// SpendLimit is the agent-hours a job may use, or one fleet of it: the
// person's setting and what they granted. Unlimited holds no line: the
// guard is off, the setting is unlimited, or the person lifted it here.
func (c PersonConfig) SpendLimit(sp Spend, fleet bool) int {
	limit := c.Spend.Job
	if fleet {
		limit = c.Spend.Fleet
	}
	if !c.Spend.Guard || sp.Off || limit == Unlimited {
		return Unlimited
	}
	return limit + sp.More
}

// SpendWarned and SpendHeld are what Rules.Spend says it did, and the two
// figures of the guard: told at four fifths, and a grant is a fifth.
const (
	SpendWarned, SpendHeld = "warned", "held"
	SpendWarnAt, SpendMore = 0.8, 0.2
)

// The answers the tool's own items take besides AnswerApprove and
// AnswerSendBack: a setting's two, and the budget's three.
const (
	AnswerChange  = "change"
	AnswerLeave   = "leave"
	AnswerMore    = "more"
	AnswerNoLimit = "no limit"
)

// The refusals of phase 6, all exit 5.
const (
	RefusedPlanFirst  = "plan_first"
	RefusedProtected  = "protected"
	RefusedOverBudget = "over_budget"
	RefusedPersonOnly = "person_only"
)

// PersonOnly reports whether an item of this cause is answered by the
// person's own click or typed line and by nobody else: answer from an
// agent, and --relayed, are refused with RefusedPersonOnly.
func PersonOnly(cause string) bool {
	return slices.Contains([]string{CausePlan, CauseProtect, CauseBudget, CauseSetting}, cause)
}

// Sign is one sign of an agent going nowhere. A hard one alone is the
// verdict; a soft one needs a second beside it, or twice its time.
type Sign struct {
	Name, Words string
	Hard        bool
}

// Signs are the seven, in the order a line names them. A tool whose states
// are not exact is judged by overtime, same, check and back alone; a leader
// never by stale.
var Signs = []Sign{
	{"stale", "no progress note", false},
	{"quiet", "stopped without a word", false},
	{"overtime", "over time", false},
	{"same", "the same note repeated", false},
	{"forgot", "its memory emptied twice", false},
	{"check", "the same check failed 3 times", true},
	{"back", "sent back 3 times", true},
}

// The figures of the verdict, in one place: how often is "again and again",
// how many soft signs at once, how many times a soft sign's own span, by
// how many points a context figure falls when a tool empties its memory,
// and how much longer than quiet_seconds "still so" is. The three levels
// the verdict is told at, one climb apart.
const (
	StuckTimes = 3
	StuckSoft  = 2
	StuckTwice = 2
	CtxDrop    = 30
	QuietMore  = 10 * time.Minute

	ToldLeader, ToldOrchestrator, ToldPerson = 1, 2, 3
)

// StuckWhy words an attempt's signs (AgentSeen.StuckWhy) as every screen, event and item does:
// "no progress note · the same check failed 3 times".
func StuckWhy(why string) string {
	var words []string
	for _, sign := range Signs {
		if slices.Contains(strings.Split(why, ","), sign.Name) {
			words = append(words, sign.Words)
		}
	}
	return strings.Join(words, " · ")
}

// SetAsk is a change of one setting that an agent asked for and that waits
// for the person: the key as `set` takes it, and its value before and after.
type SetAsk struct {
	Key  string `json:"key"`
	From string `json:"from"`
	To   string `json:"to"`
}

// Owned reports whether one of the patterns covers a file: the file itself,
// a folder above it (with or without a closing /, /* or /**), a pattern of
// the shell's kind for a part of the path each, where two stars stand for
// any number of folders, or such a pattern for the file's last part alone.
// It is the one matcher: of --owns, of the scan and of the protected list.
// A pattern goes nowhere else: never to a shell, never to git.
func Owned(patterns []string, file string) bool {
	for _, p := range patterns {
		p = strings.TrimSuffix(strings.TrimSuffix(path.Clean(p), "/**"), "/*")
		last, _ := path.Match(p, path.Base(file))
		if p == "." || last || under(strings.Split(p, "/"), strings.Split(file, "/")) {
			return true
		}
	}
	return false
}

// Above reports whether a link at file stands where a folder of one of the
// patterns begins: what lies behind such a link lies under a protected path
// on disk, and no name of the list shows it. With two stars in front of a
// pattern that is any link.
func Above(patterns []string, file string) bool {
	for _, p := range patterns {
		p = strings.TrimSuffix(strings.TrimSuffix(path.Clean(p), "/**"), "/*")
		if above(strings.Split(p, "/"), strings.Split(file, "/")) {
			return true
		}
	}
	return false
}

func above(pattern, parts []string) bool {
	if len(parts) == 0 || len(pattern) == 0 || pattern[0] == "**" {
		return len(pattern) > 0
	}
	ok, _ := path.Match(pattern[0], parts[0])
	return ok && above(pattern[1:], parts[1:])
}

// under reports whether a pattern's parts cover the first parts of a path.
func under(pattern, parts []string) bool {
	switch {
	case len(pattern) == 0:
		return true
	case pattern[0] == "**":
		for i := range len(parts) + 1 {
			if under(pattern[1:], parts[i:]) {
				return true
			}
		}
		return false
	case len(parts) == 0:
		return false
	}
	ok, _ := path.Match(pattern[0], parts[0])
	return ok && under(pattern[1:], parts[1:])
}

// Protected is the person's list with the project's additions, while the
// setting is on: a project adds and never takes away, and needs no trust.
func Protected(c PersonConfig, p ProjectFile) []string {
	if !c.Protect.Files {
		return nil
	}
	return slices.DeleteFunc(slices.Concat(List(c.Protect.Paths), p.Protect.Also), func(pattern string) bool { return pattern == "" })
}
