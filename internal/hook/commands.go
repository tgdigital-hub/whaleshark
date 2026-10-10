// Package hook is the three commands an agent's own harness runs, the status
// line, the gate and the report of its state, and the settings that make the
// harness run them.
package hook

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// The forms of the command, as the settings name them.
const (
	formStatus = "statusline"
	formGate   = "gate"
	formState  = "state"
)

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) {
	k.AgentSettings = settings{k}
	k.Handle("hook", run)
}

// run never fails: a hook that ends badly would stand in an agent's way.
// Outside a pane of a project of ours it does nothing, beyond printing the
// person's own status line.
func run(c *contract.Call) (any, error) {
	if len(c.Args) == 0 {
		return nil, nil
	}
	pane, _ := contract.PaneOf(os.Getenv)
	dirs, err := c.Kit.Platform.Dirs()
	if err == nil {
		_, err = os.Stat(c.Kit.Reader().Dir(c.Root, ""))
	}
	ours := err == nil && pane != ""
	switch c.Args[0] {
	case formStatus:
		statusline(c, ours, contract.CtxPath(dirs.State, pane))
	case formGate:
		// The lead agent is told of a pause and not held, and a session of
		// the person's own is none of ours: the gate is a worker's alone.
		if ours && c.Caller.Kind == contract.Worker {
			// The settings of a kind other than Claude Code name it as a
			// second word: each kind needs its own answer.
			gate(c, dirs.State, cmp.Or(contract.Gates[append(c.Args, claude)[1]], contract.Gates[claude]))
		}
	case formState:
		// What the harness says of its agent goes to the terminals that
		// hold its pane, and only the engine's own take it.
		var m struct {
			Session string `json:"session_id"`
			Cwd     string `json:"cwd"`
		}
		if t, takes := c.Kit.Terms.(teller); takes && pane != "" && len(c.Args) > 1 {
			message(c, &m)
			t.Hook(c.Args[1], pane, m.Session, m.Cwd)
		}
	}
	return nil, nil
}

// teller is the terminals' side of `hook state`: one call, contract.OpHook,
// with the event, the pane the hook ran in, and what the agent said.
type teller interface {
	Hook(event, pane, session, cwd string) error
}

func message(c *contract.Call, v any) []byte {
	msg, _ := io.ReadAll(io.LimitReader(c.Stdin, 1<<20))
	json.Unmarshal(msg, v)
	return msg
}

// statusline writes the pane's figure first and runs the person's own status
// line second, because the harness cancels a status line that is still
// running when its next update comes.
func statusline(c *contract.Call, ours bool, file string) {
	var m struct {
		Context struct {
			Used *float64 `json:"used_percentage"`
		} `json:"context_window"`
		Workspace struct {
			Dir string `json:"project_dir"`
		} `json:"workspace"`
	}
	msg := message(c, &m)
	if len(msg) == 0 {
		return // run by hand: no harness is waiting for a line
	}
	if ours {
		f := contract.CtxFile{Versioned: contract.Versioned{Version: contract.FileVersion}, At: c.Now}
		if used := m.Context.Used; used != nil {
			f.Pct, f.Known = int(min(max(*used, 0), 100)+0.5), true
		}
		contract.WriteVersioned(c.Kit.Platform, file, contract.FileVersion, f)
	}

	// The harness shows one status line, the one set nearest the project, and
	// ours has taken that place: the person's own is the next one down.
	dir := m.Workspace.Dir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	files := []string{localFile(dir), filepath.Join(dir, ".claude", "settings.json")}
	if user := os.Getenv("CLAUDE_CONFIG_DIR"); user != "" {
		files = append(files, filepath.Join(user, "settings.json"))
	} else if home, err := os.UserHomeDir(); err == nil {
		files = append(files, filepath.Join(home, ".claude", "settings.json"))
	}
	for _, file := range files {
		var s struct {
			Line entry `json:"statusLine"`
		}
		data, _ := c.Kit.Platform.Peek(file)
		if json.Unmarshal(data, &s); s.Line.Command == "" || isOurs(s.Line.Command, formStatus) {
			continue
		}
		// The shell is handed the line in a variable. As an argument it
		// would not arrive whole on Windows, where the POSIX shell's start-up
		// code splits the command line by rules of its own and takes the
		// single quotes off a path, whose backslashes the shell then eats.
		// #nosec G204 G702 -- the person's own status line, from their own settings, run as their harness runs it
		cmd := exec.Command("sh", "-c", `eval "$`+lineEnv+`"`)
		cmd.Env = append(os.Environ(), lineEnv+"="+s.Line.Command)
		if _, err := exec.LookPath("sh"); err != nil {
			// #nosec G204 G702 -- as above, where there is no POSIX shell
			cmd = exec.Command("cmd", "/c", s.Line.Command)
		}
		cmd.Stdin, cmd.Stdout = bytes.NewReader(msg), c.Out
		cmd.Run()
		return
	}
}

// lineEnv names the variable the person's own status line is handed to the
// shell in.
const lineEnv = "WHALESHARK_OWN_STATUSLINE"

// refusal is the deny decision alone: the step is refused and the turn goes on.
const refusal = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":%q}}` + "\n"

// gate answers a worker's harness before a step. While the person has
// pressed Stop all it answers with both parts, the deny decision and the end
// of the turn: the second alone lets the step run. A pause mark that cannot
// be read holds the worker too.
func gate(c *contract.Call, stateDir string, kind contract.Gate) {
	if p, err := contract.Paused(c.Kit.Platform.Peek, stateDir); p != nil || err != nil {
		fmt.Fprintf(c.Out, kind.Answer+"\n", contract.PausedReason, contract.PausedReason)
		return
	}
	var m struct {
		Input struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	message(c, &m)
	words := strings.Fields(m.Input.Command)
	for i, w := range words {
		starts := i == 0 || strings.ContainsAny(words[i-1][len(words[i-1])-1:], ";&|")
		switch {
		case w == "--human" || strings.HasPrefix(w, "--human="):
			fmt.Fprintf(c.Out, refusal, "--human is the person's to give, not an agent's. Run the command without it.")
		case starts && strings.TrimSuffix(filepath.Base(w), ".exe") == "herdr":
			fmt.Fprintf(c.Out, refusal, "herdr is not a worker's to run: the tabs are the tool's. Use your whaleshark commands.")
		case starts && strings.TrimSuffix(filepath.Base(w), ".exe") == "whaleshark" && slices.Contains([]string{"open", "engine"}, append(words, "")[i+1]):
			fmt.Fprintf(c.Out, refusal, "The window and the keeper are the person's to run, not a worker's.")
		default:
			continue
		}
		return
	}
}
