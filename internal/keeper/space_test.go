package keeper

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/layout"
)

// spaces is the keeper's workspaces as one line: each with its name, the tab
// it shows and a star when it is in front, then every tab with its workspace.
func (r *rig) spaces() string {
	var b strings.Builder
	r.locked(func() bool {
		for _, s := range r.k.spaces() {
			fmt.Fprintf(&b, "%s %q shows %s%s; ", s.ID, s.Name, s.Tab, map[bool]string{true: "*"}[s.Front])
		}
		for _, t := range r.k.lay.Tabs {
			fmt.Fprintf(&b, "%s in %s ", t.ID, t.Space)
		}
		return true
	})
	return strings.TrimSpace(b.String())
}

func (r *rig) spacesAre(what, want string) {
	r.t.Helper()
	if got := r.spaces(); got != want {
		r.t.Fatalf("%s:\n%s\nwant\n%s", what, got, want)
	}
}

// The keeper keeps workspaces: one is made for a project's folder with its
// first tab, a tab is of one from its opening to its end whatever the call
// from a pane says and wherever its program goes, showing a tab brings its
// workspace to the front and nothing else does, each remembers its own tab,
// one ends with its last tab, and all of it comes back after a restart.
func TestWorkspaces(t *testing.T) {
	r := newRig(t)
	c := r.dial()
	events := r.dial()
	events.send(contract.WireCall{Op: contract.OpEvents})
	picture := events.reply().Snapshot
	reef, kelp := t.TempDir(), t.TempDir()
	must := func(call contract.WireCall) contract.WireReply {
		t.Helper()
		reply := c.call(call)
		if reply.Err != "" {
			t.Fatalf("%s: %+v", call.Op, reply)
		}
		return reply
	}
	first := must(contract.WireCall{Op: contract.OpSpaceOpen, Space: reef, Cwd: reef}).Pane
	if first.ID != "p1" || first.Label != contract.MainTab || first.Workspace != "s1" || !first.Focused || r.program("p1").spec.Dir != reef {
		t.Fatalf("the first tab of a workspace is %+v", first)
	}
	if !r.locked(func() bool { return r.k.lay.Tab("t1").Chat }) {
		t.Error("a workspace's first tab does not follow its chat's name")
	}
	must(contract.WireCall{Op: contract.OpSpaceOpen, Space: kelp, Cwd: kelp})
	reefName, kelpName := contract.SpaceName(reef), contract.SpaceName(kelp)
	r.spacesAre("two folders opened", fmt.Sprintf(`s1 %q shows t1; s2 %q shows t2*; t1 in s1 t2 in s2`, reefName, kelpName))
	// Opened again, nothing is made: it comes to the front with the tab it showed.
	if again := must(contract.WireCall{Op: contract.OpSpaceOpen, Space: reef + "/", Cwd: kelp}).Pane; again.ID != "p1" {
		t.Errorf("a workspace opened again answers %+v", again)
	}
	if reply := c.call(contract.WireCall{Op: contract.OpSpaceOpen, Space: reef + "/no-such-folder"}); reply.Err == "" {
		t.Error("a workspace was made for a folder that is not there")
	}
	// From a pane a tab is of that pane's workspace, though the call names another.
	own := must(contract.WireCall{Op: contract.OpTabCreate, Space: kelp, Label: "worker", From: "p1"}).Pane
	far := must(contract.WireCall{Op: contract.OpTabCreate, Space: kelp, Label: "far"}).Pane
	none := must(contract.WireCall{Op: contract.OpTabCreate, Label: "by hand"}).Pane
	split := must(contract.WireCall{Op: contract.OpSplit, Pane: far.ID, Dir: contract.Right}).Pane
	if own.Workspace != "s1" || far.Workspace != "s2" || none.Workspace != "s1" || split.Workspace != "s2" {
		t.Errorf("the tabs' workspaces are %s, %s, %s and the split's %s", own.Workspace, far.Workspace, none.Workspace, split.Workspace)
	}
	// A tab stays where it is when its program goes to another folder.
	c.call(contract.WireCall{Op: contract.OpAgentStart, Kind: "claude", Pane: "p1"}) // answered when its wait is over
	must(contract.WireCall{Op: contract.OpHook, Kind: "SessionStart", Pane: "p1", Session: "s-1", Cwd: kelp})
	r.until("the agent's folder", func() bool { return r.locked(func() bool { return r.k.panes["p1"].rec.Cwd == kelp }) })
	was := fmt.Sprintf(`s1 %q shows t1*; s2 %q shows t2; t1 in s1 t2 in s2 t3 in s1 t4 in s2 t5 in s1`, reefName, kelpName)
	r.spacesAre("five tabs", was)
	// Work in a workspace that is not in front changes nothing in front.
	for _, call := range []contract.WireCall{{Op: contract.OpScreen, Pane: far.ID}, {Op: contract.OpSize, Pane: far.ID},
		{Op: contract.OpRun, Pane: far.ID, Argv: []string{"a-program"}}, {Op: contract.OpTabRename, Tab: far.Tab, Label: "far away"},
		{Op: contract.OpResize, Pane: far.ID, Dir: contract.Right, Ratio: 0.1}, {Op: contract.OpPaneClose, Pane: split.ID}} {
		must(call)
	}
	if r.spacesAre("after work behind", was); r.focused() != "p1" {
		t.Errorf("the keys went to %s", r.focused())
	}
	// A screen of ours never opens over a project it is not about.
	if reply := c.call(contract.WireCall{Op: contract.OpOverlay, Argv: []string{"a-program"}, From: far.ID}); reply.Err != contract.ErrNotFront.Error() {
		t.Errorf("an overlay asked from behind: %+v", reply)
	}
	// Showing a tab, or giving a pane the keys, brings its workspace along,
	// and each workspace remembers the tab it showed.
	must(contract.WireCall{Op: contract.OpTabFocus, Tab: far.Tab})
	r.spacesAre("a tab behind shown", strings.NewReplacer("t1*", "t1", "shows t2", "shows t4*").Replace(was))
	must(contract.WireCall{Op: contract.OpPaneFocus, Pane: none.ID})
	r.spacesAre("a pane behind given the keys", strings.NewReplacer("t1*", "t5*", "shows t2", "shows t4").Replace(was))
	if reply := must(contract.WireCall{Op: contract.OpOverlay, Argv: []string{"a-program"}, From: "p1"}); reply.Err != "" {
		t.Errorf("an overlay asked from the workspace in front: %+v", reply)
	}
	var over string
	r.locked(func() bool { over = r.k.over.rec.ID; return true })
	r.program(over).Close()
	r.until("the overlay to go", func() bool { return r.locked(func() bool { return r.k.over == nil }) })
	// A name a person gave, and none: the folder's again.
	must(contract.WireCall{Op: contract.OpSpaceRename, Space: "s2", Label: " --the\x1b[31m kelp\u202e forest that is far too long a name "})
	r.spacesAre("a workspace named", fmt.Sprintf(`s1 %q shows t5*; s2 "the [31m kelp forest tha" shows t4; t1 in s1 t2 in s2 t3 in s1 t4 in s2 t5 in s1`, reefName))
	must(contract.WireCall{Op: contract.OpSpaceRename, Space: "s1", Label: "the reef"})
	if reply := c.call(contract.WireCall{Op: contract.OpSpaceRename, Space: "s9", Label: "x"}); reply.Err != contract.ErrNoPane.Error() {
		t.Errorf("a workspace that is not there was named: %+v", reply)
	}
	must(contract.WireCall{Op: contract.OpSpaceRename, Space: "s2"})
	named := fmt.Sprintf(`s1 "the reef" shows t5*; s2 %q shows t4; t1 in s1 t2 in s2 t3 in s1 t4 in s2 t5 in s1`, kelpName)
	r.spacesAre("named and unnamed", named)

	// The keeper started again: the same tabs under the same ids in the same
	// workspaces, the same one in front, the name a person gave.
	r.again()
	c = r.dial()
	r.spacesAre("after a restart", named)
	if got := must(contract.WireCall{Op: contract.OpSnapshot}).Snapshot; len(got.Spaces) != 2 || !got.Spaces[0].Named || got.Spaces[0].Root != reef || got.Spaces[1].Named ||
		slices.ContainsFunc(got.Panes, func(p contract.Pane) bool {
			return p.Workspace != map[bool]string{true: "s2", false: "s1"}[p.Tab == "t2" || p.Tab == "t4"]
		}) {
		t.Errorf("the picture after a restart: %+v", got)
	}
	if next := must(contract.WireCall{Op: contract.OpSpaceOpen, Cwd: reef}).Pane; next.Workspace != "s3" || next.Tab != "t6" {
		t.Errorf("the tabs of no project after a restart: %+v", next)
	}
	// A tab closed: the next of its own workspace shows, never another's; a
	// workspace ends with its last tab, and then the first that is left is
	// in front. A closed workspace takes every tab of its own and no other.
	must(contract.WireCall{Op: contract.OpTabFocus, Tab: "t3"})
	must(contract.WireCall{Op: contract.OpTabClose, Tab: "t3"})
	r.spacesAre("the tab that showed closed", fmt.Sprintf(`s1 "the reef" shows t5*; s2 %q shows t4; s3 "other tabs" shows t6; t1 in s1 t2 in s2 t4 in s2 t5 in s1 t6 in s3`, kelpName))
	must(contract.WireCall{Op: contract.OpTabClose, Tab: "t4"})
	must(contract.WireCall{Op: contract.OpSpaceClose, Space: "s1"})
	r.spacesAre("a workspace closed", fmt.Sprintf(`s2 %q shows t2*; s3 "other tabs" shows t6; t2 in s2 t6 in s3`, kelpName))
	if r.focused() != "p2" || r.locked(func() bool { return len(r.k.panes) != 2 || len(r.k.tabs) != 2 }) {
		t.Errorf("the keys are in %s", r.focused())
	}
	must(contract.WireCall{Op: contract.OpPaneClose, Pane: "p2"})
	r.spacesAre("a workspace's last pane closed", `s3 "other tabs" shows t6*; t6 in s3`)
	must(contract.WireCall{Op: contract.OpTabClose, Tab: "t6"})
	r.spacesAre("the last tab closed", "")
	if next := must(contract.WireCall{Op: contract.OpTabCreate, Label: "anew"}).Pane; next.Workspace != "s4" {
		t.Errorf("a workspace's id was given twice: %+v", next)
	}

	// Every reader of events was told each time, and the events applied to
	// its first picture give the workspaces as they were before the restart.
	for told := 0; ; {
		ev := events.reply().Event
		if ev == nil {
			t.Fatal("the events ended")
		}
		if contract.Apply(picture, *ev); ev.Kind == contract.EvSpace {
			told++
		}
		if ev.Kind == contract.EvSpace && len(ev.Spaces) == 2 && ev.Spaces[0].Name == "the reef" && ev.Spaces[1].Name == kelpName {
			if told < 8 || !slices.Equal(picture.Spaces, ev.Spaces) || picture.Spaces[0].Tab != "t5" || !picture.Spaces[0].Front {
				t.Errorf("after %d events of workspaces the picture holds %+v", told, picture.Spaces)
			}
			break
		}
	}
}

