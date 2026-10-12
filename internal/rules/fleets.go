package rules

import (
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Phase 5 owes here the six rules of Fleet Mode, as the contract's Rules
// words them: each stands here as a refusal until then, so that nothing of
// a fleet is written by half a rule. With them: AddTask places a group (a
// task with Fleet, in a run of contract.ModeFleets only, where every task
// is one; contract.RefusedNotFleets elsewhere), a fleet's name is held to a
// task's name and is unique among the run's fleets and the open tabs, and
// Allowed knows contract.Leader, which is bound to its fleet's run as an
// orchestrator is to its own.
var notFleets = refuse(contract.ExitFailed, "not_built", "Fleet Mode is %v.", contract.ErrNotBuilt)

func (Rules) Room(*contract.State, map[string]*contract.State, string, string, contract.PersonConfig, int) error {
	return notFleets
}

func (Rules) Lead(_, _ *contract.State, _ string, _ contract.Binding, _ string, _ time.Time) error {
	return notFleets
}

func (Rules) Park(_, _ *contract.State, _ string, _ time.Time) ([]string, error) {
	return nil, notFleets
}

func (Rules) Ended(_, _ *contract.State, _ bool, _ time.Time) error { return notFleets }

func (Rules) Twin(_, _ *contract.State, _ string, _ time.Time) (string, error) {
	return "", notFleets
}

func (Rules) Down(_, _ *contract.State, _ string, _ time.Time) (bool, error) {
	return false, notFleets
}
