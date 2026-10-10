// Package evidence is not built yet. Its owner replaces this file.
//
// It owes the kit contract.Evidence, three calls. List names what an attempt
// saved in its evidence folder (attempts/<attempt>/evidence under the run's
// folder, which the kit's Reader gives), oldest first, each with its size,
// its time and its kind by the file's first bytes: "png", "jpeg", "webp",
// or none. It stops at contract.EvidenceFiles files and
// contract.EvidenceBytes bytes, and lists nothing a link leads to outside
// the folder. Open opens the file at a place in that list through a handle
// that cannot leave the folder (os.Root), so a caller never gives a name.
// Answering says which of the ports it is given a site answers on now, by
// connecting to the loopback address and waiting a fifth of a second at
// most for all of them together. It binds no command and reads no record:
// which ports are live tasks' slots is its caller's to know.
package evidence

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(*contract.Kit) {}