// A window's hello says the folder `open` was typed in and its project: that
// workspace comes to the front, made with its first tab where there is
// none, and typed again nothing is made. A window that says nothing gets a
// first tab only where there is no tab at all.
func TestAWindowsHelloOpensItsWorkspace(t *testing.T) {
	r := newRig(t)
	reef := t.TempDir()
	hello := func(space, cwd string) {
		t.Helper()
		before := r.k.seq
		o := &outer{client: r.dial(), r: r}
		o.send(contract.WireCall{Op: contract.OpAttach, W: 100, H: 30, Cwd: cwd, Space: space, Env: []string{"TERM=xterm-256color"}})
		go io.Copy(io.Discard, o.conn)
		r.until("the window", func() bool {
			return r.locked(func() bool { return len(r.k.wins) > 0 && r.k.seq > before || cwd == "" })
		})
	}
	r.window(100, 30)
	r.until("a first tab", func() bool { return r.locked(func() bool { return len(r.k.lay.Tabs) == 1 }) })
	r.spacesAre("a window that names no folder", `s1 "other tabs" shows t1*; t1 in s1`)
	hello(reef, reef)
	want := fmt.Sprintf(`s1 "other tabs" shows t1; s2 %q shows t2*; t1 in s1 t2 in s2`, contract.SpaceName(reef))
	if r.spacesAre("a window in a project's folder", want); r.program("p2").spec.Dir != reef || !r.locked(func() bool { return r.k.lay.Tab("t2").Label == contract.MainTab }) {
		t.Errorf("the workspace's first tab is in %q", r.program("p2").spec.Dir)
	}
	hello("", reef)
	r.spacesAre("a window in a folder of no project", strings.NewReplacer("t1;", "t1*;", "t2*", "t2").Replace(want))
	hello(reef, reef)
	hello("", "")
	r.spacesAre("the same folder again", want)
}

