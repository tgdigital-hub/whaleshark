package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// The project's on_event is run only once approved, is given each event
// once, as JSON, in the order raised, and a command that hangs is cut off
// at its time and passed: the record is never held for it.
func TestMirror(t *testing.T) {
	l := newLogin(t)
	if l.k.Platform.System() == "windows" {
		t.Skip("the command is a line for a POSIX shell")
	}
	got := filepath.Join(t.TempDir(), "events")
	s := &contract.State{Version: contract.StateVersion, Tasks: map[string]*contract.Task{}, Attempts: map[string]*contract.Attempt{},
		Questions: map[string]*contract.Question{}, Run: contract.Run{ID: "r1", CreatedAt: time.Now(), Limit: 20}}
	if err := l.k.Store.Create(l.root, s); err != nil {
		t.Fatal(err)
	}
	raise := func(kind, task string) {
		err := l.k.Store.Change(l.root, "r1", func(s *contract.State) error {
			s.Counters.Seq++
			s.Inbox.Events = append(s.Inbox.Events, contract.Event{Seq: s.Counters.Seq, At: time.Now(), Kind: kind, Task: task, Text: kind + " " + task})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	mirrored := func() (lines []string) {
		data, _ := os.ReadFile(got)
		for line := range strings.Lines(string(data)) {
			var e struct {
				Run  string `json:"run"`
				Seq  int    `json:"seq"`
				Kind string `json:"kind"`
				Task string `json:"task"`
			}
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				t.Fatalf("not JSON: %q", line)
			}
			lines = append(lines, strings.Join([]string{e.Run, e.Kind, e.Task}, " "))
		}
		return lines
	}
	// It hangs at the stale event, after writing it down.
	script := filepath.Join(t.TempDir(), "mirror.sh")
	err := os.WriteFile(script, []byte("cat >> \"$1\"; echo >> \"$1\"\ntail -n 1 \"$1\" | grep -q '\"kind\":\"stale\"' && sleep 30\nexit 0\n"), 0o600)
	if err == nil {
		err = os.WriteFile(filepath.Join(l.root, "whaleshark.toml"), []byte("[hooks]\non_event = 'sh \""+script+"\" \""+got+"\"'\n"), 0o600)
	}
	if err != nil {
		t.Fatal(err)
	}
	raise("done", "T1")
	raise("question", "T2")
	Mirror(l.k, l.root, "r1")
	if lines := mirrored(); len(lines) != 0 {
		t.Fatalf("ran before it was approved: %q", lines)
	}
	lines, _ := contract.TrustLines(l.root)
	if err := contract.WriteTrust(l.k.Platform, l.root, lines, time.Now()); err != nil {
		t.Fatal(err)
	}
	Mirror(l.k, l.root, "r1")
	Mirror(l.k, l.root, "r1")
	if lines := mirrored(); strings.Join(lines, ", ") != "r1 done T1, r1 question T2" {
		t.Fatalf("mirrored %q", lines)
	}
	eventWait = time.Second
	t.Cleanup(func() { eventWait = 10 * time.Second })
	raise("stale", "T3")
	raise("merged", "T4")
	began := time.Now()
	Mirror(l.k, l.root, "r1")
	if took := time.Since(began); took > 5*time.Second {
		t.Fatalf("a command that hangs held the call for %s", took)
	}
	Mirror(l.k, l.root, "r1")
	if lines := mirrored(); strings.Join(lines, ", ") != "r1 done T1, r1 question T2, r1 stale T3, r1 merged T4" {
		t.Fatalf("mirrored %q", lines)
	}
}
