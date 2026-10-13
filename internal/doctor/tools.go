package doctor

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// paneMemory is what the engine was measured to need for one more pane, in
// megabytes: the keeper's part, an agent's own program not counted.
const paneMemory = 5

// tools is the rows of the agent tools: the login's own descriptions held
// to being the login's alone, everything among them that is not used, and
// each tool that is wired in as the guide and the settings screen word it,
// its rank and what is known of it, with where its program is. The last row is how
// many agents the machine's memory carries, whatever the limits allow.
func (e *exam) tools() {
	dir := filepath.Join(e.dirs.Config, contract.ToolsDir)
	files, _ := filepath.Glob(filepath.Join(dir, "*.toml"))
	for _, file := range append([]string{dir}, files...) {
		e.private("what describes your agent tools", file)
	}
	tools, faults := contract.Tools(e.k.Platform, e.dirs)
	for _, fault := range faults {
		e.say(problem, "", "%s", fault)
	}
	for _, t := range tools {
		line := t.Name + ": " + t.Rank() + " · " + t.Facts()
		if t.Suits != "" {
			line += "; suits: " + t.Suits
		}
		if path, err := exec.LookPath(t.Run[0]); err == nil {
			e.say(ok, "", "the agent tool %s; found: %s", line, path)
		} else if filepath.IsAbs(t.Run[0]) {
			e.say(problem, "whaleshark set tools."+t.Name+".run <its program and arguments>", "the program of the agent tool %s is gone: %s", t.Name, t.Run[0])
		} else {
			e.say(note, "", "the agent tool %s; not installed: no %s is on your path", line, t.Run[0])
		}
	}
	mb := memory()
	if mb == 0 {
		e.say(note, "", "how many agents this machine's memory carries cannot be told on this system")
	} else {
		e.say(note, "", "this machine has %d MB of memory: the engine carries about %d agents at %d MB a pane, an agent's own program not counted", mb, mb/paneMemory, paneMemory)
	}
}

// memory is this machine's memory in megabytes, 0 where the system does
// not say. The kit's Machine is it by agentMemory: what the warning of
// unlimited names as "about N agents here".
func memory() (mb int) {
	info, _ := os.ReadFile(sysRoot + "/proc/meminfo")
	if _, rest, found := strings.Cut(string(info), "MemTotal:"); found {
		fmt.Sscan(rest, &mb)
		mb >>= 10
	} else if out, err := ask("sysctl", "-n", "hw.memsize"); err == nil {
		fmt.Sscan(out, &mb)
		mb >>= 20
	}
	return mb
}

// Trial is what the one test of a tool saw, step by step, and the last line
// its screen showed at rest, which may serve as its description's ready.
type Trial struct {
	Started bool   `json:"started"`
	Pasted  bool   `json:"pasted"`
	Worked  bool   `json:"worked"`
	Rested  bool   `json:"rested"`
	Rest    string `json:"rest,omitempty"`
}

// tryWait is how long each step of a tool's test is given.
const tryWait = 20 * time.Second

// try is `doctor --try NAME`, which the settings screen's "Try it" runs: the
// one test of a tool that is wired in, said step by step in one line and
// kept, with the day, as the tool's Tried, whether or not every step came.
func try(c *contract.Call, name string) (any, error) {
	dirs, err := c.Kit.Platform.Dirs()
	tools, _ := contract.Tools(c.Kit.Platform, dirs)
	t, wired := contract.FindTool(tools, name)
	if err != nil || !wired || !t.Found() {
		return nil, &contract.Refusal{Exit: contract.ExitFailed, Code: "no_tool",
			Message: contract.NotWired(name)}
	}
	tr, err := Try(c.Kit, t, tryWait)
	tried := contract.ToolTried{Day: c.Now.Format(time.DateOnly)}
	for i, came := range []bool{tr.Started, tr.Pasted, tr.Worked, tr.Rested} {
		if came {
			tried.Saw = append(tried.Saw, contract.TriedSaw[i])
		}
	}
	saw := strings.Join(append([]string{t.Name}, tried.Saw...), " · ")
	if kerr := keep(c.Kit.Platform, dirs, t, tried); kerr != nil {
		fmt.Fprintf(c.Err, "What was seen is not kept: %v.\n", kerr)
	}
	if err != nil {
		return nil, &contract.Refusal{Exit: contract.ExitFailed, Code: "trial", Data: tr,
			Message: cli.Plain(fmt.Sprintf("%s, and then: %v.", saw, err))}
	}
	fmt.Fprintf(c.Out, "%s. At rest its screen shows: %s\n", saw, cli.Plain(tr.Rest))
	return tr, nil
}

