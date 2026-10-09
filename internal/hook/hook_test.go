package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/internal/store"
)

// helperEnv turns this test program into a person's own status line: it
// says whether the file named there exists and how long its message was.
const helperEnv = "WHALESHARK_TEST_STATUSLINE"

func TestMain(m *testing.M) {
	if file := os.Getenv(helperEnv); file != "" {
		msg, _ := io.ReadAll(os.Stdin)
		_, err := os.Stat(file)
		fmt.Printf("mine written=%v bytes=%d\n", err == nil, len(msg))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// login is a pretended login with one project of ours, and the program as
// the entry point builds it from the packages the hooks stand on.
type login struct {
	t                 *testing.T
	k                 *contract.Kit
	home, root, state string
}

func newLogin(t *testing.T) *login {
	home, root := t.TempDir(), t.TempDir()
	for _, name := range []string{"HOME", "USERPROFILE"} {
		t.Setenv(name, home)
	}
	for _, name := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(name, filepath.Join(home, name))
	}
	for _, name := range []string{contract.EnvPane, contract.EnvActivePane, contract.EnvTermPane, contract.EnvTermActivePane,
		contract.EnvAttempt, contract.EnvRun, contract.EnvFrom, contract.EnvClock, "CLAUDE_CONFIG_DIR", helperEnv} {
		t.Setenv(name, "")
	}
	t.Setenv(contract.EnvRoot, root)
	k := contract.NewKit()
	for _, plug := range []func(*contract.Kit){platform.Plug, store.Plug, Plug, cli.Plug} {
		plug(k)
	}
	dirs, err := k.Platform.Dirs()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(k.Reader().Dir(root, ""), 0o700); err != nil {
		t.Fatal(err)
	}
	return &login{t, k, home, root, dirs.State}
}

// hook runs one form of the command as a harness would, and holds it to
// its promise: exit 0 and nothing on standard error, whatever it was given.
func (l *login) hook(stdin string, args ...string) string {
	l.t.Helper()
	var out, errw bytes.Buffer
	code := l.k.Main(l.k, append([]string{"hook"}, args...), strings.NewReader(stdin), &out, &errw)
	if code != 0 || errw.Len() > 0 {
		l.t.Fatalf("hook %v: exit %d, standard error %q", args, code, errw.String())
	}
	return out.String()
}

func (l *login) pause() {
	l.t.Helper()
	if err := contract.SetPaused(l.state, &contract.Pause{At: time.Now(), By: "human"}); err != nil {
		l.t.Fatal(err)
	}
}

func (l *login) ctx(pane string) (f contract.CtxFile) {
	l.t.Helper()
	if err := contract.ReadVersioned(os.ReadFile, contract.CtxPath(l.state, pane), contract.FileVersion, &f); err != nil {
		l.t.Fatal(err)
	}
	return f
}

// recorded is a message Claude Code handed its status line in a herdr pane,
// with invented names in place of the paths and ids, the project's folder
// put in, and the figure of the context window (5 when it was recorded).
func recorded(t *testing.T, project, used string) string {
	data, err := os.ReadFile(filepath.Join("testdata", "statusline.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := json.Marshal(project)
	return strings.NewReplacer(`"PROJECT"`, string(dir), "USED", used).Replace(string(data))
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStatuslineWritesTheFigure(t *testing.T) {
	l := newLogin(t)
	t.Setenv(contract.EnvPane, "w1:p2")
	before := time.Now()
	if out := l.hook(recorded(t, l.root, "5"), "statusline"); out != "" {
		t.Errorf("no status line of the person's own, yet it printed %q", out)
	}
	if f := l.ctx("w1:p2"); f.Pct != 5 || !f.Known || f.At.Before(before) || f.Version != contract.FileVersion {
		t.Errorf("the file holds %+v", f)
	}
	if name := filepath.Base(contract.CtxPath(l.state, "w1:p2")); name != "w1+p2.json" {
		t.Errorf("the file is named %s", name)
	}

	l.hook(recorded(t, l.root, "41.6"), "statusline")
	if f := l.ctx("w1:p2"); f.Pct != 42 || !f.Known {
		t.Errorf("41.6 gave %+v", f)
	}
	l.hook(recorded(t, l.root, "null"), "statusline")
	if f := l.ctx("w1:p2"); f.Known || f.At.IsZero() {
		t.Errorf("an empty figure gave %+v, not unknown", f)
	}
	l.hook("not a message", "statusline")
	if f := l.ctx("w1:p2"); f.Known || f.At.IsZero() {
		t.Errorf("a broken message gave %+v, not unknown", f)
	}

	// The keeper's variable counts before herdr's.
	t.Setenv(contract.EnvTermPane, "p7")
	l.hook(recorded(t, l.root, "9"), "statusline")
	if f := l.ctx("p7"); f.Pct != 9 || !f.Known {
		t.Errorf("in a pane of the keeper's the file holds %+v", f)
	}

	// A file a newer program wrote is left as it is.
	newer := `{"version": 99, "pct": 77}`
	write(t, contract.CtxPath(l.state, "p7"), newer)
	l.hook(recorded(t, l.root, "9"), "statusline")
	if data, _ := os.ReadFile(contract.CtxPath(l.state, "p7")); string(data) != newer {
		t.Errorf("a newer file was replaced: %s", data)
	}
}

func TestStatuslineOutsideOurPanes(t *testing.T) {
	l := newLogin(t)
	none := func(why string) {
		t.Helper()
		l.hook(recorded(t, l.root, "5"), "statusline")
		if entries, _ := os.ReadDir(filepath.Join(l.state, "ctx")); len(entries) > 0 {
			t.Errorf("%s: a file was written", why)
		}
	}
	none("no pane")
	t.Setenv(contract.EnvPane, "w1:p2")
	t.Setenv(contract.EnvRoot, t.TempDir())
	none("a pane, in a folder that is no project of ours")
}

// The person's own status line still prints, with the same message, after
// the figure is written; and nearest the project wins, as in the harness.
func TestStatuslineChains(t *testing.T) {
	l := newLogin(t)
	dialect := contract.ShellPosix
	if _, err := exec.LookPath("sh"); err != nil {
		dialect = contract.ShellCmd
	}
	mine := l.k.Platform.Quote(dialect, []string{os.Args[0]})
	own, _ := json.Marshal(map[string]any{"statusLine": entry{"command", mine}})
	write(t, filepath.Join(l.home, ".claude", "settings.json"), string(own))
	if _, err := l.k.AgentSettings.Ensure(claude, l.root, "", true); err != nil {
		t.Fatal(err)
	}
	file := contract.CtxPath(l.state, "w1:p2")
	t.Setenv(helperEnv, file)
	msg := recorded(t, l.root, "5")
	want := fmt.Sprintf("mine written=true bytes=%d\n", len(msg))

	t.Setenv(contract.EnvPane, "w1:p2")
	if out := l.hook(msg, "statusline"); out != want {
		t.Errorf("in a pane of ours it printed %q, want %q", out, want)
	}
	if out := l.hook("", "statusline"); out != "" {
		t.Errorf("with no message it printed %q", out)
	}
	t.Setenv(contract.EnvPane, "")
	t.Setenv(helperEnv, filepath.Join(l.home, "none"))
	if out, want := l.hook(msg, "statusline"), strings.Replace(want, "true", "false", 1); out != want {
		t.Errorf("outside a pane it printed %q, want %q", out, want)
	}

	// Moved by the variable Claude Code has for it; and the project's own
	// shared file comes before the login's.
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(l.home, "moved"))
	if out := l.hook(msg, "statusline"); out != "" {
		t.Errorf("with the settings folder moved it printed %q", out)
	}
	write(t, filepath.Join(l.root, ".claude", "settings.json"), string(own))
	if out := l.hook(msg, "statusline"); !strings.HasPrefix(out, "mine ") {
		t.Errorf("the project's own status line printed %q", out)
	}
}

// answer is what Claude Code reads from a hook before a tool call.
type answer struct {
	Continue   *bool  `json:"continue"`
	StopReason string `json:"stopReason"`
	Specific   struct {
		Event    string `json:"hookEventName"`
		Decision string `json:"permissionDecision"`
		Reason   string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

func step(command string) string {
	msg, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]string{"command": command}})
	return string(msg)
}

func TestGateWhilePaused(t *testing.T) {
	l := newLogin(t)
	t.Setenv(contract.EnvPane, "w1:p2")
	t.Setenv(contract.EnvAttempt, "T3.1")
	if out := l.hook(step("echo second"), "gate"); out != "" {
		t.Fatalf("with no mark the gate said %q", out)
	}
	l.pause()
	out := l.hook(step("echo second"), "gate")
	var a answer
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		t.Fatalf("%q: %v", out, err)
	}
	// Form B, both parts: the deny refuses the step, continue ends the turn.
	if a.Continue == nil || *a.Continue || a.StopReason != contract.PausedReason {
		t.Errorf("the turn is not ended: %s", out)
	}
	if a.Specific.Event != "PreToolUse" || a.Specific.Decision != "deny" || a.Specific.Reason != contract.PausedReason {
		t.Errorf("the step is not refused: %s", out)
	}

	// A mark that cannot be read holds too.
	if err := os.Remove(filepath.Join(l.state, "paused")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(l.state, "paused"), 0o700); err != nil {
		t.Fatal(err)
	}
	if l.hook(step("echo second"), "gate") != out {
		t.Error("a mark that cannot be read let the step through")
	}
}

// Every agent in a shared folder runs the same gate: it holds the workers.
func TestGateIsAWorkersAlone(t *testing.T) {
	l := newLogin(t)
	l.pause()
	silent := func(why string) {
		t.Helper()
		if out := l.hook(step("herdr tab close w1:t1 --human"), "gate"); out != "" {
			t.Errorf("%s: the gate said %q", why, out)
		}
	}
	t.Setenv(contract.EnvAttempt, "T3.1")
	silent("a worker with no pane")
	t.Setenv(contract.EnvAttempt, "")
	t.Setenv(contract.EnvPane, "w1:p1")
	silent("a pane that is no worker's")
	t.Setenv(contract.EnvAttempt, "T3.1")
	t.Setenv(contract.EnvRoot, t.TempDir())
	silent("a worker in a folder that is no project of ours")
}

func TestGateTurnsDownCommandLines(t *testing.T) {
	l := newLogin(t)
	t.Setenv(contract.EnvPane, "w1:p2")
	t.Setenv(contract.EnvAttempt, "T3.1")
	for command, word := range map[string]string{
		"herdr tab close w1:t1":                  "herdr",
		"cd src && /usr/local/bin/herdr pane ls": "herdr",
		"sleep 1; herdr agent list":              "herdr",
		"whaleshark answer q7 yes --human":       "--human",
		"whaleshark pause --human=1":             "--human",
		"go test ./...":                          "",
		"echo herdr is a word":                   "",
		"ls --human-readable":                    "",
		"":                                       "",
	} {
		out := l.hook(step(command), "gate")
		if word == "" {
			if out != "" {
				t.Errorf("%q: the gate said %q", command, out)
			}
			continue
		}
		var a answer
		if err := json.Unmarshal([]byte(out), &a); err != nil {
			t.Fatalf("%q gave %q: %v", command, out, err)
		}
		// The deny alone: the step is refused and the turn goes on.
		if a.Continue != nil || a.Specific.Event != "PreToolUse" || a.Specific.Decision != "deny" || !strings.HasPrefix(a.Specific.Reason, word) {
			t.Errorf("%q gave %s", command, out)
		}
	}
	if out := l.hook(`{"tool_name":"Read","tool_input":{"file_path":"a"}}`, "gate"); out != "" {
		t.Errorf("a step that is no command line: the gate said %q", out)
	}
}

func TestStateAndUnknownFormsDoNothing(t *testing.T) {
	l := newLogin(t)
	l.pause()
	t.Setenv(contract.EnvPane, "w1:p2")
	t.Setenv(contract.EnvAttempt, "T3.1")
	for _, args := range [][]string{{"state", "Stop"}, {"other"}} {
		if out := l.hook(step("echo"), args...); out != "" {
			t.Errorf("hook %v printed %q", args, out)
		}
	}
}

func settingsOf(t *testing.T, path string) (v map[string]any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("%s: %v", data, err)
	}
	return v
}

