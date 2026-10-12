// Package phase5 plays the whole of phase 5 once with scripted agents: a job
// with Fleet Mode off, where the left section, the two panes opened with the
// run and the layout's settings are all that is new, and a job with Fleet
// Mode on, from an orchestrator through its fleets' leaders to their workers
// and back. The suite of every scenario plays both files on the engine's
// double and on the real keeper and holds the two logs to each other.
package phase5

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/panes"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/view"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func TestMain(m *testing.M) { scenario.Main(m) }

// play runs one flow on the engine's double, with the real panes.
func play(t *testing.T, file string) *scenario.Project {
	t.Helper()
	bin, err := exec.LookPath("whaleshark")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(contract.EnvBin, bin)
	return scenario.Run(t, file, func(p *scenario.Project, name string, tm *term.Term) {
		for _, kv := range p.Env(p.Own) {
			k, v, _ := strings.Cut(kv, "=")
			t.Setenv(k, v)
		}
		p.Plug(view.Plug)
		panes.Run(name, tm, p.Kit, p.Root, "")
	})
}

func TestAJobWithFleetModeOff(t *testing.T) { play(t, "fleet-mode-off.scn") }
