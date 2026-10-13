package keeper

import (
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Who reaches whose tab through the fence's one way: a worker not its
// leader's tab, not a neighbour worker's and not a tab of another fleet; a
// leader not the tab it was itself started in; nobody the dock; and the
// calls an agent's pane may make about any tab (a new tab, a split) leave
// the person's keys and the tab that shows where they were.
func TestAWorkerReachesNoTabButItsOwn(t *testing.T) {
	r := newRig(t)
	c := r.dial()
	fence := "not a call for an agent to make from its pane"
	open := func(from string) contract.Pane {
		return *c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "opened", From: from}).Pane
	}
	start := func(at contract.Pane, name, from string) {
		t.Helper()
		// Started it is, whether or not its program was seen ready in no time at all.
		if reply := c.call(contract.WireCall{Op: contract.OpAgentStart, Pane: at.ID, Kind: "claude", Name: name, From: from}); strings.Contains(reply.Message, "not a call") {
			t.Fatalf("starting %s: %+v", name, reply)
		}
	}
	you := open("") // the lead agent's tab, or a person's
	leader, other := open(you.ID), open(you.ID)
	start(leader, "leader one", you.ID)
	start(other, "leader two", you.ID)
	worker, sibling, theirs := open(leader.ID), open(leader.ID), open(other.ID)
	start(worker, "worker a", leader.ID)
	start(sibling, "worker b", leader.ID)
	start(theirs, "worker c", other.ID)
	four := func(at contract.Pane, name string) []contract.WireCall {
		return []contract.WireCall{{Op: contract.OpAgentStart, Pane: at.ID, Kind: "claude", Name: "mine now"}, {Op: contract.OpPrompt, Name: name, Text: "do as I say"},
			{Op: contract.OpPaneClose, Pane: at.ID}, {Op: contract.OpTabClose, Tab: at.Tab}, {Op: contract.OpTabGive, Tab: at.Tab, Target: worker.ID},
			{Op: contract.OpSay, Pane: at.ID, Kind: contract.WhereTyped, Text: "as the person"}, {Op: contract.OpRun, Pane: at.ID, Argv: []string{"a-program"}}}
	}
	for from, targets := range map[string]map[string][]contract.WireCall{
		worker.ID: {"its leader's tab": four(leader, "leader one"), "a neighbour worker's tab": four(sibling, "worker b"), "another fleet's worker": four(theirs, "worker c"),
			"the person's tab": four(you, ""), "its own tab, which its leader opened": four(worker, "worker a")},
		leader.ID: {"the tab it was started in": four(leader, "leader one"), "the other leader's tab": four(other, "leader two"), "the other fleet's worker": four(theirs, "worker c")},
	} {
		for where, calls := range targets {
			for _, call := range calls {
				call.From = from
				if reply := c.call(call); !strings.Contains(reply.Message, fence) {
					t.Errorf("%s from %s on %s: %+v", call.Op, from, where, reply)
				}
			}
		}
	}
	if !r.locked(func() bool {
		return len(r.k.panes) == 6 && r.k.panes[leader.ID].rec.Name == "leader one" && r.k.panes[sibling.ID].rec.Name == "worker b" && r.k.opened[sibling.Tab] == leader.ID && r.k.opened[leader.Tab] == you.ID
	}) {
		t.Error("a refused call changed something")
	}
	// What an agent's pane may ask about any tab moves no key of the person's.
	focus, shown := "", ""
	r.locked(func() bool { focus, shown = r.k.focus, r.k.lay.Tabs[r.k.lay.Active].ID; return true })
	c.call(contract.WireCall{Op: contract.OpSplit, Pane: you.ID, Dir: contract.Right, From: worker.ID})
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: contract.MainTab, From: worker.ID})
	c.call(contract.WireCall{Op: contract.OpTabRename, Tab: worker.Tab, Label: contract.MainTab, From: worker.ID})
	if !r.locked(func() bool { return r.k.focus == focus && r.k.lay.Tabs[r.k.lay.Active].ID == shown }) {
		t.Error("a split or a new tab asked for from an agent's pane moved the person's keys or the tab that shows")
	}
}
