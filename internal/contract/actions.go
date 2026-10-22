package contract

import "slices"

// Action is one row of the action table: the only things a click or a key in
// a pane, or a button on the page, may run. Run is the command line, with
// {id}, {task}, {file}, {name}, {key} and {value} filled in from the view
// model and a private temp file; empty means only the screen changes. A row
// of the left section is about one run of one project among several: what
// it runs gets --root and --run of that row (SideRow) added. How is
// one of the How constants. Key is the single key in our panes and Prefix the
// key after the command key that works from anywhere.
type Action struct {
	ID, Label   string
	Run         []string
	How         string
	Key, Prefix string
	Confirm     bool
}

const (
	HowChild  = "child"  // started by the pane or the page, which waits for it
	HowTab    = "tab"    // a child too, which moves itself into a tab of its own: it can take minutes
	HowPoint  = "point"  // a fixed pointer typed into the lead agent's tab
	HowResize = "resize" // the terminals' own resize, then set fleet.width
	HowScreen = "screen" // nothing is run
)

var Actions = []Action{
	{ID: "answer", Label: "Answer", Run: []string{"answer", "{id}", "--file", "{file}", "--human"}, How: HowChild},
	{ID: "undo", Label: "Undo", Run: []string{"answer", "{id}", "--undo", "--human"}, How: HowChild, Key: "u"},
	{ID: "carry-on", Label: "Tell it to carry on", Run: []string{string(PointCarryOn)}, How: HowPoint},
	{ID: "ask-lead", Label: "Ask the lead", Run: []string{string(PointQuestion)}, How: HowPoint},
	{ID: "note-lead", Label: "Send the lead agent a note", Run: []string{"tell", "lead", "--file", "{file}", "--human"}, How: HowChild},
	{ID: "accept", Label: "Accept", Run: []string{"accept", "{task}", "--human"}, How: HowTab},
	{ID: "accept-by-hand", Label: "Accept by hand", Run: []string{"accept", "{task}", "--by-hand", "--file", "{file}", "--human"}, How: HowChild},
	{ID: "send-back", Label: "Send back", Run: []string{"reject", "{task}", "--file", "{file}", "--human"}, How: HowChild},
	{ID: "stop", Label: "Stop", Run: []string{"stop", "{task}", "--human"}, How: HowChild, Confirm: true},
	{ID: "start", Label: "Start", Run: []string{"start", "{task}", "--human"}, How: HowTab},
	{ID: "retry", Label: "Try again", Run: []string{"start", "{task}", "--retry", "--human"}, How: HowTab},
	{ID: "note", Label: "Send a note or a link", Run: []string{"tell", "{task}", "--file", "{file}", "--human"}, How: HowChild},
	{ID: "close", Label: "Close the tab", Run: []string{"close", "{task}", "--human"}, How: HowChild},
	{ID: "stop-all", Label: "Stop all", Run: []string{"pause", "--everywhere", "--human"}, How: HowChild, Key: "S", Prefix: "shift+a", Confirm: true},
	{ID: "resume", Label: "Resume", Run: []string{"resume", "--everywhere", "--human"}, How: HowChild, Key: "r"},
	{ID: "dnd", Label: "Do not disturb", Run: []string{"set", "dnd", "{value}"}, How: HowChild, Key: "d", Prefix: "shift+m"},
	{ID: "mute", Label: "Mute", Run: []string{"set", "mute", "{value}"}, How: HowChild, Key: "m", Prefix: "m"},
	{ID: "theme", Label: "Next colour scheme", Run: []string{"set", "theme", "{value}"}, How: HowChild, Key: "t"},
	{ID: "set", Label: "Settings", Run: []string{"set", "{key}", "{value}"}, How: HowChild},
	{ID: "catchup", Label: "Catch me up", Run: []string{"catchup"}, How: HowChild, Key: "c", Prefix: "u"},
	{ID: "team", Label: "Run a team...", Run: []string{"team", "run", "{name}", "--goal", "--file", "{file}", "--human"}, How: HowChild},
	{ID: "go", Label: "Go there", Run: []string{"jump", "{task}"}, How: HowChild, Key: "enter"},
	{ID: "fleet", Label: "Fleet on and off", Run: []string{"ui", "fleet", "{value}"}, How: HowChild, Key: "f", Prefix: "f"},
	{ID: "actions", Label: "Action pane on and off", Run: []string{"ui", "actions", "{value}"}, How: HowChild},
	{ID: "to-actions", Label: "Go to the action pane", Run: []string{"ui", "actions", "focus"}, How: HowChild, Key: "a", Prefix: "a"},
	{ID: "narrower", Label: "Narrower", How: HowResize, Key: "["},
	{ID: "wider", Label: "Wider", How: HowResize, Key: "]"},
	{ID: "menu", Label: "Actions", Run: []string{"ui", "menu"}, How: HowScreen, Key: "/", Prefix: "space"},
	{ID: "fold", Label: "Fold or unfold", How: HowScreen, Key: "x"},
	{ID: "show", Label: "Show", How: HowScreen},
	{ID: "keys", Label: "All keys", How: HowScreen, Key: "?"},
	{ID: "forget-phone", Label: "Forget a phone", Run: []string{"dash", "phone", "forget", "{id}"}, How: HowChild},
	{ID: "phone-yes", Label: "Approve a phone", Run: []string{"dash", "phone", "approve", "{id}"}, How: HowChild},
	{ID: "phone-no", Label: "Refuse a phone", Run: []string{"dash", "phone", "refuse", "{id}"}, How: HowChild},
}

