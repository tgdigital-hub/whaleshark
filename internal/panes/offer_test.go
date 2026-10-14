package panes

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/term/termtest"
	"github.com/tgdigital-hub/whaleshark/internal/view"
)

// boxOf is the program of a menu without a loop, on the afternoon of three
// workspaces: opened for a kind of thing and its id, and then its project,
// its run, its task and the word of a row, as the left section starts it.
func boxOf(t *testing.T, w, h int, want ...string) *pane {
	f := testkit.Spaces()
	k := contract.NewKit()
	k.Side, k.Platform = view.Side, system(t.TempDir(), nil)
	p := newPane(offer, termtest.New(w, h).Term, k, "", "", func() time.Time { return f.In.Now })
	p.plainMarks, p.me, p.in, p.picture, p.cfg, p.want = false, "", f.In, f.In.Terms, f.In.Person, want
	p.draw()
	return p
}

// sized is a menu's program in a pane of the size the left section asks the
// keeper for: the menu's own box.
func sized(t *testing.T, want ...string) *pane {
	p := boxOf(t, 60, 20, want...)
	w, h := p.th.menu()
	return boxOf(t, w, h, want...)
}

// turned draws a menu's program again in a pane of the size it has asked
// the keeper for by now, as the keeper gives it.
func turned(p *pane) {
	p.draw()
	p.t.Resize(p.boxed[0], p.boxed[1])
	p.draw()
}

const toms, ops = "/work/tomsleeps", "/work/ops-portal"

// The menus as the design draws them: on a tab the first block alone; on a
// workspace its two rows and the dim line that is none; on an agent's row
// what its work offers under a line.
func TestTheMenusAreTheFrames(t *testing.T) {
	for _, c := range []struct {
		name string
		want []string
		grid string
	}{
		{"an agent's tab", []string{contract.OfferAgent, "t-p2", toms, "r1", "T1"}, `
┌──────────────────────┐
│›Rename…              │
│ Stop agent & close   │
└──────────────────────┘`},
		{"a workspace", []string{contract.OfferSpace, "s3", ops, none, none}, `
┌──────────────────────┐
│›Rename…              │
│ Remove from list     │
│  (your files stay)   │
└──────────────────────┘`},
		{"an agent's row", []string{contract.OfferAgent, "t-p2", toms, "r1", "T1", "row"}, `
┌────────────────────────┐
│›Rename…                │
│ Stop agent & close     │
├────────────────────────┤
│ Answer                 │
│ Send a note or a link  │
└────────────────────────┘`},
		{"the orchestrator's row", []string{contract.OfferTab, "t-p1", toms, "r1", none, "row"}, `
┌─────────────────────────────┐
│›Rename…                     │
│ Close tab                   │
├─────────────────────────────┤
│ Send the lead agent a note  │
└─────────────────────────────┘`},
		{"a tab made by hand", []string{contract.OfferTab, "t-p5"}, `
┌──────────────────────┐
│›Rename…              │
│ Close tab            │
└──────────────────────┘`},
	} {
		p := sized(t, c.want...)
		shot("menu-"+strings.ReplaceAll(c.name, " ", "-"), p)
		if got := strings.TrimRight(grid(p), "\n"); got != strings.TrimPrefix(c.grid, "\n") {
			t.Errorf("the menu of %s is\n%s\nwant%s", c.name, got, c.grid)
		}
	}
	// A thing that is not there, or offers nothing, has no menu.
	for _, want := range [][]string{{contract.OfferSpace, "s9"}, {contract.OfferAgent, "t-p2", toms, "r1", "T9"}, {contract.OfferTab, "t-none"}, {contract.OfferFleet, "billing", toms, "r1", "G1"}} {
		if p := boxOf(t, 40, 10, want...); !p.done || strings.TrimSpace(grid(p)) != "" {
			t.Errorf("a menu for %v is drawn:\n%s", want, grid(p))
		}
	}
}

