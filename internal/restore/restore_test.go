package restore_test

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/layout"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/internal/restore"
	"github.com/tgdigital-hub/whaleshark/internal/screen"
)

var update = flag.Bool("update", false, "write the service entries as the approved ones")

// login is a pretended login in a folder of the test's own, as the
// variables a program of ours is started with.
func login(t *testing.T) (env []string, dirs contract.Dirs) {
	t.Helper()
	base := t.TempDir()
	env = os.Environ()
	for _, v := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "APPDATA", "LOCALAPPDATA"} {
		dir := filepath.Join(base, strings.ToLower(v))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		env = append(env, v+"="+dir)
	}
	p := platform.New(runtime.GOOS)
	p.Home, p.Env = filepath.Join(base, "home"), func(name string) string {
		for i := len(env) - 1; i >= 0; i-- {
			if v, ok := strings.CutPrefix(env[i], name+"="); ok {
				return v
			}
		}
		return ""
	}
	dirs, err := p.Dirs()
	if err != nil {
		t.Fatal(err)
	}
	return env, dirs
}

// run is one start of the stand-in keeper in a login.
type run struct {
	t     *testing.T
	cmd   *exec.Cmd
	stdin io.WriteCloser
	report
}

func startKeeper(t *testing.T, env []string, gone string) *run {
	t.Helper()
	self, _ := os.Executable()
	path := filepath.Join(t.TempDir(), "report.json")
	r := &run{t: t, cmd: exec.Command(self)}
	r.cmd.Env = append(slices.Clone(env), envRole+"=keeper", envReport+"="+path, envGone+"="+gone,
		envLost+"="+filepath.Join(gone, "agent-program"+filepath.Ext(self)))
	r.cmd.Stderr = os.Stderr
	r.stdin, _ = r.cmd.StdinPipe()
	if err := r.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.stop() })
	for end := time.Now().Add(60 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if data, err := os.ReadFile(path); err == nil {
			if err = json.Unmarshal(data, &r.report); err != nil {
				t.Fatal(err)
			}
			return r
		} else if time.Now().After(end) {
			t.Fatal("the stand-in keeper did not get every pane ready")
		}
	}
}

// stop ends the keeper as a stop on purpose does: it saves, ends its
// programs and goes.
func (r *run) stop() error {
	r.stdin.Close()
	return r.cmd.Wait()
}

// kill ends the keeper outright, with no word to it, and waits until every
// program of its panes has seen its terminal end.
func (r *run) kill(gone string, before int) int {
	r.t.Helper()
	time.Sleep(150 * time.Millisecond)
	r.cmd.Process.Kill()
	r.cmd.Wait()
	for end := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		marks, _ := filepath.Glob(filepath.Join(gone, "p*"))
		if len(marks) == before+len(r.Panes) {
			return len(marks)
		} else if time.Now().After(end) {
			r.t.Fatalf("%d of %d programs ended with the keeper: %q", len(marks)-before, len(r.Panes), marks)
		}
	}
}

func (r *run) pane(id string) seen {
	r.t.Helper()
	for _, p := range r.Panes {
		if p.Rec.ID == id {
			return p
		}
	}
	r.t.Fatalf("no pane %s", id)
	return seen{}
}

// index is where a line with this text is, the last one, or -1.
func index(lines []string, text string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], text) {
			return i
		}
	}
	return -1
}

