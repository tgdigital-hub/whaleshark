package phase3

import (
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// An upgrade during a run (8j): the first sweep by a program of another
// version than any before leaves one upgraded event for the lead agent, and
// the record is otherwise what it was. A second sweep says nothing more.
func TestAnUpgradeDuringARun(t *testing.T) {
	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	f.State.Run.Programs = []string{"0.4"}
	p := scenario.Prepare(t, f, nil)
	before, _ := p.Record()
	for range 2 {
		if out, err := p.Command(scenario.Orch, "status").CombinedOutput(); err != nil {
			t.Fatalf("status: %v\n%s", err, out)
		}
	}
	after, _ := p.Record()
	var told []contract.Event
	for _, e := range after.Inbox.Events[len(before.Inbox.Events):] {
		if e.Kind == "upgraded" {
			told = append(told, e)
		}
	}
	if len(told) != 1 || !strings.HasPrefix(told[0].Text, "upgraded to ") || !strings.HasSuffix(told[0].Text, "; start `wait` again") || len(after.Run.Programs) != 2 {
		t.Fatalf("the upgrade left %+v, and the run knows the programs %v", told, after.Run.Programs)
	}
	for id, a := range before.Attempts {
		if now := after.Attempts[id]; now == nil || now.State != a.State || now.TokenHash != a.TokenHash {
			t.Errorf("%s was %s and is %+v", id, a.State, now)
		}
	}
	for id, task := range before.Tasks {
		if now := after.Tasks[id]; now == nil || now.Status != task.Status {
			t.Errorf("%s was %s and is %+v", id, task.Status, now)
		}
	}
	// The lead agent's next wait hands it the event.
	out, _ := p.Command(scenario.Orch, "wait", "--timeout", "0").CombinedOutput()
	if !strings.Contains(string(out), "upgraded") {
		t.Errorf("wait does not tell of the upgrade:\n%s", out)
	}
}
