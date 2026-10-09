package contract

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// A frame comes back as it was written, a malformed one is refused before
// its body is read, and an error keeps its identity across the socket.
func TestFrameAndErrors(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, FrameBytes, []byte("abc")); err != nil {
		t.Fatal(err)
	}
	if kind, body, err := ReadFrame(&buf); err != nil || kind != FrameBytes || string(body) != "abc" {
		t.Errorf("read back %q %q %v", kind, body, err)
	}
	for _, bad := range [][]byte{{'x', 0, 0, 0, 0}, {FrameCall, 0xff, 0, 0, 0}} {
		if _, _, err := ReadFrame(bytes.NewReader(bad)); !errors.Is(err, ErrFrame) {
			t.Errorf("frame % x: %v, want ErrFrame", bad, err)
		}
	}
	if err := WriteFrame(&buf, FrameCall, make([]byte, FrameMax+1)); !errors.Is(err, ErrFrame) {
		t.Errorf("a body over the limit was written: %v", err)
	}
	got := ErrOf(WireReply{Err: ErrCode(fmt.Errorf("pane p7: %w", ErrNoPane)), Message: "pane p7 is gone"})
	if !errors.Is(got, ErrNoPane) || !strings.HasSuffix(got.Error(), "pane p7 is gone") {
		t.Errorf("the error came back as %v", got)
	}
	if ErrCode(nil) != "" || ErrOf(WireReply{}) != nil || ErrCode(errors.New("x")) != "failed" {
		t.Error("no error, or a plain one, was not carried as such")
	}
}

// The keeper's pane variables come before herdr's, and a picture gives a
// space for a cell nothing was written to.
func TestPaneOfSettingsAndPicture(t *testing.T) {
	env := map[string]string{EnvPane: "w1:p1", EnvActivePane: "w1:p2"}
	get := func(k string) string { return env[k] }
	if pane, active := PaneOf(get); pane != "w1:p1" || active != "w1:p2" {
		t.Errorf("herdr's variables gave %q %q", pane, active)
	}
	env[EnvTermPane] = "p7"
	if pane, active := PaneOf(get); pane != "p7" || active != "" {
		t.Errorf("the keeper's variables gave %q %q", pane, active)
	}
	missing := func(string) ([]byte, error) { return nil, os.ErrNotExist }
	if s, err := ReadSettings(missing, Dirs{}); err != nil || s.Keys.Command != "ctrl+space" || s.Scrollback.Keep != 2000 {
		t.Errorf("settings with no file: %+v %v", s, err)
	}
	file := func(string) ([]byte, error) {
		return []byte("[clipboard]\nprograms = \"on\"\n[[shortcut]]\nkey = \"s\"\ntype = \"shell\"\nargv = [\"pause\"]\n"), nil
	}
	if s, err := ReadSettings(file, Dirs{}); err != nil || s.Clipboard.Programs != "on" || s.Keys.Style != "auto" || len(s.Shortcuts) != 1 || s.Shortcuts[0].Argv[0] != "pause" {
		t.Errorf("settings from a file: %+v %v", s, err)
	}
	p := NewPicture(6, 3)
	p.Cells[0].Text, p.Cells[3].Text, p.Cells[6].Text = "D", "y", "x"
	if got := p.Text(); got != "D  y\nx" {
		t.Errorf("the picture reads %q", got)
	}
}

// An event of each kind brings a picture to where the terminals are.
func TestApply(t *testing.T) {
	s := &Snapshot{Panes: []Pane{{ID: "p1", Tab: "t1", Focused: true}, {ID: "p2", Tab: "t2"}}}
	Apply(s, TermEvent{Seq: 1, Kind: EvOpened, Pane: Pane{ID: "p0", Tab: "t2"}})
	Apply(s, TermEvent{Seq: 2, Kind: EvFocus, Pane: Pane{ID: "p2", Tab: "t2", Focused: true}})
	Apply(s, TermEvent{Seq: 3, Kind: EvRenamed, Pane: Pane{Tab: "t2", Label: "new name"}})
	Apply(s, TermEvent{Seq: 4, Kind: EvClosed, Pane: Pane{ID: "p0"}})
	want := []Pane{{ID: "p1", Tab: "t1"}, {ID: "p2", Tab: "t2", Label: "new name", Focused: true}}
	if !slices.Equal(s.Panes, want) || s.Seq != 4 {
		t.Errorf("after four events: %+v, seq %d", s.Panes, s.Seq)
	}
	if Apply(s, TermEvent{Seq: 5, Kind: EvTabClosed, Pane: Pane{Tab: "t2"}}); len(s.Panes) != 1 || s.Panes[0].ID != "p1" {
		t.Errorf("after the tab closed: %+v", s.Panes)
	}
}
