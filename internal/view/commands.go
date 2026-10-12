// Package view builds the one view model, which the panes, the plain text
// and the page all print, and prints it as plain text.
package view

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Phase 5 owes here: the kit's Side, the left section's model, built from a
// contract.SideInput as contract.Side words it and proven on testkit.Fleets
// and on twenty and two hundred agents; in a run of contract.ModeFleets one
// card a fleet (Card.Fleet), View.Limits and an item's Fleet; View.Tools
// where more than one tool is wired in; and the same in status, show and
// catchup, with every printer equal.

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) {
	k.View = Build
	k.Handle("status", status)
	k.Handle("graph", graph)
	k.Handle("log", messages)
	k.Handle("show", show)
	k.Handle("jump", jump)
	k.Handle("catchup", catchup)
}
