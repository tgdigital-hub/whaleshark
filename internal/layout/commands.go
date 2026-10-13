// Package layout is a tab as a tree of splits: sizes in cells, the tab row, the
// dividing lines, and what a click landed on. A tree of splits with shares is
// the ordinary way to divide a window; everything here is written from that
// idea and from the design's own pictures.
package layout

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Plug binds nothing: the keeper holds a Layout and calls it.
func Plug(*contract.Kit) {}
