package contract

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// ErrNotBuilt is what a stand-in answers where it cannot answer with nothing.
var ErrNotBuilt = errors.New("not built yet")

// ErrNoRun is what the store answers for a run that does not exist.
var ErrNoRun = errors.New("no such run")

// Reader is the part of the store the panes, the page and the views use.
type Reader interface {
	// Read loads a run under the shared lock. A file newer than this program
	// is ErrNewer; a run that does not exist is ErrNoRun.
	Read(root, run string) (*State, error)
	// Runs lists the ids of a project's runs, oldest first.
	Runs(root string) ([]string, error)
	// Current is the run a person's commands address by default.
	Current(root string) (string, error)
	// History returns the acknowledged events and the audit trail of a run.
	History(root, run string) ([]Event, error)
	// Dir is the folder of a run; with an empty run, the project's own folder of ours.
	Dir(root, run string) string
}

// Store is the one writer path. Only a command walks it.
type Store interface {
	Reader
	// Create writes a new run and its folder.
	Create(root string, s *State) error
	// Change loads the run under the exclusive lock, applies fn and saves.
	// When fn returns an error nothing is written. fn calls nothing outside.
	Change(root, run string, fn func(*State) error) error
	// Append adds acknowledged events to the run's history.
	Append(root, run string, events []Event) error
	// SetCurrent rewrites the current pointer.
	SetCurrent(root, run string) error
	// Remove deletes a closed run's folder.
	Remove(root, run string) error
	// Both is Change for two runs of one project under its one lock: an
	// orchestrator's run, up, and a fleet's, low. Nobody reads or writes
	// either between the two loads and the two saves, and up is saved first.
	// A program killed between the saves leaves up changed and low not: what
	// lies in both (a question and its twin) is finished by the next sweep.
	Both(root, up, low string, fn func(up, low *State) error) error
}

// Snapshot is the terminals' picture of every pane at one moment. Instance is
// new at every start of the keeper, so a restart is seen without inference;
// Seq is the number of the last event the picture holds.
type Snapshot struct {
	At       time.Time `json:"at"`
	Instance string    `json:"instance,omitempty"`
	Seq      uint64    `json:"seq,omitempty"`
	Panes    []Pane    `json:"panes"`
	// Spaces are the workspaces, in the order they were opened.
	Spaces []Space `json:"spaces,omitempty"`
}

// Space is one workspace as the keeper keeps it: a project's folder (empty
// for the tabs of no project), the name the left section shows, whether a
// person gave it, the tab it shows when it is in front, and whether it is.
// An ID ("s1", "s2", …) is never given twice. Every tab is of one workspace
// from the moment it is opened until it is closed; one workspace is in
// front, and the tab row, the tab that shows and the keys are its own. The
// keeper reads no run file and no list of projects: Root is what the caller
// said, checked to be a folder. A workspace ends with its last tab.
type Space struct {
	ID    string `json:"id"`
	Root  string `json:"root,omitempty"`
	Name  string `json:"name"`
	Named bool   `json:"named,omitempty"`
	Tab   string `json:"tab,omitempty"`
	Front bool   `json:"front,omitempty"`
}

// OtherTabs is the name of the workspace without a folder, and LabelMost
// the longest label.
const (
	OtherTabs = "other tabs"
	LabelMost = 24
)

// SpaceName is the name a workspace has until a person gives it one, and
// again after an empty name: its folder's.
func SpaceName(root string) string {
	if root == "" {
		return OtherTabs
	}
	return Label(filepath.Base(root))
}

// Label is a label as the keeper keeps it, a tab's or a workspace's: one
// line without what steers a terminal or turns the reading direction, the
// blanks round it gone, never the form of an option, LabelMost characters
// at most. A label is a label only: no call takes one as an argument, a
// path or a pane's name.
func Label(text string) string {
	text = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if steers(r) {
			return ' '
		}
		return r
	}, text)), " ")
	r := []rune(strings.TrimSpace(strings.TrimLeft(text, "- ")))
	return strings.TrimSpace(string(r[:min(len(r), LabelMost)]))
}

// IsMainTab says a label reads as the main agent's tab's, in any letter case
// and with any blanks round it: in one workspace one tab at most has it.
func IsMainTab(label string) bool { return strings.EqualFold(strings.TrimSpace(label), MainTab) }

