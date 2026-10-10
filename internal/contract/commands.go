package contract

import (
	"slices"
	"strconv"
	"strings"
)

// CallerKind is who runs a command, worked out the same way for every command.
type CallerKind string

const (
	Worker       CallerKind = "worker"
	Orchestrator CallerKind = "orchestrator"
	Unbound      CallerKind = "unbound"
	Human        CallerKind = "human"
)

// Caller is who is calling: the kind, the pane if any, the attempt for
// a worker, and for a human where the command says it came from. A worker is
// known by EnvAttempt in its tab or, in a tab that lost its variables, by EnvPane
// being the pane recorded as a live attempt's place.
type Caller struct {
	Kind    CallerKind
	Pane    string
	Attempt string
	Where   string
}

// Who is the set of callers a command is marked for.
type Who uint8

const (
	W Who = 1 << iota // a worker
	O                 // the orchestrator, and with it the human
	H                 // the human
	U                 // also an unbound caller: it reads only, changes no run, or binds the pane
)

const all = W | O | H | U

// Exit codes, the same in text and JSON.
const (
	ExitOK      = 0
	ExitFailed  = 1
	ExitUsage   = 2
	ExitEnv     = 3
	ExitMissing = 4
	ExitRefused = 5
	ExitClash   = 6
	ExitNoLand  = 7
	ExitPending = 75
)

// PhaseE is the engine's phase in a Phase field: it is built between
// phases 1 and 2 and has a letter for a name.
const PhaseE = 5

// PhaseName is how a phase is written for a person.
func PhaseName(phase int) string {
	if phase == PhaseE {
		return "E"
	}
	return strconv.Itoa(phase)
}

// Command is one row of the command table: the single source for the parser,
// the caller check, help and the guides.
type Command struct {
	Name    string
	Section string // of the command set: setup, runs, worker, views, code
	Phase   int    // the phase that builds it; PhaseE for the engine's
	Who     Who
	Usage   string
	Help    string
	Exits   []int
	Flags   []Flag
	Subs    []Sub
	Example string
}

// Sub is a sub-command or form whose callers or phase differ from its command's.
type Sub struct {
	Name  string
	Who   Who
	Phase int
}

// Flag is one option. Value is empty for a switch, else the name of what it
// takes. Phase and Who are zero where they are the command's own.
type Flag struct {
	Name, Value string
	Phase       int
	Who         Who
}

// Flags every command takes. Free text can always come from --file, or from
// standard input when the path is "-".
var CommonFlags = flags("json", "run=ID", "root=DIR", "help", "human", "file=PATH")

// flags builds a flag list from "name", "name=VALUE", "name@phase" (a digit)
// and "name!" (the human only).
func flags(specs ...string) []Flag {
	out := make([]Flag, len(specs))
	for i, s := range specs {
		f := &out[i]
		if human, ok := strings.CutSuffix(s, "!"); ok {
			s, f.Who = human, H
		}
		if before, phase, ok := strings.Cut(s, "@"); ok {
			s, f.Phase = before, int(phase[0]-'0')
		}
		f.Name, f.Value, _ = strings.Cut(s, "=")
	}
	return out
}

// May reports whether a kind of caller may run a command, or one form of it.
func (c *Command) May(kind CallerKind, sub string) bool {
	who := c.Who
	for _, s := range c.Subs {
		if s.Name == sub && s.Who != 0 {
			who = s.Who
		}
	}
	switch kind {
	case Worker:
		return who&W != 0
	case Orchestrator:
		return who&O != 0
	case Human:
		return who&(O|H) != 0
	}
	return who&U != 0
}