// A label is cleaned and cut whoever gives it, a tab a person named follows
// its chat no longer, and one tab of a workspace at most reads as the main
// agent's, in any letter case and with any blanks round it.
func TestATabsNameIsHeldToTheRules(t *testing.T) {
	r := newRig(t)
	c := r.dial()
	reef, kelp := t.TempDir(), t.TempDir()
	c.call(contract.WireCall{Op: contract.OpSpaceOpen, Space: reef})
	c.call(contract.WireCall{Op: contract.OpSpaceOpen, Space: kelp})
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "worker", From: "p1"})
	for _, label := range []string{"orchestrator ", "  ORCHESTRATOR", "\tOrchestrator\x07\n"} {
		if reply := c.call(contract.WireCall{Op: contract.OpTabRename, Tab: "t3", Label: label}); reply.Err != contract.ErrMainTab.Error() {
			t.Errorf("a second tab named %q in one workspace: %+v", label, reply)
		}
	}
	tab := func(id string) (t layout.Tab) {
		r.locked(func() bool { t = *r.k.lay.Tab(id); return true })
		return t
	}
	if got := tab("t3"); got.Label != "worker" || got.Named {
		t.Errorf("a refused name changed the tab: %+v", got)
	}
	// The other workspace's own, named so again, follows its chat again.
	for _, c2 := range []struct {
		label, want string
		chat        bool
	}{{"--force\x07 the\ttide\u202e", "force the tide", false}, {"a name that is longer than twenty-four characters", "a name that is longer th", false}, {contract.MainTab, contract.MainTab, true}} {
		if reply := c.call(contract.WireCall{Op: contract.OpTabRename, Tab: "t2", Label: c2.label}); reply.Err != "" {
			t.Fatalf("tab-rename %q: %+v", c2.label, reply)
		}
		if got := tab("t2"); got.Label != c2.want || got.Chat != c2.chat || got.Named == c2.chat || r.locked(func() bool { return r.k.panes["p2"].rec.Label != c2.want }) {
			t.Errorf("named %q the tab is %+v", c2.label, got)
		}
	}
	c.call(contract.WireCall{Op: contract.OpTabRename, Tab: "t1", Label: "the lead"})
	if reply := c.call(contract.WireCall{Op: contract.OpTabRename, Tab: "t3", Label: "orchestrator"}); reply.Err != "" || tab("t3").Label != "orchestrator" || tab("t1").Chat {
		t.Errorf("the name was free again: %+v", reply)
	}
	c.call(contract.WireCall{Op: contract.OpAgentStart, Pane: "p3", Kind: "claude", Name: "quiet otter"})
	// An agent the tool started names no tab, its own neither; it opens,
	// names and closes no workspace, and says nothing of what waits.
	for _, call := range []contract.WireCall{{Op: contract.OpTabRename, Tab: "t3", Label: "mine"}, {Op: contract.OpSpaceOpen, Space: reef},
		{Op: contract.OpSpaceRename, Space: "s1", Label: "mine"}, {Op: contract.OpSpaceClose, Space: "s1"}, {Op: contract.OpMarks, Marks: map[string]int{"t3": 9}}} {
		call.From = "p3"
		if reply := c.call(call); !strings.Contains(reply.Message, "not a call for an agent") {
			t.Errorf("%s from an agent's pane: %+v", call.Op, reply)
		}
	}
	if got := r.spaces(); !strings.HasPrefix(got, fmt.Sprintf("s1 %q shows t1; s2", contract.SpaceName(reef))) || tab("t3").Label != "orchestrator" {
		t.Errorf("a fenced call changed something: %s", got)
	}
}

