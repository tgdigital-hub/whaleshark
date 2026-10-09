// Package testkit is the contract of the tests: the fixture format, the
// grammar of a scenario file, and how the fake herdr starts a fake agent and
// pushes events. It is test code; nothing the program ships imports it.
package testkit

import (
	"embed"
	"encoding/json"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// FixtureVersion is raised by every change to the shape of a fixture.
const FixtureVersion = 1

// Fixture is one prepared moment: everything the view model is built from,
// and the view model each kind of caller must then be shown. The fixture
// builder writes State as the run's state.json, Ctx as the context files and
// UI as ui.json, and hands Herdr to the fake herdr as its picture.
type Fixture struct {
	Version   int                         `json:"version"`
	Name      string                      `json:"name"`
	Now       time.Time                   `json:"now"`
	Checked   time.Time                   `json:"checked"`
	Elsewhere int                         `json:"elsewhere"`
	State     contract.State              `json:"state"`
	Herdr     *contract.Snapshot          `json:"herdr"`
	Ctx       map[string]contract.CtxFile `json:"ctx"`
	UI        contract.UIFile             `json:"ui"`
	// Views holds the expected view model by caller: "human" and "orchestrator".
	Views map[contract.CallerKind]*contract.View `json:"views"`
}

// Input is the fixture as the view builder takes it, for one caller.
func (f *Fixture) Input(caller contract.CallerKind) contract.ViewInput {
	return contract.ViewInput{
		Now: f.Now, Caller: caller, State: &f.State, Herdr: f.Herdr, Ctx: f.Ctx, UI: f.UI,
		Limits: contract.ProjectDefaults(), Elsewhere: f.Elsewhere, Checked: f.Checked,
		Swept: contract.Swept{Ran: true, At: f.Checked}, Watched: true,
	}
}

//go:embed fixtures/*.json
var fixtures embed.FS

// Evening is the fixture of an evening with twelve workers, at 21:14.
const Evening = "evening-2114"

// Load returns a fixture by name.
func Load(name string) (*Fixture, error) {
	data, err := fixtures.ReadFile("fixtures/" + name + ".json")
	if err != nil {
		return nil, err
	}
	f := new(Fixture)
	return f, json.Unmarshal(data, f)
}

// A scenario file (*.scn) is one step per line; a line starting with "#" is
// a comment. Lines lists every kind of line by its first word, with its shape.
// "-> expect <what>" may end any line that runs a command.
var Lines = map[string]string{
	"fixture":           `fixture <name>                     start from a prepared run`,
	"task":              `task <id> "<title>" [flags]        the flags of task add`,
	"agent":             `agent <attempt>: <step> | <step>   the script the fake agent of that attempt plays`,
	"orch:":             `orch: <command>                    a real command, run as the orchestrator; "--ack last" is the last delivery id`,
	"human:":            `human: <command>                   a real command, run as the human`,
	"as-worker":         `as-worker <attempt>: <command>     a real command, run from that attempt's tab`,
	"as-unbound:":       `as-unbound: <command>              a real command, run from a pane no run is bound to`,
	"clock":             `clock +<duration>                  move the injected clock`,
	"kill-wait":         `kill-wait                          kill the running wait`,
	"kill-orchestrator": `kill-orchestrator                  end the orchestrator's session`,
	"restart-herdr":     `restart-herdr                      the fake herdr restarts as the real one does`,
	"herdr-event":       `herdr-event <kind> <task>          the fake herdr changes its picture and pushes the line`,
	"herdr-drop":        `herdr-drop <kind> <task>           the fake herdr changes its picture and pushes nothing`,
	"notices":           `notices on|off                     file changes arrive with or without a notice`,
	"pane":              `pane fleet|actions: <cols>x<rows>  start a pane on a pretended terminal`,
	"click":             `click <text>                       click the first cell of that text in the pane`,
	"key":               `key <key>                          press one key in the pane`,
	"type":              `type "<text>"                      type into the pane`,
	"expect-row":        `expect-row <text>                  some row of the pane's grid contains the text`,
	"expect-last-line":  `expect-last-line <text>            the pane's last line contains the text`,
	"assert:":           `assert: <fact>; <fact>             facts about the record once every step has run`,
}

// AgentSteps is every step of a fake agent's script. Steps are separated by " | ".
var AgentSteps = map[string]string{
	"progress":          `progress <0-100> "<note>"`,
	"ask":               `ask "<question>"`,
	"expect-answer":     `expect-answer "<text>"`,
	"mail":              `mail`,
	"write-result":      `write-result`,
	"report":            `report done|failed "<summary>"`,
	"report-with-token": `report-with-token <token>`,
	"sleep":             `sleep <seconds>`,
	"die":               `die                    exit without reporting`,
	"hang":              `hang                   stay idle`,
	"block":             `block                  the fake herdr shows the pane at a prompt`,
	"edit":              `edit <file>`,
	"commit":            `commit`,
}

// How the fake herdr and the fake agent find each other. The scenario runner
// holds the fake herdr in its own process. A program named herdr on the PATH
// stands in for the real one: it sends its arguments, as one line of JSON, to
// the socket file EnvFake names and prints the answer. On "agent start" the
// fake herdr starts the program EnvAgent names with the environment the tab
// was created with, and that program plays the script registered for the
// attempt in its WHALESHARK_ATTEMPT. The event stream is served on the socket
// file EnvEvents names, in herdr's own lines.
const (
	EnvFake   = "FAKEHERDR_SOCKET"
	EnvEvents = "FAKEHERDR_EVENTS"
	EnvAgent  = "FAKEHERDR_AGENT"
)

// FakeCall is what the stand-in program sends; FakeAnswer is what it gets back.
type FakeCall struct {
	Args []string `json:"args"`
	Env  []string `json:"env"`
	Cwd  string   `json:"cwd"`
}

type FakeAnswer struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	Exit   int    `json:"exit"`
}

// What Push and Drop take besides a state: the agent left its pane, the
// pane was closed, the pane got the focus.
const (
	PushGone    = "gone"
	PushClosed  = "closed"
	PushFocused = "focused"
)

// FakeHerdr is the fake as the scenario runner drives it in its own process.
type FakeHerdr interface {
	contract.Herdr
	// Script registers what the fake agent of an attempt plays.
	Script(attempt, script string)
	// Push changes the picture of a pane and pushes the line; Drop changes
	// the picture and pushes nothing. kind is one of herdr's five states
	// (the contract's Status constants), PushGone, PushClosed or PushFocused.
	Push(kind, pane string)
	Drop(kind, pane string)
	// Lose drops the next pushed line silently.
	Lose()
	// Restart does what the real herdr does: the same pane and tab ids, new
	// terminal ids, none of our variables in any tab, an empty shell where a
	// pane program ran, and every event connection closed.
	Restart()
	// Calls is every call made so far, in order, as the arguments of the
	// real herdr command line: the proof that no key was sent.
	Calls() [][]string
}