// After a row is taken the same box is a line to type on with the name as
// it is, or the row's question in the contract's words, opened on the button
// that keeps where the row ends what a person may not have meant.
func TestWhatATakenRowTurnsTheBoxInto(t *testing.T) {
	p := sized(t, contract.OfferAgent, "t-p2", toms, "r1", "T1")
	press(p, "enter")
	turned(p)
	if got, want := strings.TrimRight(grid(p), "\n"), `
┌ Rename ────────────────────┐
│ sign-up page               │
│ enter keep · esc leave     │
└────────────────────────────┘`; got != strings.TrimPrefix(want, "\n") {
		t.Errorf("the typing line is\n%s\nwant%s", got, want)
	}
	p = sized(t, contract.OfferAgent, "t-p2", toms, "r1", "T1")
	press(p, "s")
	turned(p)
	if got, want := strings.TrimRight(grid(p), "\n"), `
┌────────────────────────────────┐
│ Stop sign-up page and close    │
│ its tab? Its work stays in its │
│ own copy of the code.          │
│ ›[ Stop & close ]  [ Keep ]    │
└────────────────────────────────┘`; got != strings.TrimPrefix(want, "\n") {
		t.Errorf("the question of a stop is\n%s\nwant%s", got, want)
	}
	p = sized(t, contract.OfferSpace, "s3", ops, "-", "-")
	press(p, "down", "enter")
	turned(p)
	if got, want := strings.TrimRight(grid(p), "\n"), `
┌──────────────────────────────────┐
│ Remove ops-portal from the list? │
│ Its 5 agents are stopped and its │
│ tabs closed, the orchestrator's  │
│ chat too. The job stays, stopped.│
│ No folder or file is deleted.    │
│  [ Remove ] ›[ Keep ]            │
└──────────────────────────────────┘`; got != strings.TrimPrefix(want, "\n") {
		t.Errorf("the question of a remove is\n%s\nwant%s", got, want)
	}
	// The pane is the box at each of its sizes: nothing of it stands beside
	// the frame, where the conversation would be blanked.
	for _, keys := range [][]string{nil, {"enter"}, {"s"}} {
		p := sized(t, contract.OfferAgent, "t-p2", toms, "r1", "T1", "row")
		press(p, keys...)
		turned(p)
		w, h := p.box()
		if p.t.W != w || p.t.H != h || !strings.HasSuffix(p.t.Row(0), "┐") || !strings.HasPrefix(p.t.Row(h-1), "└") || !strings.HasSuffix(p.t.Row(h-1), "┘") {
			t.Errorf("after %v the pane is %d by %d and the box %d by %d:\n%s", keys, p.t.W, p.t.H, w, h, grid(p))
		}
	}
	// The arrows and Tab go between the two buttons; Escape keeps.
	if press(p, "left"); p.keep {
		t.Error("the left arrow left the button that keeps in front")
	}
	if press(p, "tab", "esc"); !p.keep || !p.done {
		t.Errorf("after tab and esc: keep %v, done %v", p.keep, p.done)
	}
	// A workspace with no job is asked about without a count; the
	// orchestrator's tab with its agents, on the button that keeps; a tab
	// with nothing of an agent's in it is closed without a question.
	for _, c := range []struct {
		want []string
		key  string
		text string
		keep bool
	}{
		{[]string{contract.OfferSpace, "s2"}, "r", "", false},
		{[]string{contract.OfferSpace, "s2"}, "down", "", false},
		{[]string{contract.OfferTab, "t-p1", toms, "r1", none, "row"}, "c", fmt.Sprintf(contract.AskCloseLead, 3), true},
		{[]string{contract.OfferAgent, "t-p2", toms, "r1", "T1"}, "s", fmt.Sprintf(contract.AskStopClose, "sign-up page"), false},
	} {
		p := boxOf(t, 60, 20, c.want...)
		if press(p, c.key); p.th == nil || p.asking == "" && c.text != "" || p.keep != c.keep {
			t.Errorf("%v after %q: asking %q, keep %v", c.want, c.key, p.asking, p.keep)
		} else if ask, _, _ := p.th.asks(p.asking); ask != c.text {
			t.Errorf("%v after %q asks %q, want %q", c.want, c.key, ask, c.text)
		}
	}
	bare := boxOf(t, 60, 20, contract.OfferSpace, "s2")
	if ask, doing, keep := bare.th.asks("remove-space"); ask != fmt.Sprintf(contract.AskRemoveBare, "TestRunFoldersAndPointer") || doing != contract.ButtonRemove || !keep {
		t.Errorf("a workspace with no job is asked about with %q %q %v", ask, doing, keep)
	}
}

