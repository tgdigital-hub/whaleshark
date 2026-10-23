package runtask_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// A task's effort is one of four words, and its own arguments hold nothing
// that widens a worker's permissions or replaces its settings: those are
// the person's to approve in whaleshark.toml.
func TestTaskEffortAndArgs(t *testing.T) {
	p := scenario.Prepare(t, nil, nil)
	orch := func(exit int, args ...string) func(words ...string) string {
		return func(words ...string) string { return play(t, p.Command(scenario.Orch, args...), exit, words...) }
	}
	write(t, filepath.Join(p.Root, "b.md"), briefText)
	orch(0, "run", "new", "a shop")("Run r1 is open")
	add := func(id string, more ...string) []string {
		return append([]string{"task", "add", id, "task " + id, "--brief", "b.md", "--check", "none"}, more...)
	}
	orch(2, add("A", "--effort", "huge")...)("low, medium, high and max")
	for _, c := range []struct{ args, says string }{
		{"--dangerously-skip-permissions", "agent.args"},
		{"--permission-mode=bypassPermissions", "agent.args"},
		{"--verbose --allowedTools Bash", "agent.args"},
		{"--settings /srv/team/s.json", "agent.settings"},
		{"--model big", "give the task --model"},
		{"--effort max", "give the task --effort"},
		{"--add-dir /srv/other", "inside the project"},
		{"--add-dir ../other", "inside the project"},
	} {
		orch(2, add("A", "--args="+c.args)...)(c.says)
	}
	orch(0, add("A", "--effort", "high", "--args=--verbose --add-dir docs --max-turns 5")...)("ready")
	s, _ := p.Record()
	if a := s.Tasks["A"]; a.Effort != "high" || !slices.Equal(a.Args, []string{"--verbose", "--add-dir", "docs", "--max-turns", "5"}) {
		t.Errorf("the task holds effort %q and arguments %q", a.Effort, a.Args)
	}
}