// The row's check: the keeper killed outright with twelve agents, started
// again; and again; and then stopped on purpose and started a fourth time.
func TestKilledOutrightWithTwelveAgentsAndStartedAgain(t *testing.T) {
	env, dirs := login(t)
	gone := t.TempDir()
	self, _ := os.Executable()
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	lost := filepath.Join(gone, "agent-program"+filepath.Ext(self))
	if err = os.WriteFile(lost, data, 0o700); err != nil {
		t.Fatal(err)
	}

	first := startKeeper(t, env, gone)
	if len(first.Panes) != 19 || first.NTab != 6 || first.NPane != 20 {
		t.Fatalf("the first keeper has %d panes, counters %d and %d", len(first.Panes), first.NTab, first.NPane)
	}
	agents := 0
	for _, p := range first.Panes {
		if p.Rec.Agent == "claude" && p.Rec.Session != "" && p.Rec.Name != "lost" {
			agents++
		}
	}
	if agents != 12 {
		t.Fatalf("%d agents with a session, want twelve", agents)
	}
	marks := first.kill(gone, 0)
	// A program that has just ended can hold its file a moment longer.
	for end := time.Now().Add(10 * time.Second); os.Remove(lost) != nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("the program of one agent could not be removed")
		}
	}

	before, restarts := first, 0
	check := func(now *run) {
		t.Helper()
		restarts++
		if now.Instance == before.Instance {
			t.Error("the instance id is the same after a restart")
		}
		if string(now.Layout) != string(before.Layout) {
			t.Errorf("the layout came back as\n%s\nwas\n%s", now.Layout, before.Layout)
		}
		if now.NTab != before.NTab || now.NPane != before.NPane {
			t.Errorf("counters %d, %d came back as %d, %d", before.NTab, before.NPane, now.NTab, now.NPane)
		}
		if !slices.EqualFunc(mapKeys(now.Vars), mapKeys(before.Vars), func(a, b string) bool {
			return a == b && slices.Equal(now.Vars[a], before.Vars[b])
		}) {
			t.Errorf("the tabs' variables came back as %v, were %v", now.Vars, before.Vars)
		}
		if len(now.Panes) != len(before.Panes) {
			t.Fatalf("%d panes came back of %d", len(now.Panes), len(before.Panes))
		}
		for _, was := range before.Panes {
			id := was.Rec.ID
			p := now.pane(id)
			if p.Rec.Tab != was.Rec.Tab || p.Rec.Label != was.Rec.Label || p.Rec.Cwd != was.Rec.Cwd || p.W != was.W || p.H != was.H {
				t.Errorf("%s came back as %+v %dx%d, was %+v %dx%d", id, p.Rec, p.W, p.H, was.Rec, was.W, was.H)
			}
			orig := first.pane(id)
			mark := index(p.Lines, "-- restarted ")
			old, fresh := index(p.Lines[:max(mark, 0)], id+" line 60"), index(p.Lines, "args:")
			if mark < 0 || old < 0 || fresh < mark || index(p.Lines, id+" line 60") < fresh {
				t.Errorf("%s: its old lines, the line of the restart and its new program are at %d, %d, %d of %d", id, old, mark, fresh, len(p.Lines))
				continue
			}
			if n := len(slices.DeleteFunc(slices.Clone(p.Lines), func(l string) bool { return !strings.Contains(l, "-- restarted ") })); n != restarts {
				t.Errorf("%s shows %d lines of a restart after %d", id, n, restarts)
			}
			vars := "env: reef-" + was.Rec.Tab + " T" + was.Rec.Tab[1:] + " " + id
			if index(p.Lines[fresh:], vars) < 0 {
				t.Errorf("%s: its new program does not say %q", id, vars)
			}
			switch {
			case orig.Rec.Agent == "claude" && orig.Rec.Session != "" && orig.Rec.Name != "lost":
				want := append([]string{orig.Argv[0], "--resume", orig.Rec.Session}, orig.Argv[1:]...)
				if !slices.Equal(p.Started, want) || !slices.Equal(p.Argv, orig.Argv) {
					t.Errorf("%s was started with %q and keeps %q, want %q and %q", id, p.Started, p.Argv, want, orig.Argv)
				}
				if p.Rec.Agent != "claude" || p.Rec.Name != orig.Rec.Name || p.Rec.Session != orig.Rec.Session ||
					index(p.Lines[fresh:], "resumed "+orig.Rec.Session) < 0 {
					t.Errorf("%s: agent %s was not resumed in its session %s: %+v", id, orig.Rec.Name, orig.Rec.Session, p.Rec)
				}
			case len(orig.Argv) > 1 && orig.Argv[1] == "ui":
				if !slices.Equal(p.Started, orig.Argv) || !slices.Equal(p.Argv, orig.Argv) {
					t.Errorf("our pane %s was started with %q, want %q", id, p.Started, orig.Argv)
				}
			default:
				if len(p.Started) != 1 || p.Argv != nil || p.Rec.Agent != "" || p.Rec.Name != "" || p.Rec.Session != "" {
					t.Errorf("%s should be a shell that says so: started %q, keeps %q, %+v", id, p.Started, p.Argv, p.Rec)
				}
			}
		}
		before = now
	}

	second := startKeeper(t, env, gone)
	check(second)
	second.kill(gone, marks)
	third := startKeeper(t, env, gone)
	check(third)

	// Stopped on purpose, as before an upgrade: everything is in the file.
	if err = third.stop(); err != nil {
		t.Fatal(err)
	}
	f, _, err := restore.Load(platform.New(runtime.GOOS), dirs)
	if err != nil || len(f.Panes) != 19 {
		t.Fatalf("after a stop on purpose the file gives %v, %v", f, err)
	}
	check(startKeeper(t, env, gone))
}

