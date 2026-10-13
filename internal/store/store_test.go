package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/test/ownlogin"
)

const (
	envWriter = "STORE_TEST_WRITER" // the project a child process writes to
	envWrites = "STORE_TEST_WRITES" // how many changes it makes; none is until killed
)

// TestMain turns this test program into a writer when it is started as one.
func TestMain(m *testing.M) {
	if root := os.Getenv(envWriter); root != "" {
		n, _ := strconv.Atoi(os.Getenv(envWrites))
		writer(root, n)
		return
	}
	ownlogin.Main(m)
}

// writer changes the run and appends to its history, as a command does,
// until it has done n or for ever. Each change counts one up.
func writer(root string, n int) {
	st := real()
	for i := 0; n == 0 || i < n; i++ {
		var e contract.Event
		err := st.Change(root, "r1", func(s *contract.State) error {
			s.Counters.Seq++
			e = contract.Event{Seq: s.Counters.Seq, Kind: "done", Text: strings.Repeat("x", 200)}
			s.Inbox.Events = append(s.Inbox.Events, e)
			return nil
		})
		if err == nil {
			err = st.Append(root, "r1", []contract.Event{e, e}) // twice: it is read once
		}
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		if i == 0 {
			fmt.Println("writing")
		}
	}
}

// private: Windows keeps a file to its login by the folder it is in, and
// shows no mode to check.
const private = runtime.GOOS != "windows"

// plugging keeps the tests that make a store in each of several goroutines
// to one at a time here. Plugging is a program's first step, taken once
// before anything runs beside it, and the platform's sets what all share.
var plugging sync.Mutex

func real() *Store {
	plugging.Lock()
	defer plugging.Unlock()
	k := contract.NewKit()
	platform.Plug(k)
	Plug(k)
	return k.Store.(*Store)
}

// evening is the fixture's run, under the id given.
func evening(t testing.TB, run string) *contract.State {
	t.Helper()
	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	f.State.Run.ID = run
	return &f.State
}

func project(t testing.TB, runs ...string) (*Store, string) {
	t.Helper()
	st, root := real(), t.TempDir()
	for _, run := range runs {
		if err := st.Create(root, evening(t, run)); err != nil {
			t.Fatal(err)
		}
	}
	return st, root
}

func start(t testing.TB, root string, writes int) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), envWriter+"="+root, envWrites+"="+strconv.Itoa(writes))
	return cmd
}

func TestRunFoldersAndPointer(t *testing.T) {
	st, root := real(), t.TempDir()
	if _, err := st.Read(root, "r1"); !errors.Is(err, contract.ErrNoRun) {
		t.Fatalf("a project with nothing of ours: %v", err)
	}
	if runs, err := st.Runs(root); runs != nil || err != nil {
		t.Fatalf("runs of nothing: %v %v", runs, err)
	}
	if run, err := st.Current(root); run != "" || err != nil {
		t.Fatalf("current of nothing: %q %v", run, err)
	}
	if err := st.SetCurrent(root, "r1"); !errors.Is(err, contract.ErrNoRun) {
		t.Fatalf("pointing at no run: %v", err)
	}
	if _, err := os.Stat(st.Dir(root, "")); err == nil {
		t.Fatal("reading made a folder")
	}

	for _, run := range []string{"r10", "r2", "r1"} {
		if err := st.Create(root, evening(t, run)); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Create(root, evening(t, "r2")); err == nil {
		t.Fatal("a run was created twice")
	}
	if runs, _ := st.Runs(root); !slices.Equal(runs, []string{"r1", "r2", "r10"}) {
		t.Fatalf("runs, oldest first: %v", runs)
	}
	want, _ := json.Marshal(evening(t, "r2"))
	s, err := st.Read(root, "r2")
	if got, _ := json.Marshal(s); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("the record read back is not the one created: %v", err)
	}
	if _, err := st.Read(root, "r3"); !errors.Is(err, contract.ErrNoRun) {
		t.Fatalf("a run that does not exist: %v", err)
	}
	if err := st.Change(root, "r3", func(*contract.State) error { return nil }); !errors.Is(err, contract.ErrNoRun) {
		t.Fatalf("changing a run that does not exist: %v", err)
	}
	if _, err := st.History(root, "r3"); !errors.Is(err, contract.ErrNoRun) {
		t.Fatalf("the history of a run that does not exist: %v", err)
	}
	if got := st.Dir(root, "r2"); got != filepath.Join(root, ".whaleshark", "runs", "r2") {
		t.Fatalf("the run's folder: %s", got)
	}

	if err := st.SetCurrent(root, "r2"); err != nil {
		t.Fatal(err)
	}
	if run, err := st.Current(root); run != "r2" || err != nil {
		t.Fatalf("current: %q %v", run, err)
	}
	// The pointer is written under one name, so a writer killed in it leaves
	// at most that file, and the next one takes it over.
	os.WriteFile(filepath.Join(st.Dir(root, ""), "current.tmp"), []byte("half"), 0o600)
	if err := st.SetCurrent(root, "r2"); err != nil {
		t.Fatal(err)
	}
	if left, _ := os.ReadDir(st.Dir(root, "")); len(left) != 4 {
		t.Fatalf("beside the pointer, the two locks and the runs there is more: %v", left)
	}
	for _, path := range []string{"", "lock", "current", "runs", "runs/r2", "runs/r2/state.json"} {
		info, err := os.Stat(filepath.Join(st.Dir(root, ""), path))
		if err != nil {
			t.Fatal(err)
		}
		if private && info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%q is open to others: %v", path, info.Mode())
		}
	}

	if err := st.Remove(root, "r2"); err == nil {
		t.Fatal("an open run was removed")
	}
	if err := st.Change(root, "r2", func(s *contract.State) error { s.Run.ClosedAt = time.Now(); return nil }); err != nil {
		t.Fatal(err)
	}
	// What a removal of another run was cut off in goes with this one.
	os.MkdirAll(filepath.Join(st.Dir(root, ""), "runs", ".gone-r7", "attempts"), 0o700)
	if err := st.Remove(root, "r2"); err != nil {
		t.Fatal(err)
	}
	if runs, _ := st.Runs(root); !slices.Equal(runs, []string{"r1", "r10"}) {
		t.Fatalf("runs after a removal: %v", runs)
	}
	if run, err := st.Current(root); run != "" || err != nil {
		t.Fatalf("current still names a removed run: %q %v", run, err)
	}
	if left, _ := os.ReadDir(filepath.Join(st.Dir(root, ""), "runs")); len(left) != 2 {
		t.Fatalf("the removal left something: %v", left)
	}
	if err := st.SetCurrent(root, ""); err != nil {
		t.Fatal(err)
	}
}

