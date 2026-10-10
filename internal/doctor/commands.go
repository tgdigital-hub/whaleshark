// Package doctor checks what the tool needs and what it left behind, one
// line a check, each with its fix. It is not built yet; its owner replaces
// this file.
//
// It binds one command, doctor. What it checks it asks through the kit: the
// keeper with Terms.Version, worktrees with Placement.Repair, port slots with
// contract.ReadSlots and FreeSlots, what was approved with contract.ReadTrust
// and a project's Held keys.
package doctor

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(*contract.Kit) {}
