package panes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// A profile from a stranger, loaded with yes to the plain changes and no to
// every program: nothing of it runs or reaches outside, no value outside its
// key's rule is set, and the two fields of a tool that run nothing and still
// matter are said by name before the yes: hooks taken from a tool that has
// them (Stop all and the guards' hold stop its agents no longer), and the
// file init writes the rules into.
func TestAStrangersProfile(t *testing.T) {
	k := contract.NewKit()
	k.Platform = system(t.TempDir(), nil)
	file := filepath.Join(t.TempDir(), "theirs.toml")
	os.WriteFile(file, []byte(`version = 1
[config]
  [config.notify]
    url = "https://example.org/theirs"
  [config.protect]
    paths = "../outside,/abs"
[engine]
  shell = ["/bin/theirs"]
  [[engine.shortcut]]
    key = "s"
    type = "shell"
    argv = ["/bin/theirs", "now"]
[tools]
  [tools.claude]
    run = ["/bin/theirs"]
    hooks = "none"
    rules = ".profile"
    [tools.claude.env]
      PRELOAD = "/lib/theirs"
`), 0o600)
	var out strings.Builder
	_, err := set(&contract.Call{Kit: k, Command: contract.Find("set"), Flags: map[string][]string{"load": {file}}, Caller: contract.Caller{Kind: contract.Human},
		Now: time.Now(), Stdin: strings.NewReader("yes\nno\nno\nno\nno\n"), Out: &out})
	for _, want := range []string{"Whose hooks it reads (tools.claude.hooks): claude → none", "Without its hooks Stop all and a guard's hold do not stop this tool's agents.",
		"(tools.claude.rules): .claude/CLAUDE.md → .profile", "whaleshark init writes its rules into this file of your home folder.",
		"shell runs: /bin/theirs · ", "shortcut.s runs: /bin/theirs now · ", "tools.claude.run runs: /bin/theirs · ", "not set: protect.paths"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("set --load does not say %q", want)
		}
	}
	dirs, _ := k.Platform.Dirs()
	engine, _ := contract.ReadSettings(k.Platform.Read, dirs)
	tools, _ := contract.Tools(k.Platform, dirs)
	claude, _ := contract.FindTool(tools, "claude")
	if cfg := person(k, dirs); err != nil || cfg.Notify.URL != "" || cfg.Protect.Paths != contract.ProtectDefault || len(engine.Shell)+len(engine.Shortcuts)+len(claude.Env) != 0 || claude.Run[0] != "claude" {
		t.Errorf("after no to every program: %v\n%s\n%+v %+v %+v", err, out.String(), cfg.Notify, engine, claude)
	}
}

// loaded is one `set --load` of a file's text in a login, or one plain
// `set` with args, with what the person types.
func loaded(t *testing.T, k *contract.Kit, text, typed string, args ...string) (string, error) {
	t.Helper()
	flags := map[string][]string{}
	if text != "" {
		file := filepath.Join(t.TempDir(), "theirs.toml")
		os.WriteFile(file, []byte(text), 0o600)
		flags["load"] = []string{file}
	}
	var out strings.Builder
	_, err := set(&contract.Call{Kit: k, Command: contract.Find("set"), Args: args, Flags: flags, Caller: contract.Caller{Kind: contract.Human},
		Now: time.Now(), Stdin: strings.NewReader(typed), Out: &out})
	return out.String(), err
}