func TestChangeIsAllOrNothing(t *testing.T) {
	st, root := project(t, "r1")
	file := filepath.Join(st.Dir(root, "r1"), stateFile)
	before, _ := os.ReadFile(file)
	stamp := time.Now().Add(-time.Hour)
	os.Chtimes(file, stamp, stamp)
	untouched := func(what string) {
		t.Helper()
		after, _ := os.ReadFile(file)
		info, _ := os.Stat(file)
		if !bytes.Equal(before, after) || !info.ModTime().Equal(stamp) {
			t.Fatalf("%s wrote the file", what)
		}
	}

	no := &contract.Refusal{Exit: contract.ExitRefused, Code: "no"}
	err := st.Change(root, "r1", func(s *contract.State) error {
		s.Counters.Seq = 999
		delete(s.Tasks, "T7")
		return no
	})
	if err != no {
		t.Fatalf("the function's own error is not what came back: %v", err)
	}
	untouched("a refused change")

	if err := st.Change(root, "r1", func(*contract.State) error { return nil }); err != nil {
		t.Fatal(err)
	}
	untouched("a change that changed nothing")

	err = st.Change(root, "r1", func(s *contract.State) error { s.Tasks["T7"].After = []string{"../T1"}; return nil })
	if err == nil {
		t.Fatal("a record the validator refuses was saved")
	}
	untouched("a change the validator refused")

	if err := st.Change(root, "r1", func(s *contract.State) error { s.Counters.Seq = 999; return nil }); err != nil {
		t.Fatal(err)
	}
	if s, _ := st.Read(root, "r1"); s.Counters.Seq != 999 {
		t.Fatal("a change was not saved")
	}
	if left, _ := os.ReadDir(st.Dir(root, "r1")); len(left) != 1 {
		t.Fatalf("a write left more than the record: %v", left)
	}
}

// counting is a platform that counts its locks and can refuse a replace.
type counting struct {
	contract.Platform
	held, taken atomic.Int32
	busy        error
}

func (c *counting) Lock(path string, exclusive bool) (func(), error) {
	unlock, err := c.Platform.Lock(path, exclusive)
	if err != nil {
		return nil, err
	}
	c.held.Add(1)
	c.taken.Add(1)
	return func() { c.held.Add(-1); unlock() }, nil
}

func (c *counting) Replace(tmp, final string) error {
	if c.held.Load() != 1 {
		return errors.New("a replace with no lock held")
	}
	if c.busy != nil {
		return c.busy
	}
	return c.Platform.Replace(tmp, final)
}

