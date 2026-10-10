package runtask_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/guide"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func TestMain(m *testing.M) { scenario.Main(m) }

const briefText = "# Target\nthe parser\n## Change\nadd it\nConstraints: none\n**Ownership** src\nAcceptance\nit passes\n"

// play runs one real command as a caller, holds it to its exit code and to
// words it must print, and returns what it printed.
func play(t *testing.T, cmd *exec.Cmd, exit int, words ...string) string {
	t.Helper()
	out, _ := cmd.CombinedOutput()
	if got := cmd.ProcessState.ExitCode(); got != exit {
		t.Fatalf("%v: exit %d, expected %d\n%s", cmd.Args[1:], got, exit, out)
	}
	for _, w := range words {
		if !bytes.Contains(out, []byte(w)) {
			t.Fatalf("%v: expected %q in what it printed:\n%s", cmd.Args[1:], w, out)
		}
	}
	return string(out)
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPlanThreeTasks(t *testing.T) {
	p := scenario.Prepare(t, nil, nil)
	orch := func(exit int, args ...string) func(words ...string) string {
		return func(words ...string) string { return play(t, p.Command(scenario.Orch, args...), exit, words...) }
	}
	write(t, filepath.Join(p.Root, "b.md"), briefText)
	write(t, filepath.Join(p.Root, "thin.md"), "# Target\nonly this\n")

	orch(5, "task", "add", "A", "data model", "--brief", "b.md", "--check", "none")("No run is open here")
	orch(0, "run", "new", "a shop")("Run r1 is open")
	orch(2, "run", "new", "b", "--park", "--base", "main")("phase 2")
	orch(5, "run", "new", "another")("still open", "--park")
	orch(0, "task", "add", "A", "data model", "--brief", "b.md", "--check", "exit 0", "--owns", "src/model/**")("A", "ready")
	orch(0, "task", "add", "B", "the page with a list", "--name", "list page", "--brief", "b.md", "--check", "exit 0",
		"--after", "A", "--owns", "src/**")("pending")
	orch(0, "task", "add", "C", "whole site", "--brief", "b.md", "--check", "none", "--after", "data model,B", "--cwd", "site")("pending")

	orch(2, "task", "add", "D", "x", "--name", "one two three four", "--brief", "b.md", "--check", "none")("at most three words")
	orch(5, "task", "add", "D", "x", "--name", "List Page", "--brief", "b.md", "--check", "none")("is taken")
	orch(5, "task", "add", "D", "you", "--brief", "b.md", "--check", "none")("is taken")
	orch(2, "task", "add", "D", "add tests for the parser", "--brief", "b.md", "--check", "none")("--name")
	orch(2, "task", "add", "D", "x", "--brief", "b.md")("needs a check")
	orch(2, "task", "add", "D", "x", "--check", "none")("needs its brief")
	orch(2, "task", "add", "D", "x", "--brief", "thin.md", "--check", "none")("lacks the heading Change, Constraints, Ownership, Acceptance")
	orch(4, "task", "add", "D", "x", "--brief", "b.md", "--check", "none", "--after", "Z")("no task")
	orch(5, "task", "add", "a", "x", "--brief", "b.md", "--check", "none")("taken")
	orch(5, "task", "add", "D", "lead", "--brief", "b.md", "--check", "none")("taken")
	orch(2, "task", "add", "D", "-x", "--brief", "b.md", "--check", "none")("--name")
	orch(2, "task", "add", "-D", "x", "--brief", "b.md", "--check", "none")()
	play(t, p.Command(scenario.Unbound, "task", "add", "D", "x", "--brief", "b.md", "--check", "none"), 5, "run takeover")

	s, dir := p.Record()
	if len(s.Tasks) != 3 {
		t.Fatalf("a refused task was kept: %d tasks", len(s.Tasks))
	}
	c := s.Tasks["C"]
	if strings.Join(c.After, ",") != "A,B" || c.Placement != "cwd:site" || c.Brief != "briefs/C.md" || s.Tasks["B"].Name != "list page" {
		t.Fatalf("C is recorded as %+v", c)
	}
	if read(t, filepath.Join(dir, "briefs", "C.md")) != briefText {
		t.Fatal("the brief was not kept as given")
	}
	if _, err := os.Stat(filepath.Join(dir, "briefs", "D.md")); err == nil {
		t.Fatal("a refused task left a brief behind")
	}

	// Edit, and what it frees.
	orch(0, "task", "edit", "C", "--after", "A", "--check", "exit 0")("C is pending", "Changed: check, after")
	orch(2, "task", "edit", "C")("--brief, --check")
	// The word none takes the last dependency away, which frees the task.
	orch(0, "task", "edit", "C", "--after", "none", "--owns", "none")("C is ready", "Changed: owns, after")
	orch(0, "task", "edit", "C", "--after", "A")("C is pending")
	orch(0, "task", "cancel", "list page")("B is cancelled")
	orch(5, "task", "reset", "A")("only a failed task")
	orch(0, "run", "list")("r1", "3 tasks", "1 kept", "pane "+p.Lead)
	orch(0, "run", "show")("3 tasks", "pane "+p.Lead)
	orch(0, "run", "close")("Run r1 is closed")
	orch(0, "run", "new", "the next")("Run r2 is open")
	orch(0, "run", "rm", "r1")("Run r1 is removed")
	orch(4, "run", "use", "r1")("no run")
	orch(2, "run", "sideways")()
	if runs, _ := p.Kit.Store.Runs(p.Root); len(runs) != 1 || runs[0] != "r2" {
		t.Fatalf("the runs are %v", runs)
	}

	// The person's own tab is never bound; another tab takes the run over.
	play(t, p.Command(scenario.Human, "run", "takeover"), 5, "lead agent's tab")
	play(t, p.Command(scenario.Unbound, "run", "takeover"), 0, "bound to this tab")
	orch(5, "run", "close")()
	if s, _ := p.Record(); s.Run.Orchestrator.Pane != p.Own || s.Run.Gen != 1 {
		t.Fatalf("after the takeover the run is bound to %+v, gen %d", s.Run.Orchestrator, s.Run.Gen)
	}
}

// review puts a task of the fixture in front of accept with a check of ours.
func review(f *testkit.Fixture, task, check string) {
	t := f.State.Tasks[task]
	t.Status, t.Check = contract.TaskReview, check
	a := f.State.Attempts[t.Attempts[len(t.Attempts)-1]]
	a.State, a.Report = contract.AttemptReported, &contract.Report{Outcome: contract.ReportDone, Summary: "ok", At: f.Now}
}

func TestEditResetAndAccept(t *testing.T) {
	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	review(f, "T7", "echo all good")
	review(f, "T8", "echo two tests broken && exit 3")
	review(f, "T9", contract.CheckNone)
	review(f, "T10", "test -f go.txt || sleep 4")
	review(f, "T12", "echo slow; (sleep 2; touch outlived) & sleep 3")
	f.State.Tasks["T14"].Status, f.State.Tasks["T14"].Failures = contract.TaskFailed, 3
	p := scenario.Prepare(t, f, nil)
	orch := func(exit int, args ...string) func(words ...string) string {
		return func(words ...string) string { return play(t, p.Command(scenario.Orch, args...), exit, words...) }
	}

	// Two of the agents at work are in the folder the checks run in.
	err = p.Kit.Store.Change(p.Root, "r3", func(s *contract.State) error {
		s.Attempts["T1.1"].Place.Cwd, s.Attempts["T2.1"].Place.Cwd = p.Root, p.Root
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	orch(0, "task", "reset", "T14")("T14 is pending")
	orch(0, "task", "edit", "go live", "--after", "P1")("Ready now: T14")
	orch(5, "task", "edit", "T1", "--check", "none")("only a pending, ready or failed")
	orch(5, "task", "cancel", "T1")("live attempt")
	orch(0, "task", "cancel", "T13")("T13 is cancelled")

	out := orch(0, "accept", "email sender", "--json")()
	var passed struct{ Result struct{ Tail, Log string } }
	if json.Unmarshal([]byte(out), &passed); passed.Result.Tail != "all good" || !strings.Contains(read(t, passed.Result.Log), "all good") {
		t.Fatalf("the passing check answered %s", out)
	}
	orch(1, "accept", "T8")("two tests broken", "check.log", "whaleshark reject T8")
	orch(5, "accept", "T9")("accepted by hand", "--by-hand")
	orch(5, "accept", "T7", "--by-hand", "read it")("not waiting to be checked")
	orch(5, "accept", "T1")("not waiting to be checked")
	orch(2, "accept", "T7", "T8")("phase 2")
	play(t, p.Command("T1.1", "accept", "T9", "--by-hand", "x"), 5, "not a worker's command")
	play(t, p.Command(scenario.Human, "accept", "T9", "--by-hand", "read the page"), 0, "done (by hand)")

	// A check that is killed with its accept: the attempt waits in checking
	// with the lock free, and the next accept starts again.
	if _, err := exec.LookPath("sh"); err == nil {
		killed := p.Command(scenario.Orch, "accept", "T10")
		if err := killed.Start(); err != nil {
			t.Fatal(err)
		}
		for end := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
			if s, _ := p.Record(); s.Attempts["T10.1"].State == contract.AttemptChecking {
				break
			}
			if time.Now().After(end) {
				t.Fatal("the accept never marked the attempt as being checked")
			}
		}
		orch(5, "accept", "T8")("Another accept is running")
		killed.Process.Kill()
		killed.Wait()
		if s, _ := p.Record(); s.Attempts["T10.1"].State != contract.AttemptChecking || s.Tasks["T10"].Status != contract.TaskReview {
			t.Fatal("a killed accept changed the record")
		}
		write(t, filepath.Join(p.Root, "go.txt"), "")
		orch(0, "accept", "T10")("T10 is done, checked", "where 2 other agents are working")

		// A check that outlasts the project's limit is stopped and has failed.
		write(t, filepath.Join(p.Root, "whaleshark.toml"), "[check]\ntimeout_seconds = 1\n")
		orch(1, "accept", "T12")("the check was stopped after 1s")
		// And what the check itself started has ended with it.
		time.Sleep(3 * time.Second)
		if _, err := os.Stat(filepath.Join(p.Root, "outlived")); err == nil {
			t.Fatal("a program the stopped check had started lived on")
		}
	}

	s, _ := p.Record()
	a8 := s.Attempts["T8.1"]
	if a8.State != contract.AttemptReported || a8.Check == nil || a8.Check.OK || a8.Check.Tail != "two tests broken" || a8.Accept != nil {
		t.Fatalf("the failed check is recorded as %+v, check %+v", a8, a8.Check)
	}
	if !slices.ContainsFunc(s.Inbox.Events, func(e contract.Event) bool { return e.Kind == "check_failed" && e.Task == "T8" }) {
		t.Fatal("no check_failed event was raised")
	}
	if t7, t9 := s.Tasks["T7"], s.Tasks["T9"]; t7.Status != contract.TaskDone || t7.Accepted.How != contract.AcceptCheck ||
		t9.Accepted == nil || t9.Accepted.How != contract.AcceptByHand || t9.Accepted.Note != "read the page" || s.Tasks["T8"].Status != contract.TaskReview {
		t.Fatalf("accepted: T7 %+v, T9 %+v, T8 %s", t7.Accepted, t9.Accepted, s.Tasks["T8"].Status)
	}

	play(t, p.Command(scenario.Human, "run", "use", "r3"), 0, "Run r3 is where your commands go now")
}

func TestInitInTwoProjects(t *testing.T) {
	p := scenario.Prepare(t, nil, nil)
	home, _ := os.UserHomeDir()
	dirs, err := p.Kit.Platform.Dirs()
	if err != nil {
		t.Fatal(err)
	}
	second := p.Root + "-two"
	if err := os.MkdirAll(second, 0o700); err != nil {
		t.Fatal(err)
	}
	// The second project is a git repository: our folder is excluded from it, once.
	exclude := filepath.Join(second, ".git", "info", "exclude")
	if out, err := exec.Command("git", "init", "-q", second).CombinedOutput(); err != nil {
		t.Skipf("git is needed for this test: %v\n%s", err, out)
	}
	write(t, exclude, "*.log")
	in := func(root, who, answers string, exit int, args ...string) func(words ...string) string {
		return func(words ...string) string {
			cmd := p.Command(who, args...)
			cmd.Dir, cmd.Stdin = root, strings.NewReader(answers)
			return play(t, cmd, exit, words...)
		}
	}
	rules, settings := filepath.Join(home, ".claude", "CLAUDE.md"), filepath.Join(home, "config", "herdr", "config.toml")
	const mine, theirs = "# My own rules\nBe brief.\n", "[ui]\naccent = \"blue\"\n"
	write(t, rules, mine)
	write(t, settings, theirs)
	login := filepath.Join(dirs.State, "installed.json")
	uses := func(path string) map[string]int {
		var in contract.Installed
		if err := contract.ReadVersioned(os.ReadFile, path, contract.FileVersion, &in); err != nil {
			t.Fatal(err)
		}
		out := map[string]int{}
		for _, e := range in.Entries {
			out[e.Kind] = e.Uses
		}
		return out
	}

	if out := in(p.Root, scenario.Orch, "", 0, "init", "--print-block")(); out != guide.Block {
		t.Fatalf("--print-block printed %q", out)
	}
	if _, err := os.Stat(filepath.Join(p.Root, ".whaleshark")); err == nil {
		t.Fatal("--print-block set the project up")
	}
	in(p.Root, scenario.Orch, "", 0, "init", "--agent", "claude,pi")("readable by you only", "herdr 0.9.1", "the lock works", "git version", rules, guide.FirstMessage)
	in(p.Root, scenario.Orch, "yes\n", 0, "init", "--keys")("shift+a", "Stop all", "says yes")
	if read(t, settings) != theirs {
		t.Fatal("the shortcuts were written on an agent's word")
	}
	in(p.Root, scenario.Human, "\nyes\n", 0, "init", "--keys")("Which agent tools", "The shortcuts are written")
	in(p.Root, scenario.Human, "\nno\n", 0, "init", "--keys", "--agent", "claude")("Nothing was written")
	in(second, scenario.Human, "claude\nyes\n", 0, "init", "--keys")("Which agent tools", rules, "The shortcuts are written")

	if got := uses(login); got["block"] != 2 || got["keys"] != 2 {
		t.Fatalf("the login's record counts %v, expected two uses of each", got)
	}
	if !strings.Contains(read(t, rules), guide.Block) || strings.Count(read(t, settings), "[[keys.command]]") != 7 {
		t.Fatalf("after init: %q\n%q", read(t, rules), read(t, settings))
	}
	in(second, scenario.Orch, "", 0, "init")()
	if got := read(t, exclude); got != "*.log\n.whaleshark/\n" {
		t.Fatalf("git's list of exclusions is %q", got)
	}
	if roots, _ := contract.ReadProjects(os.ReadFile, dirs.State); len(roots) != 2 {
		t.Fatalf("the list of projects is %v", roots)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(filepath.Join(p.Root, ".whaleshark")); info.Mode().Perm() != 0o700 {
			t.Fatalf("our folder has mode %v", info.Mode().Perm())
		}
	}

	// Removing one project leaves the other whole.
	other := read(t, filepath.Join(second, ".whaleshark", "installed.json"))
	in(p.Root, scenario.Human, "", 0, "init", "--remove")("still used by 1 other", "out of your list")
	if got := uses(login); got["block"] != 1 || got["keys"] != 1 {
		t.Fatalf("after one removal the login's record counts %v", got)
	}
	if !strings.Contains(read(t, rules), guide.Block) || strings.Count(read(t, settings), "[[keys.command]]") != 7 ||
		read(t, filepath.Join(second, ".whaleshark", "installed.json")) != other {
		t.Fatal("removing one project took something from the other")
	}
	if roots, _ := contract.ReadProjects(os.ReadFile, dirs.State); len(roots) != 1 || roots[0] != second {
		t.Fatalf("the list of projects is %v", roots)
	}
	in(p.Root, scenario.Human, "", 0, "init", "--remove")()
	if got := uses(login); got["block"] != 1 {
		t.Fatalf("a second removal counted again: %v", got)
	}
	in(second, scenario.Human, "", 0, "init", "--remove")("the rules block is out", "the shortcuts are out")
	if read(t, rules) != mine || read(t, settings) != theirs {
		t.Fatalf("not byte for byte as before:\n%q\n%q", read(t, rules), read(t, settings))
	}
	if _, err := os.Stat(login); err == nil {
		t.Fatal("the login's record is left with nothing in it")
	}

	// --remove --all takes out what every project uses, whatever the counts.
	in(p.Root, scenario.Human, "claude\nyes\n", 0, "init", "--keys")()
	in(second, scenario.Human, "claude\nyes\n", 0, "init", "--keys")()
	in(p.Root, scenario.Human, "", 0, "init", "--remove", "--all")("Every project is out")
	if read(t, rules) != mine || read(t, settings) != theirs {
		t.Fatal("--remove --all left something behind")
	}
	if roots, _ := contract.ReadProjects(os.ReadFile, dirs.State); len(roots) != 0 {
		t.Fatalf("the list of projects is %v", roots)
	}
	in(p.Root, scenario.Human, "", 2, "init", "--all")()

	// A folder anyone may write is refused, and so is a newer record.
	if runtime.GOOS != "windows" {
		open := p.Root + "-open"
		if err := os.Mkdir(open, 0o700); err != nil {
			t.Fatal(err)
		}
		os.Chmod(open, 0o777)
		in(open, scenario.Human, "", 3, "init")("Another login can write")
	}
	write(t, login, `{"version": 99, "entries": []}`)
	in(p.Root, scenario.Human, "", 3, "init")("newer whaleshark")
}
