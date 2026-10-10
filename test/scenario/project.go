// Package scenario runs the scripted tests of the whole tool: a made-up
// project on disk, the terminals beside it, fake agents in its tabs, and
// the real program started as each kind of caller. The terminals are the
// engine's double or the real keeper. README.md is the guide.
package scenario

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/internal/store"
	"github.com/tgdigital-hub/whaleshark/test/fakeengine"
)

// tools is the folder Main built the three programs into, and module the
// folder of the whole tree.
var tools, module string

// Main is the TestMain of a package that runs scenarios. It builds the real
// program and the fake agent once, puts their folder first on the PATH, so
// that no test starts a real agent, and names the fake agent for the double.
func Main(m *testing.M) {
	os.Exit(func() int {
		dir, err := os.MkdirTemp("", "scenario")
		if err != nil {
			fmt.Fprintln(os.Stderr, "scenario:", err)
			return 1
		}
		defer os.RemoveAll(dir)
		out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
		if module = strings.TrimSpace(string(out)); err == nil {
			build := exec.Command("go", "build", "-o", dir+string(filepath.Separator),
				"./cmd/whaleshark", "./test/fakeagent")
			build.Dir, build.Stderr = module, os.Stderr
			err = build.Run()
		}
		// The real keeper starts an agent by its kind's name, found on the
		// PATH: under both names stands the fake agent, and no real one.
		for _, kind := range []string{"claude", "codex"} {
			if agent, _ := filepath.Glob(filepath.Join(dir, "fakeagent*")); err == nil && len(agent) == 1 {
				err = os.Link(agent[0], filepath.Join(dir, kind+filepath.Ext(agent[0])))
			}
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "scenario: building the programs:", err)
			return 1
		}
		os.Setenv("PATH", dir+string(filepath.ListSeparator)+os.Getenv("PATH"))
		agent, _ := exec.LookPath("fakeagent")
		os.Setenv(testkit.EnvAgent, agent)
		tools = dir
		return m.Run()
	}())
}

// The callers a command can be run as; any other is the id of an attempt.
const (
	Orch    = "orch"    // from the lead agent's pane
	Human   = "human"   // with --human, from a pane of the person's own
	Page    = "page"    // as a button of the page: its mark and no pane
	Unbound = "unbound" // from a pane no run is bound to
)

// Backend is what stands behind the terminals of a project.
type Backend string

const (
	Double Backend = "double" // the engine's double, reached as the keeper is
	Keeper Backend = "keeper" // the real keeper, started for the test with a socket of its own
)

// driver is what the runner asks of a stand-in beyond the terminals' interface.
type driver interface {
	Load(*contract.Snapshot)
	Script(attempt, script string)
	Push(kind, pane string)
	Drop(kind, pane string)
	Restart()
}

// Project is one made-up project, in a home folder of its own.
type Project struct {
	Root   string
	Kit    *contract.Kit    // the platform is real, the terminals are the backend's
	Double *fakeengine.Fake // on Double only
	Lead   string           // the lead agent's pane
	Own    string           // a pane of the person's own, which no run records
	// Log is what a run came to, in words no backend changes: each command
	// with how it ended and, once Run has played its last line, the record.
	Log []string

	on    Backend
	drive driver   // nil on a Keeper with no picture and no agents
	env   []string // what a program needs to reach the terminals
	t     testing.TB
	bin   string
	clock string
	now   time.Time
	seq   int // the last event number the fixture came with
}

// Prepare makes a project. With a fixture it creates the run through the
// store and points at it, plants a token for every live attempt, writes
// ui.json and the context files, and gives the double the fixture's
// picture with the two panes ui.json names. scripts is what the fake agents play, by
// attempt; one for an attempt the fixture shows in a tab is started there at
// once, the way start will, in a tab of its own that the record then names.
func Prepare(t testing.TB, f *testkit.Fixture, scripts map[string]string) *Project {
	t.Helper()
	return PrepareOn(t, Double, f, scripts)
}