// Commands is the command table, in the order help prints it.
var Commands = []Command{
	{Name: "init", Section: "setup", Phase: 1, Who: H | O | U, Exits: []int{0, 3},
		Usage:   "init [--agent KIND,...] [--keys] [--print-block] [--remove [--all]] [--allow-synced]",
		Help:    "set this project up, check what it needs, and offer the rules for the lead agent",
		Flags:   flags("agent=KIND,...", "keys", "print-block", "remove", "all", "allow-synced"),
		Example: "whaleshark init --agent claude"},
	{Name: "doctor", Section: "setup", Phase: 2, Who: H | O | U, Exits: []int{0, 1},
		Usage:   "doctor [--fix] [--server] [--notify]",
		Help:    "one line per check, each with its fix",
		Flags:   flags("fix", "server@3", "notify"),
		Example: "whaleshark doctor --server"},
	{Name: "guide", Section: "setup", Phase: 1, Who: all, Exits: []int{0},
		Usage: "guide [worker|lead]", Help: "the guide that matches this version",
		Example: "whaleshark guide lead"},
	{Name: "trust", Section: "setup", Phase: 2, Who: H, Exits: []int{0, 1},
		Usage: "trust", Help: "show what the project's settings would run or reach, and approve it",
		Example: "whaleshark trust"},
	{Name: "help", Section: "setup", Phase: 1, Who: all, Exits: []int{0, 2},
		Usage: "help [command] [--json]", Help: "this list, or one command in full",
		Example: "whaleshark help start"},
	{Name: "version", Section: "setup", Phase: 1, Who: all, Exits: []int{0},
		Usage: "version", Help: "version, build, and the engine's version seen",
		Example: "whaleshark version"},
	{Name: "dash", Section: "setup", Phase: 3, Who: H | U, Exits: []int{0, 3},
		Usage:   "dash start|stop [--forget]|url|status / dash phone on|off|list|forget <phone>",
		Help:    "the page's server for this login, and its door for phones",
		Flags:   flags("forget"),
		Example: "whaleshark dash phone on"},
	{Name: "connect", Section: "setup", Phase: 3, Who: H | U, Exits: []int{0, 3},
		Usage:   "connect <ssh-target> [--terminals] [--at-login | --not-at-login] [--port N]",
		Help:    "from your own computer: keep one connection to the server for the page and the terminals",
		Flags:   flags("terminals", "at-login", "not-at-login", "port=N"),
		Example: "whaleshark connect myserver"},
	{Name: "server", Section: "setup", Phase: 3, Who: H | U, Exits: []int{0, 1, 3},
		Usage:   "server setup|harden|add|phone|tunnel|upgrade|status",
		Help:    "for the administrator, with sudo, on the server: set it up, add people, upgrade",
		Flags:   flags("admin=NAME", "admin-key=FILE", "user=NAME", "key=FILE", "off", "name=NAME", "aud=TAG", "email=LIST", "team=NAME"),
		Example: "sudo whaleshark server add --user ana --key ana.pub"},
	{Name: "ui", Section: "setup", Phase: 1, Who: H | O | U, Exits: []int{0, 3},
		Usage:   "ui [close] / ui fleet|actions [on|off] / ui menu / ui run <pane>",
		Help:    "open or close the fleet pane and the action pane in the lead agent's tab",
		Example: "whaleshark ui fleet off"},
	{Name: "open", Section: "setup", Phase: PhaseE, Who: H | U, Exits: []int{0, 3},
		Usage: "open", Help: "the window: your tabs and panes in this terminal; closing it stops nothing",
		Example: "whaleshark open"},
	{Name: "engine", Section: "setup", Phase: PhaseE, Who: H | U, Exits: []int{0, 3},
		Usage: "engine run|stop|status", Help: "the keeper that holds every pane's terminal for this login: run it, stop it, see it",
		Example: "whaleshark engine status"},
	{Name: "set", Section: "setup", Phase: 1, Who: H | U, Exits: []int{0, 2},
		Usage: "set <key> <value>", Help: "your own settings: theme, dnd, mute, nudge.sound, nudge.popup, nudge.phone, actions, fleet.width",
		Example: "whaleshark set theme kelp"},
	{Name: "hook", Section: "setup", Phase: 1, Who: all, Exits: []int{0},
		Usage: "hook statusline / hook gate [kind] / hook state <event>", Help: "run by an agent's own harness, never typed",
		Subs: []Sub{{Name: "state", Phase: PhaseE}}},

	{Name: "run", Section: "runs", Phase: 1, Who: O | H, Exits: []int{0, 4, 5},
		Usage:   `run new "<objective>" [--base REF] [--limit N] [--park] / run list / run show / run use <id> / run close [--abandon] / run rm <id> / run takeover`,
		Help:    "start, list, show, switch, close or delete a run, or bind it to this pane",
		Flags:   flags("base=REF", "limit=N", "park", "abandon"),
		Subs:    []Sub{{Name: "new", Who: O | H | U}, {Name: "takeover", Who: O | H | U}, {Name: "use", Who: O | H | U}, {Name: "list", Who: O | H | U}, {Name: "show", Who: O | H | U}},
		Example: "whaleshark run close"},
	{Name: "task", Section: "runs", Phase: 1, Who: O, Exits: []int{0, 2, 4, 5},
		Usage:   `task add <id> "<title>" --brief FILE --check CMD|none [--name "<up to three words>"] [--after A,B] [--owns PATTERN,...] [--shared | --cwd DIR] [--agent KIND] [--model M] [--browser-check] / task edit <id> [--brief FILE] [--check CMD|none] [--owns ...] [--after ...] / task cancel <id> / task reset <id>`,
		Help:    "add a task with its brief and its check, change it, cancel it or reset it",
		Flags:   flags("brief=FILE", "check=CMD|none", "name=NAME", "after=A,B", "owns=PATTERN,...", "shared", "cwd=DIR", "agent=KIND", "model=M", "browser-check"),
		Example: `whaleshark task add T3 "url parser" --brief b/T3.md --after T1 --owns 'src/url/**' --check 'make test-url'`},
	{Name: "start", Section: "runs", Phase: 1, Who: O, Exits: []int{0, 1, 3, 5},
		Usage:   "start <task>... | --ready [--retry] [--resume] [--agent KIND] [--model M] [--allow-overlap]",
		Help:    "start a worker for each task, each in a tab named after it; run it in the background",
		Flags:   flags("ready", "retry", "resume@2", "agent=KIND", "model=M", "allow-overlap"),
		Example: "whaleshark start T3"},
	{Name: "wait", Section: "runs", Phase: 1, Who: O, Exits: []int{0, 4, 5},
		Usage:   "wait [--for KINDS] [--timeout SEC] [--ack ID]",
		Help:    "block until something happens, then hand out the oldest batch of events",
		Flags:   flags("for=KINDS", "timeout=SEC", "ack=ID"),
		Example: "whaleshark wait --ack d16 --timeout 540"},
	{Name: "answer", Section: "runs", Phase: 1, Who: O | H, Exits: []int{0, 2, 4, 5},
		Usage:   `answer <id> "<text>" [--relayed] / answer <id> --undo`,
		Help:    "answer a question or an item; --undo reopens one whose answer nothing has used yet",
		Flags:   flags("relayed", "undo!"),
		Example: `whaleshark answer q7 "use main"`},
	{Name: "escalate", Section: "runs", Phase: 1, Who: O, Exits: []int{0, 4, 5},
		Usage: "escalate <qid>", Help: "mark a worker's question as one for the human",
		Example: "whaleshark escalate q7"},
	{Name: "need", Section: "runs", Phase: 1, Who: O, Exits: []int{0, 4, 5, 75},
		Usage:   `need question|choice|signoff|todo "<text>" [--task T] [--holds] [--options a,b] [--urgent] [--wait [--timeout SEC]] / need close <id>`,
		Help:    "put an item in front of the person, or withdraw one",
		Flags:   flags("task=T", "holds@2", "options=a,b", "urgent", "wait@2", "timeout=SEC@2"),
		Example: `whaleshark need choice "ship with the old API kept?" --task T10 --holds`},
	{Name: "tell", Section: "runs", Phase: 1, Who: O, Exits: []int{0, 5},
		Usage:   `tell <task> "<text>" [--now | --quiet] / tell lead "<text>"`,
		Help:    "send a worker a message; tell lead is the person's note to the lead agent",
		Flags:   flags("now", "quiet"),
		Subs:    []Sub{{Name: "lead", Who: H}},
		Example: "whaleshark tell T3 --file notes.md"},
	{Name: "accept", Section: "runs", Phase: 1, Who: O, Exits: []int{0, 1, 5},
		Usage:   `accept <task>... [--by-hand "<what I ran or read>"]`,
		Help:    "run the task's check and, when it passes, keep the work; several tasks at once from phase 2",
		Flags:   flags("by-hand=NOTE"),
		Example: "whaleshark accept T3"},
	{Name: "reject", Section: "runs", Phase: 1, Who: O, Exits: []int{0, 5},
		Usage: `reject <task> "<why>"`, Help: "send a result back to the same agent, or to ready if it is proven gone",
		Example: `whaleshark reject T3 "tests for IPv6 missing"`},
	{Name: "stop", Section: "runs", Phase: 1, Who: O, Exits: []int{0, 5},
		Usage: "stop <task> [--keep-tab]", Help: "stop a task's attempt and close its tab",
		Flags:   flags("keep-tab"),
		Example: "whaleshark stop T7"},
	{Name: "close", Section: "runs", Phase: 1, Who: O, Exits: []int{0, 5},
		Usage:   "close <task>... | --settled [--keep-worktree] [--discard]",
		Help:    "close the tabs of attempts that have ended",
		Flags:   flags("settled", "keep-worktree@2", "discard@2"),
		Example: "whaleshark close --settled"},
	{Name: "pause", Section: "runs", Phase: 1, Who: H, Exits: []int{0},
		Usage: "pause [--everywhere]", Help: "Stop all: every agent stops at its next step and nothing new starts",
		Flags:   flags("everywhere"),
		Example: "whaleshark pause --human"},
	{Name: "resume", Section: "runs", Phase: 1, Who: H, Exits: []int{0, 5},
		Usage: "resume [--everywhere]", Help: "end the pause and tell each paused agent to carry on",
		Flags:   flags("everywhere"),
		Example: "whaleshark resume --human"},
	{Name: "team", Section: "runs", Phase: 2, Who: O | H, Exits: []int{0, 4, 5},
		Usage:   `team save <name> / team list / team show <name> / team run <name> [--goal "<text>"] / team rm <name>`,
		Help:    "saved teams: a set of tasks kept as a file and added to a run in one step",
		Flags:   flags("goal=TEXT"),
		Subs:    []Sub{{Name: "list", Who: O | H | U}, {Name: "show", Who: O | H | U}},
		Example: `whaleshark team run "site review" --goal "the checkout pages"`},

	{Name: "report", Section: "worker", Phase: 1, Who: W, Exits: []int{0, 1, 5},
		Usage: `report done|failed "<summary>"`, Help: "say that the task is finished, or that it could not be",
		Example: `whaleshark report done "Parser handles IPv6; 41 tests pass."`},
	{Name: "progress", Section: "worker", Phase: 1, Who: W, Exits: []int{0, 5},
		Usage: `progress <0-100> "<note>"`, Help: "note how far the work is",
		Example: `whaleshark progress 40 "tests written"`},
	{Name: "ask", Section: "worker", Phase: 1, Who: W, Exits: []int{0, 4, 5, 75},
		Usage:   `ask "<question>" [--options a,b] [--timeout SEC] / ask --resume <qid>`,
		Help:    "ask a question and wait for the answer",
		Flags:   flags("options=a,b", "timeout=SEC", "resume=QID"),
		Example: `whaleshark ask "Which base branch?" --options main,release`},
	{Name: "mail", Section: "worker", Phase: 1, Who: W, Exits: []int{0},
		Usage: "mail", Help: "read the messages from the orchestrator",
		Example: "whaleshark mail"},

	{Name: "status", Section: "views", Phase: 1, Who: O | H | U, Exits: []int{0},
		Usage:   "status [--all] [--everywhere] [--items] [--sweep --quiet]",
		Help:    "the team, with what waits for the person on top",
		Flags:   flags("all", "everywhere", "items", "sweep", "quiet"),
		Example: "whaleshark status"},
	{Name: "graph", Section: "views", Phase: 2, Who: O | H | U, Exits: []int{0},
		Usage: "graph [--ready]", Help: "the plan: tasks in the order they depend on each other",
		Flags:   flags("ready"),
		Example: "whaleshark graph --ready"},
	{Name: "log", Section: "views", Phase: 2, Who: O | H | U, Exits: []int{0},
		Usage: "log [--all] [--task ID]", Help: "the messages both ways, newest last",
		Flags:   flags("all", "task=ID"),
		Example: "whaleshark log --task T3"},
	{Name: "show", Section: "views", Phase: 1, Who: O | H | U, Exits: []int{0, 4},
		Usage:   "show <task>[.n] [--screen] [--diff [--stat]]",
		Help:    "one task in full, with what to do next on top",
		Flags:   flags("screen", "diff@2", "stat@2"),
		Example: "whaleshark show T3 --screen"},
	{Name: "jump", Section: "views", Phase: 1, Who: H | U, Exits: []int{0, 4},
		Usage: "jump [task | lead]", Help: "go to a task's tab, or back to the lead agent's",
		Example: `whaleshark jump "sign-up page"`},
	{Name: "catchup", Section: "views", Phase: 1, Who: H | O | U, Exits: []int{0},
		Usage: "catchup [--since TIME]", Help: "what happened while you were away",
		Flags:   flags("since=TIME"),
		Example: "whaleshark catchup"},

	{Name: "overlap", Section: "code", Phase: 2, Who: O | H | U, Exits: []int{0, 1, 6, 7},
		Usage: "overlap [task...]", Help: "files touched by more than one task, and pairs that would conflict",
		Example: "whaleshark overlap --json"},
	{Name: "sync", Section: "code", Phase: 2, Who: O, Exits: []int{0, 5},
		Usage: "sync <task>", Help: "send a worker the brief for bringing its copy up to date",
		Example: "whaleshark sync T7"},
	{Name: "land", Section: "code", Phase: 2, Who: H | O, Exits: []int{0, 1, 5},
		Usage: "land [--pr | --local]", Help: "check the collected work, then open one pull request or merge it; the lead agent only if the project allows",
		Flags:   flags("pr", "local"),
		Example: "whaleshark land --pr"},
}

