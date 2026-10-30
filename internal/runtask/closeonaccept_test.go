package runtask_test

import (
	"context"
	"slices"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// In pr-per-task mode a task that changes no code has no pull request to wait
// for, and no merge will ever close its tab: with [tasks] close_on_accept its
// accept does.
func TestAReadonlyTaskClosesOnAcceptWithPullRequests(t *testing.T) {
	h := open(t, map[string]string{"R.1": "write-result | report done \"read\""})
	h.approve("[land]\nmode = \"pr-per-task\"\n[tasks]\nclose_on_accept = true\n")
	h.orch(0, "task", "add", "R", "a look", "--brief", "b.md", "--kind", "readonly")("ready")
	h.orch(0, "start", "R")()
	h.reported("R")
	s, _ := h.p.Record()
	pane := s.Attempts["R.1"].Place.Pane
	h.orch(0, "accept", "R")("R is done", "closed: ")
	snap, err := h.p.Kit.Terms.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(snap.Panes, func(p contract.Pane) bool { return p.ID == pane }) {
		t.Errorf("the tab %s stayed open", pane)
	}
}
