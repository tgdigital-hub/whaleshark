// Package platform is the operating system. It is the only package that asks
// which system it runs on; the others reach it through contract.Platform.
package platform

import (
	"runtime"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) {
	k.Platform = New(runtime.GOOS)
	contract.LoginDirs = func() (contract.Dirs, error) { return New(runtime.GOOS).Dirs() }
}
