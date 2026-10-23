package integrate_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// fakeGH is a stand-in for gh that keeps its pull requests as files in one
// folder: a number for each branch, the body, the order of the merges, and
// the checks a test writes as checks-<n>; with none written they run.
const fakeGH = `#!/bin/sh
d='%s'
case "$1 $2" in
"pr view")
	if [ "$5" = state,mergeCommit ]; then
		if [ -f "$d/merged-$3" ]; then echo "MERGED $(cat "$d/merged-$3")"; else echo "OPEN null"; fi
		exit 0
	fi
	n=$(cat "$d/branch-$(echo "$3" | tr / _)" 2>/dev/null) || exit 1
	state=OPEN; [ -f "$d/merged-$n" ] && state=MERGED
	echo "$n https://example.test/pull/$n $state" ;;
"pr create")
	n=$(( $(cat "$d/count" 2>/dev/null || echo 0) + 1 )); echo $n > "$d/count"
	head=; prev=
	for a in "$@"; do [ "$prev" = --head ] && head=$a; prev=$a; done
	echo $n > "$d/branch-$(echo "$head" | tr / _)"
	echo "$*" > "$d/args-$n"
	cat > "$d/body-$n"
	echo "https://example.test/pull/$n" ;;
"pr ready") echo "$3" >> "$d/ready" ;;
"pr checks")
	if [ -f "$d/checks-$3" ]; then cat "$d/checks-$3"; else echo '[{"name":"ci","bucket":"pending"}]'; fi ;;
"pr merge")
	echo "$3" >> "$d/order"; echo "$4" > "$d/method-$3"; printf '%%040d' "$3" > "$d/merged-$3" ;;
*) exit 1 ;;
esac
`

// forge is a run in pr-per-task mode with a bare repository as its remote
// and the stand-in for gh first on the path.
type forge struct {
	*run
	remote, gh string
	now        time.Time
}

func openForge(t *testing.T, toml string, files map[string]string) *forge {
	r := open(t, files)
	if r.k.Platform.System() == "windows" {
		t.Skip("the stand-in for gh is a shell script")
	}
	f := &forge{run: r, remote: t.TempDir(), gh: t.TempDir(), now: contract.Now()}
	testkit.Git(t, f.remote, "init", "-q", "--bare", "-b", "main")
	r.git(r.root, "remote", "add", "origin", f.remote)
	r.git(r.root, "push", "-q", "origin", "main")
	tools := t.TempDir()
	write(t, filepath.Join(tools, "gh"), strings.ReplaceAll(strings.Replace(fakeGH, "%s", f.gh, 1), "%%", "%"))
	must(t, os.Chmod(filepath.Join(tools, "gh"), 0o700))
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	r.approve("[land]\nmode = \"pr-per-task\"\n" + toml)
	return f
}

// checks sets what the checks of pull request n say.
func (f *forge) checks(n int, json string) {
	write(f.t, filepath.Join(f.gh, "checks-"+strconv.Itoa(n)), json)
}

// watch is one look of the sweep's, a little more than its spacing later.
func (f *forge) watch() {
	f.t.Helper()
	f.now = f.now.Add(31 * time.Second)
	must(f.t, f.k.Integrator.Watch(f.root, "r1", f.now))
}

func (f *forge) said(file string) string {
	data, _ := os.ReadFile(filepath.Join(f.gh, file))
	return strings.TrimSpace(string(data))
}

