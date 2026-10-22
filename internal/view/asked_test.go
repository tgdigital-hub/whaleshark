package view

import (
	"slices"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
)

// billing is the run of the morning's fleet billing, as one caller sees it.
func billing(f *testkit.ThreeFleets, by contract.CallerKind) contract.ViewInput {
	in := f.View
	in.State, in.Fleets, in.Caller = f.View.Fleets["billing"], nil, by
	return in
}

// lines is every line a view tells its caller to run, card by card and item by item.
func lines(v *contract.View) (out []string) {
	for _, s := range v.Sections {
		for _, c := range s.Cards {
			out = append(out, c.Next...)
		}
	}
	for _, it := range v.Items {
		out = append(out, it.Next...)
	}
	return out
}

// A fleet leader is the lead agent of its fleet's run: status tells it the
// lines it tells an orchestrator there, and a worker of that run none of them.
func TestALeaderReadsItsFleetAsItsLeadAgent(t *testing.T) {
	f := testkit.Fleets()
	lead, leader, worker := lines(Build(billing(f, contract.Orchestrator))), lines(Build(billing(f, contract.Leader))), lines(Build(billing(f, contract.Worker)))
	if len(lead) == 0 || !slices.Equal(lead, leader) || slices.Equal(lead, worker) {
		t.Errorf("the lead agent is told\n%q\na leader\n%q\na worker\n%q", lead, leader, worker)
	}
}

// Whether Stop all holds an agent is its tool's form of hooks, not its
// tool's name: a tool of the person's own that is Claude Code pointed
// elsewhere is held as Claude Code is, and one with no hooks is not.
func TestAToolIsHeldByItsFormOfHooks(t *testing.T) {
	for hooks, want := range map[string]string{contract.HooksClaude: contract.WordFinishing, contract.HooksNone: contract.WordStillRunning} {
		f := testkit.Fleets()
		in := billing(f, contract.Human)
		in.Tools = []contract.Tool{{Name: "mine", Run: []string{"/opt/mine/bin/mine"}, Hooks: hooks, Mine: true}}
		in.State.Run.Paused = &contract.Pause{At: in.Now}
		a := in.State.Attempts["T3.1"]
		a.Agent.Kind, a.Gated = "mine", true
		if c := cardOf(Build(in), "T3"); c == nil || c.Look != contract.LookPaused || c.Word != want {
			t.Errorf("a paused agent of a tool with the hooks %q reads %+v, want the word %q", hooks, c, want)
		}
	}
}

// The one warning line of every screen is on the left section too, worded
// by the contract from what the person changed and has not looked at since.
func TestTheSideWarnsOfAChangedTool(t *testing.T) {
	f := testkit.Fleets()
	if w := Side(f.In).Warn; w != "" {
		t.Errorf("nothing changed, and the side warns %q", w)
	}
	f.In.Changed = []string{"tools.hermit.run", "tools.hermit.env.KEY"}
	if w := Side(f.In).Warn; w != "agent tool changed: hermit" {
		t.Errorf("the side warns %q", w)
	}
	f.In.Terms, f.In.Projects = &contract.Snapshot{}, nil
	if w := Side(f.In).Warn; w != "agent tool changed: hermit" {
		t.Errorf("with no workspace at all the side warns %q", w)
	}
}
