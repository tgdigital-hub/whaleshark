package overlap

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

const groupBrief = "# Target\nthe shop\n## Change\nall of it\n## Constraints\nnone\n## Ownership\nits own files\n## Acceptance\nit passes\n"

// lead starts a group as start does for a fleet that is new: a run of the
// fleet's own that begins at the collected work, a tab for its leader, the
// attempt placed there and made the leader. It returns a folder that stands
// on the fleet's branch, where the work its leader accepts is committed.
func (f *fix) lead(group, fleet, run string) string {
	f.t.Helper()
	k, up, now := f.k, f.state(), contract.Now()
	low := k.Rules.NewRun(run, "the fleet "+fleet, 8, &contract.GitRef{Ref: up.Run.Integration.Branch, OID: up.Run.Integration.Tip}, contract.Binding{}, now)
	_, in, err := k.Integrator.Begin(f.root, low)
	must(f.t, err)
	must(f.t, k.Rules.Integrated(low, *in))
	must(f.t, k.Store.Create(f.root, low))
	tab, err := k.Terms.TabCreate("", "", f.root, fleet, nil)
	must(f.t, err)
	must(f.t, k.Store.Both(f.root, up.Run.ID, run, func(up, low *contract.State) error {
		id, err := k.Rules.Start(up, group, false, false, contract.TokenHash("token-of-"+group), contract.Agent{Kind: "claude", Name: "a-" + run}, false, now)
		if err == nil {
			err = k.Rules.Placed(up, id, contract.Place{Tab: tab.Tab, Pane: tab.ID, Cwd: f.root, OpenedByUs: true}, now)
		}
		if err == nil {
			err = k.Rules.Lead(up, low, id, contract.Binding{Pane: tab.ID}, in.Branch, now)
		}
		if err == nil {
			err = k.Rules.Working(up, id, now)
		}
		// What a sweep would have seen of the leader in its tab.
		up.Attempts[id].Seen.Liveness = contract.Live
		return err
	}))
	dir := filepath.Join(f.work, run)
	f.git(f.root, "worktree", "add", "-q", dir, in.Branch)
	return dir
}

// leader runs the real program from the tab of a group's leader, with what a
// start gives that tab, and holds it to its exit code.
func (f *fix) leader(exit int, group string, args ...string) string {
	f.t.Helper()
	up := f.state()
	a := up.Attempts[group+".1"]
	dir := filepath.Join(f.k.Store.Dir(f.root, up.Run.ID), "attempts", a.ID)
	must(f.t, os.MkdirAll(dir, 0o700))
	must(f.t, os.WriteFile(filepath.Join(dir, "token"), []byte("token-of-"+group+"\n"), 0o600))
	must(f.t, os.WriteFile(filepath.Join(dir, "result.md"), []byte("done as the brief asks\n"), 0o600))
	bin, _ := exec.LookPath("whaleshark")
	cmd := f.p.Command(scenario.Unbound, args...)
	cmd.Env = append(cmd.Env, contract.EnvPane+"="+a.Place.Pane, contract.EnvRoot+"="+f.root, contract.EnvRun+"="+up.Run.ID,
		contract.EnvTask+"="+group, contract.EnvAttempt+"="+a.ID, contract.EnvBin+"="+bin)
	out, _ := cmd.CombinedOutput()
	if got := cmd.ProcessState.ExitCode(); got != exit {
		f.t.Fatalf("%s's leader, %v: exit %d, expected %d\n%s", group, args, got, exit, out)
	}
	return string(out)
}

func (f *fix) human(exit int, args ...string) string {
	f.t.Helper()
	cmd := f.p.Command(scenario.Human, args...)
	out, _ := cmd.CombinedOutput()
	if got := cmd.ProcessState.ExitCode(); got != exit {
		f.t.Fatalf("the person, %v: exit %d, expected %d\n%s", args, got, exit, out)
	}
	return string(out)
}

