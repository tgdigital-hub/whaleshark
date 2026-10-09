package herdr

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// platform is the two calls the adapter makes on the operating system.
type platform struct{ contract.NoPlatform }

func (platform) Replace(tmp, final string) error { return os.Rename(tmp, final) }
func (platform) Quote(argv []string) string      { return "'" + strings.Join(argv, "' '") + "'" }

// stub is an adapter whose herdr is a function; it keeps every command line.
func stub(t *testing.T, answer func(env, args []string) (string, string, int)) (*Adapter, *[]string) {
	k := contract.NewKit()
	k.Platform = platform{}
	a, calls := New(k), new([]string)
	a.Settings = filepath.Join(t.TempDir(), "config.toml")
	a.Call = func(_ context.Context, env, args []string) ([]byte, []byte, int, error) {
		*calls = append(*calls, strings.Join(args, " "))
		if answer == nil {
			return []byte(`{"id":"x","result":{}}`), nil, 0, nil
		}
		out, errOut, exit := answer(env, args)
		return []byte(out), []byte(errOut), exit, nil
	}
	return a, calls
}

func TestCallsAreTheExactCommandLines(t *testing.T) {
	a, calls := stub(t, nil)
	a.TabCreate("/work/shop", "url parser", []string{"WHALESHARK_RUN=r3", "WHALESHARK_TASK=T3"})
	a.AgentStart("h3fa1-r3-t3-1", "claude", "w1:p2", []string{"--model", "small"}, 180*time.Second)
	a.Prompt("h3fa1-r3-t3-1", "Read and follow /work/prompt.md", 30*time.Second)
	a.Point("w1:p2", contract.PointAnswered, "q7")
	a.Point("w1:p2", contract.PointMail, "")
	a.Run("w1:p3", []string{"exec", "whaleshark", "ui", "run", "fleet"})
	a.Screen("w1:p3")
	a.Split("w1:p1", "right", 0.7)
	a.Swap("w1:p1", "w1:p4")
	a.Resize("w1:p3", "left", 0.02)
	a.PaneClose("w1:p3")
	a.TabRename("w1:t2", "--focus")
	a.TabFocus("w1:t2")
	a.TabClose("w1:t2")
	a.Notify("sign-up page — needs you", "must the old link keep working?", true)
	a.Notify("quiet", "", false)
	a.Snapshot(context.Background())
	a.Version()
	want := []string{
		"tab create --cwd /work/shop --label url parser --no-focus --env WHALESHARK_RUN=r3 --env WHALESHARK_TASK=T3",
		"agent start h3fa1-r3-t3-1 --kind claude --pane w1:p2 --timeout 180000 -- --model small",
		"agent prompt h3fa1-r3-t3-1 Read and follow /work/prompt.md --wait --until working --timeout 30000",
		"agent prompt w1:p2 Your question q7 was answered. Run: whaleshark ask --resume q7",
		"agent prompt w1:p2 whaleshark: a message is waiting. Run: whaleshark mail",
		"pane run w1:p3 'exec' 'whaleshark' 'ui' 'run' 'fleet'",
		"pane read w1:p3 --source visible",
		"pane split w1:p1 --direction right --ratio 0.7 --no-focus",
		"pane swap --source-pane w1:p1 --target-pane w1:p4",
		"pane resize --pane w1:p3 --direction left --amount 0.02",
		"pane close w1:p3",
		"tab rename w1:t2 --focus",
		"tab focus w1:t2",
		"tab close w1:t2",
		"notification show sign-up page — needs you --body must the old link keep working? --sound request",
		"notification show quiet --body  --sound none",
		"api snapshot",
		"status --json",
	}
	if !slices.Equal(*calls, want) {
		t.Errorf("the command lines were\n%s\nwant\n%s", strings.Join(*calls, "\n"), strings.Join(want, "\n"))
	}
	for _, line := range *calls {
		if strings.Contains(line, "send-") {
			t.Errorf("a call that sends keys or text: %s", line)
		}
	}
}

func TestPointTakesOnlyTheFixedPointers(t *testing.T) {
	a, calls := stub(t, nil)
	if a.Point("w1:p2", "rm -rf /", "") == nil || a.Point("w1:p2", contract.PointAnswered, "q7; rm") == nil {
		t.Error("a pointer that is not on the list, or an argument that is not an id, was typed")
	}
	if len(*calls) != 0 {
		t.Errorf("herdr was called: %v", *calls)
	}
}