func TestLockAndReplaceArePlatforms(t *testing.T) {
	st, root := project(t, "r1")
	c := &counting{Platform: st.p}
	st.p = c
	seq := func(s *contract.State) error { s.Counters.Seq++; return nil }
	steps := []func() error{
		func() error { return st.Create(root, evening(t, "r2")) },
		func() error { return st.Change(root, "r1", seq) },
		func() error { return st.Change(root, "r1", func(*contract.State) error { return errors.New("no") }) },
		func() error { return st.Append(root, "r1", []contract.Event{{Seq: 1}}) },
		func() error { return st.SetCurrent(root, "r1") },
		func() error { _, err := st.Read(root, "r1"); return err },
		func() error { _, err := st.History(root, "r1"); return err },
		func() error { _, err := st.Read(root, "r9"); return err },
	}
	for i, step := range steps {
		step()
		// Two each: the turn, given back as soon as the lock is had, and the lock.
		if c.taken.Load() != int32(2*(i+1)) || c.held.Load() != 0 {
			t.Fatalf("step %d: %d locks taken, %d still held", i, c.taken.Load(), c.held.Load())
		}
	}

	c.busy = &contract.Refusal{Exit: contract.ExitEnv, Code: "state_busy"}
	before, _ := os.ReadFile(filepath.Join(st.Dir(root, "r1"), stateFile))
	if err := st.Change(root, "r1", seq); err != c.busy {
		t.Fatalf("state_busy was not handed on as it is: %v", err)
	}
	if err := st.SetCurrent(root, "r2"); err != c.busy {
		t.Fatalf("state_busy from the pointer was not handed on as it is: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(st.Dir(root, "r1"), stateFile))
	if run, _ := st.Current(root); !bytes.Equal(before, after) || run != "r1" {
		t.Fatal("a busy file was changed all the same")
	}
	if left, _ := os.ReadDir(st.Dir(root, "r1")); len(left) != 2 {
		t.Fatalf("a refused replace left its unfinished file: %v", left)
	}
}

// plant rewrites the version line of a file of ours.
func plant(t *testing.T, file string, version int) []byte {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	head := regexp.MustCompile(`"version": \d+`).ReplaceAll(data[:min(len(data), 40)], []byte(`"version": `+strconv.Itoa(version)))
	data = append(head, data[min(len(data), 40):]...)
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestVersionRule(t *testing.T) {
	st, root := project(t, "r1", "r2")
	if err := st.SetCurrent(root, "r1"); err != nil {
		t.Fatal(err)
	}
	newerState := func(err error) bool {
		var r *contract.Refusal
		return errors.As(err, &r) && r.Exit == contract.ExitEnv && r.Code == "newer_state"
	}

	// An older program meets a newer file.
	file := filepath.Join(st.Dir(root, "r1"), stateFile)
	newer := plant(t, file, contract.StateVersion+1)
	if _, err := st.Read(root, "r1"); !errors.Is(err, contract.ErrNewer) {
		t.Fatalf("reading a newer record: %v", err)
	}
	ran := false
	if err := st.Change(root, "r1", func(*contract.State) error { ran = true; return nil }); !newerState(err) || ran {
		t.Fatalf("changing a newer record: %v, function ran: %v", err, ran)
	}
	if err := st.Remove(root, "r1"); !newerState(err) {
		t.Fatalf("removing a newer record: %v", err)
	}
	// The history has the version of the record beside it and no other.
	history := filepath.Join(st.Dir(root, "r1"), historyFile)
	os.WriteFile(history, []byte("{\"seq\":1,\"shape\":\"of a later day\"}\n"), 0o600)
	if _, err := st.History(root, "r1"); !errors.Is(err, contract.ErrNewer) {
		t.Fatalf("reading the history of a newer run: %v", err)
	}
	if err := st.Append(root, "r1", []contract.Event{{Seq: 2}}); !newerState(err) {
		t.Fatalf("adding to the history of a newer run: %v", err)
	}
	if lines, _ := os.ReadFile(history); bytes.Count(lines, []byte("\n")) != 1 {
		t.Fatalf("the history of a newer run was added to:\n%s", lines)
	}
	if after, _ := os.ReadFile(file); !bytes.Equal(after, newer) {
		t.Fatal("a newer record was written")
	}
	// A newer shape that this program cannot even decode is still "newer".
	os.WriteFile(file, []byte(`{"version": 7, "tasks": ["a list now"]}`), 0o600)
	if _, err := st.Read(root, "r1"); !errors.Is(err, contract.ErrNewer) {
		t.Fatalf("reading a newer shape: %v", err)
	}

	pointer := filepath.Join(st.Dir(root, ""), currentFile)
	newer = plant(t, pointer, contract.FileVersion+1)
	if run, err := st.Current(root); run != "" || err != nil {
		t.Fatalf("a newer pointer is not treated as absent: %q %v", run, err)
	}
	if err := st.SetCurrent(root, "r2"); !newerState(err) {
		t.Fatalf("writing a newer pointer: %v", err)
	}
	if after, _ := os.ReadFile(pointer); !bytes.Equal(after, newer) {
		t.Fatal("a newer pointer was written")
	}

	// A newer program meets an older file: the first write keeps it beside.
	file = filepath.Join(st.Dir(root, "r2"), stateFile)
	older := plant(t, file, contract.StateVersion-1)
	if err := st.Change(root, "r2", func(*contract.State) error { return nil }); err != nil {
		t.Fatal(err)
	}
	kept, _ := os.ReadFile(filepath.Join(st.Dir(root, "r2"), "state.v"+strconv.Itoa(contract.StateVersion-1)+".json"))
	if s, err := st.Read(root, "r2"); err != nil || s.Version != contract.StateVersion || !bytes.Equal(kept, older) {
		t.Fatalf("the upgrade: %v, old file kept: %v", err, bytes.Equal(kept, older))
	}
}

func TestTornHistoryLine(t *testing.T) {
	st, root := project(t, "r1")
	if events, err := st.History(root, "r1"); events != nil || err != nil {
		t.Fatalf("a run with no history yet: %v %v", events, err)
	}
	three := []contract.Event{{Seq: 1, Kind: "done", Task: "T1"}, {Seq: 2, Kind: "failed"}, {Seq: 3, Kind: "human", Text: "two\nlines"}}
	if err := st.Append(root, "r1", three); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(st.Dir(root, "r1"), historyFile)
	whole, _ := os.ReadFile(file)
	f, _ := os.OpenFile(file, os.O_WRONLY|os.O_APPEND, 0)
	f.WriteString(`{"seq":4,"kind":"do`)
	f.Close()

	seqs := func() (out []int) {
		t.Helper()
		events, err := st.History(root, "r1")
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range events {
			out = append(out, e.Seq)
		}
		return out
	}
	if got := seqs(); !slices.Equal(got, []int{1, 2, 3}) {
		t.Fatalf("with a torn last line: %v", got)
	}
	if err := st.Append(root, "r1", []contract.Event{{Seq: 5, Kind: "done"}}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(file)
	if got := seqs(); !slices.Equal(got, []int{1, 2, 3, 5}) || !bytes.HasPrefix(data, whole) || bytes.Contains(data, []byte(`"do{`)) {
		t.Fatalf("after the next append: %v\n%s", got, data)
	}
	// A batch that an acknowledgement cut off half-way wrote a second time is read once.
	st.Append(root, "r1", []contract.Event{{Seq: 5, Kind: "done"}, {Seq: 6, Kind: "done"}})
	if got := seqs(); !slices.Equal(got, []int{1, 2, 3, 5, 6}) {
		t.Fatalf("an event that is there twice: %v", got)
	}
	if info, _ := os.Stat(file); private && info.Mode().Perm() != 0o600 {
		t.Fatalf("the history is open to others: %v", info.Mode())
	}

	// A file that is nothing but a torn line.
	os.WriteFile(file, []byte(`{"seq":1`), 0o600)
	if got := seqs(); got != nil {
		t.Fatalf("only a torn line: %v", got)
	}
	st.Append(root, "r1", three[:1])
	if got := seqs(); !slices.Equal(got, []int{1}) {
		t.Fatalf("an append over only a torn line: %v", got)
	}
	// A whole line that is not an event is damage, and is said.
	os.WriteFile(file, []byte("{\"seq\":1}\nnot an event\n{\"seq\":3}\n"), 0o600)
	if _, err := st.History(root, "r1"); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("a damaged line in the middle: %v", err)
	}
}

func TestLoadedValuesPassTheValidator(t *testing.T) {
	st, root := project(t, "r1")
	file := filepath.Join(st.Dir(root, "r1"), stateFile)
	good, _ := os.ReadFile(file)
	esc := string(rune(27))
	for what, change := range map[string]func(s *contract.State){
		"a task id that climbs":         func(s *contract.State) { s.Tasks["../T7"] = s.Tasks["T7"]; s.Tasks["../T7"].ID = "../T7" },
		"a task filed under another id": func(s *contract.State) { s.Tasks["T99"] = s.Tasks["T7"] },
		"a task that is nothing":        func(s *contract.State) { s.Tasks["T99"] = nil },
		"a title that steers a screen":  func(s *contract.State) { s.Tasks["T7"].Title = esc + "]0;gone" },
		"a name of two lines":           func(s *contract.State) { s.Tasks["T7"].Name = "a\nb" },
		"a pattern that climbs":         func(s *contract.State) { s.Tasks["T7"].Owns = []string{"../other/**"} },
		"a folder read as an option":    func(s *contract.State) { s.Tasks["T7"].Placement = contract.PlaceCwd + "--upload-pack=x" },
		"an agent kind with a space":    func(s *contract.State) { s.Tasks["T7"].Agent = "claude --yes" },
		"a pane read as an option":      func(s *contract.State) { s.Run.Orchestrator.Pane = "-w1" },
		"an attempt that is nothing":    func(s *contract.State) { s.Attempts["T7.9"] = nil },
		"an attempt of a bad task":      func(s *contract.State) { s.Attempts["T7.1"].Task = "T7;x" },
		"an item of a bad attempt":      func(s *contract.State) { s.Questions["q1"] = &contract.Question{ID: "q1", Attempt: "T7"} },
		"another run's record":          func(s *contract.State) { s.Run.ID = "r2" },
		"a fleet named as an option":    func(s *contract.State) { s.Tasks["T7"].Fleet = "--x" },
		"a fleet of four words":         func(s *contract.State) { s.Run.Fleets = map[string]*contract.Fleet{"a b c d": {Run: "r2"}} },
		"a fleet whose run climbs":      func(s *contract.State) { s.Run.Fleets = map[string]*contract.Fleet{"billing": {Run: "../r2"}} },
		"a fleet's tool with a space": func(s *contract.State) {
			s.Run.Fleets = map[string]*contract.Fleet{"billing": {Run: "r2", Tool: "x y"}}
		},
		"a parent that climbs":         func(s *contract.State) { s.Run.Parent = &contract.Twin{Run: "..", Fleet: "billing"} },
		"an attempt that leads a path": func(s *contract.State) { s.Attempts["T7.1"].Leads = "a/b" },
		"a twin that climbs": func(s *contract.State) {
			s.Questions["q1"] = &contract.Question{ID: "q1", Up: &contract.Twin{Run: "r1", ID: "../q"}}
		},
	} {
		s := new(contract.State)
		if err := json.Unmarshal(good, s); err != nil {
			t.Fatal(err)
		}
		change(s)
		data, _ := json.Marshal(s)
		os.WriteFile(file, data, 0o600)
		if _, err := st.Read(root, "r1"); err == nil || errors.Is(err, contract.ErrNoRun) {
			t.Errorf("%s was loaded: %v", what, err)
		}
		if err := st.Change(root, "r1", func(*contract.State) error { return nil }); err == nil {
			t.Errorf("%s was loaded for a change", what)
		}
	}
	os.WriteFile(file, good[:len(good)/2], 0o600)
	if _, err := st.Read(root, "r1"); err == nil {
		t.Error("half a record was loaded")
	}

	for _, run := range []string{"../r1", "-r", "r 1", strings.Repeat("r", 17)} {
		if _, err := st.Read(root, run); err == nil || errors.Is(err, contract.ErrNoRun) {
			t.Errorf("the run id %q was taken: %v", run, err)
		}
		s := evening(t, run)
		if err := st.Create(root, s); err == nil {
			t.Errorf("a run %q was created", run)
		}
	}
	if left, _ := os.ReadDir(st.Dir(root, "")); len(left) != 3 {
		t.Fatalf("a refused id left something besides the two locks and the runs: %v", left)
	}
}

// TestKills kills a writer 500 times in the middle of its writes. Every
// time, the record and the history must read whole and must not have gone back.
func TestKills(t *testing.T) {
	const kills, at = 500, 10
	var torn, unfinished atomic.Int32
	var wg sync.WaitGroup
	for range at {
		wg.Go(func() {
			st, root := project(t, "r1")
			first, _ := st.Read(root, "r1")
			dir, seen, had := st.Dir(root, "r1"), first.Counters.Seq, len(first.Inbox.Events)-first.Counters.Seq
			for i := range kills / at {
				cmd := start(t, root, 0)
				out, _ := cmd.StdoutPipe()
				if err := cmd.Start(); err != nil {
					t.Error(err)
					return
				}
				line, _ := bufio.NewReader(out).ReadString('\n')
				time.Sleep(time.Duration(rand.IntN(12000)) * time.Microsecond)
				cmd.Process.Kill()
				cmd.Wait()
				if line != "writing\n" {
					t.Errorf("kill %d: the writer said %q", i, line)
					return
				}

				if _, err := os.Stat(filepath.Join(dir, stateFile+".tmp")); err == nil {
					unfinished.Add(1)
				}
				if data, _ := os.ReadFile(filepath.Join(dir, historyFile)); !bytes.HasSuffix(data, []byte{'\n'}) {
					torn.Add(1)
				}
				s, err := st.Read(root, "r1")
				if err != nil {
					t.Errorf("kill %d: the record cannot be read: %v", i, err)
					return
				}
				events, err := st.History(root, "r1")
				if err != nil {
					t.Errorf("kill %d: the history cannot be read: %v", i, err)
					return
				}
				// Every change is in the record once; the history, where each
				// was written twice, gives each once and may lack only what
				// the killed writer still owed.
				n := s.Counters.Seq - first.Counters.Seq
				if s.Counters.Seq <= seen || len(s.Inbox.Events)-had != s.Counters.Seq || len(events) > n || len(events) < n-i-1 {
					t.Errorf("kill %d: %d changes, at %d after %d, with %d events and %d lines of history", i, n, s.Counters.Seq, seen, len(s.Inbox.Events), len(events))
					return
				}
				for j, e := range events {
					if j > 0 && e.Seq < events[j-1].Seq {
						t.Errorf("kill %d: the history goes back at line %d", i, j+1)
						return
					}
				}
				seen = s.Counters.Seq
			}
			left, _ := os.ReadDir(dir)
			if len(left) > 3 {
				t.Errorf("the kills left files behind: %v", left)
			}
		})
	}
	wg.Wait()
	t.Logf("%d kills; %d found a write of the record unfinished, %d a torn line of history", kills, unfinished.Load(), torn.Load())
}

func TestTwentyProcesses(t *testing.T) {
	const writers, each = 20, 20
	st, root := project(t, "r1")
	before, _ := st.Read(root, "r1")
	stop := make(chan struct{})
	var reads atomic.Int32
	var readers, wg sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for last := 0; ; reads.Add(1) {
				select {
				case <-stop:
					return
				default:
				}
				s, err := st.Read(root, "r1")
				if err != nil || s.Counters.Seq < last {
					t.Errorf("a reader beside the writers: %v", err)
					return
				}
				last = s.Counters.Seq
				// Readers that never pause hold the shared lock between
				// them without a gap, and no writer gets in.
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
	for range writers {
		wg.Go(func() {
			if out, err := start(t, root, each).CombinedOutput(); err != nil {
				t.Errorf("a writer failed: %v\n%s", err, out)
			}
		})
	}
	wg.Wait()
	close(stop)
	readers.Wait()
	s, err := st.Read(root, "r1")
	if err != nil {
		t.Fatal(err)
	}
	events, err := st.History(root, "r1")
	if got := s.Counters.Seq - before.Counters.Seq; got != writers*each || len(events) != writers*each || err != nil {
		t.Fatalf("%d processes made %d changes and %d events of history, not %d and %d: %v", writers, got, len(events), writers*each, writers*each, err)
	}
	t.Logf("%d changes by %d processes, %d reads beside them", writers*each, writers, reads.Load())
}

// big is a run of 200 tasks, each with one attempt, and 1,000 events.
func big() *contract.State {
	now := time.Date(2026, 10, 8, 21, 14, 0, 0, time.UTC)
	s := &contract.State{Version: contract.StateVersion, Run: contract.Run{ID: "r1", Objective: "a long run", CreatedAt: now, Limit: 20},
		Tasks: map[string]*contract.Task{}, Attempts: map[string]*contract.Attempt{}, Questions: map[string]*contract.Question{}}
	for i := 1; i <= 200; i++ {
		id := "T" + strconv.Itoa(i)
		s.Tasks[id] = &contract.Task{ID: id, Name: "task " + strconv.Itoa(i), Title: "build the part with the number " + strconv.Itoa(i),
			Brief: "briefs/" + id + ".md", Owns: []string{"src/part" + strconv.Itoa(i) + "/**", "docs/" + id + ".md"}, Check: "npm test",
			Placement: contract.PlaceShared, Status: contract.TaskRunning, Attempts: []string{id + ".1"}, CreatedAt: now}
		s.Attempts[id+".1"] = &contract.Attempt{ID: id + ".1", Task: id, N: 1, State: contract.AttemptWorking, StateSince: now,
			TokenHash: strings.Repeat("ab", 32), Agent: contract.Agent{Kind: "claude", Name: "h1a2b-r1-t" + strconv.Itoa(i) + "-1"},
			Place:    contract.Place{Tab: "w1:t" + strconv.Itoa(i), Pane: "w1:p" + strconv.Itoa(i), Workspace: "w1", Cwd: "project", OpenedByUs: true},
			Seen:     contract.AgentSeen{Status: contract.StatusWorking, At: now, Liveness: contract.Live, LastWorking: now},
			Progress: &contract.Progress{Pct: 40, Note: "half of the tests pass, working on the rest", At: now}, StartedAt: now}
	}
	for i := 1; i <= 1000; i++ {
		s.Inbox.Events = append(s.Inbox.Events, contract.Event{Seq: i, At: now, Kind: "done", Task: "T" + strconv.Itoa(i%200+1),
			Attempt: "T" + strconv.Itoa(i%200+1) + ".1", Text: "the part is built and its tests pass; the result file says what is left"})
	}
	s.Counters.Seq = 1000
	return s
}

// middle is the middle one and the slowest of some times.
func middle(d []time.Duration) (mid, worst time.Duration) {
	slices.Sort(d)
	return d[len(d)/2].Round(10 * time.Microsecond), d[len(d)-1].Round(10 * time.Microsecond)
}

// TestWriteTime reports what one change and one read cost at 200 tasks and
// 1,000 events, and how much of the change is the flush to disk.
func TestWriteTime(t *testing.T) {
	const times = 60
	st, root := real(), t.TempDir()
	if err := st.Create(root, big()); err != nil {
		t.Fatal(err)
	}
	dir := st.Dir(root, "r1")
	info, _ := os.Stat(filepath.Join(dir, stateFile))
	var change, read, flushes, add []time.Duration
	for i := range times {
		at := time.Now()
		if err := st.Change(root, "r1", func(s *contract.State) error { s.Counters.Seq++; return nil }); err != nil {
			t.Fatal(err)
		}
		change = append(change, time.Since(at))

		at = time.Now()
		if _, err := st.Read(root, "r1"); err != nil {
			t.Fatal(err)
		}
		read = append(read, time.Since(at))

		// The two flushes of a change again, by themselves.
		at = time.Now()
		f, _ := os.OpenFile(filepath.Join(dir, stateFile), os.O_RDWR, 0)
		f.WriteAt([]byte{'{'}, 0)
		f.Sync()
		f.Close()
		flush(dir)
		flushes = append(flushes, time.Since(at))

		at = time.Now()
		if err := st.Append(root, "r1", []contract.Event{{Seq: i, Kind: "done", Text: "one line of history"}}); err != nil {
			t.Fatal(err)
		}
		add = append(add, time.Since(at))
	}
	mid, worst := middle(change)
	t.Logf("a record of 200 tasks and 1,000 events is %d bytes", info.Size())
	t.Logf("one change (lock, load, check, save, flush): middle %v, slowest %v of %d", mid, worst, times)
	mid, worst = middle(flushes)
	t.Logf("of which the two flushes to disk:             middle %v, slowest %v", mid, worst)
	mid, worst = middle(read)
	t.Logf("one read (lock, load, check):                 middle %v, slowest %v", mid, worst)
	mid, worst = middle(add)
	t.Logf("one line of history, flushed:                 middle %v, slowest %v", mid, worst)
}

// Readers that never pause do not keep a writer out: everybody passes the
// turn one at a time, and a writer keeps it while the readers inside leave.
func TestReadersDoNotStarveAWriter(t *testing.T) {
	st, root := project(t, "r1")
	const readers, changes = 16, 100
	var stop atomic.Bool
	var reads atomic.Int64
	var wg sync.WaitGroup
	for range readers {
		wg.Go(func() {
			for r := real(); !stop.Load(); reads.Add(1) {
				if _, err := r.Read(root, "r1"); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	began := time.Now()
	for range changes {
		if err := st.Change(root, "r1", func(s *contract.State) error { s.Counters.Seq++; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	took := time.Since(began)
	stop.Store(true)
	wg.Wait()
	t.Logf("%d changes beside %d readers that never pause: %v each, %d reads meanwhile", changes, readers, took/changes, reads.Load())
	// Without the turn no change got in for minutes; with it one takes about
	// 10 ms. A second each is the line between the two that still holds on a
	// machine loaded far beyond its cores, where 100 ms did not.
	if took > changes*time.Second {
		t.Errorf("a change waited %v on average behind readers", took/changes)
	}
}

// Two runs of one project are changed as one step (design 19.1): a function
// that fails writes neither, both are saved when it does not, and each is
// upgraded as a single run would be.
func TestBothRunsUnderOneLock(t *testing.T) {
	st, root := project(t, "r1", "r2")
	plant(t, filepath.Join(st.Dir(root, "r2"), stateFile), contract.StateVersion-1)
	failed := errors.New("no")
	err := st.Both(root, "r1", "r2", func(up, low *contract.State) error {
		up.Run.Mode, low.Run.Parent = contract.ModeFleets, &contract.Twin{Run: "r1", Fleet: "billing"}
		return failed
	})
	if s, _ := st.Read(root, "r1"); err != failed || s.Run.Mode != "" {
		t.Fatalf("a refused change was written: %v", err)
	}
	if err := st.Both(root, "r1", "r2", func(up, low *contract.State) error {
		up.Run.Mode, low.Run.Parent = contract.ModeFleets, &contract.Twin{Run: "r1", Fleet: "billing"}
		up.Run.Fleets = map[string]*contract.Fleet{"billing": {Run: low.Run.ID, Tool: "codex"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	up, _ := st.Read(root, "r1")
	low, err := st.Read(root, "r2")
	if err != nil || up.Run.Fleets["billing"].Run != "r2" || low.Run.Parent.Fleet != "billing" || low.Version != contract.StateVersion {
		t.Fatalf("after the change: %+v %+v %v", up.Run.Fleets, low.Run.Parent, err)
	}
	if _, err := os.Stat(filepath.Join(st.Dir(root, "r2"), "state.v"+strconv.Itoa(contract.StateVersion-1)+".json")); err != nil {
		t.Errorf("the older file of the lower run was not kept: %v", err)
	}
	if err := st.Both(root, "r1", "r9", func(_, _ *contract.State) error { return nil }); !errors.Is(err, contract.ErrNoRun) {
		t.Errorf("a fleet's run that is not there: %v", err)
	}
	if err := st.Both(root, "r1", "../r2", func(_, _ *contract.State) error { return nil }); err == nil {
		t.Error("a run's name that is a path was taken")
	}
}

// line is how long the slowest write of the test below may wait for its
// turn, for whoever measures it: a machine busy with other work makes any
// write wait, so no figure is held unless one is asked for.
var line = flag.Duration("line", 0, "hold the slowest write of twenty agents under one lock to this long")

// Twenty agents of four fleets and their orchestrator write under the
// project's one lock, each of twenty writers changing its fleet's run
// fifteen times, every fifth time with the orchestrator's run in the same
// step, while eight readers never pause. Every write gets its turn and none
// is lost: each counts one up in the record it changes, and the counts come
// to what was written. How long the slowest waited is said, and held only
// to a figure that was asked for (-line 5s).
func TestTwentyAgentsOfFourFleetsUnderOneLock(t *testing.T) {
	st, root := project(t, "r1", "r2", "r3", "r4", "r5")
	written := map[string]int{"r1": 60, "r2": 75, "r3": 75, "r4": 75, "r5": 75}
	was := map[string]int{}
	for run := range written {
		s, err := st.Read(root, run)
		if err != nil {
			t.Fatal(err)
		}
		was[run] = s.Counters.Seq
	}
	stop, readers := make(chan struct{}), sync.WaitGroup{}
	for range 8 {
		readers.Go(func() {
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
					st.Read(root, "r"+strconv.Itoa(1+i%5))
				}
			}
		})
	}
	var writers sync.WaitGroup
	var worst atomic.Int64
	began := time.Now()
	for w := range 20 {
		writers.Go(func() {
			low := "r" + strconv.Itoa(2+w%4)
			for i := range 15 {
				at := time.Now()
				var err error
				if i%5 == 4 {
					err = st.Both(root, "r1", low, func(up, down *contract.State) error {
						up.Counters.Seq++
						down.Counters.Seq++
						return nil
					})
				} else {
					err = st.Change(root, low, func(s *contract.State) error { s.Counters.Seq++; return nil })
				}
				if took := time.Since(at); err != nil {
					t.Error(err)
				} else if took.Nanoseconds() > worst.Load() {
					worst.Store(took.Nanoseconds())
				}
			}
		})
	}
	writers.Wait()
	close(stop)
	readers.Wait()
	for run, want := range written {
		if s, err := st.Read(root, run); err != nil || s.Counters.Seq-was[run] != want {
			t.Errorf("%s holds %d of the %d writes made to it (%v)", run, s.Counters.Seq-was[run], want, err)
		}
	}
	took, slowest := time.Since(began), time.Duration(worst.Load())
	t.Logf("300 writes by twenty writers beside eight readers: %v in all, %v a write, the slowest %v", took.Round(time.Millisecond),
		(took / 300).Round(time.Microsecond), slowest.Round(time.Millisecond))
	if *line > 0 && slowest > *line {
		t.Errorf("a write waited %v for its turn, more than the %v asked for", slowest, *line)
	}
}

// Phase 6 in the record. A file of the version before knows none of the new
// fields: it is read as it stands, and the first change writes it back as
// the new version with what that change added, the older file kept beside
// it. What an approval or an agent's wish names passes the validator on
// every load, so a record edited by hand cannot name a task that is no id
// or a setting that is none.
func TestTheRecordOfPhaseSix(t *testing.T) {
	st, root := project(t, "r2")
	file := filepath.Join(st.Dir(root, "r2"), stateFile)
	older := plant(t, file, 2)
	if s, err := st.Read(root, "r2"); err != nil || s.Version != 2 || s.Run.Plan != nil || s.Run.Protect != nil || s.Run.Spend != (contract.Spend{}) {
		t.Fatalf("a record of version 2 as it stands: %+v, %v", s, err)
	}
	at := time.Date(2026, 10, 12, 9, 2, 0, 0, time.UTC)
	err := st.Change(root, "r2", func(s *contract.State) error {
		s.Run.Plan = &contract.Approval{Approved: []string{"T1", "T2"}, At: at, By: &contract.Origin{Caller: contract.Human, Where: contract.WherePane}}
		s.Run.Protect = &contract.Approval{Seen: "f1", Files: []string{"db/migrations/0042_refunds.sql"}, Item: "n9"}
		s.Run.Spend = contract.Spend{More: 8, Warned: true}
		for _, a := range s.Attempts {
			a.Rested, a.Seen.StuckSince, a.Seen.StuckWhy, a.Seen.StuckTold = 90*time.Minute, at, "stale,check", contract.ToldLeader
		}
		for _, q := range s.Questions {
			q.Tasks, q.Set = []string{"T1"}, &contract.SetAsk{Key: "spend.guard", From: contract.On, To: contract.Off}
		}
		for _, task := range s.Tasks {
			task.CheckFails = 3
		}
		return nil
	})
	kept, _ := os.ReadFile(filepath.Join(st.Dir(root, "r2"), "state.v2.json"))
	s, rerr := st.Read(root, "r2")
	if err != nil || rerr != nil || s.Version != contract.StateVersion || contract.StateVersion != 3 || !bytes.Equal(kept, older) ||
		!slices.Equal(s.Run.Plan.Approved, []string{"T1", "T2"}) || s.Run.Plan.By.Where != contract.WherePane || s.Run.Protect.Item != "n9" || s.Run.Spend.More != 8 {
		t.Fatalf("the upgrade to version 3: %v, %v, %+v", err, rerr, s)
	}
	for _, a := range s.Attempts {
		if a.Rested != 90*time.Minute || a.Seen.StuckWhy != "stale,check" || a.Seen.StuckTold != contract.ToldLeader || !a.Seen.StuckSince.Equal(at) {
			t.Fatalf("an attempt's phase 6 fields read back: %+v", a)
		}
	}
	for what, spoil := range map[string]func(*contract.State){
		"a plan's task that is no id": func(s *contract.State) { s.Run.Plan.Approved = []string{"../T1"} },
		"an item that is no id":       func(s *contract.State) { s.Run.Protect.Item = "n 9" },
		"a setting that is none": func(s *contract.State) {
			for _, q := range s.Questions {
				q.Set = &contract.SetAsk{Key: "no.such"}
			}
		},
	} {
		if err := st.Change(root, "r2", func(s *contract.State) error { spoil(s); return nil }); err == nil {
			t.Errorf("%s was written", what)
		}
		if _, err := st.Read(root, "r2"); err != nil {
			t.Fatalf("after %s was refused the record does not read: %v", what, err)
		}
	}
	// The kit's afternoon with every guard on is a record this store takes.
	g := testkit.Guarded()
	for _, s := range []*contract.State{g.Up, g.Low["billing"], g.Low["search"]} {
		if err := st.Create(root, s); err != nil {
			t.Errorf("the fixture's run %s: %v", s.Run.ID, err)
		}
	}
	if contract.HoldPath(root, "r2") != filepath.Join(st.Dir(root, "r2"), contract.HoldFile) {
		t.Errorf("the hold file is not in the run's folder: %s", contract.HoldPath(root, "r2"))
	}
}
