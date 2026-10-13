package contract

import (
	"bytes"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Key is one setting. Keys is every setting the program has: `set`, its
// help, the settings screen and the tests read this table and nothing else,
// and a test fails for a field of a settings file that has no row here.
//
// Name is what `set` takes; a part in angle brackets stands for a name the
// person gives. Section is its section of the settings screen, empty for
// another name of a row. Label is its name in plain words. Values is a
// choice's values with bars between, a number's least and most as "16..60"
// with "|unlimited" where that is a value, and empty for the rest. Default
// is what holds when nothing is said. Store is the file it lies in and Path
// where in that file, when that is not Name. Asks is what a change asks
// first. Help is what stands under the row in front, wrapped to four lines
// at most: for a switch that guards, what happens with it off, with it on,
// and what it never does.
type Key struct {
	Name    string `json:"name"`
	Section string `json:"section,omitempty"`
	Label   string `json:"label,omitempty"`
	Kind    string `json:"kind"`
	Values  string `json:"values,omitempty"`
	Default string `json:"default,omitempty"`
	Store   string `json:"store"`
	Path    string `json:"-"`
	Asks    string `json:"asks,omitempty"`
	Help    string `json:"help,omitempty"`
}

// The kinds of value: Words is a command cut into arguments at its spaces,
// List a list with commas between, a Tool a wired tool's name. The stores:
// the person's config.toml, the engine's settings.toml, which the keeper
// notices, ui.json for what changes many times a day, the login's tools
// folder, the project's whaleshark.toml and the server's file.
//
// What a change asks first. AsksRuns: the value can run a program or reach
// outside, so it is the person's alone to set (exit 5, human_only), the
// screen shows the exact value and asks once, and one warning line stays on
// every screen until the section was seen again (UIFile.Changed).
// AsksApproval: a key of the project's file that trust lists; it is written
// and held until `whaleshark trust`, which is never a button. AsksAdmin: the
// administrator's; shown, and never set here.
const (
	KindChoice, KindSwitch, KindNumber, KindText = "choice", "switch", "number", "text"
	KindWords, KindList, KindTool                = "words", "list", "tool"

	StoreConfig, StoreEngine, StoreUI     = "config", "engine", "ui"
	StoreTools, StoreProject, StoreServer = "tools", "project", "server"

	AsksRuns, AsksApproval, AsksAdmin = "runs", "approval", "admin"
	// AsksShown: written by the program from what it saw, shown, never set.
	AsksShown = "shown"

	// What `set` does with a key for an agent (Key.Agent): SetFree is set at
	// once and flagged on every screen until the person has seen its
	// section (UIFile.ByAgent); SetAsks holds agents or is how the person is
	// called, and waits as an item for their click; SetPerson is theirs alone.
	SetFree, SetAsks, SetPerson = "free", "asks", "person"
)

// Sections are the sections of the settings screen, in its order.
const (
	SecLook, SecLayout, SecNudges, SecKeys, SecScroll   = "Look", "Layout", "Nudges", "Keys", "Scrolling & copy"
	SecFleets, SecTools, SecProject, SecPage, SecServer = "Fleet Mode", "Agent tools", "This project", "Page & phone", "This server"
	SecGuards                                           = "Guardrails"
)

var Sections = []string{SecLook, SecLayout, SecNudges, SecKeys, SecScroll, SecFleets, SecGuards, SecTools, SecProject, SecPage, SecServer}

const fleetLimit = "2..|unlimited"

// dragIt ends the description of every size that a line shows: 21.5.
const dragIt = "Drag its edge to change it; a double-click puts it back."

var Keys = []Key{
	{"theme", SecLook, "Colour scheme", KindChoice, "reef|kelp|ember|paper|mono|none", "reef", StoreConfig, "ui.theme", "", "The colours of every pane of ours, on your terminal's own background; paper is for a light terminal, none leaves the terminal's own colours."},
	{"ui.marks", SecLook, "Marks", KindChoice, "auto|plain", "auto", StoreConfig, "", "", "Plain marks for a terminal that cannot show the drawn ones; the variable WHALESHARK_MARKS still wins."},
	{"ui.first_steps", SecLook, "First steps", KindSwitch, "", "on", StoreUI, "first_steps", "", "Five lines in the left section that show a newcomer what to type and where. They tick themselves off and go away. Switch on to see them again."},

	{"side.place", SecLayout, "The left section", KindChoice, "left|right", "left", StoreEngine, "", "", "At which edge of the window it stands. It cannot be switched off."},
	{"side.width", SecLayout, "Width of the left section", KindNumber, "16..60", "28", StoreEngine, "", "", "In columns. A narrow window shows less of it. " + dragIt},
	{"panes.fleet", SecLayout, "The fleet pane", KindChoice, "right|left|bottom|off", "right", StoreConfig, "", "", "Where it opens when a job starts. Off closes it; its key and whaleshark status remain."},
	{"panes.actions", SecLayout, "The action pane", KindChoice, "bottom|top|off", "bottom", StoreConfig, "", "", "Where what waits for you is listed. With it off the left section still counts what needs you."},
	{"actions", "", "", KindChoice, "bottom|top", "bottom", StoreConfig, "panes.actions", "", ""},
	{"fleet.width", SecLayout, "Width of the fleet pane", KindNumber, "10..500", "46", StoreUI, "fleet_width", "", "In columns, where it stands beside the conversation. " + dragIt},
	{"fleet.height", SecLayout, "Height of the fleet pane", KindNumber, "5..30", "9", StoreUI, "fleet_height", "", "In rows, where it lies at the bottom. " + dragIt},
	{"side.split", SecLayout, "Where the agents begin", KindNumber, "20..80", "50", StoreUI, "side_split", "", "How far down the left section the agents begin, in hundredths of its height. As it comes they begin half-way. Drag the heavy line to change it; a double-click puts it back."},
	{"actions.height", SecLayout, "Height of the action pane", KindNumber, "3..40|0", "0", StoreUI, "actions_height", "", "In rows. 0 is a quarter of the tab, as it comes. " + dragIt},
	{"folded", SecLayout, "The action pane folded", KindSwitch, "", "off", StoreUI, "", "", "Folded it is one line, and opens when an item holds work up."},

	{"dnd", SecNudges, "Do not disturb", KindChoice, "off|on|until TIME", "off", StoreUI, "", "", "Holds back every nudge that is not urgent, until you switch it off or until a time of day."},
	{"mute", SecNudges, "Mute", KindSwitch, "", "off", StoreUI, "", "", "No sound; everything else still shows."},
	{"nudge.sound", SecNudges, "A sound when you are needed", KindSwitch, "", "on", StoreConfig, "", "", "Off: an item for you makes no sound; it still shows everywhere."},
	{"nudge.popup", SecNudges, "A pop-up when you are needed", KindSwitch, "", "on", StoreConfig, "", "", "Off: no notice from your system; the panes and the page still show the item."},
	{"nudge.phone", SecNudges, "A nudge to your phone", KindSwitch, "", "on", StoreConfig, "", "", "Reaches the paired phones and the address below."},
	{"nudge.lead_minutes", SecNudges, "Minutes before a silent lead agent is said", KindNumber, "1..600", "10", StoreConfig, "", "", "How long your lead agent may rest with news unread before you are told."},
	{"notify.url", SecNudges, "Address a nudge is sent to", KindText, "", "", StoreConfig, "", AsksRuns, "One request goes there each time you are needed: a service of your choosing."},
	{"notify.link", SecNudges, "Link a nudge opens", KindText, "", "", StoreConfig, "", "", "What a tap on the notice opens."},
	{"notify.full_text", SecNudges, "Send the text along", KindSwitch, "", "off", StoreConfig, "", "", "Off sends the title alone; the text may hold what your agents wrote."},
	{"ui.settle_seconds", SecNudges, "Seconds an answer can be taken back", KindNumber, "0..60", "4", StoreConfig, "", "", "An answer is used only after this long; u undoes it until then."},
	{"update.notice", SecNudges, "Tell me when a new version is out", KindSwitch, "", "on", StoreConfig, "", "", "On: once a day, when you open the window, WhaleShark asks GitHub for the number of the latest release and shows one line if yours is older. Nothing about you or this computer is sent, and nothing is installed. Off: it never asks."},

	{"keys.command", SecKeys, "The command key", KindText, "", "ctrl+space", StoreEngine, "", "", "The key that starts every shortcut of the window."},
	{"keys.style", SecKeys, "Copy and paste keys", KindChoice, "auto|mac|windows|linux", "auto", StoreEngine, "", "", "Whose keys copy and paste; auto is the system you sit at."},
	{"shortcut.<key>", SecKeys, "A shortcut's command", KindWords, "", "", StoreEngine, "shortcut", AsksRuns, "What the key runs after the command key; nothing takes the shortcut away."},

	{"scrollback.lines", SecScroll, "Lines a pane keeps", KindNumber, "0..1000000", "5000", StoreEngine, "", "", ""},
	{"scrollback.keep", SecScroll, "Lines kept across a restart", KindNumber, "0..1000000", "2000", StoreEngine, "", "", "0 keeps none on disk."},
	{"clipboard.programs", SecScroll, "A program writing your clipboard", KindChoice, "off|ask|on", "ask", StoreEngine, "", "", ""},
	{"shell", SecScroll, "What a new pane starts", KindWords, "", "", StoreEngine, "", AsksRuns, "Empty is your login's own shell."},

	{"fleets.mode", SecFleets, "Fleet Mode", KindSwitch, "", "off", StoreConfig, "", "", "On: a new job is an orchestrator with fleets under it. Takes hold at the next job."},
	{"fleets.leaders", SecFleets, "Fleet leaders at once", KindNumber, fleetLimit, "4", StoreConfig, "", "", "How many fleets may run side by side. Takes hold at the next start; nothing running is stopped."},
	{"fleets.agents", SecFleets, "Agents in one fleet", KindNumber, fleetLimit, "8", StoreConfig, "", "", "The leader is not counted. Takes hold at the next start."},
	{"fleets.total", SecFleets, "Agents in all", KindNumber, fleetLimit, "20", StoreConfig, "", "", "Leaders and workers of every fleet together. On a shared server the administrator's limit wins if lower."},
	{"fleets.leader", SecFleets, "A leader's tool", KindTool, "", DefaultTool, StoreConfig, "", "", "When the orchestrator names none."},
	{"fleets.worker", SecFleets, "A worker's tool", KindTool, "leader", FleetWorkerLeader, StoreConfig, "", "", "When its leader names none; leader is the leader's own."},

	{"plan.first", SecGuards, "Plan first, then start", KindSwitch, "", "off", StoreConfig, "", "", "Off: your orchestrator starts agents as soon as it has a plan. On: it must show you the plan first (which fleets, which tasks, how many agents, which tool), and nothing starts until you approve it. What it adds later is shown the same way."},
	{"protect.files", SecGuards, "Protected files", KindSwitch, "", "off", StoreConfig, "", "", "Off: every change is checked and landed like any other. On: when a job changes a file on your list, the work goes on as usual, but the job cannot be landed until you have looked at those files and approved them. Agents are never slowed or stopped by this."},
	{"protect.paths", SecGuards, "Your protected list", KindList, "", ProtectDefault, StoreConfig, "", "", "Paths and patterns with commas between, for every project of yours: db/migrations/**, .env*, deploy/**. Two stars reach into folders."},
	{"spend.guard", SecGuards, "Spending guard", KindSwitch, "", "off", StoreConfig, "", "", "Off: a job runs until it is done. On: WhaleShark counts agent-hours (one agent open for one hour) and, at your limit, holds the job's agents where they are and asks you whether to go on. Tokens and money are not counted: agent tools do not report them reliably."},
	{"spend.job", SecGuards, "Agent-hours a job may use", KindNumber, "1..|unlimited", "40", StoreConfig, "", "", "At four fifths the orchestrator is told to wrap up. At the limit the job is held and you are asked. Eight agents for five hours are forty."},
	{"spend.fleet", SecGuards, "Agent-hours one fleet may use", KindNumber, "1..|unlimited", "unlimited", StoreConfig, "", "", "The same for one fleet alone; the other fleets go on."},
	{"stuck.watch", SecGuards, "Stuck detector", KindSwitch, "", "on", StoreConfig, "", "", "On: WhaleShark notices an agent that is going nowhere (long work with nothing to show, the same check failing again and again, the same note repeated) and tells its leader, then the orchestrator, then you. It never stops an agent by itself. Off: you find out when you look."},
	{"stuck.climb_minutes", SecGuards, "Minutes before it is passed up", KindNumber, "1..600", "10", StoreConfig, "", "", "How long whoever was told has to deal with it before the level above hears: first the leader, then the orchestrator, then you."},

	{"tools.<name>.label", SecTools, "Its name in words", KindText, "", "", StoreTools, "label", "", ""},
	{"tools.<name>.run", SecTools, "The command that starts it", KindWords, "", "", StoreTools, "run", AsksRuns, "The program as a full path, then its first arguments. Never a line for a shell."},
	{"tools.<name>.model", SecTools, "How a model is named", KindWords, "", "", StoreTools, "model", "", "With {model} where the name goes; empty when the tool has no such option."},
	{"tools.<name>.folder", SecTools, "How one more folder is given", KindWords, "", "", StoreTools, "folder", "", "With {dir} where the folder goes."},
	{"tools.<name>.each", SecTools, "That option before every folder", KindSwitch, "", "off", StoreTools, "each", "", ""},
	{"tools.<name>.resume", SecTools, "How a conversation is continued", KindWords, "", "", StoreTools, "resume", "", "With {session}; empty and it starts afresh."},
	{"tools.<name>.hooks", SecTools, "Whose hooks it reads", KindChoice, "claude|codex|none", "none", StoreTools, "hooks", "", "With hooks its states are exact and Stop all can hold it; without, it is busy or quiet."},
	{"tools.<name>.rules", SecTools, "The file it reads when it starts", KindText, "", "", StoreTools, "rules", "", "From your home folder, as .claude/CLAUDE.md or .codex/AGENTS.md: init writes the short rules there, so its agents know their part."},
	{"tools.<name>.suits", SecTools, "What it suits", KindText, "", "", StoreTools, "suits", "", "Your sentence; the orchestrator reads it when it chooses a tool."},
	{"tools.<name>.screen.title", SecTools, "Its title while a chat has no name", KindText, "", "", StoreTools, "screen.title", "", "With it the main tab follows a name you give the chat."},
	{"tools.<name>.tried", SecTools, "Tried on this computer", KindList, "", "", StoreTools, "tried.saw", AsksShown, "What WhaleShark saw when it last started this tool and gave it one line. Press Try it again after you change the command."},
	{"tools.<name>.screen.ready", SecTools, "Its line while it waits", KindText, "", "", StoreTools, "screen.ready", "", ""},
	{"tools.<name>.screen.working", SecTools, "Its line while it works", KindText, "", "", StoreTools, "screen.working", "", ""},
	{"tools.<name>.screen.question", SecTools, "Two phrases of an open question", KindList, "", "", StoreTools, "screen.question", "", ""},
	{"tools.<name>.screen.trust", SecTools, "Its question about a folder", KindText, "", "", StoreTools, "screen.trust", "", ""},
	{"tools.<name>.env.<VAR>", SecTools, "A variable it is started with", KindText, "", "", StoreTools, "env", AsksRuns, "For a local model: its address, a key. Shown as dots; nothing takes it away."},

	{"worktrees.dir", SecProject, "Where copies of the code are made", KindText, "", ".whaleshark/worktrees", StoreProject, "", AsksApproval, "A folder; every task works in a copy of the code under it."},
	{"worktrees.branch_prefix", SecProject, "What a task's branch begins with", KindText, "", "whaleshark/", StoreProject, "", "", ""},
	{"worktrees.share", SecProject, "Files shared into a copy", KindList, "", "", StoreProject, "", AsksApproval, "Files git does not hold that every copy needs, linked in from the project."},
	{"worktrees.port_block", SecProject, "Ports a task gets", KindNumber, "1..100", "10", StoreProject, "", "", ""},
	{"setup.script", SecProject, "The setup script", KindText, "", "", StoreProject, "", AsksApproval, "Run in every new copy of the code."},
	{"setup.script_windows", SecProject, "The setup script on Windows", KindText, "", "", StoreProject, "", AsksApproval, "Run in every new copy of the code where the system is Windows."},
	{"setup.timeout_seconds", SecProject, "Seconds the setup may take", KindNumber, "1..86400", "600", StoreProject, "", "", ""},
	{"agent.args", SecProject, "Arguments its agents are started with", KindList, "", "", StoreProject, "", AsksApproval, "Added to the command of every agent of the tool below that is started in this project."},
	{"agent.tool", SecProject, "The tool those arguments are for", KindTool, "", DefaultTool, StoreProject, "", AsksApproval, "An agent of any other tool is started without them: what one tool takes, another refuses."},
	{"check.timeout_seconds", SecProject, "Seconds a check may take", KindNumber, "1..86400", "900", StoreProject, "", AsksApproval, "A check that runs longer is ended and counts as failed."},
	{"land.who", SecProject, "Who may land the work", KindChoice, "human|orchestrator", "human", StoreProject, "", AsksApproval, "human: only you land. orchestrator: your lead agent may land too, when the check passes."},
	{"land.check", SecProject, "The check before landing", KindText, "", "", StoreProject, "", AsksApproval, "A command run on the collected work; nothing lands unless it passes."},
	{"limits.agents", SecProject, "Agents in one run", KindNumber, "1..1000", "20", StoreProject, "", AsksApproval, "A fleet's run takes the lower of this and your own limit for a fleet."},
	{"limits.quiet_seconds", SecProject, "Seconds before an agent is quiet", KindNumber, "1..86400", "120", StoreProject, "", "", ""},
	{"limits.resume_per_second", SecProject, "Agents woken a second", KindNumber, "1..100", "2", StoreProject, "", "", ""},
	{"limits.stale_minutes", SecProject, "Minutes before progress is stale", KindNumber, "1..10000", "30", StoreProject, "", "", ""},
	{"limits.max_minutes", SecProject, "Minutes a task may take", KindNumber, "0..100000", "0", StoreProject, "", "", "0 holds no line."},
	{"protect.also", SecProject, "This project's additions", KindList, "", "", StoreProject, "", "", "More protected paths for everyone who works in this project. A project can add to your list and never take from it."},

	{"server.agents", SecServer, "Agents a login may run", KindNumber, "", "", StoreServer, "agents", AsksAdmin, ""},
	{"server.road", SecServer, "How phones reach the page", KindText, "", "", StoreServer, "road", AsksAdmin, "At a public name behind Cloudflare's login: the one road offered for now."},
	{"server.notice", SecServer, "A notice of a new version", KindSwitch, "", "on", StoreServer, "notice", AsksAdmin, "On: the administrator's server status asks once a day whether a newer release is out. No login's program asks on a server."},
	{"server.team", SecServer, "The team at Cloudflare", KindText, "", "", StoreServer, "team", AsksAdmin, ""},
	{"server.rules", SecServer, "The port rules", KindText, "", "", StoreServer, "rules", AsksAdmin, ""},
	{"server.login", SecServer, "The logins and their ports", KindText, "", "", StoreServer, "login", AsksAdmin, ""},
}

// AdminLine is what a row of the administrator's shows in a value's place,
// and Dots what every screen and `set` show for a variable of a tool.
const (
	AdminLine = "the administrator's: sudo whaleshark server status"
	Dots      = "••••••"
)

// ProtectDefault is the list protected files starts with.
const ProtectDefault = ".env*,**/migrations/**,**/secrets/**,.github/workflows/**,deploy/**"

// asking are the keys that hold agents or are how the person is called, by
// name or by what a name begins with; the command key is how the person
// reaches Stop all and every other shortcut.
var asking = []string{"fleets.", "plan.", "protect.", "spend.", "stuck.", "nudge.", "notify.", "dnd", "mute", "ui.settle_seconds", "keys.command"}

// Agent is what `set` does with this key for the agent the person talks to
// (the lead agent, the orchestrator, a pane bound to no job): SetFree,
// SetAsks or SetPerson. A fleet leader and a worker set nothing. Theirs
// alone is everything that names a program or reaches outside, every key of
// a project's and of the server's file, of a tool's description all but
// its label and what it suits, and whether a program may write the
// person's clipboard, which is outside the tool.
func (k *Key) Agent() string {
	own := k.Name == "tools.<name>.label" || k.Name == "tools.<name>.suits"
	switch {
	case k.Asks != "" || k.Store == StoreProject || k.Store == StoreServer || k.Store == StoreTools && !own || k.Name == "clipboard.programs":
		return SetPerson
	case slices.ContainsFunc(asking, func(a string) bool { return k.Name == a || strings.HasSuffix(a, ".") && strings.HasPrefix(k.Name, a) }):
		return SetAsks
	}
	return SetFree
}

// UnlimitedWarning is what choosing unlimited for this key says first, a
// line each, and nothing for any other key: the settings screen asks with
// it once, and `set` prints it to the error stream and takes the value.
// machine is how many agents doctor reckons this computer's memory holds
// and cap what a server allows the login, 0 for unknown and for no server;
// guard is whether the spending guard is on.
func (k *Key) UnlimitedWarning(machine, cap int, guard bool) []string {
	switch {
	case !strings.HasSuffix(k.Values, "|unlimited"):
		return nil
	case strings.HasPrefix(k.Name, "spend."):
		return []string{"No limit on agent-hours. The guard will not hold this job, whatever it uses."}
	}
	memory := "this computer's memory"
	if machine > 0 {
		memory += fmt.Sprintf(" (about %d agents here)", machine)
	}
	lines := []string{"No limit on " + strings.ToLower(k.Label[:1]) + k.Label[1:] + ".",
		"WhaleShark will start as many agents as it is asked for. What still stands in the way: " + memory + " and your plan's usage."}
	if cap > 0 {
		lines = append(lines, fmt.Sprintf("This server allows you %d.", cap))
	}
	if !guard {
		lines = append(lines, "The spending guard is off.")
	}
	return lines
}

// FindKey is the row of a name `set` was given and, for a row that stands
// for many, the names the person gave: "tools.hermes.env.API" is the row
// "tools.<name>.env.<VAR>" with hermes and API.
func FindKey(name string) (key *Key, given []string) {
	words := strings.Split(name, ".")
next:
	for i := range Keys {
		pattern := strings.Split(Keys[i].Name, ".")
		if len(pattern) != len(words) {
			continue
		}
		given = nil
		for j, p := range pattern {
			if strings.HasPrefix(p, "<") && words[j] != "" {
				given = append(given, words[j])
			} else if p != words[j] {
				continue next
			}
		}
		return &Keys[i], given
	}
	return nil, nil
}

// Check holds a value to its key and returns it as it is stored: a choice
// is one of its values, a number lies between its least and its most or is
// the word unlimited where that is one, a switch is on, off or toggle, a
// tool is wired in, and nothing holds a line break.
func (k *Key) Check(value string, tools []Tool) (string, error) {
	choices := strings.Split(k.Values, "|")
	bad := func(format string, a ...any) (string, error) {
		return "", fmt.Errorf("%s: "+format, append([]any{k.Name}, a...)...)
	}
	switch k.Kind {
	case KindChoice:
		if until, ok := strings.CutPrefix(value, "until "); !slices.Contains(choices, value) && !(ok && until != "" && slices.Contains(choices, "until TIME")) {
			return bad("one of %s", strings.Join(choices, ", "))
		}
	case KindSwitch:
		if value != On && value != Off && value != "toggle" {
			return bad("on or off")
		}
	case KindNumber:
		least, most, _ := strings.Cut(choices[0], "..")
		lo, _ := strconv.Atoi(least)
		hi, err := strconv.Atoi(most)
		if err != nil {
			hi = 1 << 30
		}
		if n, err := strconv.Atoi(value); value == "unlimited" && slices.Contains(choices, value) {
			return strconv.Itoa(Unlimited), nil
		} else if err == nil && slices.Contains(choices[1:], value) { // a figure beside the range, as 0 for "as it comes"
		} else if err != nil || n < lo || n > hi {
			return bad("a number, %s or more%s", least, map[bool]string{true: ", or unlimited", false: " and " + most + " at most"}[most == ""])
		}
	case KindTool:
		if _, wired := FindTool(tools, value); value == "" || !wired && !slices.Contains(choices, value) {
			return bad("no tool called %.20q is wired in", value)
		}
	}
	if strings.ContainsFunc(value, steers) {
		return bad("one line, with no control character")
	}
	if patterns := List(value); strings.HasPrefix(k.Name, "protect.") && value != "" {
		for i, p := range patterns {
			if patterns[i] = strings.TrimSpace(p); patterns[i] == "" || !filepath.IsLocal(filepath.FromSlash(patterns[i])) || patterns[i][0] == '-' {
				return bad("patterns for paths inside a project, with commas between: %.40q is none", p)
			} else if _, err := path.Match(patterns[i], ""); err != nil {
				return bad("%.40q is a pattern that matches nothing: a bracket or a backslash in it is not closed", p)
			}
		}
		value = strings.Join(patterns, ",")
	}
	return value, nil
}

// leaf is the field of the struct v points at that a dotted path of tag
// names leads to.
func leaf(v reflect.Value, tag, path string) (reflect.Value, bool) {
	v = v.Elem()
walk:
	for _, name := range strings.Split(path, ".") {
		for i := 0; v.Kind() == reflect.Struct && i < v.NumField(); i++ {
			if n, _, _ := strings.Cut(v.Type().Field(i).Tag.Get(tag), ","); n == name {
				v = v.Field(i)
				continue walk
			}
		}
		return v, false
	}
	return v, true
}

func (k *Key) field(file any) (reflect.Value, bool) {
	tag, path := "toml", k.Path
	if k.Store == StoreUI {
		tag = "json"
	}
	if path == "" {
		path = k.Name
	}
	return leaf(reflect.ValueOf(file), tag, path)
}

// Of is the key's value in file, a pointer to what its store is read into
// (PersonConfig, Settings, UIFile, ProjectFile, Server, Tool), as `set`
// takes it; empty for a key that stands for many.
func (k *Key) Of(file any) string {
	f, ok := k.field(file)
	if !ok {
		return ""
	}
	switch v := f.Interface().(type) {
	case bool:
		return map[bool]string{true: On, false: Off}[v]
	case int:
		if v == Unlimited && strings.HasSuffix(k.Values, "|unlimited") {
			return "unlimited"
		}
		return strconv.Itoa(v)
	case string:
		if v == "" && k.Kind == KindSwitch {
			return k.Default
		}
		return v
	case []string:
		return strings.Join(v, map[bool]string{true: ",", false: " "}[k.Kind == KindList])
	}
	return ""
}

// put writes a checked value into the key's field of file.
func (k *Key) put(file any, value string) error {
	f, ok := k.field(file)
	if !ok {
		return fmt.Errorf("%s has no place in its file", k.Name)
	}
	switch f.Interface().(type) {
	case bool:
		f.SetBool(value == On || value == "toggle" && !f.Bool())
	case int:
		n, _ := strconv.Atoi(value)
		f.SetInt(int64(n))
	case string:
		if value == "toggle" {
			value = map[bool]string{true: Off, false: On}[k.Of(file) == On]
		}
		f.SetString(value)
	case []string:
		if words := strings.Fields(value); k.Kind == KindList && value == "" {
			f.Set(reflect.ValueOf([]string(nil)))
		} else if k.Kind == KindList {
			list := List(value)
			for i := range list {
				list[i] = strings.TrimSpace(list[i])
			}
			f.Set(reflect.ValueOf(list))
		} else {
			f.Set(reflect.ValueOf(words))
		}
	default:
		return fmt.Errorf("%s is not set this way", k.Name)
	}
	return nil
}

// noProjectSet is the writer of the project's file until its package is built.
func noProjectSet(root string, key *Key, value string) error { return ErrNotBuilt }

// Current is a key's value as it stands in the login's own files, as `set`
// takes it: empty for a key of a project or of the server, and Dots for a
// variable of a tool.
func Current(p Platform, d Dirs, name string) string {
	key, given := FindKey(name)
	switch {
	case key == nil:
	case key.Store == StoreConfig:
		cfg, _ := ReadPerson(p.Peek, d.Config)
		return key.Of(&cfg)
	case key.Store == StoreEngine:
		s, _ := ReadSettings(p.Peek, d)
		return key.Of(&s)
	case key.Store == StoreUI:
		var ui UIFile
		ReadVersioned(p.Peek, filepath.Join(d.State, UIFileName), FileVersion, &ui)
		return key.Of(&ui)
	case key.Store == StoreTools && len(given) == 2:
		return Dots
	case key.Store == StoreTools:
		tools, _ := Tools(p, d)
		t, _ := FindTool(tools, given[0])
		return key.Of(&t)
	}
	return ""
}

// asked puts an agent's change of a key that holds agents, or is how the
// person is called, in front of the person and changes nothing: the item
// shows the setting's own description, from what to what. Once the answer
// has settled, whoever uses it calls Set again as the person.
func asked(c *Call, d Dirs, key *Key, name, value string) (string, error) {
	was := Current(c.Kit.Platform, d, name)
	if c.Run == "" {
		return "", &Refusal{Exit: ExitRefused, Code: RefusedPersonOnly, Message: name + " waits for the person's own click, and no job is open here to ask them in: it is theirs to set, in Settings or typed in a tab of their own."}
	}
	err := c.Kit.Store.Change(c.Root, c.Run, func(s *State) error {
		_, err := c.Kit.Rules.SetAsked(s, SetAsk{Key: name, From: was, To: value}, key.Label+": "+key.Help, c.Now)
		return err
	})
	if err == nil && c.Out != nil {
		fmt.Fprintf(c.Out, "%s is %s: waiting for the person's click. Run: whaleshark wait\n", name, was)
	}
	return was, err
}

// Granted makes the change of an item of CauseSetting once its answer has
// settled as AnswerChange: Set as the person, from where their click came.
func Granted(k *Kit, root string, ask SetAsk, by Origin, now time.Time) error {
	_, err := Set(&Call{Kit: k, Root: root, Caller: Caller{Kind: Human, Where: by.Where}, Flags: map[string][]string{}, Now: now}, ask.Key, ask.To)
	return err
}

// Set changes one setting, by a key of Keys, in the file it lies in, and
// answers the value as it now stands. It is all `set` does for a key it has
// no way of its own for. A key that runs a program is the person's alone; a
// project's key goes to the kit's ProjectSet, with --project and from the
// person; the administrator's is refused with the line to type. What the
// keeper reads it notices by itself. The ways `set` has of its own (a time
// for dnd, a pane's own marks, moving an open pane) stay where they are.
//
// For a caller that is not the person Key.Agent decides: a key of theirs
// alone is refused, one that holds agents or calls them becomes an item and
// is not changed (asked), and any other is set and kept in UIFile.ByAgent,
// which every screen shows until the person has seen that section. A fleet
// leader and a worker never get here: the command table refuses them.
// Unlimited is taken, with its warning on the error stream.
func Set(c *Call, name, value string) (string, error) {
	p, agent := c.Kit.Platform, c.Caller.Kind != Human
	key, given := FindKey(name)
	refuse := func(exit int, code, format string, a ...any) (string, error) {
		return "", &Refusal{Exit: exit, Code: code, Message: fmt.Sprintf(format, a...), Next: []string{"whaleshark help set"}}
	}
	d, err := p.Dirs()
	switch project := c.Flags["project"] != nil; {
	case err != nil:
		return "", err
	case key == nil && strings.HasPrefix(name, "tools.") && value == "remove" && !agent:
		return value, RemoveTool(d, strings.TrimPrefix(name, "tools."))
	case key == nil:
		return refuse(ExitUsage, "usage", "There is no setting %q.", name)
	case key.Asks == AsksAdmin:
		return refuse(ExitRefused, "admin_only", "%s is %s", name, AdminLine)
	case key.Asks == AsksShown:
		return refuse(ExitUsage, "usage", "%s is shown, not set: whaleshark doctor --try NAME writes it.", name)
	case agent && (project || key.Agent() == SetPerson):
		return refuse(ExitRefused, "human_only", "%s is the person's to set, not an agent's.", name)
	case project != (key.Store == StoreProject):
		return refuse(ExitUsage, "usage", "%s is %s.", name, map[bool]string{true: "a setting of your own: leave --project out", false: "this project's: add --project"}[project])
	}
	tools, _ := Tools(p, d)
	was := Current(p, d, name)
	if value, err = key.Check(value, tools); err != nil {
		return refuse(ExitUsage, "usage", "%v.", err)
	} else if agent && key.Agent() == SetAsks {
		return asked(c, d, key, name, value)
	}
	if cfg, _ := ReadPerson(p.Peek, d.Config); value == strconv.Itoa(Unlimited) && c.Err != nil {
		server, _ := ReadServer(p.Peek)
		for _, line := range key.UnlimitedWarning(c.Kit.Machine(), server.Cap(Me()), cfg.Spend.Guard) {
			fmt.Fprintln(c.Err, line)
		}
	}
	var b bytes.Buffer
	switch key.Store {
	case StoreProject:
		return value, c.Kit.ProjectSet(c.Root, key, value)
	case StoreUI:
		var ui UIFile
		err = change(p, UIFileName, &ui, func() error {
			ui.Version = FileVersion
			return key.put(&ui, value)
		})
		value = key.Of(&ui)
	case StoreTools:
		t, _ := FindTool(tools, given[0])
		if t.Name = given[0]; t.Hooks == "" {
			t.Hooks = HooksNone
		}
		if t.Env = maps.Clone(t.Env); len(given) == 2 && value == "" {
			delete(t.Env, given[1])
		} else if len(given) == 2 {
			if t.Env == nil {
				t.Env = map[string]string{}
			}
			t.Env[given[1]], value = value, Dots
		} else if err = key.put(&t, value); err != nil {
			return "", err
		} else if key.Path == "rules" && value != was {
			if err = t.rulesFree(p); err != nil {
				return refuse(ExitRefused, "rules_taken", "%s: %v.", name, err)
			}
		}
		err = WriteTool(p, d, t)
	case StoreConfig:
		var cfg PersonConfig
		if cfg, err = ReadPerson(p.Read, d.Config); err == nil {
			err = key.put(&cfg, value)
		}
		if value = key.Of(&cfg); err == nil {
			err = toml.NewEncoder(&b).Encode(cfg)
		}
		if err == nil {
			err = replaceFile(p, filepath.Join(d.Config, ConfigFile), b.Bytes())
		}
	case StoreEngine:
		var s Settings
		if s, err = ReadSettings(p.Read, d); err != nil {
			return "", err
		}
		if len(given) == 1 { // a shortcut: its key, and the command it runs or nothing
			s.Shortcuts = slices.DeleteFunc(s.Shortcuts, func(e KeyEntry) bool { return e.Key == given[0] })
			if argv := strings.Fields(value); len(argv) > 0 {
				s.Shortcuts = append(s.Shortcuts, KeyEntry{Key: given[0], Type: "shell", Argv: argv})
			}
		} else if err = key.put(&s, value); err != nil {
			return "", err
		} else {
			value = key.Of(&s)
		}
		if err = toml.NewEncoder(&b).Encode(s); err == nil {
			err = replaceFile(p, SettingsPath(d), b.Bytes())
		}
	}
	// A key that holds agents, changed from a phone, is flagged like one changed away from the computer.
	if err == nil && (key.Asks == AsksRuns || agent || c.Caller.Where == WherePhone && key.Agent() == SetAsks) {
		var ui UIFile
		err = change(p, UIFileName, &ui, func() error {
			if ui.Version = FileVersion; agent {
				ui.ByAgent = append(slices.DeleteFunc(ui.ByAgent, func(a SetAsk) bool { return a.Key == name }), SetAsk{Key: name, From: was, To: value})
			} else if !slices.Contains(ui.Changed, name) {
				ui.Changed = append(ui.Changed, name)
			}
			return nil
		})
	}
	return value, err
}
