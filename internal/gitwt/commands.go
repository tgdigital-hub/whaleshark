// Package gitwt is each task's own copy of the code: a git worktree, its
// setup and its removal. It is not built yet; its owner replaces this file.
//
// It puts one thing into the kit, a contract.Placement, whose comments say
// what each call owes. It reads the project's settings with
// contract.ReadProjectFile only, which hands out no key that was not approved
// (Held), and it writes nothing into a run's record: what it returns is
// recorded by its caller with Rules.SetWorktree. A task with no worktree
// (shared, or a folder of its own) is placed as contract.NoPlacement does.
// Its tests use a real repository, testkit.Repo.
package gitwt

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(*contract.Kit) {}