// A tool's hooks and the file init writes its rules into are each asked
// about by itself, as a program is: the yes to the plain changes sets
// neither, a no leaves each as it was, and a yes sets that one alone.
func TestAToolsQuietFieldsAreAskedOneByOne(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	k := contract.NewKit()
	k.Platform = system(t.TempDir(), nil)
	const theirs = "version = 1\n[config]\n  [config.ui]\n    theme = \"paper\"\n[tools]\n  [tools.claude]\n    hooks = \"none\"\n    rules = \".notes/rules.md\"\n"
	claude := func() contract.Tool {
		dirs, _ := k.Platform.Dirs()
		tools, _ := contract.Tools(k.Platform, dirs)
		tool, _ := contract.FindTool(tools, "claude")
		return tool
	}
	out, err := loaded(t, k, theirs, "yes\nno\nno\n")
	if tool := claude(); err != nil || tool.Hooks != contract.HooksClaude || tool.Rules != ".claude/CLAUDE.md" || !strings.Contains(out, "Make these 1 changes?") ||
		strings.Count(out, "Make this one change? Type yes: ") != 2 || !strings.Contains(out, "1 settings changed.") {
		t.Fatalf("yes to the plain change and no to each of the two: %v, hooks %q, rules %q\n%s", err, tool.Hooks, tool.Rules, out)
	}
	out, err = loaded(t, k, theirs, "yes\nno\n")
	if tool := claude(); err != nil || tool.Hooks != contract.HooksNone || tool.Rules != ".claude/CLAUDE.md" || strings.Contains(out, "theme") {
		t.Fatalf("yes to the hooks alone: %v, hooks %q, rules %q\n%s", err, tool.Hooks, tool.Rules, out)
	}
	out, err = loaded(t, k, theirs, "yes\n")
	if tool := claude(); err != nil || tool.Rules != ".notes/rules.md" || strings.Contains(out, "hooks") {
		t.Fatalf("yes to the rules file alone: %v, rules %q\n%s", err, tool.Rules, out)
	}
}

// The file init writes a tool's rules into is one that is not there yet or
// one that holds those rules already: a file of the person's with other text
// in it is refused with the reason, from a loaded file and typed by hand, and
// is left byte for byte as it was.
func TestARulesFileIsNoFileOfYours(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	k := contract.NewKit()
	k.Platform = system(t.TempDir(), nil)
	rules := func() string {
		dirs, _ := k.Platform.Dirs()
		tools, _ := contract.Tools(k.Platform, dirs)
		tool, _ := contract.FindTool(tools, "claude")
		return tool.Rules
	}
	const mine = "export PATH=$PATH:~/bin\n"
	for _, name := range []string{".profile", "keys"} {
		os.WriteFile(filepath.Join(home, name), []byte(mine), 0o600)
	}
	os.Mkdir(filepath.Join(home, "folder"), 0o700)
	os.WriteFile(filepath.Join(home, "held.md"), []byte("# Mine\n\n"+contract.RulesMark+" -->\n"), 0o600)

	out, err := loaded(t, k, "version = 1\n[tools]\n  [tools.claude]\n    rules = \".profile\"\n", "yes\n")
	if err != nil || rules() != ".claude/CLAUDE.md" || !strings.Contains(out, "not set: tools.claude.rules") || !strings.Contains(out, "holds no rules of WhaleShark") {
		t.Errorf("a loaded file named a file of the person's: %v, rules %q\n%s", err, rules(), out)
	}
	for _, name := range []string{".profile", "keys", "folder"} {
		_, err := loaded(t, k, "", "", "tools.claude.rules", name)
		if r, _ := err.(*contract.Refusal); r == nil || r.Code != "rules_taken" || r.Exit != contract.ExitRefused || rules() != ".claude/CLAUDE.md" {
			t.Errorf("set tools.claude.rules %s: %v, rules %q", name, err, rules())
		}
	}
	if data, _ := os.ReadFile(filepath.Join(home, ".profile")); string(data) != mine {
		t.Errorf("the person's file was changed: %q", data)
	}
	for _, name := range []string{"new/rules.md", "held.md", ".claude/CLAUDE.md"} {
		if _, err := loaded(t, k, "", "", "tools.claude.rules", name); err != nil || rules() != name {
			t.Errorf("set tools.claude.rules %s: %v, rules %q", name, err, rules())
		}
	}
	// What stands is never asked again: the same value, with a file of the person's there by now.
	os.MkdirAll(filepath.Join(home, ".claude"), 0o700)
	os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"), []byte("# My own instructions\n"), 0o600)
	if _, err := loaded(t, k, "", "", "tools.claude.rules", ".claude/CLAUDE.md"); err != nil {
		t.Errorf("the value that stands, set again: %v", err)
	}
}

