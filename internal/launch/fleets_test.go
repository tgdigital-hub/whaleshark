package launch

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/rules"
	"github.com/tgdigital-hub/whaleshark/test/fakeengine"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// The table of kinds this folder held until phase 5, kept here alone: every
// description that is compiled in says of its tool what the table said.
func TestTheBuiltInToolsAreTheKindsOfBefore(t *testing.T) {
	kinds := map[string]struct {
		dir    string
		each   bool
		resume []string
	}{
		"claude": {"--add-dir", false, []string{"--resume"}},
		"codex":  {"--add-dir", true, []string{"resume"}},
	}
	if built := contract.BuiltIn(); len(built) != len(kinds) || contract.DefaultTool != "claude" {
		t.Fatalf("%d descriptions are built in for %d kinds; the default is %s", len(built), len(kinds), contract.DefaultTool)
	}
	for _, tool := range contract.BuiltIn() {
		k, known := kinds[tool.Name]
		folders := []string{k.dir, "/a", "/b"}
		if k.each {
			folders = []string{k.dir, "/a", k.dir, "/b"}
		}
		switch {
		case !known:
			t.Errorf("%s was no kind", tool.Name)
		case !slices.Equal(tool.Run, []string{tool.Name}):
			t.Errorf("%s runs %q", tool.Name, tool.Run)
		case !slices.Equal(tool.Folders("/a", "/b"), folders):
			t.Errorf("%s is given two folders as %q, not %q", tool.Name, tool.Folders("/a", "/b"), folders)
		case !slices.Equal(contract.Fill(tool.Resume, "session", "s7"), append(slices.Clone(k.resume), "s7")):
			t.Errorf("%s continues a conversation with %q", tool.Name, contract.Fill(tool.Resume, "session", "s7"))
		case !slices.Equal(contract.Fill(tool.Model, "model", "m"), []string{"--model", "m"}):
			t.Errorf("%s names a model with %q", tool.Name, contract.Fill(tool.Model, "model", "m"))
		}
	}
}

// morning is a job of fleets played on one backend: the person lets three
// leaders run, two agents in a fleet and six in all. What it came to is
// returned in words no backend changes.
type morning struct {
	t   *testing.T
	p   *scenario.Project
	log []string
}

// groups are the orchestrator's tasks, each placed in a fleet; billing has two.
var groups = [][2]string{{"G1", "billing"}, {"G2", "search"}, {"G3", "help pages"}, {"G4", "mail"}, {"G5", "billing"}}

func group(id, fleet string) *contract.Task {
	return &contract.Task{ID: id, Name: "group " + id, Title: "a group of work", Brief: "briefs/" + id + ".md",
		Check: contract.CheckNone, Placement: contract.PlaceFleet, Fleet: fleet, Status: contract.TaskReady, CreatedAt: contract.Now()}
}

func begin(t *testing.T, on scenario.Backend) *morning {
	t.Helper()
	scripts := map[string]string{"T1.1": "hang", "T2.1": "hang", "T3.1": "hang", "G1.2": "hang", "G6.1": "hang"}
	for _, g := range groups {
		scripts[g[0]+".1"] = "hang"
	}
	p := scenario.PrepareOn(t, on, nil, scripts)
	dirs, err := p.Kit.Platform.Dirs()
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(dirs.Config, contract.ConfigFile): "[fleets]\nmode = \"on\"\nleaders = 3\nagents = 2\ntotal = 6\n",
		filepath.Join(p.Root, "brief.md"):               "Target: one page\nChange: write it\nConstraints: none\nOwnership: your own file\nAcceptance: it is there\n",
	}
	for path, text := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
			err = os.WriteFile(path, []byte(text), 0o600)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	_, now := p.Command(scenario.Orch), contract.Now()
	s := rules.Rules{}.NewRun("r1", "a morning with fleets", 20, nil, contract.Binding{Pane: p.Lead}, now)
	s.Run.Mode = contract.ModeFleets
	for _, g := range groups {
		s.Tasks[g[0]] = group(g[0], g[1])
	}
	for _, err := range []error{contract.ListProject(p.Kit.Platform, p.Root), p.Kit.Store.Create(p.Root, s), p.Kit.Store.SetCurrent(p.Root, "r1")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	return &morning{t: t, p: p}
}

