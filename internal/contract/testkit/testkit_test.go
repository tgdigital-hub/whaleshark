package testkit

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

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
	if len(Lines) != 27 || len(AgentSteps) != 18 {
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

// The morning with three fleets says the same thing in what the left
// section is built from and in what must come out: every row is a task of
// the run it names, each leader's attempt leads its fleet's run from the
// pane that run is bound to, and the counts are the pictures'.
func TestThreeFleets(t *testing.T) {
	f := Fleets()
	shop := f.In.Projects[0]
	runs := map[string]*contract.State{}
	agents := 0
	for _, s := range shop.Runs {
		runs[s.Run.ID] = s
		agents += len(s.Attempts) + 1
	}
	up := runs["r1"]
	if agents-3 != f.Side.Workspaces[0].Agents || up.Run.Mode != contract.ModeFleets || len(up.Run.Fleets) != 3 || len(f.Cards) != 3 {
		t.Fatalf("%d agents and %d fleets, the model says %d", agents-3, len(up.Run.Fleets), f.Side.Workspaces[0].Agents)
	}
	for name, fleet := range up.Run.Fleets {
		low, group := runs[fleet.Run], up.Tasks[fleet.Groups[0]]
		lead := up.Attempts[group.Attempts[0]]
		if low == nil || low.Run.Parent.Fleet != name || group.Fleet != name || group.Placement != contract.PlaceFleet ||
			lead.Leads != fleet.Run || lead.Place.Pane != low.Run.Orchestrator.Pane || f.View.Fleets[name] != low {
			t.Errorf("the fleet %s is not whole: %+v", name, fleet)
		}
	}
	rows, fleets := 0, 0
	for _, r := range f.Side.Rows {
		switch r.Kind {
		case contract.RowFleet:
			fleets++
		case contract.RowAgent:
			if rows++; runs[r.Run] == nil || runs[r.Run].Tasks[r.Task] == nil || runs[r.Run].Tasks[r.Task].Name != r.Name {
				t.Errorf("the row %q is no task of run %s", r.Name, r.Run)
			}
		}
	}
	if fleets != 3 || rows != 11 || f.Side.First == nil || f.Side.First.Name != "refund flow" || f.Item.Fleet != "billing" {
		t.Errorf("%d fleets and %d workers' rows (help pages is folded), first %+v", fleets, rows, f.Side.First)
	}
	if up.Questions["q1"].Down.Run != "r2" || runs["r2"].Questions["q1"].Up.Run != "r1" {
		t.Error("the question that climbed has lost its other half")
	}
	if len(f.In.Terms.Panes) != 19+3+2 {
		t.Errorf("%d panes, want every agent's and the two tabs in no project", len(f.In.Terms.Panes))
	}
}

// The afternoon with every guard on says what its comment says: 33 of 40
// agent-hours, one group not yet approved, a fingerprint that is not the
// approved one, an agent's wish for a setting, and one stuck worker.
func TestGuarded(t *testing.T) {
	g := Guarded()
	spent := contract.Spent(g.Now, g.Up, g.Low["billing"], g.Low["search"])
	if limit := g.Person.SpendLimit(g.Up.Run.Spend, false); spent != 33*time.Hour || limit != 40 || float64(spent)/float64(time.Hour) < contract.SpendWarnAt*float64(limit) {
		t.Errorf("spent %v of %d agent-hours", spent, limit)
	}
	if lack := contract.Unplanned(g.Up, []string{"G1", "G2", "G3"}); len(lack) != 1 || lack[0] != "G3" || g.Up.Questions[g.Up.Run.Plan.Item].Tasks[0] != "G3" {
		t.Errorf("not approved: %v", lack)
	}
	if p := g.Up.Run.Protect; p.Print == p.Seen || g.Up.Questions[p.Item].Cause != contract.CauseProtect || !contract.Owned(contract.Protected(g.Person, contract.ProjectDefaults()), p.Files[0]) {
		t.Errorf("protected files: %+v", p)
	}
	stuck := g.Low["billing"].Attempts["T7.1"].Seen
	if contract.StuckWhy(stuck.StuckWhy) != "no progress note · the same check failed 3 times" || stuck.StuckTold != contract.ToldOrchestrator {
		t.Errorf("the stuck worker: %+v", stuck)
	}
	for id, q := range g.Up.Questions {
		if !contract.PersonOnly(q.Cause) || q.From != contract.FromTool || q.For != contract.ForHuman {
			t.Errorf("item %s is not the tool's own for the person alone: %+v", id, q)
		}
	}
	if again := Guarded(); again.Up == g.Up || again.Low["search"] == g.Low["search"] {
		t.Error("two calls share a record")
	}
}
