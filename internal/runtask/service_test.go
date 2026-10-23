package runtask_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/internal/restore"
	"github.com/tgdigital-hub/whaleshark/internal/rules"
	"github.com/tgdigital-hub/whaleshark/internal/runtask"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// down is the engine as it is when no keeper runs.
type down struct{ contract.Terminals }

func (down) Version() (string, error) { return "", contract.ErrEngineUnreachable }

// standIns puts a systemctl and a loginctl of the test's own first on the
// path. Each writes its call to the log it returns; loginctl says the
// lingering the file linger holds.
func standIns(t *testing.T) (log, linger string) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-ins are shell scripts")
	}
	bin := t.TempDir()
	log, linger = filepath.Join(bin, "calls"), filepath.Join(bin, "linger")
	write(t, filepath.Join(bin, "systemctl"), "#!/bin/sh\necho \"systemctl $*\" >> "+log+"\n")
	write(t, filepath.Join(bin, "loginctl"), "#!/bin/sh\necho \"loginctl $*\" >> "+log+"\ncat "+linger+"\n")
	for _, name := range []string{"systemctl", "loginctl"} {
		if err := os.Chmod(filepath.Join(bin, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log, linger
}

// calls is what the stand-ins were asked since the last look.
func calls(t *testing.T, log string) string {
	data, _ := os.ReadFile(log)
	os.Remove(log)
	return string(data)
}

// On Linux --at-login makes the keeper one of the login's own services: the
// unit restore words, switched on, and started where no keeper runs; a
// keeper that runs is left alone. Lingering is said either way, and when it
// is off, what to ask the administrator. --remove --all switches the
// service off and takes its file out again.
func TestAtLoginOnLinuxIsAUserService(t *testing.T) {
	p := scenario.Prepare(t, nil, nil)
	rules.Plug(p.Kit)
	cli.Plug(p.Kit)
	runtask.Plug(p.Kit)
	p.Kit.Platform = platform.New("linux")
	file := contract.ServerFile
	t.Cleanup(func() { contract.ServerFile = file })
	contract.ServerFile = filepath.Join(t.TempDir(), "server.toml")
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "WHALESHARK_") && name != contract.EnvClock {
			t.Setenv(name, "")
		}
	}
	for _, kv := range p.Env(p.Own) {
		name, value, _ := strings.Cut(kv, "=")
		t.Setenv(name, value)
	}
	t.Setenv("USER", "me")
	t.Chdir(p.Root)
	log, linger := standIns(t)
	init := func(words ...string) string {
		t.Helper()
		var out strings.Builder
		if exit := p.Kit.Main(p.Kit, append([]string{"init", "--human"}, words...), strings.NewReader(""), &out, &out); exit != 0 {
			t.Fatalf("init %v: exit %d\n%s", words, exit, out.String())
		}
		return out.String()
	}
	dirs, _ := p.Kit.Platform.Dirs()
	unit := filepath.Join(filepath.Dir(dirs.Config), "systemd", "user", contract.UnitKeeper)
	self, _ := os.Executable()
	_, want := restore.Service("linux", self, os.Getenv)

	write(t, linger, "no\n")
	out := init("--adopt", "--at-login")
	if text, err := os.ReadFile(unit); err != nil || string(text) != want || !strings.Contains(want, `ExecStart="`+self+`" engine run`) {
		t.Errorf("the unit in %s: %v\n%s", unit, err, text)
	}
	if got := calls(t, log); !strings.Contains(got, "systemctl --user daemon-reload\nsystemctl --user enable "+contract.UnitKeeper+"\nloginctl show-user me --property=Linger --value\n") ||
		strings.Contains(got, " start ") {
		t.Errorf("with a keeper running, the calls:\n%s", got)
	}
	for _, words := range []string{"the one running keeps your tabs", "lingering is off for me", "as root: loginctl enable-linger me"} {
		if !strings.Contains(out, words) {
			t.Errorf("with lingering off, init does not say %q:\n%s", words, out)
		}
	}

	p.Kit.Terms = down{p.Kit.Terms}
	write(t, linger, "yes\n")
	out = init("--at-login")
	if got := calls(t, log); !strings.Contains(got, "systemctl --user start "+contract.UnitKeeper+"\n") {
		t.Errorf("with no keeper running, the calls:\n%s", got)
	}
	if !strings.Contains(out, "the engine runs as one of your services now") || !strings.Contains(out, "lingering is on for me") ||
		strings.Contains(out, "enable-linger") {
		t.Errorf("with lingering on:\n%s", out)
	}

	out = init("--remove", "--all")
	if got := calls(t, log); !strings.Contains(got, "systemctl --user disable "+contract.UnitKeeper+"\nsystemctl --user daemon-reload\n") {
		t.Errorf("at --remove --all, the calls:\n%s", got)
	}
	if _, err := os.Stat(unit); !os.IsNotExist(err) || !strings.Contains(out, "no longer one of your services") {
		t.Errorf("the unit after --remove --all: %v\n%s", err, out)
	}
}