// SideActions are the rows phase 5 adds: what the left section offers on a
// row's mark and at its foot. A fleet is parked and written to through its
// group. The three keys after the command key that go with the last three
// (s, g and b) are the engine's own and not among the seven init --keys
// writes. The list of every action shows these rows once its pane can run
// them; until then they are in no list.
var SideActions = []Action{
	{ID: "park", Label: "Park the fleet", Run: []string{"stop", "{task}", "--human"}, How: HowChild, Confirm: true},
	{ID: "note-leader", Label: "Send its leader a note", Run: []string{"tell", "{task}", "--file", "{file}", "--human"}, How: HowChild},
	{ID: "settings", Label: "The settings screen", Run: []string{"ui", "settings"}, How: HowScreen},
	{ID: "side-keys", Label: "Go to the left section", How: HowScreen},
	{ID: "side-fold", Label: "Fold the left section", How: HowScreen},
	{ID: "workspace", Label: "Open a project", How: HowScreen},
	// Phase 6: the log as a file, and what two of the tool's items offer
	// beside their answers. A stuck item's three are go, stop and note-leader.
	{ID: "export", Label: "Export the log of this job", Run: []string{"log", "--export"}, How: HowChild},
	{ID: "show-changes", Label: "Show the changes", Run: []string{"show", "{task}", "--diff"}, How: HowTab},
	{ID: "open-plan", Label: "Open the plan", Run: []string{"graph"}, How: HowTab},
}

// OfferActions are the rows phase 7 adds: what a menu at the pointer holds
// beside the work's own rows, and nothing else may be run from a menu
// (OfferRows says which on what). Each is about the one thing the menu was
// opened for, so they are in no list of every action. A menu's rows are
// these labels and no name: a name stands in a question and in the typing
// line only. The typed name goes to the command by a private file, and to
// the keeper as the call's own field. The three of HowScreen are calls of
// the keeper: TabRename, SpaceRename and TabClose, which asks first where
// an agent is in the tab. "remove-space" is run once for every open job of
// the project, and when each ended well SpaceClose follows; it deletes
// nothing, by having no code that does.
var OfferActions = []Action{
	{ID: "rename", Label: "Rename…", Run: []string{"task", "edit", "{task}", "--name", "--file", "{file}", "--human"}, How: HowChild},
	{ID: "rename-tab", Label: "Rename…", How: HowScreen},
	{ID: "rename-space", Label: "Rename…", How: HowScreen},
	{ID: "close-tab", Label: "Close tab", How: HowScreen},
	{ID: "stop-close", Label: "Stop agent & close", Run: []string{"stop", "{task}", "--human"}, How: HowChild, Confirm: true},
	{ID: "remove-space", Label: "Remove from list", Run: []string{"stop", "--all", "--human"}, How: HowChild, Confirm: true},
}

