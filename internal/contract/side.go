package contract

import (
	"slices"
	"strings"
	"time"
)

// The left section's width in cells: what a person may set, and what it is
// when nothing is said. Under SideNamesFrom columns of window it is at most
// SideNames wide and shows names only, under SideCutFrom SideCut with names
// cut, and under SideRailFrom a rail of SideRail: marks and counts.
const (
	SideMin, SideMax, SideWidth              = 16, 60, 28
	SideNames, SideCut, SideRail             = 22, 14, 4
	SideNamesFrom, SideCutFrom, SideRailFrom = 160, 100, 72
	// TalkMin is the least the conversation is left while the window has
	// SideRailFrom columns or more: a pane that would take it below is a strip.
	TalkMin = 50
	// FleetWidth and FleetNarrow are the fleet pane's width beside the
	// conversation at SideNamesFrom columns and under; FleetColumn is one
	// column of cards where the fleet lies at the bottom, and FleetHeight
	// its rows there. Under FleetOffUnder columns the fleet is not shown.
	FleetWidth, FleetNarrow, FleetColumn, FleetHeight = 46, 36, 38, 9
	FleetOffUnder                                     = 40
)

// The forms a region can have. The left section: full (state words where
// they fit), names only, names cut with an ellipsis, or a rail of marks and
// counts. The fleet: cards of three lines, or of two with the context a
// figure. A pane of ours: open as it is set (full), its one line, which
// opens over the tab on a click or its key (strip), or off.
const (
	FormFull, FormNames, FormCut, FormRail     = "full", "names", "cut", "rail"
	FormCards3, FormCards2, FormStrip, FormOff = "cards3", "cards2", "strip", "off"
)

// Layout is what the person set and what the moment is: the left section's
// width, where the two panes lie (PersonConfig.Panes), the fleet's own width
// or 0, and whether an item holds work up, which opens a folded action pane.
type Layout struct {
	Side, FleetWide int
	Fleet, Actions  string
	Holding         bool
}

// Form is the form of every region at one width of the window. Side and
// Fleet are widths in cells without their dividing line, Fleet 0 when the
// fleet is no pane beside the conversation; Talk is what the conversation
// keeps.
type Form struct {
	Side, Fleet, Talk            int
	SideForm, FleetForm, Actions string
}

// Fit decides the form of every region for a window of cols columns, and is
// the only place that does: the keeper lays the dock out by it and the panes
// draw by it. Regions keep their side and their order at every width, and a
// form changes at one width, the same going up and going down, because Fit
// remembers nothing.
func Fit(cols int, l Layout) Form {
	f := Form{Side: min(max(l.Side, SideMin), SideMax), SideForm: FormFull, FleetForm: FormOff, Actions: FormOff}
	switch {
	case cols < SideRailFrom:
		f.Side, f.SideForm = SideRail, FormRail
	case cols < SideCutFrom:
		f.Side, f.SideForm = SideCut, FormCut
	case cols < SideNamesFrom:
		f.Side, f.SideForm = min(f.Side, SideNames), FormNames
	}
	f.Talk = cols - f.Side - 1
	if l.Actions != Off {
		if f.Actions = FormStrip; cols >= SideNamesFrom || cols >= SideCutFrom && l.Holding {
			f.Actions = FormFull
		}
	}
	room := f.Talk - 1 - TalkMin // what a pane beside the conversation may take
	switch {
	case l.Fleet == Off || cols < FleetOffUnder:
	case l.Fleet == Bottom:
		if f.FleetForm = FormStrip; cols >= SideRailFrom {
			f.FleetForm = FormCards2
		}
	case cols >= SideNamesFrom && room >= FleetNarrow:
		wide := l.FleetWide
		if wide == 0 {
			wide = FleetWidth
		}
		f.Fleet, f.FleetForm = min(max(wide, FleetNarrow), room), FormCards3
	case cols >= SideCutFrom && room >= FleetNarrow:
		f.Fleet, f.FleetForm = FleetNarrow, FormCards2
	default:
		f.FleetForm = FormStrip
	}
	if f.Fleet > 0 {
		f.Talk -= f.Fleet + 1
	}
	return f
}

// SideVersion is raised by every change to the shape of the side model.
// SideTitle is the left section's own title, and Bottom and Top the two
// places of a pane of ours that are no side.
const (
	SideVersion = 1
	SideTitle   = "WHALESHARK"
	Bottom, Top = "bottom", "top"
)

// SideProject is one project of the login's list as the left section is
// built from it: its folder, the branch it is on, and its open runs, oldest
// first. A run of ModeFleets is followed by its fleets' runs.
type SideProject struct {
	Root, Branch string
	Runs         []*State
	Saved        []SavedFleet // the fleets the person keeps here: a row each, with work or without
}

// WordSaved is the word of a saved fleet's row while it has no work.
const WordSaved = "saved"

// SideInput is everything the left section is built from. Rows is how many
// rows the agents panel has, 0 for no limit; Folded names the fleets the
// person folded, as "run/fleet"; Find is what was typed to narrow the rows;
// Changed is UIFile.Changed, which Warn is worded from.
type SideInput struct {
	Now      time.Time
	Projects []SideProject
	Terms    *Snapshot
	Tools    []Tool
	Person   PersonConfig
	Rows     int
	Folded   []string
	Find     string
	Changed  []string
	Checked  time.Time
	Notes    []string
}

