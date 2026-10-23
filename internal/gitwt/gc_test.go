package gitwt

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/rules"
	"github.com/tgdigital-hub/whaleshark/internal/store"
)

// gc takes the copies of the code of tasks that ended long enough ago, and
// their branches once landed: a dry run removes nothing, a task still at
// work, one that ended lately and a copy with work not committed are kept,
// and so is the branch of cancelled work that never landed.
func TestGC(t *testing.T) {
	d := project(t, map[string]string{"a.txt": "one\n"})
	k := d.tr.k
	store.Plug(k)
	rules.Plug(k)
	long, lately := time.Now().Add(-10*24*time.Hour), time.Now().Add(-time.Hour)
	d.s.Version, d.s.Attempts, d.s.Questions = contract.StateVersion, map[string]*contract.Attempt{}, map[string]*contract.Question{}
	d.s.Run.Base, d.s.Run.CreatedAt, d.s.Run.Limit = &contract.GitRef{Ref: "main"}, long, 20
	for id, status := range map[string]contract.TaskStatus{"A": contract.TaskDone, "B": contract.TaskCancelled, "C": contract.TaskDone,
		"D": contract.TaskDone, "E": contract.TaskRunning} {
		d.s.Tasks[id] = &contract.Task{ID: id, Name: "page " + id, Status: status, Placement: contract.PlaceWorktree, SettledAt: long}
		dir, w := d.place(id, "page "+id)
		testkit.Commit(t, dir, "the work of "+id, map[string]string{id + ".txt": id + "\n"})
		if id == "A" {
			d.git("merge", "-q", "--no-edit", w.Branch)
		}
	}
	d.s.Tasks["C"].SettledAt = lately
	d.s.Tasks["E"].SettledAt, d.s.Tasks["E"].Attempts = time.Time{}, []string{"E.1"}
	d.s.Attempts["E.1"] = &contract.Attempt{ID: "E.1", Task: "E", N: 1, State: contract.AttemptWorking}
	d.write(".whaleshark/worktrees/r1-D/note.txt", "not committed\n")
	if err := k.Store.Create(d.root, d.s); err != nil {
		t.Fatal(err)
	}
	run := func(flags map[string][]string) (string, error) {
		var out bytes.Buffer
		_, err := k.Handler("gc")(&contract.Call{Kit: k, Flags: flags, Caller: contract.Caller{Kind: contract.Human}, Root: d.root, Now: time.Now(), Out: &out})
		return out.String(), err
	}
	gc := func(flags map[string][]string) string {
		t.Helper()
		said, err := run(flags)
		if err != nil {
			t.Fatalf("gc %v: %v", flags, err)
		}
		return said
	}
	tree := func(id string) string { return filepath.Join(d.root, ".whaleshark", "worktrees", "r1-"+id) }
	said := gc(map[string][]string{"dry-run": {""}})
	for _, want := range []string{"would remove: the copy of A of run r1", "would delete: the branch whaleshark/r1-A-page-a of A",
		"would remove: the copy of B of run r1", "kept: the branch whaleshark/r1-B-page-b of B of run r1, whose work is not proven landed",
		"kept: the copy of D of run r1, which holds work not committed"} {
		if !strings.Contains(said, want) {
			t.Fatalf("the dry run did not say %q:\n%s", want, said)
		}
	}
	if strings.Contains(said, " C of") || strings.Contains(said, " E of") || !exists(tree("A")) || !d.has("whaleshark/r1-A-page-a") {
		t.Fatalf("the dry run went too far:\n%s", said)
	}
	said = gc(nil)
	s, _ := k.Store.Read(d.root, "r1")
	for id, gone := range map[string]bool{"A": true, "B": true, "C": false, "D": false, "E": false} {
		if exists(tree(id)) == gone || s.Tasks[id].Worktree.RemovedAt.IsZero() != !gone {
			t.Fatalf("%s: copy there %v, record %+v\n%s", id, exists(tree(id)), s.Tasks[id].Worktree, said)
		}
	}
	if d.has("whaleshark/r1-A-page-a") || !d.has("whaleshark/r1-B-page-b") {
		t.Fatalf("branches after gc:\n%s", d.git("branch"))
	}
	if said = gc(map[string][]string{"older-than": {"30d"}}); !strings.Contains(said, "Nothing to remove") {
		t.Fatalf("gc --older-than 30d said:\n%s", said)
	}
	if _, err := run(map[string][]string{"older-than": {"soon"}}); code(err) != "usage" {
		t.Fatalf("--older-than soon: %v", err)
	}
	if _, err := os.Stat(tree("D")); err != nil {
		t.Fatal(err)
	}
}
