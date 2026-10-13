// Package input is keys, the mouse and pasted text on their way in: the engine's own
// shortcuts, and everything else to the program in the pane.
package input

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Phase 7 owes here: the right button written on to a program that asked for
// the mouse; and five things routing decides: a right press on a tab or a
// group (layout.OnTab, layout.OnGroup) asks for its menu, a second press on
// a line within the one span of a double-click puts it back (the dock's
// edge too, which it folds no longer), the pointer came onto or left a
// line, the press after a click on a line puts it there, and a press
// beside a placed box closes it and reaches nothing else.

// Plug binds this package's part into the kit: nothing, because the keeper
// calls it directly and it needs nothing of the kit.
func Plug(*contract.Kit) {}
