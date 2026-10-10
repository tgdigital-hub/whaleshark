// Package view builds the one view model, which the panes, the plain text
// and the page all print, and prints it as plain text.
package view

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) {
	k.View = Build
	k.Handle("status", status)
	k.Handle("show", show)
	k.Handle("jump", jump)
	k.Handle("catchup", catchup)
}