// said holds what a command printed to words it must have in it.
func said(t *testing.T, out string, words ...string) {
	t.Helper()
	for _, w := range words {
		if !strings.Contains(out, w) {
			t.Fatalf("expected %q in:\n%s", w, out)
		}
	}
}

// The row's first scenario: two fleets change one line of one file. The
// orchestrator's scan says so while both are at work; the first group is
// collected as a merge; the second no longer lands and is refused with the
// files; sync sends its leader the brief, and a worker of that fleet merges
// the collected work into the fleet's branch; then it is collected too, the
// whole job lands in one step, and the fleets' runs go with the job's.
func TestTwoFleetsOnOneFileThroughScanSyncMergeAndLanding(t *testing.T) {
	f := start(t, map[string]string{"cart.js": alpha(), "notes.txt": "notes\n"})
	// Fleet Mode is the person's switch, and holds from the next job.
	f.human(0, "set", "fleets.mode", "on")
	f.play(0, "run", "close")
	said(t, f.play(0, "run", "new", "the shop, by fleets"), "Run r2 is open")
	if s := f.state(); s.Run.Mode != contract.ModeFleets || s.Run.Limit < 100 || s.Run.Integration == nil {
		t.Fatalf("the job is no orchestrator's run: mode %q, limit %d", s.Run.Mode, s.Run.Limit)
	}
	must(t, os.WriteFile(filepath.Join(f.root, "b.md"), []byte(groupBrief), 0o600))
	said(t, f.play(contract.ExitUsage, "task", "add", "G1", "billing work", "--brief", "b.md", "--check", "exit 0"), "--fleet")
	said(t, f.play(contract.ExitUsage, "task", "add", "G1", "billing work", "--brief", "b.md", "--check", "exit 0", "--fleet", "billing", "--shared"), "Only one of")
	f.play(0, "task", "add", "G1", "billing work", "--brief", "b.md", "--check", "git cat-file -e HEAD:billing.js", "--fleet", "billing")
	f.play(0, "task", "add", "G2", "search work", "--brief", "b.md", "--check", "git cat-file -e HEAD:search.js && git cat-file -e HEAD:billing.js", "--fleet", "search")
	if g := f.state().Tasks["G2"]; g.Fleet != "search" || g.Placement != contract.PlaceFleet || g.Status != contract.TaskReady {
		t.Fatalf("the group is recorded as %+v", g)
	}

	billing, search := f.lead("G1", "billing", "r3"), f.lead("G2", "search", "r4")
	testkit.Commit(t, billing, "T1: the cart's total", map[string]string{"cart.js": alpha(10), "billing.js": "b\n"})
	testkit.Commit(t, search, "T1: the cart's search", map[string]string{"cart.js": alpha(10, 11), "search.js": "s\n"})

	// Scan: both at work, one line of one file.
	said(t, f.play(contract.ExitClash, "overlap"), "G1 and G2 would conflict", "cart.js", "whaleshark sync G2")
	testkit.Commit(t, billing, "T2: the invoice", map[string]string{"invoice.txt": "i\n"})
	// The first leader reports; the orchestrator's wait hands over the
	// report and, from the scan that the report made due, the overlap.
	f.leader(0, "G1", "report", "done", "billing is done")
	said(t, f.play(0, "wait", "--timeout", "20"), "done G1", "overlap G2", "cart.js")

	// Merge: the first group is collected with its fleet's commits kept.
	said(t, f.play(0, "accept", "G1"), "G1 is done, checked")
	tip := f.git(f.root, "rev-parse", "whaleshark/r2")
	if got := f.git(f.root, "log", "--format=%s", "main.."+tip); got != "G1: billing work\nT2: the invoice\nT1: the cart's total" {
		t.Fatalf("collected for G1:\n%s", got)
	}
	// The second no longer lands, and is not collected over the first.
	said(t, f.play(contract.ExitNoLand, "overlap"), "G2 no longer lands on the collected work", "cart.js", "whaleshark sync G2")
	f.leader(0, "G2", "report", "done", "search is done")
	said(t, f.play(contract.ExitRefused, "accept", "G2"), "does not merge with the collected work", "cart.js", "whaleshark sync G2")
	if f.git(f.root, "rev-parse", "whaleshark/r2") != tip || f.state().Tasks["G2"].Status != contract.TaskReview {
		t.Fatal("a group that clashes moved the branch or left its review")
	}

	// Sync: the leader is sent the brief, and its result goes back with it.
	said(t, f.play(0, "sync", "G2"), "Sent to G2 as message", "1 kind of clash", "its result was sent back")
	brief := filepath.Join(f.k.Store.Dir(f.root, "r2"), "msg", "sync-G2.md")
	text, err := os.ReadFile(brief)
	must(t, err)
	said(t, string(text), "whaleshark/r2", "cart.js", "content")
	if g := f.state().Tasks["G2"]; g.Status != contract.TaskRunning {
		t.Fatalf("after sync the group is %s", g.Status)
	}
	said(t, f.leader(0, "G2", "mail"), "sync-G2.md")
	// The leader hands the brief to a worker of its fleet as it stands: a
	// task of the fleet's own run, which is the leader's to plan in.
	said(t, f.leader(0, "G2", "task", "add", "T2", "up to date", "--brief", brief, "--check", "exit 0"), "T2", "ready")
	low, err := f.k.Store.Read(f.root, "r4")
	must(t, err)
	if low.Tasks["T2"] == nil || f.state().Tasks["T2"] != nil {
		t.Fatal("the leader's task did not go into its fleet's run")
	}
	// The worker merges the collected work into the fleet's branch and
	// settles the clash, keeping what both sides were for.
	if out, err := exec.Command("git", "-C", search, "merge", "-q", "whaleshark/r2").CombinedOutput(); err == nil {
		t.Fatalf("the merge was expected to clash:\n%s", out)
	}
	must(t, os.WriteFile(filepath.Join(search, "cart.js"), []byte(alpha(10, 11)+"// with billing's total\n"), 0o600))
	f.git(search, "add", "cart.js")
	f.git(search, "commit", "-q", "-m", "T2: brought up to date with the collected work")
	said(t, f.play(0, "overlap"), "No file the tasks have changed is shared, and everything lands.")
	f.leader(0, "G2", "report", "done", "search is done, on top of billing")
	said(t, f.play(0, "accept", "G2"), "G2 is done, checked")
	tip = f.git(f.root, "rev-parse", "whaleshark/r2")
	if got := f.git(f.root, "log", "--first-parent", "--format=%s|%(trailers:key=Whaleshark-Task,valueonly,separator=)", "main.."+tip); got != "G2: search work|r2/G2\nG1: billing work|r2/G1" {
		t.Fatalf("the job's own line of commits:\n%s", got)
	}
	said(t, f.git(f.root, "log", "--format=%s", "main.."+tip), "T1: the cart's search", "T1: the cart's total", "T2: the invoice")

	// Landing is the person's, once, for the whole job; a leader cannot.
	f.leader(contract.ExitRefused, "G2", "land", "--local")
	said(t, f.human(0, "land", "--local"), "merged into main")
	if f.git(f.root, "rev-parse", "main") != tip || !strings.Contains(f.git(f.root, "show", "main:cart.js"), "with billing's total") {
		t.Fatal("main does not hold the job")
	}
	// The job ends, and its fleets' runs with it: closed, then removed with
	// their copies and with the branches the job's branch holds all of.
	f.git(f.root, "worktree", "remove", "--force", billing)
	f.git(f.root, "worktree", "remove", "--force", search)
	// Each leader's attempt ended when its group was accepted; nothing is live.
	said(t, f.play(0, "run", "close"), "Run r2 is closed")
	for _, run := range []string{"r3", "r4"} {
		low, err := f.k.Store.Read(f.root, run)
		if must(t, err); low.Run.ClosedAt.IsZero() {
			t.Fatalf("the fleet's run %s was left open under a closed job", run)
		}
	}
	out := f.play(0, "run", "rm", "r2")
	if runs, _ := f.k.Store.Runs(f.root); strings.Join(runs, ",") != "r1" || strings.Contains(out, "Kept") {
		t.Fatalf("after run rm the runs are %v, and it printed:\n%s", runs, out)
	}
	// The first, empty run of this project was closed and not removed: its branch alone stays.
	if left := strings.Fields(f.git(f.root, "branch", "--list", "whaleshark/*")); len(left) != 1 || left[0] != "whaleshark/r1" {
		t.Fatalf("branches were left: %s", left)
	}
}

