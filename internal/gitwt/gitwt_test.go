package gitwt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
)

// desk is one project of a pretended login: a real repository, the real
// platform, and a run with nothing in it yet.
type desk struct {
	*testing.T
	tr   *trees
	root string
	s    *contract.State
	said *strings.Builder
}

func project(t *testing.T, files map[string]string) *desk {
	t.Helper()
	for _, pair := range testkit.GitEnv() {
		if name, value, _ := strings.Cut(pair, "="); strings.HasPrefix(name, "GIT_") {
			t.Setenv(name, value)
		}
	}
	home := t.TempDir()
	for _, name := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(name, filepath.Join(home, name))
	}
	t.Setenv("XDG_CACHE_HOME", "")
	k := contract.NewKit()
	platform.Plug(k)
	Plug(k)
	d := &desk{T: t, tr: k.Placement.(*trees), root: testkit.Repo(t, files), said: new(strings.Builder)}
	d.tr.warn = d.said
	d.write(".git/info/exclude", contract.ProjectDir+"/\n") // as init does
	d.s = &contract.State{Run: contract.Run{ID: "r1"}, Tasks: map[string]*contract.Task{}}
	return d
}

func (d *desk) write(name, text string) {
	d.Helper()
	path := filepath.Join(d.root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		d.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		d.Fatal(err)
	}
}

// trust approves the project's settings as they stand.
func (d *desk) trust() {
	d.Helper()
	lines, err := contract.TrustLines(d.root)
	if err == nil {
		err = contract.WriteTrust(d.tr.k.Platform, d.root, lines, contract.Now())
	}
	if err != nil {
		d.Fatal(err)
	}
}

// place adds a task, places it and records what comes back, as start does.
func (d *desk) place(id, name string) (string, *contract.Worktree) {
	d.Helper()
	if d.s.Tasks[id] == nil {
		d.s.Tasks[id] = &contract.Task{ID: id, Name: name, Placement: contract.PlaceWorktree}
	}
	dir, w, err := d.tr.Place(d.root, d.s, id)
	if err != nil {
		d.Fatalf("place %s: %v", id, err)
	}
	d.s.Tasks[id].Worktree = w
	return dir, w
}

func (d *desk) git(args ...string) string { d.Helper(); return testkit.Git(d, d.root, args...) }

func (d *desk) has(branch string) bool {
	_, err := git(d.root, "rev-parse", "--verify", "-q", "refs/heads/"+branch)
	return err == nil
}

func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

func code(err error) string {
	var r *contract.Refusal
	if errors.As(err, &r) {
		return r.Code
	}
	return ""
}

