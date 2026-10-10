// Package testkit is the contract of the tests: the fixture format, the
// grammar of a scenario file, and how the engine's double starts a fake agent and
// tells a change. It is test code; nothing the program ships imports it.
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
// UI as ui.json, and hands Terms to the terminals as their picture.
type Fixture struct {
	Version   int                         `json:"version"`
	Name      string                      `json:"name"`
	Now       time.Time                   `json:"now"`
	Checked   time.Time                   `json:"checked"`
	Elsewhere int                         `json:"elsewhere"`
	State     contract.State              `json:"state"`
	Terms     *contract.Snapshot          `json:"terminals"`
	Ctx       map[string]contract.CtxFile `json:"ctx"`
	UI        contract.UIFile             `json:"ui"`
	// Views holds the expected view model by caller: "human" and "orchestrator".
	Views map[contract.CallerKind]*contract.View `json:"views"`
}

// Input is the fixture as the view builder takes it, for one caller.
func (f *Fixture) Input(caller contract.CallerKind) contract.ViewInput {
	return contract.ViewInput{
		Now: f.Now, Caller: caller, State: &f.State, Terms: f.Terms, Ctx: f.Ctx, UI: f.UI,
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
	"file":              `file <path>: <line> | <line>       write a file in the project folder, as a brief`,
	"agent":             `agent <attempt>: <step> | <step>   the script the fake agent of that attempt plays`,
	"orch:":             `orch: <command>                    a real command, run as the orchestrator; "--ack last" is the last delivery id`,
	"human:":            `human: <command>                   a real command, run as the human`,
	"as-worker":         `as-worker <attempt>: <command>     a real command, run from that attempt's tab`,
	"as-unbound:":       `as-unbound: <command>              a real command, run from a pane no run is bound to`,
	"clock":             `clock +<duration>                  move the injected clock`,
	"kill-wait":         `kill-wait                          kill the running wait`,
	"kill-orchestrator": `kill-orchestrator                  end the orchestrator's session`,
	"restart-terminals": `restart-terminals                  the terminals restart as the real keeper does`,
	"term-event":        `term-event <kind> <task>           the terminals' picture changes, and the change is told`,
	"term-drop":         `term-drop <kind> <task>            the same, told to nobody where a stand-in can`,
	"notices":           `notices on|off                     file changes arrive with or without a notice`,
	"pane":              `pane fleet|actions: <cols>x<rows>  start a pane on a pretended terminal`,
	"click":             `click <text>                       click the first cell of that text in the pane`,
	"key":               `key <key>                          press one key in the pane`,
	"type":              `type "<text>"                      type into the pane`,
	"expect-row":        `expect-row <text>                  some row of the pane's grid contains the text`,
	"expect-last-line":  `expect-last-line <text>            the pane's last line contains the text`,
	"assert:":           `assert: <fact>; <fact>             facts about the record once every step has run`,
	"await:":            `await: <fact>; <fact>              wait, where the line stands, until the record says so`,
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
	"block":             `block                  the pane's agent is at a prompt`,
	"edit":              `edit <file>`,
	"commit":            `commit`,
}

// How the engine's double and the fake agent find each other. The scenario
// runner holds the double in its own process and serves it on a socket
// file, which every program it starts reaches as it reaches the keeper. On
// an agent's start the double starts the program EnvAgent names with the
// variables the tab was created with, and that program plays the script
// registered for the attempt in its WHALESHARK_ATTEMPT.
const (
	// EnvCorpus names a second folder of recordings for the replay tests of
	// the engine, beside test/corpus: recordings that are not published.
	EnvCorpus = "WHALESHARK_CORPUS_EXTRA"
	EnvAgent  = "FAKEAGENT_PROGRAM"
	// EnvScripts names the folder of the fake agents' scripts, one file an
	// attempt, for the real keeper: it starts an agent by its kind's name and
	// knows nothing of scripts, so the fake agent finds its own there. Set,
	// it also has the fake agent report every moment of a session, as a real
	// agent's hooks do.
	EnvScripts = "FAKEAGENT_SCRIPTS"
)

// What Push and Drop take besides a state: the agent left its pane, the
// pane was closed, the pane got the focus.
const (
	PushGone    = "gone"
	PushClosed  = "closed"
	PushFocused = "focused"
)
