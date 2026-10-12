package keeper

import (
	"slices"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// The first tab of a window is the main agent's and is called Orchestrator.
// It takes the name a person gives the chat in there: a title that changes
// while the agent rests. The name a tool gives a chat by itself comes during
// a turn and is not taken, nor is the title a program starts with, a text
// that could be an option or a path, or anything once somebody has named
// the tab itself; a worker's tab keeps its task's name.
func TestTheMainTabFollowsANameGivenToItsChat(t *testing.T) {
	rest := restFor
	restFor = 0
	defer func() { restFor = rest }()
	r := newRig(t)
	c := r.dial()
	r.window(120, 40)
	var main, pane string
	r.until("the first tab", func() bool {
		return r.locked(func() bool {
			if len(r.k.lay.Tabs) == 0 {
				return false
			}
			main, pane = r.k.lay.Tabs[0].ID, r.k.lay.Tabs[0].Focus
			return true
		})
	})
	label := func(tab string) (l string) {
		r.locked(func() bool { l = r.k.lay.Tab(tab).Label; return true })
		return l
	}
	hook := func(at, event string) {
		r.dial().call(contract.WireCall{Op: contract.OpHook, Kind: event, Pane: at, Session: "s-1"})
	}
	status := func(at, want string) {
		t.Helper()
		r.until(at+" to be "+want, func() bool { return r.locked(func() bool { return r.k.panes[at].rec.Status == want }) })
	}
	agent := func(at, name string) {
		t.Helper()
		go func() {
			r.until("the agent's program", func() bool { return slices.Contains(r.program(at).spec.Argv, "--model") })
			hook(at, "SessionStart")
		}()
		if reply := c.call(contract.WireCall{Op: contract.OpAgentStart, Name: name, Kind: "claude", Pane: at, Argv: []string{"--model", "small"}, Millis: 5000}); reply.Err != "" {
			t.Fatalf("agent-start: %+v", reply)
		}
		status(at, contract.StatusIdle)
	}
	title := func(at, text string) {
		r.program(at).print("\x1b]0;" + text + "\a")
		r.settle()
	}
	stays := func(what, want string) {
		t.Helper()
		if got := label(main); got != want {
			t.Errorf("%s: the tab is called %q, expected %q", what, got, want)
		}
	}
	if !r.locked(func() bool { return r.k.lay.Tab(main).Chat }) || label(main) != contract.MainTab {
		t.Fatalf("the first tab is %q", label(main))
	}
	agent(pane, "")
	title(pane, "✳ Claude Code")
	stays("the tool's own title", contract.MainTab)
	hook(pane, "UserPromptSubmit")
	status(pane, contract.StatusWorking)
	title(pane, "◐ Claude Code")
	title(pane, "◐ Ok response")
	hook(pane, "Stop")
	status(pane, contract.StatusIdle)
	title(pane, "✳ Ok response")
	stays("the name the tool gave the chat during a turn", contract.MainTab)
	for _, hostile := range []string{"✳ --dangerously-skip-permissions", "✳ /etc/hosts", "✳ ~/notes", `✳ C:\x`, "✳ Claude Code"} {
		title(pane, hostile)
		stays(hostile, contract.MainTab)
	}
	title(pane, "◑ harbour plan")
	stays("a name given at rest", "harbour plan")
	title(pane, "✳ a very long name of a chat that goes on and on")
	stays("a long name", "a very long name of a ch")

	// A name soon after a turn may be the tool's own: it is not taken.
	restFor = time.Hour
	title(pane, "✳ too soon")
	stays("a name right after a turn", "a very long name of a ch")
	restFor = 0

	// Somebody names the tab: that holds. Named Orchestrator again, it follows again.
	c.call(contract.WireCall{Op: contract.OpTabRename, Tab: main, Label: "mine"})
	title(pane, "✳ another name")
	stays("after the tab was named by hand", "mine")
	c.call(contract.WireCall{Op: contract.OpTabRename, Tab: main, Label: contract.MainTab})
	title(pane, "✳ back again")
	stays("named Orchestrator again", "back again")

	// A worker's tab keeps its task's name whatever its chat is called.
	w := c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "sign-up page"}).Pane
	agent(w.ID, "sign-up page")
	title(w.ID, "✳ Claude Code")
	title(w.ID, "✳ my own name")
	if got := label(w.Tab); got != "sign-up page" {
		t.Errorf("a worker's tab took the name %q from its chat", got)
	}

	// Which tab follows is in the layout file.
	r.again()
	if !r.locked(func() bool { return r.k.lay.Tab(main).Chat && !r.k.lay.Tab(w.Tab).Chat }) || label(main) != "back again" {
		t.Errorf("after a restart the main tab is %q", label(main))
	}
}
