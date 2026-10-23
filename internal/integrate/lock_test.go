package integrate_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// locked reads one lock of the project, or a free one with nobody in line.
func (r *run) locked(name string) contract.Lock {
	for _, l := range contract.ReadLocks(r.k.Platform.Peek, r.root) {
		if l.Name == name {
			return l
		}
	}
	return contract.Lock{Name: name}
}

// Two takers of a held lock are put in line, in the order they came, and
// neither is let through: the holder lets it go, and only the first in line
// may take it then. Every one of them sees the lock in list and in status.
func TestTwoTakersQueue(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n"})
	play(t, r.p.Command(scenario.Orch, "lock", "take", "deploy", "--task", "A", "--stale", "45m", "--note", "prod deploy"), 0, "You hold the lock deploy")
	play(t, r.p.Command(scenario.Orch, "lock", "take", "deploy", "--task", "A"), 0) // the holder again
	play(t, r.p.Command(scenario.Orch, "lock", "take", "deploy", "--task", "B"), 5, "r1/A holds it since", "number 1 in line")
	play(t, r.p.Command(scenario.Human, "lock", "take", "deploy"), 5, "number 2 in line")
	play(t, r.p.Command(scenario.Orch, "lock", "take", "deploy", "--task", "B"), 5, "number 1 in line")
	if l := r.locked("deploy"); l.Holder != "r1/A" || l.Stale != 45*60 || l.Note != "prod deploy" || len(l.Waiting) != 2 || l.Waiting[0].Holder != "r1/B" || l.Waiting[1].Holder != "the person" {
		t.Fatalf("the lock is %+v", l)
	}
	play(t, r.p.Command(scenario.Unbound, "lock", "list"), 0, "lock deploy: r1/A since", "(prod deploy); waiting: r1/B, the person")
	play(t, r.p.Command(scenario.Orch, "status"), 0, "lock deploy: r1/A since")
	play(t, r.p.Command(scenario.Human, "lock", "release", "merge"), 4, "Nobody holds the lock merge")
	play(t, r.p.Command(scenario.Orch, "lock", "release", "deploy", "--task", "A"), 0, "let go")
	play(t, r.p.Command(scenario.Human, "lock", "take", "deploy"), 5, "r1/B is first in line")
	play(t, r.p.Command(scenario.Orch, "lock", "take", "deploy", "--task", "B"), 0, "You hold the lock deploy")
	// The person leaves the line, and the lock's file goes with its holding.
	play(t, r.p.Command(scenario.Human, "lock", "release", "deploy"), 0)
	if l := r.locked("deploy"); l.Holder != "r1/B" || len(l.Waiting) != 0 {
		t.Fatalf("the lock is %+v", l)
	}
	play(t, r.p.Command(scenario.Orch, "lock", "release", "deploy", "--task", "B"), 0)
	if _, err := os.Stat(filepath.Join(contract.LocksDir(r.root), "deploy.json")); !os.IsNotExist(err) {
		t.Fatalf("a free lock with nobody in line is still a file: %v", err)
	}
	play(t, r.p.Command(scenario.Orch, "lock", "list"), 0, "No lock is held.")
	play(t, r.p.Command(scenario.Orch, "lock", "take", "no/such"), 2)
	play(t, r.p.Command(scenario.Orch, "lock", "take", "deploy", "--stale", "soon"), 2)
}

// A lock past its stale time is told the lead agent once, as lock_stale, and
// nobody takes it by itself; the lead agent may steal it only from then on,
// a person at any time, a worker or a stranger never. Held one stale time
// longer, it is the person's to-do, which goes once the holding has.
func TestAStaleLockIsToldAndStolenOnlyByTheAllowed(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n"})
	play(t, r.p.Command(scenario.Orch, "lock", "take", "deploy", "--task", "A", "--stale", "45m"), 0)
	play(t, r.p.Command(scenario.Orch, "lock", "steal", "deploy", "--task", "B"), 5, "not stale before")
	play(t, r.p.Command(scenario.Unbound, "lock", "steal", "deploy"), 5)
	sweep := func() {
		t.Helper()
		r.p.Clock(10 * time.Second)
		play(t, r.p.Command(scenario.Orch, "status", "--sweep", "--quiet"), 0)
	}
	sweep()
	if strings.Contains(r.kinds(), "lock_stale") {
		t.Fatalf("a fresh lock was told: %s", r.kinds())
	}
	r.p.Clock(46 * time.Minute)
	sweep()
	sweep()
	if n := strings.Count(r.kinds(), "lock_stale"); n != 1 || r.locked("deploy").Holder != "r1/A" || r.locked("deploy").ToldIn != "r1" {
		t.Fatalf("%d lock_stale, and the lock is %+v", n, r.locked("deploy"))
	}
	for _, e := range r.state().Inbox.Events {
		if e.Kind == "lock_stale" && (!strings.Contains(e.Text, "held by r1/A since") || !strings.Contains(e.Text, "past its 45m") || e.Data["lock"] != "deploy") {
			t.Fatalf("lock_stale says %q %v", e.Text, e.Data)
		}
	}
	play(t, r.p.Command(scenario.Orch, "status"), 0, "stale since")
	r.p.Clock(46 * time.Minute)
	sweep()
	item := r.locked("deploy").Item
	if q := r.state().Questions[item]; q == nil || q.Cause != contract.CauseLock || q.Form != contract.FormTodo || q.State != contract.QuestionOpen {
		t.Fatalf("the person was handed %q: %+v", item, q)
	}
	play(t, r.p.Command(scenario.Human, "pause"), 0)
	play(t, r.p.Command(scenario.Orch, "lock", "steal", "deploy", "--task", "B"), 5, "paused")
	play(t, r.p.Command(scenario.Human, "resume"), 0)
	play(t, r.p.Command(scenario.Orch, "lock", "steal", "deploy", "--task", "B"), 0, "taken from r1/A")
	if l := r.locked("deploy"); l.Holder != "r1/B" || !l.Told.IsZero() || l.Item != "" {
		t.Fatalf("after the steal the lock is %+v", l)
	}
	sweep()
	if q := r.state().Questions[item]; q.State != contract.QuestionClosed {
		t.Fatalf("the person's to-do is %s after the steal", q.State)
	}
	// The person steals a lock that is not stale, and lets it go.
	play(t, r.p.Command(scenario.Human, "lock", "steal", "deploy"), 0, "taken from r1/B")
	play(t, r.p.Command(scenario.Human, "lock", "release", "deploy"), 0)
}
