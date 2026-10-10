package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/gitwt"
	"github.com/tgdigital-hub/whaleshark/internal/guide"
	"github.com/tgdigital-hub/whaleshark/internal/pick"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/internal/store"
)

// No test may reach the clipboard of whoever runs it: the program that
// would write it is replaced before anything starts, and what it was given
// is kept for the test to look at.
var board []string

func TestMain(m *testing.M) {
	pick.Run = func(argv []string, in []byte) error {
		board = append(board, argv[0]+": "+string(in))
		return nil
	}
	os.Exit(m.Run())
}

// terms is the keeper as a test wants it: its version, its picture, and
// what it answers a pop-up.
type terms struct {
	contract.NoTerminals
	version string
	err     error
	snap    contract.Snapshot
	reason  string
	shown   []string
}

func (t *terms) Version() (string, error) { return t.version, t.err }
func (t *terms) Snapshot(context.Context) (*contract.Snapshot, error) {
	return &t.snap, nil
}
func (t *terms) Notify(title, body string, sound bool) (string, string, error) {
	t.shown = append(t.shown, body)
	return t.reason, "", nil
}

// hooks answers for an agent's settings: with args, the file lacks our entries.
type hooks struct {
	contract.NoAgentSettings
	args []string
}

func (h *hooks) Ensure(string, string, string, bool) (contract.AgentSetup, error) {
	return contract.AgentSetup{File: "settings", Args: h.args}, nil
}

type nudges struct {
	contract.NoNotifier
	sent []contract.Nudge
}

func (n *nudges) Nudge(c contract.Nudge) error { n.sent = append(n.sent, c); return nil }

// desk is a pretended login with one project in a scratch repository, set
// up as init leaves it, and a keeper of this version that shows no pane.
type desk struct {
	*testing.T
	k      *contract.Kit
	root   string
	dirs   contract.Dirs
	terms  *terms
	hooks  *hooks
	nudges *nudges
	caller contract.Caller
	errw   bytes.Buffer
}

func project(t *testing.T) *desk {
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
	for _, name := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "SSH_CONNECTION", "SSH_TTY", contract.EnvSocket, contract.EnvSystem} {
		t.Setenv(name, "")
	}
	server := contract.ServerFile
	contract.ServerFile = filepath.Join(home, "etc", "whaleshark", "server.toml")
	t.Cleanup(func() { contract.ServerFile = server })
	d := &desk{T: t, k: contract.NewKit(), terms: &terms{version: contract.Version}, hooks: &hooks{}, nudges: &nudges{}}
	platform.Plug(d.k)
	store.Plug(d.k)
	gitwt.Plug(d.k)
	Plug(d.k)
	d.k.Terms, d.k.AgentSettings, d.k.Notifier = d.terms, d.hooks, d.nudges
	d.root = testkit.Repo(t, map[string]string{"a.txt": "one\n"})
	d.dirs, _ = d.k.Platform.Dirs()
	d.write(filepath.Join(d.root, ".git", "info", "exclude"), contract.ProjectDir+"/\n")
	for _, dir := range []string{d.dirs.State, d.dirs.Cache, filepath.Join(d.root, contract.ProjectDir)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := contract.WriteProjects(d.k.Platform, d.dirs.State, []string{d.root}); err != nil {
		t.Fatal(err)
	}
	return d
}

func (d *desk) write(path, text string) {
	d.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		d.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		d.Fatal(err)
	}
}

// old writes a file and dates it two minutes back, as a killed writer's is.
func (d *desk) old(path string) {
	d.Helper()
	d.write(path, "{}\n")
	then := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(path, then, then); err != nil {
		d.Fatal(err)
	}
}

// newRun creates a run with the tasks named, and returns its record.
func (d *desk) newRun(id string, tasks ...string) *contract.State {
	d.Helper()
	s := &contract.State{Run: contract.Run{ID: id, CreatedAt: time.Now()}, Tasks: map[string]*contract.Task{},
		Attempts: map[string]*contract.Attempt{}, Questions: map[string]*contract.Question{}}
	for _, task := range tasks {
		s.Tasks[task] = &contract.Task{ID: task, Name: "task " + strings.ToLower(task), Placement: contract.PlaceWorktree}
	}
	if err := d.k.Store.Create(d.root, s); err != nil {
		d.Fatal(err)
	}
	return s
}

