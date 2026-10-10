// Package integrate is the run's integration branch: where accepted work is
// collected, and how it reaches the main branch. It is not built yet; its
// owner replaces this file.
//
// It puts a contract.Integrator into the kit and binds one command, land. It
// writes nothing into a run's record but through the rules: Begin's answer
// and a landed tip go to Rules.Integrated, and accept hands the commit of
// Collect to Rules.Checked, which moves the tip. The run's own check is
// [land] check of contract.ReadProjectFile, which is empty until approved.
// Its tests use a real repository, testkit.Repo.
package integrate

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(*contract.Kit) {}
