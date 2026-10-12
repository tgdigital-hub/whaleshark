package hook

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const claude = contract.DefaultTool

// describe wires a tool in by a file in the login's own settings folder.
func (l *login) describe(name, hooks string) {
	l.t.Helper()
	dirs, err := l.k.Platform.Dirs()
	if err != nil {
		l.t.Fatal(err)
	}
	file := filepath.Join(dirs.Config, contract.ToolsDir, name+".toml")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		l.t.Fatal(err)
	}
	program, _ := filepath.Abs(filepath.Join(l.home, "bin", name))
	text := "run = ['" + filepath.ToSlash(program) + "']\nhooks = '" + hooks + "'\n"
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		l.t.Fatal(err)
	}
}

// Which hooks a tool gets is its description's word and nothing else: one
// that reads Claude Code's form gets that file, the status line and a gate
// that holds; one that reads Codex's gets that file and is not held; one
// that reads none gets nothing. Nothing here knows a tool by its name.
func TestAToolGetsTheHooksItsDescriptionNames(t *testing.T) {
	l := newLogin(t)
	l.describe("local", contract.HooksClaude)
	l.describe("other", contract.HooksCodex)
	l.describe("plain", contract.HooksNone)
	read := func(file string) string {
		data, _ := os.ReadFile(file)
		return string(data)
	}

	setup, err := l.k.AgentSettings.Ensure("local", l.root, "", true)
	if text := read(setup.File); err != nil || !setup.Gated || setup.File != localFile(l.root) ||
		!strings.Contains(text, " hook gate\"") || !strings.Contains(text, " hook statusline\"") || !strings.Contains(text, " hook state Stop\"") {
		t.Errorf("a tool of Claude Code's form: %+v, %v\n%s", setup, err, text)
	}
	// In a folder that is not its alone it is handed the same beside it.
	scratch := t.TempDir()
	if setup, err := l.k.AgentSettings.Ensure("local", t.TempDir(), scratch, false); err != nil || !setup.Gated ||
		!reflect.DeepEqual(setup.Args, []string{"--settings", filepath.Join(scratch, "claude-settings.json")}) {
		t.Errorf("beside a folder: %+v, %v", setup, err)
	}

	setup, err = l.k.AgentSettings.Ensure("other", l.root, "", true)
	if text := read(setup.File); err != nil || setup.Gated || setup.File != filepath.Join(l.root, ".codex", "hooks.json") ||
		!strings.Contains(text, " hook gate codex\"") || strings.Contains(text, "statusline") {
		t.Errorf("a tool of Codex's form: %+v, %v\n%s", setup, err, text)
	}

	for _, name := range []string{"plain", "nobody"} {
		if setup, err := l.k.AgentSettings.Ensure(name, l.root, t.TempDir(), true); err != nil || !reflect.DeepEqual(setup, contract.AgentSetup{}) {
			t.Errorf("%s: %+v, %v", name, setup, err)
		}
	}

	// What was written goes again by the description, and only ours.
	for _, name := range []string{"local", "other", "plain", "nobody"} {
		if err := l.k.AgentSettings.Remove(name, l.root); err != nil {
			t.Errorf("remove %s: %v", name, err)
		}
	}
	for _, file := range []string{localFile(l.root), filepath.Join(l.root, ".codex", "hooks.json")} {
		if text := read(file); strings.Contains(text, " hook ") {
			t.Errorf("%s still holds ours:\n%s", file, text)
		}
	}
}
