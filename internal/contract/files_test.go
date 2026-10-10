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

	root := t.TempDir()
	if p, err := ReadProjectFile(root); err != nil || p.Limits.Agents != 20 || p.Land.Who != ForHuman {
		t.Fatalf("defaults: %+v, %v", p, err)
	}
	os.WriteFile(filepath.Join(root, "whaleshark.toml"), []byte("[limits]\nagents = 6\n[agent]\nargs = [\"--x\"]\n"), 0o600)
	p, err := ReadProjectFile(root)
	if err != nil || p.Limits.Agents != 6 || p.Limits.StaleMinutes != 30 || !slices.Equal(p.Agent.Args, []string{"--x"}) {
		t.Errorf("read %+v, %v", p, err)
	}
}

// plain is a platform that replaces with the plain rename.
type plain struct{ NoPlatform }

func (plain) Replace(tmp, final string) error { return os.Rename(tmp, final) }
