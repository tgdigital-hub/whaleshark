// Package inbox holds the two things that keep a run moving while nobody
// types: wait, which hands the lead agent its events, and the sweep, which
// holds the record against the terminals' picture.
package inbox

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// stampFile holds when the run was last swept.
const stampFile = contract.SweepFile

// Phase 6 owes here: the sweep calls Rules.Going after Seen, and Rules.Passed
// with Store.Both where it says so; Rules.Spend from contract.Spent and
// SpendLimit, Rules.Hold for each fleet, then Kit.Hold; Rules.Use on the
// settled answers of the tool's own items, and contract.Granted for a setting;
// both leaders told of a clash across fleets.

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) {
	k.Sweeper = sweeper{k}
	k.Handle("wait", wait)
}