// has reports the status line of a settings file and how many gates it holds.
func has(t *testing.T, path string) (status string, gates int) {
	t.Helper()
	var s struct {
		Line  entry `json:"statusLine"`
		Hooks struct {
			Pre []group `json:"PreToolUse"`
		} `json:"hooks"`
	}
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("%s: %v", data, err)
	}
	for _, g := range s.Hooks.Pre {
		for _, h := range g.Hooks {
			if h.Type == "command" && h.Command == quoted(os.Args[0])+" hook gate" && g.Matcher == "*" {
				gates++
			}
		}
	}
	return s.Line.Command, gates
}

func quoted(bin string) string {
	return platform.New("linux").Quote(contract.ShellPosix, []string{bin})
}

func TestEnsureWritesTheFoldersFile(t *testing.T) {
	l := newLogin(t)
	file := localFile(l.root)
	setup, err := l.k.AgentSettings.Ensure(claude, l.root, "", true)
	if err != nil || setup.File != file || setup.Args != nil || !setup.Gated {
		t.Fatalf("%+v, %v", setup, err)
	}
	if status, gates := has(t, file); status != quoted(os.Args[0])+" hook statusline" || gates != 1 {
		t.Errorf("the file holds status line %q and %d gates", status, gates)
	}
	first, _ := os.ReadFile(file)
	for _, write := range []bool{true, false} {
		setup, err = l.k.AgentSettings.Ensure(claude, l.root, t.TempDir(), write)
		if again, _ := os.ReadFile(file); err != nil || setup.File != file || setup.Args != nil || string(again) != string(first) {
			t.Errorf("asked again (write %v): %+v, %v\n%s", write, setup, err, again)
		}
	}
	if err := l.k.AgentSettings.Remove(claude, l.root); err != nil {
		t.Fatal(err)
	}
	if v := settingsOf(t, file); len(v) != 0 {
		t.Errorf("after the removal the file holds %v", v)
	}
}

