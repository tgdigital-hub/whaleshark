package rules

import (
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Phase 6 owes here: the eight rules below, whose words are in
// contract.Rules, and with them: Answer refusing an item of a cause
// contract.PersonOnly names to all but the person; Use writing what such an
// answer decides; Resume and the end of a hold adding to Attempt.Rested;
// Progress counting Progress.Same; Checked counting Task.CheckFails; Synced
// counting a group. Nothing calls these until they are built.
var notBuilt = refuse(contract.ExitFailed, "not_built", "%v", contract.ErrNotBuilt)

func (Rules) Planned(*contract.State, []string, string, *contract.Origin, time.Time) *contract.Refusal {
	return notBuilt
}
func (Rules) Protect(*contract.State, string, string, []string, time.Time) bool { return false }
func (Rules) Landable(*contract.State, string, []string) error                  { return nil }
func (Rules) Spend(*contract.State, string, time.Duration, int, time.Time) string {
	return ""
}
func (Rules) Hold(up, low *contract.State, now time.Time) bool { return false }
func (Rules) Going(*contract.State, string, bool, contract.CtxFile, contract.ProjectFile, time.Duration, time.Time) (changed, up bool) {
	return false, false
}
func (Rules) Passed(up, low *contract.State, attempt string, now time.Time) error { return nil }
func (Rules) SetAsked(*contract.State, contract.SetAsk, string, time.Time) (string, error) {
	return "", notBuilt
}