// do runs one command as the orchestrator, the person or a leader, holds it
// to its exit code and to words it prints, and notes it. env is added to the
// caller's own variables.
func (m *morning) do(who string, exit int, words []string, env string, line ...string) string {
	m.t.Helper()
	cmd := m.p.Command(who, line...)
	if env != "" {
		cmd.Env = append(cmd.Env, env)
	}
	out := must(m.t, cmd, exit, words...)
	m.log = append(m.log, fmt.Sprintf("%s: %s -> %d", who, strings.Join(line, " "), exit))
	return out
}

func (m *morning) read(run string) *contract.State {
	m.t.Helper()
	s, err := m.p.Kit.Store.Read(m.p.Root, run)
	if err != nil {
		m.t.Fatal(err)
	}
	return s
}

// tabs is how many tabs carry a label.
func (m *morning) tabs(label string) (n int) {
	m.t.Helper()
	snap, err := m.p.Terms().Snapshot(m.t.Context())
	if err != nil {
		m.t.Fatal(err)
	}
	for _, pane := range snap.Panes {
		if pane.Label == label {
			n++
		}
	}
	return n
}

// record is the end of the log: every run with its fleets, tasks and attempts.
func (m *morning) record() []string {
	runs, _ := m.p.Kit.Store.Runs(m.p.Root)
	for _, id := range runs {
		s := m.read(id)
		m.log = append(m.log, fmt.Sprintf("run %s mode %q parent %+v open %t bound %t", id, s.Run.Mode, s.Run.Parent, s.Run.ClosedAt.IsZero(), s.Run.Orchestrator.Pane != ""))
		for _, name := range slices.Sorted(func(yield func(string) bool) {
			for name := range s.Run.Fleets {
				if !yield(name) {
					return
				}
			}
		}) {
			f := s.Run.Fleets[name]
			m.log = append(m.log, fmt.Sprintf("fleet %s: run %s, tool %s, leader %t, ended %t, groups %v", name, f.Run, f.Tool, f.Pane != "", !f.EndedAt.IsZero(), f.Groups))
		}
		var lines []string
		for id, t := range s.Tasks {
			lines = append(lines, fmt.Sprintf("%s task %s %s", s.Run.ID, id, t.Status))
		}
		for id, a := range s.Attempts {
			lines = append(lines, fmt.Sprintf("%s attempt %s %s leads %q", s.Run.ID, id, a.State, a.Leads))
		}
		slices.Sort(lines)
		m.log = append(m.log, lines...)
	}
	return m.log
}

const (
	asLeader = contract.EnvDepth + "=1"
	asWorker = contract.EnvDepth + "=2"
)

