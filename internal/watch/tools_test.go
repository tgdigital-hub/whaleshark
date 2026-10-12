package watch

import (
	"reflect"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// The descriptions compiled into the contract were written from this
// folder's table by hand. Before the table goes, every word of it is held
// against them: the same kinds, the same hooks, the same words on a screen,
// and a program called as its kind is, which is how a shell's pane knows it.
func TestTheBuiltInDescriptionsAreTheTable(t *testing.T) {
	built := contract.BuiltIn()
	if len(built) != len(kinds) {
		t.Fatalf("%d descriptions are compiled in, and the table has %d kinds", len(built), len(kinds))
	}
	for _, b := range built {
		k, known := kinds[b.Name]
		want := contract.ToolScreen{Ready: k.ready, Working: k.working, Trust: k.trust}
		if k.question != [2]string{} {
			want.Question = k.question[:]
		}
		if !known || b.Exact() != k.hooks || !reflect.DeepEqual(b.Screen, want) || len(b.Run) != 1 || b.Run[0] != b.Name {
			t.Errorf("%s: the description is %+v, the table has %+v (known: %v)", b.Name, b, k, known)
		}
	}
}
