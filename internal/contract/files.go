package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// ReadVersioned decodes a JSON file of ours into v. A missing file leaves v
// untouched and is no error; a newer file is ErrNewer.
func ReadVersioned(path string, max int, v any) error {
	data, err := os.ReadFile(path)
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

// WriteVersioned replaces a JSON file of ours, private to the login, by
// moving a finished file over it with replace, which is the platform's
// Replace. It refuses to replace a newer file.
func WriteVersioned(replace func(tmp, final string) error, path string, max int, v any) error {
	if err := ReadVersioned(path, max, new(Versioned)); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(append(data, '\n')); err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return replace(tmp.Name(), path)
}

// Projects is projects.json in the login's state folder: the project folders
// init has been run in.
type Projects struct {
	Versioned
	Roots []string `json:"roots"`
}

const projectsFile = "projects.json"

// ReadProjects returns the login's project folders, in the order they were added.
func ReadProjects(stateDir string) ([]string, error) {
	var p Projects
	err := ReadVersioned(filepath.Join(stateDir, projectsFile), FileVersion, &p)
	return p.Roots, err
}

// WriteProjects replaces the login's list of project folders.
func WriteProjects(replace func(tmp, final string) error, stateDir string, roots []string) error {
	p := Projects{Versioned{FileVersion}, roots}
	return WriteVersioned(replace, filepath.Join(stateDir, projectsFile), FileVersion, p)
}

const pauseFile = "paused"

// Paused reads the login's pause mark, the one file the gate reads. It
// reports when and by whom Stop all was pressed, or nil when it was not.
func Paused(stateDir string) (*Pause, error) {
	data, err := os.ReadFile(filepath.Join(stateDir, pauseFile))
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

// SetPaused writes the pause mark, or removes it when p is nil.
func SetPaused(stateDir string, p *Pause) error {
	path := filepath.Join(stateDir, pauseFile)
	if p == nil {
		if err := os.Remove(path); !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	line := p.At.UTC().Format(time.RFC3339) + " " + p.By + "\n"
	return os.WriteFile(path, []byte(line), 0o600)
}

// The variables a worker's tab is created with, herdr's own pane variable,
// and the variable that names a file holding the time for a scripted test.
//
// EnvFrom is where a pane or the page's server says its child command comes
// from: WherePane, WherePage or WherePhone. The page's server starts its
// children without EnvPane and EnvAttempt, and the page's mark counts only
// then. EnvActivePane is what herdr hands the command of a shortcut, which
// has no pane of its own. EnvNotices set to NoticesOff makes a watcher lose
// its file notices, so a test can see it find that out.
const (
	EnvFrom       = "WHALESHARK_FROM"
	EnvActivePane = "HERDR_ACTIVE_PANE_ID"
	EnvNotices    = "WHALESHARK_NOTICES"
	NoticesOff    = "off"
)

const (
	EnvRoot     = "WHALESHARK_ROOT"
	EnvRun      = "WHALESHARK_RUN"
	EnvTask     = "WHALESHARK_TASK"
	EnvAttempt  = "WHALESHARK_ATTEMPT"
	EnvDepth    = "WHALESHARK_DEPTH"
	EnvBin      = "WHALESHARK_BIN"
	EnvEvidence = "WHALESHARK_EVIDENCE"
	EnvPane     = "HERDR_PANE_ID"
	EnvClock    = "WHALESHARK_CLOCK"
)

// Now is the clock every command uses: the wall clock, or the time written
// in the file EnvClock names.
func Now() time.Time {
	if path := os.Getenv(EnvClock); path != "" {
		// #nosec G703 -- the clock file of a scripted test, named by the login's own environment
		if data, err := os.ReadFile(path); err == nil {
			if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(data))); err == nil {
				return t
			}
		}
	}
	return time.Now()
}

// ProjectFile is whaleshark.toml. Every key has a default; the keys that can
// run something or reach outside the project count only once trusted.
type ProjectFile struct {
	Worktrees struct {
		Dir          string   `toml:"dir"`
		BranchPrefix string   `toml:"branch_prefix"`
		Share        []string `toml:"share"`
		PortBlock    int      `toml:"port_block"`
	} `toml:"worktrees"`
	Setup struct {
		Script         string `toml:"script"`
		ScriptWindows  string `toml:"script_windows"`
		TimeoutSeconds int    `toml:"timeout_seconds"`
	} `toml:"setup"`
	Agent struct {
		Args []string `toml:"args"`
	} `toml:"agent"`
	Check struct {
		TimeoutSeconds int `toml:"timeout_seconds"`
	} `toml:"check"`
	Land struct {
		Who   string `toml:"who"`
		Check string `toml:"check"`
	} `toml:"land"`
	Limits struct {
		Agents          int `toml:"agents"`
		QuietSeconds    int `toml:"quiet_seconds"`
		ResumePerSecond int `toml:"resume_per_second"`
		StaleMinutes    int `toml:"stale_minutes"`
		MaxMinutes      int `toml:"max_minutes"`
	} `toml:"limits"`
	Notify struct {
		FullText bool `toml:"full_text"`
	} `toml:"notify"`
}

// ProjectDefaults is a ProjectFile with every default filled in.
func ProjectDefaults() ProjectFile {
	var p ProjectFile
	p.Worktrees.Dir = ".whaleshark/worktrees"
	p.Worktrees.BranchPrefix = "whaleshark/"
	p.Worktrees.PortBlock = 10
	p.Setup.TimeoutSeconds = 600
	p.Check.TimeoutSeconds = 900
	p.Land.Who = ForHuman
	p.Limits.Agents = 20
	p.Limits.QuietSeconds = 120
	p.Limits.ResumePerSecond = 2
	p.Limits.StaleMinutes = 30
	return p
}

// ReadProjectFile reads whaleshark.toml from a project folder over the
// defaults. A missing file gives the defaults.
func ReadProjectFile(root string) (ProjectFile, error) {
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
	FleetPane   string            `json:"fleet_pane,omitempty"`
	ActionsPane string            `json:"actions_pane,omitempty"`
	FleetWidth  int               `json:"fleet_width,omitempty"`
	Folded      bool              `json:"folded,omitempty"`
	DND         bool              `json:"dnd,omitempty"`
	DNDUntil    time.Time         `json:"dnd_until,omitzero"`
	Mute        bool              `json:"mute,omitempty"`
	Drafts      map[string]string `json:"drafts,omitempty"`
	LastHere    time.Time         `json:"last_here,omitzero"`
}

// CtxFile is the file CtxPath names: how full an agent's context is, as its own
// status line last said. Known is false when the figure was empty.
type CtxFile struct {
	Versioned
	Pct   int       `json:"pct"`
	Known bool      `json:"known"`
	At    time.Time `json:"at"`
}

// CtxPath is the file of one pane's context figure. A pane id of herdr's
// holds a colon, which a Windows file name cannot: the file has "+" in its
// place, which no pane id holds.
func CtxPath(stateDir, pane string) string {
	return filepath.Join(stateDir, "ctx", strings.ReplaceAll(pane, ":", "+")+".json")
}

// ReadCtx returns the context figure of every live attempt's pane that has
// one, by pane id.
func ReadCtx(stateDir string, s *State) map[string]CtxFile {
	out := map[string]CtxFile{}
	for _, a := range s.Attempts {
		var f CtxFile
		if a.State.Live() && a.Place.Pane != "" && ReadVersioned(CtxPath(stateDir, a.Place.Pane), FileVersion, &f) == nil && !f.At.IsZero() {
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
		Theme         string `toml:"theme"`
		Actions       string `toml:"actions"`
		SettleSeconds int    `toml:"settle_seconds"`
	} `toml:"ui"`
	Nudge struct {
		Sound       bool `toml:"sound"`
		Popup       bool `toml:"popup"`
		Phone       bool `toml:"phone"`
		LeadMinutes int  `toml:"lead_minutes"`
	} `toml:"nudge"`
}

// PersonDefaults is a PersonConfig with every default filled in.
func PersonDefaults() PersonConfig {
	var c PersonConfig
	c.UI.Theme = "reef"
	c.UI.Actions = "bottom"
	c.UI.SettleSeconds = 4
	c.Nudge.Sound, c.Nudge.Popup, c.Nudge.Phone = true, true, true
	c.Nudge.LeadMinutes = 10
	return c
}
