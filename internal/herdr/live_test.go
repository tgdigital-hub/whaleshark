package herdr

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// TestLive runs the adapter against a real herdr. It changes things, so it
// runs only when HERDR_LIVE_TEST=1 and both HERDR_SOCKET_PATH and
// HERDR_CONFIG_PATH point at a throwaway session of the tester's own, with
// one workspace open; it refuses herdr's default session. With
// HERDR_LIVE_AGENT set to an agent kind, and HERDR_LIVE_CWD to a folder that
// agent trusts, it also starts a real agent there and one in a folder the
// agent does not trust, and prompts neither.
func TestLive(t *testing.T) {
	if os.Getenv("HERDR_LIVE_TEST") != "1" {
		t.Skip("set HERDR_LIVE_TEST=1 with a throwaway herdr session to run this")
	}
	k := contract.NewKit()
	k.Platform = platform{}
	a := New(k)
	st, err := a.status(context.Background())
	if err != nil || !st.Server.Running || !strings.Contains(st.Server.Socket, "sessions") ||
		st.Server.Socket != os.Getenv("HERDR_SOCKET_PATH") || os.Getenv("HERDR_CONFIG_PATH") == "" {
		t.Fatalf("not a running throwaway session with its own settings file: %+v, %v", st, err)
	}
	cwd := t.TempDir()
	if dir := os.Getenv("HERDR_LIVE_CWD"); dir != "" {
		cwd = dir
	}
	herdr := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("herdr", args...).CombinedOutput(); err != nil {
			t.Fatalf("herdr %v: %v: %s", args, err, out)
		}
	}

	pic, events, err := a.Events(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// settle applies what arrives until the picture built from events equals a snapshot.
	settle := func(what string) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			now, err := a.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if slices.Equal(pic.Panes, now.Panes) {
				return
			}
			select {
			case e, open := <-events:
				if !open {
					t.Fatalf("%s: the event connection closed", what)
				}
				t.Logf("%s: %s %+v", what, e.Kind, e.Pane)
				Apply(pic, e)
			case <-deadline:
				for i := range max(len(pic.Panes), len(now.Panes)) {
					if i >= len(pic.Panes) || i >= len(now.Panes) || pic.Panes[i] != now.Panes[i] {
						t.Errorf("%s: pane %d differs:\nevents: %+v\nherdr:  %+v", what, i, pic.Panes[min(i, len(pic.Panes)-1)], now.Panes[min(i, len(now.Panes)-1)])
					}
				}
				t.FailNow()
			}
		}
	}

	tab, err := a.TabCreate(cwd, "live test", []string{"WHALESHARK_LIVE=yes"})
	if err != nil || tab.Label != "live test" || tab.Focused || tab.Terminal == "" {
		t.Fatalf("tab create: %+v, %v", tab, err)
	}
	defer a.TabClose(tab.Tab)
	settle("a new tab")
	if err := a.TabRename(tab.Tab, "--focus"); err != nil {
		t.Fatal(err)
	}
	settle("a tab renamed to something that looks like an option")
	side, err := a.Split(tab.ID, "right", 0.7)
	if err != nil || side.Tab != tab.Tab {
		t.Fatalf("split: %+v, %v", side, err)
	}
	settle("a split")
	if err := a.Resize(side.ID, "left", 0.02); err != nil {
		t.Fatal(err)
	}
	if err := a.Swap(tab.ID, side.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.Run(tab.ID, []string{"env"}); err != nil {
		t.Fatal(err)
	}
	for range 100 {
		if screen, _ := a.Screen(tab.ID); strings.Contains(screen, "WHALESHARK_LIVE=yes") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if screen, err := a.Screen(tab.ID); !strings.Contains(screen, "WHALESHARK_LIVE=yes") {
		t.Errorf("the tab's own variable did not reach its shell: %v\n%s", err, screen)
	}

	// An agent's states, reported the way herdr's own hook for an agent reports them.
	for _, state := range []string{"working", "blocked", "idle"} {
		herdr("pane", "report-agent", side.ID, "--source", "whaleshark:test", "--agent", "claude", "--state", state)
		settle("an agent " + state)
	}
	herdr("pane", "report-agent", side.ID, "--source", "whaleshark:test", "--agent", "claude", "--state", "blocked")
	if err := a.Point(side.ID, contract.PointMail, ""); Code(err) != "agent_blocked" {
		t.Errorf("a pointer to an agent at a prompt: %v", err)
	}
	herdr("pane", "release-agent", side.ID, "--source", "whaleshark:test", "--agent", "claude")
	settle("the agent gone")
	if err := a.TabFocus(tab.Tab); err != nil {
		t.Fatal(err)
	}
	settle("the focus moved")

	if kind := os.Getenv("HERDR_LIVE_AGENT"); kind != "" {
		if err := a.AgentStart("whaleshark-live", kind, side.ID, nil, 60*time.Second); err != nil {
			t.Fatalf("agent start: %v", err)
		}
		settle("a real agent started")
		if i := slices.IndexFunc(pic.Panes, func(p contract.Pane) bool { return p.ID == side.ID }); pic.Panes[i].Name != "whaleshark-live" {
			t.Errorf("the events did not bring the agent's name: %+v", pic.Panes[i])
		}
		strange, err := a.TabCreate(t.TempDir(), "not trusted", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.AgentStart("whaleshark-strange", kind, strange.ID, nil, 60*time.Second); !errors.Is(err, contract.ErrAgentNotReady) {
			t.Errorf("an agent in a folder it does not trust: %v", err)
		}
		a.TabClose(strange.Tab)
		settle("the second agent's tab closed")
	}

	if err := a.PaneClose(side.ID); err != nil {
		t.Fatal(err)
	}
	settle("a pane closed")
	if err := a.PaneClose(side.ID); Code(err) != "pane_not_found" {
		t.Errorf("closing it again: %v", err)
	}
	if err := a.AgentStart("x", "claude", tab.ID, nil, time.Second); Code(err) != "invalid_agent_timeout" {
		t.Errorf("a start timeout herdr refuses: %v", err)
	}

	before, _ := os.ReadFile(os.Getenv("HERDR_CONFIG_PATH"))
	if err := a.SetKeys(seven); err != nil {
		t.Errorf("herdr refuses the shortcuts: %v", err)
	}
	if reason, delivery, err := a.Notify("a title", "a body", false); err != nil || reason == "" || delivery == "" {
		t.Errorf("notify: %q %q %v", reason, delivery, err)
	} else {
		t.Logf("notify: reason %q, delivery %q", reason, delivery)
	}
	if err := a.SetKeys(nil); err != nil {
		t.Error(err)
	}
	if after, _ := os.ReadFile(os.Getenv("HERDR_CONFIG_PATH")); string(after) != string(before) {
		t.Errorf("the settings file is not as it was:\n%q\n%q", before, after)
	}

	if err := a.TabClose(tab.Tab); err != nil {
		t.Fatal(err)
	}
	settle("the tab closed")
	if v, err := a.Version(); err != nil || v == "" {
		t.Errorf("version %q, %v", v, err)
	}
}