// A name an agent or a folder chose is cleaned before it stands in a
// question, and is never a row of a menu.
func TestANameInAMenuIsCleaned(t *testing.T) {
	th := thing{kind: contract.OfferAgent, name: "pay\x1b[2Jme\u202enow", first: []string{"rename", "stop-close"}}
	ask, _, _ := th.asks("stop-close")
	if strings.ContainsAny(ask, "\x1b\u202e") || !strings.Contains(ask, "pay") {
		t.Errorf("the question is %q", ask)
	}
	p := sized(t, contract.OfferAgent, "t-p2", toms, "r1", "T1", "row")
	if got := grid(p); strings.Contains(got, "sign-up") || strings.Contains(got, "tomsleeps") {
		t.Errorf("a menu shows a name:\n%s", got)
	}
}

// spaced is a pane on the evening's project as one workspace of the
// keeper's, in front, with the project's folder as its own: the left
// section, or with want the program of a menu, for which want says what it
// is opened for once the model is there.
func spaced(t *testing.T, kind string, w, h int, want func(home string, s *contract.Side) []string) *world {
	return startWith(t, kind, w, h, nil, func(wd *world) { spaceIn(t, wd, want) })
}

// another is spaced for a second pane of one test.
func another(t *testing.T, kind string, w, h int, want func(home string, s *contract.Side) []string) *world {
	return begin(t, kind, w, h, nil, func(wd *world) { spaceIn(t, wd, want) })
}

func spaceIn(t *testing.T, wd *world, want func(home string, s *contract.Side) []string) {
	dirs, _ := wd.sys.Dirs()
	if err := contract.WriteProjects(wd.sys, dirs.State, []string{wd.home, filepath.Join(wd.home, "elsewhere")}); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(wd.home, "elsewhere"), 0o700)
	snap := *wd.fx.Terms
	snap.Panes = slices.Clone(snap.Panes)
	for i := range snap.Panes {
		snap.Panes[i].Workspace = "s1"
	}
	snap.Spaces = []contract.Space{{ID: "s1", Root: wd.home, Name: "shop", Tab: snap.Panes[0].Tab, Front: true}}
	wd.double.Load(&snap)
	k := wd.p.k
	k.Store = listed{k.Store.(reader)}
	k.Side = func(in contract.SideInput) *contract.Side {
		s := view.Side(in)
		if want != nil && wd.p.want == nil && in.Terms != nil {
			wd.p.want = want(wd.home, s)
		}
		return s
	}
}

// inLoop has the pane's own loop do a thing, and waits for it: a test reads
// nothing of a pane beside its loop.
func (wd *world) inLoop(do func(p *pane)) {
	got := make(chan struct{})
	go wd.p.tell(news{do: func() {
		do(wd.p)
		close(got)
	}})
	<-got
}

// task is the task of a row of the pane's model, by the row's name.
func (wd *world) rowOf(name string) (r contract.SideRow) {
	wd.inLoop(func(p *pane) { r = rowOf(p.bar, name) })
	return r
}

// row finds a row of the model by its name.
func rowOf(s *contract.Side, name string) contract.SideRow {
	return s.Rows[slices.IndexFunc(s.Rows, func(r contract.SideRow) bool { return r.Name == name })]
}

// ends waits for the program of a menu to end, which closes its pane.
func (wd *world) ends(within time.Duration) {
	wd.t.Helper()
	select {
	case <-wd.done:
	case <-time.After(within):
		wd.t.Fatalf("the menu's program has not ended; it shows:\n%s", wd.screen())
	}
}

func agentMenu(name string) func(string, *contract.Side) []string {
	return func(home string, s *contract.Side) []string {
		r := rowOf(s, name)
		return []string{contract.OfferAgent, r.Tab, r.Root, r.Run, r.Task, "row"}
	}
}

// A renamed agent: the row's first letter takes it, the name as it is
// stands on the line, and what is typed goes to the command by a private
// file, as the person; then the program ends.
func TestAMenuRenamesAnAgentThroughTheCommand(t *testing.T) {
	wd := spaced(t, offer, 34, 9, agentMenu("sign-up page"))
	wd.shows("›Rename…", "Stop agent & close", "Answer")
	task := wd.rowOf("sign-up page").Task
	wd.key("r")
	wd.shows("┌ Rename ─", "sign-up page", contract.RenameKeys)
	wd.s.Type(" two")
	wd.s.Key("enter")
	if got := wd.ran("task edit " + task); got != fmt.Sprintf("task edit %s --name --file %q --human", task, "sign-up page two") {
		t.Errorf("the rename ran %q", got)
	}
	wd.ends(5 * time.Second)
}

