package windows

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// boards are the panes of ours in a tab that show the fleet's board.
func boards(t *testing.T, p *scenario.Project, tab string) (ids []string) {
	t.Helper()
	terms := p.Terms()
	snap, err := terms.Snapshot(context.Background())
	if err != nil {
		return nil
	}
	for _, q := range snap.Panes {
		if text, _ := terms.Screen(q.ID); q.Ours && q.Tab == tab && strings.Contains(text, "FLEET") {
			ids = append(ids, q.ID)
		}
	}
	return ids
}

// TestTheFleetPaneOnTheRealKeeper is the fleet pane as a person meets it, on
// the real keeper in a window of 120 by 30 as Windows Terminal opens: the
// window shows the main agent's tab and the left section opens the board
// beside it with no job open, saying so; then a job's start places the
// panes, as `ui` does, and one board stands, which stays. Each pane is the
// program's own file started with the words of a pane, in a pseudo-console
// on Windows.
func TestTheFleetPaneOnTheRealKeeper(t *testing.T) {
	// The keeper and the left section in it begin in a folder of no project,
	// as a person's keeper started at log-on does.
	t.Chdir(t.TempDir())
	p := scenario.PrepareOn(t, scenario.Keeper, nil, nil)
	terms := p.Terms()
	main, err := terms.TabCreate(p.Root, "", p.Root, contract.MainTab, nil)
	if err == nil {
		err = terms.TabFocus(main.Tab)
	}
	if err != nil {
		t.Fatal(err)
	}
	window(t, p.Kit, 120, 30)
	if !soon(30*time.Second, func() bool { return len(boards(t, p, main.Tab)) == 1 }) {
		snap, _ := terms.Snapshot(context.Background())
		side, _ := terms.Screen("dock")
		t.Fatalf("the main agent's tab shows and no fleet opened beside it: %+v %+v\n%s", snap.Panes, snap.Spaces, side)
	}
	text, _ := terms.Screen(boards(t, p, main.Tab)[0])
	if !strings.Contains(text, "no job yet") {
		t.Errorf("the board with no job open shows:\n%s", text)
	}
	cmd := p.Command(scenario.Orch, "ui")
	cmd.Env = append(cmd.Env, contract.EnvPane+"="+main.ID)
	out, err := cmd.CombinedOutput()
	t.Logf("FACT ui, as a job's start places the panes: %v %s", err, out)
	if !soon(30*time.Second, func() bool { return len(boards(t, p, main.Tab)) == 1 }) {
		t.Fatalf("after a job's start the boards are %v", boards(t, p, main.Tab))
	}
	time.Sleep(3 * time.Second)
	if got := boards(t, p, main.Tab); len(got) != 1 {
		t.Errorf("the boards a few seconds on: %v", got)
	} else {
		w, h, _ := terms.Size(got[0])
		t.Logf("FACT the fleet pane is %dx%d in a window of 120x30", w, h)
	}
}
