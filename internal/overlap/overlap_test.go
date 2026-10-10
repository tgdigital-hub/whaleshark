package overlap

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/gitwt"
	"github.com/tgdigital-hub/whaleshark/internal/integrate"
	"github.com/tgdigital-hub/whaleshark/internal/rules"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// -files N builds a repository of N files with twenty tasks and times a
// scan of it; with 2000 the scan must take less than three seconds.
var files = flag.Int("files", 0, "time a scan of twenty tasks on a repository of this many files")

func TestMain(m *testing.M) { scenario.Main(m) }

// fix is a made-up project that is a real repository, with one open run
// whose branch was begun.
type fix struct {
	t    testing.TB
	p    *scenario.Project
	k    *contract.Kit
	o    scanner
	root string
	work string // where the tasks' copies of the code are
}

func start(t testing.TB, base map[string]string) *fix {
	t.Helper()
	// Every git of the test, the program's own included, has the settings
	// of the test kit and nobody's own.
	for _, pair := range testkit.GitEnv()[len(os.Environ()):] {
		name, value, _ := strings.Cut(pair, "=")
		t.Setenv(name, value)
	}
	p := scenario.Prepare(t, nil, nil)
	rules.Plug(p.Kit)
	gitwt.Plug(p.Kit)
	integrate.Plug(p.Kit)
	Plug(p.Kit)
	work, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	f := &fix{t: t, p: p, k: p.Kit, o: scanner{p.Kit}, root: p.Root, work: work}
	f.git(f.root, "init", "-q", "-b", "main")
	// Git's own tidying after a commit runs on behind it, and would pack
	// objects under a test that counts them.
	f.git(f.root, "config", "maintenance.auto", "false")
	f.git(f.root, "config", "gc.auto", "0")
	must(t, os.WriteFile(filepath.Join(f.root, ".git", "info", "exclude"), []byte(".whaleshark\nwhaleshark.toml\n"), 0o600))
	testkit.Commit(t, f.root, "start", base)
	f.play(0, "run", "new", "a shop")
	return f
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (f *fix) git(dir string, args ...string) string { return testkit.Git(f.t, dir, args...) }

func (f *fix) state() *contract.State {
	s, _ := f.p.Record()
	return s
}

func (f *fix) change(fn func(*contract.State)) {
	f.t.Helper()
	must(f.t, f.k.Store.Change(f.root, "r1", func(s *contract.State) error { fn(s); return nil }))
}

// play runs the real program as the lead agent and holds it to its exit
// code and to words it must print.
func (f *fix) play(exit int, args ...string) string {
	f.t.Helper()
	cmd := f.p.Command(scenario.Orch, args...)
	out, _ := cmd.CombinedOutput()
	if got := cmd.ProcessState.ExitCode(); got != exit {
		f.t.Fatalf("%v: exit %d, expected %d\n%s", args, got, exit, out)
	}
	return string(out)
}

// task gives a task its own copy of the code, cut from the collected work,
// with one commit of the files given, and an agent at work in it.
func (f *fix) task(id string, files map[string]string, owns ...string) string {
	f.t.Helper()
	dir, branch := filepath.Join(f.work, id), "whaleshark/r1-"+id
	f.git(f.root, "worktree", "add", "-q", "-b", branch, dir, "whaleshark/r1")
	if files != nil {
		testkit.Commit(f.t, dir, "the work of "+id, files)
	}
	f.change(func(s *contract.State) {
		s.Tasks[id] = &contract.Task{ID: id, Name: "page " + id, Title: "the " + id + " page", Status: contract.TaskRunning, Owns: owns,
			Check: "exit 0", Attempts: []string{id + ".1"}, Worktree: &contract.Worktree{Path: dir, Branch: branch}}
		s.Attempts[id+".1"] = &contract.Attempt{ID: id + ".1", Task: id, N: 1, State: contract.AttemptWorking}
	})
	return dir
}

// collect moves the run's branch on by one commit of the files given, as an
// accepted task would.
func (f *fix) collect(files map[string]string) {
	f.t.Helper()
	dir := filepath.Join(f.work, "collected")
	f.git(f.root, "worktree", "add", "-q", "--detach", dir, "whaleshark/r1")
	f.git(f.root, "update-ref", "refs/heads/whaleshark/r1", testkit.Commit(f.t, dir, "collected", files))
}

// frozen is everything a scan must leave as it was: the store's objects,
// every branch, and each folder's files and index.
func (f *fix) frozen() string {
	all := f.git(f.root, "count-objects", "-v") + f.git(f.root, "for-each-ref") + f.git(f.root, "stash", "list")
	for _, line := range strings.Split(f.git(f.root, "worktree", "list", "--porcelain"), "\n") {
		if dir, ok := strings.CutPrefix(line, "worktree "); ok {
			index, _ := os.ReadFile(f.git(dir, "rev-parse", "--path-format=absolute", "--git-path", "index"))
			all += f.git(dir, "status", "--porcelain", "--untracked-files=all") + string(index)
		}
	}
	return all
}

// scan scans, holds the scan to having changed nothing, and returns each
// finding as one line.
func (f *fix) scan(deep bool, tasks ...string) []string {
	f.t.Helper()
	before := f.frozen()
	found, err := f.o.Scan(context.Background(), f.root, f.state(), tasks, deep)
	must(f.t, err)
	if f.frozen() != before {
		f.t.Fatal("the scan changed the repository or a task's folder")
	}
	return lines(found)
}

func lines(found []contract.Finding) []string {
	out := []string{}
	for _, f := range found {
		out = append(out, fmt.Sprintf("%s %s (%s) %s", f.Kind, strings.Join(f.Tasks, "+"), f.How, strings.Join(f.Files, " ")))
	}
	return out
}

func same(t testing.TB, what string, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s:\n got  %q\n want %q", what, got, want)
	}
}

