package launch

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// The command line a worker's tool is given after its hooks: the project's
// arguments, the task's after them, one model, one effort and the person's
// settings file, an option given twice keeping the task's value.
func TestWorkerCommandLine(t *testing.T) {
	claude, _ := contract.FindTool(contract.BuiltIn(), "claude")
	codex, _ := contract.FindTool(contract.BuiltIn(), "codex")
	project := func(edit func(*contract.ProjectFile)) *launcher {
		l := &launcher{self: "/bin/ws", project: contract.ProjectDefaults()}
		l.project.Agent.Args = []string{"--permission-mode", "acceptEdits", "--model", "big", "--allowedTools", "Bash({whaleshark}:*)", "Bash(git:*)"}
		if edit != nil {
			edit(&l.project)
		}
		return l
	}
	approved := []string{"--permission-mode", "acceptEdits", "--model", "big", "--allowedTools", "Bash(/bin/ws:*)", "Bash(git:*)"}
	for _, c := range []struct {
		name    string
		l       *launcher
		tool    contract.Tool
		task    contract.Task
		model   string
		carried bool
		want    []string
	}{
		{"project args only", project(nil), claude, contract.Task{}, "", false, approved},
		{"a task's model over one in the args", project(nil), claude, contract.Task{}, "small",
			false, []string{"--permission-mode", "acceptEdits", "--allowedTools", "Bash(/bin/ws:*)", "Bash(git:*)", "--model", "small"}},
		{"the task's effort over the project's", project(func(p *contract.ProjectFile) { p.Agent.Effort = "medium" }), claude,
			contract.Task{Effort: "high"}, "", false, append(slices.Clone(approved), "--effort", "high")},
		{"the project's effort", project(func(p *contract.ProjectFile) { p.Agent.Effort = "medium" }), claude,
			contract.Task{}, "", false, append(slices.Clone(approved), "--effort", "medium")},
		{"a task's args after the project's, its value winning", project(nil), claude,
			contract.Task{Args: []string{"--verbose", "--allowedTools", "Read", "--add-dir", "docs"}}, "",
			false, []string{"--permission-mode", "acceptEdits", "--model", "big", "--verbose", "--allowedTools", "Read", "--add-dir", "docs"}},
		{"the folder option may come again", project(func(p *contract.ProjectFile) { p.Agent.Args = []string{"--add-dir", "a"} }), claude,
			contract.Task{Args: []string{"--add-dir", "b"}}, "", false, []string{"--add-dir", "a", "--add-dir", "b"}},
		{"the settings file", project(func(p *contract.ProjectFile) { p.Agent.Settings = "/srv/team/settings.json" }), claude,
			contract.Task{}, "", false, append(slices.Clone(approved), "--settings", "/srv/team/settings.json")},
		{"the settings file, carried with the hooks", project(func(p *contract.ProjectFile) { p.Agent.Settings = "/srv/team/settings.json" }), claude,
			contract.Task{}, "", true, approved},
		{"another tool: none of the project's args, no effort, no settings", project(func(p *contract.ProjectFile) {
			p.Agent.Effort, p.Agent.Settings = "high", "/srv/team/settings.json"
		}), codex, contract.Task{Args: []string{"--search"}}, "small-1", false, []string{"--search", "--model", "small-1"}},
	} {
		if got := c.l.command(c.tool, &c.task, c.model, c.carried); !slices.Equal(got, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

// The person's settings file reaches a worker in a shared folder with the
// hooks added to it, as the one --settings it is given, and its deny rules
// are all still there; one that is no full path stops the start.
func TestWorkerGetsTheSettingsFile(t *testing.T) {
	p := evening(t, 2, func(f *testkit.Fixture) { f.State.Tasks["R1"].Effort = "high" })
	file := filepath.Join(t.TempDir(), "settings.json")
	deny := `{"permissions": {"deny": ["Bash(rm -rf:*)", "Read(./.env)"]}}`
	if err := os.WriteFile(file, []byte(deny), 0o600); err != nil {
		t.Fatal(err)
	}
	approve(t, p, "[agent]\neffort = \"medium\"\nsettings = \""+filepath.ToSlash(file)+"\"\n")
	must(t, p.Command(scenario.Orch, "start", "R1"), 0, "working (R1.1)")
	got := started(t, p, "R1.1").Argv
	if len(got) != 4 || got[0] != "--settings" || got[1] == file || !slices.Equal(got[2:], []string{"--effort", "high"}) {
		t.Fatalf("the worker was started with %q", got)
	}
	data, err := os.ReadFile(got[1])
	if err != nil {
		t.Fatal(err)
	}
	if text := string(data); !strings.Contains(text, "Bash(rm -rf:*)") || !strings.Contains(text, "Read(./.env)") || !strings.Contains(text, " hook gate") {
		t.Errorf("the file the worker was given lost the person's rules or the gate:\n%s", text)
	}
	approve(t, p, "[agent]\nsettings = \"settings.json\"\n")
	must(t, p.Command(scenario.Orch, "start", "R2"), 5, "full path")
}

// With results.mirror_dir approved, a worker is given its task's notes
// folder as one more folder, and its prompt names it with the brief's Return.
func TestWorkerGetsTheNotesFolder(t *testing.T) {
	p := evening(t, 2, func(f *testkit.Fixture) { f.State.Tasks["R1"].Return = "result.md: what changed." })
	notes := t.TempDir()
	approve(t, p, "[results]\nmirror_dir = \""+filepath.ToSlash(notes)+"/{run}-{task}\"\n")
	must(t, p.Command(scenario.Orch, "start", "R1"), 0, "working (R1.1)")
	s, dir := p.Record()
	folder := filepath.Join(notes, s.Run.ID+"-R1")
	if got := started(t, p, "R1.1").Argv; len(got) != 4 || got[0] != "--settings" || !slices.Equal(got[2:], []string{"--add-dir", folder}) {
		t.Fatalf("the worker was started with %q", got)
	}
	prompt, err := os.ReadFile(filepath.Join(contract.AttemptDir(dir, "R1.1"), "prompt.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Notes folder: " + folder, "## Return\nWrite exactly this, as your brief's Return heading says:\nresult.md: what changed."} {
		if !strings.Contains(string(prompt), want) {
			t.Errorf("the prompt lacks %q:\n%s", want, prompt)
		}
	}
}
