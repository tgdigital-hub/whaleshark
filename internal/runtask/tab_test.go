package runtask

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/test/fakeengine"
)

// The tab a run's lead agent sits in is called Orchestrator where nobody
// has named it, with Fleet Mode on or off; a tab with a name keeps it.
func TestTheLeadAgentsTabIsCalledOrchestrator(t *testing.T) {
	double := fakeengine.New()
	defer double.Close()
	bare, _ := double.TabCreate("", "", t.TempDir(), "", nil)
	named, _ := double.TabCreate("", "", t.TempDir(), "harbour plan", nil)
	k := contract.NewKit()
	k.Terms = double
	mainTab(k, bare.ID)
	mainTab(k, named.ID)
	snap, err := double.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range snap.Panes {
		got[p.ID] = p.Label
	}
	if got[bare.ID] != contract.MainTab || got[named.ID] != "harbour plan" {
		t.Errorf("the tabs are called %q and %q", got[bare.ID], got[named.ID])
	}
}

// A name compared with the run's tasks only is said so, unless no engine
// runs: then no tab is open, and none can hold the name.
func TestTheTabsNoteOnlyWhereATabCouldHoldTheName(t *testing.T) {
	for err, said := range map[error]bool{
		fmt.Errorf("%w: dial unix: no such file", contract.ErrEngineUnreachable): false,
		errors.New("the reply was cut short"):                                    true,
	} {
		var out bytes.Buffer
		unasked(&contract.Call{Err: &out}, err)
		if got := out.String() != ""; got != said {
			t.Errorf("after %q the note was said: %v\n%s", err, got, out.String())
		}
	}
}