func mapKeys(m map[string][]string) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// A keeper killed at any moment of a write leaves a file that reads back.
func TestKilledWhileWriting(t *testing.T) {
	env, dirs := login(t)
	self, _ := os.Executable()
	for i := range 12 {
		cmd := exec.Command(self)
		cmd.Env, cmd.Stderr = append(slices.Clone(env), envRole+"=writer"), os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		// Not before its first write is done: no file is no fault.
		for end := time.Now().Add(30 * time.Second); time.Now().Before(end); time.Sleep(time.Millisecond) {
			if _, err := os.Stat(restore.Path(dirs)); err == nil {
				break
			}
		}
		time.Sleep(time.Duration(i*7) * time.Millisecond)
		cmd.Process.Kill()
		cmd.Wait()
		f, lay, err := restore.Load(platform.New(runtime.GOOS), dirs)
		if err != nil || f == nil || len(lay.Tabs) != 1 || len(f.Panes[0].Lines) != 10000 {
			t.Fatalf("killed the writer for the %d. time: %v, %v", i+1, f != nil, err)
		}
		if left, _ := os.ReadDir(dirs.State); len(left) != 1 {
			t.Fatalf("%d files are left in the state folder, want the layout file alone", len(left))
		}
	}
}

func TestLoad(t *testing.T) {
	p := platform.New(runtime.GOOS)
	_, dirs := login(t)
	if f, lay, err := restore.Load(p, dirs); f != nil || lay != nil || err != nil {
		t.Fatalf("no file: %v, %v, %v", f, lay, err)
	}
	write := func(text string) {
		t.Helper()
		os.MkdirAll(dirs.State, 0o700)
		if err := os.WriteFile(restore.Path(dirs), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Not a file of ours, half a file, and a layout whose focus is no pane.
	for _, bad := range []string{"reef", `{"version":1,"layout":{"tabs":[{"id":"t1"`,
		`{"version":1,"layout":{"tabs":[{"id":"t1","focus":"p9","root":{"pane":"p1"}}],"active":0}}`} {
		write(bad)
		if f, _, err := restore.Load(p, dirs); f != nil || !errors.Is(err, restore.ErrUnusable) {
			t.Fatalf("%q: %v, %v", bad, f, err)
		}
		if kept, _ := os.ReadFile(restore.Path(dirs) + ".bad"); string(kept) != bad {
			t.Fatalf("%q was not kept aside: %q", bad, kept)
		}
		if err := restore.Save(p, dirs, bigFile()); err != nil {
			t.Fatalf("a save after %q: %v", bad, err)
		}
	}
	newer := `{"version":2,"layout":{}}`
	write(newer)
	if f, _, err := restore.Load(p, dirs); f != nil || !errors.Is(err, contract.ErrNewer) {
		t.Fatalf("a newer file: %v, %v", f, err)
	}
	if kept, _ := os.ReadFile(restore.Path(dirs)); string(kept) != newer {
		t.Fatal("a newer file was changed")
	}

	// A file that is sound but not in order: a place with no pane of its
	// own, a pane with no place, variables of a tab that is gone, counters
	// behind the ids, records that say what only the layout knows.
	write(`{"version":1,"w":100,"h":30,"tabs_made":1,"panes_made":2,
	 "layout":{"tabs":[{"id":"t4","label":"reef","focus":"p9","root":{"share":0.5,"a":{"pane":"p9"},"b":{"pane":"p3"}}}],"active":0},
	 "vars":{"t4":["A=1"],"t2":["B=2"]},
	 "panes":[{"pane":{"pane":"p3","tab":"t1","label":"old","cwd":"/reef","focused":true,"terminal":"x.1","status":"working"},"lines":["one"]},
	          {"pane":{"pane":"p5","tab":"t4"}}]}`)
	f, lay, err := restore.Load(p, dirs)
	if err != nil {
		t.Fatal(err)
	}
	want := []restore.Pane{{Rec: contract.Pane{ID: "p9", Tab: "t4", Label: "reef", Status: contract.StatusUnknown}},
		{Rec: contract.Pane{ID: "p3", Tab: "t4", Label: "reef", Cwd: "/reef", Status: contract.StatusUnknown}, Lines: []string{"one"}}}
	got, _ := json.Marshal(f.Panes)
	wanted, _ := json.Marshal(want)
	if string(got) != string(wanted) || f.NTab != 4 || f.NPane != 9 || len(f.Vars) != 1 || lay.W != 100 || lay.H != 30 {
		t.Fatalf("got %s, counters %d and %d, variables %v, window %dx%d", got, f.NTab, f.NPane, f.Vars, lay.W, lay.H)
	}
}

func TestWhatAPaneStarts(t *testing.T) {
	for _, c := range []struct {
		agent, session string
		argv, run      []string
	}{
		{"claude", "s1", []string{"claude", "--model", "m3"}, []string{"claude", "--resume", "s1", "--model", "m3"}},
		{"claude", "s2", []string{"/opt/reef/claude", "--resume", "s1", "--settings", "f.json", "-c", "--session-id=s0", "-r", "--fork-session", "--session-id", "s0", "--resume=s1"},
			[]string{"/opt/reef/claude", "--resume", "s2", "--settings", "f.json"}},
		{"claude", "", []string{"claude"}, nil},
		// A session that is no plain id is nobody's: as the value of the
		// option to resume it would be an option of its own.
		{"claude", "--dangerously-skip-permissions", []string{"claude"}, nil},
		{"claude", "s1 --and-more", []string{"claude"}, nil},
		{"claude", "s1\n", []string{"claude"}, nil},
		{"claude", "s1", nil, nil},
		{"codex", "s1", []string{"codex"}, nil},
		{"", "", []string{"/opt/reef/whaleshark", "ui", "run", "fleet", "--root", "/reef"}, []string{"/opt/reef/whaleshark", "ui", "run", "fleet", "--root", "/reef"}},
		{"", "", []string{"/opt/reef/whaleshark", "accept", "T7"}, nil},
		{"", "", nil, nil},
	} {
		lay := layout.Layout{}
		lay.Add("t1", "one", "p1")
		f := &restore.File{Panes: []restore.Pane{{Rec: contract.Pane{ID: "p1", Agent: c.agent, Name: "reef", Session: c.session}, Argv: c.argv}}}
		f.Layout, _ = json.Marshal(&lay)
		_, dirs := login(t)
		p := platform.New(runtime.GOOS)
		if err := restore.Save(p, dirs, f); err != nil {
			t.Fatal(err)
		}
		f, _, err := restore.Load(p, dirs)
		if err != nil {
			t.Fatal(err)
		}
		got := f.Panes[0]
		if !slices.Equal(got.Run, c.run) {
			t.Errorf("%s %q %q starts %q, want %q", c.agent, c.session, c.argv, got.Run, c.run)
		}
		if shell := c.run == nil; shell != (got.Argv == nil) || shell != (got.Rec.Name == "") || (got.Rec.Agent != "") != (c.agent != "" && !shell) {
			t.Errorf("%s %q %q comes back as %+v with %q", c.agent, c.session, c.argv, got.Rec, got.Argv)
		}
	}
}

func TestOldLines(t *testing.T) {
	s := screen.New(20, 5)
	if got := restore.Text(s, 100); got != nil {
		t.Fatalf("an empty screen gives %q", got)
	}
	for i := range 30 {
		s.Write([]byte("\x1b[31mline\x1b[m " + string(rune('a'+i%26)) + strings.Repeat(" ", i%3) + "\r\n"))
	}
	s.Write([]byte("last"))
	all := restore.Text(s, 100)
	if len(all) != 31 || all[0] != "line a" || all[29] != "line d" || all[30] != "last" {
		t.Fatalf("%d lines: %q", len(all), all)
	}
	if got := restore.Text(s, 3); !slices.Equal(got, all[28:]) {
		t.Fatalf("the last three: %q", got)
	}
	if got := restore.Text(s, 0); got != nil {
		t.Fatalf("none kept: %q", got)
	}

	// A file somebody changed cannot make the screen answer, ring, copy
	// or set a title: its lines are shown as text.
	fresh := screen.New(40, 6)
	bad := "\x1b]52;c;cmVlZg==\x07\x1b[c\x1b]0;title\x07\a\x1b[2Jplain\ttext"
	restore.Show(fresh, append(slices.Clone(all[26:]), bad), time.Date(2026, 3, 4, 21, 14, 0, 0, time.UTC))
	if len(fresh.Reply()) > 0 || len(fresh.Events()) > 0 || fresh.Title() != "" {
		t.Fatal("a saved line steered the screen")
	}
	want := []string{"line a", "line b", "line c", "line d", "last", "plain text", "-- restarted 21:14 --"}
	if got := restore.Text(fresh, 100); !slices.Equal(got, want) {
		t.Fatalf("shown: %q", got)
	}

	// However long the lines are, a pane's text stays under a megabyte.
	wide := screen.New(1000, 4)
	wide.Scrollback(5000)
	row := []byte(strings.Repeat("w", 999) + "\r\n")
	for range 3000 {
		wide.Write(row)
	}
	size := 0
	for _, line := range restore.Text(wide, 5000) {
		size += len(line)
	}
	if size > 1<<20 || size < 1<<19 {
		t.Fatalf("%d bytes of text kept", size)
	}
}

// files counts the writes of a Saver.
type files struct {
	contract.Files
	n atomic.Int32
}

// A write is counted once the file is in place: a test that waits for the
// count and then reads would else find no file yet.
func (f *files) Replace(tmp, final string) error {
	err := f.Files.Replace(tmp, final)
	f.n.Add(1)
	return err
}

func TestSaver(t *testing.T) {
	_, dirs := login(t)
	fs := &files{Files: platform.New(runtime.GOOS)}
	var label atomic.Value
	label.Store("zero")
	s := restore.Start(fs, dirs, func() *restore.File {
		lay := layout.Layout{}
		lay.Add("t1", label.Load().(string), "p1")
		f := &restore.File{}
		f.Layout, _ = json.Marshal(&lay)
		return f
	})
	read := func() string {
		t.Helper()
		_, lay, err := restore.Load(fs, dirs)
		if err != nil || lay == nil {
			t.Fatalf("reading back: %v", err)
		}
		return lay.Tabs[0].Label
	}
	wait := func(n int32, within time.Duration) {
		t.Helper()
		for end := time.Now().Add(within); fs.n.Load() < n; time.Sleep(5 * time.Millisecond) {
			if time.Now().After(end) {
				t.Fatalf("%d writes, want %d", fs.n.Load(), n)
			}
		}
	}
	time.Sleep(50 * time.Millisecond)
	if fs.n.Load() != 0 {
		t.Fatal("a write before anything changed")
	}
	// A change is written at once; a burst of changes is few writes, and
	// the last state is the one in the file.
	label.Store("one")
	s.Changed()
	wait(1, 2*time.Second)
	if read() != "one" {
		t.Fatal("the change is not in the file")
	}
	for i := range 200 {
		label.Store("burst")
		if s.Changed(); i%20 == 0 {
			time.Sleep(time.Millisecond)
		}
	}
	wait(2, 2*time.Second)
	time.Sleep(400 * time.Millisecond)
	if n := fs.n.Load(); n > 4 || read() != "burst" {
		t.Fatalf("%d writes for a burst, the file says %q", n, read())
	}
	// Output is written a few seconds later, once.
	n := fs.n.Load()
	label.Store("flowed")
	s.Flowed()
	wait(n+1, 5*time.Second)
	time.Sleep(100 * time.Millisecond)
	if fs.n.Load() != n+1 || read() != "flowed" || s.Err() != nil {
		t.Fatalf("after output: %d writes, %q, %v", fs.n.Load()-n, read(), s.Err())
	}
	label.Store("stopped")
	s.Stop()
	if read() != "stopped" {
		t.Fatal("the stop did not write")
	}
}

// A pane's last lines are in a file of their own beside the layout file:
// written a few seconds after output and at the stop, never for a change of
// the layout, and not again while the pane's program has printed nothing.
// The lines of a pane that is gone go with it.
func TestAPanesLinesAreWrittenWhenItPrinted(t *testing.T) {
	_, dirs := login(t)
	fs := &files{Files: platform.New(runtime.GOOS)}
	var printed, read atomic.Uint64
	var alone atomic.Bool
	s := restore.Start(fs, dirs, func() *restore.File {
		lay, f := layout.Layout{}, &restore.File{}
		lay.Add("t1", "one", "p1")
		f.Panes = []restore.Pane{{Rec: contract.Pane{ID: "p1"}, Count: printed.Load(), Text: func() []string {
			return []string{"reef " + strconv.FormatUint(printed.Load(), 10)}
		}}}
		if !alone.Load() {
			lay.Split("p1", contract.Right, 0.5, "p2")
			f.Panes = append(f.Panes, restore.Pane{Rec: contract.Pane{ID: "p2"}, Count: 7, Text: func() []string {
				read.Add(1)
				return []string{"quiet"}
			}})
		}
		f.Layout, _ = json.Marshal(&lay)
		return f
	})
	wait := func(n int32) {
		t.Helper()
		for end := time.Now().Add(6 * time.Second); fs.n.Load() < n; time.Sleep(5 * time.Millisecond) {
			if time.Now().After(end) {
				t.Fatalf("%d writes, want %d", fs.n.Load(), n)
			}
		}
		time.Sleep(300 * time.Millisecond)
		if fs.n.Load() != n {
			t.Fatalf("%d writes, want %d and no more", fs.n.Load(), n)
		}
	}
	lines := func() (got []string) {
		t.Helper()
		f, _, err := restore.Load(fs, dirs)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range f.Panes {
			got = append(got, p.Rec.ID+": "+strings.Join(p.Lines, "|"))
		}
		return got
	}
	// A change of the layout reads no pane's lines and writes one small file.
	s.Changed()
	wait(1)
	if left, _ := os.ReadDir(dirs.State); len(left) != 1 || read.Load() != 0 {
		t.Fatalf("after a change: %d files, the quiet pane read %d times", len(left), read.Load())
	}
	// Output: the layout and both panes' lines. More of it: the layout and
	// the lines of the pane that printed.
	printed.Store(1)
	s.Flowed()
	wait(4)
	if got := lines(); !slices.Equal(got, []string{"p1: reef 1", "p2: quiet"}) {
		t.Fatalf("after output the files give %q", got)
	}
	printed.Store(2)
	s.Flowed()
	wait(6)
	if got := lines(); !slices.Equal(got, []string{"p1: reef 2", "p2: quiet"}) || read.Load() != 1 {
		t.Fatalf("after more output the files give %q, the quiet pane read %d times", got, read.Load())
	}
	// The stop writes what printed since, and a closed pane's lines go.
	printed.Store(3)
	alone.Store(true)
	s.Stop()
	if left, _ := os.ReadDir(dirs.State); len(left) != 2 || fs.n.Load() != 8 || s.Err() != nil {
		t.Fatalf("after the stop: %d files, %d writes, %v", len(left), fs.n.Load(), s.Err())
	}
	// Reading back removes what belongs to no pane, and a file of lines
	// that cannot be read costs the pane its lines and nothing else.
	os.WriteFile(restore.Path(dirs)+".p9", []byte(`{"version":1,"text":""}`), 0o600)
	if got := lines(); !slices.Equal(got, []string{"p1: reef 3"}) {
		t.Fatalf("after the stop the files give %q", got)
	}
	os.WriteFile(restore.Path(dirs)+".p1", []byte(`{"version":1,"text":"cmVlZg=="}`), 0o600)
	if left, _ := os.ReadDir(dirs.State); len(left) != 2 || !slices.Equal(lines(), []string{"p1: "}) {
		t.Fatalf("%d files after reading back", len(left))
	}
}

func TestServiceEntries(t *testing.T) {
	env := map[string]string{"PATH": "/opt/reef tools/bin:/usr/bin", "SHELL": "/bin/zsh", "LANG": "en_GB.UTF-8",
		"XDG_STATE_HOME": `/srv/reef/state "a" & <b> 100% $HOME \n`}
	self := `/opt/reef tools/whale"shark" & $co <100%>`
	for system, file := range map[string]string{"darwin": "engine.plist", "linux": "engine.service"} {
		name, text := restore.Service(system, self, func(k string) string { return env[k] })
		path := filepath.Join("testdata", file)
		if *update {
			os.MkdirAll("testdata", 0o755)
			os.WriteFile(path, []byte(text), 0o644)
		}
		want, _ := os.ReadFile(path)
		if text != strings.ReplaceAll(string(want), "\r\n", "\n") || filepath.Ext(name) != filepath.Ext(file) {
			t.Errorf("%s: the entry %s is\n%s", system, name, text)
		}
	}
	if name, text := restore.Service("windows", self, os.Getenv); name != "" || text != "" {
		t.Errorf("windows has an entry: %s", name)
	}

	// The macOS entry read back as what it is: every value as it was given.
	_, text := restore.Service("darwin", self, func(k string) string { return env[k] })
	var values []string
	for d := xml.NewDecoder(strings.NewReader(text)); ; {
		tok, err := d.Token()
		if err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if s, ok := tok.(xml.StartElement); ok && s.Name.Local == "string" {
			var v string
			if err = d.DecodeElement(&v, &s); err != nil {
				t.Fatal(err)
			}
			values = append(values, v)
		}
	}
	for _, v := range []string{self, "engine", "run", env["PATH"], env["XDG_STATE_HOME"]} {
		if !slices.Contains(values, v) {
			t.Errorf("the entry does not hold %q: %q", v, values)
		}
	}
	// And by the system's own checker, where there is one.
	if plutil, err := exec.LookPath("plutil"); err == nil {
		path := filepath.Join(t.TempDir(), "entry.plist")
		os.WriteFile(path, []byte(text), 0o600)
		if out, err := exec.Command(plutil, "-lint", path).CombinedOutput(); err != nil {
			t.Errorf("plutil: %s", out)
		}
	}
}
