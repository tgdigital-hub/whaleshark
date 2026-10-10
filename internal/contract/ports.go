package contract

import (
	"context"
	"errors"
	"os"
	"os/exec"
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
}

// Snapshot is the terminals' picture of every pane at one moment. Instance is
// new at every start of the keeper, so a restart is seen without inference;
// Seq is the number of the last event the picture holds.
type Snapshot struct {
	At       time.Time `json:"at"`
	Instance string    `json:"instance,omitempty"`
	Seq      uint64    `json:"seq,omitempty"`
	Panes    []Pane    `json:"panes"`
}

// Pane is one pane as the terminals show it. Agent is empty when the pane
// holds a bare shell. Status is one of the Status constants. Terminal is new
// each time the pane's program is started; Workspace is always empty.
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
)

// TermEvent is one change, in the order it happened: Seq counts up by one.
type TermEvent struct {
	Seq  uint64 `json:"seq"`
	Kind string `json:"kind"`
	Pane Pane   `json:"pane"`
}

// Apply brings a picture up to date with one event of Events.
func Apply(s *Snapshot, e TermEvent) {
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
	// TabCreate opens a tab without focusing it; env is KEY=VALUE.
	TabCreate(cwd, label string, env []string) (Pane, error)
	TabRename(tab, label string) error
	TabFocus(tab string) error
	TabClose(tab string) error
	AgentStart(name, kind, pane string, args []string, timeout time.Duration) error
	// Prompt delivers an agent's first prompt and waits until it is working.
	Prompt(name, text string, timeout time.Duration) error
	// Point types one of the fixed one-line pointers into an agent's pane.
	Point(pane string, p Pointer, arg string) error
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
	Overlay(argv []string, w, h float64) error
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
	// Drop removes the branch of a closed run once it was landed.
	Drop(root string, s *State) error
}

// Finding is one result of an overlap scan, kept on the run once announced.
// Kind is "same" (one file, merges cleanly), "overlap" (would conflict),
// "scope" or "lands". Of two tasks that would conflict the first keeps its
// place and the second is the one told to wait or sync.
type Finding struct {
	Kind  string   `json:"kind"`
	Tasks []string `json:"tasks"`
	Files []string `json:"files,omitempty"`
	How   string   `json:"how,omitempty"`
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

// Evidence lists what a worker saved from its browser check.
type Evidence interface {
	List(root, run, attempt string) ([]EvidenceFile, error)
}

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

func (NoTerminals) Version() (string, error)                            { return "", ErrNotBuilt }
func (NoTerminals) Snapshot(context.Context) (*Snapshot, error)         { return nil, ErrNotBuilt }
func (NoTerminals) TabCreate(string, string, []string) (Pane, error)    { return Pane{}, ErrNotBuilt }
func (NoTerminals) TabRename(string, string) error                      { return ErrNotBuilt }
func (NoTerminals) TabFocus(string) error                               { return ErrNotBuilt }
func (NoTerminals) TabClose(string) error                               { return ErrNotBuilt }
func (NoTerminals) Prompt(string, string, time.Duration) error          { return ErrNotBuilt }
func (NoTerminals) Point(string, Pointer, string) error                 { return ErrNotBuilt }
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
func (NoIntegrator) Drop(string, *State) error                               { return nil }

func (NoOverlap) Scan(context.Context, string, *State, []string, bool) ([]Finding, error) {
	return nil, nil
}

func (NoNotifier) Nudge(Nudge) error                                { return nil }
func (NoNotifier) Items(string, string, *Snapshot, time.Time) error { return nil }

func (NoEvidence) List(string, string, string) ([]EvidenceFile, error) { return nil, nil }

func (NoAgentSettings) Ensure(string, string, string, bool) (AgentSetup, error) {
	return AgentSetup{}, nil
}
func (NoAgentSettings) Remove(string, string) error { return nil }

func (NoSweeper) Sweep(string, string, time.Time) (Swept, *Snapshot, error) {
	return Swept{}, nil, nil
}