// A deep scan of an orchestrator's run does not look at the folder that
// stands on a fleet's branch: the branch is the fleet's work, and what is not
// committed is looked for in its tasks' own folders. With two groups of one
// fleet open the fleet is still one, named by its first.
func TestAFleetIsScannedAsOneAndOnlyItsBranch(t *testing.T) {
	f := start(t, map[string]string{"cart.js": alpha()})
	f.change(func(s *contract.State) { s.Run.Mode = contract.ModeFleets })
	must(t, os.WriteFile(filepath.Join(f.root, "b.md"), []byte(groupBrief), 0o600))
	for _, g := range [][2]string{{"G1", "billing"}, {"G2", "search"}, {"G3", "billing"}} {
		f.play(0, "task", "add", g[0], "work "+g[0], "--brief", "b.md", "--check", "exit 0", "--fleet", g[1])
	}
	billing, search := f.lead("G1", "billing", "r2"), f.lead("G2", "search", "r3")
	// The second group of billing goes to the leader that sits.
	must(t, f.k.Store.Both(f.root, "r1", "r2", func(up, low *contract.State) error {
		id, err := f.k.Rules.Start(up, "G3", false, false, "", contract.Agent{}, false, time.Now())
		if err == nil {
			err = f.k.Rules.Lead(up, low, id, contract.Binding{}, "", time.Now())
		}
		return err
	}))
	testkit.Commit(t, billing, "T1", map[string]string{"cart.js": alpha(10)})
	testkit.Commit(t, search, "T1", map[string]string{"cart.js": alpha(10, 11)})
	must(t, os.WriteFile(filepath.Join(search, "loose.txt"), []byte("not committed\n"), 0o600))
	want := "overlap G1+G2 (billing and search: content) cart.js"
	same(t, "found", f.scan(false), want)
	same(t, "found by the deep scan", f.scan(true), want)
	same(t, "found for the fleet's second group", f.scan(false, "G3"), want)
	// A fleet that ended is scanned no more.
	f.change(func(s *contract.State) { s.Run.Fleets["search"].EndedAt = time.Now() })
	same(t, "found once a fleet ended", f.scan(false))
}