// A new worktree lies inside the project in a folder of the login's own, on
// a branch of its own with no upstream, cut from the branch the project is
// on, with the base written into git; a second call changes nothing.
func TestPlace(t *testing.T) {
	d := project(t, map[string]string{"a.txt": "one\n"})
	head := d.git("rev-parse", "HEAD")
	dir, w := d.place("T7", "Login fix")
	holder := filepath.Join(d.root, ".whaleshark", "worktrees")
	if dir != filepath.Join(holder, "r1-T7") || w.Path != dir || !exists(filepath.Join(dir, "a.txt")) {
		t.Fatalf("placed in %s, record %+v", dir, w)
	}
	if w.Branch != "whaleshark/r1-T7-login-fix" || w.BaseRef != "main" || w.BaseOID != head || w.Setup != setupNone || w.KeepBranch || w.CreatedAt.IsZero() {
		t.Fatalf("record %+v", w)
	}
	if got := d.git("config", "branch."+w.Branch+".base"); got != "main" {
		t.Errorf("git has the base as %q", got)
	}
	if _, err := git(d.root, "config", "branch."+w.Branch+".merge"); err == nil {
		t.Error("the branch follows another")
	}
	if info, _ := os.Stat(holder); d.tr.k.Platform.System() != "windows" && info.Mode().Perm() != 0o700 {
		t.Errorf("the folder of the worktrees has mode %v", info.Mode().Perm())
	}
	if d.said.Len() > 0 {
		t.Errorf("it said %q", d.said)
	}
	testkit.Commit(t, dir, "work", map[string]string{"b.txt": "two\n"})
	tip := testkit.Git(t, dir, "rev-parse", "HEAD")
	again, same := d.place("T7", "Login fix")
	if again != dir || same != w || testkit.Git(t, dir, "rev-parse", "HEAD") != tip {
		t.Fatalf("a second call gave %s, %+v", again, same)
	}

	// A folder deleted by hand is made again on the same branch.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if again, back := d.place("T7", "Login fix"); again != dir || back.Branch != w.Branch || testkit.Git(t, dir, "rev-parse", "HEAD") != tip {
		t.Fatalf("after its folder was deleted: %s, %+v", again, back)
	}

	// A project that is one folder of its repository works in that folder
	// of the worktree.
	app := filepath.Join(d.root, "app")
	testkit.Commit(t, d.root, "an app", map[string]string{"app/main.txt": "app\n"})
	if dir, w := (&desk{T: t, tr: d.tr, root: app, s: d.s}).place("P", "part"); dir != filepath.Join(w.Path, "app") || !exists(filepath.Join(dir, "main.txt")) {
		t.Fatalf("a project inside its repository is placed in %s of %s", dir, w.Path)
	}

	// A folder that is in the way is not a worktree of ours: nothing of it is touched.
	theirs := filepath.Join(holder, "r1-X", "kept.txt")
	os.MkdirAll(filepath.Dir(theirs), 0o700)
	os.WriteFile(theirs, []byte("kept\n"), 0o600)
	d.s.Tasks["X"] = &contract.Task{ID: "X", Placement: contract.PlaceWorktree}
	if _, _, err := d.tr.Place(d.root, d.s, "X"); err == nil || !exists(theirs) || d.has("whaleshark/r1-X") {
		t.Fatalf("a folder in the way: %v", err)
	}

	// A task with no worktree, and a project with no git.
	d.s.Tasks["S"] = &contract.Task{ID: "S", Placement: contract.PlaceShared}
	d.s.Tasks["C"] = &contract.Task{ID: "C", Placement: contract.PlaceCwd + "sub"}
	for id, want := range map[string]string{"S": d.root, "C": "sub"} {
		if dir, w, err := d.tr.Place(d.root, d.s, id); dir != want || w != nil || err != nil {
			t.Errorf("%s is placed in %s, %v, %v", id, dir, w, err)
		}
	}
	bare := t.TempDir()
	if dir, w, err := d.tr.Place(bare, d.s, "T7"); dir != bare || w != nil || err != nil {
		t.Errorf("without git: %s, %v, %v", dir, w, err)
	}
	if n, _, err := d.tr.Left(bare); n != 0 || err != nil {
		t.Errorf("without git %d are left behind, %v", n, err)
	}
}

// The base is the run's collected work when there is some, else the run's
// base, else the main branch of where the project came from, fetched when
// it can be and used as it stands when it cannot.
func TestBase(t *testing.T) {
	d := project(t, map[string]string{"a.txt": "one\n"})
	far := testkit.Repo(t, map[string]string{"a.txt": "one\n"})
	d.git("remote", "add", "origin", far)
	d.git("fetch", "-q", "origin")
	d.git("remote", "set-head", "origin", "main")
	newer := testkit.Commit(t, far, "newer", map[string]string{"far.txt": "far\n"})
	if _, w := d.place("A", "from afar"); w.BaseRef != "origin/main" || w.BaseOID != newer {
		t.Fatalf("cut from %s %s, want the newest of origin/main", w.BaseRef, w.BaseOID)
	}
	d.git("remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone"))
	if _, w := d.place("B", "offline"); w.BaseRef != "origin/main" || w.BaseOID != newer {
		t.Fatalf("with nothing to fetch from: cut from %s %s", w.BaseRef, w.BaseOID)
	}
	d.git("branch", "release", "main")
	d.s.Run.Base = &contract.GitRef{Ref: "release"}
	if _, w := d.place("C", "on the base"); w.BaseRef != "release" || w.BaseOID != d.git("rev-parse", "main") {
		t.Fatalf("cut from %s %s, want the run's base", w.BaseRef, w.BaseOID)
	}
	d.git("branch", "whaleshark/r1", newer)
	d.s.Run.Integration = &contract.Integration{Branch: "whaleshark/r1"}
	if _, w := d.place("D", "on the work"); w.BaseRef != "whaleshark/r1" || w.BaseOID != newer {
		t.Fatalf("cut from %s %s, want the collected work", w.BaseRef, w.BaseOID)
	}

	// A branch of that name that was there before is used and never ours.
	d.git("branch", "whaleshark/r1-E-mine", "main")
	if dir, w := d.place("E", "mine"); !w.KeepBranch || testkit.Git(t, dir, "rev-parse", "HEAD") != d.git("rev-parse", "main") {
		t.Fatalf("an existing branch: %+v", w)
	}

	empty := t.TempDir()
	testkit.Git(t, empty, "init", "-q", "-b", "main")
	d.s.Tasks["Z"] = &contract.Task{ID: "Z", Placement: contract.PlaceWorktree}
	if _, _, err := d.tr.Place(empty, d.s, "Z"); code(err) != "no_commit" {
		t.Errorf("a repository with no commit answered %v", err)
	}
	d.write("whaleshark.toml", "[worktrees]\nbranch_prefix = \"a..b/\"\n")
	d.s.Tasks["F"] = &contract.Task{ID: "F", Placement: contract.PlaceWorktree}
	if _, _, err := d.tr.Place(d.root, d.s, "F"); code(err) != "bad_branch" {
		t.Errorf("a prefix no branch can have answered %v", err)
	}
}

