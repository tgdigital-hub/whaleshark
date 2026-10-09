package contract

import (
	"strings"
	"time"
)

// ViewVersion is raised by every change to the shape of the view model.
const ViewVersion = 1

// Look is what a person sees a task as. The order of the constants is the
// one order every screen uses.
type Look string

const (
	LookNeedsYou    Look = "needs you"
	LookAtPrompt    Look = "at a prompt"
	LookFailed      Look = "failed"
	LookCheckFailed Look = "check failed"
	LookToCheck     Look = "to check"
	LookBuilding    Look = "building"
	LookAsking      Look = "asking"
	LookIdle        Look = "idle"
	LookPaused      Look = "paused"
	LookStopped     Look = "stopped"
	LookQueued      Look = "queued"
	LookDone        Look = "done"
)

// LookOf is one look's mark, its mark where the terminal cannot show that
// one, its colour name, and the section of the team list it is listed in.
type LookOf struct {
	Look                         Look
	Mark, Plain, Colour, Section string
}

// Looks is the words and marks, in the one order.
var Looks = []LookOf{
	{LookNeedsYou, "▲", "!", "needs", "NEEDS YOU"},
	{LookAtPrompt, "◆", "#", "blocked", "NEEDS YOU"},
	{LookFailed, "✖", "x", "failed", "FAILED"},
	{LookCheckFailed, "●", "*", "failed", "TO CHECK"},
	{LookToCheck, "●", "*", "check", "TO CHECK"},
	{LookBuilding, "◐", "~", "building", "BUILDING"},
	{LookAsking, "◐", "~", "building", "BUILDING"},
	{LookIdle, "◌", "?", "idle", "IDLE"},
	{LookPaused, "‖", "=", "paused", "PAUSED"},
	{LookStopped, "■", "-", "dim", "STOPPED"},
	{LookQueued, "○", ".", "dim", "QUEUED"},
	{LookDone, "✔", "v", "done", "DONE"},
}

// PlainMarks reports whether a printer must use the Plain marks: the person
// asked for them (WHALESHARK_MARKS=plain), or the terminal's locale does not
// say UTF-8. The page never needs them.
func PlainMarks(getenv func(string) string) bool {
	if m := getenv("WHALESHARK_MARKS"); m != "" {
		return m == "plain"
	}
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := strings.ToUpper(getenv(name)); v != "" {
			return !strings.Contains(v, "UTF-8") && !strings.Contains(v, "UTF8")
		}
	}
	return true
}

// Pale is the colour of a progress bar whose figure is old: the scheme's
// "building" half-way to its ground, so a scheme stays sixteen colours.
// With no colour the bar is drawn with BarPale in place of BarFull.
func Pale(colour, ground uint32) uint32 {
	var out uint32
	for shift := 0; shift < 24; shift += 8 {
		out |= ((colour>>shift&0xff + ground>>shift&0xff) / 2) << shift
	}
	return out
}

const (
	BarFull = "█"
	BarPale = "▓"
)

// The other words a card can carry in the place of its look's own.
const (
	WordStarting     = "starting"
	WordChecking     = "checking"
	WordFinishing    = "finishing a step"
	WordStillRunning = "still running"
	WordDoneByHand   = "done (by hand)"
)

// Colours is the sixteen names a colour scheme gives a colour to. A scheme
// is nothing else. The warning colour is "blocked".
var Colours = []string{
	"text", "dim", "frame", "accent", "needs", "blocked", "failed", "check",
	"building", "idle", "paused", "done", "bar_empty", "ctx_low", "ctx_mid", "ctx_high",
}

// The context bar turns from ctx_low to ctx_mid and to ctx_high at these figures.
const (
	CtxMid  = 60
	CtxHigh = 85
)

