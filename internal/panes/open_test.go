package panes

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/test/fakeherdr"
)

// desk is a fake herdr with one tab, and `ui` run from a pane of it.
type desk struct {
	t     *testing.T
	k     *contract.Kit
	herdr *fakeherdr.Fake
	me    string
	seen  int // the calls already looked at
}

func newDesk(t *testing.T) *desk {
	t.Parallel()
	home := t.TempDir()
	k := contract.NewKit()
	k.Platform = system(home, nil)
	f, err := fakeherdr.New(k)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Close)
	k.Herdr = f
	d := &desk{t: t, k: k, herdr: f}
	d.me = d.panes()[0].ID
	return d
}

func (d *desk) panes() []contract.Pane {
	d.t.Helper()
	snap, err := d.herdr.Snapshot(context.Background())
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

// did is what herdr was asked to change since the last time, one call a line.
func (d *desk) did() string {
	var lines []string
	calls := d.herdr.Calls()
	for _, c := range calls[d.seen:] {
		if line := strings.Join(c, " "); c[0] == "pane" && c[1] != "read" && c[1] != "layout" {
			lines = append(lines, line)
		}
	}
	d.seen = len(calls)
	return strings.Join(lines, "\n")
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
	mine, err := d.herdr.Split(d.me, "down", 0.5) // a pane the person opened
	if err != nil {
		t.Fatal(err)
	}
	d.did()
	o, err := d.ui(d.me)
	if err != nil || o.Fleet == "" || o.Actions == "" {
		t.Fatalf("ui: %+v, %v", o, err)
	}
	// The line typed into a pane is spelled for the shell of this system.
	self, _ := d.k.Platform.SelfPath()
	typed := func(kind string) string {
		return d.k.Platform.Quote("", []string{"exec", self, "ui", "run", kind, "--root", "/project"})
	}
	want := "pane split " + d.me + " --direction right --ratio 0.7 --no-focus\n" +
		"pane run " + o.Fleet + " " + typed("fleet") + "\n" +
		"pane split " + d.me + " --direction down --ratio 0.75 --no-focus\n" +
		"pane run " + o.Actions + " " + typed("actions")
	if got := d.did(); got != want {
		t.Errorf("ui asked herdr for\n%s\nwant\n%s", got, want)
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
	if off, err := d.ui(d.me, "fleet"); err != nil || off.Fleet != "" || off.Actions != o.Actions || d.did() != "pane close "+o.Fleet {
		t.Errorf("ui fleet: %+v, %v", off, err)
	}
	on, err := d.ui(d.me, "fleet", "on")
	if got := d.did(); err != nil || !strings.HasPrefix(got, "pane close "+o.Actions+"\npane split "+d.me+" --direction right") || strings.Count(got, "pane split") != 2 {
		t.Errorf("ui fleet on: %+v, %v\n%s", on, err, got)
	}
	if same, err := d.ui(d.me, "actions", "on"); err != nil || same != on || d.did() != "" {
		t.Errorf("ui actions on: %+v, %v", same, err)
	}
	// From one of the two, a pane can be closed and none opened.
	if _, err := d.ui(on.Fleet, "actions", "off"); err != nil || d.did() != "pane close "+on.Actions {
		t.Errorf("ui actions off from the fleet: %v", err)
	}
	if _, err := d.ui(on.Fleet, "actions"); err == nil || d.did() != "" {
		t.Errorf("ui actions from the fleet opened a pane: %v", err)
	}
	// Close removes ours and nothing else, and is safe to say twice.
	for range 2 {
		if o, err := d.ui(d.me, "close"); err != nil || o != (Opened{}) {
			t.Errorf("ui close: %+v, %v", o, err)
		}
	}
	if got := d.did(); got != "pane close "+on.Fleet {
		t.Errorf("ui close asked herdr for\n%s", got)
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
		t.Errorf("ui from a pane herdr does not know: %v", err)
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
	other, err := d.herdr.TabCreate("/project", "elsewhere", nil)
	if err != nil {
		t.Fatal(err)
	}
	d.did()
	for _, args := range [][]string{nil, {"close"}, {"fleet"}} {
		if _, err := d.ui(other.ID, args...); usage(err) != contract.ExitEnv || !strings.Contains(err.Error(), "another tab") || d.did() != "" {
			t.Errorf("ui %v from another tab: %v", args, err)
		}
	}

	// herdr started afresh gives the same pane ids to other terminals: a
	// pane that is not the terminal that was opened is not ours.
	d.herdr.Restart()
	d.did()
	if _, err := d.ui(d.me, "close"); err != nil || d.did() != "" {
		t.Errorf("ui close after a restart closed a pane: %v", err)
	}
	if len(d.panes()) != 4 || o.Fleet == "" {
		t.Errorf("panes after the restart: %+v", d.panes())
	}
}

func TestUIFitsTheFleetToTheScreen(t *testing.T) {
	d := newDesk(t)
	for _, c := range []struct {
		width, kept int
		split       string
	}{
		{150, 0, "--ratio 0.7 "},   // three tenths at first
		{100, 0, "--ratio 0.66 "},  // or the cells a card needs
		{150, 60, "--ratio 0.6 "},  // then the width it was left at
		{80, 60, "--ratio 0.667 "}, // a third at most on a narrow screen
		{80, 20, "--ratio 0.75 "},  // or less
		{300, 20, "--ratio 0.9 "},  // never under herdr's tenth
		{59, 0, ""},                // and no panes at all under sixty columns
	} {
		d.herdr.Width = c.width
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
