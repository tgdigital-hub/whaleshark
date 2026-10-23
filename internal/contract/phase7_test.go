package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// 21.10: a space event brings the whole list of workspaces and a menu event
// brings nothing but its number; neither touches a pane.
func TestAPictureFollowsItsWorkspaces(t *testing.T) {
	s := &Snapshot{Seq: 4, Panes: []Pane{{ID: "p1", Tab: "t1", Workspace: "s1"}}, Spaces: []Space{{ID: "s1", Name: "shop", Tab: "t1", Front: true}}}
	now := []Space{{ID: "s1", Name: "shop", Tab: "t1"}, {ID: "s2", Root: "/work/blog", Name: "blog", Tab: "t2", Front: true}}
	Apply(s, TermEvent{Seq: 5, Kind: EvSpace, Spaces: now})
	Apply(s, TermEvent{Seq: 6, Kind: EvMenu, Pane: Pane{Tab: "t1"}, X: 40, Y: 0})
	if !reflect.DeepEqual(s.Spaces, now) || s.Seq != 6 || len(s.Panes) != 1 || s.Panes[0].Workspace != "s1" {
		t.Errorf("the picture after a space and a menu event: %+v", s)
	}
	var back Snapshot
	data, _ := json.Marshal(s)
	if json.Unmarshal(data, &back); !reflect.DeepEqual(back.Spaces, now) {
		t.Errorf("the workspaces do not survive the socket: %s", data)
	}
}

// 21.11: a label is one plain line of 24 characters at most that is never
// the form of an option, and the main agent's tab is known in any case.
func TestALabelIsALabelOnly(t *testing.T) {
	for in, want := range map[string]string{
		"sign-up page": "sign-up page", "  two   words ": "two words", "--help": "help", " - -x": "x", "a\x1b[31mb\nc": "a [31mb c",
		"left\u202eright\u2066!": "left right !", "an exceedingly long name for one tab": "an exceedingly long name", "": "", "\t\n": "",
	} {
		if got := Label(in); got != want {
			t.Errorf("Label(%q) = %q, want %q", in, got, want)
		}
	}
	for label, want := range map[string]bool{MainTab: true, " orchestrator ": true, "ORCHESTRATOR": true, "orchestrators": false, "": false} {
		if IsMainTab(label) != want {
			t.Errorf("IsMainTab(%q) is not %v", label, want)
		}
	}
	if TabLabel("C-42 an exceedingly long name for one tab that goes on and on and on") != "C-42 an exceedingly long name for one tab that goes on and o" {
		t.Errorf("a tab the program opens is cut at %d", TabMost)
	}
	if SpaceName("") != OtherTabs || SpaceName("/work/shop-site") != "shop-site" || SpaceName("/work/-x\x07y") != "x y" {
		t.Errorf("a workspace's own name: %q, %q, %q", SpaceName(""), SpaceName("/work/shop-site"), SpaceName("/work/-x\x07y"))
	}
}

// 21.11: seventeen calls are fenced where twelve were, the four through the
// fence are the same four, every call that names a pane, a tab or a
// workspace is walled, and the new refusals keep their names over the socket.
func TestTheFenceAndTheWallOfPhaseSeven(t *testing.T) {
	added := []string{OpTabRename, OpSpaceOpen, OpSpaceRename, OpSpaceClose, OpMarks}
	for _, op := range added {
		if !slices.Contains(Fenced, op) || slices.Contains(OwnTab, op) {
			t.Errorf("%s is not fenced, or has a way through", op)
		}
	}
	if len(Fenced) != 17 || len(OwnTab) != 4 || len(slices.Compact(slices.Sorted(slices.Values(Fenced)))) != 17 {
		t.Errorf("%d fenced calls and %d through", len(Fenced), len(OwnTab))
	}
	// What names nothing of a workspace, or only the caller's own pane.
	free := []string{OpHello, OpVersion, OpSnapshot, OpEvents, OpTabCreate, OpNotify, OpSetKeys, OpOverlay, OpDock, OpAttach,
		OpWindowSize, OpHook, OpStop, OpStatus, OpSpaceOpen, OpMarks}
	for _, op := range Walled {
		if slices.Contains(free, op) {
			t.Errorf("%s is walled and names no pane, tab or workspace of another", op)
		}
	}
	if len(Walled)+len(free) != 34 || len(slices.Compact(slices.Sorted(slices.Values(slices.Concat(Walled, free))))) != 34 {
		t.Errorf("%d walled and %d not: a call of the keeper is in neither list or in both", len(Walled), len(free))
	}
	for _, err := range []error{ErrSpace, ErrNotFront, ErrMainTab} {
		if got := ErrOf(WireReply{Err: ErrCode(fmt.Errorf("%w: t9", err)), Message: "t9"}); !errors.Is(got, err) {
			t.Errorf("%v came over the socket as %v", err, got)
		}
	}
	if ErrSpace.Error() != "that tab is another workspace's" || ErrNotFront.Error() != "that tab is not in front" ||
		ErrMainTab.Error() != "a workspace has one tab named Orchestrator" {
		t.Error("the keeper's three new refusals are not worded as the design words them")
	}
	var _ Terminals = NoTerminals{}
}