const theirs = `{
  "permissions": {"allow": ["Bash(go test:*)"], "defaultMode": "acceptEdits"},
  "hooks": {
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "cd tools && ./check <in >out", "timeout": 5}]}],
    "Stop": [{"hooks": [{"type": "command", "command": "say done"}]}]
  }
}`

func TestEnsureKeepsWhatThePersonHad(t *testing.T) {
	l := newLogin(t)
	file := localFile(l.root)
	write(t, file, theirs)
	before := settingsOf(t, file)
	if _, err := l.k.AgentSettings.Ensure(claude, l.root, "", true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(file)
	if _, gates := has(t, file); gates != 1 || !strings.Contains(string(data), `"cd tools && ./check <in >out"`) {
		t.Errorf("the file holds %s", data)
	}
	// The same file with the program somewhere else is put right, not added to.
	write(t, file, strings.ReplaceAll(string(data), quoted(os.Args[0]), "/old/whaleshark"))
	if _, err := l.k.AgentSettings.Ensure(claude, l.root, "", true); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(file); strings.Contains(string(data), "/old/") {
		t.Errorf("the old entries stayed: %s", data)
	}
	if status, gates := has(t, file); gates != 1 || !isOurs(status, formStatus) {
		t.Errorf("status line %q and %d gates", status, gates)
	}
	if err := l.k.AgentSettings.Remove(claude, l.root); err != nil {
		t.Fatal(err)
	}
	if after := settingsOf(t, file); !reflect.DeepEqual(before, after) {
		t.Errorf("after the removal the file holds %v, and held %v", after, before)
	}

	// A status line of the person's own in this very file is not replaced.
	write(t, file, `{"statusLine": {"type": "command", "command": "my-line", "padding": 0}}`)
	if _, err := l.k.AgentSettings.Ensure(claude, l.root, "", true); err != nil {
		t.Fatal(err)
	}
	if status, gates := has(t, file); status != "my-line" || gates != 1 {
		t.Errorf("status line %q and %d gates", status, gates)
	}
	if l.k.AgentSettings.Remove(claude, l.root); settingsOf(t, file)["statusLine"].(map[string]any)["padding"] != 0.0 {
		t.Error("the person's status line did not come through whole")
	}
}

// A file the person said no to is never changed: the hooks ride on the
// agent's command line, from a file of ours.
func TestEnsureWithoutWriting(t *testing.T) {
	l := newLogin(t)
	file, scratch := localFile(l.root), t.TempDir()
	for _, had := range []string{"", theirs} {
		if had != "" {
			write(t, file, had)
		}
		setup, err := l.k.AgentSettings.Ensure(claude, l.root, scratch, false)
		if err != nil || setup.File != "" || !setup.Gated || len(setup.Args) != 2 || setup.Args[0] != "--settings" {
			t.Fatalf("%+v, %v", setup, err)
		}
		if status, gates := has(t, setup.Args[1]); !isOurs(status, formStatus) || gates != 1 || filepath.Dir(setup.Args[1]) != scratch {
			t.Errorf("the file of ours holds status line %q and %d gates", status, gates)
		}
		if data, _ := os.ReadFile(file); string(data) != had {
			t.Errorf("the person's file was changed: %s", data)
		}
	}
}

func TestEnsureRefusesWhatItCannotRead(t *testing.T) {
	l := newLogin(t)
	file := localFile(l.root)
	for _, bad := range []string{`{"hooks": `, `{"hooks": []}`, `{"statusLine": "mine"}`} {
		write(t, file, bad)
		if _, err := l.k.AgentSettings.Ensure(claude, l.root, t.TempDir(), true); err == nil {
			t.Errorf("%s: no error", bad)
		}
		if err := l.k.AgentSettings.Remove(claude, l.root); err == nil {
			t.Errorf("%s: removed with no error", bad)
		}
		if data, _ := os.ReadFile(file); string(data) != bad {
			t.Errorf("%s was changed to %s", bad, data)
		}
	}
}

func TestOtherKindsGetNothing(t *testing.T) {
	l := newLogin(t)
	setup, err := l.k.AgentSettings.Ensure("codex", l.root, t.TempDir(), true)
	if err != nil || !reflect.DeepEqual(setup, contract.AgentSetup{}) {
		t.Errorf("%+v, %v", setup, err)
	}
	if err := l.k.AgentSettings.Remove("codex", l.root); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(l.root, ".claude")); err == nil {
		t.Error("a folder was made for a kind with no settings of ours")
	}
}

