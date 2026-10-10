// Package platform is the operating system. It is the only package that asks
// which system it runs on; the others reach it through contract.Platform.
package platform

import (
	"path/filepath"
	"runtime"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) {
	k.Platform = New(runtime.GOOS)
	contract.TrustHome = func() (string, error) {
		dirs, err := New(runtime.GOOS).Dirs()
		return filepath.Join(dirs.State, "trust"), err
	}
}
