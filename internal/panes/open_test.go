package panes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/test/fakeengine"
)

// desk is the engine's double with one tab, and `ui` run from a pane of it.
type desk struct {
	t      *testing.T
	k      *contract.Kit
	double *fakeengine.Fake
	me     string
	seen   int // the calls already looked at
}

func newDesk(t *testing.T) *desk {
	t.Parallel()
	home := t.TempDir()
	k := contract.NewKit()
	k.Platform = system(home, nil)
	f := fakeengine.New()
	t.Cleanup(f.Close)
	k.Terms = f
	first, err := f.TabCreate("/project", "1", nil)
	if err != nil {
		t.Fatal(err)
	}
	return &desk{t: t, k: k, double: f, me: first.ID}
}

func (d *desk) panes() []contract.Pane {
	d.t.Helper()
	snap, err := d.double.Snapshot(context.Background())
	if err != nil {
		d.t.Fatal(err)
	}
	return snap.Panes
}

func (d *desk) ui(pane string, args ...string) (Opened, error) {
	c := &contract.Call{Kit: d.k, Command: contract.Find("ui"), Args: args, Caller: contract.Caller{Pane: pane}, Root: "/project", Out: io.Discard}
	got, err := ui(c)
	o, _ := got.(Opened)
	return o, err
}

// did is what the terminals were asked to change in a tab since the last
// time, one call a line: the call, its pane, and what it was given.
func (d *desk) did() string {
	var lines []string
	calls := d.double.Calls()
	for _, c := range calls[d.seen:] {
		if slices.Contains([]string{contract.OpSplit, contract.OpRun, contract.OpSwap, contract.OpResize, contract.OpPaneClose, contract.OpPaneFocus}, c.Op) {
			lines = append(lines, said(c))
		}
	}
	d.seen = len(calls)
	return strings.Join(lines, "\n")
}

// said is one call in a line: the call, its pane or tab, and what it was given.
func said(c contract.WireCall) string {
	line := []string{c.Op, c.Pane, c.Tab, c.Dir, c.Target}
	if c.Ratio != 0 {
		line = append(line, fmt.Sprint(c.Ratio))
	}
	line = append(append(line, c.Argv...), fakeengine.Typed(c))
	return strings.Join(strings.Fields(strings.Join(line, " ")), " ")
}

func (d *desk) focus() string {
	for _, p := range d.panes() {
		if p.Focused {
			return p.ID
		}
	}
	return ""
}

func TestUIOpensBothPanesOnceAndClosesOnlyItsOwn(t *testing.T) {
	d := newDesk(t)
	mine, err := d.double.Split(d.me, "down", 0.5) // a pane the person opened
	if err != nil {
		t.Fatal(err)
	}
	d.did()
	o, err := d.ui(d.me)
	if err != nil || o.Fleet == "" || o.Actions == "" {
		t.Fatalf("ui: %+v, %v", o, err)
	}
	// A pane's program is started from its list of arguments, with no
	// shell and no word in front: so the keeper knows it as ours.
	self, _ := d.k.Platform.SelfPath()
	want := "split " + d.me + " right 0.7\n" +
		"run " + o.Fleet + " " + self + " ui run fleet --root /project\n" +
		"split " + d.me + " down 0.75\n" +
		"run " + o.Actions + " " + self + " ui run actions --root /project"
	if got := d.did(); got != want {
		t.Errorf("ui asked the terminals for\n%s\nwant\n%s", got, want)
	}
	for _, q := range d.panes() {
		if q.Ours != (q.ID == o.Fleet || q.ID == o.Actions) {
			t.Errorf("pane %s is marked as ours: %v", q.ID, q.Ours)
		}
	}
	if d.focus() != d.me {
		t.Errorf("the keys went to %s", d.focus())
	}

	// A second time it finds its panes and does nothing.
	if again, err := d.ui(d.me); err != nil || again != o || d.did() != "" {
		t.Errorf("ui again: %+v, %v", again, err)
	}
	// One of them off and on: the fleet is split off first, so the action
	// pane makes way and comes back.
	if off, err := d.ui(d.me, "fleet"); err != nil || off.Fleet != "" || off.Actions != o.Actions || d.did() != "pane-close "+o.Fleet {
		t.Errorf("ui fleet: %+v, %v", off, err)
	}
	on, err := d.ui(d.me, "fleet", "on")
	if got := d.did(); err != nil || !strings.HasPrefix(got, "pane-close "+o.Actions+"\nsplit "+d.me+" right") || strings.Count(got, "split "+d.me) != 2 {
		t.Errorf("ui fleet on: %+v, %v\n%s", on, err, got)
	}
	if same, err := d.ui(d.me, "actions", "on"); err != nil || same != on || d.did() != "" {
		t.Errorf("ui actions on: %+v, %v", same, err)
	}
	// Close removes ours and nothing else, and is safe to say twice.
	for range 2 {
		if o, err := d.ui(d.me, "close"); err != nil || o != (Opened{}) {
			t.Errorf("ui close: %+v, %v", o, err)
		}
	}
	if got := d.did(); got != "pane-close "+on.Actions+"\npane-close "+on.Fleet {
		t.Errorf("ui close asked the terminals for\n%s", got)
	}
	if left := d.panes(); len(left) != 2 || d.focus() != d.me {
		t.Errorf("after ui close: %+v, and the keys in %s (the person's pane is %s)", left, d.focus(), mine.ID)
	}
}