// A stop asks once, on the button that does it; a refusal is the command's
// own sentence in the box for two seconds, and then the box closes.
func TestAMenuStopsAnAgentAndShowsARefusal(t *testing.T) {
	wd := spaced(t, offer, 34, 9, agentMenu("sign-up page"))
	wd.shows("Stop agent & close")
	os.WriteFile(filepath.Join(wd.home, "refuse"), []byte("That task is being accepted."), 0o600)
	// The keeper is asked for the size of what the box turns into, each
	// time it turns: the pane is the box, and nothing beside it.
	asked := func(w, h float64) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			var last contract.WireCall
			for _, c := range wd.double.Calls() {
				if c.Op == contract.OpOverlay && len(c.Argv) == 0 {
					last = c
				}
			}
			if last.Dir == contract.OverAt && last.W == w && last.H == h {
				return
			} else if time.Now().After(end) {
				t.Fatalf("the keeper was last asked for a box of %v by %v, not %v by %v", last.W, last.H, w, h)
			}
		}
	}
	wd.key("down", "enter")
	wd.shows("›[ Stop & close ]  [ Keep ]", "its tab?")
	asked(34, 6)
	task := wd.rowOf("sign-up page").Task
	wd.key("enter")
	if got := wd.ran("stop " + task); got != "stop "+task+" --human" {
		t.Errorf("the stop ran %q", got)
	}
	wd.shows("That task is being")
	asked(saidWide, 3)
	at := time.Now()
	wd.ends(saidFor + 3*time.Second)
	if time.Since(at) < saidFor/2 {
		t.Errorf("the refusal stood for %v", time.Since(at))
	}
}

// What a row's work offers is run from the menu as from the row: an answer
// is typed and goes to the item.
func TestAMenuRunsWhatTheWorkOffers(t *testing.T) {
	wd := spaced(t, offer, 34, 9, agentMenu("sign-up page"))
	wd.shows("Answer")
	wd.key("a")
	wd.shows("┌ Answer ─")
	wd.s.Type("yes")
	wd.s.Key("enter")
	if got := wd.ran("answer "); !strings.Contains(got, `--file "yes" --human`) {
		t.Errorf("the answer ran %q", got)
	}
	wd.ends(5 * time.Second)
}

// A tab is renamed and closed by the keeper's own calls and by no command:
// an ended agent's tab is closed so, which leaves its copy of the code.
func TestAMenuRenamesAndClosesATabThroughTheKeeper(t *testing.T) {
	tab := func(home string, s *contract.Side) []string {
		return []string{contract.OfferTab, rowOf(s, "sign-up page").Tab}
	}
	wd := spaced(t, offer, 34, 6, tab)
	wd.shows("›Rename…", "Close tab")
	wd.key("enter")
	wd.shows("┌ Rename ─")
	wd.s.Type(" x")
	wd.s.Key("enter")
	wd.asked("tab-rename")
	wd.ends(5 * time.Second)
	if calls := wd.double.Calls(); !slices.ContainsFunc(calls, func(c contract.WireCall) bool {
		return c.Op == contract.OpTabRename && strings.HasSuffix(c.Label, " x")
	}) {
		t.Errorf("the keeper was asked: %+v", calls)
	}
	shut := another(t, offer, 34, 6, tab)
	shut.shows("Close tab")
	shut.key("c")
	shut.shows("An agent is in this tab.", "›[ Close ]  [ Keep ]")
	shut.key("enter")
	shut.asked("tab-close")
	shut.ends(5 * time.Second)
	if got := shut.children("close"); len(got) > 0 {
		t.Errorf("a tab was closed with a command: %v", got)
	}
}

// listing is a folder as a person would compare it: every file under it
// with its size and when it was last written, but for the project's own
// record, and for what is the test's and not the project's: what it wrote
// down of the children, the word it has them refuse with, and the pretended
// login, which this world keeps in the same folder.
func listing(t *testing.T, root string) string {
	var lines []string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		rel, _ := filepath.Rel(root, path)
		if err != nil || rel == "children.log" || rel == "refuse" || rel == "." {
			return nil
		} else if info.IsDir() && (rel == contract.ProjectDir || strings.HasPrefix(rel, ".") || rel == "roaming" || rel == "local") {
			return filepath.SkipDir
		}
		lines = append(lines, fmt.Sprintf("%s %d %v %v", rel, info.Size(), info.Mode(), info.ModTime().UnixNano()))
		return nil
	})
	return strings.Join(lines, "\n")
}

