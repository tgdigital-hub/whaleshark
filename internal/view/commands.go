// Package view builds the one view model, which the panes, the plain text
// and the page all print, and prints it as plain text.
package view

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Phase 7 owes here: the side model of contract.SideVersion 2. Workspaces
// from Terms.Spaces and a tab's from Pane.Workspace, no folder asked; a run
// in the workspace of its lead agent's tab; Need, Tab, Tabs, Gone, More,
// Split (contract.SplitRow), Job, Marks, Closed; each row's and workspace's
// Offer by contract.OfferRows, with "stop" and "close" no longer among a
// row's Actions and "answer" first on a row that has an Item. testkit's
// Spaces().Side and Fleets().Side7 must come out as written.

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) {
	k.View, k.Side, k.PlanText = Build, Side, PlanText
	k.Handle("status", status)
	k.Handle("graph", graph)
	k.Handle("log", messages)
	k.Handle("show", show)
	k.Handle("jump", jump)
	k.Handle("catchup", catchup)
}