// Five tasks become five pull requests, each with its own title and body,
// and each is merged by itself, one at a time, in the order its checks pass.
// Nothing is merged while checks run, and the run's branch never moves.
func TestFivePullRequests(t *testing.T) {
	f := openForge(t, "", map[string]string{"a.txt": "one\n"})
	ids := []string{"A", "B", "C", "D", "E"}
	for _, id := range ids {
		f.task(id, map[string]string{"page-" + id + ".txt": id + "\n"})
	}
	_, runDir := f.p.Record()
	write(t, filepath.Join(contract.AttemptDir(runDir, "C.1"), contract.PRBody), "the wire contract of C\n")
	tip := f.branch()
	play(t, f.p.Command(scenario.Orch, "accept", "A", "B", "C", "D", "E"), 0, "https://example.test/pull/5", "merged or ci_failed")
	s := f.state()
	for i, id := range ids {
		pr := s.Tasks[id].PR
		if pr == nil || pr.Number != i+1 || pr.State != contract.PRWatching || s.Tasks[id].Status != contract.TaskReview {
			t.Fatalf("%s waits on %+v as %s", id, pr, s.Tasks[id].Status)
		}
		if pushed := testkit.Git(t, f.remote, "rev-parse", s.Tasks[id].Worktree.Branch); pushed != pr.Head {
			t.Fatalf("the remote has %s at %s, the pull request %s", id, pushed, pr.Head)
		}
	}
	if args := f.said("args-3"); !strings.Contains(args, "--base main --head whaleshark/r1-C --title C: the C page --body-file -") || f.said("body-3") != "the wire contract of C" {
		t.Fatalf("gh was handed %q with the body %q", args, f.said("body-3"))
	}
	f.watch()
	if f.said("order") != "" {
		t.Fatalf("merged while the checks ran: %s", f.said("order"))
	}
	for n, number := range []int{4, 2, 5, 1, 3} {
		f.checks(number, `[{"name":"ci","bucket":"pass"},{"name":"docs","bucket":"skipping"}]`)
		f.watch()
		order := strings.Fields(f.said("order"))
		id := ids[number-1]
		if len(order) != n+1 || order[n] != strconv.Itoa(number) || f.said("method-"+strconv.Itoa(number)) != "--squash" {
			t.Fatalf("after %d went green the merges are %v", number, order)
		}
		s := f.state()
		if s.Tasks[id].Status != contract.TaskDone || s.Tasks[id].PR.State != contract.PRMerged || !strings.HasSuffix(s.Tasks[id].Accepted.Commit, strconv.Itoa(number)) {
			t.Fatalf("%s is %s with %+v", id, s.Tasks[id].Status, s.Tasks[id].PR)
		}
		if heads := testkit.Git(t, f.remote, "for-each-ref", "--format=%(refname)"); strings.Contains(heads, "-"+id) {
			t.Fatalf("the branch of %s is still on the remote: %s", id, heads)
		}
	}
	if kinds := f.kinds(); strings.Count(kinds, "merged") != 5 || strings.Contains(kinds, "ci_failed") {
		t.Fatalf("the events %s", kinds)
	}
	if f.branch() != tip || f.git(f.root, "rev-parse", "main") != f.state().Run.Base.OID {
		t.Fatal("the run's branch or the project's own main moved")
	}
	play(t, f.p.Command(scenario.Human, "land", "--pr"), 5, "pr-per-task")
}

// A red check goes back to the worker; while it does nothing is merged, even
// when the checks turn green. The worker's fix is accepted again on the same
// pull request, which is watched again and merged.
func TestARedCheckGoesBack(t *testing.T) {
	f := openForge(t, "draft_first = true\n", map[string]string{"a.txt": "one\n"})
	dir := f.task("A", map[string]string{"a.txt": "two\n"})
	play(t, f.p.Command(scenario.Orch, "accept", "A"), 0, "https://example.test/pull/1")
	if args := f.said("args-1"); !strings.Contains(args, "--draft") || f.said("ready") != "1" {
		t.Fatalf("not a draft made ready: %q, ready %q", args, f.said("ready"))
	}
	f.checks(1, `[{"name":"ci","bucket":"pass"},{"name":"lint","bucket":"fail"}]`)
	f.watch()
	s := f.state()
	if pr := s.Tasks["A"].PR; pr.State != contract.PRFailed || !strings.Contains(f.kinds(), "ci_failed") {
		t.Fatalf("after a red check %+v, events %s", pr, f.kinds())
	}
	if e := s.Inbox.Events[len(s.Inbox.Events)-1]; e.Task != "A" || !strings.Contains(e.Text, "lint") || strings.Contains(e.Text, "ci,") {
		t.Fatalf("the event %+v", e)
	}
	f.change(func(s *contract.State) { must(t, f.k.Rules.Reject(s, "A", "lint fails", contract.Live, f.now)) })
	f.checks(1, `[{"name":"ci","bucket":"pass"},{"name":"lint","bucket":"pass"}]`)
	f.watch()
	if f.said("order") != "" {
		t.Fatal("merged while the task was sent back")
	}
	fix := testkit.Commit(t, dir, "lint", map[string]string{"a.txt": "two, linted\n"})
	f.change(func(s *contract.State) {
		s.Tasks["A"].Status, s.Attempts["A.1"].State = contract.TaskReview, contract.AttemptReported
		s.Attempts["A.1"].Report = &contract.Report{Outcome: contract.ReportDone}
	})
	play(t, f.p.Command(scenario.Orch, "accept", "A"), 0, "https://example.test/pull/1")
	if pr := f.state().Tasks["A"].PR; pr.State != contract.PRWatching || pr.Head != fix || testkit.Git(t, f.remote, "rev-parse", "whaleshark/r1-A") != fix || f.said("count") != "1" {
		t.Fatalf("the fix was not watched on the same pull request: %+v", pr)
	}
	f.watch()
	if s := f.state(); f.said("order") != "1" || s.Tasks["A"].Status != contract.TaskDone || !strings.HasSuffix(f.kinds(), "merged") {
		t.Fatalf("the fix was not merged: %s, events %s", s.Tasks["A"].Status, f.kinds())
	}
}

