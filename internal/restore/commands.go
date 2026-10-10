// Package restore is the layout file, and bringing every tab and pane back
// after a restart: what the keeper saves, how it is read back and put in
// order, a pane's last lines as text, and the text of the login's service
// entry that starts the keeper. It runs nothing itself: the keeper calls it.
// Written from the design's own description of a restart; the service entries
// from the systems' manuals, launchd.plist(5), systemd.service(5) and
// systemd.syntax(7).
package restore

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Plug binds this package's part into the kit: it has no command of its own.
func Plug(*contract.Kit) {}
