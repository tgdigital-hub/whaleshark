// Package input is keys, the mouse and pasted text on their way in: the engine's own
// shortcuts, and everything else to the program in the pane.
package input

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Plug binds this package's part into the kit: nothing, because the keeper
// calls it directly and it needs nothing of the kit.
func Plug(*contract.Kit) {}