const ignores = ".env\nnode_modules/\ncache/\nout\n"

// What .worktreeinclude names is copied and what share names is linked, only
// what git ignores; the rest of what git ignores is counted; a link is no
// work that waits to be committed.
func TestCarry(t *testing.T) {
	d := project(t, map[string]string{
		".gitignore":       ignores,
		"README.md":        "read\n",
		".worktreeinclude": "# what a worker needs\n.env\n\nabsent.txt\nREADME.md\n",
		"whaleshark.toml":  "[worktrees]\nshare = [\"node_modules\", \"absent\"]\n",
	})
	d.write(".env", "KEY=value\n")
	d.write("node_modules/pkg/index.js", "x\n")
	d.write("cache/blob", "y\n")
	if os.Symlink(d.root, filepath.Join(t.TempDir(), "probe")) != nil {
		t.Skip("this login can make no link")
	}

	// Not approved: nothing is linked, and Left counts what would have been.
	if n, some, err := d.tr.Left(d.root); n != 2 || !slices.Equal(some, []string{"cache", "node_modules"}) || err != nil {
		t.Fatalf("left behind before approval: %d %v %v", n, some, err)
	}
	dir, _ := d.place("A", "plain")
	if !exists(filepath.Join(dir, ".env")) || exists(filepath.Join(dir, "node_modules")) {
		t.Fatal("before approval the copy is missing or the link was made")
	}

	d.trust()
	d.said.Reset()
	dir, _ = d.place("B", "linked")
	if data, _ := os.ReadFile(filepath.Join(dir, ".env")); string(data) != "KEY=value\n" {
		t.Errorf("the copy holds %q", data)
	}
	if to, err := os.Readlink(filepath.Join(dir, "node_modules")); err != nil || to != filepath.Join(d.root, "node_modules") {
		t.Errorf("node_modules is linked to %q, %v", to, err)
	}
	for _, want := range []string{"README.md is not ignored", "absent is not in the project"} {
		if !strings.Contains(d.said.String(), want) {
			t.Errorf("it did not say %q, but %q", want, d.said)
		}
	}
	if strings.Contains(d.said.String(), "absent.txt") {
		t.Errorf("a file .worktreeinclude names and nobody has was spoken of: %q", d.said)
	}
	if clean, err := d.tr.Clean(dir); !clean || err != nil {
		t.Errorf("a fresh worktree with a link is not clean: %v", err)
	}
	if n, some, _ := d.tr.Left(d.root); n != 1 || !slices.Equal(some, []string{"cache"}) {
		t.Errorf("left behind: %d %v", n, some)
	}
	if _, err := d.tr.Remove(d.root, d.s, "B", false); err != nil || !exists(filepath.Join(d.root, "node_modules", "pkg", "index.js")) {
		t.Fatalf("removing the worktree: %v; what it linked to must stay", err)
	}
}