func TestUIActsInItsOwnTabOnlyAndOnItsOwnPanesOnly(t *testing.T) {
	d := newDesk(t)
	usage := func(err error) int {
		var r *contract.Refusal
		if errors.As(err, &r) {
			return r.Exit
		}
		return -1
	}
	if _, err := d.ui(""); usage(err) != contract.ExitEnv {
		t.Errorf("ui outside a pane: %v", err)
	}
	if _, err := d.ui("w9:p9"); usage(err) != contract.ExitEnv {
		t.Errorf("ui from a pane the terminals do not know: %v", err)
	}
	for _, args := range [][]string{{"sideways"}, {"fleet", "maybe"}, {"close", "now"}, {"fleet", "on", "now"}, {"run", "menu"}} {
		if _, err := d.ui(d.me, args...); usage(err) != contract.ExitUsage {
			t.Errorf("ui %v: %v", args, err)
		}
	}
	o, err := d.ui(d.me)
	if err != nil {
		t.Fatal(err)
	}
	other, err := d.double.TabCreate("/project", "elsewhere", nil)
	if err != nil {
		t.Fatal(err)
	}
	d.did()
	for _, args := range [][]string{nil, {"close"}, {"fleet"}} {
		if _, err := d.ui(other.ID, args...); usage(err) != contract.ExitEnv || !strings.Contains(err.Error(), "another tab") || d.did() != "" {
			t.Errorf("ui %v from another tab: %v", args, err)
		}
	}

	// After a restart of the keeper our panes are back and still ours:
	// nothing is opened a second time.
	d.double.Restart()
	d.did()
	if again, err := d.ui(d.me); err != nil || again != o || d.did() != "" {
		t.Errorf("ui after a restart: %+v, %v", again, err)
	}
	// A pane that no longer runs our program is not ours, whatever the
	// file remembers of its id, and is never closed.
	if err := d.double.Run(o.Fleet, []string{"sh"}); err != nil {
		t.Fatal(err)
	}
	d.did()
	if _, err := d.ui(d.me, "close"); err != nil || d.did() != "pane-close "+o.Actions {
		t.Errorf("ui close closed a pane that is not ours: %v", err)
	}
	if len(d.panes()) != 3 {
		t.Errorf("panes at the end: %+v", d.panes())
	}
}

func TestUIFitsTheFleetToTheScreen(t *testing.T) {
	d := newDesk(t)
	for _, c := range []struct {
		width, kept int
		split       string
	}{
		{150, 0, " right 0.7\n"},   // three tenths at first
		{100, 0, " right 0.66\n"},  // or the cells a card needs
		{150, 60, " right 0.6\n"},  // then the width it was left at
		{80, 60, " right 0.667\n"}, // a third at most on a narrow screen
		{80, 20, " right 0.75\n"},  // or less
		{300, 20, " right 0.9\n"},  // never under a tenth
		{59, 0, ""},                // and no panes at all under sixty columns
	} {
		d.double.Width = c.width
		dirs, _ := d.k.Platform.Dirs()
		file := filepath.Join(dirs.State, "ui.json")
		if err := contract.WriteVersioned(d.k.Platform, file, contract.FileVersion, contract.UIFile{Versioned: contract.Versioned{Version: contract.FileVersion}, FleetWidth: c.kept}); err != nil {
			t.Fatal(err)
		}
		d.did()
		_, err := d.ui(d.me, "fleet", "on")
		got := d.did()
		if c.split == "" && (err == nil || got != "") || c.split != "" && (err != nil || !strings.Contains(got, c.split)) {
			t.Errorf("on %d columns with a fleet left at %d: %v\n%s", c.width, c.kept, err, got)
		}
		d.ui(d.me, "close")
	}
}