func (d *desk) change(run string, fn func(*contract.State)) {
	d.Helper()
	if err := d.k.Store.Change(d.root, run, func(s *contract.State) error { fn(s); return nil }); err != nil {
		d.Fatal(err)
	}
}

// doctor runs the command and returns its report with its exit code.
func (d *desk) doctor(flags ...string) (*exam, int) {
	d.Helper()
	c := &contract.Call{Kit: d.k, Command: contract.Find("doctor"), Flags: map[string][]string{}, Caller: d.caller,
		Root: d.root, Now: time.Now(), Out: new(bytes.Buffer), Err: &d.errw}
	for _, f := range flags {
		c.Flags[f] = []string{""}
	}
	result, err := run(c)
	if testing.Verbose() {
		d.Logf("doctor %v\n%s", flags, c.Out)
	}
	var r *contract.Refusal
	if errors.As(err, &r) {
		return r.Data.(*exam), r.Exit
	} else if err != nil {
		d.Fatal(err)
	}
	return result.(*exam), 0
}

// line returns the first line that holds every word.
func (e *exam) line(words ...string) *Line {
	for i, l := range e.Lines {
		all := true
		for _, w := range words {
			all = all && strings.Contains(l.Text+" "+l.Fix, w)
		}
		if all {
			return &e.Lines[i]
		}
	}
	return nil
}

// want holds a report to a line with a mark and words.
func (d *desk) want(e *exam, mark string, words ...string) {
	d.Helper()
	if l := e.line(words...); l == nil || l.Mark != mark {
		d.Errorf("expected %s %q, got %+v in\n%s", mark, words, l, e.text())
	}
}

func (e *exam) text() string {
	var b strings.Builder
	for _, l := range e.Lines {
		fmt.Fprintf(&b, "  %-8s %s | %s\n", l.Mark, l.Text, l.Fix)
	}
	return b.String()
}

func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

// A project as init leaves it, with the keeper of this version running, has
// no problem; nothing is changed, and nobody's clipboard is touched for a
// caller that is no person at a terminal.
func TestClean(t *testing.T) {
	d := project(t)
	e, exit := d.doctor()
	if exit != 0 || e.Problems != 0 {
		t.Fatalf("exit %d, %d problems:\n%s", exit, e.Problems, e.text())
	}
	if d.k.Platform.System() != "windows" { // which cannot tell whose a folder is
		d.want(e, ok, "state folder", "yours alone")
		d.want(e, ok, "nobody else can write to")
	}
	d.want(e, ok, "the engine is running")
	d.want(e, ok, "git version")
	d.want(e, ok, "the lock works")
	d.want(e, ok, "every copy of the code")
	d.want(e, ok, "no port slot")
	d.want(e, ok, "pop-up")
	d.want(e, note, "no agent tool", "whaleshark init --agent")
	d.want(e, note, "only when you run doctor yourself")
	if len(board) != 0 || d.errw.Len() != 0 {
		t.Errorf("the clipboard was reached: %v %q", board, d.errw.String())
	}

	bare := &desk{T: t, k: d.k, root: t.TempDir(), terms: d.terms}
	if e, exit = bare.doctor("server"); exit != 0 {
		t.Fatalf("outside a project: exit %d\n%s", exit, e.text())
	}
	bare.want(e, note, "is not set up as a project", "whaleshark init")
	bare.want(e, note, " server")
}