// A line that leads out of the project, by its form or through a link, and
// more to copy than the budget are refused before anything is made.
func TestCarryRefused(t *testing.T) {
	outside := t.TempDir()
	for name, tc := range map[string]struct{ include, share, code string }{
		"climbs":       {"../secret\n", "", "outside_project"},
		"absolute":     {filepath.Join(outside, "secret") + "\n", "", "outside_project"},
		"an option":    {"-rf\n", "", "outside_project"},
		"through link": {"out/secret\n", "", "outside_project"},
		"shared":       {"", "../elsewhere", "outside_project"},
		"too much":     {"cache\n", "", "include_too_big"},
	} {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{".gitignore": ignores, ".worktreeinclude": tc.include + ".env\n"}
			if tc.share != "" {
				files["whaleshark.toml"] = "[worktrees]\nshare = [\"" + tc.share + "\"]\n"
			}
			d := project(t, files)
			d.trust()
			d.write(".env", "KEY=value\n")
			d.write("cache/blob", "y\n")
			if err := os.Truncate(filepath.Join(d.root, "cache", "blob"), copyBudget+1); err != nil {
				t.Fatal(err)
			}
			if name == "through link" {
				if os.Symlink(outside, filepath.Join(d.root, "out")) != nil {
					t.Skip("this login can make no link")
				}
			}
			d.s.Tasks["A"] = &contract.Task{ID: "A", Placement: contract.PlaceWorktree}
			_, w, err := d.tr.Place(d.root, d.s, "A")
			if code(err) != tc.code || w != nil {
				t.Fatalf("answered %v, %v", w, err)
			}
			if exists(filepath.Join(d.root, ".whaleshark", "worktrees")) || d.has("whaleshark/r1-A") {
				t.Error("something was made all the same")
			}
		})
	}
}

// The setup line runs only once approved, in the new worktree, and is ended
// when its time is up.
func TestSetup(t *testing.T) {
	both := func(line string, seconds string) string {
		return "[setup]\nscript = '" + line + "'\nscript_windows = '" + line + "'\ntimeout_seconds = " + seconds + "\n"
	}
	d := project(t, map[string]string{"whaleshark.toml": both("echo made>made.txt", "60")})
	dir, w := d.place("A", "held back")
	if w.Setup != setupUntrusted || exists(filepath.Join(dir, "made.txt")) {
		t.Fatalf("not approved: setup %s", w.Setup)
	}
	d.trust()
	dir, w = d.place("B", "runs")
	if w.Setup != setupOK || !exists(filepath.Join(dir, "made.txt")) {
		t.Fatalf("approved: setup %s", w.Setup)
	}
	d.write("whaleshark.toml", both("echo broken&&exit 3", "60"))
	d.trust()
	_, w = d.place("C", "fails")
	if log, _ := os.ReadFile(w.Path + setupLog); w.Setup != setupFailed || !strings.Contains(string(log), "broken") {
		t.Fatalf("a failing line: setup %s, log %q", w.Setup, log)
	}
	slow := "sleep 60"
	if d.tr.k.Platform.System() == "windows" {
		slow = "ping -n 60 localhost"
	}
	d.write("whaleshark.toml", both(slow, "1"))
	d.trust()
	began := time.Now()
	_, w = d.place("D", "too slow")
	if log, _ := os.ReadFile(w.Path + setupLog); w.Setup != setupFailed || time.Since(began) > 30*time.Second || !strings.Contains(string(log), "stopped after 1s") {
		t.Fatalf("a line past its time: setup %s after %v, log %q", w.Setup, time.Since(began), log)
	}
}

// A project in a syncing folder gets its worktrees under the login's cache,
// and is told that the agents will ask there.
type syncing struct{ contract.Platform }

func (syncing) Synced(string) bool { return true }

