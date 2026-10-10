package integrate_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/gitwt"
	"github.com/tgdigital-hub/whaleshark/internal/integrate"
	"github.com/tgdigital-hub/whaleshark/internal/rules"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func TestMain(m *testing.M) { scenario.Main(m) }

// run is a made-up project that is a real repository, with one open run
// whose branch was begun: what run new and start will have done.
type run struct {
	t    *testing.T
	p    *scenario.Project
	k    *contract.Kit
	root string
	work string // where the tasks' copies of the code are
}

func open(t *testing.T, files map[string]string) *run {
	t.Helper()
	// Every git of the test, the program's own included, has the settings
	// of the test kit and nobody's own.
	for _, pair := range testkit.GitEnv()[len(os.Environ()):] {
		name, value, _ := strings.Cut(pair, "=")
		t.Setenv(name, value)
	}
	p := scenario.Prepare(t, nil, nil)
	rules.Plug(p.Kit)
	gitwt.Plug(p.Kit) // the run's copy is equipped as a task's own is
	integrate.Plug(p.Kit)
	work, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	r := &run{t: t, p: p, k: p.Kit, root: p.Root, work: work}
	testkit.Git(t, r.root, "init", "-q", "-b", "main")
	write(t, filepath.Join(r.root, ".git", "info", "exclude"), ".whaleshark\nwhaleshark.toml\n")
	testkit.Commit(t, r.root, "start", files)
	play(t, p.Command(scenario.Orch, "run", "new", "a shop"), 0)
	// run new has written down the base and begun the run's branch there.
	if s := r.state(); s.Run.Base == nil || s.Run.Base.Ref != "main" || s.Run.Integration == nil || s.Run.Integration.Tip != s.Run.Base.OID {
		t.Fatalf("after run new the record has the base %+v and the branch %+v", s.Run.Base, s.Run.Integration)
	}
	return r
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(path), 0o700))
	must(t, os.WriteFile(path, []byte(text), 0o600))
}

// play runs the real program as a caller and holds it to its exit code and
// to words it must print.
func play(t *testing.T, cmd *exec.Cmd, exit int, words ...string) string {
	t.Helper()
	out, _ := cmd.CombinedOutput()
	if got := cmd.ProcessState.ExitCode(); got != exit {
		t.Fatalf("%v: exit %d, expected %d\n%s", cmd.Args[1:], got, exit, out)
	}
	// A line to type is written for the system's shell, which on Windows
	// puts each word in single quotes: the words are looked for without them.
	plain := strings.ReplaceAll(string(out), "'", "")
	for _, w := range words {
		if !strings.Contains(string(out), w) && !strings.Contains(plain, w) {
			t.Fatalf("%v: expected %q in what it printed:\n%s", cmd.Args[1:], w, out)
		}
	}
	return string(out)
}

func (r *run) state() *contract.State {
	s, _ := r.p.Record()
	return s
}

func (r *run) change(fn func(*contract.State)) {
	r.t.Helper()
	must(r.t, r.k.Store.Change(r.root, "r1", func(s *contract.State) error { fn(s); return nil }))
}

func (r *run) git(dir string, args ...string) string { return testkit.Git(r.t, dir, args...) }

// branch is where the run's branch stands in git, whatever the record says.
func (r *run) branch() string { return r.git(r.root, "rev-parse", "whaleshark/r1") }

// task gives a task its own copy of the code, cut from the collected work,
// with one commit of the files given, and leaves it waiting to be checked.
func (r *run) task(id string, files map[string]string) string {
	r.t.Helper()
	dir, branch := filepath.Join(r.work, id), "whaleshark/r1-"+id
	r.git(r.root, "worktree", "add", "-q", "-b", branch, dir, r.state().Run.Integration.Tip)
	testkit.Commit(r.t, dir, "the work of "+id, files)
	r.change(func(s *contract.State) {
		s.Tasks[id] = &contract.Task{ID: id, Name: "page " + id, Title: "the " + id + " page", Status: contract.TaskReview,
			Check: "exit 0", Attempts: []string{id + ".1"}, Worktree: &contract.Worktree{Path: dir, Branch: branch}}
		s.Attempts[id+".1"] = &contract.Attempt{ID: id + ".1", Task: id, N: 1, State: contract.AttemptReported,
			Report: &contract.Report{Outcome: contract.ReportDone}}
	})
	return dir
}