// From a pane that holds an agent every call that names a pane, a tab or a
// workspace of another workspace is refused and changes nothing; the same
// calls inside its own workspace are not the wall's to refuse.
func TestTheWallBetweenWorkspaces(t *testing.T) {
	r := newRig(t)
	c := r.dial()
	reef, kelp := t.TempDir(), t.TempDir()
	c.call(contract.WireCall{Op: contract.OpSpaceOpen, Space: reef}) // t1 p1
	c.call(contract.WireCall{Op: contract.OpSpaceOpen, Space: kelp}) // t2 p2
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "near", From: "p1"})
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "far", From: "p2"})
	c.call(contract.WireCall{Op: contract.OpSplit, Pane: "p3", Dir: contract.Right}) // p5
	c.call(contract.WireCall{Op: contract.OpSplit, Pane: "p4", Dir: contract.Right}) // p6
	c.call(contract.WireCall{Op: contract.OpAgentStart, Pane: "p1", Kind: "claude"}) // a person's own agent
	c.call(contract.WireCall{Op: contract.OpAgentStart, Pane: "p4", Kind: "claude", Name: "far otter"})
	c.call(contract.WireCall{Op: contract.OpAgentStart, Pane: "p3", Kind: "claude", Name: "near otter"})
	if !r.locked(func() bool { return r.k.panes["p1"].rec.Agent == "claude" && r.k.panes["p1"].rec.Name == "" }) {
		t.Fatal("the pane the calls come from holds no agent of a person's own")
	}
	calls := func(pane, other, tab, name, space string) (out []contract.WireCall) {
		for _, op := range contract.Walled {
			call := contract.WireCall{Op: op, From: "p1", Pane: pane, Tab: tab, Space: space, Label: "x", Dir: contract.Right, Ratio: 0.1,
				Argv: []string{"a-program"}, Kind: "claude", Text: "go on"}
			switch op {
			case contract.OpPrompt:
				call.Pane, call.Name = "", name
			case contract.OpSwap, contract.OpTabGive:
				call.Target = other
			case contract.OpSay:
				call.Kind = contract.WhereTyped
			}
			out = append(out, call)
		}
		return out
	}
	state := func() string {
		s := c.call(contract.WireCall{Op: contract.OpSnapshot}).Snapshot
		for i := range s.Panes {
			s.Panes[i].Status = ""
		}
		return fmt.Sprintf("%+v %+v", s.Panes, s.Spaces) + r.spaces()
	}
	before := state()
	for _, call := range calls("p4", "p6", "t4", "far otter", "s2") {
		if reply := c.call(call); reply.Err != contract.ErrSpace.Error() {
			t.Errorf("%s on another workspace's: %+v", call.Op, reply)
		}
	}
	// A pane of its own named beside something of another workspace is no way through.
	for _, call := range []contract.WireCall{{Op: contract.OpSwap, Pane: "p3", Target: "p6"}, {Op: contract.OpTabGive, Tab: "t3", Target: "p6"},
		{Op: contract.OpPaneClose, Pane: "p3", Tab: "t4"}, {Op: contract.OpSpaceClose, Space: "s2"}, {Op: contract.OpSpaceRename, Space: "s2", Label: "mine"}} {
		call.From = "p1"
		if reply := c.call(call); reply.Err != contract.ErrSpace.Error() {
			t.Errorf("%s across the wall: %+v", call.Op, reply)
		}
	}
	if after := state(); after != before {
		t.Errorf("a call the wall refused changed the picture:\n%s\nwas\n%s", after, before)
	}
	// A pane with no agent in it, the dock's and a call that names no pane are behind no wall.
	for _, from := range []string{"p5", layout.DockPane, ""} {
		if reply := c.call(contract.WireCall{Op: contract.OpScreen, Pane: "p4", From: from}); reply.Err != "" {
			t.Errorf("a screen read from %q: %+v", from, reply)
		}
	}
	for _, call := range calls("p3", "p5", "t3", "near otter", "s1") {
		if reply := c.call(call); reply.Err == contract.ErrSpace.Error() {
			t.Errorf("%s inside its own workspace: %+v", call.Op, reply)
		}
	}
	if got := r.spaces(); !strings.HasPrefix(got, "s2 ") {
		t.Errorf("its own workspace, closed from inside, left %s", got)
	}
}

