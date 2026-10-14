package panes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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
	first, err := f.TabCreate("", "", "/project", "1", nil)
	if err != nil {
		t.Fatal(err)
	}
	return &desk{t: t, k: k, double: f, me: first.ID}
}

// panes are the panes of the tabs: the dock, which is in every picture and
// in no tab, is nothing ui opens, closes or counts.
func (d *desk) panes() []contract.Pane {
	d.t.Helper()
	snap, err := d.double.Snapshot(context.Background())
	if err != nil || len(snap.Panes) == 0 || !snap.Panes[0].Dock {
		d.t.Fatalf("the picture: %+v, %v", snap, err)
	}
	return snap.Panes[1:]
}

func (d *desk) ui(pane string, args ...string) (Opened, error) {
	c := &contract.Call{Kit: d.k, Command: contract.Find("ui"), Args: args, Caller: contract.Caller{Pane: pane}, Root: "/project", Out: io.Discard}
	got, err := ui(c)
	o, _ := got.(Opened)
	return o, err
}

// only is the one pair of panes a file holds: a desk has one workspace.
func only(ui contract.UIFile) (pair contract.Pair) {
	for _, pair = range ui.Panes {
	}
	return pair
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
	want := "split " + d.me + " right 36\n" +
		"run " + o.Fleet + " " + self + " ui run fleet --root /project\n" +
		"split " + d.me + " down 2\n" +
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
	other, err := d.double.TabCreate("", "", "/project", "elsewhere", nil)
	if err != nil {
		t.Fatal(err)
	}
	d.did()
	// From another tab, as a lead agent's after a takeover, the panes come
	// to it: they are closed where they were, which no agent could do there,
	// and opened beside the pane that asked.
	moved, err := d.ui(other.ID)
	if got := d.did(); err != nil || moved.Fleet == "" || moved.Fleet == o.Fleet || moved.Actions == o.Actions ||
		!strings.HasPrefix(got, "pane-close "+o.Fleet+"\npane-close "+o.Actions+"\nsplit "+other.ID+" right") {
		t.Errorf("ui from another tab: %+v, %v\n%s", moved, err, got)
	}
	// Closing from the first tab closes them where they are now.
	if none, err := d.ui(d.me, "close"); err != nil || none != (Opened{}) || d.did() != "pane-close "+moved.Fleet+"\npane-close "+moved.Actions {
		t.Errorf("ui close from another tab than the panes': %+v, %v", none, err)
	}
	if o, err = d.ui(d.me); err != nil {
		t.Fatal(err)
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

// The fleet opens at the width it comes with, so that its line is put back
// to that, and is then brought to the width it was left at.
func TestUIFitsTheFleetToTheScreen(t *testing.T) {
	d := newDesk(t)
	for _, c := range []struct {
		width, kept int
		split       string
		then        bool
	}{
		{150, 0, " right 46\n", false}, // in a wide window the width it comes with
		{100, 0, " right 36\n", false}, // in a narrower one the cells a card needs
		{150, 60, " right 46\n", true}, // then brought to the width it was left at
		{300, 20, " right 46\n", true}, // never under what a card needs
		{80, 60, " down 2\n", false},   // its strip where the conversation would have too little
		{10, 0, "", false},             // and no fleet at all under forty columns
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
		if strings.Contains(got, "\nresize ") != c.then {
			t.Errorf("on %d columns with a fleet left at %d the fleet was then brought to its width by\n%s", c.width, c.kept, got)
		}
		d.ui(d.me, "close")
	}
}

// The window's width changes: the left section, which is always there, fits
// the two panes to it again once the width rests, in the tab they stand in
// and in the form Fit gives that width. With no pane of ours open nothing is
// opened.
func TestThePanesAreFittedAgainWhenTheWindowsWidthChanges(t *testing.T) {
	wd := start(t, side, 40, 30, nil)
	splits := func(from int) (lines []string) {
		for _, c := range wd.double.Calls()[from:] {
			if c.Op == contract.OpSplit {
				lines = append(lines, said(c))
			}
		}
		return lines
	}
	wd.double.Window(150)
	rested := make(chan bool)
	wd.p.tell(news{do: func() { wd.p.tick(1); wd.p.tick(1); close(rested) }})
	if <-rested; len(splits(0)) != 0 {
		t.Fatalf("with no pane of ours open the new width opened %q", splits(0))
	}
	if err := Panes(wd.p.k, ""); err != nil {
		t.Fatal(err)
	}
	// The tabs our two panes stand in, once the file names the two that are open.
	tab := func() (tabs []string) {
		snap, _ := wd.double.Snapshot(context.Background())
		_, ui, _, _ := screen(wd.p.k)
		for _, p := range snap.Panes {
			if p.Ours && !p.Dock && (p.ID == only(ui).Fleet || p.ID == only(ui).Actions) {
				tabs = append(tabs, p.Tab)
			}
		}
		return tabs
	}
	home := tab()
	if len(home) != 2 || home[0] != home[1] {
		t.Fatalf("the two panes opened in the tabs %q", home)
	}
	lay := contract.Layout{Fleet: contract.Right, Actions: contract.PersonDefaults().Panes.Actions, Side: contract.SideWidth}
	for _, width := range []int{70, 200, 110} {
		form, seen := contract.Fit(width+contract.SideWidth+1, lay), len(wd.double.Calls())
		want := fmt.Sprint(" right ", form.Fleet)
		if form.FleetForm == contract.FormStrip {
			want = fmt.Sprint(" down ", stripRows)
		}
		wd.double.Window(width)
		if !wd.until(10*time.Second, func() bool {
			return slices.ContainsFunc(splits(seen), func(line string) bool { return strings.Contains(line, want) })
		}) {
			t.Fatalf("at %d columns the fleet was not opened again with %q: %q", width, want, splits(seen))
		}
		if !wd.until(10*time.Second, func() bool { return slices.Equal(tab(), home) }) {
			t.Fatalf("at %d columns the panes stand in the tabs %q, not %q", width, tab(), home)
		}
	}
}

// Each workspace has its own two panes: opening them in a second workspace
// closes nothing in the first, the file names a pair for each by the
// workspace's folder, an older file's one pair becomes the pair of the
// workspace it is in, and closing closes the pair of that workspace alone.
func TestEachWorkspaceHasItsOwnTwoPanes(t *testing.T) {
	d := newDesk(t)
	d.double.Width = 200
	first, err := d.ui(d.me)
	if err != nil || first.Fleet == "" || first.Actions == "" {
		t.Fatalf("the first workspace's panes: %+v, %v", first, err)
	}
	// The file as a program of before phase 7 wrote it.
	dirs, _ := d.k.Platform.Dirs()
	file := filepath.Join(dirs.State, "ui.json")
	old := contract.UIFile{Versioned: contract.Versioned{Version: contract.FileVersion}, FleetPane: first.Fleet, ActionsPane: first.Actions}
	if err := contract.WriteVersioned(d.k.Platform, file, contract.FileVersion, old); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	there, err := d.double.SpaceOpen(root, root)
	if err != nil {
		t.Fatal(err)
	}
	d.did()
	second, err := d.ui(there.ID)
	if got := d.did(); err != nil || strings.Contains(got, "pane-close") || second.Fleet == "" || second.Fleet == first.Fleet || second.Actions == first.Actions {
		t.Fatalf("the second workspace's panes: %+v, %v\n%s", second, err, got)
	}
	_, ui, _, _ := screen(d.k)
	if ui.FleetPane != "" || ui.ActionsPane != "" || len(ui.Panes) != 2 || ui.Panes[root] != contract.Pair(second) || ui.Panes[""] != contract.Pair(first) {
		t.Errorf("the file names %+v", ui)
	}
	snap, _ := d.double.Snapshot(context.Background())
	if pair := pairOf(ui, snap, rootOf(snap, second.Fleet)); pair != contract.Pair(second) || rootOf(snap, first.Actions) != "" {
		t.Errorf("the pair of the pane's own workspace is %+v", pair)
	}
	// Closing in the second workspace leaves the first's pair where it is.
	if _, err := d.ui(there.ID, "close"); err != nil {
		t.Fatal(err)
	}
	if got := d.did(); !strings.Contains(got, "pane-close "+second.Fleet) || strings.Contains(got, first.Fleet) || strings.Contains(got, first.Actions+"\n") {
		t.Errorf("closing the second workspace's panes did\n%s", got)
	}
	if _, ui, _, _ := screen(d.k); len(ui.Panes) != 1 || ui.Panes[""] != contract.Pair(first) {
		t.Errorf("after the close the file names %+v", ui.Panes)
	}
}

// measured is the terminals with the conversation's size as a test says it.
type measured struct {
	contract.Terminals
	mu   *sync.Mutex
	w, h *int
	dock *int // what the left section has more than its width as it comes
}

func (m measured) Size(pane string) (int, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if pane == "dock" {
		return contract.SideWidth + *m.dock, 40, nil
	}
	return *m.w, *m.h, nil
}

// A pane beside the conversation whose edge a person dragged has its size
// written once it has rested: the fleet its width, the action pane its
// height. A size that changed with the window, where the two together
// measure another figure, is nobody's choice and is not written.
func TestADraggedEdgeIsStoredOnceItHasRested(t *testing.T) {
	for _, c := range []struct {
		kind, key string
		w, h      int
		to        [2]int
	}{{fleet, "fleet.width", 46, 30, [2]int{52, 30}}, {actions, "actions.height", 100, 12, [2]int{100, 15}}} {
		var mu sync.Mutex
		tw, th, dock := 150-c.w, 43-c.h, 0
		wd := begin(t, c.kind, c.w, c.h, nil, func(wd *world) {
			ours(wd)
			wd.p.k.Terms = measured{wd.double, &mu, &tw, &th, &dock}
		})
		move := func(w, h, talkW, talkH int) {
			mu.Lock()
			tw, th = talkW, talkH
			mu.Unlock()
			wd.resize(w, h)
		}
		wd.s.Still(2 * twice) // the two are measured once the picture is there
		// The window grew: this pane and the conversation both did.
		move(c.w+2, c.h+2, tw+9, th+9)
		wd.s.Still(3 * twice)
		if got := wd.children("set " + c.key); len(got) != 0 {
			t.Errorf("%s: a window that changed wrote %v", c.kind, got)
		}
		// The left section's edge was dragged: the conversation alone gave
		// it three cells, and this pane was told nothing.
		mu.Lock()
		tw, dock = tw-3, 3
		mu.Unlock()
		// A drag of the edge: what this pane gains the conversation loses.
		move(c.to[0], c.to[1], tw-(c.to[0]-c.w-2), th-(c.to[1]-c.h-2))
		want := fmt.Sprintf("set %s %d --human", c.key, max(c.to[0]*map[bool]int{true: 1}[c.kind == fleet], c.to[1]*map[bool]int{true: 1}[c.kind == actions]))
		if got := wd.ran("set " + c.key); got != want {
			t.Errorf("%s: the drag wrote %q, want %q", c.kind, got, want)
		}
		wd.s.Still(3 * twice)
		if got := wd.children("set " + c.key); len(got) != 1 {
			t.Errorf("%s: one drag wrote %v", c.kind, got)
		}
		// Another line was dragged, the left section's edge or the other
		// pane's: this pane changed along its other side only, and the two
		// measure together what they did. Nothing is written.
		mu.Lock()
		talkW, talkH := tw, th
		mu.Unlock()
		move(c.to[0]-4*map[bool]int{true: 1}[c.kind == actions], c.to[1]-4*map[bool]int{true: 1}[c.kind == fleet], talkW, talkH)
		wd.s.Still(3 * twice)
		if got := wd.children("set " + c.key); len(got) != 1 {
			t.Errorf("%s: a change along its other side wrote %v", c.kind, got)
		}
		// The action pane put back to the quarter it comes as is written as 0.
		if sum := c.to[1] + talkH; c.kind == actions {
			quarter := sum - int(talkShare*float64(sum)+0.5)
			move(c.to[0]-4, quarter, talkW, sum-quarter)
			wd.s.Still(3 * twice)
			if got := wd.children("set " + c.key); len(got) != 2 || !strings.HasPrefix(got[1], "set actions.height 0 ") {
				t.Errorf("the action pane put back to a quarter wrote %v", got)
			}
		}
	}
}