// record writes down a collected commit, as Rules.Checked does for accept.
func (r *run) record(id, made string) {
	r.change(func(s *contract.State) {
		s.Run.Integration.Tip, s.Tasks[id].Status = made, contract.TaskDone
		s.Tasks[id].Accepted = &contract.Accepted{How: contract.AcceptCheck, Commit: made}
	})
}

var errCheck = errors.New("the check failed")

// accept is the steps of the command for one task: prepare, run the check
// where Prepare says, collect, and write the commit down.
func (r *run) accept(id string, check func(dir string) bool) (string, error) {
	c, err := r.k.Integrator.Prepare(r.root, r.state(), id)
	if err != nil {
		return "", err
	}
	if check != nil && !check(c.Dir) {
		return "", errCheck
	}
	made, err := r.k.Integrator.Collect(r.root, r.state(), id, c)
	if err == nil {
		r.record(id, made)
	}
	return made, err
}

// code is the code of a refusal, or the error's own text.
func code(err error) string {
	var r *contract.Refusal
	switch {
	case err == nil:
		return ""
	case errors.As(err, &r):
		return r.Code
	}
	return err.Error()
}

func has(dir, file string) bool {
	_, err := os.Stat(filepath.Join(dir, file))
	return err == nil
}

// The collect step alone: the checked tree becomes one commit on the branch
// with one parent, the tip, and git refuses to move a branch that moved.
func TestCollect(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n"})
	base := r.branch()
	dir := r.task("A", map[string]string{"a.txt": "two\n", "new.txt": "new\n"})
	checked := contract.Checked{Dir: dir, OID: r.git(dir, "rev-parse", "HEAD"), Tip: base}

	made, err := r.k.Integrator.Collect(r.root, r.state(), "A", checked)
	must(t, err)
	if r.branch() != made || r.git(r.root, "rev-parse", made+"^") != base || r.git(r.root, "rev-list", "--count", base+".."+made) != "1" {
		t.Fatalf("the branch is at %s, not at one commit %s on top of %s", r.branch(), made, base)
	}
	if r.git(r.root, "rev-parse", made+"^{tree}") != r.git(r.root, "rev-parse", checked.OID+"^{tree}") {
		t.Fatal("the collected tree is not the tree that was checked")
	}
	if got := r.git(r.root, "log", "-1", "--format=%s|%(trailers:key=Whaleshark-Task,valueonly,separator=)", made); got != "A: the A page|r1/A" {
		t.Fatalf("the commit says %q", got)
	}
	if r.git(r.root, "status", "--porcelain", "--untracked-files=no") != "" || r.git(r.root, "rev-parse", "main") != base {
		t.Fatal("collecting touched the project's own checkout")
	}

	// A second task checked on the old tip is not collected over the first.
	other := r.task("B", map[string]string{"b.txt": "b\n"})
	stale := contract.Checked{Dir: other, OID: r.git(other, "rev-parse", "HEAD"), Tip: base}
	if _, err := r.k.Integrator.Collect(r.root, r.state(), "B", stale); code(err) != "tip_moved" || r.branch() != made {
		t.Fatalf("a moved tip: %v, branch at %s", err, r.branch())
	}
	// Nothing checked means nothing collected: a task in a shared folder.
	if made, err := r.k.Integrator.Collect(r.root, r.state(), "B", contract.Checked{}); made != "" || err != nil {
		t.Fatalf("nothing to collect gave %q, %v", made, err)
	}
}

func TestBegin(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n"})
	s := r.state()
	if in := s.Run.Integration; in.Branch != "whaleshark/r1" || in.Tip != r.git(r.root, "rev-parse", "main") || r.branch() != in.Tip {
		t.Fatalf("the branch begins at %+v", in)
	}
	// Begun again, as after a run new that was cut off, it is the same branch.
	if _, again, err := r.k.Integrator.Begin(r.root, s); err != nil || *again != *s.Run.Integration {
		t.Fatalf("begun again: %+v, %v", again, err)
	}
	old := testkit.Commit(t, r.root, "more", map[string]string{"c.txt": "c\n"})
	s.Run.Base = nil
	if _, _, err := r.k.Integrator.Begin(r.root, s); code(err) != "branch_exists" {
		t.Fatalf("a branch of that name somewhere else: %v", err)
	}
	// A run can begin at a commit that is named.
	s.Run.ID, s.Run.Base = "r2", &contract.GitRef{Ref: "main~1"}
	if _, in, err := r.k.Integrator.Begin(r.root, s); err != nil || in.Tip == old || in.Tip != r.git(r.root, "rev-parse", "main~1") {
		t.Fatalf("a named base: %+v, %v", in, err)
	}
	if _, err := integrate.Base(r.root, "no-such"); code(err) != "no_base" {
		t.Fatalf("a base that does not exist: %v", err)
	}
	// Without git there is no branch, and that is no error.
	s.Run.Base = nil
	if _, in, err := r.k.Integrator.Begin(t.TempDir(), s); in != nil || err != nil {
		t.Fatalf("no git: %+v, %v", in, err)
	}
}