// The keeper: stopped, of another version by either sign, with a socket
// another login could open, and with a layout file it refused.
func TestEngine(t *testing.T) {
	d := project(t)
	d.terms.err = contract.ErrEngineUnreachable
	e, exit := d.doctor()
	d.want(e, note, "the engine is not running", "whaleshark open")
	if exit != 0 {
		t.Errorf("a stopped engine is exit %d", exit)
	}
	d.terms.err = contract.ErrWireVersion
	e, exit = d.doctor()
	d.want(e, problem, "restart needed", "whaleshark engine stop")
	if exit != contract.ExitFailed || e.Problems != 1 {
		t.Errorf("exit %d, %d problems", exit, e.Problems)
	}
	d.terms.err, d.terms.version = nil, "0.0.1-other"
	e, _ = d.doctor()
	d.want(e, problem, "restart needed")
	d.terms.version = contract.Version

	d.write(filepath.Join(d.dirs.State, "layout.json.bad"), "{")
	e, _ = d.doctor()
	d.want(e, note, "layout.json.bad", "did not come back")

	if d.k.Platform.System() == "windows" {
		return // no permission bits to seed
	}
	sock := filepath.Join(t.TempDir(), "open", "engine.sock")
	t.Setenv(contract.EnvSocket, sock)
	d.write(sock, "")
	for _, path := range []string{sock, filepath.Dir(sock), d.dirs.State} {
		if err := os.Chmod(path, 0o777); err != nil { // #nosec G302 -- the fault this test seeds
			t.Fatal(err)
		}
	}
	e, exit = d.doctor()
	d.want(e, problem, "the engine's socket", "open to another login", again)
	d.want(e, problem, "the folder of the engine's socket", "can be written by another login")
	d.want(e, problem, "your state folder", "open to another login")
	if exit != contract.ExitFailed {
		t.Errorf("exit %d", exit)
	}
	// A folder that is not ours is named and never changed.
	e, exit = d.doctor("fix")
	d.want(e, fixed, "the engine's socket", "yours alone now")
	d.want(e, fixed, "your state folder")
	d.want(e, problem, "the folder of the engine's socket")
	if info, _ := os.Stat(filepath.Dir(sock)); exit != contract.ExitFailed || info.Mode().Perm() != 0o777 {
		t.Errorf("after --fix: exit %d, the socket's folder has mode %v\n%s", exit, info.Mode().Perm(), e.text())
	}
	if err := os.Chmod(filepath.Dir(sock), 0o755); err != nil { // #nosec G302 -- a folder others may read, as a person's own often is
		t.Fatal(err)
	}
	if info, _ := os.Stat(sock); info.Mode().Perm() != 0o600 {
		t.Errorf("after --fix the socket has mode %v", info.Mode().Perm())
	}
	if e, exit = d.doctor(); exit != 0 || e.line("open to another login") != nil {
		t.Errorf("after the repair: exit %d\n%s", exit, e.text())
	}
}

// What init checks and wrote: the repository, the rules block, the hooks,
// and the settings nobody approved.
func TestProject(t *testing.T) {
	d := project(t)
	block := filepath.Join(t.TempDir(), "RULES.md")
	d.write(block, "my own notes\n")
	mine := contract.Installed{Versioned: contract.Versioned{Version: contract.FileVersion}, Entries: []contract.InstalledEntry{
		{Kind: "block", File: block, Agent: "claude"}, {Kind: "hook", File: "settings", Agent: "claude"}}}
	if err := contract.WriteVersioned(d.k.Platform, filepath.Join(d.root, contract.ProjectDir, installedFile), contract.FileVersion, mine); err != nil {
		t.Fatal(err)
	}
	d.hooks.args = []string{"--settings", "elsewhere"}
	d.write(filepath.Join(d.root, ".git", "info", "exclude"), "")
	d.write(filepath.Join(d.root, "whaleshark.toml"), "[setup]\nscript = \"make deps\"\n")

	e, exit := d.doctor()
	d.want(e, problem, "the rules for the lead agent are gone from", "whaleshark init --agent claude")
	d.want(e, problem, "the status line and the gate for claude are not in", "whaleshark init --agent claude")
	d.want(e, problem, "git does not leave .whaleshark alone", "whaleshark init")
	d.want(e, note, "1 setting of this project", "nobody approved", "setup.script", "whaleshark trust")
	if exit != contract.ExitFailed || e.Problems != 3 {
		t.Errorf("exit %d, %d problems\n%s", exit, e.Problems, e.text())
	}

	d.write(block, "my own notes\n"+guide.Block)
	d.hooks.args = nil
	d.write(filepath.Join(d.root, ".git", "info", "exclude"), contract.ProjectDir+"/\n")
	lines, err := contract.TrustLines(d.root)
	if err == nil {
		err = contract.WriteTrust(d.k.Platform, d.root, lines, time.Now())
	}
	if err != nil {
		t.Fatal(err)
	}
	e, exit = d.doctor()
	d.want(e, ok, "the rules for the lead agent are in", "as this version writes them")
	d.want(e, ok, "the status line and the gate for claude are set")
	if exit != 0 || e.line("whaleshark trust") != nil {
		t.Errorf("exit %d\n%s", exit, e.text())
	}

	d.write(block, strings.Replace(guide.Block, "\n", "\nan added line\n", 1))
	d.write(filepath.Join(d.root, "whaleshark.toml"), "[setup]\nscript = \"make all\"\n")
	e, _ = d.doctor()
	d.want(e, note, "was changed since init wrote it")
	d.want(e, note, "they changed since the approval of")

	// A repository nobody committed to, and a folder that is none.
	fresh := &desk{T: t, k: d.k, root: t.TempDir(), terms: d.terms}
	testkit.Git(t, fresh.root, "init", "-q", "-b", "main")
	fresh.write(filepath.Join(fresh.root, contract.ProjectDir, "x"), "")
	fresh.write(filepath.Join(fresh.root, ".git", "info", "exclude"), contract.ProjectDir+"/\n")
	e, _ = fresh.doctor()
	fresh.want(e, problem, "the repository has no commit yet", "git commit")
	plain := &desk{T: t, k: d.k, root: t.TempDir(), terms: d.terms}
	plain.write(filepath.Join(plain.root, contract.ProjectDir, "x"), "")
	e, exit = plain.doctor()
	plain.want(e, note, "the project is no git repository")
	if exit != 0 {
		t.Errorf("a project without git: exit %d\n%s", exit, e.text())
	}
}

