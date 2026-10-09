// Package store keeps a run on disk: one file per run, one lock per project,
// and every change a load, a rule and a replace under that lock (6.1, 6.9).
// It reaches the operating system through contract.Platform; what it adds is
// the flush to disk.
package store

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) { k.Store = &Store{k.Platform} }
