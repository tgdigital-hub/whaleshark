package dash

import (
	"net/http"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// orchestrated is a store whose one run is an orchestrator's: two fleets
// are named in it, and the run of one of them cannot be read.
type orchestrated struct {
	contract.Store
	run string
}

func (o orchestrated) Read(root, run string) (*contract.State, error) {
	switch run {
	case o.run:
		s, err := o.Store.Read(root, run)
		if err == nil {
			s.Run.Mode = contract.ModeFleets
			s.Run.Fleets = map[string]*contract.Fleet{"billing": {Run: "r90"}, "search": {Run: "r91"}}
		}
		return s, err
	case "r90":
		return &contract.State{Version: contract.StateVersion, Run: contract.Run{ID: run, Parent: &contract.Twin{Run: o.run, Fleet: "billing"}}}, nil
	}
	return nil, contract.ErrNoRun
}

// The view of an orchestrator's run is built with each fleet's own run and
// the person's limits; a fleet whose run cannot be read is left out, and a
// run that is no orchestrator's is built as it always was.
func TestAnOrchestratorsRunIsViewedWithItsFleets(t *testing.T) {
	b := evening(t).signIn()
	b.drawn()
	var in contract.ViewInput
	build := b.p.Kit.View
	b.p.Kit.View = func(i contract.ViewInput) *contract.View {
		in = i
		return build(i)
	}
	b.refused("the team", b.ask("GET", contract.RouteTeam, nil, nil), http.StatusOK)
	if in.State == nil || in.Fleets != nil || in.Person != (contract.PersonConfig{}) {
		t.Errorf("with Fleet Mode off the view was handed %d fleets and the limits %+v", len(in.Fleets), in.Person.Fleets)
	}
	b.p.Kit.Store = orchestrated{b.p.Kit.Store, in.State.Run.ID}
	b.refused("the team of an orchestrator's run", b.ask("GET", contract.RouteTeam, nil, nil), http.StatusOK)
	low := in.Fleets["billing"]
	if len(in.Fleets) != 1 || low == nil || low.Run.ID != "r90" || low.Run.Parent.Fleet != "billing" {
		t.Errorf("the fleets the view was handed: %+v", in.Fleets)
	}
	if want := contract.PersonDefaults().Fleets; in.Person.Fleets != want || want.Agents == 0 {
		t.Errorf("the limits the view was handed: %+v, the person's are %+v", in.Person.Fleets, want)
	}
}