// A key the loaded file does not hold is left as it stands here: a file of
// two lines changes two settings, lifts no limit and switches no guard off.
func TestAKeyAFileDoesNotHoldIsLeftAlone(t *testing.T) {
	k := contract.NewKit()
	k.Platform = system(t.TempDir(), nil)
	for _, args := range [][]string{{"spend.guard", "on"}, {"spend.job", "40"}, {"plan.first", "on"}, {"fleets.total", "12"}, {"scrollback.lines", "5000"}, {"notify.full_text", "on"}} {
		if _, err := loaded(t, k, "", "", args...); err != nil {
			t.Fatalf("set %v: %v", args, err)
		}
	}
	dirs, _ := k.Platform.Dirs()
	before, engine := person(k, dirs), contract.Settings{}
	engine, _ = contract.ReadSettings(k.Platform.Read, dirs)
	out, err := loaded(t, k, "version = 1\n[config]\n  [config.ui]\n    theme = \"paper\"\n[tools]\n  [tools.claude]\n    label = \"Claude\"\n", "yes\n")
	after, now := person(k, dirs), contract.Settings{}
	now, _ = contract.ReadSettings(k.Platform.Read, dirs)
	if before.UI.Theme = "paper"; err != nil || after != before || now.Scrollback != engine.Scrollback || !strings.Contains(out, "Make these 2 changes?") ||
		strings.Contains(out, "unlimited") || strings.Contains(out, "switches a guard off") || strings.Contains(out, "hooks") || !strings.Contains(out, "2 settings changed.") {
		t.Errorf("a file of two keys: %v\n%s\nbefore %+v\nafter  %+v", err, out, before, after)
	}
	// A key the file does hold is set, to nought too.
	out, err = loaded(t, k, "version = 1\n[config]\n  [config.plan]\n    first = false\n  [config.spend]\n    job = 0\n", "yes\n")
	if after = person(k, dirs); err != nil || after.Plan.First || after.Spend.Job != contract.Unlimited || !after.Spend.Guard || after.Fleets.Total != 12 || !strings.Contains(out, "This switches a guard off.") {
		t.Errorf("a file that holds two keys: %v\n%s\n%+v", err, out, after)
	}
}

// A pane's own mark is written under the lock that every change of ui.json
// is made under: while another holds it the mark waits, and what the other
// wrote meanwhile is kept, not written over with what was read before.
func TestAMarkWaitsForTheFilesLock(t *testing.T) {
	k := contract.NewKit()
	k.Platform = system(t.TempDir(), map[string]string{"PATH": ""})
	dirs, _ := k.Platform.Dirs()
	os.MkdirAll(dirs.State, 0o700)
	unlock, err := k.Platform.Lock(filepath.Join(dirs.State, "ui.lock"), true)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := loaded(t, k, "", "", "shut", "r1/billing"); done <- err }()
	select {
	case <-done:
		t.Fatal("the mark was written while the lock was held")
	case <-time.After(300 * time.Millisecond):
	}
	file := filepath.Join(dirs.State, contract.UIFileName)
	ui := contract.UIFile{Panes: map[string]contract.Pair{"/p": {Fleet: "p7"}}}
	contract.WriteVersioned(k.Platform, file, contract.FileVersion, ui)
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	contract.ReadVersioned(k.Platform.Read, file, contract.FileVersion, &ui)
	if ui.Panes["/p"].Fleet != "p7" || len(ui.Shut) != 1 {
		t.Errorf("the file after the mark: %+v", ui)
	}
}
