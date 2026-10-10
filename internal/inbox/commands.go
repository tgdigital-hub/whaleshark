// Package inbox holds the two things that keep a run moving while nobody
// types: wait, which hands the lead agent its events, and the sweep, which
// holds the record against the terminals' picture.
package inbox

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// The files of a run's folder this package names.
const (
	stateFile = "state.json"
	waitLock  = "wait.lock"
	stampFile = "sweep.at"
)

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) {
	k.Sweeper = sweeper{k}
	k.Handle("wait", wait)
}
