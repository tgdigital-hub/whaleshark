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
	SideVersion = 2
	SideTitle   = "WHALESHARK"
	Bottom, Top = "bottom", "top"
	// SideSplit is where the agents begin when nothing is said, in
	// hundredths of the section's height, and what a person may set. The
	// heavier line is held so that the upper panel keeps its title and
	// SpaceRows rows, and the lower its title and AgentRows.
	SideSplit, SideSplitMin, SideSplitMax = 50, 20, 80
	SpaceRows, AgentRows                  = 2, 4
	// SideHead and SideFoot are the rows over the upper panel's title (the
	// section's own title and a blank) and under the agents panel (the two
	// buttons and the line of how fresh it is); Warn and NewVersion, where
	// set, are one more row of the foot each.
	SideHead, SideFoot = 2, 2
)

// SplitRow is the row of the left section the heavier line stands on, from
// 0 at its top: split hundredths of height rows down (0 is SideSplit), held
// between the two panels' least. above is how many rows stand over the
// upper panel's title and below how many under the agents panel (SideHead
// and SideFoot). It is a place and not a remainder: the same whatever the
// number of workspaces. The pane draws by it and a drag is held to it. The
// upper panel then has SplitRow-above-1 rows for workspaces, with a line
// between every two: (rows+1)/2 of them fit, and where there are more the
// last of those rows is "· N more" and the one in front keeps its row.
func SplitRow(height, split, above, below int) int {
	if split == 0 {
		split = SideSplit
	}
	split = min(max(split, SideSplitMin), SideSplitMax)
	least, most := above+1+SpaceRows, height-below-1-AgentRows-1
	return max(min(height*split/100, most), least)
}

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
// rows the agents panel has, 0 for no limit; Height is the section's rows
// and Split UIFile.SideSplit, from which the builder says where the heavier
// line stands and how many workspaces the upper panel has rows for (with
// Height 0 nothing is cut and Side.Split is 0); Gone names the workspaces'
// folders that are no longer on the disk, which the caller looked for,
// since the builder reads nothing; Folded names the fleets the
// person folded, as "run/fleet"; Find is what was typed to narrow the rows;
// Changed is UIFile.Changed, which Warn is worded from. UI is ui.json, for
// the first steps and what an agent changed; NewVersion is VersionLine's
// answer, which the caller reads, since the builder reads nothing.
type SideInput struct {
	Now        time.Time
	Projects   []SideProject
	Terms      *Snapshot
	Tools      []Tool
	Person     PersonConfig
	Rows       int
	Folded     []string
	Find       string
	Changed    []string
	Checked    time.Time
	Notes      []string
	UI         UIFile
	NewVersion string
	Height     int
	Split      int
	Gone       []string
}

// Side is the one description of the left section, which its pane prints
// and computes nothing of: no order, no count, no word, no mark. Since
// phase 7 the workspaces are the keeper's (Terms.Spaces, in their order,
// the tabs of no project last) and a tab's workspace is Pane.Workspace: the
// builder asks no folder. A run is shown in the workspace its lead agent's
// tab is of, and one with no such tab in the workspace whose Root is its
// project's; a project with an open run that is shown nowhere has no row
// and is in Closed. Rows are the agents panel of the workspace in front. What needs the person is never
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
	// changed: hermes", and after it what an agent changed (AgentChanged).
	// Steps is which of FirstSteps are ticked, a bit each from the first,
	// and StepsShown whether they are drawn: the lit one is the first not
	// ticked. NewVersion is the dim line at the foot, empty for none.
	Title      string    `json:"title"`
	Count      string    `json:"count,omitempty"`
	Hint       string    `json:"hint,omitempty"`
	Warn       string    `json:"warn,omitempty"`
	Rows       []SideRow `json:"rows"`
	Done       int       `json:"done,omitempty"`
	Fresh      Fresh     `json:"fresh"`
	Steps      int       `json:"steps,omitempty"`
	StepsShown bool      `json:"steps_shown,omitempty"`
	NewVersion string    `json:"new_version,omitempty"`
	// More is how many workspaces the upper panel has no row for: its last
	// row then reads "· 2 more", and the one in front always has a row.
	// Split is the row the heavier line stands on (SplitRow). Job is the one
	// dim line of a workspace opened on an open job nobody is bound to
	// (JobOpenHere), or NoFolder. Marks is what the pane hands the keeper
	// with Terminals.Marks: by tab, how many things wait for the person
	// with the agent of that tab, in every workspace. Closed is what [+] lists:
	// the login's projects that have no workspace and no job shown in
	// another's, those with an open job first, each with Word as
	// "job open · 4 stopped".
	More   int            `json:"more,omitempty"`
	Split  int            `json:"split"`
	Job    string         `json:"job,omitempty"`
	Marks  map[string]int `json:"marks,omitempty"`
	Closed []Workspace    `json:"closed,omitempty"`
}

// Workspace is one row of the upper panel: its name as the keeper has it
// (Space.Name: the folder's, or the one a person gave), its branch, and at
// its right Need, what waits for the person there (needs you, at a prompt),
// then how many agents it has, or Tabs where it has no job. It carries no
// mark at its left; Look, the worst among its agents, stays for the rail
// alone. ID is the Space's and Tab where a click goes: the tab it shows.
// Offer is its menu's rows, ids of the action table (OfferRows). Gone says
// its folder is no longer on the disk: the row is dimmed and can be removed.
// Word is set in Side.Closed only.
type Workspace struct {
	Root   string   `json:"root,omitempty"`
	Name   string   `json:"name"`
	Branch string   `json:"branch,omitempty"`
	Look   Look     `json:"look,omitempty"`
	Agents int      `json:"agents"`
	Front  bool     `json:"front,omitempty"`
	Other  bool     `json:"other,omitempty"`
	ID     string   `json:"id,omitempty"`
	Need   int      `json:"need,omitempty"`
	Tab    string   `json:"tab,omitempty"`
	Tabs   int      `json:"tabs,omitempty"`
	Offer  []string `json:"offer,omitempty"`
	Gone   bool     `json:"gone,omitempty"`
	Word   string   `json:"word,omitempty"`
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
// a click goes: the agent's, the leader's or the lead agent's tab. Offer is
// the first block of the row's menu (OfferRows, by the row's kind and
// whether its agent is at work) and Actions what its work offers, which
// follow under a line in the same menu; both are ids of the action table,
// and a menu opened on the tab row holds Offer alone. Item is the item
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
	Offer   []string `json:"offer,omitempty"`
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