// The kinds of thing a menu opens for: the third word of `ui run offer`.
const OfferTab, OfferAgent, OfferFleet, OfferSpace = "tab", "agent", "fleet", "space"

// FilesStay is the dim line under "Remove from list", which is no row.
const FilesStay = "(your files stay)"

// OfferRows are the first rows of a menu, before the line and the work's own
// (SideRow.Actions): by what the menu is for, and by whether an agent of
// ours is at work there (for a fleet: whether it is not parked). A fleet is
// not renamed, and the tabs of no project have no name to give: other says
// the workspace is that one. tab says the thing has a tab: without one
// nothing is closed, an agent at work is still stopped, and a tab's own
// menu holds nothing. Only what applies is offered. With one of
// these in a menu, "stop" and "close" are not among the work's own rows,
// which are then: answer, accept, send-back, note, retry, note-lead.
func OfferRows(kind string, working, other, tab bool) []string {
	switch {
	case kind == OfferFleet && working:
		return []string{"park", "note-leader"}
	case kind == OfferFleet:
		return []string{"retry"}
	case kind == OfferSpace && other:
		return []string{"remove-space"}
	case kind == OfferSpace:
		return []string{"rename-space", "remove-space"}
	case kind == OfferAgent && working:
		return []string{"rename", "stop-close"}
	case kind == OfferAgent && tab:
		return []string{"rename", "close-tab"}
	case kind == OfferAgent:
		return []string{"rename"}
	case tab:
		return []string{"rename-tab", "close-tab"}
	}
	return nil
}

// The words of a menu's questions and of the box they stand in (21.9). A
// name is cleaned and stands after our own words; N is a count. KeepFirst
// are the rows whose question opens on the button that does nothing.
const (
	AskCloseAgent = "An agent is in this tab. Close it?"
	AskCloseLead  = "This is the orchestrator's tab. Its %d agents go on without it. Close it?"
	AskStopClose  = "Stop %s and close its tab? Its work stays in its own copy of the code."
	AskRemove     = "Remove %s from the list? Its %d agents are stopped and its tabs closed, the orchestrator's chat too. The job stays, stopped. No folder or file is deleted."
	AskRemoveBare = "Remove %s from the list? Its tabs are closed. No folder or file is deleted."
	RenameTitle   = "Rename"
	RenameKeys    = "enter keep · esc leave"
	ButtonKeep    = "[ Keep ]"
	ButtonClose   = "[ Close ]"
	ButtonStop    = "[ Stop & close ]"
	ButtonRemove  = "[ Remove ]"
	// NameTaken is what a rename is refused with: the name, and the
	// workspace whose tab has it.
	NameTaken = "The name %q is taken by an agent in %s. Give another."
	// JobOpenHere is the agents panel's one dim line in a workspace opened
	// on an open job that no agent is bound to.
	JobOpenHere = "a job is open here · tell your agent to take it over"
	NoFolder    = "folder not found"
)

var KeepFirst = []string{"remove-space", "close-tab"}

// FindAction is a row of any of the three tables by its id.
func FindAction(id string) (Action, bool) {
	for _, a := range slices.Concat(Actions, SideActions, OfferActions) {
		if a.ID == id {
			return a, true
		}
	}
	return Action{}, false
}

// Never lists what neither the panes nor the page may ever run.
var Never = []string{"land", "trust", "run rm", "run close --abandon", "task cancel", "report", "progress", "ask", "mail"}

// Pointer is one of the fixed one-line texts the tool may type into an
// agent's tab. %s takes an id or a count and nothing else.
type Pointer string

