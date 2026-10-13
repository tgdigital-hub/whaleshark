package contract

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestProjects(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if roots, err := ReadProjects(os.ReadFile, dir); err != nil || roots != nil {
		t.Fatalf("no file yet: %v, %v", roots, err)
	}
	want := []string{"/work/shop", "/work/api"}
	if err := WriteProjects(plain{}, dir, want); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadProjects(os.ReadFile, dir); err != nil || !slices.Equal(got, want) {
		t.Fatalf("read back %v, %v", got, err)
	}
	// Private as this system shows it: Windows keeps no such bits, and a
	// file there is private by the folder of the login it lies in.
	path, probe := filepath.Join(dir, "projects.json"), filepath.Join(t.TempDir(), "probe")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	private, _ := os.Stat(probe)
	if info, _ := os.Stat(path); info.Mode().Perm() != private.Mode().Perm() {
		t.Errorf("projects.json is %v, want private", info.Mode().Perm())
	}
	if left, _ := filepath.Glob(path + ".*"); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}

	newer := []byte(`{"version": 99, "roots": ["/other"], "later": true}`)
	if err := os.WriteFile(path, newer, 0o600); err != nil {
		t.Fatal(err)
	}
	if roots, err := ReadProjects(os.ReadFile, dir); !errors.Is(err, ErrNewer) || roots != nil {
		t.Errorf("a newer file must read as absent: %v, %v", roots, err)
	}
	if err := WriteProjects(plain{}, dir, want); !errors.Is(err, ErrNewer) {
		t.Errorf("a newer file must not be written: %v", err)
	}
	if kept, _ := os.ReadFile(path); string(kept) != string(newer) {
		t.Errorf("the newer file was changed")
	}
}

func TestPauseMark(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if p, err := Paused(os.ReadFile, dir); p != nil || err != nil {
		t.Fatalf("no mark yet: %v, %v", p, err)
	}
	at := time.Date(2026, 10, 8, 21, 14, 0, 0, time.UTC)
	if err := SetPaused(plain{}, dir, &Pause{At: at, By: WherePane}); err != nil {
		t.Fatal(err)
	}
	if p, err := Paused(os.ReadFile, dir); err != nil || p == nil || !p.At.Equal(at) || p.By != WherePane {
		t.Fatalf("read back %+v, %v", p, err)
	}
	for range 2 {
		if err := SetPaused(plain{}, dir, nil); err != nil {
			t.Fatal(err)
		}
	}
	if p, _ := Paused(os.ReadFile, dir); p != nil {
		t.Errorf("the mark is still there")
	}
}

func TestClockAndProjectFile(t *testing.T) {
	clock := filepath.Join(t.TempDir(), "clock")
	os.WriteFile(clock, []byte("2026-10-08T21:14:00Z\n"), 0o600)
	t.Setenv(EnvClock, clock)
	if got := Now(); got.Format(time.TimeOnly) != "21:14:00" {
		t.Errorf("the injected clock says %v", got)
	}
	// Read while the file is empty, as a read on Windows can find it when a
	// test moves the clock at that moment: the time that comes is the file's,
	// not the wall clock's. Seen on a hosted Windows machine: a blocked ask
	// took the wall clock once, and its nine minutes were up.
	os.WriteFile(clock, nil, 0o600)
	time.AfterFunc(100*time.Millisecond, func() { os.WriteFile(clock, []byte("2026-10-08T21:20:00Z\n"), 0o600) })
	if got := Now(); got.Format(time.TimeOnly) != "21:20:00" {
		t.Errorf("read while it was being replaced, the injected clock says %v", got)
	}
	// A clock that is named and was never written is the wall clock.
	t.Setenv(EnvClock, filepath.Join(t.TempDir(), "none"))
	if at := time.Now(); Now().Before(at) {
		t.Errorf("with no clock file the time is %v", Now())
	}
	t.Setenv(EnvClock, clock)

	root := t.TempDir()
	if p, err := ReadProjectFile(root); err != nil || p.Limits.Agents != 20 || p.Land.Who != ForHuman {
		t.Fatalf("defaults: %+v, %v", p, err)
	}
	os.WriteFile(filepath.Join(root, "whaleshark.toml"), []byte("[limits]\nagents = 6\n[agent]\nargs = [\"--x\"]\n"), 0o600)
	p, err := ReadProjectFile(root)
	if err != nil || p.Limits.Agents != 6 || p.Limits.StaleMinutes != 30 || p.Agent.Args != nil || !slices.Equal(p.Held, []string{"agent.args"}) {
		t.Errorf("before anybody approved it, read %+v, %v", p, err)
	}
	approvals(t)
	WriteTrust(plain{}, root, []string{"agent.args = [--x]"}, Now())
	if p, err = ReadProjectFile(root); err != nil || p.Held != nil || !slices.Equal(p.Agent.Args, []string{"--x"}) || p.Agent.Tool != DefaultTool {
		t.Errorf("once approved, read %+v, %v", p, err)
	}
	// The arguments are for one tool: another tool named for them is a line
	// of its own to approve, and until then they are nobody's.
	os.WriteFile(filepath.Join(root, "whaleshark.toml"), []byte("[agent]\nargs = [\"--x\"]\ntool = \"codex\"\n"), 0o600)
	lines, _ := TrustLines(root)
	if p, err = ReadProjectFile(root); err != nil || p.Agent.Args != nil || p.Agent.Tool != DefaultTool || !slices.Equal(lines, []string{"agent.args = [--x]", "agent.tool = codex"}) {
		t.Errorf("with another tool named for the arguments: %+v, %v, to approve %q", p.Agent, err, lines)
	}
	WriteTrust(plain{}, root, lines, Now())
	if p, err = ReadProjectFile(root); err != nil || p.Agent.Tool != "codex" || !slices.Equal(p.Agent.Args, []string{"--x"}) {
		t.Errorf("once that is approved: %+v, %v", p.Agent, err)
	}
	// More agents at once than usual is the person's to approve; fewer is not.
	os.WriteFile(filepath.Join(root, "whaleshark.toml"), []byte("[limits]\nagents = 500\nstale_minutes = 5\n"), 0o600)
	if p, err = ReadProjectFile(root); err != nil || p.Limits.Agents != 20 || p.Limits.StaleMinutes != 5 || !slices.Equal(p.Held, []string{"limits.agents"}) {
		t.Errorf("a limit above the usual one, before anybody approved it, read %+v, %v", p, err)
	}
	WriteTrust(plain{}, root, []string{"limits.agents = 500"}, Now())
	if p, err = ReadProjectFile(root); err != nil || p.Limits.Agents != 500 || p.Held != nil {
		t.Errorf("a limit above the usual one, once approved, read %+v, %v", p, err)
	}
}

// approvals gives the test a folder of a login's own to keep approvals in.
func approvals(t *testing.T) string {
	home, was := t.TempDir(), LoginDirs
	LoginDirs = func() (Dirs, error) { return Dirs{State: home}, nil }
	t.Cleanup(func() { LoginDirs = was })
	return home
}

// plain is a platform that replaces with the plain rename.
type plain struct{ NoPlatform }

func (plain) Replace(tmp, final string) error { return os.Rename(tmp, final) }