// What waits in each tab is told by the dock alone, a figure for a tab that
// is not there is dropped, and a closed tab's goes with it.
func TestMarksAreTheDocksToTell(t *testing.T) {
	r := newRig(t)
	c := r.dial()
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "one"})
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "two"})
	marks := func() (m string) {
		r.locked(func() bool { m = fmt.Sprint(r.k.lay.Marks); return true })
		return m
	}
	for _, from := range []string{"", "p1"} {
		if reply := c.call(contract.WireCall{Op: contract.OpMarks, Marks: map[string]int{"t1": 2}, From: from}); reply.Err == "" || marks() != "map[]" {
			t.Errorf("marks from %q: %+v, and the keeper holds %s", from, reply, marks())
		}
	}
	if reply := c.call(contract.WireCall{Op: contract.OpMarks, Marks: map[string]int{"t1": 2, "t2": 1, "t9": 4}, From: layout.DockPane}); reply.Err != "" || marks() != "map[t1:2 t2:1]" {
		t.Errorf("marks from the dock: %+v, and the keeper holds %s", reply, marks())
	}
	if c.call(contract.WireCall{Op: contract.OpTabClose, Tab: "t2"}); marks() != "map[t1:2]" {
		t.Errorf("after its tab closed the keeper holds %s", marks())
	}
}

