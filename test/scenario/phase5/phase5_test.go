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

// With Fleet Mode on. What the file's own lines cannot say is held here: each
// group was collected as a merge that carries its trailer and keeps its
// tasks' commits, and when the job is closed no agent of a fleet is left in
// a tab, the workers a leader could not close itself among them.
func TestAJobWithFleetModeOn(t *testing.T) {
	p := play(t, "fleet-mode-on.scn")
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", p.Root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	merges := git("log", "--merges", "--format=%s / %(trailers:key=Whaleshark-Task,valueonly,separator=%x20)", "whaleshark/r1")
	if merges != "G5: Bill the taxes / r1/G5\nG1: Build billing / r1/G1\n" {
		t.Errorf("the groups collected on the job's branch:\n%s", merges)
	}
	if work := git("log", "--no-merges", "--format=%s", "main..whaleshark/r1"); work != "B2: Invoices\nB1: Refunds\n" {
		t.Errorf("the tasks' commits in what was collected:\n%s", work)
	}
	snap, err := p.Terms().Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, pane := range snap.Panes {
		if !pane.Dock && !pane.Ours && pane.ID != p.Lead && pane.ID != p.Own {
			left = append(left, pane.Label)
		}
	}
	// One tab stays, as in any run: that of the leader whose program ended, to be looked at.
	if len(left) != 1 || left[0] != "search" {
		t.Errorf("tabs left when the job is closed: %q", left)
	}
}