// ViewInput is everything the one view model is built from.
type ViewInput struct {
	Now    time.Time
	Caller CallerKind
	State  *State
	// Terms is the terminals' picture, nil when they cannot be reached.
	Terms *Snapshot
	// Ctx is each agent's context figure by pane id.
	Ctx    map[string]CtxFile
	UI     UIFile
	Limits ProjectFile
	// Elsewhere counts the person's other open runs that need them.
	Elsewhere int
	// Checked is when both sources of news last agreed with the screen; Swept
	// is the last sweep, and Watched says whether anybody is sweeping.
	Checked time.Time
	Swept   Swept
	Watched bool
	// All adds the cards nobody is working on: stopped, queued and done.
	All bool
	// Notes are what only the screen that asks knows about its own sources,
	// such as "slow updates"; they follow the builder's own on the age line.
	Notes []string
}

// View is the one description of the screen. The panes, the plain text, the
// page and the phone print it; none computes an order, a count, a word or a
// colour of its own.
type View struct {
	Version   int        `json:"version"`
	At        time.Time  `json:"at"`
	Caller    CallerKind `json:"caller"`
	Run       string     `json:"run"`
	Objective string     `json:"objective"`
	// Alerts are the lines on top of every screen, such as the lead agent
	// not listening or herdr not being reachable.
	Alerts []string `json:"alerts,omitempty"`
	Counts Counts   `json:"counts"`
	// Items is what waits for the person, what holds work up first, then
	// oldest first; Held are those Do not disturb keeps back, and Answered
	// the last answers, which can be undone until used.
	Items    []Item    `json:"items"`
	Held     []Item    `json:"held,omitempty"`
	Answered []Item    `json:"answered,omitempty"`
	Sections []Section `json:"sections"`
	Strip    Strip     `json:"strip"`
	Fresh    Fresh     `json:"fresh"`
}

// Counts are the figures of the headers and the last lines.
type Counts struct {
	Agents      int `json:"agents"`
	NeedYou     int `json:"need_you"`
	CheckFailed int `json:"check_failed"`
	ToCheck     int `json:"to_check"`
	Waiting     int `json:"waiting"`
	Holding     int `json:"holding"`
	DoneToday   int `json:"done_today"`
	Elsewhere   int `json:"elsewhere"`
}

// Section is one block of the team list: the cards that share a heading.
type Section struct {
	Name  string `json:"name"`
	Cards []Card `json:"cards"`
}

// Card is one agent the tool started: a mark, a name, a state, two bars and
// one line of news.
type Card struct {
	Task string `json:"task"`
	Name string `json:"name"`
	Look Look   `json:"look"`
	// Word is what stands beside the mark when it is not the look's own
	// word: "starting", "checking", "finishing a step", "still running".
	Word   string `json:"word,omitempty"`
	Colour string `json:"colour"`
	// Since is when the card took this look; screens print its age.
	Since time.Time `json:"since"`
	// Flag is the small mark after the name: something for the person is
	// about this agent and holds nothing up. Item is that item, or the one
	// that makes the card "needs you".
	Flag     bool      `json:"flag,omitempty"`
	Item     string    `json:"item,omitempty"`
	Unseen   bool      `json:"unseen,omitempty"`
	Progress int       `json:"progress"`
	Said     time.Time `json:"said,omitzero"`
	// Pale is a bar whose figure is old while the agent is still busy.
	Pale bool `json:"pale,omitempty"`
	// Ctx is how full the agent's context is; CtxKnown is false for "unknown".
	Ctx      int    `json:"ctx"`
	CtxKnown bool   `json:"ctx_known"`
	News     string `json:"news"`
	// Clash is the other task that changes the same file, and whether the
	// two changes would conflict or only meet.
	Clash   *Clash   `json:"clash,omitempty"`
	Tab     string   `json:"tab,omitempty"`
	Try     int      `json:"try"`
	Agent   string   `json:"agent,omitempty"`
	Next    []string `json:"next,omitempty"`    // the lines this caller may run, as plain text shows them after ">"
	Actions []string `json:"actions,omitempty"` // ids of the action table offered on this card
}

type Clash struct {
	Task      string `json:"task"`
	Name      string `json:"name"`
	File      string `json:"file"`
	Conflicts bool   `json:"conflicts"`
}