func TestOldGit(t *testing.T) {
	for line, old := range map[string]bool{"git version 2.37.9": true, "git version 1.9.0": true, "git version 2.38.0": false,
		"git version 2.54.0 (a vendor's build)": false, "git version 3.0.1": false, "something else": true} {
		if got := oldGit(line); got != old {
			t.Errorf("%q: old is %v", line, got)
		}
	}
}

// The worktree checks, for real: a worktree no run records and a removal
// that was cut off are found, named, and with --fix put right.
func TestWorktrees(t *testing.T) {
	d := project(t)
	s := d.newRun("r1", "T1", "T2", "T3")
	paths := map[string]string{}
	for _, task := range []string{"T1", "T2", "T3"} {
		dir, w, err := d.k.Placement.Place(d.root, s, task)
		if err != nil {
			t.Fatal(err)
		}
		paths[task] = dir
		// A worktree younger than its setup may take is being made, not left over.
		mark := testkit.Git(t, dir, "rev-parse", "--path-format=absolute", "--git-path", "whaleshark")
		then := time.Now().Add(-time.Hour)
		if err := os.Chtimes(mark, then, then); err != nil {
			t.Fatal(err)
		}
		if task == "T1" {
			d.change("r1", func(s *contract.State) { s.Tasks[task].Worktree = w })
		}
	}
	intent := fmt.Sprintf("{\"version\":1,\"path\":%q,\"at\":\"2026-01-02T03:04:05Z\"}\n", paths["T3"])
	d.write(filepath.Join(d.root, contract.ProjectDir, "removals.jsonl"), intent)

	e, exit := d.doctor()
	d.want(e, problem, "The worktree "+paths["T2"], "is in no run's record", again)
	d.want(e, problem, "The removal of "+paths["T3"], "was cut off", again)
	if exit != contract.ExitFailed || e.Problems != 2 || !exists(paths["T2"]) || !exists(paths["T3"]) {
		t.Fatalf("exit %d, %d problems\n%s", exit, e.Problems, e.text())
	}
	// Work that is not committed is never removed.
	d.write(filepath.Join(paths["T2"], "new.txt"), "work\n")
	e, exit = d.doctor("fix")
	d.want(e, fixed, "The removal of "+paths["T3"], "put right")
	d.want(e, problem, "The worktree "+paths["T2"], "left as it is")
	if exit != contract.ExitFailed || exists(paths["T3"]) || !exists(paths["T2"]) || !exists(paths["T1"]) {
		t.Fatalf("exit %d\n%s", exit, e.text())
	}
	os.Remove(filepath.Join(paths["T2"], "new.txt"))
	e, exit = d.doctor("fix")
	d.want(e, fixed, "The worktree "+paths["T2"], "put right")
	if exit != 0 || exists(paths["T2"]) || !exists(paths["T1"]) {
		t.Fatalf("exit %d\n%s", exit, e.text())
	}
	e, _ = d.doctor()
	d.want(e, ok, "every copy of the code belongs to a task")
}

