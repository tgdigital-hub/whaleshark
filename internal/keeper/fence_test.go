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

	// A tab the agent's own pane opened is the one thing it may start an
	// agent in, type a prompt at and close: a fleet leader and its workers.
	// Another leader's tab, the lead agent's, a person's, and its own once a
	// person runs a program there are refused as before.
	fence := "not a call for an agent to make from its pane"
	open := func(from string) contract.Pane {
		return *c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "opened", From: from}).Pane
	}
	four := func(at contract.Pane, name string) []contract.WireCall {
		return []contract.WireCall{{Op: contract.OpAgentStart, Pane: at.ID, Kind: "claude", Name: name}, {Op: contract.OpPrompt, Name: name, Text: "go on"},
			{Op: contract.OpPaneClose, Pane: at.ID}, {Op: contract.OpTabClose, Tab: at.Tab}}
	}
	leads := open("p2") // p2 holds no agent of the tool's: the lead agent's pane, or a person's
	c.call(contract.WireCall{Op: contract.OpAgentStart, Pane: leads.ID, Kind: "claude", Name: "second leader", From: "p2"})
	others, own, taken := open(leads.ID), open("p1"), open("p1")
	c.call(contract.WireCall{Op: contract.OpAgentStart, Pane: others.ID, Kind: "claude", Name: "its worker", From: leads.ID})
	// Its last pane closed, a tab is gone: the fourth call is made on a second tab.
	for _, call := range append(four(own, "my worker")[:3], four(open("p1"), "")[3]) {
		call.From = "p1"
		if reply := c.call(call); strings.Contains(reply.Message, fence) {
			t.Errorf("%s on a tab the agent's pane opened itself: %+v", call.Op, reply)
		}
	}
	if !r.locked(func() bool {
		return r.k.panes[own.ID] == nil && r.k.lay.Tab(own.Tab) == nil && r.k.opened[own.Tab] == ""
	}) {
		t.Error("the agent's own tabs were not closed, or their opener is still on record")
	}
	c.call(contract.WireCall{Op: contract.OpAgentStart, Pane: taken.ID, Kind: "claude", Name: "taken over", From: "p1"})
	c.call(contract.WireCall{Op: contract.OpRun, Pane: taken.ID, Argv: []string{"a-program"}})
	for where, calls := range map[string][]contract.WireCall{
		"another leader's tab": four(others, "its worker"), "the lead agent's tab": four(leads, "second leader"),
		"a person's tab": four(contract.Pane{ID: "p2", Tab: "t2"}, "a person's"), "its own tab that a person's program took over": four(taken, "taken over"),
		// Naming a tab of its own beside a pane that is not in it opens nothing.
		"a pane named with a tab of its own": {{Op: contract.OpPaneClose, Pane: "p2", Tab: taken.Tab}},
	} {
		for _, call := range calls {
			call.From = "p1"
			if reply := c.call(call); !strings.Contains(reply.Message, fence) {
				t.Errorf("%s on %s: %+v", call.Op, where, reply)
			}
		}
	}
	// A tab changes hands by tab-give alone, and that is the lead agent's
	// pane's or the person's to call: a leader cannot give itself a
	// neighbour's tab, and a worker cannot give at all, its own tab neither.
	for who, call := range map[string]contract.WireCall{
		"a leader giving itself another leader's tab": {Op: contract.OpTabGive, Tab: others.Tab, Target: "p1", From: "p1"},
		"a leader giving itself a person's tab":       {Op: contract.OpTabGive, Tab: "t2", Target: "p1", From: "p1"},
		"a worker giving its own tab to itself":       {Op: contract.OpTabGive, Tab: others.Tab, Target: others.ID, From: others.ID},
		"a worker giving its tab to another leader":   {Op: contract.OpTabGive, Tab: others.Tab, Target: "p1", From: others.ID},
	} {
		if reply := c.call(call); !strings.Contains(reply.Message, fence) {
			t.Errorf("%s: %+v", who, reply)
		}
	}
	if !r.locked(func() bool {
		return r.k.panes["p2"].argv == nil && r.k.panes[others.ID].rec.Name == "its worker" && r.k.panes[leads.ID].rec.Name == "second leader" && r.k.panes[taken.ID] != nil &&
			r.k.opened[others.Tab] == leads.ID && r.k.opened["t2"] == ""
	}) {
		t.Error("a refused call changed something")
	}
	// The lead agent's pane hands the second leader's worker to the first: a
	// leader started again. From then on it is the first's to prompt and
	// close, and no longer the second's.
	if reply := c.call(contract.WireCall{Op: contract.OpTabGive, Tab: others.Tab, Target: "p1", From: "p2"}); reply.Err != "" {
		t.Fatalf("tab-give from the lead agent's pane: %+v", reply)
	}
	if reply := c.call(contract.WireCall{Op: contract.OpTabClose, Tab: others.Tab, From: leads.ID}); !strings.Contains(reply.Message, fence) {
		t.Errorf("the leader before closed a tab that was given away: %+v", reply)
	}
	if reply := c.call(contract.WireCall{Op: contract.OpTabClose, Tab: others.Tab, From: "p1"}); reply.Err != "" {
		t.Errorf("a tab it was given: %+v", reply)
	}

	hook("UserPromptSubmit", "--dangerously-skip-permissions")
	r.until("the agent to be at work", func() bool {
		return r.locked(func() bool { return r.k.panes["p1"].rec.Status == contract.StatusWorking })
	})
	if got := r.locked(func() bool { return r.k.panes["p1"].rec.Session == "s-1" }); !got {
		t.Error("a session that is an option was taken from a hook")
	}
}

// Who opened a tab is in the layout file: after a restart of the keeper a
// leader that came back in its conversation still closes its worker's tab,
// and nobody else's.
func TestATabsOpenerIsKeptOverARestart(t *testing.T) {
	r := newRig(t)
	c := r.dial()
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "leader"})
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "you"})
	c.call(contract.WireCall{Op: contract.OpAgentStart, Name: "quiet otter", Kind: "claude", Pane: "p1"})
	c.call(contract.WireCall{Op: contract.OpHook, Kind: "SessionStart", Pane: "p1", Session: "s-1"})
	r.until("the session in the record", func() bool { return r.locked(func() bool { return r.k.panes["p1"].rec.Session == "s-1" }) })
	own := c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "worker", From: "p1"}).Pane
	r.again()
	c = r.dial()
	if !r.locked(func() bool {
		return r.k.panes["p1"].rec.Name == "quiet otter" && r.k.opened[own.Tab] == "p1" && len(r.k.opened) == 1
	}) {
		t.Fatalf("after the restart the keeper holds %v", r.k.opened)
	}
	if reply := c.call(contract.WireCall{Op: contract.OpTabClose, Tab: "t2", From: "p1"}); reply.Err == "" {
		t.Errorf("a person's tab was closed from the agent's pane: %+v", reply)
	}
	if reply := c.call(contract.WireCall{Op: contract.OpTabClose, Tab: own.Tab, From: "p1"}); reply.Err != "" {
		t.Errorf("its own tab after the restart: %+v", reply)
	}
}
