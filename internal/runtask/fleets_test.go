package runtask_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/guide"
	"github.com/tgdigital-hub/whaleshark/internal/rules"
	"github.com/tgdigital-hub/whaleshark/internal/runtask"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// fleetsOn switches Fleet Mode on in the person's own settings, as the
// settings screen and `set fleets.mode on` do.
func fleetsOn(t *testing.T, p *scenario.Project) {
	t.Helper()
	dirs, err := p.Kit.Platform.Dirs()
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dirs.Config, contract.ConfigFile), "[fleets]\nmode = \"on\"\n")
}

// A run is of the mode the person's setting has when it begins, and stays
// so. With Fleet Mode off nothing of a run says fleet, and --fleet is refused
// with the line that switches the mode on.
func TestARunTakesFleetModeFromThePersonsSetting(t *testing.T) {
	p := scenario.Prepare(t, nil, nil)
	orch := func(exit int, args ...string) func(words ...string) string {
		return func(words ...string) string { return play(t, p.Command(scenario.Orch, args...), exit, words...) }
	}
	write(t, filepath.Join(p.Root, "b.md"), briefText)
	orch(5, "run", "close")("No run is open here")
	orch(0, "run", "new", "as it always was")("Run r1 is open")
	if s, _ := p.Record(); s.Run.Mode != "" || s.Run.Limit != 20 || s.Run.Fleets != nil {
		t.Fatalf("with Fleet Mode off the run is %+v", s.Run)
	}
	orch(5, "task", "add", "G1", "x", "--brief", "b.md", "--check", "none", "--fleet", "billing")("Fleet Mode was off", "whaleshark set fleets.mode on")
	orch(0, "task", "add", "A", "x", "--brief", "b.md", "--check", "none")("ready")

	// Switched on, it holds from the next job; the open one stays as it is.
	fleetsOn(t, p)
	orch(5, "task", "add", "G1", "y", "--brief", "b.md", "--check", "none", "--fleet", "billing")("Fleet Mode was off")
	orch(0, "run", "new", "by fleets", "--park")("Run r2 is open")
	s, err := p.Kit.Store.Read(p.Root, "r2")
	if err != nil || s.Run.Mode != contract.ModeFleets || s.Run.Limit != 1000 {
		t.Fatalf("with Fleet Mode on the run is %+v, %v", s.Run, err)
	}
	// A limit that was given is the run's all the same.
	orch(0, "run", "new", "by fleets, few groups", "--park", "--limit", "6")("Run r3 is open")
	if s, _ := p.Kit.Store.Read(p.Root, "r3"); s.Run.Limit != 6 {
		t.Fatalf("the limit given was not kept: %d", s.Run.Limit)
	}
	// A config.toml that lies in the project is nobody's setting.
	q := scenario.Prepare(t, nil, nil)
	write(t, filepath.Join(q.Root, contract.ConfigFile), "[fleets]\nmode = \"on\"\n")
	play(t, q.Command(scenario.Orch, "run", "new", "a job"), 0, "Run r1 is open")
	if s, _ := q.Record(); s.Run.Mode != "" {
		t.Fatal("a file of the project's switched Fleet Mode on")
	}
}

// call runs a command's handler in this process, as the lead agent, on a
// kit whose panes call is the test's own.
func call(t *testing.T, p *scenario.Project, pane string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	c := &contract.Call{Kit: p.Kit, Command: contract.Find(args[0]), Args: args[1:], Flags: map[string][]string{}, Root: p.Root, Now: contract.Now(),
		Caller: contract.Caller{Kind: contract.Orchestrator, Pane: pane}, Stdin: strings.NewReader(""), Out: &out, Err: &out}
	c.Run, _ = p.Kit.Store.Current(p.Root)
	_, err := p.Kit.Handler(args[0])(c)
	return out.String(), err
}