// Remove from list: the question opens on the button that keeps, so Enter
// by habit removes nothing. Removed, every agent of each open job the
// workspace shows is stopped by the one command, and only when that ended
// well are its tabs closed; the project's folder is what it was, file for
// file. A stop that is refused leaves the workspace and says which task.
func TestRemoveStopsTheJobClosesTheTabsAndDeletesNothing(t *testing.T) {
	space := func(string, *contract.Side) []string { return []string{contract.OfferSpace, "s1"} }
	fill := func(wd *world) string {
		os.MkdirAll(filepath.Join(wd.home, "src", "deep"), 0o700)
		os.WriteFile(filepath.Join(wd.home, "src", "deep", "main.go"), []byte("package main\n"), 0o600)
		os.WriteFile(filepath.Join(wd.home, "notes.txt"), []byte("mine"), 0o600)
		return listing(t, wd.home)
	}
	kept := spaced(t, offer, 38, 10, space)
	before := fill(kept)
	kept.shows("Remove from list", contract.FilesStay)
	kept.key("down", "enter")
	kept.shows("›[ Keep ]", "folder or file is deleted.")
	kept.key("enter")
	kept.ends(5 * time.Second)
	if got := kept.children("stop"); len(got) > 0 || listing(t, kept.home) != before {
		t.Errorf("Enter on the button that keeps ran %v", got)
	}

	wd := another(t, offer, 38, 10, space)
	before = fill(wd)
	wd.shows("Remove from list")
	wd.key("down", "enter")
	wd.shows("›[ Keep ]")
	wd.key("left")
	wd.shows("›[ Remove ]")
	wd.key("enter")
	if got := wd.ran("stop --all"); got != "stop --all --human" {
		t.Errorf("the remove ran %q", got)
	}
	wd.asked("space-close")
	wd.ends(5 * time.Second)
	if after := listing(t, wd.home); after != before {
		t.Errorf("the project's folder changed:\n%s\nwas\n%s", after, before)
	}
	if snap, _ := wd.double.Snapshot(t.Context()); len(snap.Spaces) != 0 && slices.ContainsFunc(snap.Spaces, func(s contract.Space) bool { return s.ID == "s1" }) {
		t.Errorf("the workspace is still the keeper's: %+v", snap.Spaces)
	}

	stays := another(t, offer, 38, 10, space)
	os.WriteFile(filepath.Join(stays.home, "refuse"), []byte("T3 is being accepted and was not stopped."), 0o600)
	before = fill(stays)
	stays.shows("Remove from list")
	stays.key("down", "enter", "left", "enter")
	stays.ran("stop --all")
	stays.shows("T3 is being accepted")
	stays.ends(saidFor + 3*time.Second)
	if slices.ContainsFunc(stays.double.Calls(), func(c contract.WireCall) bool { return c.Op == contract.OpSpaceClose }) || listing(t, stays.home) != before {
		t.Errorf("a refused stop closed the workspace: %+v", stays.double.Calls())
	}
}

// raw sends the pretended terminal one report of the pointer: the button's
// number as a terminal says it, the cell, and whether it is a press.
func (wd *world) raw(button, x, y int, press bool) {
	wd.s.Type(fmt.Sprintf("\x1b[<%d;%d;%d%s", button, x+1, y+1, map[bool]string{true: "M", false: "m"}[press]))
}

// placed waits for a menu asked of the keeper and returns the call.
func (wd *world) placed() contract.WireCall {
	wd.t.Helper()
	wd.asked("ui run offer")
	return wd.double.Calls()[wd.nextCall-1]
}