// 21.9: the two new settings, word for word, and the three sizes whose
// description gains its last sentence.
func TestTheTwoNewSettings(t *testing.T) {
	split, _ := FindKey("side.split")
	height, _ := FindKey("actions.height")
	if split == nil || split.Section != SecLayout || split.Label != "Where the agents begin" || split.Default != "50" || split.Store != StoreUI || split.Agent() != SetFree ||
		split.Help != "How far down the left section the agents begin, in hundredths of its height. As it comes they begin half-way. Drag the heavy line to change it; a double-click puts it back." {
		t.Errorf("side.split: %+v", split)
	}
	if height == nil || height.Section != SecLayout || height.Label != "Height of the action pane" || height.Default != "0" || height.Store != StoreUI || height.Agent() != SetFree ||
		height.Help != "In rows. 0 is a quarter of the tab, as it comes. Drag its edge to change it; a double-click puts it back." {
		t.Errorf("actions.height: %+v", height)
	}
	for value, ok := range map[string]bool{"20": true, "50": true, "80": true, "19": false, "81": false, "0": false, "half": false} {
		if _, err := split.Check(value, nil); (err == nil) != ok {
			t.Errorf("side.split %s: %v", value, err)
		}
	}
	for value, ok := range map[string]bool{"0": true, "3": true, "40": true, "1": false, "2": false, "41": false, "-3": false} {
		if _, err := height.Check(value, nil); (err == nil) != ok {
			t.Errorf("actions.height %s: %v", value, err)
		}
	}
	for _, name := range []string{"side.width", "fleet.width", "fleet.height", "actions.height"} {
		if k, _ := FindKey(name); !strings.HasSuffix(k.Help, " Drag its edge to change it; a double-click puts it back.") {
			t.Errorf("%s does not end by saying that its edge is dragged: %q", name, k.Help)
		}
	}
	ui := UIFile{SideSplit: 35, ActionsHeight: 9}
	if split.Of(&ui) != "35" || height.Of(&ui) != "9" {
		t.Errorf("the two keys read %q and %q from the file", split.Of(&ui), height.Of(&ui))
	}
}

// 21.3: the heavier line stands on a place, whatever the number of
// workspaces, and both panels keep their least.
func TestWhereTheAgentsBegin(t *testing.T) {
	for _, c := range [][3]int{{30, 0, 15}, {30, 50, 15}, {40, 50, 20}, {24, 50, 12}, {30, 20, 6}, {30, 80, 22}, {30, 5, 6}, {30, 99, 22}, {12, 50, 5}, {40, 35, 14}} {
		if got := SplitRow(c[0], c[1], SideHead, SideFoot); got != c[2] {
			t.Errorf("a section of %d rows with the line at %d hundredths: row %d, want %d", c[0], c[1], got, c[2])
		}
	}
	// The upper panel keeps its title and two rows, the lower its title and four.
	for h := 14; h < 80; h++ {
		for split := SideSplitMin; split <= SideSplitMax; split += 10 {
			row := SplitRow(h, split, SideHead, SideFoot)
			if row-SideHead-1 < SpaceRows || h-SideFoot-row-2 < AgentRows {
				t.Fatalf("%d rows at %d: the line on row %d leaves a panel too little", h, split, row)
			}
		}
	}
}