const (
	PointEvents   Pointer = "whaleshark: %s events waiting. Run: whaleshark wait"
	PointAnswer   Pointer = "whaleshark: 1 answer waiting. Run: whaleshark wait"
	PointQuestion Pointer = "whaleshark: a question is waiting. Run: whaleshark wait"
	PointCarryOn  Pointer = "whaleshark: questions for the human go in the action pane (`whaleshark need`). Then run: whaleshark wait"
	PointAnswered Pointer = "Your question %s was answered. Run: whaleshark ask --resume %[1]s"
	PointMail     Pointer = "whaleshark: a message is waiting. Run: whaleshark mail"
	PointFleet    Pointer = "whaleshark: new work for your fleet. Run: whaleshark mail"
	PointResumed  Pointer = "whaleshark: the person says go on: the hold is over. Carry on with your task now; first run: whaleshark mail"
	PointLeadOn   Pointer = "whaleshark: the person says go on: the hold is over. Carry on now; run: whaleshark wait"
)

// Gate is how the gate hook answers one kind of agent while the person has
// pressed Stop all. Answer is printed on standard output with the reason put
// in twice. Holds is true only for a kind whose answer was tried on a real
// agent and both refused the step and ended the turn; every other kind is
// stopped cooperatively and shown as still running.
type Gate struct {
	Answer string
	Holds  bool
}

var Gates = map[string]Gate{
	"claude": {Holds: true, Answer: `{"continue":false,"stopReason":%q,"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":%q}}`},
	"codex":  {Answer: `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":%[2]q}}`},
}

// What a stopped agent is told: by the gate, and by its own next command when
// it has no gate.
const (
	PausedReason = "the person has paused all work"
	PausedReply  = "The person has paused all work. Stop now, change nothing more, and wait to be told."
	PausedLine   = "paused: stop now and wait to be told"
)

// ValueRule is one rule of the validator every value passes before anything
// is touched, and again when it is loaded from disk.
type ValueRule struct {
	Value   string
	Max     int    // characters; 0 is no limit
	Words   int    // 0 is no limit
	Pattern string // the value must match; empty is no pattern
	OneLine bool   // no line breaks and no control characters
	Inside  bool   // a relative path with no "..", no leading "-", still inside the project once links are resolved
}

var ValueRules = []ValueRule{
	{Value: "id", Max: 16, Pattern: `^[A-Za-z0-9_][A-Za-z0-9_-]*$`},
	{Value: "title", Max: 80, OneLine: true},
	{Value: "name", Max: 24, Words: 3, Pattern: `^\s*[^-\s]`, OneLine: true}, // no option, also once the spaces before it are gone
	{Value: "number", Max: 9, Pattern: `^[0-9]+$`},
	{Value: "attempt", Pattern: `^[A-Za-z0-9_][A-Za-z0-9_-]{0,15}\.[0-9]{1,9}$`},
	{Value: "pane", Max: 64, Pattern: `^[A-Za-z0-9][A-Za-z0-9:._-]*$`},
	{Value: "agent", Max: 32, Pattern: `^[a-z][a-z0-9_-]*$`},
	{Value: "model", Max: 64, Pattern: `^[^-]`, OneLine: true}, // an argument on the agent's command line: never the form of an option
	{Value: "slug", Pattern: `^[a-z0-9-]+$`},
	{Value: "path", Inside: true, OneLine: true},
	{Value: "text"},
}

// List splits what a flag that takes a list was given: at every comma that
// is not inside braces, so a pattern such as src/{a,b}/** stays whole. An
// empty part is kept, for the validator to refuse.
func List(values ...string) []string {
	var out []string
	for _, v := range values {
		depth, from := 0, 0
		for i, r := range v {
			switch {
			case r == '{':
				depth++
			case r == '}' && depth > 0:
				depth--
			case r == ',' && depth == 0:
				out, from = append(out, v[from:i]), i+1
			}
		}
		out = append(out, v[from:])
	}
	return out
}