// The two panes of ours open in the lead agent's tab when a run gets its
// lead agent: at run new from its pane, at run takeover and at run use.
func TestTheRunsPanesOpenWhereItIsBound(t *testing.T) {
	p := scenario.Prepare(t, nil, nil)
	rules.Plug(p.Kit)
	runtask.Plug(p.Kit)
	var opened []string
	p.Kit.Panes = func(_ *contract.Kit, pane string) error { opened = append(opened, pane); return nil }
	for _, step := range [][]string{{"w1:p1", "run", "new", "a shop"}, {"w1:p7", "run", "takeover"}, {"w1:p9", "run", "use", "r1"}, {"w1:p9", "run", "list"}} {
		if out, err := call(t, p, step[0], step[1:]...); err != nil {
			t.Fatalf("%v: %v\n%s", step, err, out)
		}
	}
	if got := strings.Join(opened, " "); got != "w1:p1 w1:p7 w1:p9" {
		t.Fatalf("the panes were opened for %q", got)
	}
	// A run made by the person is bound to no tab, and opens nothing.
	play(t, p.Command(scenario.Human, "run", "new", "the person's own", "--park"), 0, "No tab drives it yet")
}

// crew is an orchestrator's run with two fleets, as start leaves them: each
// with a run of its own, its leader the attempt of its group, and a worker
// at work in billing.
func crew(t *testing.T) (*scenario.Project, func(exit int, args ...string) func(words ...string) string) {
	t.Helper()
	p := scenario.Prepare(t, nil, nil)
	fleetsOn(t, p)
	rules.Plug(p.Kit)
	orch := func(exit int, args ...string) func(words ...string) string {
		return func(words ...string) string { return play(t, p.Command(scenario.Orch, args...), exit, words...) }
	}
	write(t, filepath.Join(p.Root, "b.md"), briefText)
	orch(0, "run", "new", "the shop")("Run r1 is open")
	for n, g := range [][2]string{{"G1", "billing"}, {"G2", "search"}, {"G3", "billing"}} {
		orch(0, "task", "add", g[0], "work "+g[0], "--brief", "b.md", "--check", "none", "--fleet", g[1])("ready")
		if g[0] == "G3" {
			continue // a second group for billing, not started
		}
		k, run, now := p.Kit, []string{"r2", "r3"}[n], contract.Now()
		if err := k.Store.Create(p.Root, k.Rules.NewRun(run, "the fleet "+g[1], 8, nil, contract.Binding{}, now)); err != nil {
			t.Fatal(err)
		}
		tab, err := k.Terms.TabCreate(p.Root, g[1], nil)
		if err == nil {
			err = k.Store.Both(p.Root, "r1", run, func(up, low *contract.State) error {
				id, err := k.Rules.Start(up, g[0], false, false, "", contract.Agent{Kind: "claude", Name: "a-" + run}, false, now)
				if err == nil {
					err = k.Rules.Placed(up, id, contract.Place{Tab: tab.Tab, Pane: tab.ID}, now)
				}
				if err == nil {
					err = k.Rules.Lead(up, low, id, contract.Binding{Pane: tab.ID}, "", now)
				}
				if err == nil && run == "r2" {
					err = k.Rules.AddTask(low, contract.Task{ID: "T1", Title: "the invoice", Check: "none", Placement: contract.PlaceShared}, nil, now)
				}
				if err == nil && run == "r2" {
					_, err = k.Rules.Start(low, "T1", false, false, "", contract.Agent{Kind: "claude", Name: "a-worker"}, false, now)
				}
				return err
			})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return p, orch
}

func runOf(t *testing.T, p *scenario.Project, id string) *contract.State {
	t.Helper()
	s, err := p.Kit.Store.Read(p.Root, id)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Dropping a fleet's last group gives the fleet up: its run is closed in the
// same step. While an agent of the fleet lives the cancel is refused whole,
// and a group that is not the fleet's last is dropped alone.
func TestCancellingAFleetsLastGroupGivesTheFleetUp(t *testing.T) {
	p, orch := crew(t)
	stop := func(run, task string) {
		t.Helper()
		if err := p.Kit.Store.Change(p.Root, run, func(s *contract.State) error { return p.Kit.Rules.Stop(s, task, false, contract.Now()) }); err != nil {
			t.Fatal(err)
		}
	}
	// G3 is one of two groups of billing: it goes alone.
	orch(0, "task", "cancel", "G3")("G3 is cancelled")
	if up := runOf(t, p, "r1"); !up.Run.Fleets["billing"].EndedAt.IsZero() || !runOf(t, p, "r2").Run.ClosedAt.IsZero() {
		t.Fatal("a fleet with a group left was given up")
	}
	// The leader's own attempt is live: the rule for any task.
	orch(5, "task", "cancel", "G1")("live attempt", "whaleshark stop G1")
	stop("r1", "G1")
	// Its worker is still at work: the fleet is not given up under it.
	orch(5, "task", "cancel", "G1")("still live")
	if up := runOf(t, p, "r1"); up.Tasks["G1"].Status == contract.TaskCancelled || !runOf(t, p, "r2").Run.ClosedAt.IsZero() {
		t.Fatalf("a refused cancel left the group %s", up.Tasks["G1"].Status)
	}
	stop("r2", "T1")
	// The tab its worker ran in goes with the fleet, as at the close of a last group.
	tab, err := p.Kit.Terms.TabCreate(p.Root, runOf(t, p, "r2").Tasks["T1"].Name, nil)
	if err == nil {
		err = p.Kit.Store.Change(p.Root, "r2", func(s *contract.State) error {
			s.Attempts["T1.1"].Place = contract.Place{Tab: tab.Tab, Pane: tab.ID, OpenedByUs: true}
			return nil
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	orch(0, "task", "cancel", "G1")("G1 is cancelled")
	if snap, _ := p.Kit.Terms.Snapshot(t.Context()); slices.ContainsFunc(snap.Panes, func(q contract.Pane) bool { return q.ID == tab.ID }) {
		t.Error("the tab of the fleet's worker stayed when the fleet was given up")
	}
	up := runOf(t, p, "r1")
	if f := up.Run.Fleets["billing"]; f.EndedAt.IsZero() || f.Pane != "" || runOf(t, p, "r2").Run.ClosedAt.IsZero() || up.Tasks["G1"].Status != contract.TaskCancelled {
		t.Fatalf("the fleet was not given up: %+v", f)
	}
	if f := up.Run.Fleets["search"]; !f.EndedAt.IsZero() || !runOf(t, p, "r3").Run.ClosedAt.IsZero() {
		t.Fatal("another fleet went with it")
	}
}

// An orchestrator's run takes its fleets' runs with it. While a fleet still
// has an agent at work nothing is closed, the job's run and every other
// fleet's included: no fleet is closed under a job that stays open.
func TestAJobIsClosedAndRemovedWithItsFleets(t *testing.T) {
	p, orch := crew(t)
	stop := func(run, task string) {
		t.Helper()
		if err := p.Kit.Store.Change(p.Root, run, func(s *contract.State) error { return p.Kit.Rules.Stop(s, task, false, contract.Now()) }); err != nil {
			t.Fatal(err)
		}
	}
	open := func(why string) {
		t.Helper()
		for _, id := range []string{"r1", "r2", "r3"} {
			if !runOf(t, p, id).Run.ClosedAt.IsZero() {
				t.Fatalf("%s: a refused close closed %s", why, id)
			}
		}
	}
	// The leaders are at work and no fleet has a worker left: the job's own
	// run refuses, and no fleet's run is closed under it.
	stop("r2", "T1")
	orch(5, "run", "close")("still live")
	open("the job's run refused")
	stop("r1", "G1")
	stop("r1", "G2")
	if err := p.Kit.Store.Change(p.Root, "r2", func(s *contract.State) error {
		_, err := p.Kit.Rules.Start(s, "T1", true, false, "", contract.Agent{Kind: "claude", Name: "a-worker-2"}, false, contract.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Billing's worker is at work: the leaders are gone, the job is not closed.
	orch(5, "run", "close")("still live")
	open("a fleet's run refused")
	// Removing an open job closes it first, and is refused the same way, whole.
	orch(5, "run", "rm", "r1")("still live")
	if runs, _ := p.Kit.Store.Runs(p.Root); len(runs) != 3 || !runOf(t, p, "r3").Run.ClosedAt.IsZero() {
		t.Fatalf("a refused removal left the runs %v", runs)
	}
	stop("r2", "T1")
	orch(0, "run", "close")("Run r1 is closed")
	for _, id := range []string{"r1", "r2", "r3"} {
		if runOf(t, p, id).Run.ClosedAt.IsZero() {
			t.Fatalf("%s was left open", id)
		}
	}
	// A leader cannot end, hand over or remove its fleet's run: the tool does.
	orch(0, "run", "rm", "r1")("Run r1 is removed")
	if runs, _ := p.Kit.Store.Runs(p.Root); len(runs) != 0 {
		t.Fatalf("after the job was removed the runs are %v", runs)
	}
}

// set --project writes one key into whaleshark.toml and leaves every other
// line as it stands. What it writes waits for the person's approval like any
// change to the file, and a file that would no longer read is not written.
func TestSetWritesOneKeyOfTheProjectsFile(t *testing.T) {
	p := scenario.Prepare(t, nil, nil)
	file := filepath.Join(p.Root, "whaleshark.toml")
	human := func(exit int, args ...string) func(words ...string) string {
		return func(words ...string) string { return play(t, p.Command(scenario.Human, args...), exit, words...) }
	}
	play(t, p.Command(scenario.Human, "init"), 0)
	// No file yet: it is made, with the one section and the one line.
	human(0, "set", "limits.stale_minutes", "45", "--project")()
	if got := read(t, file); got != "[limits]\nstale_minutes = 45\n" {
		t.Fatalf("the file made:\n%s", got)
	}
	const mine = "# Our shop.\n[land]\n# who lands\nwho = \"human\"  # for now\n\n[limits]\nstale_minutes = 45\nquiet_seconds = 90 # a quiet team\n\n[[extra]]\nname = \"x\"\n"
	write(t, file, mine)
	human(0, "set", "land.check", `make "all" test`, "--project")()
	human(0, "set", "limits.quiet_seconds", "200", "--project")()
	human(0, "set", "worktrees.share", ".env, node_modules", "--project")()
	want := "# Our shop.\n[land]\ncheck = \"make \\\"all\\\" test\"\n# who lands\nwho = \"human\"  # for now\n\n[limits]\nstale_minutes = 45\nquiet_seconds = 200\n\n[[extra]]\nname = \"x\"\n\n[worktrees]\nshare = [\".env\", \"node_modules\"]\n"
	if got := read(t, file); got != want {
		t.Fatalf("the file after three keys:\n%s\nexpected:\n%s", got, want)
	}
	// It reads as it was written, and what can run something is held until trust.
	project, err := contract.ReadProjectFile(p.Root)
	if err != nil || project.Limits.QuietSeconds != 200 || project.Limits.StaleMinutes != 45 || project.Land.Check != "" || !strings.Contains(strings.Join(project.Held, " "), "land.check") {
		t.Fatalf("the file reads as %+v, held %v, %v", project.Limits, project.Held, err)
	}
	// Only the person, and only a value the key takes.
	play(t, p.Command(scenario.Orch, "set", "limits.quiet_seconds", "5", "--project"), 5, "is the person's to give")
	human(2, "set", "limits.quiet_seconds", "soon", "--project")("a number")
	human(2, "set", "limits.quiet_seconds", "5")("add --project")
	// A value over several lines cannot be replaced line by line: nothing is written.
	const long = "[worktrees]\nshare = [\n  \".env\",\n]\n"
	write(t, file, long)
	human(5, "set", "worktrees.share", "a,b", "--project")("would no longer read", "by hand")
	if got := read(t, file); got != long {
		t.Fatalf("a refused set changed the file:\n%s", got)
	}
	if _, err := os.Stat(file + ".new"); err == nil {
		t.Fatal("a file was left beside the project's")
	}
}

// init asks with the tools that are wired in and found; it brings a rules
// block of an earlier text up to date where init wrote it; and it leaves the
// hooks of a tool that is no longer wired in, saying so and keeping their
// record, since their form went with the tool's description.
func TestInitKnowsTheWiredToolsAndAnOlderBlock(t *testing.T) {
	p := scenario.Prepare(t, nil, nil)
	home, _ := os.UserHomeDir()
	dirs, err := p.Kit.Platform.Dirs()
	if err != nil {
		t.Fatal(err)
	}
	human := func(answers string, exit int, args ...string) func(words ...string) string {
		return func(words ...string) string {
			cmd := p.Command(scenario.Human, args...)
			cmd.Stdin = strings.NewReader(answers)
			return play(t, cmd, exit, words...)
		}
	}
	// A tool of the person's own, described by a file, its program found.
	bin, _ := p.Kit.Platform.SelfPath()
	wire := func() {
		t.Helper()
		if err := contract.WriteTool(p.Kit.Platform, dirs, contract.Tool{Name: "hermes", Label: "Hermes", Run: []string{bin}, Hooks: contract.HooksClaude}); err != nil {
			t.Fatal(err)
		}
	}
	wire()
	out := human("\n", 0, "init")("Which agent tools do you use here? (claude, codex, hermes;")
	if strings.Contains(out, "gemini") {
		t.Fatalf("init offered a tool that is not wired in:\n%s", out)
	}

	// The rules block: written, then as an earlier program left it.
	rulesFile := filepath.Join(home, ".claude", "CLAUDE.md")
	write(t, rulesFile, "# Mine\n")
	human("", 0, "init", "--agent", "claude")("the rules for the lead agent are in")
	earlier := read(t, filepath.Join("..", "guide", "block1.md"))
	if earlier == guide.Block || !strings.Contains(read(t, rulesFile), guide.Block) {
		t.Fatal("the test needs two texts of the block, and the current one written")
	}
	write(t, rulesFile, strings.Replace(read(t, rulesFile), guide.Block, earlier, 1))
	human("", 0, "init", "--agent", "claude")("were an earlier version's")
	if got := read(t, rulesFile); got != "# Mine\n\n"+guide.Block {
		t.Fatalf("the block was not brought up to date:\n%s", got)
	}
	if out := human("", 0, "init", "--agent", "claude")(); strings.Contains(out, "earlier version") {
		t.Fatalf("a current block was replaced again:\n%s", out)
	}
	// One the person changed is left, and said, as before.
	write(t, rulesFile, strings.Replace(read(t, rulesFile), "whaleshark status", "whaleshark status, please", 1))
	human("", 0, "init", "--agent", "claude")("it is left alone")
	// An earlier block that init did not write is the person's too.
	q := scenario.Prepare(t, nil, nil)
	home, _ = os.UserHomeDir()
	write(t, filepath.Join(home, ".claude", "CLAUDE.md"), earlier)
	play(t, q.Command(scenario.Human, "init", "--agent", "claude"), 0, "it is left alone")
	if got := read(t, filepath.Join(home, ".claude", "CLAUDE.md")); got != earlier {
		t.Fatal("a block init never wrote was changed")
	}

	// Hooks of a tool that is no longer wired in.
	r := scenario.Prepare(t, nil, nil)
	p = r
	if dirs, err = r.Kit.Platform.Dirs(); err != nil {
		t.Fatal(err)
	}
	wire()
	out = human("", 0, "init", "--agent", "hermes")("the status line and the gate for hermes are set in ")
	_, settings, _ := strings.Cut(out, "the status line and the gate for hermes are set in ")
	settings, _, _ = strings.Cut(settings, "\n")
	hooked := read(t, settings)
	if !strings.Contains(hooked, "whaleshark") {
		t.Fatalf("the hooks were not written into %s:\n%s", settings, hooked)
	}
	if err := contract.RemoveTool(dirs, "hermes"); err != nil {
		t.Fatal(err)
	}
	human("", 0, "init", "--remove")("the hooks for hermes stay in", "no longer wired in")
	if read(t, settings) != hooked {
		t.Fatal("hooks whose form is not known were touched")
	}
	wire()
	human("", 0, "init", "--remove")("the hooks for hermes are out of")
	if strings.Contains(read(t, settings), "whaleshark") {
		t.Fatalf("with the tool wired in again its hooks stayed:\n%s", read(t, settings))
	}
}

// A fleet the person keeps: saved with what it works on before any work,
// listed in status and at the end of the orchestrator's guide, joined by a
// group in the spelling it was saved under, still there when the job is
// closed, and forgotten only when asked. A fleet nobody saved is made by
// naming it at `task add`, and is in no list once its work has gone.
func TestAFleetIsSavedWithWhatItWorksOn(t *testing.T) {
	p := scenario.Prepare(t, nil, nil)
	orch := func(exit int, args ...string) func(words ...string) string {
		return func(words ...string) string { return play(t, p.Command(scenario.Orch, args...), exit, words...) }
	}
	write(t, filepath.Join(p.Root, "b.md"), briefText)
	orch(0, "run", "fleet")("No fleet is saved here")
	// Saved with no job open and Fleet Mode off: it says what is still missing.
	orch(0, "run", "fleet", "shop  front", "everything of the shop front: its pages and its checkout")(`The fleet "shop front" is saved`, "It stays when its work is done", "Fleet Mode is off")
	fleetsOn(t, p)
	orch(0, "run", "fleet", "Parcel Desk", "the parcel desk:\n what customers send in")(`"Parcel Desk" is saved · the parcel desk: what customers send in`)
	for _, bad := range [][]string{{"--oops", "x"}, {"one two three four", "x"}, {"a name of twenty-five chars", "x"}, {"ok", strings.Repeat("long ", 50)}} {
		play(t, p.Command(scenario.Orch, append([]string{"run", "fleet", "--"}, bad...)...), 2, "at most three words")
	}
	orch(4, "run", "fleet", "admin")("No fleet \"admin\" is saved here")
	orch(0, "run", "fleet")("Parcel Desk · saved · the parcel desk", "shop front · saved · everything of the shop front")

	orch(0, "run", "new", "the portals")("Run r1 is open")
	orch(0, "status")("saved fleet shop front · no work now · everything of the shop front", "saved fleet Parcel Desk · no work now")
	orch(0, "guide", "lead")("## Fleets the person keeps here", "- shop front: everything of the shop front: its pages and its checkout", "- Parcel Desk: the parcel desk")
	// Work for a saved fleet joins it however the name is typed; a name that
	// fits none makes a fleet on the spot.
	orch(0, "task", "add", "G1", "ten things", "--brief", "b.md", "--check", "none", "--fleet", "SHOP front")("G1")
	orch(0, "task", "add", "G2", "one thing", "--brief", "b.md", "--check", "none", "--fleet", "price list")("G2")
	if s, _ := p.Record(); s.Tasks["G1"].Fleet != "shop front" || s.Tasks["G2"].Fleet != "price list" {
		t.Fatalf("the groups are in %q and %q", s.Tasks["G1"].Fleet, s.Tasks["G2"].Fleet)
	}
	// What it works on can be said anew; the job's end leaves it saved.
	orch(0, "run", "fleet", "SHOP FRONT", "the shop front and its reports")(`The fleet "shop front" is saved · the shop front and its reports`)
	orch(0, "task", "cancel", "G1")()
	orch(0, "task", "cancel", "G2")()
	orch(0, "run", "close", "--abandon")()
	orch(0, "run", "fleet")("shop front · saved · the shop front and its reports", "Parcel Desk · saved")
	if out := orch(0, "run", "fleet")(); strings.Contains(out, "price list") {
		t.Errorf("a fleet nobody saved is listed:\n%s", out)
	}
	orch(0, "run", "fleet", "parcel desk", "--forget")(`The fleet "Parcel Desk" is saved no longer`)
	if out := orch(0, "run", "fleet")("shop front"); strings.Contains(out, "Parcel") {
		t.Errorf("a forgotten fleet is listed:\n%s", out)
	}
	// A file that came with somebody else's folder names no fleet that is none.
	write(t, filepath.Join(p.Root, contract.ProjectDir, "fleets.json"), `{"version":1,"fleets":[{"name":"--model x","purpose":"p"},{"name":"fine","purpose":"a\nb"},{"name":"kept","purpose":"plain"}]}`)
	if out := orch(0, "run", "fleet")("kept · saved · plain"); strings.Contains(out, "model") || strings.Contains(out, "fine") {
		t.Errorf("an entry that is no fleet is listed:\n%s", out)
	}
	// Where the project brings something nobody approved, what a fleet works
	// on is the person's to read and no agent's: not in the list, not by
	// naming the fleet, not in status, not in the guide. Approved, it is.
	write(t, filepath.Join(p.Root, "whaleshark.toml"), "[setup]\nscript = \"make ready\"\n")
	orch(0, "run", "new", "the next job")("is open")
	for _, line := range [][]string{{"run", "fleet"}, {"run", "fleet", "kept"}, {"status"}, {"guide", "lead"}} {
		if out := orch(0, line...)("kept"); strings.Contains(out, " plain\n") {
			t.Errorf("%v shows an agent a sentence from a project nobody approved:\n%s", line, out)
		}
	}
	orch(0, "guide", "lead")("- kept\n", "once the person has approved what this project brings: `whaleshark trust`")
	play(t, p.Command(scenario.Human, "run", "fleet"), 0, "kept · saved · plain")
	lines, err := contract.TrustLines(p.Root)
	if err == nil {
		err = contract.WriteTrust(p.Kit.Platform, p.Root, lines, contract.Now())
	}
	if err != nil {
		t.Fatal(err)
	}
	orch(0, "run", "fleet")("kept · saved · plain")
	orch(0, "guide", "lead")("- kept: plain")
}