// Side is the one description of the left section, which its pane prints
// and computes nothing of: no order, no count, no word, no mark. A
// workspace is a project of the login's list with an open run or a tab open
// in its folder; tabs in no project are the last one, Other. Rows are the
// agents panel of the workspace in front. What needs the person is never
// cut from Rows; what gives way, in this order: done rows, which are only
// ever the count Done; rows of a group that neither need him nor are in
// front, into one RowMore; fleets not in front, folded to their line.
type Side struct {
	Version int       `json:"version"`
	At      time.Time `json:"at"`
	// NeedYou counts what needs the person in every workspace together, and
	// First is the row a click on the count goes to.
	NeedYou    int         `json:"need_you"`
	First      *SideRow    `json:"first,omitempty"`
	Workspaces []Workspace `json:"workspaces"`
	// Title names the workspace in front. Count is set when rows were cut:
	// "200 agents · 3 need you". Hint is the one dim line of a workspace
	// with no job. Warn is the one line in the warning colour: "agent tool
	// changed: hermes".
	Title string    `json:"title"`
	Count string    `json:"count,omitempty"`
	Hint  string    `json:"hint,omitempty"`
	Warn  string    `json:"warn,omitempty"`
	Rows  []SideRow `json:"rows"`
	Done  int       `json:"done,omitempty"`
	Fresh Fresh     `json:"fresh"`
}

// Workspace is one row of the upper panel: the worst look among its agents
// (empty for none), its folder's name, with its parent folder too where two
// are named alike, its branch, and how many agents it has.
type Workspace struct {
	Root   string `json:"root,omitempty"`
	Name   string `json:"name"`
	Branch string `json:"branch,omitempty"`
	Look   Look   `json:"look,omitempty"`
	Agents int    `json:"agents"`
	Front  bool   `json:"front,omitempty"`
	Other  bool   `json:"other,omitempty"`
}

// The kinds of row in the agents panel: the lead agent (in Fleet Mode the
// orchestrator), a fleet, an agent the tool started, a tab the person
// opened there himself, and "· 3 more", which a click unfolds.
const RowLead, RowFleet, RowAgent, RowTab, RowMore = "lead", "fleet", "agent", "tab", "more"

// SideMarks are the marks of rows that have no look of their own, each with
// its form for a terminal that cannot show it: the lead agent's, a tab's, a
// fleet open and a fleet folded.
var SideMarks = map[string][2]string{RowLead: {"●", "*"}, RowTab: {"·", "."}, RowMore: {"·", "."}, "open": {"▾", "v"}, "folded": {"▸", ">"}}

// SideRow is one row of the agents panel. Depth is 0 for the lead agent, a
// fleet and, with Fleet Mode off, an agent; 1 for a fleet's worker. A
// fleet's row carries the worst look below it, its leader's Tool, its Agents
// of its Limit (Unlimited prints no limit) and whether it is Open or Parked.
// Root, Run and Task say what a click or an action is about, and Tab where
// a click goes: the agent's, the leader's or the lead agent's tab. Actions
// are ids of the action table offered on the row's mark; Item is the item
// "answer" is about, and ItemRun the run it is in where that is not Run: a
// fleet's worker whose question climbed is answered in the run above.
type SideRow struct {
	Kind    string   `json:"kind"`
	Depth   int      `json:"depth,omitempty"`
	Look    Look     `json:"look,omitempty"`
	Word    string   `json:"word,omitempty"`
	Name    string   `json:"name"`
	Tool    string   `json:"tool,omitempty"`
	Agents  int      `json:"agents,omitempty"`
	Limit   int      `json:"limit,omitempty"`
	Open    bool     `json:"open,omitempty"`
	Parked  bool     `json:"parked,omitempty"`
	More    int      `json:"more,omitempty"`
	Root    string   `json:"root,omitempty"`
	Run     string   `json:"run,omitempty"`
	Task    string   `json:"task,omitempty"`
	Tab     string   `json:"tab,omitempty"`
	Item    string   `json:"item,omitempty"`
	ItemRun string   `json:"item_run,omitempty"`
	Front   bool     `json:"front,omitempty"`
	Unseen  bool     `json:"unseen,omitempty"`
	Actions []string `json:"actions,omitempty"`
}

// Warned is the one line in the warning colour that every screen carries
// while changed, which is UIFile.Changed, holds a setting that runs a
// program: "agent tool changed: hermes", a tool named once however many of
// its keys changed, and "setting changed: shell" for any other.
func Warned(changed []string) string {
	var tools, rest, out []string
	for _, name := range changed {
		if part := strings.Split(name, "."); part[0] != StoreTools || len(part) < 2 {
			rest = append(rest, name)
		} else if !slices.Contains(tools, part[1]) {
			tools = append(tools, part[1])
		}
	}
	if len(tools) > 0 {
		out = append(out, "agent tool changed: "+strings.Join(tools, ", "))
	}
	if len(rest) > 0 {
		out = append(out, "setting changed: "+strings.Join(rest, ", "))
	}
	return strings.Join(out, " · ")
}

// noSide is the side model until the view package plugs its builder in: the
// place with its title and one line that says so.
func noSide(in SideInput) *Side {
	return &Side{Version: SideVersion, At: in.Now, Hint: "the left section is " + ErrNotBuilt.Error(),
		Fresh: Fresh{Checked: in.Checked}}
}