func TestFallback(t *testing.T) {
	d := project(t, map[string]string{"a.txt": "one\n"})
	d.tr.k.Platform = syncing{d.tr.k.Platform}
	dirs, _ := d.tr.k.Platform.Dirs()
	dir, w := d.place("A", "elsewhere")
	if !inside(filepath.Join(dirs.Cache, "worktrees"), dir) || inside(d.root, dir) || !exists(filepath.Join(dir, "a.txt")) {
		t.Fatalf("placed in %s", dir)
	}
	if !strings.Contains(d.said.String(), "outside the project") {
		t.Errorf("no warning: %q", d.said)
	}
	d.said.Reset()
	if d.place("B", "second"); d.said.Len() > 0 {
		t.Errorf("warned a second time: %q", d.said)
	}
	if _, err := d.tr.Remove(d.root, d.s, "A", false); err != nil || exists(w.Path) {
		t.Fatalf("removing it: %v", err)
	}
}

// Work that is not committed keeps a worktree, unless it is discarded; the
// branch stays either way, and a removed worktree is made again on it.
func TestRemove(t *testing.T) {
	d := project(t, map[string]string{"a.txt": "one\n"})
	if w, err := d.tr.Remove(d.root, d.s, "none", false); w != nil || err != nil {
		t.Fatalf("a task with no worktree: %v, %v", w, err)
	}
	dir, w := d.place("A", "edits")
	if clean, err := d.tr.Clean(dir); !clean || err != nil {
		t.Fatalf("a fresh worktree is not clean: %v", err)
	}
	if clean, _ := d.tr.Clean(d.root); !clean {
		t.Fatal("the project's own folder is no worktree of ours and must read as clean")
	}
	for name, text := range map[string]string{"a.txt": "changed\n", "new.txt": "new\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if clean, _ := d.tr.Clean(dir); clean {
			t.Fatalf("%s waits to be committed and the worktree reads as clean", name)
		}
		if _, err := d.tr.Remove(d.root, d.s, "A", false); code(err) != "uncommitted" || !exists(dir) {
			t.Fatalf("with %s not committed: %v", name, err)
		}
		testkit.Git(t, dir, "checkout", "-q", "--", "a.txt")
		os.Remove(filepath.Join(dir, "new.txt"))
	}
	tip := testkit.Commit(t, dir, "work", map[string]string{"b.txt": "two\n"})
	gone, err := d.tr.Remove(d.root, d.s, "A", false)
	if err != nil || gone.RemovedAt.IsZero() || exists(dir) || d.git("rev-parse", w.Branch) != tip {
		t.Fatalf("removing committed work: %+v, %v", gone, err)
	}
	if strings.Contains(d.git("worktree", "list"), "r1-A") || !w.RemovedAt.IsZero() {
		t.Fatal("git still lists it, or the caller's record was written")
	}
	d.s.Tasks["A"].Worktree = gone
	if same, err := d.tr.Remove(d.root, d.s, "A", false); err != nil || !same.RemovedAt.Equal(gone.RemovedAt) {
		t.Fatalf("removing it twice: %+v, %v", same, err)
	}
	back, again := d.place("A", "edits")
	if back != dir || again.KeepBranch || !again.RemovedAt.IsZero() || testkit.Git(t, back, "rev-parse", "HEAD") != tip {
		t.Fatalf("made again: %+v", again)
	}
	os.WriteFile(filepath.Join(back, "junk.txt"), []byte("junk\n"), 0o600)
	if _, err := d.tr.Remove(d.root, d.s, "A", true); err != nil || exists(back) || !d.has(w.Branch) {
		t.Fatalf("discarding: %v", err)
	}
	if open, err := d.tr.open(d.root); len(open) != 0 || err != nil {
		t.Fatalf("removals left open: %v, %v", open, err)
	}
}

