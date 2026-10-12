package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
)

// claudeScreen is the words of Claude Code's screen as they are compiled in.
func claudeScreen() contract.ToolScreen {
	tool, _ := contract.FindTool(contract.BuiltIn(), "claude")
	return tool.Screen
}

// standIn wires a tool in the one way a person can: a file in the settings
// folder of a pretended login. Its program is this test program under
// another name, which the description's own variable turns into the fake
// agent of a mode. It returns the description as the one reader gives it,
// and what a pane's program needs besides to reach the stand.
func standIn(t *testing.T, s *stand, mode, screen string) (contract.Tool, []string) {
	t.Helper()
	home := t.TempDir()
	for _, name := range []string{"HOME", "USERPROFILE"} {
		t.Setenv(name, home)
	}
	for _, name := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(name, filepath.Join(home, name))
	}
	k := contract.NewKit()
	platform.Plug(k)
	d, err := k.Platform.Dirs()
	self, rerr := os.ReadFile(os.Args[0])
	if err != nil || rerr != nil {
		t.Fatal(err, rerr)
	}
	program := filepath.Join(t.TempDir(), "helper")
	file := filepath.Join(d.Config, contract.ToolsDir, "standin.toml")
	text := fmt.Sprintf("label = 'Stand-in'\nrun = [%q, %q]\nhooks = 'none'\n%s\n[env]\n%s = 'agent'\n", program, mode, screen, roleEnv)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string][]byte{program: self, file: []byte(text)} {
		if err := os.WriteFile(path, data, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	Plug(k)
	t.Cleanup(func() { wired = contract.BuiltIn })
	tool, known := contract.FindTool(wired(), "standin")
	if !known || tool.Hooks != contract.HooksNone || tool.Exact() {
		_, faults := contract.Tools(k.Platform, d)
		t.Fatalf("the stand-in is not wired in: %+v, %v", tool, faults)
	}
	env := s.env("")[1:]
	for name, value := range tool.Env {
		env = append(env, name+"="+value)
	}
	return tool, env
}

const standInWords = "[screen]\nready = '? for shortcuts'\nworking = 'esc to interrupt'\nquestion = ['Do you want to', 'Esc to cancel']"

// A tool nobody compiled in, described by a file with the words of its
// screen, is started from that description, prompted and seen to rest; and
// a question on its screen is one, where nothing is typed.
func TestAToolDescribedByAFile(t *testing.T) {
	s := listen(t)
	tool, env := standIn(t, s, "screen", standInWords)
	p := open(t, env, tool.Run...)
	w := s.watch(t, p, tool.Name)
	if err := w.Ready(wait); err != nil {
		t.Fatalf("ready: %v\n%s", err, p.Text())
	}
	if err := w.Prompt("say ready", wait); err != nil {
		t.Fatalf("the prompt: %v\n%s", err, p.Text())
	}
	if st := s.reach(t, idle, wait); st.Agent != "standin" {
		t.Errorf("the agent is called %q", st.Agent)
	}
	if got, want := s.statuses(), []string{idle, atWork, idle}; !slices.Equal(after(got, idle), want) {
		t.Errorf("the states were %v, want them to end %v", got, want)
	}
	if got := p.wasTyped(); !typedAs(got, "say ready") {
		t.Errorf("typed %q", got)
	}
	if err := w.Prompt("one step", wait); err != nil {
		t.Fatal(err)
	}
	s.reach(t, blocked, wait)
	if err := w.Prompt("more", time.Second); err != contract.ErrAgentBlocked {
		t.Errorf("a prompt at the stand-in's question: %v", err)
	}
}

// The same tool with no word of its screen in its description is busy or
// quiet and no more: started, prompted and seen to rest all the same.
func TestAToolDescribedByAFileWithoutWords(t *testing.T) {
	s := listen(t)
	tool, env := standIn(t, s, "other", "")
	p := open(t, env, tool.Run...)
	w := s.watch(t, p, tool.Name)
	if err := w.Ready(wait); err != nil {
		t.Fatalf("ready: %v\n%s", err, p.Text())
	}
	if err := w.Prompt("say ready", wait); err != nil {
		t.Fatalf("the prompt: %v\n%s", err, p.Text())
	}
	s.reach(t, Quiet, wait)
	if got, want := s.statuses(), []string{Busy, Quiet, Busy, Quiet}; !slices.Equal(after(got, Busy), want) {
		t.Errorf("the states were %v, want them to end %v", got, want)
	}
}

// Started by hand in a shell, a described tool is known by the name of its
// program and called by the name of its description.
func TestAToolOfAFileInAShell(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh here")
	}
	s := listen(t)
	tool, env := standIn(t, s, "screen", standInWords)
	p := open(t, env, "/bin/sh")
	s.watch(t, p, "")
	s.reach(t, idle, wait)
	p.press(tool.Run[0] + " screen\r")
	for end := time.Now().Add(wait); ; time.Sleep(10 * time.Millisecond) {
		if st := s.reach(t, idle, wait); st.Agent == "standin" {
			break
		} else if time.Now().After(end) {
			t.Fatalf("the tool in the shell was not seen: %+v\n%s", st, p.Text())
		}
	}
}
