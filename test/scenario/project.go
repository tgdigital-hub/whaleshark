// Package scenario runs the scripted tests of the whole tool: a made-up
// project on disk, the fake herdr beside it, fake agents in its tabs, and
// the real program started as each kind of caller. README.md is the guide.
package scenario

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/test/fakeherdr"
)

// tools is the folder Main built the three programs into.
var tools string

// Main is the TestMain of a package that runs scenarios. It builds the real
// program, the stand-in for herdr and the fake agent once, puts their folder
// first on the PATH, so that nothing a test starts can reach a real herdr,
// and names the fake agent for the fake herdr.
func Main(m *testing.M) {
	os.Exit(func() int {
		dir, err := os.MkdirTemp("", "scenario")
		if err != nil {
			fmt.Fprintln(os.Stderr, "scenario:", err)
			return 1
		}
		defer os.RemoveAll(dir)
		module, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
		if err == nil {
			build := exec.Command("go", "build", "-o", dir+string(filepath.Separator),
				"./cmd/whaleshark", "./test/fakeherdr/herdr", "./test/fakeagent")
			build.Dir, build.Stderr = strings.TrimSpace(string(module)), os.Stderr
			err = build.Run()
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

// Project is one made-up project, in a home folder of its own.
type Project struct {
	Root  string
	Kit   *contract.Kit // the platform is real, herdr is the fake
	Herdr *fakeherdr.Fake
	Lead  string // the lead agent's pane
	Own   string // a pane of the person's own, which no run records

	t         testing.TB
	bin       string
	clock     string
	now       time.Time
	seq       int  // the last event number the fixture came with
	restarted bool // herdr was restarted: no tab holds a variable of ours
}

// Prepare makes a project. With a fixture it writes the run's state.json,
// the current pointer, ui.json and the context files, and gives the fake
// herdr the fixture's picture. scripts is what the fake agents play, by
// attempt; one for an attempt the fixture shows in a tab is started there at
// once, the way start will, in a tab of its own that the record then names.
func Prepare(t testing.TB, f *testkit.Fixture, scripts map[string]string) *Project {
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
		t: t, clock: filepath.Join(base, "clock"), now: time.Now()}
	platform.Plug(p.Kit)
	p.bin, _ = exec.LookPath("whaleshark")
	if p.Herdr, err = fakeherdr.New(p.Kit); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Herdr.Close)
	p.Kit.Herdr = p.Herdr
	for id, script := range scripts {
		p.Herdr.Script(id, script)
	}
	p.must(os.MkdirAll(p.Root, 0o700))

	picture := []contract.Pane{{ID: p.Lead, Tab: "w1:t1", Label: "Lead", Cwd: p.Root, Focused: true,
		Agent: "claude", Status: contract.StatusIdle}}
	if f != nil {
		p.now, p.seq, picture = f.Now, f.State.Counters.Seq, f.Herdr.Panes
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
	// The fake herdr numbers what it opens from 1, as the picture of a
	// fixture does: tabs are opened and forgotten until its numbers are past
	// the picture's, or a tab opened later would be taken for one of those.
	top := 0
	for _, pane := range picture {
		top = max(top, number(pane.ID), number(pane.Tab))
	}
	if f != nil {
		top = max(top, number(f.UI.FleetPane), number(f.UI.ActionsPane))
	}
	for n := 0; n <= top; {
		spare, err := p.Herdr.TabCreate("", "", nil)
		p.must(err)
		n = number(spare.Tab)
	}
	p.Herdr.Load(&contract.Snapshot{Panes: append(picture, contract.Pane{ID: p.Own, Tab: "w1:t0", Label: "you", Cwd: p.Root})})
	if f == nil {
		return p
	}

	run := filepath.Join(p.Root, ".whaleshark", "runs", f.State.Run.ID)
	dirs, err := p.Kit.Platform.Dirs()
	p.must(err)
	ctx := maps.Clone(f.Ctx)
	for _, a := range own {
		prompt := filepath.Join(run, "attempts", a.ID, "prompt.md")
		p.must(os.MkdirAll(filepath.Dir(prompt), 0o700))
		p.must(os.WriteFile(prompt, []byte("Play your script.\n"), 0o600))
		pane, err := p.Herdr.TabCreate(p.Root, f.State.Tasks[a.Task].Name, p.tab(f.State.Run.ID, a))
		p.must(err)
		p.must(p.Herdr.AgentStart(a.Agent.Name, a.Agent.Kind, pane.ID, nil, 10*time.Second))
		p.must(p.Herdr.Prompt(a.Agent.Name, "Read and follow "+prompt, 10*time.Second))
		if c, ok := ctx[a.Place.Pane]; ok {
			delete(ctx, a.Place.Pane)
			ctx[pane.ID] = c
		}
		a.Place.Tab, a.Place.Pane, a.Place.Terminal, a.Place.Cwd = pane.Tab, pane.ID, pane.Terminal, p.Root
	}
	p.must(contract.WriteVersioned(os.Rename, filepath.Join(run, "state.json"), contract.StateVersion, &f.State))
	p.must(os.WriteFile(filepath.Join(p.Root, ".whaleshark", "current"), []byte(f.State.Run.ID+"\n"), 0o600))
	p.must(contract.WriteVersioned(os.Rename, filepath.Join(dirs.State, "ui.json"), contract.FileVersion, f.UI))
	for pane, c := range ctx {
		p.must(contract.WriteVersioned(os.Rename, filepath.Join(dirs.State, "ctx", pane+".json"), contract.FileVersion, c))
	}
	return p
}

var digits = regexp.MustCompile(`[0-9]+$`)

// number is the number an id of herdr's ends in.
func number(id string) int {
	n, _ := strconv.Atoi(digits.FindString(id))
	return n
}

func (p *Project) must(err error) {
	p.t.Helper()
	if err != nil {
		p.t.Fatalf("scenario: %v", err)
	}
}

// tab is what a worker's tab holds. The clock is among it because the fake
// herdr hands a fake agent none of our variables but its tab's.
func (p *Project) tab(run string, a *contract.Attempt) []string {
	return []string{contract.EnvRoot + "=" + p.Root, contract.EnvRun + "=" + run, contract.EnvTask + "=" + a.Task,
		contract.EnvAttempt + "=" + a.ID, contract.EnvBin + "=" + p.bin, contract.EnvClock + "=" + p.clock}
}

// Clock moves the clock every command and every pane of the project reads.
// It stands still in between.
func (p *Project) Clock(by time.Duration) {
	p.now = p.now.Add(by)
	p.must(os.WriteFile(p.clock, []byte(p.now.Format(time.RFC3339Nano)), 0o600))
}

// Record reads the current run's state.json as it is on disk now, and names
// the run's folder.
func (p *Project) Record() (*contract.State, string) {
	p.t.Helper()
	id, err := os.ReadFile(filepath.Join(p.Root, ".whaleshark", "current"))
	p.must(err)
	dir := filepath.Join(p.Root, ".whaleshark", "runs", strings.TrimSpace(string(id)))
	s := new(contract.State)
	p.must(contract.ReadVersioned(filepath.Join(dir, "state.json"), contract.StateVersion, s))
	return s, dir
}

// Command is the real program with these arguments, ready to be run as that
// caller: in the project's folder, with nothing on its standard input, the
// fake herdr for herdr, and of the variables the caller rules read only the
// caller's own, whatever the test itself was started from.
func (p *Project) Command(who string, args ...string) *exec.Cmd {
	p.t.Helper()
	cmd := exec.Command(p.bin, args...)
	cmd.Dir, cmd.Env = p.Root, p.Herdr.Env()
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		ours := strings.HasPrefix(name, "WHALESHARK_") || strings.HasPrefix(name, "HERDR_")
		if !ours || name == contract.EnvClock || name == contract.EnvNotices {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	switch who {
	case Orch:
		cmd.Env = append(cmd.Env, contract.EnvPane+"="+p.Lead)
	case Human:
		cmd.Args = slices.Insert(cmd.Args, min(2, len(cmd.Args)), "--human")
		fallthrough
	case Unbound:
		cmd.Env = append(cmd.Env, contract.EnvPane+"="+p.Own)
	case Page:
		cmd.Env = append(cmd.Env, contract.EnvFrom+"="+contract.WherePage)
	default:
		s, _ := p.Record()
		a := s.Attempts[who]
		if a == nil {
			p.t.Fatalf("scenario: the record has no attempt %s", who)
		}
		if cmd.Env = append(cmd.Env, contract.EnvPane+"="+a.Place.Pane); !p.restarted {
			cmd.Env = append(cmd.Env, p.tab(s.Run.ID, a)...)
		}
	}
	return cmd
}