func TestErrorsAreMapped(t *testing.T) {
	for _, c := range []struct {
		name, stderr string
		exit         int
		code         string
	}{
		{"stopped at a prompt while starting", `{"error":{"code":"agent_not_ready","message":"agent a is blocked during startup"},"id":"cli:agent:start"}`, 1, "agent_not_ready"},
		{"a pane that is gone", `{"error":{"code":"pane_not_found","message":"pane w1:p99 not found"},"id":"cli:pane:close"}`, 1, "pane_not_found"},
		{"no server", `{"id":"cli:api:snapshot","error":{"code":"server_not_running","message":"no herdr server is running"}}`, 1, Unreachable},
		{"a command line herdr does not know", "invalid split direction: sideways\n", 2, Failed},
		{"no herdr answer at all", "", 1, Failed},
	} {
		a, _ := stub(t, func(_, _ []string) (string, string, int) { return "", c.stderr, c.exit })
		err := a.AgentStart("a", "claude", "w1:p1", nil, time.Minute)
		if Code(err) != c.code {
			t.Errorf("%s: code %q, want %q (%v)", c.name, Code(err), c.code, err)
		}
		if errors.Is(err, contract.ErrAgentNotReady) != (c.code == "agent_not_ready") {
			t.Errorf("%s: ErrAgentNotReady is wrong for %v", c.name, err)
		}
	}
	a, _ := stub(t, nil)
	a.Call = func(context.Context, []string, []string) ([]byte, []byte, int, error) {
		return nil, nil, 0, errors.New("herdr: executable file not found")
	}
	if _, err := a.Snapshot(context.Background()); Code(err) != Unreachable {
		t.Errorf("a herdr that cannot be started: %v", err)
	}
}

// The answer of herdr 0.9.1 to "api snapshot", shortened to two panes.
const realSnapshot = `{"id":"cli:api:snapshot","result":{"snapshot":{"agents":[{"agent":"claude","agent_session":{"agent":"claude","kind":"id","source":"herdr:claude","value":"s-1"},"agent_status":"done","cwd":"/work/shop","focused":true,"name":"lead","pane_id":"w1:p1","revision":4,"tab_id":"w1:t1","terminal_id":"term_a","workspace_id":"w1"}],"focused_pane_id":"w1:p1","layouts":[],"panes":[{"agent":"claude","agent_session":{"agent":"claude","kind":"id","source":"herdr:claude","value":"s-1"},"agent_status":"done","cwd":"/work/shop","focused":true,"pane_id":"w1:p1","revision":4,"tab_id":"w1:t1","terminal_id":"term_a","workspace_id":"w1"},{"agent_status":"unknown","cwd":"/work/shop","focused":false,"pane_id":"w1:p2","revision":0,"tab_id":"w1:t2","terminal_id":"term_b","workspace_id":"w1"}],"protocol":22,"tabs":[{"label":"Lead","tab_id":"w1:t1","workspace_id":"w1"},{"label":"url parser","tab_id":"w1:t2","workspace_id":"w1"}],"version":"0.9.1","workspaces":[]}}}`

var realPanes = []contract.Pane{
	{ID: "w1:p1", Tab: "w1:t1", Workspace: "w1", Terminal: "term_a", Label: "Lead", Cwd: "/work/shop", Focused: true,
		Agent: "claude", Name: "lead", Session: "s-1", Status: "done"},
	{ID: "w1:p2", Tab: "w1:t2", Workspace: "w1", Terminal: "term_b", Label: "url parser", Cwd: "/work/shop", Status: "unknown"},
}

func TestSnapshotJoinsPanesTabsAndAgents(t *testing.T) {
	a, _ := stub(t, func(_, _ []string) (string, string, int) { return realSnapshot, "", 0 })
	s, err := a.Snapshot(context.Background())
	if err != nil || !slices.Equal(s.Panes, realPanes) {
		t.Errorf("got %+v, %v", s, err)
	}
}