// A clean merge: the second task is checked with the first one's work in its
// folder, and each is one commit.
func TestCleanMerge(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n", "b.txt": "one\n"})
	r.task("A", map[string]string{"a.txt": "two\n"})
	dir := r.task("B", map[string]string{"b.txt": "two\n"})
	first, err := r.accept("A", nil)
	must(t, err)
	second, err := r.accept("B", func(dir string) bool {
		a, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
		return string(a) == "two\n"
	})
	if err != nil {
		t.Fatalf("B was checked without the work of A in its folder: %v", err)
	}
	if r.git(r.root, "rev-parse", second+"^") != first || r.git(r.root, "diff", "--name-only", first, second) != "b.txt" {
		t.Fatal("B is not one commit of its own file on top of A")
	}
	if r.git(dir, "status", "--porcelain") != "" || r.git(dir, "rev-parse", "HEAD^{tree}") != r.git(r.root, "rev-parse", second+"^{tree}") {
		t.Fatal("what was collected is not what stands in B's folder")
	}
}

func TestConflictIsRefusedWithItsFiles(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n", "b.txt": "one\n"})
	r.task("A", map[string]string{"a.txt": "from A\n", "b.txt": "from A\n"})
	dir := r.task("B", map[string]string{"a.txt": "from B\n", "c.txt": "c\n"})
	first, err := r.accept("A", nil)
	must(t, err)
	head := r.git(dir, "rev-parse", "HEAD")

	_, err = r.accept("B", nil)
	var refusal *contract.Refusal
	if !errors.As(err, &refusal) || refusal.Code != "conflict" || refusal.Exit != contract.ExitRefused {
		t.Fatalf("a conflict: %v", err)
	}
	if !strings.Contains(refusal.Message, "a.txt") || strings.Contains(refusal.Message, "b.txt") || refusal.Next[0] != "whaleshark sync B" {
		t.Fatalf("the refusal names %q, next %v", refusal.Message, refusal.Next)
	}
	if r.branch() != first || r.git(dir, "rev-parse", "HEAD") != head || r.git(dir, "status", "--porcelain") != "" {
		t.Fatal("a refused merge touched the branch or the worker's folder")
	}
}

// Two tasks that merge cleanly and break each other: the check of the second
// runs on the merged tree, so it fails and nothing of it is collected.
func TestCheckRunsOnTheMergedTree(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n"})
	r.task("A", map[string]string{"renamed.txt": "one\n", "a.txt": ""})
	dir := r.task("B", map[string]string{"uses-a.txt": "a.txt\n"})
	needsA := func(dir string) bool { return has(dir, "a.txt") }
	if !needsA(dir) {
		t.Fatal("B's check does not pass alone")
	}
	first, err := r.accept("A", nil)
	must(t, err)
	if _, err := r.accept("B", needsA); err != errCheck || r.branch() != first {
		t.Fatalf("B was collected though its check fails with A: %v", err)
	}
}

func TestWorkNotCommittedIsRefused(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n"})
	dir := r.task("A", map[string]string{"a.txt": "two\n"})
	write(t, filepath.Join(dir, "left.txt"), "not committed\n")
	if _, err := r.accept("A", nil); code(err) != "uncommitted" {
		t.Fatalf("a file git does not know: %v", err)
	}
}

// The worker commits, or changes a file, while its work is being checked.
func TestWorkerMovesDuringTheCheck(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n"})
	r.task("A", map[string]string{"a.txt": "two\n"})
	base := r.branch()
	_, err := r.accept("A", func(dir string) bool {
		testkit.Commit(t, dir, "one more thing", map[string]string{"late.txt": "late\n"})
		return true
	})
	if code(err) != "moved" || r.branch() != base {
		t.Fatalf("a commit during the check: %v", err)
	}
	_, err = r.accept("A", func(dir string) bool {
		write(t, filepath.Join(dir, "a.txt"), "three\n")
		return true
	})
	if code(err) != "moved" || r.branch() != base {
		t.Fatalf("a changed file during the check: %v", err)
	}
}

