package runtask_test

import (
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// tabs is every open tab's label with the keeper's own name for the agent
// in it, in order.
func (h *shop) tabs() (out []string) {
	h.t.Helper()
	snap, err := h.p.Terms().Snapshot(h.t.Context())
	if err != nil {
		h.t.Fatal(err)
	}
	for _, p := range snap.Panes {
		out = append(out, p.Label+"="+p.Name)
	}
	slices.Sort(out)
	return out
}

// renamed is the scenario of a rename while the agent works: the record
// and the tab say the new name, the keeper's own name for the agent and the
// attempt are as they were, and the tab is still the task's to close. A
// name that is taken, or no name, changes nothing.
func renamed(t *testing.T, on scenario.Backend) (log []string) {
	h := openOn(t, on, map[string]string{"A.1": "hang", "B.1": "hang"})
	h.add(map[string]string{"A": "none", "B": "none"}, "A", "B")
	if _, err := h.p.Terms().TabCreate("", "", h.p.Root, "by hand", nil); err != nil {
		t.Fatal(err)
	}
	s, _ := h.p.Record()
	agent, other, before := s.Attempts["A.1"].Agent.Name, s.Attempts["B.1"].Agent.Name, h.tabs()
	if !slices.Contains(before, "part A="+agent) || s.Attempts["A.1"].State != contract.AttemptWorking {
		t.Fatalf("A is not at work in a tab of its name: %v, %s", before, s.Attempts["A.1"].State)
	}
	note := func(what string) {
		s, _ := h.p.Record()
		a := s.Attempts["A.1"]
		log = append(log, fmt.Sprintf("%s: A %q %s, attempt %s in the same tab %t, tabs %s", what, s.Tasks["A"].Name, s.Tasks["A"].Status,
			a.State, a.Agent.Name == agent, strings.NewReplacer(agent, "<its agent>", other, "<the agent of B>").Replace(strings.Join(h.tabs(), "; "))))
	}

	// What is refused changes neither the record nor a tab.
	snap, _ := h.p.Terms().Snapshot(t.Context())
	for _, row := range []struct {
		name string
		exit int
		word string
	}{
		{"part B", 5, "name_taken"}, {"PART  b", 5, "name_taken"}, {"By Hand", 5, "name_taken"}, {"B", 5, "name_taken"},
		{"orchestrator", 5, "name_taken"}, {"lead", 5, "name_taken"},
		{"one two three four", 2, ""}, {"a name of far too many letters", 2, ""}, {"-rf", 2, ""}, {"", 2, ""},
		{"a\x1b]0;owned\a", 2, ""}, {"two\nlines", 2, ""},
	} {
		out := h.orch(row.exit, "task", "edit", "A", "--name="+row.name, "--json")(row.word)
		// A name a tab has is refused with the workspace that tab is of.
		for _, p := range snap.Panes {
			for _, sp := range snap.Spaces {
				if strings.EqualFold(p.Label, row.name) && p.Workspace == sp.ID && !strings.Contains(out, "is taken by an agent in "+sp.Name+". Give another.") {
					t.Errorf("--name %q is refused without its workspace %q: %s", row.name, sp.Name, out)
				}
			}
		}
		if got, _ := h.p.Record(); got.Tasks["A"].Name != "part A" || !slices.Equal(h.tabs(), before) {
			t.Fatalf("--name %q was refused and changed something: %q, %v", row.name, got.Tasks["A"].Name, h.tabs())
		}
	}
	note("refused")

	// The lead agent renames a task at work; with a check beside it the
	// edit is refused as before, and the name with it.
	h.orch(5, "task", "edit", "A", "--name", "sign-in page", "--check", "none")("only a pending, ready or failed")
	note("with a check")
	h.orch(0, "task", "edit", "part A", "--name", "  sign-in   page ")("A is running", "Changed: name.")
	note("renamed")
	if got := h.tabs(); !slices.Contains(got, "sign-in page="+agent) || slices.Contains(got, "part A="+agent) || len(got) != len(before) {
		t.Fatalf("the tab did not follow the name, or the agent's own name went with it: %v", got)
	}
	h.orch(0, "task", "edit", "A", "--name", "sign-in page")()
	h.orch(5, "task", "edit", "B", "--name", "Sign-In Page")("is taken")

	// The person renames it from a menu: the name rides in a private file,
	// and what he did is written down as his.
	file := filepath.Join(t.TempDir(), "name")
	write(t, file, "door page\n")
	play(t, h.p.Command(scenario.Human, "task", "edit", "A", "--name", "--file", file), 0, "Changed: name.")
	s, _ = h.p.Record()
	if ev := s.Inbox.Events[len(s.Inbox.Events)-1]; ev.Kind != "human" || ev.Data["what"] != "task edit" || ev.Data["on"] != "A" {
		t.Errorf("the person's rename is written down as %+v", ev)
	}
	note("by the person")

	// No agent renames as the person, and a worker renames nothing.
	play(t, h.p.Command(scenario.Orch, "task", "edit", "A", "--name", "mine", "--human", "--json"), 5, "not_human")
	play(t, h.p.Command("B.1", "task", "edit", "B", "--name", "mine", "--json"), 5, "not_for_worker")
	play(t, h.p.Command("B.1", "task", "edit", "B", "--name", "mine", "--human", "--json"), 5, "not_human")
	note("tried by agents")

	// The tab is the task's still: a stop by the new name closes it.
	h.orch(0, "stop", "door page")("its tab: closed")
	note("stopped")
	if got := h.tabs(); slices.ContainsFunc(got, func(tab string) bool { return strings.HasSuffix(tab, "="+agent) }) {
		t.Errorf("the renamed tab was not closed with its task: %v", got)
	}
	// An ended task is renamed too, and the name it had is free again.
	h.orch(0, "task", "edit", "A", "--name", "part C")()
	h.orch(0, "task", "edit", "B", "--name", "door page")()
	note("after it ended")
	return log
}

func TestARenameWhileTheAgentWorks(t *testing.T) {
	logs := map[scenario.Backend][]string{}
	for _, on := range []scenario.Backend{scenario.Double, scenario.Keeper} {
		t.Run(string(on), func(t *testing.T) {
			if on == scenario.Keeper && runtime.GOOS == "windows" {
				t.Skip("NOT PROVEN HERE: on Windows the real keeper plays the first scenarios of the suite only, for the time they take there")
			}
			logs[on] = renamed(t, on)
		})
	}
	if on := logs[scenario.Keeper]; on != nil && !slices.Equal(on, logs[scenario.Double]) {
		t.Errorf("on the keeper:\n%s\n\non the double:\n%s", strings.Join(on, "\n"), strings.Join(logs[scenario.Double], "\n"))
	}
}