// flight puts a task in flight into a fleet's own run: a copy of the code
// cut from the fleet's branch, with one commit of the files given, and an
// agent at work in it.
func (f *fix) flight(run, id string, files map[string]string) string {
	f.t.Helper()
	low, err := f.k.Store.Read(f.root, run)
	must(f.t, err)
	dir, branch := filepath.Join(f.work, run+"-"+id), "whaleshark/"+run+"-"+id
	f.git(f.root, "worktree", "add", "-q", "-b", branch, dir, low.Run.Integration.Branch)
	if files != nil {
		testkit.Commit(f.t, dir, "the work of "+id, files)
	}
	must(f.t, f.k.Store.Change(f.root, run, func(s *contract.State) error {
		s.Tasks[id] = &contract.Task{ID: id, Name: "part " + id, Title: "the part " + id, Status: contract.TaskRunning, Check: "exit 0",
			Attempts: []string{id + ".1"}, Worktree: &contract.Worktree{Path: dir, Branch: branch}}
		s.Attempts[id+".1"] = &contract.Attempt{ID: id + ".1", Task: id, N: 1, State: contract.AttemptWorking}
		return nil
	}))
	return dir
}

// fleets makes the first run an orchestrator's, with one group handed to
// each fleet named and its leader at work: the fleets' runs are r2, r3, ...
func (f *fix) fleets(names ...string) {
	f.t.Helper()
	f.change(func(s *contract.State) { s.Run.Mode = contract.ModeFleets })
	must(f.t, os.WriteFile(filepath.Join(f.root, "b.md"), []byte(groupBrief), 0o600))
	for i, name := range names {
		group := fmt.Sprint("G", i+1)
		f.play(0, "task", "add", group, name+" work", "--brief", "b.md", "--check", "exit 0", "--fleet", name)
		f.lead(group, name, fmt.Sprint("r", i+2))
	}
}

