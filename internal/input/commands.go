// Package input is keys, the mouse and pasted text on their way in: the engine's own
// shortcuts, and everything else to the program in the pane.
package input

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Phase 5 owes here: three keys after the command key: b folds the dock
// (contract.DockFold), g gives it the keys and, pressed again, gives them
// back, s opens the settings screen over the tab. A click in the dock goes
// to its program and moves no keys.

// Plug binds this package's part into the kit: nothing, because the keeper
// calls it directly and it needs nothing of the kit.
func Plug(*contract.Kit) {}