// alpha is a file of thirty lines, with the lines given replaced.
func alpha(changed ...int) string {
	var b strings.Builder
	for n := 1; n <= 30; n++ {
		if slices.Contains(changed, n) {
			fmt.Fprintf(&b, "line %d, changed with %v\n", n, changed)
		} else {
			fmt.Fprintf(&b, "line %d\n", n)
		}
	}
	return b.String()
}

var base = map[string]string{"alpha.txt": alpha(), "beta.txt": alpha(), "picture.bin": "\x00\x01 one"}

// The experiment's eleven scenarios, a to k, and letter-case twins.
func TestScenarios(t *testing.T) {
	type files = map[string]string
	folds := "overlap T1+T2 (same file on this disk) NOTES.md Notes.md"
	for _, sc := range []struct {
		name  string
		tasks []files
		want  []string
	}{
		{"a different files", []files{{"alpha.txt": alpha(2)}, {"beta.txt": alpha(2)}}, nil},
		{"b one file, far apart", []files{{"alpha.txt": alpha(2)}, {"alpha.txt": alpha(28)}}, []string{"same T1+T2 (merges cleanly) alpha.txt"}},
		{"c one line", []files{{"alpha.txt": alpha(10)}, {"alpha.txt": alpha(10, 11)}}, []string{"overlap T1+T2 (content) alpha.txt"}},
		{"d renamed and edited", []files{{"alpha.txt": "", "renamed.txt": alpha()}, {"alpha.txt": alpha(2)}},
			[]string{"same T1+T2 (merges cleanly; T1 moved alpha.txt to renamed.txt) alpha.txt"}},
		{"e deleted and edited", []files{{"alpha.txt": ""}, {"alpha.txt": alpha(2)}}, []string{"overlap T1+T2 (modify/delete) alpha.txt"}},
		{"f both add", []files{{"helper.txt": "one\n"}, {"helper.txt": "two\n"}}, []string{"overlap T1+T2 (add/add) helper.txt"}},
		{"g three", []files{{"alpha.txt": alpha(10)}, {"alpha.txt": alpha(10, 11), "beta.txt": alpha(2)}, {"beta.txt": alpha(28)}},
			[]string{"overlap T1+T2 (content) alpha.txt", "same T2+T3 (merges cleanly) beta.txt"}},
		{"j binary", []files{{"picture.bin": "\x00\x02 two"}, {"picture.bin": "\x00\x03 three"}}, []string{"overlap T1+T2 (binary) picture.bin"}},
		{"k a file and a folder", []files{{"docs": "a file\n"}, {"docs/intro.txt": "in a folder\n"}}, []string{"overlap T1+T2 (file/directory) docs"}},
		{"letter case", []files{{"Notes.md": "one\n"}, {"NOTES.md": "two\n"}}, []string{folds}},
	} {
		t.Run(sc.name, func(t *testing.T) {
			f := start(t, base)
			for i, files := range sc.tasks {
				f.task(fmt.Sprint("T", i+1), files)
			}
			if sc.want != nil && sc.want[0] == folds && f.k.Platform.PathKey("A") != f.k.Platform.PathKey("a") {
				sc.want = nil // where letter case tells files apart, these are two
			}
			same(t, "found", f.scan(false), sc.want...)
			same(t, "found by the deep scan", f.scan(true), sc.want...)
		})
	}

	// h: work not committed is a note, and a clash only for the deep scan.
	t.Run("h not committed", func(t *testing.T) {
		f := start(t, base)
		f.task("T1", map[string]string{"alpha.txt": alpha(10)})
		dir := f.task("T2", nil)
		must(t, os.WriteFile(filepath.Join(dir, "alpha.txt"), []byte(alpha(10, 11)), 0o600))
		must(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o600))
		note := "same T1+T2 (merges cleanly; T2 has it not committed) alpha.txt"
		same(t, "found", f.scan(false), note)
		clash := "overlap T1+T2 (content, not committed) alpha.txt"
		same(t, "found by the deep scan", f.scan(true), clash)
		// Once the run has it, a scan that cannot see it does not lose it.
		found, _ := f.o.Scan(context.Background(), f.root, f.state(), nil, true)
		f.change(func(s *contract.State) { f.k.Rules.Found(s, found, time.Now()) })
		same(t, "found after the deep scan", f.scan(false), note, clash)
		f.git(dir, "checkout", "--", "alpha.txt")
		same(t, "found once the work is gone", f.scan(false))
	})

	// i: the collected work moves under a task.
	t.Run("i the base moved", func(t *testing.T) {
		f := start(t, base)
		f.task("T1", map[string]string{"alpha.txt": alpha(10)})
		f.task("T2", map[string]string{"beta.txt": alpha(10)})
		f.collect(map[string]string{"alpha.txt": alpha(10, 12)})
		same(t, "found", f.scan(false), "lands T1 (content) alpha.txt")
	})
}

