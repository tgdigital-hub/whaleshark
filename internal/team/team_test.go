package team_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func TestMain(m *testing.M) { scenario.Main(m) }

const briefText = "# Target\nthe pages\n## Change\nread them\nConstraints: none\nOwnership: none\nAcceptance\na list\n"

// play runs one real command, holds it to its exit code and to words it
// must print, and returns what it printed.
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

// tasks is what makes two runs' tasks the same: all of a task that a team
// keeps, with its brief's text, and nothing of when or how far.
func tasks(t *testing.T, p *scenario.Project) string {
	t.Helper()
	s, dir := p.Record()
	var lines []string
	for id, task := range s.Tasks {
		brief, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(task.Brief)))
		line, _ := json.Marshal([]any{id, task.Name, task.Title, task.Check, task.After, task.Owns, task.Agent, task.Model,
			task.Placement, task.Status, len(task.After), string(brief)})
		lines = append(lines, strings.ReplaceAll(string(line), "null", "[]"))
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

// A team saved from a run and run into a new one gives the same tasks.
func TestSaveAndRun(t *testing.T) {
	p := scenario.Prepare(t, nil, nil)
	orch := func(exit int, args ...string) func(words ...string) string {
		return func(words ...string) string { return play(t, p.Command(scenario.Orch, args...), exit, words...) }
	}
	write(t, filepath.Join(p.Root, "b.md"), briefText)
	orch(0, "run", "new", "a site review")()
	orch(4, "team", "run", "site review")("There is no team site-review")
	orch(0, "team", "list")()
	orch(0, "task", "add", "W", "write up the findings", "--name", "report writer", "--brief", "b.md", "--check", "none")()
	orch(0, "task", "add", "C", "read every page", "--name", "copy check", "--brief", "b.md", "--check", "exit 0",
		"--owns", "pages/**", "--agent", "codex", "--model", "small")()
	orch(0, "task", "add", "L", "find broken links", "--name", "link check", "--brief", "b.md", "--check", "none", "--after", "C")()
	orch(0, "task", "edit", "W", "--after", "C,L")()
	want := tasks(t, p)
	// A cancelled task is not kept, and nothing waits for it.
	orch(0, "task", "add", "X", "a mistake", "--name", "not wanted", "--brief", "b.md", "--check", "none", "--after", "L")()
	orch(0, "task", "edit", "W", "--after", "C,L,X")()
	orch(0, "task", "cancel", "X")()
	before := tasks(t, p)

	orch(2, "team", "save", "../elsewhere")("letters, digits and dashes")
	orch(2, "team", "keep", "x")("no form of team")
	orch(2, "team", "save")("Usage")
	orch(0, "team", "save", "Site Review")("site-review is saved: 3 tasks")
	orch(5, "team", "save", "site review")("already", "team rm site-review")
	play(t, p.Command(scenario.Unbound, "team", "list"), 0, "site-review", "3 tasks, yours", "a site review")
	play(t, p.Command(scenario.Unbound, "team", "show", "site review"), 0, `C "copy check": read every page; check: exit 0`, "after: C, L")
	play(t, p.Command(scenario.Unbound, "team", "run", "site review"), 5)
	orch(5, "team", "run", "site review")("taken")
	if now := tasks(t, p); now != before {
		t.Fatalf("a refused team changed the run:\n%s", now)
	}

	orch(0, "run", "close")()
	orch(0, "run", "new", "the next site")()
	out := orch(0, "team", "run", "site review", "--json")()
	if !strings.Contains(out, `"id":"C"`) || !strings.Contains(out, `"status":"pending"`) {
		t.Fatalf("team run answered %s", out)
	}
	if got := tasks(t, p); got != want || strings.Count(got, "\n") != 2 {
		t.Fatalf("the team gave\n%s\nand the run it was saved from had\n%s", got, want)
	}
	if s, _ := p.Record(); len(s.Attempts) != 0 {
		t.Fatal("team run started something")
	}

	// A paused run takes no team; the person's own run of one is written down.
	play(t, p.Command(scenario.Human, "pause"), 0)
	orch(5, "team", "run", "site review")("paused")
	play(t, p.Command(scenario.Human, "resume"), 0)

	orch(0, "team", "rm", "site review")("removed")
	orch(4, "team", "rm", "site review")("no team")
	dirs, _ := p.Kit.Platform.Dirs()
	if left, _ := filepath.Glob(filepath.Join(dirs.Config, "teams", "*")); len(left) != 0 {
		t.Fatalf("team rm left %v", left)
	}
}