// The left section opens a menu where it is asked for: under a right press
// on a row, one cell right of and below the pointer; under the full stop on
// the chosen row; and for the keeper's word about a tab, with the first
// block alone. A row that has just moved by itself opens none.
func TestTheLeftSectionOpensTheMenuOfTheThingUnderIt(t *testing.T) {
	wd := startWith(t, side, 28, 40, nil, func(wd *world) {
		spaceIn(t, wd, nil)
		wd.p.me = "dock"
	})
	wd.shows("sign-up page", "▌shop")
	x, y, _ := wd.s.Find("sign-up page")
	wd.settle()
	wd.raw(2, x, y, true)
	c := wd.placed()
	r := wd.rowOf("sign-up page")
	if got := strings.Join(c.Argv[1:], " "); c.Dir != contract.OverAt || c.Pane != "dock" || c.X != x+1 || c.Y != y+1 ||
		got != strings.Join([]string{"ui run offer agent", r.Tab, r.Root, r.Run, r.Task, "row"}, " ") {
		t.Errorf("the menu of a row was asked for with %+v", c)
	}
	var th thing
	wd.inLoop(func(p *pane) { th, _ = p.thingOf(contract.OfferAgent, r.Tab, r, true) })
	if w, h := th.menu(); c.W != float64(w) || c.H != float64(h) || w < menuWide || h != len(r.Offer)+len(r.Actions)+3 {
		t.Errorf("the menu's pane is %v by %v, the thing's box %d by %d", c.W, c.H, w, h)
	}
	// A workspace's row, and the tabs of no project.
	sx, sy, _ := wd.s.Find("shop")
	wd.raw(2, sx, sy, true)
	// No part of the command line is a lone dash, for which the command
	// would read its standard input and the menu never stand.
	if c := wd.placed(); strings.Join(c.Argv[1:6], " ") != "ui run offer space s1" || c.Y != sy+1 || slices.Contains(c.Argv, "-") || c.Argv[len(c.Argv)-1] != none {
		t.Errorf("the menu of a workspace was asked for with %+v", c)
	}
	// The keeper's word: a person asked for the menu of a tab on the tab row.
	wd.double.Menu(r.Tab, 40, 0)
	if c := wd.placed(); c.Pane != "" || c.X != 41 || c.Y != 1 || slices.Contains(c.Argv, "row") || c.Argv[4] != contract.OfferAgent {
		t.Errorf("the menu of a tab was asked for with %+v", c)
	}
	// The full stop on the chosen row, from the keys.
	wd.key("down", ".")
	c = wd.placed()
	if _, ky, _ := wd.s.Find("▌"); c.Pane != "dock" || c.X != 6 || !slices.Contains(c.Argv, "row") {
		t.Errorf("the full stop asked for %+v (the bar is on row %d)", c, ky)
	}
	// The click guard, asked before a menu opens.
	n := len(wd.double.Calls())
	wd.settle()
	wd.inLoop(func(p *pane) { p.movedAt[y] = time.Now() })
	wd.raw(2, x, y, true)
	wd.shows("the list moved")
	for _, c := range wd.double.Calls()[n:] {
		if c.Op == contract.OpOverlay {
			t.Errorf("a row that moved opened a menu: %+v", c)
		}
	}
}

// The heavier line is dragged, stored once when the button is let go, put
// back by a double-click, and moved by a click and then a click.
func TestTheHeavierLineIsDraggedPutBackAndStored(t *testing.T) {
	wd := spaced(t, side, 28, 40, nil)
	wd.shows("AGENTS · shop")
	line := func() int {
		for y := range 40 {
			if strings.HasPrefix(wd.s.Row(y), "━") || strings.HasPrefix(wd.s.Row(y), "━") {
				return y
			}
		}
		return -1
	}
	if line() != 20 {
		t.Fatalf("the line stands on row %d:\n%s", line(), wd.screen())
	}
	wd.raw(0, 5, 20, true)
	for y := 19; y >= 12; y-- {
		wd.raw(32, 5, y, true)
	}
	wd.shows("▲▼ 12 rows")
	wd.raw(0, 5, 12, false)
	if got := wd.ran("set side.split"); got != "set side.split 30 --human" {
		t.Errorf("the drag wrote %q", got)
	}
	if !wd.until(3*time.Second, func() bool { return line() == 12 && !strings.Contains(wd.s.Row(12), "rows") }) {
		t.Fatalf("after the drag the section is\n%s", wd.screen())
	}
	if got := wd.children("set side.split"); len(got) != 1 {
		t.Errorf("one drag wrote %v", got)
	}
	// A click on it and a click where it should go.
	wd.raw(0, 5, 12, true)
	wd.raw(0, 5, 12, false)
	wd.shows("click where it should")
	time.Sleep(twice + 50*time.Millisecond)
	wd.raw(0, 9, 24, true)
	wd.raw(0, 9, 24, false)
	if got := wd.ran("set side.split"); got != "set side.split 60 --human" {
		t.Errorf("the second click wrote %q", got)
	}
	if !wd.until(3*time.Second, func() bool { return line() == 24 }) {
		t.Fatalf("after the two clicks the section is\n%s", wd.screen())
	}
	// A double-click puts it back to how it comes.
	wd.raw(0, 5, 24, true)
	wd.raw(0, 5, 24, false)
	wd.raw(0, 5, 24, true)
	wd.raw(0, 5, 24, false)
	if got := wd.ran("set side.split"); got != "set side.split 50 --human" {
		t.Errorf("the double-click wrote %q", got)
	}
	if !wd.until(3*time.Second, func() bool { return line() == 20 }) {
		t.Fatalf("after the double-click the section is\n%s", wd.screen())
	}
}