// The event lines below are herdr 0.9.1's own, with the ids changed.
func TestEventsSplitLinesAndEndWithTheConnection(t *testing.T) {
	socket := filepath.Join(shortDir(t), "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	a, _ := stub(t, func(_, args []string) (string, string, int) {
		if args[0] == "status" {
			return `{"client":{"version":"0.9.1"},"server":{"running":true,"version":"0.9.1","socket":"` + socket + `"}}`, "", 0
		}
		return realSnapshot, "", 0
	})
	request := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		request <- string(buf[:n])
		// The answer and two events in one write, then an event in two halves, then the end.
		conn.Write([]byte(`{"id":"whaleshark","result":{"type":"subscription_started"}}` + "\n" +
			`{"data":{"agent":"claude","agent_status":"working","pane_id":"w1:p1","workspace_id":"w1"},"event":"pane.agent_status_changed"}` + "\n" +
			`{"data":{"pane_id":"w1:p2","type":"pane_focused","workspace_id":"w1"},"event":"pane_focused"}` + "\n" +
			`{"data":{"label":"new na`))
		time.Sleep(20 * time.Millisecond)
		conn.Write([]byte(`me","tab_id":"w1:t2","type":"tab_renamed","workspace_id":"w1"},"event":"tab_renamed"}` + "\n" +
			`{"data":{"agent":"claude","final_status":"working","pane_id":"w1:p1","released":true,"type":"pane_agent_detected","workspace_id":"w1"},"event":"pane_agent_detected"}` + "\n" +
			`{"data":{"tab_id":"w1:t1","type":"tab_closed","workspace_id":"w1"},"event":"tab_closed"}` + "\n"))
		conn.Close()
	}()
	pic, events, err := a.Events(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(pic.Panes, realPanes) {
		t.Errorf("the first snapshot: %+v", pic.Panes)
	}
	var kinds []string
	for e := range events {
		kinds = append(kinds, e.Kind)
		Apply(pic, e)
	}
	if want := []string{Status, Focused, TabNamed, Released, TabClosed}; !slices.Equal(kinds, want) {
		t.Errorf("events %v, want %v", kinds, want)
	}
	want := []contract.Pane{{ID: "w1:p2", Tab: "w1:t2", Workspace: "w1", Terminal: "term_b", Label: "new name",
		Cwd: "/work/shop", Focused: true, Status: "unknown"}}
	if !slices.Equal(pic.Panes, want) {
		t.Errorf("the picture after the events: %+v", pic.Panes)
	}
	asked := <-request
	if !strings.Contains(asked, `{"type":"pane.agent_status_changed","pane_id":"w1:p1"}`) ||
		!strings.Contains(asked, `{"type":"pane.updated"}`) || strings.Contains(asked, `{"type":"pane.agent_status_changed"}`) {
		t.Errorf("the subscription request: %s", asked)
	}
}

