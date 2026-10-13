// Package layout is a tab as a tree of splits: sizes in cells, the tab row, the
// dividing lines, and what a click landed on. A tree of splits with shares is
// the ordinary way to divide a window; everything here is written from that
// idea and from the design's own pictures.
package layout

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Phase 7 owes here, first: the workspaces with the tab each shows and which
// is in front; on a tab its workspace, its group, whether it is the group's
// first and whether a person named it; the tab row of the workspace in
// front on one line or two, with a group's count and what waits in it;
// OnGroup answered by At; a file held to: every tab's workspace exists,
// every workspace has a tab, one is in front. Then: the line that is lit or
// held and its label, a line put back to what its split was opened with.

// Plug binds nothing: the keeper holds a Layout and calls it.
func Plug(*contract.Kit) {}
