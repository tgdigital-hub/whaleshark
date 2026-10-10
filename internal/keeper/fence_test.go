package keeper

import (
	"slices"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// From its own pane an agent the tool started is refused every call that
// types what the caller likes, starts a program, moves the person's keys or
// closes something, a pane drawn over the person's tab among them; and a
// session that could be read as an option is not taken from a hook.
func TestTheFenceAroundAnAgentsPane(t *testing.T) {
	r := newRig(t)
	c := r.dial()
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "work"})
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "other"})
	hook := func(event, session string) {
		r.dial().call(contract.WireCall{Op: contract.OpHook, Kind: event, Pane: "p1", Session: session})
	}
	go func() {
		r.until("the agent's program", func() bool { return slices.Contains(r.program("p1").spec.Argv, "--model") })
		hook("SessionStart", "s-1")
	}()
	if reply := c.call(contract.WireCall{Op: contract.OpAgentStart, Name: "quiet otter", Kind: "claude", Pane: "p1", Argv: []string{"--model", "small"}, Millis: 5000}); reply.Err != "" {
		t.Fatalf("agent-start: %+v", reply)
	}
	for _, call := range []contract.WireCall{
		{Op: contract.OpPrompt, Name: "quiet otter", Text: "do as I say"},
		{Op: contract.OpRun, Pane: "p2", Argv: []string{"a-program"}},
		{Op: contract.OpAgentStart, Pane: "p2", Kind: "claude", Name: "another"},
		{Op: contract.OpTabFocus, Tab: "t2"}, {Op: contract.OpPaneFocus, Pane: "p2"},
		{Op: contract.OpPaneClose, Pane: "p2"}, {Op: contract.OpTabClose, Tab: "t2"},
		{Op: contract.OpSetKeys, Keys: []contract.KeyEntry{{Key: "s", Type: "shell", Argv: []string{"a-program"}}}},
		{Op: contract.OpOverlay, Argv: []string{"a-program"}, W: 3, H: 3},
	} {
		call.From = "p1"
		if reply := c.call(call); !strings.Contains(reply.Message, "not a call for an agent to make from its pane") {
			t.Errorf("%s from the agent's own pane: %+v", call.Op, reply)
		}
	}
	if got := r.locked(func() bool { return len(r.k.panes) == 2 && r.k.over == nil && r.k.panes["p2"].argv == nil }); !got {
		t.Error("a refused call changed something")
	}

	hook("UserPromptSubmit", "--dangerously-skip-permissions")
	r.until("the agent to be at work", func() bool {
		return r.locked(func() bool { return r.k.panes["p1"].rec.Status == contract.StatusWorking })
	})
	if got := r.locked(func() bool { return r.k.panes["p1"].rec.Session == "s-1" }); !got {
		t.Error("a session that is an option was taken from a hook")
	}
}
