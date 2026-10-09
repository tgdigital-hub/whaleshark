// Package term is the terminal layer: raw mode, keys, the mouse and pasted
// text in, a grid of cells out, of which only what changed is sent. It knows
// nothing about tasks or agents. The size it works with is the one the
// terminal reports, which inside herdr is already the pane's inside: two
// columns and two rows less than the pane's place, because of herdr's frame.
package term

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Plug binds nothing: the layer has no command of its own.
func Plug(*contract.Kit) {}
