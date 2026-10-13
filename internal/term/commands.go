// Package term is the terminal layer: raw mode, keys, the mouse and pasted
// text in, a grid of cells out, of which only what changed is sent. It knows
// nothing about tasks or agents. The size it works with is the one the
// terminal reports, which in a pane of the keeper's is the pane's whole place.
package term

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Plug binds nothing: the layer has no command of its own.
func Plug(*contract.Kit) {}
