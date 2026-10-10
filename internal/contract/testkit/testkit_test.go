package testkit

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

func TestEvening(t *testing.T) {
	f, err := Load(Evening)
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != FixtureVersion || f.State.Version != contract.StateVersion || f.Now.Format("15:04") != "21:14" {
		t.Fatalf("version %d, state %d, now %v", f.Version, f.State.Version, f.Now)
	}
	if len(f.State.Tasks) != 19 || len(f.Terms.Panes) != 13 {
		t.Errorf("%d tasks and %d panes, want 19 and 13", len(f.State.Tasks), len(f.Terms.Panes))
	}
	order := func(l contract.Look) int {
		return slices.IndexFunc(contract.Looks, func(x contract.LookOf) bool { return x.Look == l })
	}
	for _, caller := range []contract.CallerKind{contract.Human, contract.Orchestrator} {
		v := f.Views[caller]
		if v == nil || v.Caller != caller {
			t.Fatalf("no view for %s", caller)
		}
		cards, last := 0, -1
		for _, s := range v.Sections {
			for _, c := range s.Cards {
				cards++
				task := f.State.Tasks[c.Task]
				if task == nil || task.Name != c.Name {
					t.Errorf("card %s %q is not a task of the run", c.Task, c.Name)
				}
				if order(c.Look) < last {
					t.Errorf("card %s is out of the one order", c.Task)
				}
				last = order(c.Look)
				if contract.Looks[last].Section != s.Name {
					t.Errorf("card %s (%s) is under %s", c.Task, c.Look, s.Name)
				}
				for _, line := range c.Next {
					if caller != contract.Human && bytes.Contains([]byte(line), []byte("--human")) {
						t.Errorf("card %s shows --human to the %s", c.Task, caller)
					}
				}
			}
		}
		if cards != 12 || v.Counts.Agents != 12 || len(v.Items) != 3 || v.Counts.Waiting != 3 || v.Counts.Holding != 2 {
			t.Errorf("%s: %d cards, %d items, counts %+v", caller, cards, len(v.Items), v.Counts)
		}
		for _, it := range v.Items {
			if q := f.State.Questions[it.ID]; q == nil || q.Text != it.Text || q.For != contract.ForHuman {
				t.Errorf("item %s is not an open item for the human", it.ID)
			}
			if human := bytes.Contains([]byte(it.Next[0]), []byte("--human")); human != (caller == contract.Human) {
				t.Errorf("item %s: next line %q shown to the %s", it.ID, it.Next[0], caller)
			}
		}
	}
}

// A fixture must hold nothing the types do not know, so that it cannot drift from them.
func TestFixtureMatchesTypes(t *testing.T) {
	data, err := fixtures.ReadFile("fixtures/" + Evening + ".json")
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(new(Fixture)); err != nil {
		t.Error(err)
	}
}

func TestGrammar(t *testing.T) {
	if len(Lines) != 25 || len(AgentSteps) != 15 {
		t.Errorf("%d kinds of line and %d agent steps", len(Lines), len(AgentSteps))
	}
}

// The scratch repository is a real one: a second copy of the code can be
// made from it, and a change there is a commit on a branch of its own.
func TestRepo(t *testing.T) {
	root := Repo(t, map[string]string{"a.txt": "one\n", "src/b.txt": "two\n"})
	tree := filepath.Join(t.TempDir(), "T1")
	Git(t, root, "worktree", "add", "-q", "--no-track", "-b", "whaleshark/r1-T1", tree, "main")
	id := Commit(t, tree, "T1: change a", map[string]string{"a.txt": "three\n", "src/b.txt": ""})
	if got := Git(t, root, "rev-parse", "whaleshark/r1-T1"); got != id || got == Git(t, root, "rev-parse", "main") {
		t.Fatalf("the worktree's commit is %s and its branch is at %s", id, got)
	}
	if got := Git(t, root, "diff", "--name-status", "main", "whaleshark/r1-T1"); got != "M\ta.txt\nD\tsrc/b.txt" {
		t.Fatalf("the branch changed %q", got)
	}
}