// A click on a workspace's row brings it to the front by the workspace's own
// call, which leaves the keys where they are; [+] lists the projects with
// no workspace and opens one's workspace; a folder that is gone is said.
func TestAWorkspacesRowAndThePlusSign(t *testing.T) {
	wd := spaced(t, side, 28, 40, nil)
	wd.click("shop")
	wd.asked("space-open")
	for _, c := range wd.double.Calls() {
		if c.Op == contract.OpTabFocus || c.Op == contract.OpDock {
			t.Errorf("a click on a workspace's row moved the keys: %+v", c)
		}
	}
	wd.click("[+]")
	wd.over("open a project")
	wd.shows("elsewhere")
	wd.s.Key("enter")
	wd.asked("space-open")
	if c := wd.double.Calls()[wd.nextCall-1]; c.Space != filepath.Join(wd.home, "elsewhere") {
		t.Errorf("[+] asked for %+v", c)
	}
	wd.shows("▌elsewhere")
}

// What waits in each tab is told the keeper as the model says it, from the
// dock's pane, and again when it changes.
func TestTheLeftSectionTellsTheKeeperWhatWaitsInEachTab(t *testing.T) {
	os.Setenv(contract.EnvPane, "dock")
	defer os.Unsetenv(contract.EnvPane)
	wd := begin(t, side, 28, 40, nil, func(wd *world) { spaceIn(t, wd, nil) })
	wd.shows("sign-up page")
	var want map[string]int
	wd.inLoop(func(p *pane) { want = p.bar.Marks })
	if len(want) == 0 || !wd.until(3*time.Second, func() bool { return fmt.Sprint(wd.double.Told()) == fmt.Sprint(want) }) {
		t.Fatalf("the keeper was told %v, the model says %v", wd.double.Told(), want)
	}
}

// The timing scenario with three workspaces: a change of the record is on
// the left section within 200 ms.
func TestTheHalvesShowAChangeWithin200ms(t *testing.T) {
	var name string
	wd := startWith(t, side, 28, 40, nil, func(wd *world) {
		spaceIn(t, wd, nil)
		snap, _ := wd.double.Snapshot(t.Context())
		for i, root := range []string{"/w/two", "/w/three"} {
			id := fmt.Sprint("s", i+2)
			snap.Spaces = append(snap.Spaces, contract.Space{ID: id, Root: root, Name: filepath.Base(root), Tab: "t-" + id})
			snap.Panes = append(snap.Panes, contract.Pane{ID: "p-" + id, Tab: "t-" + id, Label: contract.MainTab, Cwd: root, Workspace: id})
		}
		wd.double.Load(snap)
		build := wd.p.k.Side
		wd.p.k.Side = func(in contract.SideInput) *contract.Side {
			s := build(in)
			wd.mu.Lock()
			defer wd.mu.Unlock()
			if name != "" && len(s.Rows) > 1 {
				s.Rows[1].Name = name
			}
			return s
		}
	})
	wd.shows("AGENTS · shop", "two", "three")
	late := 0
	for i := range 20 {
		at := time.Now()
		wd.mu.Lock()
		name = fmt.Sprint("step ", i)
		wd.mu.Unlock()
		wd.touch()
		if !wd.within(200*time.Millisecond-time.Since(at), fmt.Sprint("step ", i)) {
			late++
			wd.shows(fmt.Sprint("step ", i))
		}
		time.Sleep(30 * time.Millisecond)
	}
	if late > 1 {
		t.Errorf("%d of 20 changes took longer than 200 ms to be on the left section", late)
	}
}

var _ = term.Left
