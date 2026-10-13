package launch

import (
	"slices"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// live counts the agents at work over every run of the project: a leader
// that holds two groups is one.
func (m *morning) live() (n int) {
	runs, _ := m.p.Kit.Store.Runs(m.p.Root)
	for _, id := range runs {
		n += live(m.read(id))
	}
	return n
}

// stopAll is a job of two fleets, one of whose leaders holds two groups,
// stopped in one step by the person: no agent of it and no leader tries it
// with any luck, nothing of the job is closed, and every fleet starts again.
func stopAll(t *testing.T, on scenario.Backend) []string {
	m := begin(t, on)
	for _, g := range []string{"G1", "G2", "G5"} {
		m.do(scenario.Orch, 0, []string{"(" + g + ".1)"}, "", "start", g)
	}
	for _, id := range []string{"T1", "T2"} {
		m.do("G1.1", 0, nil, asLeader, "task", "add", id, "bill "+id, "--brief", "brief.md", "--check", "none")
	}
	m.do("G1.1", 0, []string{"(T1.1)", "(T2.1)"}, asLeader, "start", "T1", "T2")
	m.do("G2.1", 0, nil, asLeader, "task", "add", "T1", "find T1", "--brief", "brief.md", "--check", "none")
	m.do("G2.1", 0, []string{"(T1.1)"}, asLeader, "start", "T1")
	labels := []string{"billing", "search", "bill T1", "bill T2", "find T1"}
	tabs := func() (n int) {
		for _, label := range labels {
			n += m.tabs(label)
		}
		return n
	}
	if m.live() != 5 || tabs() != 5 {
		t.Fatalf("%d agents are at work in %d tabs, expected 5 in 5", m.live(), tabs())
	}

	// What an agent could try: the lead agent, a leader and a worker, each
	// plainly and as the person. A leader renames no task of its fleet.
	for _, who := range []string{scenario.Orch, "G1.1", "r2/T1.1"} {
		env := map[string]string{"G1.1": asLeader}[who]
		m.do(who, 5, nil, env, "stop", "--all", "--json")
		m.do(who, 5, []string{"not_human"}, env, "stop", "--all", "--human", "--json")
	}
	m.do(scenario.Orch, 5, []string{"human_only", "--all is the person's to give"}, "", "stop", "--all", "--json")
	m.do("G1.1", 5, []string{contract.RefusedLeader, "A fleet leader renames no task"}, asLeader, "task", "edit", "T1", "--name", "mine", "--json")
	m.do(scenario.Human, 2, []string{"stop takes one task, or --all."}, "", "stop", "--all", "G1")
	m.do(scenario.Human, 2, []string{"stop takes one task, or --all."}, "", "stop")
	if low := m.read("r2"); m.live() != 5 || tabs() != 5 || low.Tasks["T1"].Name != "bill T1" {
		t.Fatalf("a refused command changed something: %d at work, %d tabs, T1 is %q", m.live(), tabs(), low.Tasks["T1"].Name)
	}

	out := m.do(scenario.Human, 0, []string{"billing stopped (G1.1); its tab: kept", "search stopped (G2.1); its tab: closed", "billing stopped (G5.1); its tab: closed"}, "", "stop", "--all")
	if n := strings.Count(out, "The fleet is parked"); n != 2 {
		t.Errorf("%d fleets were parked, expected 2:\n%s", n, out)
	}
	up := m.read("r1")
	for _, name := range []string{"billing", "search"} {
		f := up.Run.Fleets[name]
		if low := m.read(f.Run); f.Pane != "" || !f.EndedAt.IsZero() || !low.Run.ClosedAt.IsZero() || !up.Run.ClosedAt.IsZero() {
			t.Errorf("%s is not parked with its run open: %+v", name, f)
		}
	}
	if m.live() != 0 || tabs() != 0 {
		t.Fatalf("after stop --all %d agents are at work and %d of their tabs are open", m.live(), tabs())
	}
	for _, g := range []string{"G1", "G2", "G5"} {
		if up.Tasks[g].Status != contract.TaskReady {
			t.Errorf("%s is %s after the stop, not ready to start again", g, up.Tasks[g].Status)
		}
	}
	if ev := up.Inbox.Events[len(up.Inbox.Events)-1]; ev.Kind != "human" || ev.Data["what"] != "stop" {
		t.Errorf("the person's stop is written down as %+v", ev)
	}
	// Nothing is left to stop, and that is no failure; every fleet comes back.
	m.do(scenario.Human, 0, []string{`"stopped":[]`}, "", "stop", "--all", "--json")
	m.do(scenario.Orch, 0, []string{"(G1.2)"}, "", "start", "G1", "--retry")
	m.do(scenario.Orch, 0, []string{"(G2.2)"}, "", "start", "G2", "--retry")
	if p := m.p; p.Double != nil {
		spaces(t, m)
	}
	return m.record()
}

// spaces is what only the double shows: every tab a start opened was
// handed the project's folder as its workspace, a leader's tab its fleet as
// its group, and a leader's worker's tab is of that group by the pane it
// was opened from.
func spaces(t *testing.T, m *morning) {
	t.Helper()
	want := map[string]string{"billing": "billing", "search": "search", "bill T1": "", "bill T2": "", "find T1": ""}
	seen := 0
	for _, c := range m.p.Double.Calls() {
		group, ours := want[c.Label]
		if c.Op != contract.OpTabCreate || !ours {
			continue
		}
		if seen++; c.Space != m.p.Root || c.Group != group {
			t.Errorf("the tab %q was opened in the workspace %q with the group %q", c.Label, c.Space, c.Group)
		}
	}
	snap, err := m.p.Terms().Snapshot(t.Context())
	if err != nil || seen < 7 {
		t.Fatalf("%d tabs were opened by a start, expected seven: %v", seen, err)
	}
	for _, pane := range snap.Panes {
		if fleet := map[string]string{"billing": "billing", "search": "search"}[pane.Label]; fleet != "" && m.p.Double.Group(pane.Tab) != fleet {
			t.Errorf("the tab of the leader of %s is of the group %q", fleet, m.p.Double.Group(pane.Tab))
		}
	}
}

func TestStopAllOnAJobOfTwoFleets(t *testing.T) { onBoth(t, stopAll) }

// In a job without fleets every agent at work is stopped and its tab
// closed. A task that will not stop is passed by, the rest are stopped, and
// the command ends as refused with that task's name.
func TestStopAllPassesByWhatWillNotStop(t *testing.T) {
	p := evening(t, 3, nil)
	must(t, p.Command(scenario.Orch, "start", "R1", "R2", "R3"), 0)
	err := p.Kit.Store.Change(p.Root, "r3", func(s *contract.State) error {
		s.Attempts["R2.1"].State = contract.AttemptChecking
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	at := func() (ids []string) {
		s, _ := p.Record()
		for id, a := range s.Attempts {
			if a.State.Live() {
				ids = append(ids, id)
			}
		}
		slices.Sort(ids)
		return ids
	}
	before := at()
	if len(before) < 4 {
		t.Fatalf("the evening has %v at work, expected the fixture's own and three more", before)
	}
	must(t, p.Command(scenario.Orch, "stop", "--all"), 5, "--all is the person's to give")
	must(t, p.Command("R1.1", "stop", "--all"), 5)
	if got := at(); !slices.Equal(got, before) {
		t.Fatalf("an agent's stop --all left %v at work of %v", got, before)
	}
	out := must(t, p.Command(scenario.Human, "stop", "--all"), 5, "R2 is being accepted", "ready 1 stopped", "ready 3 stopped")
	if got := at(); !slices.Equal(got, []string{"R2.1"}) {
		t.Fatalf("after stop --all %v are at work, expected the one being accepted:\n%s", got, out)
	}
	for _, label := range []string{"ready 1", "ready 3"} {
		if n := labelled(t, p, label); n != 0 {
			t.Errorf("%d tabs of %q are open after the stop", n, label)
		}
	}
	if n := labelled(t, p, "ready 2"); n != 1 {
		t.Errorf("the tab of the task that was not stopped: %d open", n)
	}
	if s, _ := p.Record(); !s.Run.ClosedAt.IsZero() {
		t.Error("stop --all closed the job")
	}
}

func labelled(t *testing.T, p *scenario.Project, label string) (n int) {
	t.Helper()
	snap, err := p.Terms().Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, pane := range snap.Panes {
		if pane.Label == label {
			n++
		}
	}
	return n
}