// PrepareOn is Prepare with the backend named. The real
// keeper is handed a picture as it hands one to itself: in its layout file,
// read at its start, with the agents started in their panes by its own call.
func PrepareOn(t testing.TB, on Backend, f *testkit.Fixture, scripts map[string]string) *Project {
	t.Helper()
	if tools == "" {
		t.Fatal("scenario: the package's TestMain must call scenario.Main")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"HOME": "", "USERPROFILE": "", "XDG_CONFIG_HOME": "config",
		"XDG_STATE_HOME": "state", "XDG_CACHE_HOME": "cache", "APPDATA": "roaming", "LOCALAPPDATA": "local"} {
		t.Setenv(name, filepath.Join(base, "home", dir))
	}
	p := &Project{Root: filepath.Join(base, "project"), Kit: contract.NewKit(), Lead: "w1:p1", Own: "w1:p0",
		t: t, on: on, clock: filepath.Join(base, "clock"), now: time.Now()}
	platform.Plug(p.Kit)
	store.Plug(p.Kit)
	p.bin, _ = exec.LookPath("whaleshark")
	p.must(os.MkdirAll(p.Root, 0o700))
	if on == Keeper && p.Kit.Platform.System() == "windows" {
		t.Skip("the keeper's terminals on Windows are proven on a real one, later")
	}
	// A socket's path is short, so its folder is not under the test's own.
	dir, err := os.MkdirTemp("", "ws")
	p.must(err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	if p.env = []string{contract.EnvSocket + "=" + socket}; on == Keeper && f == nil && len(scripts) == 0 {
		p.keeper(socket, dir)
		lead, err := p.Kit.Terms.TabCreate(p.Root, "Lead", nil)
		p.must(err)
		own, err := p.Kit.Terms.TabCreate(p.Root, "you", nil)
		p.must(err)
		p.Lead, p.Own = lead.ID, own.ID
	} else if on == Keeper {
		p.drive = &onKeeper{p: p, socket: socket, dir: dir}
	} else {
		// The double answers the version the keeper would, and has a window.
		p.Double = fakeengine.New()
		p.Double.Build, p.Double.Attached = contract.Version, true
		t.Cleanup(p.Double.Close)
		p.must(p.Double.Listen(socket))
		p.Kit.Terms, p.drive = p.Double, p.Double
	}
	for id, script := range scripts {
		p.drive.Script(id, script)
	}

	picture := []contract.Pane{{ID: p.Lead, Tab: "w1:t1", Label: "Lead", Cwd: p.Root, Focused: true,
		Agent: "claude", Status: contract.StatusIdle}}
	if f != nil {
		p.now, p.seq, picture = f.Now, f.State.Counters.Seq, f.Terms.Panes
		if o := f.State.Run.Orchestrator; o != nil {
			p.Lead = o.Pane
		}
	}
	p.Clock(0)
	t.Setenv(contract.EnvClock, p.clock)
	var own []*contract.Attempt // the fixture's attempts that get a tab and a fake agent of their own
	if f != nil {
		for _, id := range slices.Sorted(maps.Keys(f.State.Attempts)) {
			if a := f.State.Attempts[id]; a.State.Live() && a.Place.Pane != "" && scripts[id] != "" {
				own = append(own, a)
			}
		}
	}
	picture = slices.DeleteFunc(slices.Clone(picture), func(pane contract.Pane) bool {
		return slices.ContainsFunc(own, func(a *contract.Attempt) bool { return a.Place.Pane == pane.ID })
	})
	picture = append(picture, contract.Pane{ID: p.Own, Tab: "w1:t0", Label: "you", Cwd: p.Root})
	if f != nil {
		for label, id := range map[string]string{"fleet": f.UI.FleetPane, "actions": f.UI.ActionsPane} {
			if id != "" && !slices.ContainsFunc(picture, func(pane contract.Pane) bool { return pane.ID == id }) {
				picture = append(picture, contract.Pane{ID: id, Tab: "w1:t1", Label: label, Cwd: p.Root})
			}
		}
	}
	if p.drive != nil {
		p.drive.Load(&contract.Snapshot{Panes: picture})
	}
	if f == nil {
		return p
	}

	run := p.Kit.Store.Dir(p.Root, f.State.Run.ID)
	dirs, err := p.Kit.Platform.Dirs()
	p.must(err)
	for id, a := range f.State.Attempts {
		if a.State.Live() {
			token := filepath.Join(run, "attempts", id, "token")
			p.must(os.MkdirAll(filepath.Dir(token), 0o700))
			p.must(os.WriteFile(token, []byte("token-of-"+id+"\n"), 0o600))
			a.TokenHash = contract.TokenHash("token-of-" + id)
		}
	}
	ctx := maps.Clone(f.Ctx)
	for _, a := range own {
		prompt := filepath.Join(run, "attempts", a.ID, "prompt.md")
		p.must(os.MkdirAll(filepath.Dir(prompt), 0o700))
		p.must(os.WriteFile(prompt, []byte("Play your script.\n"), 0o600))
		pane, err := p.Kit.Terms.TabCreate(p.Root, f.State.Tasks[a.Task].Name, p.tab(f.State.Run.ID, a))
		p.must(err)
		p.must(p.Kit.Terms.AgentStart(a.Agent.Name, a.Agent.Kind, pane.ID, nil, 10*time.Second))
		if c, ok := ctx[a.Place.Pane]; ok {
			delete(ctx, a.Place.Pane)
			ctx[pane.ID] = c
		}
		a.Place.Tab, a.Place.Pane, a.Place.Terminal, a.Place.Cwd = pane.Tab, pane.ID, pane.Terminal, p.Root
	}
	p.must(p.Kit.Store.Create(p.Root, &f.State))
	p.must(p.Kit.Store.SetCurrent(p.Root, f.State.Run.ID))
	p.must(contract.WriteVersioned(p.Kit.Platform, filepath.Join(dirs.State, "ui.json"), contract.FileVersion, f.UI))
	for pane, c := range ctx {
		p.must(contract.WriteVersioned(p.Kit.Platform, contract.CtxPath(dirs.State, pane), contract.FileVersion, c))
	}
	// An agent is told to begin only now that the record is there: one that
	// reports sooner finds no run to report to.
	for _, a := range own {
		p.must(p.Kit.Terms.Prompt(a.Agent.Name, "Read and follow "+filepath.Join(run, "attempts", a.ID, "prompt.md"), 10*time.Second))
	}
	return p
}

// surroundings is the environment of the test without the variables of ours
// that say who a caller is; the clock and the notices switch stay.
func (p *Project) surroundings() (env []string) {
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(name, "WHALESHARK_") || name == contract.EnvClock || name == contract.EnvNotices {
			env = append(env, kv)
		}
	}
	return env
}

