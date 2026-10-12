// Package view builds the one view model, which the panes, the plain text
// and the page all print, and prints it as plain text.
package view

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Phase 6 owes here: Kit.PlanText; the line "spent" from contract.Spent and
// SpendLimit; a card's stuck line (contract.StuckWhy); the items of phase 6's
// five causes worded once, with their buttons; log --export, text and --json,
// its file the argument after it; the first steps and the version line in the
// side model; contract.AgentChanged among the alerts.

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) {
	k.View, k.Side = Build, Side
	k.Handle("status", status)
	k.Handle("graph", graph)
	k.Handle("log", messages)
	k.Handle("show", show)
	k.Handle("jump", jump)
	k.Handle("catchup", catchup)
}