// Item is one thing that waits for the person, with its own way to answer it.
type Item struct {
	ID   string `json:"id"`
	Form string `json:"form"`
	Look Look   `json:"look"`
	Task string `json:"task,omitempty"`
	Name string `json:"name,omitempty"`
	// Source says who is asking, in the words every screen uses: "a question
	// from the worker", "sign-off asked by the lead agent".
	Source string    `json:"source"`
	Since  time.Time `json:"since"`
	Text   string    `json:"text"`
	// News is Text as the news of a row that begins with other words: its
	// first letter lower-cased unless the next is a capital too.
	News    string   `json:"news"`
	Buttons []Button `json:"buttons,omitempty"`
	// Line is true when the item is answered on a line to type in.
	Line   bool `json:"line,omitempty"`
	Holds  bool `json:"holds,omitempty"`
	Urgent bool `json:"urgent,omitempty"`
	// Answer and By are set once it is answered; Settles is when the answer
	// may first be used and Used when it was, after which Undo is refused.
	Answer  string    `json:"answer,omitempty"`
	By      *Origin   `json:"by,omitempty"`
	Settles time.Time `json:"settles,omitzero"`
	Used    time.Time `json:"used,omitzero"`
	// Next is what plain text shows after ">": the lines this caller may
	// run, or for a caller who may not answer, "waiting for the human".
	Next []string `json:"next,omitempty"`
}

// NextJoin is the word a printer puts between two lines of a Next list, given
// the first of the two: "then" after a line that only prepares the next (it
// shows, closes or lists), and "or" after any other, which is a choice.
func NextJoin(before string) string {
	verb, _, _ := strings.Cut(strings.TrimPrefix(before, "whaleshark "), " ")
	if verb == "show" || verb == "close" || verb == "task" {
		return "then"
	}
	return "or"
}

// Button is one way to answer an item. Answer is the text it gives; one that
// ends in a colon opens the typing line for the rest. Action is the row of
// the action table it runs when that is not "answer".
type Button struct {
	Label  string `json:"label"`
	Key    string `json:"key,omitempty"`
	Answer string `json:"answer,omitempty"`
	Action string `json:"action,omitempty"`
}

// Strip is the folded action pane: the four buttons and what they say.
type Strip struct {
	Paused       bool      `json:"paused,omitempty"`
	StillRunning int       `json:"still_running,omitempty"`
	DND          bool      `json:"dnd,omitempty"`
	DNDUntil     time.Time `json:"dnd_until,omitzero"`
	Mute         bool      `json:"mute,omitempty"`
	// Away is set when the person was last here more than ten minutes ago.
	Away      time.Time `json:"away,omitzero"`
	DoneAsYou int       `json:"done_as_you,omitempty"`
}

// Fresh is the age line. A printer shows the age of Checked, counted on its
// own clock, and STALE once that passes StaleAfter.
type Fresh struct {
	Checked time.Time `json:"checked"`
	// Notes are what the screen says about its sources: "slow updates",
	// "herdr: checking every 5 s", "herdr not reachable", and of the sweep
	// either "no sweep yet" or "nobody is sweeping".
	Notes []string `json:"notes,omitempty"`
}

const (
	StaleAfter = 10 * time.Second
	AwayAfter  = 10 * time.Minute
)

// Catchup is "while you were away": built from the record, the same words
// every time. Each line is a count and what it counts.
type Catchup struct {
	From, To      time.Time
	Waiting       CatchLine
	Finished      CatchLine
	CheckFailed   CatchLine
	Clashes       CatchLine
	WentIdle      CatchLine
	AnsweredLead  int
	DoneAsYou     CatchLine
	StillBuilding int
	Stopped       int
	Failed        int
}

type CatchLine struct {
	N    int
	Text string
}

// TaskView is one task in full: what to do next, what the worker says, the
// proof, then the background, newest try first.
type TaskView struct {
	Card   Card
	Run    string
	Says   string
	Check  string
	Result *CheckResult
	ByHand string
	Owns   []string
	Brief  string
	Tries  []Try
	Items  []Item
	Screen string
}

// Try is one attempt of a task, with the files it left and its history.
type Try struct {
	Attempt  string
	State    AttemptState
	Result   string
	CheckLog string
	Evidence []EvidenceFile
	History  []Event
}