// keep writes what a test saw into the tool's own file and leaves the rest
// of that file as it stands. A built-in with no file gets one that holds its
// label, program and hooks and no more: its screen's words and its options
// stay the program's, for a later release to know better.
func keep(p contract.Platform, d contract.Dirs, t contract.Tool, tried contract.ToolTried) error {
	own := contract.Tool{Name: t.Name}
	if data, err := p.Peek(filepath.Join(d.Config, contract.ToolsDir, t.Name+".toml")); t.Mine {
		if err := toml.Unmarshal(data, &own); err != nil {
			return err
		}
	} else if err == nil {
		return errors.New("its file is not used, and doctor says why")
	}
	if own.Label, own.Hooks, own.Tried = cmp.Or(own.Label, t.Label), cmp.Or(own.Hooks, t.Hooks), tried; own.Run == nil {
		own.Run = t.Run
	}
	return contract.WriteTool(p, d, own)
}

// Try is the one test of a tool that is wired in: it is started in a tab of
// its own, in a scratch folder of the login's cache with the hooks its
// description names, handed one line, and watched until it rests; each step
// is given wait. The error names the step that did not come. The tab and
// the folder are gone when it returns.
func Try(k *contract.Kit, t contract.Tool, wait time.Duration) (tr Trial, err error) {
	dir, err := k.Platform.PrivateTemp()
	if err != nil {
		return tr, err
	}
	defer os.RemoveAll(dir)
	pane, err := k.Terms.TabCreate(dir, "try "+t.Name, nil)
	if err != nil {
		return tr, err
	}
	defer k.Terms.TabClose(pane.Tab)
	setup, _ := k.AgentSettings.Ensure(t.Name, dir, dir, true)
	const name, line = "tool-test", "say ready"
	if err = k.Terms.AgentStart(name, t.Name, pane.ID, setup.Args, wait); err != nil {
		return tr, fmt.Errorf("%s did not start: %w", t.Name, err)
	}
	tr.Started = true
	err = k.Terms.Prompt(name, line, wait)
	text, _ := k.Terms.Screen(pane.ID)
	tr.Pasted, tr.Worked = strings.Contains(text, line), err == nil
	if err != nil {
		return tr, fmt.Errorf("%s did not go to work on a line pasted at its prompt, and a tool that takes none cannot be wired in: %w", t.Name, err)
	}
	for end := time.Now().Add(wait); !tr.Rested; time.Sleep(wait / 100) {
		snap, err := k.Terms.Snapshot(context.Background())
		if err == nil && !time.Now().Before(end) {
			err = errors.New("it is still at work")
		}
		if err != nil {
			return tr, fmt.Errorf("%s did not come to rest: %w", t.Name, err)
		}
		tr.Rested = slices.ContainsFunc(snap.Panes, func(p contract.Pane) bool {
			return p.ID == pane.ID && (p.Status == contract.StatusIdle || p.Status == contract.StatusQuiet)
		})
	}
	text, _ = k.Terms.Screen(pane.ID)
	rows := strings.Split(strings.TrimSpace(text), "\n")
	tr.Rest = strings.TrimSpace(rows[len(rows)-1])
	return tr, nil
}