// play is the row's scenario: three fleets started, a fourth refused, a
// fifth group handed to a leader with room, each limit reached and named;
// then a pause over the whole job, a fleet parked and taken over, and a
// fleet ended by the close of its last group and begun again.
func play(t *testing.T, on scenario.Backend) []string {
	m := begin(t, on)
	p := m.p
	task := func(fleet, id string) []string {
		return []string{"task", "add", id, fleet + " " + id, "--brief", "brief.md", "--check", "none"}
	}

	// Three fleets: a run and a tab each, the leader bound to its run.
	// (One start each: started side by side, which fleet gets which run's number is open.)
	m.do(scenario.Orch, 0, []string{"(G1.1)"}, "", "start", "G1")
	m.do(scenario.Orch, 0, []string{"(G2.1)"}, "", "start", "G2")
	m.do(scenario.Orch, 0, []string{"(G3.1)"}, "", "start", "G3")
	up := m.read("r1")
	for _, g := range groups[:3] {
		a, f := up.Attempts[g[0]+".1"], up.Run.Fleets[g[1]]
		if f == nil || a.State != contract.AttemptWorking || a.Leads != f.Run || f.Pane != a.Place.Pane || f.Tool != contract.DefaultTool || m.tabs(g[1]) != 1 {
			t.Fatalf("%s is not led: fleet %+v, attempt %+v, %d tabs", g[1], f, a, m.tabs(g[1]))
		}
		low := m.read(f.Run)
		if low.Run.Parent == nil || *low.Run.Parent != (contract.Twin{Run: "r1", Fleet: g[1]}) || low.Run.Orchestrator.Pane != f.Pane {
			t.Fatalf("run %s of %s: parent %+v, bound to %+v", f.Run, g[1], low.Run.Parent, low.Run.Orchestrator)
		}
		prompt, err := os.ReadFile(filepath.Join(contract.AttemptDir(p.Kit.Store.Dir(p.Root, "r1"), a.ID), "prompt.md"))
		if err != nil || !strings.Contains(string(prompt), `leader of the fleet "`+g[1]+`"`) || !strings.Contains(string(prompt), "may run 2 agents") {
			t.Fatalf("the prompt of %s is not a leader's: %v\n%s", a.ID, err, prompt)
		}
	}

	// A fourth is one leader more than the person allows.
	m.do(scenario.Orch, 5, []string{contract.LimitLeaders, "3 fleets are running, which is your limit"}, "", "start", "G4", "--json")
	// A fifth group is for a fleet whose leader sits: nothing starts.
	m.do(scenario.Orch, 0, []string{"(G5.1)"}, "", "start", "G5")
	up = m.read("r1")
	one, two := up.Attempts["G1.1"], up.Attempts["G5.1"]
	if two.State != contract.AttemptWorking || two.Place != one.Place || two.Leads != "r2" || m.tabs("billing") != 1 ||
		len(two.Mail) != 1 || !strings.HasPrefix(two.Mail[0].Text, "New work for your fleet: group G5") || !slices.Equal(up.Run.Fleets["billing"].Groups, []string{"G1", "G5"}) {
		t.Fatalf("G5 did not go to the leader of billing: %+v, fleet %+v", two, up.Run.Fleets["billing"])
	}

	// Billing's leader starts its two workers from its own tab, which the
	// keeper's fence lets it do in a tab it opened itself; a third is over
	// the fleet's limit. Only a leader starts from a tab that is one deep.
	for _, id := range []string{"T1", "T2", "T3"} {
		m.do("G1.1", 0, nil, asLeader, task("bill", id)...)
	}
	m.do("G1.1", 0, []string{"(T1.1)", "(T2.1)"}, asLeader, "start", "T1", "T2")
	m.do(scenario.Human, 5, []string{contract.LimitFleet}, "", "start", "T3", "--run", "r2", "--json")
	m.do("G1.1", 5, []string{contract.LimitFleet, "2 agents are live in billing"}, asLeader, "start", "T3", "--json")
	m.do("G1.1", 5, []string{"A worker starts no workers."}, asWorker, "start", "T3")
	m.do(scenario.Orch, 5, []string{"A worker starts no workers."}, asLeader, "start", "G4")
	// Search starts one: six agents in all, and a seventh is named so.
	for _, id := range []string{"T1", "T2"} {
		m.do("G2.1", 0, nil, asLeader, task("find", id)...)
	}
	m.do("G2.1", 0, []string{"(T1.1)"}, asLeader, "start", "T1")
	m.do("G2.1", 5, []string{contract.LimitTotal, "6 agents are live in all"}, asLeader, "start", "T2", "--json")
	m.do("G1.1", 0, []string{"1 more ready task waits for a free place: the limit is 2 agents."}, asLeader, "start", "--ready")
	if low := m.read("r2"); low.Attempts["T1.1"].State != contract.AttemptWorking || low.Attempts["T2.1"].State != contract.AttemptWorking || len(low.Attempts) != 2 {
		t.Fatalf("billing's run holds %+v", low.Attempts)
	}

	// Stop all from the job reaches its fleets, and resume wakes them.
	// Billing's leader holds two groups and is one agent of the six.
	m.do(scenario.Human, 0, []string{"Paused. 6 agents stop"}, "", "pause")
	for _, run := range []string{"r1", "r2", "r3", "r4"} {
		if m.read(run).Run.Paused == nil {
			t.Fatalf("run %s is not paused with its job", run)
		}
	}
	m.do(scenario.Orch, 5, []string{"paused"}, "", "start", "G4")
	m.do(scenario.Human, 0, []string{"Resumed."}, "", "resume")
	for _, run := range []string{"r1", "r2", "r3", "r4"} {
		if m.read(run).Run.Paused != nil {
			t.Fatalf("run %s is still paused", run)
		}
	}

	// One group of two is stopped and the leader sits on; the last one
	// parks the fleet, and a retry brings a leader into the same run.
	m.do(scenario.Orch, 0, []string{"its tab: kept"}, "", "stop", "G5")
	if up = m.read("r1"); up.Run.Fleets["billing"].Pane == "" || m.tabs("billing") != 1 {
		t.Fatalf("the leader of billing went with one of its two groups: %+v", up.Run.Fleets["billing"])
	}
	m.do(scenario.Orch, 0, []string{"The fleet is parked, 2 of its agents stopped with its leader"}, "", "stop", "G1")
	up, low := m.read("r1"), m.read("r2")
	if up.Run.Fleets["billing"].Pane != "" || low.Attempts["T1.1"].State != contract.AttemptStopped || low.Tasks["T2"].Status != contract.TaskReady ||
		!low.Run.ClosedAt.IsZero() || m.tabs("billing")+m.tabs("bill T1")+m.tabs("bill T2") != 0 {
		t.Fatalf("billing is not parked: %+v, %+v, tabs %d", up.Run.Fleets["billing"], low.Attempts["T1.1"], m.tabs("billing"))
	}
	m.do(scenario.Orch, 0, []string{"(G1.2)"}, "", "start", "G1", "--retry")
	up, low = m.read("r1"), m.read("r2")
	if f := up.Run.Fleets["billing"]; f.Run != "r2" || f.Pane == "" || low.Run.Orchestrator.Pane != f.Pane || up.Attempts["G1.2"].Leads != "r2" {
		t.Fatalf("the new leader did not take billing's run over: %+v, bound to %+v", f, low.Run.Orchestrator)
	}

	// Help pages reports its one group; closing it ends the fleet, and more
	// work for the fleet begins a run of its own.
	_, dir := p.Record()
	if err := os.WriteFile(filepath.Join(dir, "attempts", "G3.1", "result.md"), []byte("The pages are written.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.do("G3.1", 0, nil, "", "report", "done", "The pages are written. Each was read. Nothing is left open.")
	m.do(scenario.Orch, 0, nil, "", "accept", "G3", "--by-hand", "read every page")
	m.do(scenario.Orch, 0, []string{"ended: the fleet help pages (run r4)"}, "", "close", "G3")
	if up = m.read("r1"); up.Run.Fleets["help pages"].EndedAt.IsZero() || m.read("r4").Run.ClosedAt.IsZero() || m.tabs("help pages") != 0 {
		t.Fatalf("help pages did not end: %+v, %d tabs", up.Run.Fleets["help pages"], m.tabs("help pages"))
	}
	if err := p.Kit.Store.Change(p.Root, "r1", func(s *contract.State) error { s.Tasks["G6"] = group("G6", "help pages"); return nil }); err != nil {
		t.Fatal(err)
	}
	m.do(scenario.Orch, 0, []string{"(G6.1)"}, "", "start", "G6")
	if f := m.read("r1").Run.Fleets["help pages"]; f.Run != "r5" || f.Pane == "" || !f.EndedAt.IsZero() || !slices.Equal(f.Groups, []string{"G6"}) {
		t.Fatalf("help pages did not begin again in a run of its own: %+v", f)
	}

	// The leader that took billing over starts a worker, stops it, which
	// closes its tab, and starts it again.
	m.do("G1.2", 0, []string{"(T3.1)"}, asLeader, "start", "T3")
	m.do("G1.2", 0, nil, asLeader, "stop", "T3")
	if n := m.tabs("bill T3"); n != 0 {
		t.Fatalf("the leader's stop left %d tabs of its worker", n)
	}
	m.do("G1.2", 0, []string{"(T3.2)"}, asLeader, "start", "T3", "--retry")
	log := m.record()
	if p.Double != nil {
		held(t, p.Double)
	}
	return log
}

// held is what only the double shows: the variables each tab was opened
// with, and the lines typed at the leader of billing.
func held(t *testing.T, double *fakeengine.Fake) {
	t.Helper()
	depth := map[string]string{}
	typed := []string{}
	var billing string
	for _, c := range double.Calls() {
		switch {
		case c.Op == contract.OpTabCreate:
			at := slices.IndexFunc(c.Env, func(kv string) bool { return strings.HasPrefix(kv, contract.EnvDepth+"=") })
			if depth[c.Label] = "none"; at >= 0 {
				depth[c.Label] = c.Env[at]
			}
		case c.Op == contract.OpAgentStart && billing == "":
			billing = c.Pane // the first agent started is the leader of billing or of another fleet: found below
		}
		if line := fakeengine.Typed(c); c.Op == contract.OpPoint && line != "" {
			typed = append(typed, line)
		}
	}
	for label, want := range map[string]string{"billing": asLeader, "search": asLeader, "help pages": asLeader, "bill T1": asWorker, "bill T3": asWorker} {
		if depth[label] != want {
			t.Errorf("the tab %q was opened with %s, expected %s", label, depth[label], want)
		}
	}
	// One line for the new group; after the pause one line a tab: the
	// leader of billing, which holds two groups and leads a run, gets one.
	mail, resumed := 0, 0
	for _, line := range typed {
		switch line {
		case string(contract.PointFleet):
			mail++
		case string(contract.PointResumed), string(contract.PointLeadOn):
			resumed++
		}
	}
	if mail != 1 || resumed != 7 {
		t.Errorf("%d pointers to new work and %d after the pause were typed, expected 1 and 7 (three leaders, three workers, the orchestrator):\n%s", mail, resumed, strings.Join(typed, "\n"))
	}
}

// The row's scenario on the engine's double and on the real keeper: what
// the job came to is the same on both.
func TestThreeFleetsAFourthRefusedAFifthGroupHandedOn(t *testing.T) {
	logs := map[scenario.Backend][]string{}
	for _, on := range []scenario.Backend{scenario.Double, scenario.Keeper} {
		t.Run(string(on), func(t *testing.T) {
			if on == scenario.Keeper && runtime.GOOS == "windows" {
				t.Skip("NOT PROVEN HERE: on Windows the real keeper plays the first scenarios of the suite only, for the time they take there")
			}
			logs[on] = play(t, on)
		})
	}
	if on := logs[scenario.Keeper]; on != nil && !slices.Equal(on, logs[scenario.Double]) {
		t.Errorf("the run on the keeper came to something else than on the double:\n%s\n\non the double:\n%s",
			strings.Join(on, "\n"), strings.Join(logs[scenario.Double], "\n"))
	}
}

// A tool nobody wired in is refused before anything exists, by its name.
func TestAToolThatIsNotWiredInStartsNothing(t *testing.T) {
	p := evening(t, 1, nil)
	out := must(t, p.Command(scenario.Orch, "start", "R1", "--agent", "hermes", "--json"), 5, "no_tool")
	if s, _ := p.Record(); len(s.Tasks["R1"].Attempts) != 0 || len(calls(p, contract.OpTabCreate)) != 0 {
		t.Fatalf("something was made for a tool that is not wired in:\n%s", out)
	}
}

// In a project with git a fleet's run begins at what the orchestrator has
// collected, on a branch of its own that the record names, and a task of
// the fleet gets its copy of the code from there, in a tab two deep.
func TestAFleetInARepository(t *testing.T) {
	for _, pair := range testkit.GitEnv()[len(os.Environ()):] {
		name, value, _ := strings.Cut(pair, "=")
		t.Setenv(name, value)
	}
	p := scenario.Prepare(t, nil, map[string]string{"G1.1": "hang", "T1.1": "hang"})
	testkit.Git(t, p.Root, "init", "-q", "-b", "main")
	testkit.Git(t, p.Root, "config", "user.name", "a test")
	testkit.Git(t, p.Root, "config", "user.email", "test@localhost")
	testkit.Commit(t, p.Root, "start", map[string]string{"b.md": brief})
	must(t, p.Command(scenario.Orch, "init"), 0)
	must(t, p.Command(scenario.Orch, "run", "new", "a job of fleets"), 0)
	err := p.Kit.Store.Change(p.Root, "r1", func(s *contract.State) error {
		s.Run.Mode, s.Tasks["G1"] = contract.ModeFleets, group("G1", "billing")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	must(t, p.Command(scenario.Orch, "start", "G1"), 0, "(G1.1)")
	up, _ := p.Record()
	low, err := p.Kit.Store.Read(p.Root, "r2")
	if err != nil {
		t.Fatal(err)
	}
	f, in := up.Run.Fleets["billing"], low.Run.Integration
	if in == nil || f.Branch != in.Branch || in.Branch == up.Run.Integration.Branch || low.Run.Base.Ref != up.Run.Integration.Branch || low.Run.Base.OID != up.Run.Integration.Tip {
		t.Fatalf("the fleet %+v; its run collects on %+v from %+v; the orchestrator's on %+v", f, in, low.Run.Base, up.Run.Integration)
	}
	if a := up.Attempts["G1.1"]; a.Place.Cwd != p.Root || up.Tasks["G1"].Worktree != nil {
		t.Fatalf("the leader is not in the project's folder without a copy: %+v", a.Place)
	}
	must(t, p.Command("G1.1", "task", "add", "T1", "the parser", "--brief", "b.md", "--check", "none"), 0)
	must(t, p.Command(scenario.Human, "start", "T1", "--run", "r2"), 0, "(T1.1)")
	if low, _ = p.Kit.Store.Read(p.Root, "r2"); low.Tasks["T1"].Worktree == nil || testkit.Git(t, low.Tasks["T1"].Worktree.Path, "merge-base", "HEAD", in.Branch) != in.Tip {
		t.Fatalf("the worker's copy does not stand on the fleet's branch: %+v", low.Tasks["T1"].Worktree)
	}
}

// Five groups started in one command, side by side: two are one fleet's, so
// one leader is started and the other group is handed to it; three more
// fleets are one over the person's limit of three, and one of them waits.
func TestGroupsStartedSideBySide(t *testing.T) {
	m := begin(t, scenario.Double)
	out, exit := run(t, m.p.Command(scenario.Orch, "start", "G1", "G2", "G3", "G4", "G5", "--json"))
	up := m.read("r1")
	leaders, refused := 0, 0
	for _, f := range up.Run.Fleets {
		if f.Pane != "" {
			leaders++
		}
	}
	for _, id := range []string{"G2", "G3", "G4"} {
		if len(up.Tasks[id].Attempts) == 0 {
			refused++
		}
	}
	one, two := up.Attempts["G1.1"], up.Attempts["G5.1"]
	if exit != 5 || !strings.Contains(out, contract.LimitLeaders) || leaders != 3 || refused != 1 || len(up.Run.Fleets) != 3 ||
		one == nil || two == nil || one.State != contract.AttemptWorking || two.State != contract.AttemptWorking || one.Place != two.Place || m.tabs("billing") != 1 {
		t.Fatalf("exit %d, %d leaders, %d refused, fleets %+v, G1.1 %+v, G5.1 %+v\n%s", exit, leaders, refused, up.Run.Fleets, one, two, out)
	}
	if runs, _ := m.p.Kit.Store.Runs(m.p.Root); len(runs) != 4 {
		t.Fatalf("the runs are %v: one for the job and one a fleet", runs)
	}
}

// A leader that holds two groups is two live attempts in one pane and one
// agent: a server's cap and Stop all's count take it once. An attempt that
// has no pane yet is each its own.
func TestALeaderWithTwoGroupsIsOneAgent(t *testing.T) {
	at := func(pane string, state contract.AttemptState) *contract.Attempt {
		return &contract.Attempt{State: state, Place: contract.Place{Pane: pane}}
	}
	s := &contract.State{Attempts: map[string]*contract.Attempt{
		"G1.1": at("p4", contract.AttemptWorking), "G5.1": at("p4", contract.AttemptWorking), "G2.1": at("p5", contract.AttemptWorking),
		"G3.1": at("", contract.AttemptStarting), "G4.1": at("", contract.AttemptStarting), "G6.1": at("p4", contract.AttemptExited),
	}}
	if n := live(s); n != 4 {
		t.Errorf("%d agents counted, expected 4", n)
	}
}