// 21.4 and 21.10: a menu holds only what applies, every row of it is a row
// of the action table, what a row runs is run as the person, and nothing a
// menu runs deletes anything.
func TestWhatAMenuHolds(t *testing.T) {
	for _, c := range []struct {
		kind                string
		working, other, tab bool
		want                string
	}{
		{OfferTab, false, false, true, "Rename… · Close tab"},
		{OfferTab, false, false, false, ""},
		{OfferAgent, true, false, true, "Rename… · Stop agent & close"},
		{OfferAgent, true, false, false, "Rename… · Stop agent & close"},
		{OfferAgent, false, false, true, "Rename… · Close tab"},
		{OfferAgent, false, false, false, "Rename…"},
		{OfferFleet, true, false, false, "Park the fleet · Send its leader a note"},
		{OfferFleet, false, false, true, "Try again"},
		{OfferSpace, false, false, true, "Rename… · Remove from list"},
		{OfferSpace, false, true, true, "Remove from list"},
	} {
		var labels []string
		for _, id := range OfferRows(c.kind, c.working, c.other, c.tab) {
			a, ok := FindAction(id)
			if !ok {
				t.Fatalf("%s is no row of the action table", id)
			}
			labels = append(labels, a.Label)
		}
		if got := strings.Join(labels, " · "); got != c.want {
			t.Errorf("the menu of a %s (working %v, other %v, a tab %v): %s, want %s", c.kind, c.working, c.other, c.tab, got, c.want)
		}
	}
	for _, id := range []string{"rename", "rename-tab", "rename-space", "close-tab", "stop-close", "remove-space"} {
		a, ok := FindAction(id)
		line := strings.Join(a.Run, " ")
		if !ok || len(a.Run) > 0 && (a.How != HowChild || a.Run[len(a.Run)-1] != "--human") || len(a.Run) == 0 && a.How != HowScreen {
			t.Errorf("the row %s: %+v", id, a)
		}
		for _, never := range append(slices.Clone(Never), "close ", "rm", "--discard", "--abandon") {
			if strings.HasPrefix(line, never) || strings.Contains(line, " "+never) {
				t.Errorf("%s runs %q, which a menu never runs", id, line)
			}
		}
	}
	rename, _ := FindAction("rename")
	remove, _ := FindAction("remove-space")
	if strings.Join(rename.Run, " ") != "task edit {task} --name --file {file} --human" || strings.Join(remove.Run, " ") != "stop --all --human" || !remove.Confirm {
		t.Errorf("a rename runs %v and a remove %v", rename.Run, remove.Run)
	}
	if !slices.Equal(KeepFirst, []string{"remove-space", "close-tab"}) || FilesStay != "(your files stay)" {
		t.Error("the questions that open on Keep, or the line under Remove from list")
	}
	if got := fmt.Sprintf(AskRemove, "ops-portal", 5); got != "Remove ops-portal from the list? Its 5 agents are stopped and its tabs closed, the orchestrator's chat too. The job stays, stopped. No folder or file is deleted." {
		t.Errorf("the question of a remove: %s", got)
	}
}

// 21.9: the command table has the two flags and the form of ui run, the
// person alone stops all, and a task's edit takes a name.
func TestTheFlagsOfPhaseSeven(t *testing.T) {
	find := func(name string) *Command {
		i := slices.IndexFunc(Commands, func(c Command) bool { return c.Name == name })
		return &Commands[i]
	}
	stop, task, ui := find("stop"), find("task"), find("ui")
	all := slices.IndexFunc(stop.Flags, func(f Flag) bool { return f.Name == "all" })
	if all < 0 || stop.Flags[all].Who != H || !strings.Contains(stop.Usage, "stop --all") {
		t.Errorf("stop --all: %+v", stop.Flags)
	}
	if !strings.Contains(task.Usage, `task edit <id> [--name "<up to three words>"]`) || !slices.Contains(task.Exits, 2) || !slices.Contains(task.Exits, 5) {
		t.Errorf("task edit --name: %s %v", task.Usage, task.Exits)
	}
	if !strings.HasSuffix(ui.Usage, "ui run offer <kind> <id>") || len(Commands) != 44 {
		t.Errorf("ui run offer: %s; %d commands", ui.Usage, len(Commands))
	}
	name := "login form"
	if edit := (TaskEdit{Name: &name, Taken: []string{"Orchestrator"}}); edit.Brief != nil || *edit.Name != name {
		t.Error("an edit of a name alone")
	}
}

// 21.10: the two panes of each workspace, and an older file's one pair read
// as the pair of the workspace those panes are in.
func TestThePanesOfAWorkspace(t *testing.T) {
	snap := &Snapshot{Spaces: []Space{{ID: "s1", Root: "/work/shop"}, {ID: "s2", Root: "/work/blog"}},
		Panes: []Pane{{ID: "p3", Workspace: "s1"}, {ID: "p4", Workspace: "s1"}, {ID: "p9", Workspace: "s2"}}}
	old := UIFile{FleetPane: "p3", ActionsPane: "p4"}
	if got := old.PairOf("/work/shop", snap); got != (Pair{Fleet: "p3", Actions: "p4"}) {
		t.Errorf("an older file's pair in its own workspace: %+v", got)
	}
	if got := old.PairOf("/work/blog", snap); got != (Pair{}) {
		t.Errorf("an older file's pair in another workspace: %+v", got)
	}
	now := UIFile{FleetPane: "p3", Panes: map[string]Pair{"/work/blog": {Fleet: "p9"}}}
	if now.PairOf("/work/blog", snap) != (Pair{Fleet: "p9"}) || now.PairOf("/work/shop", snap) != (Pair{}) {
		t.Error("a file that names pairs is read by them alone")
	}
	data, _ := json.Marshal(UIFile{SideSplit: 40, ActionsHeight: 9, Panes: map[string]Pair{"": {Actions: "p2"}}})
	if !strings.Contains(string(data), `"side_split":40`) || !strings.Contains(string(data), `"actions_height":9`) || !strings.Contains(string(data), `"panes":{"":{"actions":"p2"}}`) {
		t.Errorf("the file's new fields: %s", data)
	}
}