// Only a pair in which a branch moved is merged again, and tasks named are
// looked at alone while the run's other findings stay.
func TestMovedAndNamed(t *testing.T) {
	f := start(t, base)
	one := f.task("T1", map[string]string{"alpha.txt": alpha(10)})
	f.task("T2", map[string]string{"alpha.txt": alpha(10, 11)})
	f.task("T3", map[string]string{"beta.txt": alpha(10)})
	made := func() int {
		g, err := open(context.Background(), f.k, f.root, f.state())
		must(t, err)
		return g.seen.Made
	}
	clash := "overlap T1+T2 (content) alpha.txt"
	same(t, "found", f.scan(false), clash)
	if n := made(); n != 6 {
		t.Fatalf("three tasks are three pairs and three landings, but %d merges were made", n)
	}
	f.scan(false)
	if n := made(); n != 6 {
		t.Fatalf("a scan with nothing moved made %d merges", n-6)
	}
	testkit.Commit(t, one, "more", map[string]string{"beta.txt": alpha(10, 11)})
	f.change(func(s *contract.State) {
		s.Run.Findings = []contract.Finding{{Kind: "overlap", Tasks: []string{"T1", "T2"}, Files: []string{"alpha.txt"}, How: "content"}}
	})
	same(t, "found for T3 alone", f.scan(false, "T3"), "overlap T3+T1 (content) beta.txt", clash)
	if n := made(); n != 7 {
		t.Fatalf("T3 alone is one pair that moved, but %d merges were made", n-6)
	}
	same(t, "found", f.scan(false), "overlap T1+T2 (content) alpha.txt", "overlap T3+T1 (content) beta.txt")
	if n := made(); n != 9 {
		t.Fatalf("one moved branch of three is two pairs and a landing, one pair known, but %d merges were made", n-7)
	}
	if _, err := f.o.Scan(context.Background(), f.root, f.state(), []string{"T9"}, false); err == nil {
		t.Fatal("a task that is not there was scanned")
	}
}

