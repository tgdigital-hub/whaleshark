package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The context file has one name for the hook that writes it and for every
// reader, and the name is one every system's file names can take.
func TestCtxPathAndReadCtx(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Base(CtxPath(dir, "w1:p2"))
	if name != "w1+p2.json" || filepath.Dir(CtxPath(dir, "w1:p2")) != filepath.Join(dir, "ctx") {
		t.Fatalf("the context file of w1:p2 is %s", CtxPath(dir, "w1:p2"))
	}
	at := time.Date(2026, 10, 8, 21, 13, 0, 0, time.UTC)
	for _, pane := range []string{"w1:p2", "w1:p3", "w1:p4"} {
		if err := WriteVersioned(plain{}, CtxPath(dir, pane), FileVersion, CtxFile{Versioned{FileVersion}, 66, true, at}); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(CtxPath(dir, "w1:p4"), []byte(`{"version": 99, "pct": 5, "known": true, "at": "2026-10-08T21:13:00Z"}`), 0o600)
	s := &State{Attempts: map[string]*Attempt{
		"T1.1": {ID: "T1.1", State: AttemptWorking, Place: Place{Pane: "w1:p2"}},
		"T2.1": {ID: "T2.1", State: AttemptAccepted, Place: Place{Pane: "w1:p3"}},
		"T3.1": {ID: "T3.1", State: AttemptWorking, Place: Place{Pane: "w1:p4"}},
		"T4.1": {ID: "T4.1", State: AttemptWorking, Place: Place{Pane: "w1:p5"}},
		"T5.1": {ID: "T5.1", State: AttemptWorking},
	}}
	got := ReadCtx(os.ReadFile, dir, s)
	if len(got) != 1 || got["w1:p2"].Pct != 66 || !got["w1:p2"].Known {
		t.Errorf("of a live pane with a figure, a settled one, a newer file, a pane with no file and no pane at all, the figures read are %+v", got)
	}
}

func TestTokenHash(t *testing.T) {
	hash := TokenHash("not-the-token")
	if !strings.HasPrefix(hash, "sha256:") || len(hash) != len("sha256:")+64 || hash != strings.ToLower(hash) {
		t.Errorf("the hash is written %q", hash)
	}
	if hash != TokenHash("not-the-token\n") || hash == TokenHash("not-the-token2") || strings.Contains(hash, "not-the-token") {
		t.Error("the hash must be the same for the token as a file holds it, another for another token, and never hold the token")
	}
}

func TestNextJoin(t *testing.T) {
	for before, want := range map[string]string{
		"whaleshark show T7": "then", "whaleshark close": "then", "whaleshark task list": "then",
		"whaleshark answer q7 yes --human": "or", "whaleshark accept T8": "or", "": "or",
	} {
		if got := NextJoin(before); got != want {
			t.Errorf("after %q comes %q, not %q", before, got, want)
		}
	}
}

// Until the view package is plugged in, the kit's builder says so.
func TestTheKitsViewBeforeItIsBuilt(t *testing.T) {
	at := time.Date(2026, 10, 8, 21, 14, 0, 0, time.UTC)
	v := NewKit().View(ViewInput{Now: at, Caller: Human, Checked: at})
	if v.Version != ViewVersion || len(v.Alerts) != 1 || !strings.Contains(v.Alerts[0], ErrNotBuilt.Error()) || !v.Fresh.Checked.Equal(at) {
		t.Errorf("the stand-in view is %+v", v)
	}
}
