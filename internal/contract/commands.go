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
	// Leader is a fleet leader: the attempt of a group in the orchestrator's
	// run, calling from the pane its fleet's run records as its lead
	// agent's. Its report, progress and mail are a worker's and go up, to
	// the run its attempt is of; with any other command it is this kind and
	// addresses its fleet's run, where it may what an orchestrator may but
	// for LeaderNever. Its workers know it as their orchestrator.
	Leader CallerKind = "leader"
)

// LeaderNever is what a leader is refused (exit 5, RefusedLeader): a command,
// one form of it, or a command with one flag. It lands nothing, makes and
// ends no run, hands nothing to a fleet (two levels, never three), answers
// nothing in the person's name, and asks the orchestrator with need, whose
// answer its wait hands it, not with ask, which would keep it from waiting
// on its fleet. It changes no setting: only the agent the person talks to may.
var LeaderNever = []string{"land", "run new", "run close", "run rm", "run takeover", "task --fleet", "answer --relayed", "ask", "set"}

// The refusals of Fleet Mode, all exit 5.
const (
	RefusedLeader    = "fleet_leader"
	RefusedNotFleets = "not_fleets"
	LimitLeaders     = "limit_leaders"
	LimitFleet       = "limit_fleet"
	LimitTotal       = "limit_total"
)

// MaxDepth is the deepest an agent that starts agents may be: EnvDepth is 1
// in a worker's and a leader's tab and 2 in a tab of a leader's worker. start
// refuses at 2, and at 1 for every caller but a leader.
const MaxDepth = 2

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
// takes. Phase and Who are zero where they are the command's own. Help is
// its line in help.
type Flag struct {
	Name, Value, Help string
	Phase             int
	Who               Who
}

// Flags every command takes. Free text can always come from --file, or from
// standard input when the path is "-".
var CommonFlags = flags("json: print the result as JSON", "run=ID: this run, not the open one", "root=DIR: the project in this folder, not the one here", "help: this help", "human: you are the person, typing in a tab of an agent's", "file=PATH: the text from this file, or from standard input for -")

// flags builds a flag list from "name", "name=VALUE", "name@phase" (a digit)
// and "name!" (the human only), each with ": " and its line in help after it.
func flags(specs ...string) []Flag {
	out := make([]Flag, len(specs))
	for i, s := range specs {
		f := &out[i]
		s, f.Help, _ = strings.Cut(s, ": ")
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
	case Leader:
		return who&(O|W) != 0 && !slices.Contains(LeaderNever, c.Name) && !slices.Contains(LeaderNever, c.Name+" "+sub)
	}
	return who&U != 0
}

// LeaderMay is May for a leader whose command line is known: has reports
// whether a flag was given.
func (c *Command) LeaderMay(sub string, has func(flag string) bool) bool {
	for _, never := range LeaderNever {
		if name, flag, ok := strings.Cut(never, " --"); ok && name == c.Name && has(flag) {
			return false
		}
	}
	return c.May(Leader, sub)
}

