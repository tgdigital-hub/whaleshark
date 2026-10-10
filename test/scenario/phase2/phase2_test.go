// Package phase2 plays the flows of phase 2 from end to end: a project that
// is a real git repository, each task in a copy of the code of its own, and
// the accepted work collected on the run's branch.
package phase2

import (
	"testing"

	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func TestMain(m *testing.M) { scenario.Main(m) }

func TestTasksInTheirOwnCopies(t *testing.T) { scenario.Run(t, "8c-own-copies.scn", nil) }
