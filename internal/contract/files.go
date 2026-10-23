package contract

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
)

// FileVersion is the version this program writes into every file of its own
// other than state.json.
const FileVersion = 1

// ErrNewer means a file was written by a newer program: it is treated as
// absent when read and is never written.
var ErrNewer = errors.New("written by a newer version of whaleshark")

// Versioned is the line every file of ours carries.
type Versioned struct {
	Version int `json:"version" toml:"version"`
}

// ReadVersioned decodes a JSON file of ours into v, read with read: the
// platform's Peek for a reader that shows the file, its Read for one that
// will write it back. A missing file leaves v untouched and is no error; a
// newer file is ErrNewer.
func ReadVersioned(read func(path string) ([]byte, error), path string, max int, v any) error {
	data, err := read(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var line Versioned
	if err := json.Unmarshal(data, &line); err != nil {
		return err
	}
	if line.Version > max {
		return ErrNewer
	}
	return json.Unmarshal(data, v)
}

// Files is what of the platform a file of ours is written back with.
type Files interface {
	Read(path string) ([]byte, error)
	Replace(tmp, final string) error
}

// WriteVersioned replaces a JSON file of ours, private to the login, by
// moving a finished file over it with the platform's Replace. It refuses to
// replace a newer file.
func WriteVersioned(p Files, path string, max int, v any) error {
	if err := ReadVersioned(p.Read, path, max, new(Versioned)); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	return replaceFile(p, path, append(data, '\n'))
}

// Projects is projects.json in the login's state folder: the project folders
// init has been run in.
type Projects struct {
	Versioned
	Roots []string `json:"roots"`
}

const projectsFile = "projects.json"

// ReadProjects returns the login's project folders, in the order they were added.
func ReadProjects(read func(path string) ([]byte, error), stateDir string) ([]string, error) {
	var p Projects
	err := ReadVersioned(read, filepath.Join(stateDir, projectsFile), FileVersion, &p)
	return p.Roots, err
}

// WriteProjects replaces the login's list of project folders.
func WriteProjects(p Files, stateDir string, roots []string) error {
	list := Projects{Versioned{FileVersion}, roots}
	return WriteVersioned(p, filepath.Join(stateDir, projectsFile), FileVersion, list)
}

const pauseFile = "paused"

// Paused reads the login's pause mark, the one file the gate reads. It
// reports when and by whom Stop all was pressed, or nil when it was not.
// The reader is the platform's Peek: every agent's gate reads the mark
// before every step, and none of them may stand in the way of its removal.
func Paused(read func(string) ([]byte, error), stateDir string) (*Pause, error) {
	data, err := read(filepath.Join(stateDir, pauseFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	at, by, _ := strings.Cut(strings.TrimSpace(string(data)), " ")
	p := &Pause{By: by}
	p.At, _ = time.Parse(time.RFC3339, at)
	return p, nil
}

// SetPaused writes the pause mark, or removes it when p is nil. It is
// removed by moving it to another name with the platform's Replace, which
// waits out a program that holds it open on Windows, where a plain remove
// would be refused; the moved file is of no use and goes if it can.
func SetPaused(files Files, stateDir string, p *Pause) error {
	path := filepath.Join(stateDir, pauseFile)
	if p == nil {
		if err := files.Replace(path, path+".off"); !errors.Is(err, fs.ErrNotExist) && err != nil {
			return err
		}
		os.Remove(path + ".off")
		return nil
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	line := p.At.UTC().Format(time.RFC3339) + " " + p.By + "\n"
	return os.WriteFile(path, []byte(line), 0o600)
}

// The variables a worker's tab is created with, the two the keeper sets,
// and the variable that names a file holding the time for a scripted test.
// EnvPane is in every pane's program and holds the pane's id.
//
// EnvFrom is where a pane or the page's server says its child command comes
// from: WherePane, WherePage or WherePhone. The page's server starts its
// children without EnvPane and EnvAttempt, and the page's mark counts only
// then. EnvActivePane is what the keeper hands the command of a shortcut,
// which has no pane of its own, and a pane drawn over a tab. EnvNotices set to NoticesOff makes a watcher lose
// its file notices, so a test can see it find that out.
//
// EnvOwnTab marks a tab opened for one long command of a button, and holds
// where the button was pressed. EnvSystem is how connect says which system
// the person sits at, for the window's keys.
const (
	EnvFrom       = "WHALESHARK_FROM"
	EnvActivePane = "WHALESHARK_ACTIVE_PANE"
	EnvNotices    = "WHALESHARK_NOTICES"
	NoticesOff    = "off"
	EnvOwnTab     = "WHALESHARK_OWN_TAB"
	EnvSystem     = "WHALESHARK_SYSTEM"
)

// The files of a run's folder that more than one package names (6.1): the
// record, the lock the lead agent's wait holds, the lock of one question's
// blocked ask, and an attempt's own folder with its token.
const (
	StateFile = "state.json"
	WaitLock  = "wait.lock"
)

// SweepFile is in a run's folder and says when it was last swept;
// InstalledFile is what init wrote, in the project's folder of ours and in
// the login's state folder; LayoutBad is a layout file the keeper could not
// use at a start and kept, in the state folder.
const (
	SweepFile     = "sweep.at"
	InstalledFile = "installed.json"
	LayoutBad     = "layout.json.bad"
)

func AskLock(runDir, question string) string {
	return filepath.Join(runDir, "questions", question+".lock")
}

func AttemptDir(runDir, attempt string) string { return filepath.Join(runDir, "attempts", attempt) }

// ReadToken is the record's form of the token in an attempt's own file, and
// nothing where there is no such file: the rules refuse that as a wrong one.
func ReadToken(read func(path string) ([]byte, error), runDir, attempt string) (string, error) {
	data, err := read(filepath.Join(AttemptDir(runDir, attempt), "token"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return TokenHash(string(data)), err
}

const (
	EnvRoot     = "WHALESHARK_ROOT"
	EnvRun      = "WHALESHARK_RUN"
	EnvTask     = "WHALESHARK_TASK"
	EnvAttempt  = "WHALESHARK_ATTEMPT"
	EnvDepth    = "WHALESHARK_DEPTH"
	EnvBin      = "WHALESHARK_BIN"
	EnvEvidence = "WHALESHARK_EVIDENCE"
	EnvPane     = "WHALESHARK_PANE"
	EnvClock    = "WHALESHARK_CLOCK"
)

// Now is the clock every command uses: the wall clock, or the time written
// in the file EnvClock names. A test replaces that file while commands read
// it, and a read at that moment can fail, or find it empty or, on Windows,
// missing: it is read again for half a second before the wall clock, which
// is days away from a test's, is taken in its place.
func Now() time.Time {
	path := os.Getenv(EnvClock)
	for wait := time.Millisecond; path != "" && wait <= 256*time.Millisecond; wait *= 2 {
		// #nosec G703 -- the clock file of a scripted test, named by the login's own environment
		data, err := os.ReadFile(path)
		if t, bad := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(data))); err == nil && bad == nil {
			return t
		}
		time.Sleep(wait)
	}
	return time.Now()
}

// ProjectFile is whaleshark.toml. Every key has a default; the keys that can
// run something or reach outside the project count only once trusted. Held
// names what ReadProjectFile left at its default because the project as it
// stands was not approved: keys such as "setup.script", and each team file
// as "whaleshark-teams/<file>". Whoever would have used one says so with an
// untrusted event and goes on without; a landing is refused instead.
type ProjectFile struct {
	Held      []string `toml:"-"`
	Worktrees struct {
		Dir          string   `toml:"dir"`
		BranchPrefix string   `toml:"branch_prefix"`
		Share        []string `toml:"share"`
		PortBlock    int      `toml:"port_block"`
		FetchBase    bool     `toml:"fetch_base"`
	} `toml:"worktrees"`
	Setup struct {
		Script         string `toml:"script"`
		ScriptWindows  string `toml:"script_windows"`
		TimeoutSeconds int    `toml:"timeout_seconds"`
	} `toml:"setup"`
	// Agent.Args are more arguments for the agent tool Agent.Tool names,
	// which is DefaultTool where the file names none, and for no other tool:
	// what one tool takes another refuses or reads as something else.
	Agent struct {
		Args []string `toml:"args"`
		Tool string   `toml:"tool"`
	} `toml:"agent"`
	Check struct {
		TimeoutSeconds int `toml:"timeout_seconds"`
	} `toml:"check"`
	// Land.Mode is LandRun, where accepted work is collected on the run's
	// branch and landed whole, or LandPR, where each task is its own pull
	// request, merged by itself once its checks pass.
	Land struct {
		Who         string `toml:"who"`
		Check       string `toml:"check"`
		Mode        string `toml:"mode"`
		DraftFirst  bool   `toml:"draft_first"`
		MergeMethod string `toml:"merge_method"`
		Refresh     string `toml:"refresh"`
	} `toml:"land"`
	Limits struct {
		Agents          int `toml:"agents"`
		QuietSeconds    int `toml:"quiet_seconds"`
		ResumePerSecond int `toml:"resume_per_second"`
		StaleMinutes    int `toml:"stale_minutes"`
		MaxMinutes      int `toml:"max_minutes"`
	} `toml:"limits"`
	// Protect.Also adds paths to the person's protected list for everyone
	// who works here. It can only add, so it needs no approval.
	Protect struct {
		Also []string `toml:"also,omitempty"`
	} `toml:"protect,omitempty"`
}

// The two ways work lands, and the file in an attempt's folder that holds
// the body of its task's pull request.
const (
	LandRun = "run"
	LandPR  = "pr-per-task"
	PRBody  = "pr.md"
)

// ProjectDefaults is a ProjectFile with every default filled in.
func ProjectDefaults() ProjectFile {
	var p ProjectFile
	p.Worktrees.Dir = ".whaleshark/worktrees"
	p.Worktrees.BranchPrefix = "whaleshark/"
	p.Worktrees.PortBlock = 10
	p.Setup.TimeoutSeconds = 600
	p.Check.TimeoutSeconds = 900
	p.Agent.Tool = DefaultTool
	p.Land.Who, p.Land.Mode, p.Land.MergeMethod, p.Land.Refresh = ForHuman, LandRun, "squash", "auto"
	p.Limits.Agents = 20
	p.Limits.QuietSeconds = 120
	p.Limits.ResumePerSecond = 2
	p.Limits.StaleMinutes = 30
	return p
}

// ReadProjectFile reads whaleshark.toml from a project folder over the
// defaults, and of what needs approval only what was approved (Held). A
// missing file gives the defaults.
func ReadProjectFile(root string) (ProjectFile, error) {
	p, err := readProject(root)
	if err == nil {
		err = held(root, &p)
	}
	return p, err
}

func readProject(root string) (ProjectFile, error) {
	p := ProjectDefaults()
	_, err := toml.DecodeFile(filepath.Join(root, "whaleshark.toml"), &p)
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	return p, err
}

// UIFile is ui.json in the login's state folder: what the panes remember
// about the screen, and nothing about the work.
type UIFile struct {
	Versioned
	FleetPane   string `json:"fleet_pane,omitempty"`
	ActionsPane string `json:"actions_pane,omitempty"`
	// FleetWidth is the fleet's width in cells as last seen, and FleetHeight
	// its height in rows where it lies at the bottom.
	FleetWidth  int               `json:"fleet_width,omitempty"`
	FleetHeight int               `json:"fleet_height,omitempty"`
	Folded      bool              `json:"folded,omitempty"`
	DND         bool              `json:"dnd,omitempty"`
	DNDUntil    time.Time         `json:"dnd_until,omitzero"`
	Mute        bool              `json:"mute,omitempty"`
	Drafts      map[string]string `json:"drafts,omitempty"`
	// Changed names the settings that run a program and were changed since
	// the person last saw their section of the settings screen: every
	// screen carries one line in the warning colour while it holds any.
	Changed []string `json:"changed,omitempty"`
	// Shut names the fleets folded in the left section, each "<run>/<fleet>".
	Shut     []string  `json:"shut,omitempty"`
	LastHere time.Time `json:"last_here,omitzero"`
	// ByAgent is the settings the person's agent changed since they last saw
	// those sections of the settings screen, each with what it was: one line
	// on every screen names them (AgentChanged), and u there takes one back.
	ByAgent []SetAsk `json:"by_agent,omitempty"`
	// FirstSteps is "off" once the five first steps were hidden or finished,
	// and Steps the ones ticked so far, a bit each from the first.
	FirstSteps string `json:"first_steps,omitempty"`
	Steps      int    `json:"steps,omitempty"`
	// SideSplit is where the agents begin in the left section, in hundredths
	// of its height, 0 for SideSplit; ActionsHeight the action pane's rows,
	// 0 for a quarter of the tab. Panes are the two panes of ours of each
	// workspace, by the workspace's folder (empty for the tabs of no
	// project): a pair is opened, moved and closed inside its own workspace
	// only. FleetPane and ActionsPane are what a file of before phase 7
	// holds: read once as the pair of the workspace that pane is in, and
	// written no more.
	SideSplit     int             `json:"side_split,omitempty"`
	ActionsHeight int             `json:"actions_height,omitempty"`
	Panes         map[string]Pair `json:"panes,omitempty"`
}

// Pair is the two panes of ours in one workspace, by their ids.
type Pair struct {
	Fleet   string `json:"fleet,omitempty"`
	Actions string `json:"actions,omitempty"`
}

// PairOf is a workspace's two panes as the file names them, an older file's
// one pair too: snap says which workspace those panes are in.
func (u UIFile) PairOf(root string, snap *Snapshot) Pair {
	pair := u.Panes[root]
	if snap == nil || u.Panes != nil {
		return pair
	}
	space := map[string]string{}
	for _, s := range snap.Spaces {
		space[s.ID] = s.Root
	}
	for _, p := range snap.Panes {
		if in, known := space[p.Workspace]; !known || in != root {
		} else if p.ID == u.FleetPane {
			pair.Fleet = p.ID
		} else if p.ID == u.ActionsPane {
			pair.Actions = p.ID
		}
	}
	return pair
}

// FirstSteps are the five lines of the left section's empty agents panel.
var FirstSteps = [5]string{
	"start your agent: type  claude  here →",
	"tell it what you want built, in your words",
	"your agents appear here; click one to go to it",
	"what needs you lands below; y answers",
	"ctrl+space s  settings ·  S  stops everything",
}

// AgentChanged is the line every screen carries while by, which is
// UIFile.ByAgent, holds anything: "setting changed by your agent: theme".
func AgentChanged(by []SetAsk) string {
	var keys []string
	for _, a := range by {
		keys = append(keys, a.Key)
	}
	if len(keys) == 0 {
		return ""
	}
	return "setting changed by your agent: " + strings.Join(keys, ", ")
}

// CtxFile is the file CtxPath names: how full an agent's context is, as its own
// status line last said. Known is false when the figure was empty.
type CtxFile struct {
	Versioned
	Pct   int       `json:"pct"`
	Known bool      `json:"known"`
	At    time.Time `json:"at"`
}

// CtxPath is the file of one pane's context figure. A pane id may
// hold a colon, which a Windows file name cannot: the file has "+" in its
// place, which no pane id holds.
func CtxPath(stateDir, pane string) string {
	return filepath.Join(stateDir, "ctx", strings.ReplaceAll(pane, ":", "+")+".json")
}

// ReadCtx returns the context figure of every live attempt's pane that has
// one, by pane id.
func ReadCtx(read func(path string) ([]byte, error), stateDir string, s *State) map[string]CtxFile {
	out := map[string]CtxFile{}
	for _, a := range s.Attempts {
		var f CtxFile
		if a.State.Live() && a.Place.Pane != "" && ReadVersioned(read, CtxPath(stateDir, a.Place.Pane), FileVersion, &f) == nil && !f.At.IsZero() {
			out[a.Place.Pane] = f
		}
	}
	return out
}

// TokenHash is the form a worker's token takes in the record, which never
// holds the token itself: "sha256:" and the hash of the token without the
// space around it, in hexadecimal.
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Installed is installed.json: what init wrote, so it can be removed exactly.
// The project's own copy lists its entries with Uses left at zero; the
// login's copy counts the projects that use each.
type Installed struct {
	Versioned
	Entries []InstalledEntry `json:"entries"`
}

// InstalledEntry is one thing init wrote: Kind is "hook", "block" or "keys",
// and Created says init made the file itself.
type InstalledEntry struct {
	Kind    string `json:"kind"`
	File    string `json:"file"`
	Agent   string `json:"agent,omitempty"`
	Created bool   `json:"created,omitempty"`
	Uses    int    `json:"uses,omitempty"`
}

// PersonConfig is config.toml in the login's settings folder.
type PersonConfig struct {
	UI struct {
		Theme string `toml:"theme"`
		// Actions is where the action pane lay before it could be switched
		// off: a file that says nothing under [panes] is read by it, and
		// nothing writes it any more.
		Actions       string `toml:"actions,omitempty"`
		SettleSeconds int    `toml:"settle_seconds"`
		// Marks is "auto" or "plain"; the variable of PlainMarks still wins.
		Marks string `toml:"marks"`
	} `toml:"ui"`
	// Panes is where the two panes of ours open by themselves: the fleet
	// "right", "left", "bottom" or "off", the action pane "bottom", "top" or
	// "off". The left section's own place is the engine's (Settings.Side).
	Panes struct {
		Fleet   string `toml:"fleet"`
		Actions string `toml:"actions"`
	} `toml:"panes"`
	// Fleets is Fleet Mode: whether a new job is a run of fleets, the three
	// limits (Unlimited holds no line), and the tools of a leader and of a
	// worker nobody named one for; a worker's "leader" is its leader's.
	// They are the person's and are read at every start: no project's file
	// holds them or can raise them.
	Fleets struct {
		Mode    string `toml:"mode"`
		Leaders int    `toml:"leaders"`
		Agents  int    `toml:"agents"`
		Total   int    `toml:"total"`
		Leader  string `toml:"leader"`
		Worker  string `toml:"worker"`
	} `toml:"fleets"`
	Nudge struct {
		Sound       bool `toml:"sound"`
		Popup       bool `toml:"popup"`
		Phone       bool `toml:"phone"`
		LeadMinutes int  `toml:"lead_minutes"`
	} `toml:"nudge"`
	// Notify is the phone's channel: the address one request goes to, the
	// link a tap on the notice opens, and whether a nudge's text goes along
	// with its title. The service is somebody else's, so that is the
	// person's to say and no project's.
	Notify struct {
		URL      string `toml:"url,omitempty"`
		Link     string `toml:"link,omitempty"`
		FullText bool   `toml:"full_text,omitempty"`
	} `toml:"notify,omitempty"`
	// The guardrails (guard.go), each the person's own and read when it is
	// used: no project's file holds one or can switch one off. Spend's two
	// limits are agent-hours, Unlimited for none.
	Plan struct {
		First bool `toml:"first"`
	} `toml:"plan"`
	Protect struct {
		Files bool   `toml:"files"`
		Paths string `toml:"paths"` // patterns with commas between: the struct stays comparable
	} `toml:"protect"`
	Spend struct {
		Guard bool `toml:"guard"`
		Job   int  `toml:"job"`
		Fleet int  `toml:"fleet"`
	} `toml:"spend"`
	Stuck struct {
		Watch        bool `toml:"watch"`
		ClimbMinutes int  `toml:"climb_minutes"`
	} `toml:"stuck"`
	// Update.Notice: the window asks once a day whether a newer release is out.
	Update struct {
		Notice bool `toml:"notice"`
	} `toml:"update"`
}

// ConfigFile and UIFileName are the person's two files of ours.
const (
	ConfigFile = "config.toml"
	UIFileName = "ui.json"
)

// ReadPerson reads the person's config.toml over the defaults; a missing
// file gives the defaults.
func ReadPerson(read func(path string) ([]byte, error), configDir string) (PersonConfig, error) {
	c := PersonDefaults()
	data, err := read(filepath.Join(configDir, ConfigFile))
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	c.UI.Actions, c.Panes.Actions = "", ""
	if err == nil {
		err = toml.Unmarshal(data, &c)
	}
	if c.Panes.Actions == "" {
		c.Panes.Actions = cmp.Or(c.UI.Actions, Bottom)
	}
	c.UI.Actions = ""
	return c, err
}

// Unlimited is a limit of Fleet Mode that holds no line; the machine's and a
// server's still do. FleetWorkerLeader is the worker's tool that is its leader's.
const (
	Unlimited         = 0
	FleetWorkerLeader = "leader"
	On, Off           = "on", "off"
)

// PersonDefaults is a PersonConfig with every default filled in.
func PersonDefaults() PersonConfig {
	var c PersonConfig
	c.UI.Theme, c.UI.Marks = "reef", "auto"
	c.UI.SettleSeconds = 4
	c.Panes.Fleet, c.Panes.Actions = Right, Bottom
	c.Fleets.Mode, c.Fleets.Leaders, c.Fleets.Agents, c.Fleets.Total = Off, 4, 8, 20
	c.Fleets.Leader, c.Fleets.Worker = DefaultTool, FleetWorkerLeader
	c.Nudge.Sound, c.Nudge.Popup, c.Nudge.Phone = true, true, true
	c.Nudge.LeadMinutes = 10
	c.Protect.Paths, c.Spend.Job = ProtectDefault, 40
	c.Stuck.Watch, c.Stuck.ClimbMinutes, c.Update.Notice = true, 10, true
	return c
}

// SavedFleet is a fleet the person keeps for a project: its name and what
// it works on. It stays when its work is done, where a fleet nobody saved
// ends with its work. Busy is the view's: it has work in the run shown.
type SavedFleet struct {
	Name    string    `json:"name"`
	Purpose string    `json:"purpose"`
	Since   time.Time `json:"since"`
	Busy    bool      `json:"busy,omitempty"`
}

// fleetsFile is the saved fleets, in the project's folder of ours.
const fleetsFile = "fleets.json"

// FleetName is a fleet's name as it is kept, and whether it is one: its
// words with one space between, three and 24 characters at most, no dash in
// front, nothing that steers a terminal.
func FleetName(name string) (string, bool) {
	words := strings.Fields(name)
	name = strings.Join(words, " ")
	ok := len(words) > 0 && len(words) <= 3 && utf8.RuneCountInString(name) <= 24 && name[0] != '-' &&
		!strings.ContainsFunc(name, func(r rune) bool { return steers(r) || r == '"' })
	return name, ok
}

// steers reports a character that could steer a terminal, turn the reading
// direction or break a line: no name and no sentence a file brings holds one.
func steers(r rune) bool {
	return unicode.IsControl(r) || r == 0x2028 || r == 0x2029 || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069
}

// ReadFleets reads a project's saved fleets, by name. An entry whose name
// is none, or whose purpose is not one plain line, is not a fleet: the file
// can have come with a folder somebody else made.
func ReadFleets(read func(path string) ([]byte, error), root string) ([]SavedFleet, error) {
	var f struct {
		Version int          `json:"version"`
		Fleets  []SavedFleet `json:"fleets"`
	}
	err := ReadVersioned(read, filepath.Join(root, ProjectDir, fleetsFile), FileVersion, &f)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	out := f.Fleets[:0]
	for _, s := range f.Fleets {
		name, ok := FleetName(s.Name)
		plain := !strings.ContainsFunc(s.Purpose, steers)
		if s.Busy = false; ok && name == s.Name && plain && utf8.RuneCountInString(s.Purpose) <= 200 {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b SavedFleet) int { return strings.Compare(a.Name, b.Name) })
	return out, err
}

// FleetsShown is ReadFleets as a caller may read it. What a fleet works on
// is a sentence that lies in the project's folder, and a folder can come
// from elsewhere: an agent is shown it only where nothing the project brings
// waits for the person's approval (Held), and the names alone until then.
// The person reads it always.
func FleetsShown(read func(path string) ([]byte, error), root string, by CallerKind) ([]SavedFleet, error) {
	fleets, err := ReadFleets(read, root)
	if p, perr := ReadProjectFile(root); by != Human && (perr != nil || len(p.Held) > 0) {
		for i := range fleets {
			fleets[i].Purpose = ""
		}
	}
	return fleets, err
}

// WriteFleets replaces a project's saved fleets. Its one caller is `run fleet`.
func WriteFleets(p Files, root string, fleets []SavedFleet) error {
	return WriteVersioned(p, filepath.Join(root, ProjectDir, fleetsFile), FileVersion, struct {
		Version int          `json:"version"`
		Fleets  []SavedFleet `json:"fleets"`
	}{FileVersion, fleets})
}