// The command itself, as it is built today, on the calls of this package: a
// task is collected and the record moves, and a check that commits in the
// folder it runs in, as a worker might meanwhile, collects nothing.
func TestAcceptCommand(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n"})
	r.task("A", map[string]string{"a.txt": "two\n"})
	dir := r.task("B", map[string]string{"b.txt": "b\n"})
	r.change(func(s *contract.State) { s.Tasks["B"].Check = "git commit -q --allow-empty -m late" })

	play(t, r.p.Command(scenario.Orch, "accept", "A"), 0, "A is done, checked")
	s := r.state()
	if tip := r.branch(); s.Run.Integration.Tip != tip || s.Tasks["A"].Accepted.Commit != tip || r.git(r.root, "show", "-s", "--format=%s", tip) != "A: the A page" {
		t.Fatalf("after accept the record has %+v and the branch is at %s", s.Run.Integration, r.branch())
	}
	play(t, r.p.Command(scenario.Orch, "accept", "B"), 5, "changed since", "Nothing was collected")
	if s := r.state(); r.branch() != s.Run.Integration.Tip || s.Attempts["B.1"].State != contract.AttemptReported || !has(dir, "a.txt") {
		t.Fatalf("after a refused accept B's attempt is %s", s.Attempts["B.1"].State)
	}
}

// The accept is killed after the branch moved and before the record was
// written: the next one finds the commit and makes no second.
func TestKilledBetweenCommitAndRecord(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n", "b.txt": "one\n"})
	r.task("A", map[string]string{"a.txt": "two\n"})
	r.task("B", map[string]string{"b.txt": "two\n"})
	r.task("C", map[string]string{"c.txt": "c\n"})
	_, err := r.accept("A", nil)
	must(t, err)
	base := r.branch()

	c, err := r.k.Integrator.Prepare(r.root, r.state(), "B")
	must(t, err)
	made, err := r.k.Integrator.Collect(r.root, r.state(), "B", c)
	must(t, err)
	// Killed here. The record still has the old tip.
	if s := r.state(); s.Run.Integration.Tip != base || r.branch() != made {
		t.Fatal("the test did not stop between the commit and the record")
	}
	// Another task is accepted first: it is built on the commit already there.
	third, err := r.accept("C", nil)
	if err != nil || r.git(r.root, "rev-parse", third+"^") != made {
		t.Fatalf("C after the kill: %v", err)
	}
	again, err := r.accept("B", nil)
	if err != nil || again != made || r.branch() != third || r.git(r.root, "rev-list", "--count", base+".."+third) != "2" {
		t.Fatalf("B accepted again gave %s, %v; the branch has %s commits", again, err, r.git(r.root, "rev-list", "--count", base+".."+r.branch()))
	}

	// A branch somebody moved by hand is not built on.
	r.record("B", third)
	r.task("D", map[string]string{"d.txt": "d\n"})
	r.git(r.root, "branch", "-f", "whaleshark/r1", "main")
	if _, err := r.accept("D", nil); code(err) != "branch_moved" {
		t.Fatalf("a branch moved by hand: %v", err)
	}
}

// approve writes the project's settings and approves them, as trust does.
func (r *run) approve(toml string) {
	r.t.Helper()
	write(r.t, filepath.Join(r.root, "whaleshark.toml"), toml)
	lines, err := contract.TrustLines(r.root)
	must(r.t, err)
	must(r.t, contract.WriteTrust(r.k.Platform, r.root, lines, contract.Now()))
}

