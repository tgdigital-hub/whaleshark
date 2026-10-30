package inbox_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// With [tasks] close_on_accept the sweep closes the tab and removes the copy
// of a task whose pull request is merged, and only then: the same task with
// the setting off, or with its pull request still open, keeps both.
func TestAMergedPullRequestGivesUpItsTab(t *testing.T) {
	for _, pair := range testkit.GitEnv()[len(os.Environ()):] {
		name, value, _ := strings.Cut(pair, "=")
		t.Setenv(name, value)
	}
	p := scenario.Prepare(t, nil, map[string]string{"A.1": `edit a.txt | commit | write-result | report done "in"`})
	testkit.Git(t, p.Root, "init", "-q", "-b", "main")
	brief := "# Target\nt\n# Change\nc\n# Constraints\nc\n# Ownership\no\n# Acceptance\na\n"
	testkit.Commit(t, p.Root, "start", map[string]string{"b.md": brief, ".gitignore": "whaleshark.toml\n"})
	approve := func(toml string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(p.Root, "whaleshark.toml"), []byte(toml), 0o600); err != nil {
			t.Fatal(err)
		}
		lines, err := contract.TrustLines(p.Root)
		if err == nil {
			err = contract.WriteTrust(p.Kit.Platform, p.Root, lines, contract.Now())
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	orch := func(args ...string) {
		t.Helper()
		if out, err := p.Command(scenario.Orch, args...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	const pr = "[land]\nmode = \"pr-per-task\"\n"
	approve(pr + "[tasks]\nclose_on_accept = true\n")
	orch("init")
	orch("run", "new", "a job")
	orch("task", "add", "A", "the part", "--brief", "b.md", "--check", "none")
	orch("start", "A")
	var a *contract.Attempt
	for end := time.Now().Add(20 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		s, _ := p.Record()
		if a = s.Attempts["A.1"]; a != nil && a.State == contract.AttemptReported {
			break
		}
		if time.Now().After(end) {
			t.Fatalf("no report: %+v", a)
		}
	}
	settle := func(state string) {
		t.Helper()
		_, dir := p.Record()
		err := p.Kit.Store.Change(p.Root, filepath.Base(dir), func(s *contract.State) error {
			s.Tasks["A"].Status = contract.TaskDone
			s.Tasks["A"].PR = &contract.PullRequest{Number: 1, State: state}
			s.Attempts["A.1"].State = contract.AttemptAccepted
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	look := func() (tab, copied bool) {
		t.Helper()
		p.Clock(time.Minute)
		orch("status")
		snap, err := p.Kit.Terms.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		s, _ := p.Record()
		return slices.ContainsFunc(snap.Panes, func(pane contract.Pane) bool { return pane.ID == a.Place.Pane }), s.Tasks["A"].Worktree.RemovedAt.IsZero()
	}
	settle(contract.PRWatching)
	if tab, copied := look(); !tab || !copied {
		t.Fatalf("an open pull request's task: its tab %t, its copy %t", tab, copied)
	}
	settle(contract.PRMerged)
	approve(pr)
	if tab, copied := look(); !tab || !copied {
		t.Fatalf("with the setting off: its tab %t, its copy %t", tab, copied)
	}
	approve(pr + "[tasks]\nclose_on_accept = true\n")
	if tab, copied := look(); tab || copied {
		t.Fatalf("a merged task kept its tab (%t) or its copy (%t)", tab, copied)
	}
}
