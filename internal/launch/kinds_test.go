package launch

import (
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// A task that changes no code is shown at work by what it does.
func TestAKindIsShownAtWork(t *testing.T) {
	p := evening(t, 3, func(f *testkit.Fixture) {
		f.State.Tasks["R1"].Kind, f.State.Tasks["R2"].Kind = contract.WorkReadonly, contract.WorkWatch
		f.State.Tasks["R2"].LimitMinutes = 120
	})
	for _, id := range []string{"R1", "R2", "R3"} {
		p.Double.Script(id+".1", "hang")
		must(t, p.Command(scenario.Orch, "start", id), 0, "working ("+id+".1)")
	}
	for id, want := range map[string]string{"R1": "investigating", "R2": "watching"} {
		if got := card(t, p, id); !strings.HasSuffix(got, " "+want) {
			t.Errorf("%s reads %q", id, got)
		}
	}
	if got := card(t, p, "R3"); strings.Contains(got, "investigating") || strings.Contains(got, "watching") {
		t.Errorf("a task that changes code reads %q", got)
	}
}
