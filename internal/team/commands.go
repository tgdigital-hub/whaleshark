// Package team is saved teams: a set of tasks kept as one file and added to
// a run in one step. It is not built yet; its owner replaces this file.
//
// It binds one command, team. A team adds its tasks with Rules.AddTask and
// starts nothing. A team file that comes with the project, in
// contract.TeamsDir, is used only when contract.ReadProjectFile does not
// name it in Held.
package team

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(*contract.Kit) {}
