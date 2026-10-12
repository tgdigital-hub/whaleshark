// Package layout is a tab as a tree of splits: sizes in cells, the tab row, the
// dividing lines, and what a click landed on. A tree of splits with shares is
// the ordinary way to divide a window; everything here is written from that
// idea and from the design's own pictures.
package layout

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Phase 5 owes here: the dock's place: a width in cells and a side. The tab
// row and every tab begin beside it, Places gives it first, a cell in it is
// on a pane and its edge on a line, and a drag of that edge keeps it between
// contract.SideMin and contract.SideMax and never under what contract.Fit
// leaves the conversation.

// Plug binds nothing: the keeper holds a Layout and calls it.
func Plug(*contract.Kit) {}