// Pane is one pane as the terminals show it. Agent is empty when the pane
// holds a bare shell. Status is one of the Status constants. Terminal is new
// each time the pane's program is started; Workspace is the ID of the Space
// its tab is of, and stays what it was when the tab was opened whatever
// folder the pane goes to (empty for the dock, which is beside every one).
type Pane struct {
	ID        string `json:"pane"`
	Tab       string `json:"tab"`
	Workspace string `json:"workspace"`
	Terminal  string `json:"terminal"`
	Label     string `json:"label"`
	Cwd       string `json:"cwd"`
	Focused   bool   `json:"focused,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Name      string `json:"name,omitempty"`
	Session   string `json:"session,omitempty"`
	Status    string `json:"status"`
	// Ours marks a pane program of our own, which the keeper started from
	// its list of arguments; no program can set it from inside a pane.
	Ours bool `json:"ours,omitempty"`
	// Dock is true for the one pane of the dock: the left section's place,
	// which is in no tab (Tab is empty) and shows beside every tab.
	Dock bool `json:"dock,omitempty"`
}

// What an agent is doing. An agent that has finished is idle. Busy and
// quiet are the engine's honest words for a program whose output is all
// there is to go by; a rule that does not know them reads neither as idle
// nor as working.
const (
	StatusBusy    = "busy"
	StatusQuiet   = "quiet"
	StatusWorking = "working"
	StatusIdle    = "idle"
	StatusBlocked = "blocked"
	StatusUnknown = "unknown"
)

// The kinds of TermEvent. EvRenamed and EvTabClosed carry the tab's id, and
// EvRenamed its new label; every other kind carries the pane's whole record
// as it is after the event, or as it last was when the pane is gone.
const (
	EvOpened    = "pane-opened"
	EvClosed    = "pane-closed"
	EvState     = "state"
	EvFocus     = "focus"
	EvRenamed   = "renamed"
	EvFolder    = "folder"
	EvSession   = "session"
	EvTabClosed = "tab-closed"
	// EvWindow says the window took another width; it names no pane and
	// changes no picture. The left section fits the two panes again by it.
	EvWindow = "window"
	// EvSpace: a workspace was made, named, put in front or ended. Spaces
	// is the whole list after it, and Pane is empty.
	EvSpace = "space"
	// EvMenu: a person asked for the menu of the tab Pane.Tab, at the
	// window's cell X, Y, by a right press on the tab row or by the key for
	// the tab that shows; asked for on a fleet's name, the tab is the
	// fleet's first, its leader's, whichever tab a click there would show.
	// It asks nothing by itself and changes no picture:
	// only the left section acts on it, by opening a menu there (OverlayAt),
	// in which a person must still choose.
	EvMenu = "menu"
)

// TermEvent is one change, in the order it happened: Seq counts up by one.
type TermEvent struct {
	Seq    uint64  `json:"seq"`
	Kind   string  `json:"kind"`
	Pane   Pane    `json:"pane"`
	Spaces []Space `json:"spaces,omitempty"`
	X      int     `json:"x,omitempty"`
	Y      int     `json:"y,omitempty"`
}

// Apply brings a picture up to date with one event of Events.
func Apply(s *Snapshot, e TermEvent) {
	if e.Kind == EvSpace {
		s.Spaces = slices.Clone(e.Spaces)
	}
	if e.Kind == EvWindow || e.Kind == EvSpace || e.Kind == EvMenu {
		s.Seq = max(s.Seq, e.Seq)
		return
	}
	byID := func(a, b Pane) int { return strings.Compare(a.ID, b.ID) }
	panes, found := make([]Pane, 0, len(s.Panes)+1), false
	for _, p := range s.Panes {
		switch e.Kind {
		case EvClosed:
			if p.ID == e.Pane.ID {
				continue
			}
		case EvTabClosed:
			if p.Tab == e.Pane.Tab {
				continue
			}
		case EvRenamed:
			if p.Tab == e.Pane.Tab {
				p.Label = e.Pane.Label
			}
		default:
			if e.Pane.Focused {
				p.Focused = false
			}
			if p.ID == e.Pane.ID {
				p, found = e.Pane, true
			}
		}
		panes = append(panes, p)
	}
	if e.Kind != EvClosed && e.Kind != EvTabClosed && e.Kind != EvRenamed && !found {
		panes = append(panes, e.Pane)
		slices.SortFunc(panes, byID)
	}
	s.Panes, s.Seq = panes, max(s.Seq, e.Seq)
}

// KeyEntry is one shortcut in the settings: Type is "shell" or "popup".
type KeyEntry struct {
	Key  string   `toml:"key"`
	Type string   `toml:"type"`
	Argv []string `toml:"argv"`
}

// The terminals' refusals that callers tell apart; every other is just an
// error. ErrAgentNotReady: an agent stopped at a prompt while starting, as
// opposed to a start that failed; also said of a prompt or a pointer for an
// agent that is no longer in front in its pane. ErrAgentBlocked: the agent is
// at a prompt, so nothing is typed. ErrPromptStalled: the prompt was typed
// and the agent did not start on it. ErrNoPane: the pane or its agent is
// gone. ErrEngineUnreachable: the terminals cannot be reached at all.
var (
	ErrAgentNotReady     = errors.New("agent_not_ready")
	ErrAgentBlocked      = errors.New("agent_blocked")
	ErrPromptStalled     = errors.New("agent_prompt_stalled")
	ErrNoPane            = errors.New("no such pane or agent")
	ErrEngineUnreachable = errors.New("the engine is not running")
	// ErrDock: the call would close, split, swap, zoom or start a program
	// in the dock, which runs the left section and nothing else.
	ErrDock = errors.New("the left section's place is the engine's")
	// ErrSpace: the wall between workspaces (Walled). ErrNotFront: a screen
	// of ours was asked over a tab of a workspace that is not in front.
	// ErrMainTab: a label that reads as the main agent's tab's was given
	// to a second tab of one workspace.
	ErrSpace    = errors.New("that tab is another workspace's")
	ErrNotFront = errors.New("that tab is not in front")
	ErrMainTab  = errors.New("a workspace has one tab named " + MainTab)
)

// What Dock is told: DockKeys gives the dock the keys, DockBack gives them
// back to the pane that had them, and DockFold folds the dock to its rail or
// opens it again.
const (
	DockKeys = "keys"
	DockBack = "back"
	DockFold = "fold"
)

// The directions of Split, Resize and PaneFocus, and what Notify answers.
const (
	Right, Down, Left, Up = "right", "down", "left", "up"

	NotifyShown    = "shown"
	NotifyNoWindow = "no_window"
	NotifyOff      = "off"
)

// Terminals is the only way to the panes and what runs in them: the engine's
// keeper. It has no call that sends a key to an agent.
type Terminals interface {
	Version() (string, error)
	Snapshot(ctx context.Context) (*Snapshot, error)
	// Events takes one snapshot, then delivers every change in order until
	// the keeper stops, which closes the channel.
	Events(ctx context.Context) (*Snapshot, <-chan TermEvent, error)
	// TabCreate opens a tab without focusing it; env is KEY=VALUE. A tab
	// opened from a pane is of that pane's workspace and its group, whatever
	// the call says; else it is of the workspace of the project's folder
	// space names, made where there is none and not put in front, or with
	// none named of the one in front. group names the fleet a leader's tab
	// is the first of; every tab opened from a pane of a group has that
	// group. The keeper is told a tab's fleet by this one word and reads no
	// run file for it.
	TabCreate(space, group, cwd, label string, env []string) (Pane, error)
	// TabRename gives a tab the label a person typed: Label(label), kept
	// from then on, so that the tab follows its chat's name no longer (the
	// label MainTab lets it follow again). ErrMainTab where another tab of
	// the same workspace reads as MainTab. The keeper's name for an agent
	// (Pane.Name) is the attempt's own and is not changed by any rename.
	TabRename(tab, label string) error
	// TabFocus shows a tab, and PaneFocus gives a pane the keys: the tab's
	// workspace comes to the front with it.
	TabFocus(tab string) error
	TabClose(tab string) error
	// TabGive makes pane the one that opened tab, as if it had: what a
	// leader started again may then do with the tabs of the leader before.
	TabGive(tab, pane string) error
	AgentStart(name, kind, pane string, args []string, timeout time.Duration) error
	// Prompt delivers an agent's first prompt and waits until it is working.
	Prompt(name, text string, timeout time.Duration) error
	// Point types one of the fixed one-line pointers into an agent's pane.
	Point(pane string, p Pointer, arg string) error
	// Say types a person's note as one line, Said(where, text), into the pane
	// of an agent that was started by hand and is at rest, and nowhere else.
	// ErrPromptStalled: typed and not yet taken. Any other error: not typed.
	Say(pane, where, text string) error
	// Run runs a command of ids and tool-made paths in a pane. The keeper
	// starts it there from the list, in place of the pane's shell, and closes
	// the pane when it ends.
	Run(pane string, argv []string) error
	// Screen returns the text a pane shows.
	Screen(pane string) (string, error)
	// Split opens a pane beside pane, on any of the four sides. A ratio of at
	// most 1 is the share pane keeps; above 1 it is the new pane's size in
	// cells, kept as the window changes.
	Split(pane, direction string, ratio float64) (Pane, error)
	Swap(source, target string) error
	// Resize moves a pane's dividing line by a fraction of its split: the line
	// on the side the direction names, or at the window's edge the line on
	// the pane's other side.
	Resize(pane, direction string, amount float64) error
	PaneClose(pane string) error
	// Size is a pane's width and height in cells.
	Size(pane string) (w, h int, err error)
	// PaneFocus gives the keys to the pane beside pane in a direction and
	// returns the pane that then has them: pane's neighbour, or with none
	// there whichever had them before. An empty direction gives them to
	// pane itself.
	PaneFocus(pane, direction string) (focused string, err error)
	// Notify shows the pop-up and returns the reason, one of the Notify
	// constants, and the pop-up's setting.
	Notify(title, body string, sound bool) (reason, delivery string, err error)
	// SetKeys replaces our marked shortcuts in the settings; none removes them.
	SetKeys(entries []KeyEntry) error
	// Overlay runs a program in a pane drawn over the tab until it ends; w
	// and h are shares of the window, or cells above 1.
	// ErrNotFront when it is asked from a pane whose workspace is not in front.
	Overlay(argv []string, w, h float64) error
	// OverlayAt is Overlay with a place, in cells: the corner of its box at
	// the cell x, y of pane, or of the window where pane is empty, moved to
	// stay inside the window. It is how a menu opens for the thing here:
	// under a right press, under a row's key, or under whatever else a
	// terminal leaves a person to ask with. A press beside the box closes
	// it and is used up; so do a change of the window's size and of the
	// workspace in front.
	OverlayAt(argv []string, w, h float64, pane string, x, y int) error
	// SpaceOpen puts the workspace of a project's folder in front, made
	// with one tab labelled MainTab in cwd where there is none; an empty
	// root is the tabs of no project. It answers with the pane that then
	// has the keys there.
	SpaceOpen(root, cwd string) (Pane, error)
	// SpaceRename names a workspace by its ID: Label(name), and with an
	// empty name its folder's again.
	SpaceRename(space, name string) error
	// SpaceClose closes every tab of a workspace, and it is gone. Nothing
	// on disk is touched by it.
	SpaceClose(space string) error
	// Marks tells the keeper how many things wait for the person in each
	// tab, by the tab's id, for the tab row: the whole table each time, and
	// for each tab what its own agent waits with (the keeper adds a fleet's
	// tabs up for the fleet's name on the first line). It is taken from the
	// dock's pane alone, a figure for a tab that is not there is dropped,
	// and it draws a count and nothing else.
	Marks(tabs map[string]int) error
	// Dock moves the keys to or from the dock, or folds it: the Dock words.
	// Everything else of the dock the keeper does alone: it starts
	// `ui run side` there and starts it again, draws the place while that
	// program is not up, and reads its side and width from the settings.
	Dock(what string) error
}

// Dirs are the login's three folders of ours.
type Dirs struct{ Config, State, Cache string }

// Watcher reports changed files. Slow is true once notices have proved
// unreliable and the watcher looks by itself instead.
type Watcher interface {
	Changes() <-chan string
	Slow() bool
	Close() error
}

// ErrCannotTell: the system gives no way to know who else can write.
// ErrNotOurs: a lock was asked for in a folder that belongs to another login.
var (
	ErrCannotTell = errors.New("cannot tell on this system")
	ErrNotOurs    = errors.New("the folder belongs to another login")
)

// The shells Quote can write for; empty is the shell of this system.
const (
	ShellPosix      = "posix"
	ShellPowerShell = "powershell"
	ShellCmd        = "cmd"
)

// Platform is the operating system, for every package but the one that implements it.
type Platform interface {
	Dirs() (Dirs, error)
	// Lock waits for a file lock; TryLock reports false when another process holds it.
	Lock(path string, exclusive bool) (unlock func(), err error)
	TryLock(path string) (unlock func(), ok bool, err error)
	// Replace renames tmp over final, retrying where another program holds it open.
	Replace(tmp, final string) error
	// Read reads a whole file that Replace may be replacing at that moment:
	// it never stands in a replace's way and never fails because of one. It
	// is for a reader that will write the file back: on Windows it takes
	// half a second to say that a file is not there.
	Read(path string) ([]byte, error)
	// Peek is Read for a reader that only shows the file: it stands in no
	// replace's way either, and waits for nothing. At the moment of a
	// replace on Windows it can fail or find no file; the caller keeps what
	// it had and looks again.
	Peek(path string) ([]byte, error)
	// Private makes a file or folder readable by the login only.
	Private(path string) error
	// PrivateTemp makes a fresh folder under the login's cache, never a shared one.
	PrivateTemp() (string, error)
	// WritableByOthers: another login owns it, anyone may write it, or its
	// group may and has a member besides the owner. ErrCannotTell on Windows.
	WritableByOthers(path string) (bool, error)
	Synced(path string) bool
	// PathKey is a path made comparable; never used as a real path.
	PathKey(path string) string
	// Quote joins arguments into one line for a shell.
	Quote(shell string, argv []string) string
	Watch(paths ...string) (Watcher, error)
	SelfPath() (string, error)
	// System is the system this program runs on: "darwin", "linux" or "windows".
	System() string
	// Shell is the command that runs one line in the system's own shell, set
	// up so that EndTree reaches whatever the line starts.
	Shell(line string) *exec.Cmd
	// EndTree ends a started command and everything it started.
	EndTree(cmd *exec.Cmd) error
}

// Placement decides the folder a task's attempts work in, and is every
// worktree the tool makes. Its callers hold no lock of the store: each
// result is written down afterwards, with Rules.SetWorktree.
type Placement interface {
	// Place returns the folder, creating the worktree of a task that has
	// none yet and running the project's setup there when it is trusted; w
	// is nil for a task in a shared folder. start calls it before the
	// attempt exists, and again for a retry, which changes nothing.
	Place(root string, s *State, task string) (dir string, w *Worktree, err error)
	// Remove takes a task's worktree away; without discard it refuses one
	// that holds work not committed. The branch stays. The record it
	// returns has RemovedAt set, nil for a task that had none.
	Remove(root string, s *State, task string, discard bool) (*Worktree, error)
	// Clean reports whether nothing in a worktree waits to be committed;
	// a folder that is no worktree of ours is clean.
	Clean(dir string) (bool, error)
	// Left counts what git ignores in the project and a new worktree will
	// not have, with the first few names, for the notice start prints once.
	Left(root string) (n int, some []string, err error)
	// Forget removes what a closed run left: its worktrees, and each task
	// branch that is proven merged. It lists the branches it kept.
	Forget(root string, s *State) (kept []string, err error)
	// Repair finds removals that were interrupted and worktrees no run
	// records, one line each, and with fix finishes or removes them.
	Repair(root string, runs []*State, fix bool) (found []string, err error)
	// Equip gives dir, another copy of the project's code, what a new
	// worktree gets beyond git: the carried files and the approved setup,
	// its output in log. It returns the setup's word.
	Equip(root, dir, log string) (setup string, err error)
	// Hide keeps files of ours in a worktree out of git's sight there.
	Hide(root, dir string, paths ...string) error
}

// Checked is the work an accept is about to check: the folder, the commit the
// check runs on, and the tip it must still find when it collects. All empty
// means nothing is collected and the check runs in the task's own folder.
type Checked struct{ Dir, OID, Tip string }

// Integrator collects accepted work in one place: the run's integration
// branch. It binds land itself. Nothing here writes the record: the caller
// hands what comes back to Rules.Integrated and Rules.Checked.
type Integrator interface {
	// Begin makes the branch of a new run and returns it with the base it
	// stands at: the one the run names, else the branch the project is on.
	// Where the project has no git it returns nil and no error.
	Begin(root string, s *State) (*GitRef, *Integration, error)
	// Prepare simulates the merge and brings the tip into the task's folder.
	// A conflict is a *Refusal that lists the files.
	Prepare(root string, s *State, task string) (Checked, error)
	// PrepareAll merges the tasks, in order, in the run's scratch copy: Dir
	// is that copy for each, OID the merged commit after it, and Tip empty,
	// for the caller to fill with the commit collected before it.
	PrepareAll(root string, s *State, tasks []string) ([]Checked, error)
	// Collect keeps exactly the checked tree and returns its commit; it
	// refuses when the tip or the checked folder has moved.
	Collect(root string, s *State, task string, c Checked) (commit string, err error)
	// Diff is what a task changed against the collected work, or with stat
	// one line a file.
	Diff(root string, s *State, task string, stat bool) (string, error)
	// Drop removes the run's own copy of the code and the branch of a
	// closed run that was landed or holds nothing; a branch with work that
	// was not landed stays and is named.
	Drop(root string, s *State) (kept string, err error)
	// Protected holds what the job changed between its base and its tip
	// against the patterns (Owned): the files that match, read from git
	// with -z, and their fingerprint, a hash over each path and the id git
	// gives its contents at the tip. No match gives nothing.
	Protected(root string, s *State, patterns []string) (print string, files []string, err error)
}

// Finding is one result of an overlap scan, kept on the run once announced.
// Kind is "same" (one file, merges cleanly), "overlap" (would conflict),
// "scope" or "lands". Of two tasks that would conflict the first keeps its
// place and the second is the one told to wait or sync. Work is set on a
// finding across two fleets alone: for each of Tasks, the fleet's own task
// in flight that holds the change, or "" where it is the fleet's collected
// work. It is no part of what makes two findings the same one.
type Finding struct {
	Kind  string   `json:"kind"`
	Tasks []string `json:"tasks"`
	Files []string `json:"files,omitempty"`
	How   string   `json:"how,omitempty"`
	Work  []string `json:"work,omitempty"`
}

// Overlap scans a run's tasks against each other and the collected work;
// no tasks named is all that are in flight. It returns every finding there
// is now, to be handed to Rules.Found, which knows the new ones. deep also
// looks at work not committed, as the five-minute scan does. A scan cut off
// by ctx returns what it has and ctx's error.
type Overlap interface {
	Scan(ctx context.Context, root string, s *State, tasks []string, deep bool) ([]Finding, error)
}

// Nudge is one call for the person's attention. Root and Run say whose it
// is, so that two projects' tasks of one name are kept apart.
type Nudge struct {
	Root, Run         string
	Title, Body, Task string
	Urgent            bool
}

// Notifier reaches the person when they are not looking.
type Notifier interface {
	// Nudge is one call about something that is no item.
	Nudge(n Nudge) error
	// Items puts every item of a run that waits for the person in front of
	// them and stamps it as shown; whoever swept or raised an item calls it.
	// snap is the picture the caller has just taken, or nil.
	Items(root, run string, snap *Snapshot, now time.Time) error
}

// Evidence is what a worker saved from its browser check: at most
// EvidenceFiles files and EvidenceBytes bytes an attempt, and nothing a link
// in the folder leads to outside it.
type Evidence interface {
	// List names the files, oldest first, each with its kind.
	List(root, run, attempt string) ([]EvidenceFile, error)
	// Open opens the file at a place in List's answer through a handle that
	// cannot leave the attempt's evidence folder.
	Open(root, run, attempt string, index int) (io.ReadCloser, EvidenceFile, error)
	// Answering returns those of the ports that a site answers on now.
	Answering(ports []int) []int
}

const (
	EvidenceFiles = 40
	EvidenceBytes = 50 << 20
)

// AgentSetup is what start and init hand to an agent so that its two hooks
// run. File is the settings file in the agent's folder that carries them,
// empty when they ride on the command line alone; the keeper starts such an
// agent again with the arguments it was started with. Gated is true when the gate will hold the agent.
type AgentSetup struct {
	File  string
	Args  []string
	Gated bool
}

// AgentSettings sets up the status-line hook and the gate for one agent.
type AgentSettings interface {
	// Ensure looks for the two hooks in the settings file of folder. With
	// write it adds them there, which init does on a yes and start does in a
	// folder that is the worker's alone. Without write, when the file lacks
	// them, it never changes the file: it writes a settings file into
	// scratch and returns the arguments that pass it to the agent.
	Ensure(kind, folder, scratch string, write bool) (AgentSetup, error)
	// Remove takes our entries out of the folder's settings file.
	Remove(kind, folder string) error
}

// Swept is what one sweep did. Ran is false when nobody has swept yet.
type Swept struct {
	Ran     bool      `json:"ran"`
	At      time.Time `json:"at"`
	Changed bool      `json:"changed"`
}

// Sweeper looks at every live attempt of a run once. It returns the picture
// it took, nil when it took none, so its caller need not ask for a second.
type Sweeper interface {
	Sweep(root, run string, now time.Time) (Swept, *Snapshot, error)
}

// The stand-ins: each does nothing, until the package that owns the real
// thing plugs it in.
type (
	NoStore         struct{}
	NoTerminals     struct{}
	NoPlatform      struct{}
	NoPlacement     struct{}
	NoIntegrator    struct{}
	NoOverlap       struct{}
	NoNotifier      struct{}
	NoEvidence      struct{}
	NoAgentSettings struct{}
	NoSweeper       struct{}
)

func (NoStore) Read(string, string) (*State, error)             { return nil, ErrNotBuilt }
func (NoStore) Runs(string) ([]string, error)                   { return nil, nil }
func (NoStore) Current(string) (string, error)                  { return "", nil }
func (NoStore) History(string, string) ([]Event, error)         { return nil, nil }
func (NoStore) Dir(string, string) string                       { return "" }
func (NoStore) Create(string, *State) error                     { return ErrNotBuilt }
func (NoStore) Change(string, string, func(*State) error) error { return ErrNotBuilt }
func (NoStore) Append(string, string, []Event) error            { return ErrNotBuilt }
func (NoStore) SetCurrent(string, string) error                 { return ErrNotBuilt }
func (NoStore) Remove(string, string) error                     { return ErrNotBuilt }
func (NoStore) Both(string, string, string, func(up, low *State) error) error {
	return ErrNotBuilt
}

func (NoTerminals) Version() (string, error)                    { return "", ErrNotBuilt }
func (NoTerminals) Snapshot(context.Context) (*Snapshot, error) { return nil, ErrNotBuilt }
func (NoTerminals) TabCreate(_, _, _, _ string, _ []string) (Pane, error) {
	return Pane{}, ErrNotBuilt
}
func (NoTerminals) SpaceOpen(string, string) (Pane, error) { return Pane{}, ErrNotBuilt }
func (NoTerminals) SpaceRename(string, string) error       { return ErrNotBuilt }
func (NoTerminals) SpaceClose(string) error                { return ErrNotBuilt }
func (NoTerminals) Marks(map[string]int) error             { return ErrNotBuilt }
func (NoTerminals) OverlayAt([]string, float64, float64, string, int, int) error {
	return ErrNotBuilt
}
func (NoTerminals) TabRename(string, string) error                      { return ErrNotBuilt }
func (NoTerminals) TabFocus(string) error                               { return ErrNotBuilt }
func (NoTerminals) TabClose(string) error                               { return ErrNotBuilt }
func (NoTerminals) TabGive(string, string) error                        { return ErrNotBuilt }
func (NoTerminals) Prompt(string, string, time.Duration) error          { return ErrNotBuilt }
func (NoTerminals) Point(string, Pointer, string) error                 { return ErrNotBuilt }
func (NoTerminals) Say(string, string, string) error                    { return ErrNotBuilt }
func (NoTerminals) Run(string, []string) error                          { return ErrNotBuilt }
func (NoTerminals) Screen(string) (string, error)                       { return "", ErrNotBuilt }
func (NoTerminals) Split(string, string, float64) (Pane, error)         { return Pane{}, ErrNotBuilt }
func (NoTerminals) Swap(string, string) error                           { return ErrNotBuilt }
func (NoTerminals) Resize(string, string, float64) error                { return ErrNotBuilt }
func (NoTerminals) PaneClose(string) error                              { return ErrNotBuilt }
func (NoTerminals) Size(string) (int, int, error)                       { return 0, 0, ErrNotBuilt }
func (NoTerminals) PaneFocus(string, string) (string, error)            { return "", ErrNotBuilt }
func (NoTerminals) Notify(string, string, bool) (string, string, error) { return "", "", ErrNotBuilt }
func (NoTerminals) SetKeys([]KeyEntry) error                            { return ErrNotBuilt }
func (NoTerminals) Events(context.Context) (*Snapshot, <-chan TermEvent, error) {
	return nil, nil, ErrNotBuilt
}
func (NoTerminals) AgentStart(string, string, string, []string, time.Duration) error {
	return ErrNotBuilt
}
func (NoTerminals) Overlay([]string, float64, float64) error { return ErrNotBuilt }
func (NoTerminals) Dock(string) error                        { return ErrNotBuilt }

func (NoPlatform) Dirs() (Dirs, error)                   { return Dirs{}, ErrNotBuilt }
func (NoPlatform) Lock(string, bool) (func(), error)     { return nil, ErrNotBuilt }
func (NoPlatform) TryLock(string) (func(), bool, error)  { return nil, false, ErrNotBuilt }
func (NoPlatform) Replace(string, string) error          { return ErrNotBuilt }
func (NoPlatform) Read(path string) ([]byte, error)      { return os.ReadFile(path) }
func (NoPlatform) Peek(path string) ([]byte, error)      { return os.ReadFile(path) }
func (NoPlatform) Private(string) error                  { return ErrNotBuilt }
func (NoPlatform) PrivateTemp() (string, error)          { return "", ErrNotBuilt }
func (NoPlatform) WritableByOthers(string) (bool, error) { return false, ErrNotBuilt }
func (NoPlatform) Synced(string) bool                    { return false }
func (NoPlatform) PathKey(path string) string            { return path }
func (NoPlatform) Quote(_ string, argv []string) string  { return strings.Join(argv, " ") }
func (NoPlatform) Watch(...string) (Watcher, error)      { return nil, ErrNotBuilt }
func (NoPlatform) SelfPath() (string, error)             { return "", ErrNotBuilt }
func (NoPlatform) System() string                        { return "" }
func (NoPlatform) Shell(line string) *exec.Cmd           { return exec.Command(line) }
func (NoPlatform) EndTree(*exec.Cmd) error               { return ErrNotBuilt }

// Place gives the project folder, or the folder the task names: phase 1 has
// no worktrees.
func (NoPlacement) Place(root string, s *State, task string) (string, *Worktree, error) {
	if dir, ok := strings.CutPrefix(s.Tasks[task].Placement, PlaceCwd); ok {
		return dir, nil, nil
	}
	return root, nil, nil
}
func (NoPlacement) Remove(string, *State, string, bool) (*Worktree, error) { return nil, nil }
func (NoPlacement) Clean(string) (bool, error)                             { return true, nil }
func (NoPlacement) Left(string) (int, []string, error)                     { return 0, nil, nil }
func (NoPlacement) Forget(string, *State) ([]string, error)                { return nil, nil }
func (NoPlacement) Repair(string, []*State, bool) ([]string, error)        { return nil, nil }
func (NoPlacement) Equip(string, string, string) (string, error)           { return "", nil }
func (NoPlacement) Hide(string, string, ...string) error                   { return nil }

func (NoIntegrator) Begin(string, *State) (*GitRef, *Integration, error)     { return nil, nil, nil }
func (NoIntegrator) Prepare(string, *State, string) (Checked, error)         { return Checked{}, nil }
func (NoIntegrator) PrepareAll(string, *State, []string) ([]Checked, error)  { return nil, ErrNotBuilt }
func (NoIntegrator) Collect(string, *State, string, Checked) (string, error) { return "", nil }
func (NoIntegrator) Diff(string, *State, string, bool) (string, error)       { return "", ErrNotBuilt }
func (NoIntegrator) Drop(string, *State) (string, error)                     { return "", nil }
func (NoIntegrator) Protected(string, *State, []string) (string, []string, error) {
	return "", nil, nil
}

func (NoOverlap) Scan(context.Context, string, *State, []string, bool) ([]Finding, error) {
	return nil, nil
}

func (NoNotifier) Nudge(Nudge) error                                { return nil }
func (NoNotifier) Items(string, string, *Snapshot, time.Time) error { return nil }

func (NoEvidence) List(string, string, string) ([]EvidenceFile, error) { return nil, nil }
func (NoEvidence) Answering([]int) []int                               { return nil }
func (NoEvidence) Open(string, string, string, int) (io.ReadCloser, EvidenceFile, error) {
	return nil, EvidenceFile{}, ErrNotBuilt
}

func (NoAgentSettings) Ensure(string, string, string, bool) (AgentSetup, error) {
	return AgentSetup{}, nil
}
func (NoAgentSettings) Remove(string, string) error { return nil }

func (NoSweeper) Sweep(string, string, time.Time) (Swept, *Snapshot, error) {
	return Swept{}, nil, nil
}