// The base moves under a green pull request, in a file of the task's: with
// refresh auto it is merged in, pushed and watched again before the merge;
// with ask the lead agent is told and nothing is merged.
func TestTheBaseMovesUnderAGreenPullRequest(t *testing.T) {
	for _, how := range []string{"auto", "ask"} {
		t.Run(how, func(t *testing.T) {
			f := openForge(t, "refresh = \""+how+"\"\n", map[string]string{"a.txt": "one\n\n\n\nend\n"})
			f.task("A", map[string]string{"a.txt": "two\n\n\n\nend\n"})
			play(t, f.p.Command(scenario.Orch, "accept", "A"), 0)
			other := t.TempDir()
			testkit.Git(t, other, "clone", "-q", f.remote, ".")
			moved := testkit.Commit(t, other, "elsewhere", map[string]string{"a.txt": "one\n\n\n\nthe end\n"})
			testkit.Git(t, other, "push", "-q", "origin", "main")
			f.checks(1, `[{"name":"ci","bucket":"pass"}]`)
			f.watch()
			pr := f.state().Tasks["A"].PR
			if f.said("order") != "" {
				t.Fatal("merged on a base that moved under it")
			}
			if how == "ask" {
				if pr.State != contract.PRAsked || !strings.HasSuffix(f.kinds(), "base_moved") {
					t.Fatalf("not asked: %+v, events %s", pr, f.kinds())
				}
				return
			}
			if pushed := testkit.Git(t, f.remote, "rev-parse", "whaleshark/r1-A"); pushed != pr.Head || f.git(f.root, "merge-base", "--is-ancestor", moved, pr.Head) != "" || pr.State != contract.PRWatching {
				t.Fatalf("not brought up to date: %+v", pr)
			}
			f.watch()
			if f.said("order") != "1" {
				t.Fatal("not merged once up to date")
			}
		})
	}
}

// The merge of a green pull request is done under the merge lock that
// `whaleshark lock` shows: while the person holds it nothing is merged and
// the task waits in its line; once it is let go the next look merges, and
// the lock is free again.
func TestTheMergeLockIsTheSameLock(t *testing.T) {
	f := openForge(t, "", map[string]string{"a.txt": "one\n"})
	f.task("A", map[string]string{"page-A.txt": "A\n"})
	play(t, f.p.Command(scenario.Orch, "accept", "A"), 0)
	play(t, f.p.Command(scenario.Human, "lock", "take", "merge"), 0, "You hold the lock merge")
	f.checks(1, `[{"name":"ci","bucket":"pass"}]`)
	f.watch()
	if f.said("order") != "" || f.state().Tasks["A"].Status == contract.TaskDone {
		t.Fatalf("merged under the person's lock: %s", f.said("order"))
	}
	play(t, f.p.Command(scenario.Orch, "lock", "list"), 0, "lock merge: the person since", "waiting: r1/A")
	play(t, f.p.Command(scenario.Human, "lock", "release", "merge"), 0)
	f.watch()
	if f.said("order") != "1" || f.state().Tasks["A"].Status != contract.TaskDone {
		t.Fatalf("after the lock was let go the merges are %q", f.said("order"))
	}
	if l := contract.ReadLocks(f.k.Platform.Peek, f.root); len(l) != 0 {
		t.Fatalf("the merge left the locks %+v", l)
	}
}

// A review holds the merge of the task it is of, not its pull request: green
// checks merge nothing until the review is done and says safe.
func TestAReviewHoldsTheMerge(t *testing.T) {
	f := openForge(t, "", map[string]string{"a.txt": "one\n"})
	f.task("A", map[string]string{"a.txt": "two\n"})
	f.change(func(s *contract.State) {
		s.Tasks["R"] = &contract.Task{ID: "R", Name: "review", Kind: contract.WorkReview, Reviews: "A", Check: contract.CheckNone, Status: contract.TaskRunning}
	})
	play(t, f.p.Command(scenario.Orch, "accept", "A"), 0, "https://example.test/pull/1")
	f.checks(1, `[{"name":"ci","bucket":"pass"}]`)
	f.watch()
	if f.said("order") != "" || f.state().Tasks["A"].PR.State != contract.PRWatching {
		t.Fatal("merged while its review was at work")
	}
	f.change(func(s *contract.State) {
		s.Tasks["R"].Status, s.Tasks["R"].SettledAt = contract.TaskDone, f.now
		s.Tasks["R"].Accepted = &contract.Accepted{How: contract.AcceptResult, Verdict: contract.VerdictSafe, At: f.now}
	})
	f.watch()
	if f.said("order") != "1" || f.state().Tasks["A"].Status != contract.TaskDone {
		t.Fatal("not merged once its review said safe")
	}
}