func write(t testing.TB, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		must(t, os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600))
	}
}

// Two fleets, one file, both workers still at work with nothing committed
// and nothing accepted by either leader: one deep scan of the orchestrator's
// run finds it and names both fleets. Work of one fleet is not paired there;
// that is its leader's scan, which sees its own run and no other.
func TestTwoFleetsStillAtWorkOnOneFile(t *testing.T) {
	f := start(t, map[string]string{"cart.js": alpha(), "notes.txt": alpha()})
	f.fleets("billing", "search")
	refund, also, filter := f.flight("r2", "T1", nil), f.flight("r2", "T2", nil), f.flight("r3", "T1", nil)
	write(t, refund, map[string]string{"cart.js": alpha(10), "notes.txt": alpha(3)})
	write(t, also, map[string]string{"notes.txt": alpha(3, 4)})
	write(t, filter, map[string]string{"cart.js": alpha(10, 11)})

	const where = "billing's T1 and search's T1: "
	loose := "same G1+G2 (" + where + "merges cleanly; billing's T1 has it not committed; search's T1 has it not committed) cart.js"
	clash := "overlap G1+G2 (" + where + "content, not committed) cart.js"
	same(t, "found by the scan after a report", f.scan(false), loose)
	same(t, "found by one deep scan", f.scan(true), clash)
	// The leader's own scan: its two workers, and nothing of the other fleet.
	low, err := f.k.Store.Read(f.root, "r2")
	must(t, err)
	found, err := f.o.Scan(context.Background(), f.root, low, nil, true)
	must(t, err)
	same(t, "found by billing's leader", lines(found), "overlap T2+T1 (content, not committed) notes.txt")

	// The orchestrator's wait runs the deep scan five minutes after the last
	// and hands the finding over; the next scans do not say it again.
	f.p.Clock(6 * time.Minute)
	said(t, f.play(0, "wait", "--timeout", "20"), "overlap G2", "cart.js")
	s := f.state()
	if n := events(s, "overlap"); n != 1 || len(s.Run.Findings) != 1 || !strings.HasPrefix(s.Run.Findings[0].How, where) {
		t.Fatalf("%d overlap events, findings %+v", n, s.Run.Findings)
	}
	// Each fleet's own task is a field of the finding, not only a sentence.
	if got := s.Run.Findings[0].Work; !slices.Equal(got, []string{"T1", "T1"}) {
		t.Fatalf("the finding names the fleets' own tasks as %q", got)
	}
	said(t, f.play(contract.ExitClash, "overlap"), "G1 and G2 would conflict", where, "cart.js", "whaleshark sync G2")
	testkit.Commit(t, refund, "the refund flow", map[string]string{"cart.js": alpha(10)})
	testkit.Commit(t, filter, "the cart filter", map[string]string{"cart.js": alpha(10, 11)})
	said(t, f.play(contract.ExitClash, "overlap"), where+"content)")
	// Accepted by billing's leader, it is the fleet's own work that clashes.
	f.git(filepath.Join(f.work, "r2"), "merge", "-q", "--ff-only", "whaleshark/r2-T1")
	same(t, "found once the leader accepted", f.scan(false), "overlap G1+G2 (billing and search's T1: content) cart.js")
	found, err = f.o.Scan(context.Background(), f.root, f.state(), nil, false)
	if must(t, err); len(found) != 1 || !slices.Equal(found[0].Work, []string{"", "T1"}) {
		t.Fatalf("with billing's work accepted the finding is %+v", found)
	}
	if n := events(f.state(), "overlap"); n != 1 {
		t.Fatalf("one clash was announced %d times", n)
	}

	// A run that does not say it is this job's fleet is not read.
	must(t, f.k.Store.Change(f.root, "r3", func(s *contract.State) error { s.Run.Parent.Run = "r9"; return nil }))
	f.change(func(s *contract.State) { s.Run.Findings = nil })
	same(t, "found with a run that is not this job's", f.scan(true))
}

