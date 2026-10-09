package contract

// Action is one row of the action table: the only things a click or a key in
// a pane, or a button on the page, may run. Run is the command line, with
// {id}, {task}, {file}, {name}, {key} and {value} filled in from the view
// model and a private temp file; empty means only the screen changes. How is
// one of the How constants. Key is the single key in our panes and Prefix the
// key after herdr's prefix that works from anywhere.
type Action struct {
	ID, Label   string
	Run         []string
	How         string
	Key, Prefix string
	Confirm     bool
}

const (
	HowChild  = "child"  // started by the pane or the page, which waits for it
	HowTab    = "tab"    // run by herdr in a tab of its own: it can take minutes
	HowPoint  = "point"  // a fixed pointer typed into the lead agent's tab
	HowResize = "resize" // herdr's own resize, then set fleet.width
	HowScreen = "screen" // nothing is run
)

var Actions = []Action{
	{ID: "answer", Label: "Answer", Run: []string{"answer", "{id}", "--file", "{file}", "--human"}, How: HowChild},
	{ID: "undo", Label: "Undo", Run: []string{"answer", "{id}", "--undo", "--human"}, How: HowChild, Key: "u"},
	{ID: "carry-on", Label: "Tell it to carry on", Run: []string{string(PointCarryOn)}, How: HowPoint},
	{ID: "ask-lead", Label: "Ask the lead", Run: []string{string(PointQuestion)}, How: HowPoint},
	{ID: "note-lead", Label: "Send the lead agent a note", Run: []string{"tell", "lead", "--file", "{file}", "--human"}, How: HowChild},
	{ID: "accept", Label: "Accept", Run: []string{"accept", "{task}", "--human"}, How: HowTab},
	{ID: "accept-by-hand", Label: "Accept", Run: []string{"accept", "{task}", "--by-hand", "--file", "{file}", "--human"}, How: HowChild},
	{ID: "send-back", Label: "Send back", Run: []string{"reject", "{task}", "--file", "{file}", "--human"}, How: HowChild},
	{ID: "stop", Label: "Stop", Run: []string{"stop", "{task}", "--human"}, How: HowChild, Confirm: true},
	{ID: "start", Label: "Start", Run: []string{"start", "{task}", "--human"}, How: HowTab},
	{ID: "retry", Label: "Try again", Run: []string{"start", "{task}", "--retry", "--human"}, How: HowTab},
	{ID: "note", Label: "Send a note or a link", Run: []string{"tell", "{task}", "--file", "{file}", "--human"}, How: HowChild},
	{ID: "close", Label: "Close the tab", Run: []string{"close", "{task}", "--human"}, How: HowChild},
	{ID: "stop-all", Label: "Stop all", Run: []string{"pause", "--everywhere", "--human"}, How: HowChild, Key: "S", Prefix: "shift+a", Confirm: true},
	{ID: "resume", Label: "Resume", Run: []string{"resume", "--everywhere", "--human"}, How: HowChild, Key: "r", Prefix: "shift+a"},
	{ID: "dnd", Label: "Do not disturb", Run: []string{"set", "dnd", "{value}"}, How: HowChild, Key: "d", Prefix: "shift+m"},
	{ID: "mute", Label: "Mute", Run: []string{"set", "mute", "{value}"}, How: HowChild, Key: "m", Prefix: "m"},
	{ID: "theme", Label: "Next colour scheme", Run: []string{"set", "theme", "{value}"}, How: HowChild, Key: "t"},
	{ID: "set", Label: "Settings", Run: []string{"set", "{key}", "{value}"}, How: HowChild},
	{ID: "catchup", Label: "Catch me up", Run: []string{"catchup"}, How: HowChild, Key: "c", Prefix: "u"},
	{ID: "team", Label: "Run a team...", Run: []string{"team", "run", "{name}", "--human"}, How: HowChild},
	{ID: "go", Label: "Go there", Run: []string{"jump", "{task}"}, How: HowChild, Key: "enter"},
	{ID: "fleet", Label: "Fleet on and off", Run: []string{"ui", "fleet", "{value}"}, How: HowChild, Key: "f", Prefix: "f"},
	{ID: "actions", Label: "Action pane on and off", Run: []string{"ui", "actions", "{value}"}, How: HowChild},
	{ID: "to-actions", Label: "Go to the action pane", Run: []string{"ui", "actions", "focus"}, How: HowChild, Key: "a", Prefix: "a"},
	{ID: "narrower", Label: "Narrower", How: HowResize, Key: "["},
	{ID: "wider", Label: "Wider", How: HowResize, Key: "]"},
	{ID: "menu", Label: "Every action", Run: []string{"ui", "menu"}, How: HowScreen, Key: "/", Prefix: "space"},
	{ID: "fold", Label: "Fold or unfold", How: HowScreen, Key: "x"},
	{ID: "show", Label: "Show", How: HowScreen},
	{ID: "keys", Label: "All keys", How: HowScreen, Key: "?"},
	{ID: "forget-phone", Label: "Forget a phone", Run: []string{"dash", "phone", "forget", "{id}"}, How: HowChild},
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
	PointResumed  Pointer = "whaleshark: resumed. Run: whaleshark mail"
	PointLeadOn   Pointer = "whaleshark: resumed. Run: whaleshark wait"
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
)

// ValueRule is one rule of the validator every value passes before anything
// is touched, and again when it is loaded from disk.
type ValueRule struct {
	Value   string
	Max     int    // characters; 0 is no limit
	Words   int    // 0 is no limit
	Pattern string // the whole value must match; empty is no pattern
	OneLine bool   // no line breaks and no control characters
	Inside  bool   // a relative path with no "..", no leading "-", still inside the project once links are resolved
}

var ValueRules = []ValueRule{
	{Value: "id", Max: 16, Pattern: `^[A-Za-z0-9_][A-Za-z0-9_-]*$`},
	{Value: "title", Max: 80, OneLine: true},
	{Value: "name", Max: 24, Words: 3, OneLine: true},
	{Value: "agent", Max: 32, Pattern: `^[a-z][a-z0-9_-]*$`},
	{Value: "slug", Pattern: `^[a-z0-9-]+$`},
	{Value: "path", Inside: true, OneLine: true},
	{Value: "text"},
}