// What runs leave to the login and the project: slots of tasks that are
// gone, a run removed halfway, unfinished files, old days of the log,
// answers to items that are gone, and more runs than is quick.
func TestLeftovers(t *testing.T) {
	d := project(t)
	p, state := d.k.Platform, d.dirs.State
	d.newRun("r1", "T1")
	d.change("r1", func(s *contract.State) {
		s.Questions["q1"] = &contract.Question{ID: "q1", State: contract.QuestionOpen}
		s.Questions["q2"] = &contract.Question{ID: "q2", State: contract.QuestionClosed}
	})
	for _, slot := range [][2]string{{"r1", "T1"}, {"r1", "T9"}, {"r7", "T1"}} {
		if _, err := contract.TakeSlot(p, contract.DefaultPorts, d.root, slot[0], slot[1], 10); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := contract.TakeSlot(p, contract.DefaultPorts, t.TempDir(), "r1", "T1", 10); err != nil { // no project of this login
		t.Fatal(err)
	}
	half := filepath.Join(d.root, contract.ProjectDir, "runs", ".gone-r7")
	d.write(filepath.Join(half, "state.json"), "{}")
	stray := []string{filepath.Join(state, "ui.json.1234567"), filepath.Join(state, "projects.json.42"),
		filepath.Join(state, "ctx", "w1+p2.json.7"), filepath.Join(d.root, contract.ProjectDir, "trust.json.99")}
	for _, path := range stray {
		d.old(path)
	}
	kept := []string{filepath.Join(state, "layout.json.bad"), filepath.Join(state, "slots.lock"), filepath.Join(state, "ctx", "w1+p2.json")}
	for _, path := range kept {
		d.old(path)
	}
	young := filepath.Join(state, "slots.json.555")
	d.write(young, "{}")
	kept = append(kept, young)
	today := filepath.Join(state, "log", time.Now().UTC().Format(time.DateOnly)+".log")
	aged := filepath.Join(state, "log", time.Now().AddDate(0, 0, -40).Format(time.DateOnly)+".log")
	d.write(today, "x\n")
	d.write(aged, "x\n")
	ui := contract.UIFile{Versioned: contract.Versioned{Version: contract.FileVersion}, Mute: true,
		Drafts: map[string]string{"q1": "half", "q2": "typed", "n5": "answer"}}
	if err := contract.WriteVersioned(p, filepath.Join(state, contract.UIFileName), contract.FileVersion, ui); err != nil {
		t.Fatal(err)
	}

	if err := d.k.Store.SetCurrent(d.root, "r1"); err != nil {
		t.Fatal(err)
	}
	e, exit := d.doctor()
	d.want(e, note, "nobody is watching run r1, and nothing runs in it")
	d.want(e, problem, "2 port slots held for a task that is gone", "r1 T9", "r7 T1", again)
	d.want(e, problem, "the removal of a run was cut off", "1 folder", again)
	d.want(e, problem, "4 unfinished files", again)
	d.want(e, note, "the command log holds 1 day older than 30 days", again)
	d.want(e, note, "2 half-typed answers to an item that is gone", "n5, q2", again)
	if exit != contract.ExitFailed || e.Problems != 3 || !exists(half) || !exists(aged) {
		t.Fatalf("exit %d, %d problems\n%s", exit, e.Problems, e.text())
	}

	e, exit = d.doctor("fix")
	d.want(e, fixed, "2 port slots", "given back")
	d.want(e, fixed, "the removal of a run was cut off", "deleted")
	d.want(e, fixed, "4 unfinished files", "deleted")
	d.want(e, fixed, "the command log", "deleted")
	d.want(e, fixed, "2 half-typed answers", "dropped")
	if exit != 0 {
		t.Fatalf("exit %d\n%s", exit, e.text())
	}
	for _, path := range append(stray, half, aged) {
		if exists(path) {
			t.Errorf("%s is still there", path)
		}
	}
	for _, path := range append(kept, today) {
		if !exists(path) {
			t.Errorf("%s was deleted", path)
		}
	}
	if taken, _ := contract.ReadSlots(p.Peek, state); len(taken) != 2 || taken[0].Task != "T1" || taken[0].Run != "r1" {
		t.Errorf("the slots left are %+v", taken)
	}
	ui = contract.UIFile{}
	contract.ReadVersioned(p.Peek, filepath.Join(state, contract.UIFileName), contract.FileVersion, &ui)
	if len(ui.Drafts) != 1 || ui.Drafts["q1"] != "half" || !ui.Mute {
		t.Errorf("ui.json holds %+v", ui)
	}
	e, exit = d.doctor()
	d.want(e, ok, "2 port slots in use")
	if exit != 0 || e.line(again) != nil {
		t.Errorf("after the repair: exit %d\n%s", exit, e.text())
	}

	// A closed run's slot goes; more than twenty runs are worth a word.
	d.change("r1", func(s *contract.State) { s.Run.ClosedAt = time.Now() })
	for n := 2; n <= manyRuns+1; n++ {
		d.newRun(fmt.Sprint("r", n))
	}
	e, _ = d.doctor()
	d.want(e, problem, "1 port slot held for a task that is gone", "r1 T1")
	d.want(e, note, "21 runs are kept here", "closed: r1", "whaleshark run rm")
	if e.line("nobody is watching") != nil {
		t.Errorf("a run in which nothing runs was called unwatched\n%s", e.text())
	}

	// A record that cannot be read: said, and nothing of its project is judged.
	d.write(filepath.Join(d.root, contract.ProjectDir, "runs", "r2", contract.StateFile), "{\"version\": 99}")
	e, exit = d.doctor("fix")
	d.want(e, problem, "the record of run r2 cannot be read")
	d.want(e, note, "the copies of the code are not checked")
	if taken, _ := contract.ReadSlots(p.Peek, state); exit != contract.ExitFailed || len(taken) != 2 {
		t.Errorf("exit %d, the slots left are %+v\n%s", exit, taken, e.text())
	}

	// A file of a newer program is never written.
	d.write(filepath.Join(state, "slots.json"), "{\"version\": 99}")
	e, _ = d.doctor("fix")
	d.want(e, problem, "the list of port slots cannot be read", "newer version")
}

// Whether anybody is watching a run, what did not come back after the
// keeper started again, and a tab no attempt owns.
func TestWatchedAndTabs(t *testing.T) {
	d := project(t)
	d.newRun("r1", "T1", "T2")
	d.change("r1", func(s *contract.State) {
		for _, id := range []string{"T1.1", "T2.1"} {
			a := &contract.Attempt{ID: id, Task: id[:2], State: contract.AttemptWorking}
			a.Agent.Name, a.Place.Pane = "h-r1-"+strings.ToLower(id[:2]), "p"+id[1:2]
			s.Attempts[id] = a
		}
	})
	d.terms.snap.Panes = []contract.Pane{{ID: "p1", Agent: "claude", Name: "h-r1-t1"}, {ID: "p2", Name: ""},
		{ID: "p8", Label: "old work", Agent: "claude", Name: "h-r0-t5"}, {ID: "p9", Label: "a shell"}}
	e, exit := d.doctor()
	d.want(e, note, "nobody is watching run r1 (2 live attempts)", "whaleshark ui")
	d.want(e, problem, "the agent of T2.1 did not come back in its pane p2", "whaleshark start T2 --retry --resume")
	d.want(e, note, `the tab "old work" (pane p8)`, "no attempt owns")
	if exit != contract.ExitFailed || e.Problems != 1 || e.line("T1.1") != nil || e.line("p9") != nil {
		t.Errorf("exit %d, %d problems\n%s", exit, e.Problems, e.text())
	}

	dir := d.k.Store.Dir(d.root, "r1")
	d.write(filepath.Join(dir, "sweep.at"), fmt.Sprintf("{\"version\":1,\"at\":%q}", time.Now().Add(-2*time.Second).Format(time.RFC3339Nano)))
	e, _ = d.doctor()
	d.want(e, ok, "run r1 is watched", "last looked at")
	d.write(filepath.Join(dir, "sweep.at"), "{\"version\":1,\"at\":\"2026-01-02T03:04:05Z\"}")
	unlock, held, err := d.k.Platform.TryLock(filepath.Join(dir, contract.WaitLock))
	if err != nil || !held {
		t.Fatal(err)
	}
	e, _ = d.doctor()
	d.want(e, ok, "run r1 is watched", "its lead agent waits")
	unlock()
	e, _ = d.doctor()
	d.want(e, note, "nobody is watching run r1")
}

// The pop-up's switch, and the one test nudge of --notify.
func TestNotify(t *testing.T) {
	d := project(t)
	d.terms.reason = contract.NotifyShown
	e, exit := d.doctor("notify")
	d.want(e, ok, "the pop-up and its sound are switched on")
	d.want(e, ok, "the test nudge was shown in your window")
	if exit != 0 || len(d.terms.shown) != 1 || len(d.nudges.sent) != 0 {
		t.Errorf("exit %d, shown %v, sent on %v", exit, d.terms.shown, d.nudges.sent)
	}
	d.terms.reason = contract.NotifyNoWindow
	e, _ = d.doctor("notify")
	d.want(e, note, "no window is open", "whaleshark open")
	d.want(e, note, "handed to your other channels")
	if len(d.nudges.sent) != 1 || !d.nudges.sent[0].Urgent || d.nudges.sent[0].Task != "" {
		t.Errorf("sent on: %+v", d.nudges.sent)
	}
	d.write(filepath.Join(d.dirs.Config, contract.ConfigFile), "[nudge]\npopup = false\n")
	e, _ = d.doctor("notify")
	d.want(e, note, "the pop-up is switched off", "whaleshark set nudge.popup on")
	if len(d.terms.shown) != 2 || len(d.nudges.sent) != 2 {
		t.Errorf("with the pop-up off: shown %v, sent on %v", d.terms.shown, d.nudges.sent)
	}
	if e, _ = d.doctor(); len(d.terms.shown) != 2 || len(d.nudges.sent) != 2 || e.line("test nudge") != nil {
		t.Error("a nudge was sent without --notify")
	}
	e, _ = d.doctor()
	d.want(e, note, "nudge.phone is on and no address is set", "only a phone paired with the page")
	d.write(filepath.Join(d.dirs.Config, contract.ConfigFile), "[nudge]\nphone = false\n[notify]\nurl = \"https://notices.example/x\"\n")
	e, _ = d.doctor()
	d.want(e, note, "an address for your phone is set and nudge.phone is off", "whaleshark set nudge.phone on")
	hooks := filepath.Join(d.dirs.Config, "hooks", "nudge.d")
	d.write(filepath.Join(hooks, "ring"), "#!/bin/sh\n")
	if e, _ = d.doctor(); e.line("nudge hooks") != nil {
		t.Errorf("a private hook folder was spoken of:\n%s", e.text())
	}
	if d.k.Platform.System() != "windows" { // which cannot tell whose a folder is
		os.Chmod(hooks, 0o777)
		e, _ = d.doctor()
		d.want(e, note, "the nudge hooks in", "are not run", "another login can write to "+hooks)
	}
	d.write(filepath.Join(d.dirs.Config, contract.ConfigFile), "[nudge\n")
	e, exit = d.doctor()
	d.want(e, problem, "config.toml cannot be read")
	if exit != contract.ExitFailed {
		t.Errorf("exit %d", exit)
	}
}

// The paste line: a word goes the way a copy in a pane goes from where the
// person sits, and they are asked to paste it.
func TestPaste(t *testing.T) {
	d := project(t)
	d.caller = contract.Caller{Kind: contract.Human, Where: contract.WhereTyped}
	board = nil
	e, _ := d.doctor()
	l := e.line("Paste it somewhere")
	if l == nil || !strings.HasPrefix(l.Text, pick.Copied) || len(board) != 1 || d.errw.Len() != 0 {
		t.Fatalf("on the person's own computer: %+v, programs %v, to the terminal %q", l, board, d.errw.String())
	}
	word := l.Text[strings.Index(l.Text, "whaleshark-"):][:len("whaleshark-0000")]
	// Windows' program is given two bytes a letter.
	if !strings.Contains(strings.ReplaceAll(board[0], "\x00", ""), word) {
		t.Errorf("the line names %q and the program got %q", word, board[0])
	}

	// Over an ssh typed by hand only the outer terminal can be asked.
	t.Setenv("SSH_TTY", "/dev/ttys001")
	e, _ = d.doctor()
	l = e.line("Paste it somewhere")
	if l == nil || !strings.HasPrefix(l.Text, pick.Sent) || !strings.Contains(l.Text, "does not let a program write the clipboard") ||
		len(board) != 1 || !strings.HasPrefix(d.errw.String(), "\x1b]52;c;") {
		t.Errorf("over ssh: %+v, programs %v, to the terminal %q", l, board, d.errw.String())
	}
	board = nil
}
