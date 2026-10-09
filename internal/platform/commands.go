// Package platform is not built yet. Its owner replaces this file.
package platform

import (
	// The file locks and file-change notices of this package come from this
	// module; naming it here keeps it a direct requirement until they are written.
	_ "golang.org/x/sys/cpu"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(*contract.Kit) {}