// Several at once: five tasks merged in order in the run's own copy, which
// is set up once, and collected as five commits; a clash names its task.
func TestPrepareAll(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n", ".gitignore": "made\n"})
	r.approve("[setup]\nscript = \"echo once>>made\"\nscript_windows = \"echo once>>made\"\n")
	ids := []string{"A", "B", "C", "D", "E"}
	for _, id := range ids {
		r.task(id, map[string]string{"page-" + id + ".txt": id + "\n"})
	}
	base := r.branch()
	all, err := r.k.Integrator.PrepareAll(r.root, r.state(), ids)
	if err != nil || len(all) != 5 || all[0].Tip != base {
		t.Fatalf("five prepared: %+v, %v", all, err)
	}
	dir := all[0].Dir
	dirs, _ := r.k.Platform.Dirs()
	if !strings.HasPrefix(dir, dirs.Cache) || r.git(dir, "rev-parse", "HEAD") != all[4].OID || r.branch() != base {
		t.Fatalf("the copy is %s at %s", dir, r.git(dir, "rev-parse", "HEAD"))
	}
	for i, id := range ids {
		if all[i].Dir != dir || !has(dir, "page-"+id+".txt") {
			t.Fatalf("%s is not in the run's copy", id)
		}
	}
	tip := base
	for i, id := range ids {
		all[i].Tip = tip
		made, err := r.k.Integrator.Collect(r.root, r.state(), id, all[i])
		must(t, err)
		if r.git(r.root, "rev-parse", made+"^") != tip || r.git(r.root, "diff", "--name-only", tip, made) != "page-"+id+".txt" {
			t.Fatalf("%s is not one commit of its own file", id)
		}
		r.record(id, made)
		tip = made
	}
	if r.git(r.root, "rev-parse", tip+"^{tree}") != r.git(r.root, "rev-parse", all[4].OID+"^{tree}") {
		t.Fatal("what was collected is not what stood in the copy")
	}

	// A sixth and a seventh clash with each other: the second is named, and
	// the copy was set up once, however often it was used.
	r.task("F", map[string]string{"a.txt": "from F\n"})
	r.task("G", map[string]string{"a.txt": "from G\n"})
	_, err = r.k.Integrator.PrepareAll(r.root, r.state(), []string{"F", "G"})
	var refusal *contract.Refusal
	if !errors.As(err, &refusal) || refusal.Code != "conflict" || !strings.Contains(refusal.Message, "G does not merge") || r.branch() != tip {
		t.Fatalf("a clash in a batch: %v", err)
	}
	one, err := r.k.Integrator.PrepareAll(r.root, r.state(), []string{"F"})
	must(t, err)
	if made, _ := os.ReadFile(filepath.Join(dir, "made")); strings.Count(string(made), "once") != 1 || one[0].Dir != dir {
		t.Fatalf("the copy was set up %q", made)
	}
	// A copy that somebody cleared away is made and set up again.
	must(t, os.RemoveAll(dir))
	if _, err := r.k.Integrator.PrepareAll(r.root, r.state(), []string{"F"}); err != nil || !has(dir, "made") || !has(dir, "a.txt") {
		t.Fatalf("the copy was not made again: %v", err)
	}
	// A task with no copy of its own cannot ride along.
	r.change(func(s *contract.State) {
		s.Tasks["H"] = &contract.Task{ID: "H", Name: "page H", Placement: contract.PlaceShared}
	})
	if _, err := r.k.Integrator.PrepareAll(r.root, r.state(), []string{"F", "H"}); code(err) != "needs_worktree" {
		t.Fatalf("a shared task in a batch: %v", err)
	}
}

func TestDiffAndDrop(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n"})
	dir := r.task("A", map[string]string{"a.txt": "two\n"})
	write(t, filepath.Join(dir, "a.txt"), "three\n")
	diff, err := r.k.Integrator.Diff(r.root, r.state(), "A", false)
	if err != nil || !strings.Contains(diff, "-one") || !strings.Contains(diff, "+three") {
		t.Fatalf("the diff of work in progress: %q, %v", diff, err)
	}
	r.git(dir, "checkout", "-q", "a.txt")
	_, err = r.accept("A", nil)
	must(t, err)
	stat, err := r.k.Integrator.Diff(r.root, r.state(), "A", true)
	if err != nil || !strings.Contains(stat, "a.txt") || strings.Contains(stat, "+two") {
		t.Fatalf("the short diff of collected work: %q, %v", stat, err)
	}
	if _, err := r.k.Integrator.Diff(r.root, r.state(), "Z", false); code(err) != "no_task" {
		t.Fatalf("no such task: %v", err)
	}

	// The copy goes with the run; the branch only once it was landed.
	all, err := r.k.Integrator.PrepareAll(r.root, r.state(), []string{"A"})
	must(t, err)
	r.change(func(s *contract.State) { s.Run.ClosedAt = contract.Now() })
	must(t, r.k.Integrator.Drop(r.root, r.state()))
	if has(all[0].Dir, "") || strings.Contains(r.git(r.root, "worktree", "list"), all[0].Dir) || r.branch() == "" {
		t.Fatal("dropping a run that was not landed keeps its branch and removes its copy")
	}
	r.change(func(s *contract.State) { s.Run.Integration.Landed = s.Run.Integration.Tip })
	must(t, r.k.Integrator.Drop(r.root, r.state()))
	if out := r.git(r.root, "branch", "--list", "whaleshark/r1"); out != "" {
		t.Fatalf("the landed branch is still there: %q", out)
	}
}