// sync of a group counts on the group, by the rules themselves, and says
// after the second which is collected next.
func TestSyncOfAGroupIsCounted(t *testing.T) {
	f := start(t, map[string]string{"cart.js": alpha()})
	f.fleets("billing", "search")
	f.collect(map[string]string{"cart.js": alpha(10)})
	var out strings.Builder
	for n := 1; n <= 2; n++ {
		got, err := scanner{f.k}.sync(&contract.Call{Kit: f.k, Command: contract.Find("sync"), Args: []string{"G2"},
			Caller: contract.Caller{Kind: contract.Human}, Root: f.root, Run: "r1", Now: contract.Now(), Out: &out})
		if must(t, err); got.(*synced).Times != n {
			t.Fatalf("sync %d of the group was counted as %d", n, got.(*synced).Times)
		}
	}
	if g := f.state().Tasks["G2"]; g.Synced != 2 || g.SyncBrief != "" {
		t.Fatalf("the group has synced %d, brief %q", g.Synced, g.SyncBrief)
	}
	said(t, out.String(), "G2 has been sent to sync 2 times: accept it next")
}

// Twenty copies in four fleets, each thirty files of a large repository,
// three of them from forty that all draw on: the orchestrator's scan, first,
// with nothing moved, and deep with every copy holding work not committed.
func TestTwentyInFourFleets(t *testing.T) {
	if *files == 0 || *realTree != "" {
		t.Skip("a measurement: run with -files 2000")
	}
	tree, names := map[string]string{}, []string{}
	for n := range *files {
		names = append(names, fmt.Sprintf("dir%02d/file%05d.txt", n%40, n))
		tree[names[n]] = alpha()
	}
	f := start(t, tree)
	f.fleets("billing", "search", "checkout", "accounts")
	var dirs []string
	for i := range 20 {
		change := map[string]string{}
		for j := range 27 {
			change[names[(i*97+j*31+40)%*files]] = alpha(i%30 + 1)
		}
		for j := range 3 {
			change[names[(i*7+j*13)%40]] = alpha((i+j)%30 + 1)
		}
		dirs = append(dirs, f.flight(fmt.Sprint("r", i%4+2), fmt.Sprint("T", i/4+1), change))
	}
	scan := func(deep bool) ([]contract.Finding, time.Duration) {
		began := time.Now()
		found, err := f.o.Scan(context.Background(), f.root, f.state(), nil, deep)
		must(t, err)
		return found, time.Since(began)
	}
	found, first := scan(false)
	_, again := scan(false)
	for i, dir := range dirs {
		must(t, os.WriteFile(filepath.Join(dir, names[i%40]), []byte(alpha(i%30+2)), 0o600))
	}
	wip, deep := scan(true)
	t.Logf("%d files, 20 copies in 4 fleets, %d findings: first scan %v, scan with nothing moved %v; %d findings by the deep scan in %v",
		*files, len(found), first, again, len(wip), deep)
	for _, f := range slices.Concat(found, wip) {
		if !slices.Equal(f.Tasks[:1], f.Tasks[1:]) && !strings.Contains(f.How, ": ") {
			t.Fatalf("a finding across fleets that does not name them: %+v", f)
		}
	}
	if len(found) == 0 || *files == 2000 && max(first, deep) > 3*time.Second {
		t.Fatalf("%d findings; the first scan took %v and the deep one %v", len(found), first, deep)
	}
}