// Env is what a program started in a pane has in its environment to reach
// the terminals and to be known by its pane.
func (p *Project) Env(pane string) []string {
	return append(slices.Clone(p.env), contract.EnvPane+"="+pane)
}

func (p *Project) must(err error) {
	p.t.Helper()
	if err != nil {
		p.t.Fatalf("scenario: %v", err)
	}
}

// tab is what a worker's tab holds, as start gives it. The clock and the
// notices switch are not among it: an agent has them from the keeper's own
// surroundings, which here are the test's.
func (p *Project) tab(run string, a *contract.Attempt) []string {
	return []string{contract.EnvRoot + "=" + p.Root, contract.EnvRun + "=" + run, contract.EnvTask + "=" + a.Task,
		contract.EnvAttempt + "=" + a.ID, contract.EnvBin + "=" + p.bin}
}

// Clock moves the clock every command and every pane of the project reads.
// It stands still in between.
func (p *Project) Clock(by time.Duration) {
	p.now = p.now.Add(by)
	// Replaced, not rewritten: a command that reads the clock while it is
	// being written would find it empty and take the real time instead.
	next, text := p.clock+".next", []byte(p.now.Format(time.RFC3339Nano))
	if os.WriteFile(next, text, 0o600) != nil || os.Rename(next, p.clock) != nil {
		p.must(os.WriteFile(p.clock, text, 0o600))
	}
}

// Record reads the current run as the store has it now, and names the run's
// folder.
func (p *Project) Record() (*contract.State, string) {
	p.t.Helper()
	id, err := p.Kit.Store.Current(p.Root)
	p.must(err)
	s, err := p.Kit.Store.Read(p.Root, id)
	p.must(err)
	return s, p.Kit.Store.Dir(p.Root, id)
}

// Command is the real program with these arguments, ready to be run as that
// caller: in the project's folder, with nothing on its standard input, the
// project's backend for the terminals, and of the variables the caller rules
// read only the caller's own, whatever the test itself was started from.
func (p *Project) Command(who string, args ...string) *exec.Cmd {
	p.t.Helper()
	cmd := exec.Command(p.bin, args...)
	cmd.Dir, cmd.Env = p.Root, p.surroundings()
	switch who {
	case Orch:
		cmd.Env = append(cmd.Env, p.Env(p.Lead)...)
	case Human:
		cmd.Args = slices.Insert(cmd.Args, min(2, len(cmd.Args)), "--human")
		fallthrough
	case Unbound:
		cmd.Env = append(cmd.Env, p.Env(p.Own)...)
	case Page:
		cmd.Env = append(append(cmd.Env, p.env...), contract.EnvFrom+"="+contract.WherePage)
	default:
		s, _ := p.Record()
		a := s.Attempts[who]
		if a == nil {
			p.t.Fatalf("scenario: the record has no attempt %s", who)
		}
		cmd.Env = append(append(cmd.Env, p.Env(a.Place.Pane)...), p.tab(s.Run.ID, a)...)
	}
	return cmd
}
