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