const projectTeam = `about = "Two checkers and a writer for any pages"

[[task]]
name  = "report writer"
title = "Write up what the two checkers found"
after = ["copy check", "L9"]
check = "none"

[[task]]
name  = "copy check"
title = "Read every page of {goal} for mistakes"
check = "none"
brief = "briefs/copy.md"

[[task]]
id    = "L9"
name  = "link check"
title = "Find every broken link in {goal}"
check = "exit 0"
`

// A team that comes with the project is refused, with the reason, until the
// person has approved the project as it stands; then {goal} is filled in.
func TestProjectTeam(t *testing.T) {
	p := scenario.Prepare(t, nil, nil)
	orch := func(exit int, args ...string) func(words ...string) string {
		return func(words ...string) string { return play(t, p.Command(scenario.Orch, args...), exit, words...) }
	}
	trust := func() {
		cmd := p.Command(scenario.Human, "trust")
		cmd.Stdin = strings.NewReader("yes\n")
		play(t, cmd, 0, "Approved.")
	}
	file := filepath.Join(p.Root, contract.TeamsDir, "review.toml")
	write(t, file, projectTeam)
	write(t, filepath.Join(p.Root, contract.TeamsDir, "briefs", "copy.md"), briefText+"Pages: {goal}\n")
	write(t, filepath.Join(p.Root, contract.TeamsDir, "LOUD.TOML"), projectTeam) // not a file trust lists
	orch(0, "run", "new", "a shop")()
	orch(0, "task", "add", "T1", "something else", "--name", "other work", "--brief", filepath.Join(contract.TeamsDir, "briefs", "copy.md"), "--check", "none")()

	orch(0, "team", "list")("review", "3 tasks, project, not approved")
	orch(0, "team", "show", "review")("link check")
	orch(5, "team", "run", "review", "--goal", "the checkout pages")("comes with the project", "whaleshark-teams/review.toml", "approved", "whaleshark trust")
	orch(5, "team", "rm", "review")("comes with the project")
	orch(4, "team", "run", "loud", "--goal", "x")("no team")
	if s, _ := p.Record(); len(s.Tasks) != 1 {
		t.Fatalf("an untrusted team added tasks: %d", len(s.Tasks))
	}

	trust()
	orch(0, "team", "list")("3 tasks, project  Two checkers")
	orch(2, "team", "run", "review")("with a goal", "--goal")
	orch(0, "team", "run", "review", "--goal", "the checkout pages")(`T2 "report writer" is pending`, `T3 "copy check" is ready`, `L9 "link check" is ready`)
	s, dir := p.Record()
	brief, _ := os.ReadFile(filepath.Join(dir, "briefs", "T3.md"))
	if w := s.Tasks["T2"]; strings.Join(w.After, ",") != "T3,L9" || w.Brief != "" || s.Tasks["T3"].Brief != "briefs/T3.md" ||
		s.Tasks["L9"].Title != "Find every broken link in the checkout pages" || !strings.HasSuffix(string(brief), "Pages: the checkout pages\n") {
		t.Fatalf("the team gave %+v, %+v and the brief %q", w, s.Tasks["L9"], brief)
	}

	// Changed after the approval, it is somebody else's text again.
	write(t, file, strings.ReplaceAll(projectTeam, "exit 0", "make deploy"))
	orch(5, "team", "run", "review", "--goal", "more")("comes with the project")
	trust()
	for _, bad := range [][2]string{
		{"check = \"none\"\n" + `chek = "none"`, "no key"},
		{`check = ""`, "a check is a command"},
		{"check = \"none\"\n" + `after = ["nobody"]`, "no task"},
		{"check = \"none\"\n" + `brief = "../../b.md"`, "climb"},
		{"check = \"none\"\n" + `owns = ["/etc/**"]`, "relative"},
		{"check = \"none\"\n" + `model = "--dangerous"`, "model"},
		{"check = \"none\"\n" + `name = "one two three four"`, "words"},
		{"check = \"none\"\n" + `after = ["x"]`, "wait for itself"},
	} {
		write(t, file, "[[task]]\ntitle = \"x\"\n"+bad[0]+"\n")
		trust()
		out, _ := p.Command(scenario.Orch, "team", "run", "review").CombinedOutput()
		if !strings.Contains(string(out), bad[1]) || strings.Contains(string(out), " is ready") {
			t.Fatalf("%s: team run said %s", bad[0], out)
		}
	}
	if s, _ := p.Record(); len(s.Tasks) != 4 {
		t.Fatalf("a bad team added tasks: %d", len(s.Tasks))
	}
}