// A removal killed halfway is finished by replaying its intent, at whatever
// step it was killed; a line in the log that names something not ours
// deletes nothing.
func TestInterruptedRemoval(t *testing.T) {
	for name, kill := range map[string]func(d *desk, path string){
		"before anything": func(*desk, string) {},
		"half the files":  func(_ *desk, path string) { os.Remove(filepath.Join(path, "a.txt")) },
		"git's entry too": func(_ *desk, path string) { os.Remove(filepath.Join(path, ".git")) },
		"all the files":   func(_ *desk, path string) { os.RemoveAll(path) },
	} {
		t.Run(name, func(t *testing.T) {
			d := project(t, map[string]string{"a.txt": "one\n", "b.txt": "two\n"})
			dir, _ := d.place("A", "cut off")
			if err := note(d.root, dir, false); err != nil {
				t.Fatal(err)
			}
			kill(d, dir)
			f, _ := os.OpenFile(filepath.Join(d.root, ".whaleshark", intentFile), os.O_APPEND|os.O_WRONLY, 0o600)
			f.WriteString(`{"version":1,"path":"torn`)
			f.Close()

			found, err := d.tr.Repair(d.root, []*contract.State{d.s}, false)
			if err != nil || len(found) != 1 || !strings.Contains(found[0], "was cut off.") {
				t.Fatalf("looking: %q, %v", found, err)
			}
			found, err = d.tr.Repair(d.root, []*contract.State{d.s}, true)
			if err != nil || len(found) != 1 || !strings.HasSuffix(found[0], "finished.") {
				t.Fatalf("fixing: %q, %v", found, err)
			}
			if exists(dir) || strings.Contains(d.git("worktree", "list"), "r1-A") || !d.has("whaleshark/r1-A-cut-off") {
				t.Fatal("the worktree is not all gone, or its branch went with it")
			}
			d.s.Tasks["A"].Worktree = nil
			if found, err := d.tr.Repair(d.root, []*contract.State{d.s}, true); len(found) != 0 || err != nil {
				t.Fatalf("afterwards: %q, %v", found, err)
			}
		})
	}

	d := project(t, map[string]string{"a.txt": "one\n"})
	theirs := filepath.Join(t.TempDir(), "theirs")
	os.MkdirAll(theirs, 0o700)
	os.WriteFile(filepath.Join(theirs, "kept.txt"), []byte("kept\n"), 0o600)
	if err := note(d.root, theirs, false); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(filepath.Join(d.root, ".whaleshark", intentFile), os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("\n" + `{"version":2,"path":"` + filepath.ToSlash(d.root) + `"}` + "\n")
	f.Close()
	if found, err := d.tr.Repair(d.root, nil, true); err != nil || len(found) != 1 || !exists(filepath.Join(theirs, "kept.txt")) || !exists(filepath.Join(d.root, "a.txt")) {
		t.Fatalf("a line naming a folder that is not ours: %q, %v", found, err)
	}
}

// Removing a closed run takes its worktrees and each branch proven merged
// by one of the three proofs; everything else is kept and listed.
func TestForget(t *testing.T) {
	d := project(t, map[string]string{"a.txt": "one\n", "side.txt": "side\n"})
	work := func(id, name, file string) (string, *contract.Worktree) {
		dir, w := d.place(id, name)
		testkit.Commit(t, dir, "work of "+id, map[string]string{file: id + "\n"})
		return dir, w
	}
	_, merged := work("A", "merged", "a1.txt")
	_, picked := work("B", "picked", "b1.txt")
	two, squashed := work("C", "squashed", "c1.txt")
	testkit.Commit(t, two, "more work of C", map[string]string{"c2.txt": "C\n"})
	_, unmerged := work("D", "unmerged", "d1.txt")
	d.git("branch", "whaleshark/r1-E-theirs", "main")
	_, theirs := d.place("E", "theirs")
	twisted, twist := work("F", "twisted", "f1.txt")
	dirty, soiled := d.place("G", "soiled")
	_, untouched := d.place("H", "untouched")
	_, closed := work("I", "closed", "i1.txt")

	d.git("merge", "-q", "--no-ff", "-m", "merge A", merged.Branch)
	d.git("cherry-pick", picked.Branch)
	d.git("merge", "-q", "--squash", squashed.Branch)
	d.git("commit", "-q", "-m", "C as one")
	d.git("merge", "-q", "--no-ff", "-m", "merge I", closed.Branch)
	// B's file changes again after it was picked: only its commit's like proves B.
	testkit.Commit(t, d.root, "main goes on", map[string]string{"side.txt": "moved on\n", "b1.txt": "and more\n"})
	// F's only commit beyond what main has is a merge that changes a file by hand.
	d.git("merge", "-q", "--no-ff", "-m", "merge F's first", twist.Branch+"~0")
	testkit.Git(t, twisted, "merge", "-q", "--no-commit", "--no-ff", "main")
	os.WriteFile(filepath.Join(twisted, "side.txt"), []byte("by hand\n"), 0o600)
	testkit.Git(t, twisted, "commit", "-q", "-a", "-m", "merge main, and more")
	os.WriteFile(filepath.Join(dirty, "new.txt"), []byte("new\n"), 0o600)
	gone, err := d.tr.Remove(d.root, d.s, "I", false)
	if err != nil {
		t.Fatal(err)
	}
	d.s.Tasks["I"].Worktree = gone

	kept, err := d.tr.Forget(d.root, d.s)
	want := []string{unmerged.Branch, theirs.Branch, twist.Branch, soiled.Branch + " (work not committed in " + dirty + ")"}
	if err != nil || !slices.Equal(kept, want) {
		t.Fatalf("kept %q, %v\nwant %q", kept, err, want)
	}
	for _, w := range []*contract.Worktree{merged, picked, squashed, untouched, closed} {
		if d.has(w.Branch) {
			t.Errorf("%s is merged and still there", w.Branch)
		}
		if _, err := git(d.root, "config", "--get", "branch."+w.Branch+".base"); err == nil {
			t.Errorf("git still has the base of %s", w.Branch)
		}
	}
	for _, w := range []*contract.Worktree{unmerged, theirs, twist, soiled} {
		if !d.has(w.Branch) {
			t.Errorf("%s was deleted", w.Branch)
		}
	}
	if list := d.git("worktree", "list"); strings.Count(list, "\n") != 1 || !exists(filepath.Join(dirty, "new.txt")) {
		t.Errorf("worktrees afterwards:\n%s", list)
	}
}

// A worktree no run records is found once it is no longer being made, and
// removed only when nothing in it waits to be committed.
func TestRepairOrphans(t *testing.T) {
	d := project(t, map[string]string{"a.txt": "one\n"})
	kept, _ := d.place("A", "recorded")
	lost, _ := d.place("B", "lost")
	soiled, _ := d.place("C", "soiled")
	os.WriteFile(filepath.Join(soiled, "new.txt"), []byte("new\n"), 0o600)
	delete(d.s.Tasks, "B")
	delete(d.s.Tasks, "C")
	runs := []*contract.State{d.s}
	if found, err := d.tr.Repair(d.root, runs, true); len(found) != 0 || err != nil {
		t.Fatalf("worktrees just made: %q, %v", found, err)
	}
	old := time.Now().Add(-time.Hour)
	for _, m := range marks(d.root) {
		at := testkit.Git(t, m.path, "rev-parse", "--path-format=absolute", "--git-path", markName)
		os.Chtimes(at, old, old)
	}
	found, err := d.tr.Repair(d.root, runs, false)
	if err != nil || len(found) != 2 || !strings.Contains(found[0], "(r1 B) is in no run's record.") || !strings.Contains(found[1], "holds work that is not committed.") {
		t.Fatalf("looking: %q, %v", found, err)
	}
	found, err = d.tr.Repair(d.root, runs, true)
	if err != nil || len(found) != 2 || !strings.HasSuffix(found[0], "removed.") || exists(lost) || !exists(soiled) || !exists(kept) {
		t.Fatalf("fixing: %q, %v", found, err)
	}
	if found, _ := d.tr.Repair(d.root, runs, true); len(found) != 1 {
		t.Fatalf("afterwards: %q", found)
	}
}

// Many tasks are placed at once, as one start of many does.
func TestManyAtOnce(t *testing.T) {
	d := project(t, map[string]string{"a.txt": "one\n"})
	var all sync.WaitGroup
	errs := make([]error, 12)
	for i := range errs {
		id := fmt.Sprintf("T%d", i)
		d.s.Tasks[id] = &contract.Task{ID: id, Placement: contract.PlaceWorktree}
	}
	for i := range errs {
		all.Go(func() { _, _, errs[i] = d.tr.Place(d.root, d.s, fmt.Sprintf("T%d", i)) })
	}
	all.Wait()
	if err := errors.Join(errs...); err != nil || len(marks(d.root)) != len(errs) {
		t.Fatalf("%d worktrees were made: %v", len(marks(d.root)), err)
	}
}
