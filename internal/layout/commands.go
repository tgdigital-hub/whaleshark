// Package layout is a tab as a tree of splits: sizes in cells, the tab row, the
// dividing lines, and what a click landed on. A tree of splits with shares is
// the ordinary way to divide a window; everything here is written from that
// idea and from the design's own pictures.
package layout

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Phase 7 owes here: the line that is lit or held and its label, and a line
// put back to what its split was opened with.

// Plug binds nothing: the keeper holds a Layout and calls it.
func Plug(*contract.Kit) {}