// A tab is told its fleet by one word when its leader's tab is opened, and
// every tab opened from a pane of that fleet has it, whatever the call says.
// The workspace has two tab lines exactly while a tab of it is a fleet's;
// the second line costs each pane of it one row, told its program once, and
// no pane of another workspace anything. The keys walk the line that holds
// the tab that shows.
func TestAFleetsTabsAndTheSecondTabLine(t *testing.T) {
	r := newRig(t)
	c := r.dial()
	reef, kelp := t.TempDir(), t.TempDir()
	c.call(contract.WireCall{Op: contract.OpSpaceOpen, Space: kelp}) // t1 p1
	c.call(contract.WireCall{Op: contract.OpSpaceOpen, Space: reef}) // t2 p2
	o := r.window(100, 30)
	o.shows(contract.MainTab)
	sizes := func(pane string) string {
		r.settle()
		text := c.call(contract.WireCall{Op: contract.OpScreen, Pane: pane}).Text
		return strings.Join(strings.Fields(text[strings.Index(text, "size "):]), " ")
	}
	if got := sizes("p2"); got != "size 100x28" {
		t.Fatalf("before any fleet the lead's pane was told %q", got)
	}
	open := func(from, group, label string) contract.Pane {
		t.Helper()
		reply := c.call(contract.WireCall{Op: contract.OpTabCreate, Group: group, Label: label, From: from})
		if reply.Pane == nil {
			t.Fatalf("tab-create %s: %+v", label, reply)
		}
		return *reply.Pane
	}
	lead := open("p2", "front desk", "front desk")
	one := open(lead.ID, "", "sign-up page")
	two := open(lead.ID, "back office", "login page")
	next := open("p2", "back office", "back office")
	open(next.ID, "", "exports")
	open("", "", "by hand")
	groups := func() (s string) {
		r.locked(func() bool {
			for _, tab := range r.k.lay.Tabs {
				s += fmt.Sprintf("%s:%s:%v ", tab.ID, tab.Group, tab.Head)
			}
			return true
		})
		return s
	}
	if got, want := groups(), "t1::false t2::false t3:front desk:true t4:front desk:false t5:front desk:false t6:back office:true t7:back office:false t8::false "; got != want {
		t.Fatalf("the tabs' fleets are\n%s\nwant\n%s", got, want)
	}
	c.call(contract.WireCall{Op: contract.OpMarks, Marks: map[string]int{one.Tab: 1}, From: layout.DockPane})
	o.shows(" Orchestrator │ front desk · 2 ▲1 │ back office · 1 │ other · 1")
	if _, y := o.shows(" └ leader │ sign-up page │ login page"); y != 1 {
		t.Errorf("the second tab line is on row %d", y)
	}
	if got := sizes("p2"); got != "size 100x28 size 100x27" {
		t.Errorf("with the second line the lead's pane was told %q", got)
	}
	if got := sizes("p1"); got != "size 100x28" {
		t.Errorf("a pane of the workspace without a fleet was told %q", got)
	}
	// A click on a fleet's name shows its leader's tab the first time; on a
	// name of the second line that tab; and then the fleet's name goes there.
	click := func(text string, to string) {
		t.Helper()
		x, y := o.shows(text)
		o.click(x+1, y)
		r.until("the keys in "+to+" after a click on "+text, func() bool { return r.focused() == to })
	}
	if h := r.locked(func() bool { x, _ := o.shows("back office"); return r.k.lay.At(x, 0).Kind == layout.OnGroup }); !h {
		t.Error("a fleet's name on the first line is no group to the layout")
	}
	walk := func(keys string, to string) {
		t.Helper()
		o.types("\x00" + keys)
		r.until("the keys in "+to+" after the command key and "+keys, func() bool { return r.focused() == to })
	}
	c.call(contract.WireCall{Op: contract.OpTabFocus, Tab: lead.Tab})
	click("login page", two.ID)
	walk("n", lead.ID) // the second line, round to its start
	walk("p", two.ID)
	walk("2", one.ID)
	click(contract.MainTab, "p2")
	o.shows(" └ leader │ sign-up page │ login page") // the fleet looked at last, still there
	walk("n", one.ID)                                // the first line's groups, each at the tab it showed last
	c.call(contract.WireCall{Op: contract.OpTabFocus, Tab: "t2"})
	walk("p", "p8")
	// Another workspace in front has its own count of lines, and one line
	// again once no tab of this one is a fleet's.
	c.call(contract.WireCall{Op: contract.OpTabFocus, Tab: "t1"})
	if !r.locked(func() bool { return r.k.lay.Head() == 1 }) {
		t.Error("the workspace without a fleet has two tab lines")
	}
	c.call(contract.WireCall{Op: contract.OpTabFocus, Tab: "t2"})
	for _, tab := range []string{"t3", "t4", "t5", "t6"} {
		if c.call(contract.WireCall{Op: contract.OpTabClose, Tab: tab}); !r.locked(func() bool { return r.k.lay.Head() == 2 }) {
			t.Errorf("with %s closed the second line is gone", tab)
		}
	}
	c.call(contract.WireCall{Op: contract.OpTabClose, Tab: "t7"})
	if got := sizes("p2"); !r.locked(func() bool { return r.k.lay.Head() == 1 }) || got != "size 100x28 size 100x27 size 100x28" {
		t.Errorf("with no fleet left the lead's pane was told %q", got)
	}
}