// shortDir is a folder whose path is short enough for a socket file.
func shortDir(t *testing.T) string {
	dir, err := os.MkdirTemp("", "h")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestNotifyReadsThePopUpSettingBesideTheReason(t *testing.T) {
	a, _ := stub(t, func(_, _ []string) (string, string, int) {
		return `{"id":"cli:notification:show","result":{"reason":"shown","shown":true,"type":"notification_show"}}`, "", 0
	})
	for _, c := range []struct{ file, delivery string }{
		{"", "off"},
		{"[ui]\nsidebar_width = 26\n", "off"},
		{"[ui.toast]\ndelivery = \"herdr\"\n", "herdr"},
	} {
		os.Remove(a.Settings)
		if c.file != "" {
			os.WriteFile(a.Settings, []byte(c.file), 0o600)
		}
		reason, delivery, err := a.Notify("a title", "a body", true)
		if err != nil || reason != "shown" || delivery != c.delivery {
			t.Errorf("settings %q: reason %q, delivery %q, %v", c.file, reason, delivery, err)
		}
	}
}

var seven = []contract.KeyEntry{
	{Key: "prefix+space", Type: "popup", Argv: []string{"/opt/a b/whaleshark", "ui", "run", "list"}},
	{Key: "prefix+a", Type: "shell", Argv: []string{"whaleshark", "ui", "focus", "actions"}},
	{Key: "prefix+shift+a", Type: "shell", Argv: []string{"whaleshark", "pause", "--toggle"}},
	{Key: "prefix+f", Type: "shell", Argv: []string{"whaleshark", "ui", "fleet", "toggle"}},
	{Key: "prefix+u", Type: "popup", Argv: []string{"whaleshark", "catchup"}},
	{Key: "prefix+m", Type: "shell", Argv: []string{"whaleshark", "set", "mute", "toggle"}},
	{Key: "prefix+shift+m", Type: "shell", Argv: []string{"whaleshark", "set", "dnd", "toggle"}},
}

func TestKeysWrittenAndRemovedLeaveTheFileByteForByte(t *testing.T) {
	for name, before := range map[string]string{
		"empty":           "",
		"one line":        "onboarding = false\n",
		"no last newline": "onboarding = false\n\n[ui.toast]\ndelivery = \"herdr\"",
		"windows lines":   "onboarding = false\r\n[theme]\r\nname = \"nord\"\r\n",
		"own shortcuts":   "# mine\n[keys]\nprefix = \"ctrl+b\"\n\n[[keys.command]]\nkey = \"prefix+alt+g\"\ntype = \"popup\"\ncommand = \"lazygit\"\n\n\n",
		"looks like ours": "# whaleshark shortcuts are below\n[ui]\nsidebar_width = 30\n",
	} {
		var env []string
		a, calls := stub(t, func(e, _ []string) (string, string, int) { env = e; return "config: ok\n", "", 0 })
		os.WriteFile(a.Settings, []byte(before), 0o640)
		if err := a.SetKeys(seven[:2]); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := a.SetKeys(seven); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var file struct {
			Keys struct {
				Command []struct{ Key, Type, Command string }
			}
		}
		data, _ := os.ReadFile(a.Settings)
		if _, err := toml.Decode(string(data), &file); err != nil {
			t.Fatalf("%s: the file with the entries is not TOML: %v\n%s", name, err, data)
		}
		ours := file.Keys.Command[len(file.Keys.Command)-7:]
		if strings.Count(string(data), keysBegin) != 1 || ours[0].Key != "prefix+space" || ours[0].Type != "popup" ||
			ours[0].Command != "'/opt/a b/whaleshark' 'ui' 'run' 'list'" || ours[6].Key != "prefix+shift+m" {
			t.Errorf("%s: the entries are not there once and whole:\n%s", name, data)
		}
		if !strings.HasPrefix(string(data), before) {
			t.Errorf("%s: what was in the file was changed", name)
		}
		if info, _ := os.Stat(a.Settings); info.Mode().Perm() != 0o640 {
			t.Errorf("%s: the file's permissions became %v", name, info.Mode().Perm())
		}
		if len(env) != 1 || !strings.HasPrefix(env[0], "HERDR_CONFIG_PATH=") || env[0] == "HERDR_CONFIG_PATH="+a.Settings {
			t.Errorf("%s: herdr's check was not given the new file before it replaced the old: %v", name, env)
		}
		if again := len(*calls); a.SetKeys(seven) != nil || len(*calls) != again {
			t.Errorf("%s: writing the same entries twice touched something", name)
		}
		if err := a.SetKeys(nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if after, _ := os.ReadFile(a.Settings); string(after) != before {
			t.Errorf("%s: after removing, the file is\n%q\nwant\n%q", name, after, before)
		}
		if strings.Contains(strings.Join(*calls, "\n"), "reload-config") {
			t.Errorf("%s: herdr was told to reload for a copy of its settings", name)
		}
		if left, _ := filepath.Glob(filepath.Join(filepath.Dir(a.Settings), "*")); len(left) != 1 {
			t.Errorf("%s: files left behind: %v", name, left)
		}
	}
}

// herdrCheck answers "config check" as herdr does: it names a key that is
// bound twice, and an unknown key.
func herdrCheck(env, args []string) (string, string, int) {
	if args[0] != "config" {
		return `{"id":"x","result":{}}`, "", 0
	}
	data, _ := os.ReadFile(strings.TrimPrefix(env[0], "HERDR_CONFIG_PATH="))
	issues := ""
	if strings.Count(string(data), `key = "prefix+m"`) > 1 {
		issues += "prefix+m: kept keys.command[0].key, disabled keys.command[6].key\n"
	}
	if strings.Contains(string(data), "no_such_key") {
		issues += "unknown config key no_such_key; ignoring key\n"
	}
	if issues != "" {
		return "config: issues found\n" + issues, "", 1
	}
	return "config: ok\n", "", 0
}

func TestKeysRefusedByHerdrLeaveTheFileAlone(t *testing.T) {
	a, _ := stub(t, herdrCheck)
	mine := "[[keys.command]]\nkey = \"prefix+m\"\ntype = \"shell\"\ncommand = \"make\"\n"
	os.WriteFile(a.Settings, []byte(mine), 0o600)
	if err := a.SetKeys(seven); err == nil || !strings.Contains(err.Error(), "prefix+m: kept") {
		t.Errorf("a shortcut on a key the person has bound was written, or herdr's reason was lost: %v", err)
	}
	if data, _ := os.ReadFile(a.Settings); string(data) != mine {
		t.Errorf("the file was changed: %q", data)
	}

	// A fault the person's file had before does not stop the entries, nor their removal.
	os.WriteFile(a.Settings, []byte("no_such_key = 1\n"), 0o600)
	if err := a.SetKeys(seven); err != nil {
		t.Errorf("refused for a fault that was there before: %v", err)
	}
	if err := a.SetKeys(nil); err != nil {
		t.Error(err)
	}
	os.WriteFile(a.Settings, []byte("onboarding = false\n"), 0o600)
	if a.SetKeys([]contract.KeyEntry{{Key: "prefix+a\n[evil]", Type: "shell", Argv: []string{"x"}}}) == nil {
		t.Error("a key with a line break in it was written")
	}
	if data, _ := os.ReadFile(a.Settings); string(data) != "onboarding = false\n" {
		t.Errorf("the file was changed: %q", data)
	}
}

func TestKeysReloadTheRunningServerForTheRealFile(t *testing.T) {
	a, calls := stub(t, herdrCheck)
	t.Setenv("HERDR_CONFIG_PATH", a.Settings)
	a.Settings = ""
	if err := a.SetKeys(seven); err != nil {
		t.Fatal(err)
	}
	if want := []string{"config check", "server reload-config"}; !slices.Equal(*calls, want) {
		t.Errorf("calls %v, want %v", *calls, want)
	}
}
