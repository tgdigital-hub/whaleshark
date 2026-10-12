package hook

import (
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// The descriptions compiled into the contract were written by hand from the
// tables of kinds. Before a table goes, each built-in is held to this
// folder's: it names the harness that stood under its own name, and it is
// held by Stop all exactly when that kind's gate was.
func TestTheBuiltInDescriptionsNameTheirHarness(t *testing.T) {
	built := contract.BuiltIn()
	if len(built) != len(harnesses) {
		t.Fatalf("%d descriptions are compiled in, and the table has %d harnesses", len(built), len(harnesses))
	}
	for _, b := range built {
		_, known := harnesses[b.Name]
		gate, gated := contract.Gates[b.Name]
		if !known || !gated || b.Hooks != b.Name || b.Held() != gate.Holds {
			t.Errorf("%s: hooks %q, held %v; the table knows it: %v, its gate holds: %v", b.Name, b.Hooks, b.Held(), known, gate.Holds)
		}
	}
}