// Find returns the row for a command name.
func Find(name string) *Command {
	for i := range Commands {
		if Commands[i].Name == name {
			return &Commands[i]
		}
	}
	return nil
}

// Aliases are the only other spellings a sub-command has. Destructive lists
// the commands and forms a mistyped word is never corrected into.
var (
	Aliases     = map[string]string{"remove": "rm", "delete": "rm"}
	Destructive = []string{"rm", "cancel", "reset", "stop", "close", "reject", "pause", "land"}
)

// Recorded lists the commands that change a run's record, and Unrecorded the
// rest: what only reads, what changes the login's or the project's own files
// and no run, a worker's commands, and answer, tell lead and catchup, which
// write the person's doing down themselves. A command stands in one of the
// two; a form of it ("run close") may stand in the other. What the person
// does with a recorded one is a human event that says what was done and where
// the command says it came from: the lead agent is handed it, and "done as
// you" counts it unless that place is a pane of ours, the page or a phone.
var (
	Recorded = []string{"trust", "dash phone", "run new", "run close", "run rm", "run takeover", "task", "start", "wait",
		"escalate", "need", "tell", "accept", "reject", "stop", "close", "pause", "resume", "team run", "sync", "land"}
	Unrecorded = []string{"init", "doctor", "guide", "help", "version", "dash", "connect", "server", "ui", "open",
		"engine", "set", "hook", "run", "answer", "tell lead", "team", "report", "progress", "ask", "mail", "status",
		"graph", "log", "show", "jump", "catchup", "overlap"}
)

// Deed names what a command line does to a run's record, as its human event
// says it: the command and its form, where the first argument is a word of
// the usage. It is empty for a line that is not recorded.
func (c *Command) Deed(args []string) string {
	deed := c.Name
	if words := strings.FieldsFunc(c.Usage, func(r rune) bool { return r < 'a' || r > 'z' }); len(args) > 0 && slices.Contains(words, args[0]) {
		deed += " " + args[0]
	}
	if slices.Contains(Unrecorded, deed) || !slices.Contains(Recorded, deed) && !slices.Contains(Recorded, c.Name) {
		return ""
	}
	return deed
}