// selfAt is the platform with the program somewhere else.
type selfAt struct {
	contract.Platform
	path string
}

func (s selfAt) SelfPath() (string, error) { return s.path, nil }

// One real, small Claude Code agent is asked for a step with the pause mark
// set: the step must not run. It spends a little of the login's own usage,
// so it runs only when asked for:
//
//	WHALESHARK_REAL_AGENT=1 go test -count=1 -v -run RealAgent ./internal/hook/
func TestRealAgentIsStoppedByTheGate(t *testing.T) {
	if os.Getenv("WHALESHARK_REAL_AGENT") == "" {
		t.Skip("set WHALESHARK_REAL_AGENT=1 to start one small Claude Code agent")
	}
	env := []string{contract.EnvTermPane + "=p9", contract.EnvAttempt + "=T1.1"}
	for _, v := range os.Environ() {
		// The agent's own sign-in stays; nothing of the terminals or of an
		// agent this test may itself be running in is handed on.
		if !strings.HasPrefix(v, "HERDR_") && !strings.HasPrefix(v, "CLAUDE") && !strings.HasPrefix(v, "WHALESHARK_") {
			env = append(env, v)
		}
	}
	bin := filepath.Join(t.TempDir(), "whaleshark")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/whaleshark").CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	l := newLogin(t)
	env = append(env, contract.EnvRoot+"="+l.root, "XDG_STATE_HOME="+filepath.Dir(l.state))
	l.k.Platform = selfAt{l.k.Platform, bin}
	if _, err := l.k.AgentSettings.Ensure(claude, l.root, "", true); err != nil {
		t.Fatal(err)
	}
	ask := func(name string) (ran bool, said string) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "claude", "-p", "Use the Bash tool once to run exactly this command, then say what happened: echo ran > "+name,
			"--model", "haiku", "--setting-sources", "project,local", "--allowedTools", "Bash", "--output-format", "json")
		cmd.Dir, cmd.Env = l.root, env
		out, err := cmd.CombinedOutput()
		t.Logf("%s: %v\n%s", name, err, out)
		_, err = os.Stat(filepath.Join(l.root, name))
		return err == nil, string(out)
	}
	if ran, _ := ask("before.txt"); !ran {
		t.Fatal("with no mark the agent did not run its step: the trial shows nothing")
	}
	l.pause()
	ran, said := ask("paused.txt")
	if ran {
		t.Error("the step ran with the pause mark set")
	}
	// What the harness itself reports of the turn: the step refused, and
	// the turn ended by the hook and not by the model's choice.
	for _, want := range []string{`"permission_denials":[{"tool_name":"Bash"`, `"terminal_reason":"hook_stopped"`} {
		if !strings.Contains(said, want) {
			t.Errorf("the agent's own report lacks %s", want)
		}
	}
}