// A git without the batch gets one process a pair and the same answers; one
// that cannot simulate a merge still says which files two tasks share.
func TestOlderGit(t *testing.T) {
	f := start(t, base)
	f.task("T1", map[string]string{"alpha.txt": alpha(10), "docs": "a file\n"})
	f.task("T2", map[string]string{"alpha.txt": alpha(10, 11), "beta.txt": alpha(2), "docs/intro.txt": "in a folder\n"})
	f.task("T3", map[string]string{"beta.txt": alpha(28)})
	want := f.scan(false)
	wipe := func() {
		g, err := open(context.Background(), f.k, f.root, f.state())
		must(t, err)
		g.seen.Pairs = nil
		g.save(nil)
	}
	defer func(b string, s []string) { batch, simulation = b, s }(batch, simulation)
	wipe()
	batch = "--no-such-option"
	same(t, "found one pair at a time", f.scan(false), want...)
	wipe()
	simulation = []string{"merge-tree", "--no-such-option"}
	same(t, "found by a git that cannot merge", f.scan(false), "same T1+T2 (merge not checked) alpha.txt", "same T2+T3 (merge not checked) beta.txt")
}

// A scan that is cut off says so, and a merge whose text ends early is not
// taken for a whole one.
func TestCutOff(t *testing.T) {
	f := start(t, base)
	f.task("T1", map[string]string{"alpha.txt": alpha(10)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.o.Scan(ctx, f.root, f.state(), nil, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("a scan that was cut off ended with %v", err)
	}
	tok := strings.Split("1\x00tree\x00\x00\x000\x00tree\x00a.txt\x00\x001\x00a.txt\x00CONFLICT (contents)\x00CONFLICT (add/add): both\n\x00\x00", "\x00")
	tok = tok[:len(tok)-1]
	m, next, whole := parse(tok, 2)
	if !whole || next != 4 || m.Clashes != nil {
		t.Fatalf("a clean merge read as %+v, next %d, whole %v", m, next, whole)
	}
	if m, _, whole = parse(tok, 6); !whole || !slices.Equal(m.Clashes["add/add"], []string{"a.txt"}) {
		t.Fatalf("a clash read as %+v, whole %v", m, whole)
	}
	for end := 7; end < len(tok); end++ {
		if _, _, whole = parse(tok[:end], 6); whole {
			t.Fatalf("a merge cut after %d of %d parts was taken for a whole one", end, len(tok))
		}
	}
}

// A change outside what a task owns, and one in the project's own folder.
func TestScope(t *testing.T) {
	for file, patterns := range map[string][]string{"src/auth/login.go": {"src/auth/**"}, "docs/a/b.md": {"docs/"}, "x/notes.md": {"*.md"},
		"src/a.go": {"lib", "src/*.go"}, "README": {"README"}, "any/thing": {"."}} {
		if !owned(patterns, file) {
			t.Errorf("%v does not cover %s", patterns, file)
		}
	}
	if owned([]string{"src/auth/**", "docs", "*.md"}, "src/authority/a.go") {
		t.Error("a folder's pattern covers a folder whose name only begins like it")
	}
	f := start(t, base)
	f.task("T1", map[string]string{"alpha.txt": alpha(2), "docs/intro.txt": "new\n"}, "docs/**")
	f.task("T2", map[string]string{"beta.txt": alpha(2)}, "beta.txt")
	must(t, os.WriteFile(filepath.Join(f.root, "beta.txt"), []byte("edited where no worker should be\n"), 0o600))
	same(t, "found", f.scan(false), "scope T1 (outside what it owns) alpha.txt", "scope  (in the project's own folder while workers run) beta.txt")
	f.change(func(s *contract.State) {
		s.Tasks["T4"] = &contract.Task{ID: "T4", Name: "shared", Title: "in the project's folder", Status: contract.TaskRunning, Check: "exit 0", Placement: contract.PlaceShared}
	})
	same(t, "found with a task at work in the project's folder", f.scan(false), "scope T1 (outside what it owns) alpha.txt")
}

func events(s *contract.State, kind string) (n int) {
	for _, e := range s.Inbox.Events {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// overlap ends with 0, 6 and 7, and a finding is announced once.
func TestOverlapCommand(t *testing.T) {
	f := start(t, base)
	f.task("T1", map[string]string{"alpha.txt": alpha(2)})
	f.task("T2", map[string]string{"alpha.txt": alpha(28)})
	if out := f.play(0, "overlap"); !strings.Contains(out, "T1 and T2 both change alpha.txt (merges cleanly)") {
		t.Fatalf("overlap printed:\n%s", out)
	}
	f.collect(map[string]string{"alpha.txt": alpha(2, 3)})
	for range 2 {
		out := f.play(contract.ExitNoLand, "overlap")
		for _, w := range []string{"T1 no longer lands on the collected work (content): alpha.txt", "Next: whaleshark sync T1"} {
			if !strings.Contains(out, w) {
				t.Fatalf("overlap printed no %q:\n%s", w, out)
			}
		}
	}
	f.task("T3", map[string]string{"alpha.txt": alpha(28, 29)})
	for range 2 {
		out := f.play(contract.ExitClash, "overlap")
		for _, w := range []string{"T2 and T3 would conflict (content): alpha.txt", "Next: whaleshark sync T3", "Next: whaleshark sync T1"} {
			if !strings.Contains(out, w) {
				t.Fatalf("overlap printed no %q:\n%s", w, out)
			}
		}
	}
	if out := f.play(contract.ExitNoLand, "overlap", "page T1"); strings.Contains(out, "T2 and T3") {
		t.Fatalf("overlap for one task printed another's:\n%s", out)
	}
	var got struct {
		Error struct {
			Code string
			Data scanned
		}
	}
	must(t, json.Unmarshal([]byte(f.play(contract.ExitClash, "overlap", "--json")), &got))
	if got.Error.Code != "clash" || len(got.Error.Data.Findings) < 2 || got.Error.Data.New != 0 {
		t.Fatalf("overlap --json answered %+v", got)
	}
	// T1 stopped landing, then T2 and T3 clashed: each told once.
	s := f.state()
	if n := events(s, "overlap"); n != 2 || len(s.Run.Findings) < 3 || s.Run.Scan.Due {
		t.Fatalf("after seven scans the run has %d overlap events and the findings %v", n, lines(s.Run.Findings))
	}
	cmd := f.p.Command(scenario.Unbound, "overlap", "--run", "r1")
	if out, _ := cmd.CombinedOutput(); cmd.ProcessState.ExitCode() != contract.ExitClash {
		t.Fatalf("overlap from a pane of nobody's: exit %d\n%s", cmd.ProcessState.ExitCode(), out)
	}
}

// sync counts, keeps the brief, tells a live worker and prints for a dead one.
func TestSync(t *testing.T) {
	f := start(t, base)
	f.task("T1", map[string]string{"alpha.txt": alpha(10), "helper.txt": "one\n"})
	f.change(func(s *contract.State) {
		s.Tasks["T4"] = &contract.Task{ID: "T4", Name: "shared", Title: "in the shared folder", Status: contract.TaskRunning, Check: "exit 0", Placement: contract.PlaceShared}
	})
	f.play(contract.ExitUsage, "sync")
	f.play(contract.ExitMissing, "sync", "T9")
	if out := f.play(contract.ExitRefused, "sync", "T4"); !strings.Contains(out, "no copy of the code") {
		t.Fatalf("sync of a shared task printed:\n%s", out)
	}
	if out := f.play(contract.ExitRefused, "sync", "T1"); !strings.Contains(out, "already holds") {
		t.Fatalf("sync of a task that is up to date printed:\n%s", out)
	}
	f.collect(map[string]string{"alpha.txt": alpha(10, 12), "helper.txt": "two\n"})
	out := f.play(0, "sync", "T1")
	s := f.state()
	brief := filepath.Join(f.k.Store.Dir(f.root, "r1"), "msg", "sync-T1.md")
	text, err := os.ReadFile(brief)
	must(t, err)
	mail := s.Attempts["T1.1"].Mail
	if t1 := s.Tasks["T1"]; t1.Synced != 1 || t1.SyncBrief != "msg/sync-T1.md" || len(mail) != 1 || mail[0].Body != brief ||
		!strings.Contains(out, "Sent to T1 as message") {
		t.Fatalf("after sync: synced %d, brief %q, mail %+v, printed:\n%s", t1.Synced, t1.SyncBrief, mail, out)
	}
	// The words are the guide's; held here are the facts the scan gave it.
	behind := regexp.MustCompile(`(?m)^Commits behind:\s+1$`)
	if !behind.Match(text) {
		t.Fatalf("the brief does not say one commit behind:\n%s", text)
	}
	for _, w := range []string{"whaleshark/r1", "add/add", "helper.txt", "content", "alpha.txt"} {
		if !strings.Contains(string(text), w) {
			t.Fatalf("the brief lacks %q:\n%s", w, text)
		}
	}
	f.change(func(s *contract.State) { s.Attempts["T1.1"].State = contract.AttemptExited })
	out = f.play(0, "sync", "T1")
	if s = f.state(); s.Tasks["T1"].Synced != 2 || len(s.Attempts["T1.1"].Mail) != 1 || !strings.Contains(out, "add/add") ||
		!strings.Contains(out, "No agent is alive in T1") || !strings.Contains(out, "sent to sync 2 times") {
		t.Fatalf("sync with no agent alive: synced %d, printed:\n%s", s.Tasks["T1"].Synced, out)
	}
}

// Twenty tasks, each thirty files of a large repository, three of them from
// forty that all draw on: the scan's time, first and with nothing moved.
func TestTwenty(t *testing.T) {
	if *files == 0 {
		t.Skip("a measurement: run with -files 2000")
	}
	tree := map[string]string{}
	for n := range *files {
		tree[fmt.Sprintf("dir%02d/file%05d.txt", n%40, n)] = alpha()
	}
	f := start(t, tree)
	name := func(n int) string { return fmt.Sprintf("dir%02d/file%05d.txt", n%40, n) }
	for i := range 20 {
		change := map[string]string{}
		for j := range 27 {
			change[name((i*97+j*31+40)%*files)] = alpha(i%30 + 1)
		}
		for j := range 3 {
			change[name((i*7+j*13)%40)] = alpha((i+j)%30 + 1)
		}
		f.task(fmt.Sprint("T", i+1), change)
	}
	scan := func(deep bool) ([]contract.Finding, time.Duration) {
		began := time.Now()
		found, err := f.o.Scan(context.Background(), f.root, f.state(), nil, deep)
		must(t, err)
		return found, time.Since(began)
	}
	objects := f.git(f.root, "count-objects", "-v")
	found, first := scan(false)
	_, again := scan(false)
	_, deep := scan(true)
	t.Logf("%d files, 20 tasks, 190 pairs, %d findings: first scan %v, scan with nothing moved %v, deep scan %v", *files, len(found), first, again, deep)
	if after := f.git(f.root, "count-objects", "-v"); after != objects {
		t.Fatalf("the scans left objects in the repository: before\n%s\nafter\n%s", objects, after)
	}
	if *files == 2000 && first > 3*time.Second {
		t.Fatalf("the first scan of twenty tasks took %v", first)
	}
}
