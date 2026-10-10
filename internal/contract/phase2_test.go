package contract

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

// desk is the platform with its three real parts a slot needs: the login's
// folders, a lock and a replace.
type desk struct {
	NoPlatform
	dir string
	mu  *sync.Mutex
}

func (d desk) Dirs() (Dirs, error) { return Dirs{State: d.dir}, nil }
func (d desk) Lock(string, bool) (func(), error) {
	d.mu.Lock()
	return d.mu.Unlock, nil
}
func (desk) Replace(tmp, final string) error { return os.Rename(tmp, final) }

// A task keeps its slot, two tasks never share a port whatever their sizes,
// a slot given back is the next one taken, and a full block says so.
func TestSlots(t *testing.T) {
	p, block := desk{dir: t.TempDir(), mu: new(sync.Mutex)}, Ports{Base: 20000, Count: 45}
	take := func(run, task string, size int) Slot {
		t.Helper()
		s, err := TakeSlot(p, block, "/project", run, task, size)
		if err != nil {
			t.Fatalf("%s %s: %v", run, task, err)
		}
		return s
	}
	a, b := take("r1", "A", 10), take("r1", "B", 10)
	if a.First(block) != 20000 || b.First(block) != 20010 || take("r1", "A", 10) != a {
		t.Fatalf("A has %d and B has %d", a.First(block), b.First(block))
	}
	if got := a.Env(block); !slices.Equal(got, []string{"PORT_BASE=20000", "PORT=20000"}) {
		t.Fatalf("A's tab gets %v", got)
	}
	// A project with a wider block gets the lowest group of its own size
	// that touches nobody's ports.
	if c, f := take("r2", "C", 15), take("r2", "F", 10); c.First(block) != 20030 || f.First(block) != 20020 {
		t.Fatalf("C of fifteen ports starts at %d and F after it at %d", c.First(block), f.First(block))
	}
	if none, err := TakeSlot(p, block, "/project", "r1", "D", 0); err != nil || none != (Slot{}) {
		t.Fatalf("a project that wants no ports got %+v, %v", none, err)
	}
	if _, err := TakeSlot(p, block, "/project", "r1", "E", 10); !errors.Is(err, ErrNoSlot) {
		t.Fatalf("a full block answered %v", err)
	}
	freed, err := FreeSlots(p, func(s Slot) bool { return s.Run == "r1" && s.Task == "A" })
	if err != nil || len(freed) != 1 || freed[0] != a {
		t.Fatalf("giving A's slot back freed %+v, %v", freed, err)
	}
	if e := take("r1", "E", 10); e.N != 0 {
		t.Fatalf("the slot given back was not the next one taken: %d", e.N)
	}
	taken, err := ReadSlots(os.ReadFile, p.dir)
	if err != nil || len(taken) != 4 {
		t.Fatalf("slots.json holds %+v, %v", taken, err)
	}
	// Twenty starts at once end with twenty different slots.
	wide, wg, seen := Ports{Base: 30000, Count: 1000}, sync.WaitGroup{}, sync.Map{}
	for i := range 20 {
		wg.Go(func() {
			s, err := TakeSlot(p, wide, "/other", "r1", string(rune('a'+i)), 10)
			if _, twice := seen.LoadOrStore(s.N, true); err != nil || twice {
				t.Errorf("slot %d: %v, given twice: %v", s.N, err, twice)
			}
		})
	}
	wg.Wait()
	// A newer program's file is never written over.
	os.WriteFile(filepath.Join(p.dir, slotsFile), []byte(`{"version": 99}`), 0o600)
	if _, err := TakeSlot(p, block, "/project", "r9", "Z", 10); !errors.Is(err, ErrNewer) {
		t.Fatalf("a newer slots.json was answered with %v", err)
	}
}

// What needs approval is each key that can run something or reach outside,
// and nothing else; a newer trust.json approves nothing.
func TestTrustLinesAndANewerRecord(t *testing.T) {
	root := t.TempDir()
	text := "[worktrees]\nshare = [\"node_modules\"]\nport_block = 20\n[agent]\nargs = [\"--x\"]\n[land]\nwho = \"orchestrator\"\n[notify]\nfull_text = true\n"
	os.WriteFile(filepath.Join(root, "whaleshark.toml"), []byte(text), 0o600)
	lines, err := TrustLines(root)
	want := []string{"worktrees.share = [node_modules]", "agent.args = [--x]", "land.who = orchestrator"}
	if err != nil || !slices.Equal(lines, want) {
		t.Fatalf("the lines to approve are %q, %v", lines, err)
	}
	if err := WriteTrust(plain{}, root, lines, Now()); err != nil {
		t.Fatal(err)
	}
	if p, err := ReadProjectFile(root); err != nil || len(p.Held) != 0 || p.Land.Who != ForOrchestrator || p.Worktrees.PortBlock != 20 {
		t.Fatalf("an approved project reads as %+v, %v", p, err)
	}
	os.WriteFile(filepath.Join(root, ProjectDir, trustFile), []byte(`{"version": 99, "hash": "`+trustHash(lines)+`"}`), 0o600)
	p, err := ReadProjectFile(root)
	if err != nil || len(p.Held) != 3 || p.Land.Who != ForHuman || p.Agent.Args != nil || p.Worktrees.PortBlock != 20 || !p.Notify.FullText {
		t.Fatalf("with a newer trust record the project reads as %+v, %v", p, err)
	}
}

// Every stand-in of phase 2 answers so that phase 1 goes on as it did.
func TestThePhaseTwoStandIns(t *testing.T) {
	k := NewKit()
	if clean, err := k.Placement.Clean("x"); !clean || err != nil {
		t.Error("with no worktrees a folder must count as committed")
	}
	if base, in, err := k.Integrator.Begin("x", &State{}); base != nil || in != nil || err != nil {
		t.Error("with no git a new run has no integration branch")
	}
	if _, err := k.Integrator.PrepareAll("x", &State{}, nil); !errors.Is(err, ErrNotBuilt) {
		t.Error("several tasks in one accept must say that it is not built")
	}
	brief := SyncBrief{Task: "T7", Base: "whaleshark/r3", Behind: 2, Clashes: []Finding{{Kind: "overlap", Files: []string{"a.go"}, How: "both changed"}}}
	if got := k.SyncText(brief); got != "Bring T7 up to date with whaleshark/r3 (2 commits behind).\noverlap: both changed [a.go]\n" {
		t.Errorf("until the guides word it, a sync brief reads %q", got)
	}
}