// Commands is the command table, in the order help prints it.
var Commands = []Command{
	{Name: "init", Section: "setup", Phase: 1, Who: H | O | U, Exits: []int{0, 3},
		Usage:   "init [--agent KIND,...] [--rules global|project|none] [--keys] [--at-login] [--print-block] [--remove [--all]] [--allow-synced] [--adopt]",
		Help:    "set this project up, check what it needs, and offer the rules for the lead agent",
		Flags:   flags("agent=KIND,...: write the rules and the hooks for these agent tools", "rules=WHERE!: where the rules go without the question: global, project or none", "keys: show the shortcuts and, on your yes, write them into your terminals' settings", "at-login: on Windows, start the engine when you log on", "print-block: print the rules for the lead agent, and change nothing", "remove: take out what init wrote for this project", "all: with --remove, for every project of this login", "allow-synced: set up a project in a folder synced to other machines", "adopt!: take on a record this login did not make, with its runs and checks"),
		Example: "whaleshark init --agent claude"},
	{Name: "doctor", Section: "setup", Phase: 2, Who: H | O | U, Exits: []int{0, 1},
		Usage:   "doctor [--fix] [--server] [--notify] [--try NAME]",
		Help:    "one line per check, each with its fix; --try runs the one test of an agent tool",
		Flags:   flags("fix: put right what can be put right without asking", "server@3: check this login's part of a shared server too", "notify: send one test nudge to your phones", "try=NAME!: run the one test of this agent tool"),
		Example: "whaleshark doctor --server"},
	{Name: "guide", Section: "setup", Phase: 1, Who: all, Exits: []int{0},
		Usage: "guide [worker|lead|leader]", Help: "the guide that matches this version",
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
	{Name: "dash", Section: "setup", Phase: 3, Who: H | U, Exits: []int{0, 3, 4},
		Usage:   "dash start|stop [--forget]|url|status / dash phone on|off|list|forget <phone>|approve <number>|refuse <number>",
		Help:    "the page's server for this login, and its door for phones",
		Flags:   flags("forget: with stop, sign every browser and phone out"),
		Example: "whaleshark dash phone on"},
	{Name: "connect", Section: "setup", Phase: 3, Who: H | U, Exits: []int{0, 3},
		Usage:   "connect <ssh-target> [--terminals] [--at-login | --not-at-login] [--port N]",
		Help:    "from your own computer: keep one connection to the server for the page and the terminals",
		Flags:   flags("terminals: bring the window here too, not only the page", "at-login: on macOS, connect by itself at each login", "not-at-login: stop connecting by itself at login", "port=N: the port on this computer, from 1024 to 65535"),
		Example: "whaleshark connect myserver"},
	{Name: "server", Section: "setup", Phase: 3, Who: H | U, Exits: []int{0, 1, 3},
		Usage:   "server setup|harden|add|phone|tunnel|upgrade|status",
		Help:    "for the administrator, with sudo, on the server: set it up, add people, upgrade",
		Flags:   flags("admin=NAME: setup: the administrator's login", "admin-key=FILE: setup: the administrator's SSH keys, if not the ones you came in with", "user=NAME: add, phone: the work login", "key=FILE: add: that login's SSH public key", "off: phone, tunnel: take the door away", "name=NAME: phone: the person's public name", "aud=TAG: phone: the tag of their Access application", "email=LIST: phone: their email addresses", "team=NAME: tunnel: your team's name at Cloudflare"),
		Example: "sudo whaleshark server add --user ana --key ana.pub"},
	{Name: "ui", Section: "setup", Phase: 1, Who: H | O | U, Exits: []int{0, 3},
		Usage:   "ui [close] / ui fleet|actions [on|off] / ui settings / ui menu / ui run <pane> / ui run offer <kind> <id>",
		Help:    "open or close the fleet pane and the action pane in the lead agent's tab, or open the settings screen",
		Example: "whaleshark ui fleet off"},
	{Name: "open", Section: "setup", Phase: PhaseE, Who: H | U, Exits: []int{0, 3},
		Usage: "open", Help: "the window: your tabs and panes in this terminal; closing it stops nothing",
		Example: "whaleshark open"},
	{Name: "engine", Section: "setup", Phase: PhaseE, Who: H | U, Exits: []int{0, 3},
		Usage: "engine run|stop|status", Help: "the keeper that holds every pane's terminal for this login: run it, stop it, see it",
		Example: "whaleshark engine status"},
	{Name: "set", Section: "setup", Phase: 1, Who: H | O | U, Exits: []int{0, 2, 5},
		Usage: "set <key> <value> [--project] / set --save FILE / set --load FILE", Help: "change a setting; every one is a key here and a row of the settings screen; save your settings as a file, or load one",
		Flags:   flags("project!: change this project's setting, not your own", "save=FILE!: write your settings into a file", "load=FILE!: take your settings from a file"),
		Example: "whaleshark set theme kelp"},
	{Name: "hook", Section: "setup", Phase: 1, Who: all, Exits: []int{0},
		Usage: "hook statusline / hook gate [kind] / hook state <event>", Help: "run by an agent's own harness, never typed",
		Subs: []Sub{{Name: "state", Phase: PhaseE}}},

	{Name: "run", Section: "runs", Phase: 1, Who: O | H, Exits: []int{0, 4, 5},
		Usage:   `run new "<objective>" [--base REF] [--limit N] [--park] / run list / run show / run use <id> / run close [--abandon] / run rm <id> / run takeover / run fleet ["<name>" ["<what it works on>"] [--forget]]`,
		Help:    "start, list, show, switch, close or delete a run, or bind it to this pane; keep a fleet by name with what it works on",
		Flags:   flags("base=REF: new: the branch, tag or commit the work starts from", "limit=N: new: the most agents at once", "park: new: leave the open run open beside it", "abandon: close: without landing the accepted work", "forget: fleet: stop keeping that fleet"),
		Subs:    []Sub{{Name: "new", Who: O | H | U}, {Name: "takeover", Who: O | H | U}, {Name: "use", Who: O | H | U}, {Name: "list", Who: O | H | U}, {Name: "show", Who: O | H | U}, {Name: "rm", Who: O | H | U}, {Name: "fleet", Who: O | H | U}},
		Example: "whaleshark run close"},
	{Name: "task", Section: "runs", Phase: 1, Who: O, Exits: []int{0, 2, 4, 5},
		Usage:   `task add <id> "<title>" --brief FILE --check CMD|none [--name "<up to three words>"] [--after A,B] [--owns PATTERN,...] [--shared | --cwd DIR | --fleet NAME] [--agent KIND] [--model M] [--effort low|medium|high|max] [--args="<arguments>"] [--browser-check] [--kind code|readonly|review|watch] [--reviews T] [--limit 2h] [--worktree detached] / task edit <id> [--name "<up to three words>"] [--brief FILE] [--check CMD|none] [--owns ...] [--after ...] / task cancel <id> / task reset <id>`,
		Help:    "add a task with its brief and its check, change it, cancel it or reset it",
		Flags:   flags("brief=FILE: the file that says what to do", "check=CMD|none: the command that must pass before the work is kept", "name=NAME: the worker's name, three words at most", "after=A,B: start only once these are done", "owns=PATTERN,...: the files it may change", "shared: work in the project's own folder, not a copy", "cwd=DIR: work in this folder, not a copy", "fleet=NAME: a group for this fleet's leader", "agent=KIND: the agent tool that does it", "model=M: the model it uses", "effort=LEVEL: low, medium, high or max", "args=ARGS: more arguments for its tool, after the project's, given as --args=\"...\"", "browser-check: the work must be seen in a browser", "kind=KIND: code, or readonly, review or watch for a task that changes no code: no check, no copy of its own", "reviews=T: the task a review is of; T waits for its verdict", "limit=TIME: a watch's time box, as 2h or 90m", "worktree=detached: a copy of the base of its own, with no branch and no setup"),
		Example: `whaleshark task add T3 "url parser" --brief b/T3.md --after T1 --owns 'src/url/**' --check 'make test-url'`},
	{Name: "start", Section: "runs", Phase: 1, Who: O, Exits: []int{0, 1, 3, 5},
		Usage:   "start <task>... | --ready [--retry] [--resume] [--agent KIND] [--model M] [--allow-overlap]",
		Help:    "start a worker for each task, each in a tab named after it; run it in the background",
		Flags:   flags("ready: every task that can start now", "retry: a new attempt at a task that ended", "resume@2: with --retry, the new attempt carries on where the last stopped", "agent=KIND: the agent tool for this start", "model=M: the model for this start", "allow-overlap: start though it may change the files of a running task"),
		Example: "whaleshark start T3"},
	{Name: "wait", Section: "runs", Phase: 1, Who: O, Exits: []int{0, 4, 5},
		Usage:   "wait [--for KINDS] [--timeout SEC] [--ack ID]",
		Help:    "block until something happens, then hand out the oldest batch of events",
		Flags:   flags("for=KINDS: only these kinds of event", "timeout=SEC: give up after this many seconds", "ack=ID: mark the batch handed out before as read"),
		Example: "whaleshark wait --ack d16 --timeout 540"},
	{Name: "answer", Section: "runs", Phase: 1, Who: O | H, Exits: []int{0, 2, 4, 5},
		Usage:   `answer <id> "<text>" [--relayed] / answer <id> --undo`,
		Help:    "answer a question or an item; --undo reopens one whose answer nothing has used yet",
		Flags:   flags("relayed: you pass on what the person told you", "undo!: reopen it"),
		Example: `whaleshark answer q7 "use main"`},
	{Name: "escalate", Section: "runs", Phase: 1, Who: O, Exits: []int{0, 4, 5},
		Usage: "escalate <qid>", Help: "hand a worker's question on: to the human, and from a fleet to the orchestrator",
		Example: "whaleshark escalate q7"},
	{Name: "need", Section: "runs", Phase: 1, Who: O, Exits: []int{0, 4, 5, 75},
		Usage:   `need question|choice|signoff|todo "<text>" [--task T] [--holds] [--options a,b] [--urgent] [--wait [--timeout SEC]] / need close <id>`,
		Help:    "put an item in front of the person, or withdraw one",
		Flags:   flags("task=T: the task it is about", "holds@2: with --task, the task waits for the answer", "options=a,b: the answers to choose from", "urgent: put it on top", "wait@2: wait here for the answer", "timeout=SEC@2: with --wait, give up after this many seconds"),
		Example: `whaleshark need choice "ship with the old API kept?" --task T10 --holds`},
	{Name: "tell", Section: "runs", Phase: 1, Who: O, Exits: []int{0, 5},
		Usage:   `tell <task> "<text>" [--now | --quiet] / tell lead "<text>"`,
		Help:    "send a worker a message; tell lead is the person's note to the lead agent",
		Flags:   flags("now: type it in even while the worker is busy", "quiet: leave it in the mail, and type nothing in"),
		Subs:    []Sub{{Name: "lead", Who: H}},
		Example: "whaleshark tell T3 --file notes.md"},
	{Name: "accept", Section: "runs", Phase: 1, Who: O, Exits: []int{0, 1, 5},
		Usage:   `accept <task>... [--by-hand "<what I ran or read>"]`,
		Help:    "run the task's check and, when it passes, keep the work; several tasks are checked together. With land.mode pr-per-task: push it as its own pull request, merged when its checks pass",
		Flags:   flags("by-hand=NOTE: there is no check to run: say what you ran or read"),
		Example: "whaleshark accept T3"},
	{Name: "reject", Section: "runs", Phase: 1, Who: O, Exits: []int{0, 5},
		Usage: `reject <task> "<why>"`, Help: "send a result back to the same agent, or to ready if it is proven gone",
		Example: `whaleshark reject T3 "tests for IPv6 missing"`},
	{Name: "stop", Section: "runs", Phase: 1, Who: O, Exits: []int{0, 5},
		Usage: "stop <task> [--keep-tab] / stop --all", Help: "stop a task's attempt and close its tab; --all stops every agent of the job",
		Flags:   flags("keep-tab: leave its tab open", "all!: every agent of the job"),
		Example: "whaleshark stop T7"},
	{Name: "close", Section: "runs", Phase: 1, Who: O, Exits: []int{0, 5},
		Usage:   "close <task>... | --settled [--keep-worktree] [--discard]",
		Help:    "close the tabs of ended attempts, and remove a done task's copy of the code",
		Flags:   flags("settled: every task that has ended", "keep-worktree: keep the copy of the code", "discard: remove the copy of a task that failed or was cancelled"),
		Example: "whaleshark close --settled"},
	{Name: "pause", Section: "runs", Phase: 1, Who: H, Exits: []int{0},
		Usage: "pause [--everywhere]", Help: "Stop all: every agent stops at its next step and nothing new starts",
		Flags:   flags("everywhere: in every project of this login"),
		Example: "whaleshark pause --human"},
	{Name: "resume", Section: "runs", Phase: 1, Who: H, Exits: []int{0, 5},
		Usage: "resume [--everywhere]", Help: "end the pause and tell each paused agent to carry on",
		Flags:   flags("everywhere: in every project of this login"),
		Example: "whaleshark resume --human"},
	{Name: "team", Section: "runs", Phase: 2, Who: O | H, Exits: []int{0, 4, 5},
		Usage:   `team save <name> / team list / team show <name> / team run <name> [--goal "<text>"] / team rm <name>`,
		Help:    "saved teams: a set of tasks kept as a file and added to a run in one step",
		Flags:   flags("goal=TEXT: run: what the team works on this time"),
		Subs:    []Sub{{Name: "list", Who: O | H | U}, {Name: "show", Who: O | H | U}},
		Example: `whaleshark team run "site review" --goal "the checkout pages"`},

	{Name: "report", Section: "worker", Phase: 1, Who: W, Exits: []int{0, 1, 5},
		Usage: `report done|failed "<summary>" [--task ID]`, Help: "say that the task is finished, or that it could not be",
		Flags:   flags("task=ID: a fleet leader's: which of its groups"),
		Example: `whaleshark report done "Parser handles IPv6; 41 tests pass."`},
	{Name: "progress", Section: "worker", Phase: 1, Who: W, Exits: []int{0, 5},
		Usage: `progress <0-100> "<note>" [--task ID]`, Help: "note how far the work is",
		Flags:   flags("task=ID: a fleet leader's: which of its groups"),
		Example: `whaleshark progress 40 "tests written"`},
	{Name: "ask", Section: "worker", Phase: 1, Who: W, Exits: []int{0, 4, 5, 75},
		Usage:   `ask "<question>" [--options a,b] [--timeout SEC] / ask --resume <qid>`,
		Help:    "ask a question and wait for the answer",
		Flags:   flags("options=a,b: the answers to choose from", "timeout=SEC: stop waiting after this many seconds", "resume=QID: wait again for a question asked before"),
		Example: `whaleshark ask "Which base branch?" --options main,release`},
	{Name: "mail", Section: "worker", Phase: 1, Who: W, Exits: []int{0},
		Usage: "mail", Help: "read the messages from the orchestrator",
		Example: "whaleshark mail"},

	{Name: "status", Section: "views", Phase: 1, Who: O | H | U, Exits: []int{0},
		Usage:   "status [--all] [--everywhere] [--items] [--sweep --quiet]",
		Help:    "the team, with what waits for the person on top",
		Flags:   flags("all: every task, the ended ones too", "everywhere: the open runs of every project of this login", "items: only what waits for the person", "sweep: look at the tabs and the copies, and record what changed", "quiet: print nothing"),
		Example: "whaleshark status"},
	{Name: "graph", Section: "views", Phase: 2, Who: O | H | U, Exits: []int{0},
		Usage: "graph [--ready]", Help: "the plan: tasks in the order they depend on each other",
		Flags:   flags("ready: only the tasks that can start now"),
		Example: "whaleshark graph --ready"},
	{Name: "log", Section: "views", Phase: 2, Who: O | H | U, Exits: []int{0, 4, 5},
		Usage: "log [--all] [--task ID] [--export [FILE]]", Help: "the messages both ways, newest last; --export writes the job's log as one plain file",
		Flags:   flags("all: every day and every task, not only today's", "task=ID: only this task's messages", "export!: write the job's log as one plain file"),
		Example: "whaleshark log --task T3"},
	{Name: "show", Section: "views", Phase: 1, Who: O | H | U, Exits: []int{0, 4},
		Usage:   "show <task>[.n] [--screen] [--diff [--stat]]",
		Help:    "one task in full, with what to do next on top",
		Flags:   flags("screen: what its tab shows now", "diff@2: what it changed", "stat@2: with --diff, only the count of lines in each file"),
		Example: "whaleshark show T3 --screen"},
	{Name: "jump", Section: "views", Phase: 1, Who: H | U, Exits: []int{0, 4},
		Usage: "jump [task | lead]", Help: "go to a task's tab, or back to the lead agent's",
		Example: `whaleshark jump "sign-up page"`},
	{Name: "catchup", Section: "views", Phase: 1, Who: H | O | U, Exits: []int{0},
		Usage: "catchup [--since TIME]", Help: "what happened while you were away",
		Flags:   flags("since=TIME: since this time, 15:04 or a full time"),
		Example: "whaleshark catchup"},

	{Name: "overlap", Section: "code", Phase: 2, Who: O | H | U, Exits: []int{0, 1, 4, 6, 7},
		Usage: "overlap [task...]", Help: "files touched by more than one task, and pairs that would conflict",
		Example: "whaleshark overlap --json"},
	{Name: "sync", Section: "code", Phase: 2, Who: O, Exits: []int{0, 5},
		Usage: "sync <task>", Help: "send a worker the brief for bringing its copy up to date",
		Example: "whaleshark sync T7"},
	{Name: "land", Section: "code", Phase: 2, Who: H | O, Exits: []int{0, 1, 5},
		Usage: "land [--pr | --local]", Help: "check the collected work, then open one pull request or merge it; the lead agent only if the project allows. Not with land.mode pr-per-task, where each task lands by itself",
		Flags:   flags("pr: open one pull request", "local: merge it here"),
		Example: "whaleshark land --pr"},
	{Name: "lock", Section: "code", Phase: 2, Who: all, Exits: []int{0, 2, 4, 5},
		Usage:   `lock take <name> [--task T] [--stale DURATION] [--note "<text>"] / lock release <name> / lock list / lock steal <name>`,
		Help:    "named locks that every agent and the person see: a taker of a held one waits in line, and one past its stale time is told the lead agent",
		Flags:   flags("task=T: take: the task it is held for", "stale=DURATION: take: stale after this long, such as 45m", "note=TEXT: take: what it is held for"),
		Subs:    []Sub{{Name: "steal", Who: O | H}},
		Example: `whaleshark lock take deploy --task T3 --stale 45m --note "prod deploy"`},
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
		"graph", "log", "show", "jump", "catchup", "overlap", "lock"}
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
