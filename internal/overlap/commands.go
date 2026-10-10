// Package overlap finds the files two tasks both change before they collide.
// It is not built yet; its owner replaces this file.
//
// It puts a contract.Overlap into the kit and binds two commands, overlap and
// sync. A scan touches no branch, index or file of the project and writes
// nothing into the record: its findings go to Rules.Found, which keeps them
// on the run, knows the new ones and raises their events once. Its tests use
// a real repository, testkit.Repo.
package overlap

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(*contract.Kit) {}
