package doctor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// program puts an empty program of a name into a folder, under the names
// each system would look it up by.
func (d *desk) program(dir, name string) string {
	d.Helper()
	for _, ending := range []string{"", ".exe"} {
		d.write(filepath.Join(dir, name+ending), "")
		if err := os.Chmod(filepath.Join(dir, name+ending), 0o700); err != nil {
			d.Fatal(err)
		}
	}
	return filepath.ToSlash(filepath.Join(dir, name))
}

// Every tool that is wired in has its row: found or not, what it can tell,
// whether Stop all holds it; a description that is not used is a problem
// with its reason; and the machine's memory is said in agents.
func TestTheAgentToolsHaveTheirRows(t *testing.T) {
	d := project(t)
	bin, was := t.TempDir(), ask
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() { ask = was })
	ask = func(name string, args ...string) (string, error) {
		if name == "sysctl" {
			return "17179869184", nil
		}
		return was(name, args...)
	}
	d.program(bin, "claude")
	tools := filepath.Join(d.dirs.Config, contract.ToolsDir)
	d.write(filepath.Join(tools, "hermes.toml"), "run = ['"+d.program(bin, "hermes")+"', 'chat']\nhooks = 'none'\n")
	d.write(filepath.Join(tools, "reader.toml"), "run = ['"+d.program(bin, "reader")+"']\nhooks = 'none'\n[screen]\nready = 'type here'\n")
	d.write(filepath.Join(tools, "local.toml"), "run = ['"+filepath.ToSlash(filepath.Join(bin, "gone"))+"']\nhooks = 'claude'\n")
	d.write(filepath.Join(tools, "loose.toml"), "run = ['agent']\nhooks = 'none'\n")

	e, exit := d.doctor()
	d.want(e, ok, "agent tool claude is wired in", "exact states, held by Stop all")
	if l := e.line("agent tool claude"); l == nil || strings.Contains(l.Text, "proven") {
		t.Errorf("Claude Code has run for real: %+v", l)
	}
	// Codex is on the path of some who run this, and not of others.
	if l := e.line("agent tool codex", "exact states, not held by Stop all, not yet proven here"); l == nil || l.Mark == problem {
		t.Errorf("Codex's row: %+v", l)
	}
	d.want(e, ok, "agent tool hermes is wired in", "busy or quiet only, not held by Stop all, not yet proven here")
	d.want(e, ok, "agent tool reader is wired in", "states read off its screen, not held by Stop all")
	d.want(e, problem, "program of the agent tool local is gone", "whaleshark set tools.local.run")
	d.want(e, problem, "loose.toml is not used", "full path")
	d.want(e, note, "16384 MB of memory", "about 3276 agents at 5 MB a pane")
	if exit != contract.ExitFailed || e.Problems != 2 {
		t.Errorf("exit %d, %d problems:\n%s", exit, e.Problems, e.text())
	}
	if d.k.Platform.System() == "windows" { // which cannot tell whose a file is
		return
	}
	d.want(e, ok, "what describes your agent tools is yours alone", "hermes.toml")

	// A description can hold a key: open to another login it is a problem,
	// which --fix puts right.
	if err := os.Chmod(filepath.Join(tools, "hermes.toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, _ = d.doctor()
	d.want(e, problem, "what describes your agent tools", "hermes.toml", "open to another login", again)
	e, _ = d.doctor("fix")
	d.want(e, fixed, "hermes.toml", "yours alone now")
	if info, err := os.Stat(filepath.Join(tools, "hermes.toml")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("after --fix: %v, %v", info.Mode(), err)
	}
}

// Where the system gives no figure for its memory, the row says so.
func TestMemoryThatCannotBeTold(t *testing.T) {
	d := project(t)
	was, root := ask, sysRoot
	t.Cleanup(func() { ask, sysRoot = was, root })
	ask = func(string, ...string) (string, error) { return "", errors.New("no such program") }
	e, _ := d.doctor()
	d.want(e, note, "memory carries cannot be told")
	sysRoot = t.TempDir()
	d.write(filepath.Join(sysRoot, "proc", "meminfo"), "MemTotal:        8388608 kB\nMemAvailable:    1000000 kB\n")
	e, _ = d.doctor()
	d.want(e, note, "8192 MB of memory", "about 1638 agents")
}

// trial is the keeper for the one test of a tool: an agent that starts,
// takes a line and rests, each unless told otherwise, and everything asked
// of it.
type trial struct {
	contract.NoTerminals
	start, prompt error
	rests         bool
	asked         []string
	dir           string
	args          []string
}

func (t *trial) TabCreate(cwd, label string, env []string) (contract.Pane, error) {
	t.asked, t.dir = append(t.asked, "tab "+label), cwd
	return contract.Pane{ID: "p4", Tab: "t2"}, nil
}
func (t *trial) AgentStart(name, kind, pane string, args []string, wait time.Duration) error {
	t.asked, t.args = append(t.asked, "start "+kind+" in "+pane), args
	return t.start
}
func (t *trial) Prompt(name, text string, wait time.Duration) error {
	t.asked = append(t.asked, "prompt "+text)
	return t.prompt
}
func (t *trial) Screen(string) (string, error) {
	if t.prompt != nil {
		return "a tool\n> \n", nil
	}
	return "a tool\n> say ready\nready\n\n  type here  \n\n", nil
}
func (t *trial) Snapshot(context.Context) (*contract.Snapshot, error) {
	status := contract.StatusBusy
	if t.rests {
		status = contract.StatusQuiet
	}
	return &contract.Snapshot{Panes: []contract.Pane{{ID: "p1", Status: contract.StatusIdle}, {ID: "p4", Status: status}}}, nil
}
func (t *trial) TabClose(tab string) error { t.asked = append(t.asked, "close "+tab); return nil }

// The one test of a tool: started in a tab of its own in a scratch folder,
// handed one line, seen to rest, and the line it rests at handed back. A
// step that does not come is named, and the tab and the folder go either way.
func TestTryATool(t *testing.T) {
	d := project(t)
	tool := contract.Tool{Name: "hermes", Run: []string{"/opt/hermes/hermes"}, Hooks: contract.HooksNone}
	for _, c := range []struct {
		terms trial
		want  Trial
		err   string
	}{
		{trial{rests: true}, Trial{true, true, true, true, "type here"}, ""},
		{trial{start: contract.ErrAgentNotReady}, Trial{}, "hermes did not start"},
		{trial{prompt: contract.ErrPromptStalled}, Trial{Started: true}, "cannot be wired in"},
		{trial{}, Trial{Started: true, Pasted: true, Worked: true}, "did not come to rest: it is still at work"},
	} {
		d.k.Terms, d.hooks.args = &c.terms, []string{"--settings", "x"}
		got, err := Try(d.k, tool, 50*time.Millisecond)
		if got != c.want || (err == nil) != (c.err == "") || err != nil && !strings.Contains(err.Error(), c.err) {
			t.Errorf("got %+v, %v; want %+v, %q", got, err, c.want, c.err)
		}
		if asked := c.terms.asked; len(asked) < 3 || asked[0] != "tab try hermes" || asked[1] != "start hermes in p4" || asked[len(asked)-1] != "close t2" ||
			slices.Contains(asked, "prompt say ready") != got.Started || !slices.Equal(c.terms.args, d.hooks.args) {
			t.Errorf("asked of the keeper: %v with %v", asked, c.terms.args)
		}
		if rel, err := filepath.Rel(d.dirs.Cache, c.terms.dir); err != nil || !filepath.IsLocal(rel) || exists(c.terms.dir) {
			t.Errorf("the scratch folder %s is not in the cache, or was left there", c.terms.dir)
		}
	}
}

// `doctor --try NAME` is that test as a command, which the settings screen's
// "Try it" runs: what was seen in one line, a step that did not come as a
// failure that names it, and a name nobody wired in refused before any tab.
func TestDoctorTryIsTheOneTestOfAWiredTool(t *testing.T) {
	d := project(t)
	d.write(filepath.Join(d.dirs.Config, contract.ToolsDir, "hermes.toml"), "run = ['"+d.program(t.TempDir(), "hermes")+"', 'chat']\nhooks = 'none'\n")
	try := func(name string, terms *trial) (string, *contract.Refusal) {
		out := new(bytes.Buffer)
		d.k.Terms = terms
		_, err := run(&contract.Call{Kit: d.k, Command: contract.Find("doctor"), Flags: map[string][]string{"try": {name}}, Caller: d.caller,
			Root: d.root, Now: time.Now(), Out: out, Err: &d.errw})
		r, _ := err.(*contract.Refusal)
		return out.String(), r
	}
	if out, r := try("hermes", &trial{rests: true}); r != nil || out != "hermes · started · took the paste · went to work · came to rest. At rest its screen shows: type here\n" {
		t.Errorf("a tool that passes: %q, %+v", out, r)
	}
	if _, r := try("hermes", &trial{prompt: contract.ErrPromptStalled}); r == nil || r.Exit != contract.ExitFailed || r.Code != "trial" ||
		!strings.HasPrefix(r.Message, "hermes · started, and then: ") || !strings.Contains(r.Message, "cannot be wired in") || r.Data != (Trial{Started: true}) {
		t.Errorf("a tool that takes no paste: %+v", r)
	}
	terms := &trial{}
	if _, r := try("nobody", terms); r == nil || r.Code != "no_tool" || len(terms.asked) != 0 {
		t.Errorf("a tool nobody wired in: %+v, asked %v", r, terms.asked)
	}
	if f := contract.Find("doctor").Flags; !slices.ContainsFunc(f, func(f contract.Flag) bool { return f.Name == "try" && f.Who == contract.H }) {
		t.Error("--try is not the person's alone")
	}
}
