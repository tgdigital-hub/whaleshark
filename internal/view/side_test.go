package view

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
)

// worded is a card with what the fixture's cards word and nothing else.
func worded(v *contract.View) (cards []contract.Card) {
	for _, s := range v.Sections {
		for _, c := range s.Cards {
			cards = append(cards, contract.Card{Task: c.Task, Name: c.Name, Look: c.Look, Colour: c.Colour, Agent: c.Agent, Tab: c.Tab, Try: c.Try, Fleet: c.Fleet, Item: c.Item})
		}
	}
	return cards
}

// The builders give back the morning with three fleets as it was written by
// hand from the pictures: the side model whole, and of the orchestrator's
// view each card and the one item in the fields the fixture words.
func TestTheMorningWithThreeFleets(t *testing.T) {
	f := testkit.Fleets()
	if got, want := indent(t, Side(f.In)), indent(t, f.Side); got != want {
		t.Errorf("the side model differs from the fixture's\n%s", diff(want, got))
	}
	v := Build(f.View)
	if got, want := indent(t, worded(v)), indent(t, f.Cards); got != want {
		t.Errorf("the cards differ from the fixture's\n%s", diff(want, got))
	}
	if len(v.Items) != 1 {
		t.Fatalf("%d items, want the one that climbed", len(v.Items))
	}
	it := v.Items[0]
	it.News, it.Buttons, it.Next = "", nil, nil
	if got, want := indent(t, it), indent(t, f.Item); got != want {
		t.Errorf("the item differs from the fixture's\n%s", diff(want, got))
	}
	if l := v.Limits; v.Mode != contract.ModeFleets || l == nil || *l != (contract.FleetRoom{Leaders: 3, LeadersMax: 4, Agents: 18, AgentsMax: 20}) || v.Counts.Agents != 18 {
		t.Errorf("the limits read %+v with %d agents", l, v.Counts.Agents)
	}
	want := []contract.ToolLine{{Name: "claude", Exact: true, Held: true, Proven: true, Suits: "the default; anything"}, {Name: "codex", Exact: true},
		{Name: "hermes", Suits: testkit.Hermes.Suits}}
	if !slices.Equal(v.Tools, want) {
		t.Errorf("the tools read %+v", v.Tools)
	}
	for _, width := range []int{100, 60} {
		for _, plain := range []bool{false, true} {
			name := fmt.Sprintf("status-fleets-%d%s.txt", width, map[bool]string{true: "-plain"}[plain])
			got := text(v, Options{Width: width, PlainMarks: plain})
			golden(t, name, got)
			for _, l := range strings.Split(got, "\n") {
				if cells(l) > width || l != strings.TrimRight(l, " ") || plain && strings.ContainsFunc(l, func(r rune) bool { return r > 127 && r != '·' }) {
					t.Errorf("%s: wider than the screen, ends in spaces or holds a mark that cannot be shown: %q", name, l)
				}
			}
		}
	}
}

// crowd is one project with n workers: under a lead agent, or under an
// orchestrator in fleets of forty at most. Every seventh has asked the
// person, every fifth has reported, every eleventh is done.
func crowd(n int, fleets bool) (contract.SideInput, contract.ViewInput) {
	now := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	root := filepath.Join(string(filepath.Separator)+"work", "crowd")
	snap := &contract.Snapshot{At: now}
	person := contract.PersonDefaults()
	person.Fleets.Agents, person.Fleets.Total = 40, contract.Unlimited
	state := func(id, pane string) *contract.State {
		s := &contract.State{Version: contract.StateVersion, Tasks: map[string]*contract.Task{}, Attempts: map[string]*contract.Attempt{}, Questions: map[string]*contract.Question{}}
		s.Run = contract.Run{ID: id, CreatedAt: now.Add(-time.Hour), Orchestrator: &contract.Binding{Pane: pane}}
		return s
	}
	work := func(s *contract.State, i int, id, name string) *contract.Attempt {
		pane := s.Run.ID + "-" + id
		t := &contract.Task{ID: id, Name: name, Check: "make test", Status: contract.TaskRunning, Attempts: []string{id + ".1"}, CreatedAt: now.Add(-time.Hour), SeenAt: now}
		a := &contract.Attempt{ID: id + ".1", Task: id, N: 1, State: contract.AttemptWorking, StateSince: now.Add(-time.Duration(i+1) * time.Minute), StartedAt: now.Add(-time.Hour),
			Agent: contract.Agent{Kind: "claude"}, Place: contract.Place{Tab: "t-" + pane, Pane: pane, Cwd: root, OpenedByUs: true}}
		s.Tasks[id], s.Attempts[a.ID] = t, a
		switch {
		case i%11 == 10:
			t.Status, t.SettledAt, a.State, a.EndedAt, a.Place = contract.TaskDone, now.Add(-time.Minute), contract.AttemptAccepted, now.Add(-time.Minute), contract.Place{}
			return a
		case i%7 == 6:
			a.State = contract.AttemptAsked
			s.Questions["q"+id] = &contract.Question{ID: "q" + id, Kind: contract.KindAsk, Form: contract.FormQuestion, For: contract.ForHuman, Task: id, Attempt: a.ID,
				Text: "Which way?", State: contract.QuestionOpen, CreatedAt: now.Add(-time.Minute)}
		case i%5 == 4:
			t.Status, a.State = contract.TaskReview, contract.AttemptReported
		}
		snap.Panes = append(snap.Panes, contract.Pane{ID: pane, Tab: a.Place.Tab, Label: name, Cwd: root, Agent: "claude", Status: contract.StatusWorking})
		return a
	}
	up := state("r1", "lead")
	snap.Panes = append(snap.Panes, contract.Pane{ID: "lead", Tab: "t-lead", Label: "lead", Cwd: root, Agent: "claude", Status: contract.StatusWorking})
	runs := []*contract.State{up}
	view := contract.ViewInput{Now: now, Caller: contract.Human, State: up, Terms: snap, Limits: contract.ProjectDefaults(), Checked: now,
		Swept: contract.Swept{Ran: true, At: now}, Watched: true, Person: person}
	for i := range n {
		if !fleets {
			work(up, i, fmt.Sprint("T", i+1), fmt.Sprint("job ", i+1))
			continue
		}
		at := i / 40
		name, group, id := fmt.Sprint("fleet ", at+1), fmt.Sprint("G", at+1), fmt.Sprint("r", at+2)
		if i%40 == 0 {
			a := work(up, 0, group, name)
			a.Leads, up.Tasks[group].Fleet, up.Tasks[group].Placement, up.Run.Mode = id, name, contract.PlaceFleet, contract.ModeFleets
			low := state(id, a.Place.Pane)
			low.Run.Parent = &contract.Twin{Run: "r1", Fleet: name}
			if up.Run.Fleets == nil {
				up.Run.Fleets, view.Fleets = map[string]*contract.Fleet{}, map[string]*contract.State{}
			}
			up.Run.Fleets[name], view.Fleets[name] = &contract.Fleet{Run: id, Tool: "claude", Pane: a.Place.Pane, Tab: a.Place.Tab, Groups: []string{group}}, low
			runs = append(runs, low)
		}
		work(runs[at+1], i, fmt.Sprint("T", i%40+1), fmt.Sprint("job ", i+1))
	}
	in := contract.SideInput{Now: now, Terms: snap, Person: person, Checked: now, Projects: []contract.SideProject{{Root: root, Branch: "main", Runs: runs}}}
	return in, view
}

// printed is the rows of a status text: each mark with the id beside it.
func printed(t *testing.T, v *contract.View) (rows []string) {
	t.Helper()
	body := text(v, Options{Width: 100})
	if at := strings.Index(body, "\nNEEDS YOU\n"); at >= 0 && len(v.Items) > 0 {
		body = body[at:]
	}
	for _, line := range strings.Split(body, "\n") {
		if f := strings.Fields(line); len(f) > 2 && strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "  ") {
			rows = append(rows, f[0]+" "+f[1])
		}
	}
	return rows
}

// marked is a view's cards as every printer must show them, in the view's
// order: each with the mark of its look.
func marked(v *contract.View) (rows []string) {
	for _, c := range worded(v) {
		rows = append(rows, contract.Looks[order[c.Look]].Mark+" "+c.Task)
	}
	return rows
}

// Rule 7 on two, twenty and two hundred agents, with fleets and without:
// the view's cards, the text of status, the same view through --json and
// the left section's rows are one list in one order with one mark each,
// and the bar's counts are the views'.
func TestThePrintersAgree(t *testing.T) {
	for _, n := range []int{2, 20, 200} {
		for _, fleets := range []bool{false, true} {
			in, vin := crowd(n, fleets)
			v, side := Build(vin), Side(in)
			back := new(contract.View)
			if err := json.Unmarshal([]byte(indent(t, v)), back); err != nil || indent(t, back) != indent(t, v) {
				t.Fatalf("%d agents: the view does not come back from --json as it went (%v)", n, err)
			}
			if got, want := printed(t, v), marked(v); !slices.Equal(got, want) {
				t.Errorf("%d agents: status prints\n%v\nthe view holds\n%v", n, got, want)
			}
			// The bar, run by run: a fleet's line is its card, and the rows
			// under it are the cards of the fleet's own run.
			bar, need := map[string][]string{}, 0
			for _, r := range side.Rows {
				switch r.Kind {
				case contract.RowFleet:
					c := cardOf(v, r.Task)
					if c.Fleet == nil || r.Agents != c.Fleet.Agents || r.Limit != 40 || r.Tool != c.Agent || r.Run != c.Fleet.Run || order[r.Look] > order[c.Look] {
						t.Errorf("%d agents: the fleet's row %+v is not its card %+v", n, r, c.Fleet)
					}
					bar["r1"] = append(bar["r1"], contract.Looks[order[c.Look]].Mark+" "+r.Task)
				case contract.RowAgent:
					if bar[r.Run] = append(bar[r.Run], contract.Looks[order[r.Look]].Mark+" "+r.Task); needs(r.Look) {
						need++
					}
				}
			}
			agents := v.Counts.Agents
			for _, low := range vin.Fleets {
				lin := vin
				lin.State, lin.Fleets = low, nil
				if got, want := bar[low.Run.ID], printed(t, Build(lin)); !slices.Equal(got, want) {
					t.Errorf("%d agents, run %s: the bar shows\n%v\nstatus prints\n%v", n, low.Run.ID, got, want)
				}
			}
			if got, want := bar["r1"], marked(v); !slices.Equal(got, want) {
				t.Errorf("%d agents: the bar shows\n%v\nthe view holds\n%v", n, got, want)
			}
			w := side.Workspaces[0]
			if w.Agents != agents+1 || side.NeedYou != need || (need == 0) != (n < 7) || side.Title != "crowd" || !w.Front || len(side.Workspaces) != 1 {
				t.Errorf("%d agents: the bar counts %d agents and %d that need you, the view %d and the rows %d: %+v", n, w.Agents, side.NeedYou, agents, need, w)
			}
			if n == 200 && (side.Done != 18 || need != 26 || side.First == nil || !needs(side.First.Look) || w.Look != contract.LookNeedsYou || side.Count != "") {
				t.Errorf("two hundred: %d done, %d need you, first %+v, the workspace %q", side.Done, need, side.First, w.Look)
			}
		}
	}
}

// names is the rows as a person reads them down the bar.
func names(rows []contract.SideRow) (out []string) {
	for _, r := range rows {
		name := strings.Repeat("  ", r.Depth) + r.Name
		if r.Kind == contract.RowFleet && !r.Open {
			name += " (folded)"
		}
		out = append(out, name)
	}
	return out
}

// Where the rows do not fit they give way in the order the contract words:
// the picture of the window with Fleet Mode on is the morning in thirteen
// rows, what needs the person and the row in front are never cut, and in a
// panel too short for a line a fleet the fleets not in front fold.
func TestRowsGiveWay(t *testing.T) {
	f := testkit.Fleets()
	f.In.Rows = 13
	side := Side(f.In)
	want := []string{"orchestrator", "billing", "  refund flow", "  invoice pdf", "  tax rounding", "  card retries", "  receipt mail",
		"search", "  search box", "  search results", "  filters", "  3 more", "help pages (folded)"}
	if got := names(side.Rows); !slices.Equal(got, want) || side.Count != "19 agents · 1 needs you" {
		t.Errorf("in thirteen rows, titled %q:\n%s\nwant\n%s", side.Count, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	f.In.Rows = 7
	want = []string{"orchestrator", "billing (folded)", "  refund flow", "search", "  search box", "  5 more", "help pages (folded)"}
	if got := names(Side(f.In).Rows); !slices.Equal(got, want) {
		t.Errorf("in seven rows:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	f.In.Rows, f.In.Folded = 0, []string{"r1/billing", "r1/search"}
	want = []string{"orchestrator", "billing (folded)", "  refund flow", "search (folded)", "help pages"}
	if got := names(Side(f.In).Rows)[:5]; !slices.Equal(got, want) {
		t.Errorf("a folded fleet keeps what waits for the person and nothing else:\n%s", strings.Join(got, "\n"))
	}
	for _, fleets := range []bool{false, true} {
		in, _ := crowd(200, fleets)
		whole := Side(in)
		for i := range in.Terms.Panes {
			in.Terms.Panes[i].Focused = in.Terms.Panes[i].Label == "job 130"
		}
		for _, limit := range []int{44, 60, 12} {
			in.Rows = limit
			side := Side(in)
			shown, more, must := 0, 0, 0
			for _, r := range side.Rows {
				switch {
				case r.Kind == contract.RowMore && r.More < 2:
					t.Errorf("a line that stands for one row: %+v", r)
				case r.Kind == contract.RowMore:
					more += r.More
				case r.Kind == contract.RowAgent:
					shown++
				}
				if r.Kind != contract.RowAgent && r.Kind != contract.RowMore || needs(r.Look) || r.Front {
					must++
				}
			}
			// What a fleet folded for want of room keeps to itself.
			folded := 0
			for _, r := range whole.Rows {
				if r.Depth == 1 && !needs(r.Look) && slices.ContainsFunc(side.Rows, func(x contract.SideRow) bool { return x.Kind == contract.RowFleet && x.Run == r.Run && !x.Open }) {
					folded++
				}
			}
			front := slices.ContainsFunc(side.Rows, func(r contract.SideRow) bool { return r.Front && r.Name == "job 130" })
			if shown+more+folded != 182 || side.NeedYou != 26 || !front || len(side.Rows) > max(limit, must+1) || side.Count != fmt.Sprint(whole.Workspaces[0].Agents, " agents · 26 need you") {
				t.Errorf("fleets %v, %d rows: %d shown, %d more, %d folded away, in front %v, %d rows of which %d must show, titled %q",
					fleets, limit, shown, more, folded, front, len(side.Rows), must, side.Count)
			}
			if need := slices.DeleteFunc(slices.Clone(side.Rows), func(r contract.SideRow) bool { return !needs(r.Look) || r.Kind != contract.RowAgent }); len(need) != 26 {
				t.Errorf("fleets %v, %d rows: %d rows that need the person, want every one of 26", fleets, limit, len(need))
			}
			if limit == 60 && len(side.Rows) != 60 {
				t.Errorf("fleets %v: %d rows where sixty fit", fleets, len(side.Rows))
			}
		}
	}
}

// The model for two hundred agents is built in under twenty milliseconds.
func TestTwoHundredInTime(t *testing.T) {
	for _, fleets := range []bool{false, true} {
		in, _ := crowd(200, fleets)
		in.Rows = 44
		best := time.Hour
		for range 10 {
			start := time.Now()
			Side(in)
			best = min(best, time.Since(start))
		}
		if t.Logf("fleets %v: %v", fleets, best); best > 20*time.Millisecond {
			t.Errorf("fleets %v: the model for two hundred agents took %v", fleets, best)
		}
	}
}

// What stands round the rows: a project with no job and its hint, two
// folders of one name, the tabs of no project and a tab of the person's own
// with a hostile name, what was typed, a parked fleet, a name that tries to
// write to the terminal, and no engine.
func TestTheSideRoundTheRows(t *testing.T) {
	f := testkit.Fleets()
	root := func(name ...string) string {
		return filepath.Join(append([]string{string(filepath.Separator) + "work"}, name...)...)
	}
	f.In.Projects = append(f.In.Projects, contract.SideProject{Root: root("old", "blog"), Branch: "ma\x1b[31min"}, contract.SideProject{Root: root("idle")})
	for i := range f.In.Terms.Panes {
		p := &f.In.Terms.Panes[i]
		switch p.Focused = p.Label == "notes"; p.Label {
		case "logs":
			p.Cwd, p.Agent, p.Label = root("old", "blog", "posts"), "claude", "my\x1b]0;x\aagent\nhere"
		}
	}
	f.In.Terms.Panes = append(f.In.Terms.Panes, contract.Pane{ID: "d", Dock: true, Focused: true, Cwd: root("idle")}, contract.Pane{ID: "o", Tab: "t-o", Ours: true, Cwd: root("idle")})
	side := Side(f.In)
	var spaces []string
	for _, w := range side.Workspaces {
		spaces = append(spaces, fmt.Sprint(w.Name, " ", w.Branch, " ", w.Look, " ", w.Agents, " ", w.Front))
	}
	want := []string{"shop-site main needs you 19 false", "work/blog drafts building 3 false", "old/blog ma[31min  1 false", "other tabs   1 true"}
	if !slices.Equal(spaces, want) || side.Title != "other tabs" || !slices.Equal(names(side.Rows), []string{"notes"}) || side.Rows[0].Kind != contract.RowTab || side.Hint != "" ||
		side.NeedYou != 1 || side.First.Name != "refund flow" {
		t.Errorf("the workspaces\n%s\nthe rows %v, titled %q, %d need you", strings.Join(spaces, "\n"), names(side.Rows), side.Title, side.NeedYou)
	}
	for i := range f.In.Terms.Panes {
		f.In.Terms.Panes[i].Focused = strings.HasSuffix(f.In.Terms.Panes[i].Label, "here")
	}
	side = Side(f.In)
	if r := side.Rows; len(r) != 1 || r[0].Name != "my]0;xagent here" || r[0].Look != "" || !r[0].Front || side.Title != "old/blog" || !strings.Contains(side.Hint, "no job yet") {
		t.Errorf("a tab of the person's own: %+v, hint %q", r, side.Hint)
	}

	f = testkit.Fleets()
	f.In.Find = "SEARCH"
	want = []string{"search", "  search box", "  search results", "  search help"}
	if got := names(Side(f.In).Rows); !slices.Equal(got, want) {
		t.Errorf("typed %q: %v, want %v", f.In.Find, got, want)
	}
	f.In.Find, f.In.Terms = "", nil
	up := f.In.Projects[0].Runs[0]
	up.Run.Fleets["search"].Pane, up.Run.Fleets["search"].Tab = "", ""
	up.Attempts["G2.1"].State, up.Attempts["G2.1"].Place, up.Tasks["G2"].Status = contract.AttemptStopped, contract.Place{}, contract.TaskReady
	for _, a := range f.In.Projects[0].Runs[2].Attempts {
		a.State, a.Place = contract.AttemptStopped, contract.Place{}
	}
	side = Side(f.In)
	at := slices.IndexFunc(side.Rows, func(r contract.SideRow) bool { return r.Name == "search" })
	if r := side.Rows[at]; !r.Parked || r.Look != contract.LookStopped || r.Agents != 0 || !slices.Equal(r.Actions, []string{"retry"}) || at != len(side.Rows)-1 ||
		side.Fresh.Notes[0] != "the engine is not running" || side.Workspaces[0].Agents != 12 || len(side.Workspaces) != 2 {
		t.Errorf("a parked fleet with no engine: %+v, %v, %+v", r, side.Fresh.Notes, side.Workspaces)
	}
	if v := Build(f.View); !strings.Contains(text(v, Options{Width: 200}), "claude · parked · 1 group open · 0 to check · 0 of 6 done") || *v.Limits != (contract.FleetRoom{Leaders: 2, LeadersMax: 4, Agents: 11, AgentsMax: 20}) {
		t.Errorf("a parked fleet in status: %+v\n%s", v.Limits, text(v, Options{Width: 200}))
	}
}

// fleetStore is the record of the morning: the orchestrator's run, which
// can be changed, and each fleet's.
type fleetStore struct {
	ledger
	runs []*contract.State
}

func (s fleetStore) Read(root, run string) (*contract.State, error) {
	for _, r := range s.runs {
		if r.Run.ID == run {
			return r, nil
		}
	}
	return nil, contract.ErrNoRun
}

// home is a login with a settings folder beside its state folder.
type home struct{ login }

func (h home) Dirs() (contract.Dirs, error) {
	return contract.Dirs{State: h.dir, Config: filepath.Join(h.dir, "config"), Cache: filepath.Join(h.dir, "cache")}, nil
}

// morning prepares a command in the orchestrator's run of the morning, in a
// login whose own file switches Fleet Mode on.
func morning(t *testing.T, mode, command string, args ...string) (*contract.Call, *bytes.Buffer) {
	t.Helper()
	f := testkit.Fleets()
	shop := f.In.Projects[0]
	c, out, _, _ := view(t, &testkit.Fixture{Now: f.In.Now, State: *shop.Runs[0], Terms: f.In.Terms}, command, contract.Human, args...)
	h := home{c.Kit.Platform.(login)}
	dirs, _ := h.Dirs()
	t.Setenv("APPDATA", dirs.Config)
	t.Setenv("LOCALAPPDATA", dirs.Cache)
	if err := os.MkdirAll(dirs.Config, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirs.Config, contract.ConfigFile), []byte("[fleets]\nmode = \""+mode+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := c.Kit.Store.(ledger)
	c.Kit.Platform, c.Kit.Store, c.Root = h, fleetStore{store, append([]*contract.State{store.s}, shop.Runs[1:]...)}, shop.Root
	return c, out
}

// status, show and catchup read the fleets' runs for an orchestrator's run:
// status prints the cards the builder gives from them, show a group with
// its fleet, catchup the item with the fleet it climbed from. With Fleet
// Mode switched off again the open job says that it goes on.
func TestFleetsInStatusShowAndCatchup(t *testing.T) {
	c, out := morning(t, contract.On, "status")
	result := run(t, c)
	v, ok := result.(*contract.View)
	if !ok || indent(t, worded(v)) != indent(t, testkit.Fleets().Cards) || len(v.Tools) != 2 || len(v.Alerts) != 0 {
		t.Fatalf("status gives %s", indent(t, result))
	}
	if got := out.String(); got != text(v, Options{Width: 100}) || !strings.Contains(got, "3 of 4 leaders · 18 of 20 agents") {
		t.Errorf("status prints\n%s", got)
	}
	c, out = morning(t, contract.Off, "status")
	if v := run(t, c).(*contract.View); len(v.Alerts) != 1 || !strings.HasPrefix(out.String(), "Fleet Mode is switched off") {
		t.Errorf("with the mode off again:\n%s", out)
	}
	c, out = morning(t, contract.On, "show", "G1")
	tv := run(t, c).(*contract.TaskView)
	if tv.Card.Fleet == nil || tv.Card.Fleet.Run != "r2" || len(tv.Items) != 1 || tv.Items[0].Fleet != "billing" {
		t.Errorf("show gives %s", indent(t, tv))
	}
	golden(t, "show-fleet-G1.txt", out.String())
	c, out = morning(t, contract.On, "catchup")
	if b := run(t, c).(*contract.Catchup); b.Waiting.Text != "passed up by the orchestrator: billing › refund flow" || b.StillBuilding != 2 {
		t.Errorf("catchup gives %+v\n%s", b, out)
	}
}

// A second group of a fleet is the same card until every card is asked
// for; a question a leader passed up for itself is titled by the leader.
func TestAFleetWithTwoGroups(t *testing.T) {
	f := testkit.Fleets()
	up := f.View.State
	g := *up.Tasks["G2"]
	g.ID, g.Name, g.Attempts, g.Status = "G7", "search again", nil, contract.TaskReady
	up.Tasks["G7"] = &g
	up.Run.Fleets["search"].Groups = append(up.Run.Fleets["search"].Groups, "G7")
	up.Questions["q1"].Down.ID = "q9"
	v := Build(f.View)
	if c, _ := card(t, v, "G2"); len(worded(v)) != 3 || c.Fleet.Groups != 2 || v.Items[0].Name != "leader" || v.Items[0].Title() != "billing › leader" {
		t.Errorf("two groups of one fleet: %s", indent(t, worded(v)))
	}
	f.View.All = true
	v = Build(f.View)
	if c, _ := card(t, v, "G7"); len(worded(v)) != 4 || c.Fleet == nil || c.Name != "search" || c.Look != contract.LookQueued {
		t.Errorf("every card of two groups: %s", indent(t, worded(v)))
	}
	if rows := Side(f.In).Rows; len(rows) != 15 {
		t.Errorf("the bar with two groups of one fleet: %v", names(rows))
	}
}

// A fleet the person keeps has a row in the left section while it has no
// work, worded "saved", and a line in status either way; a saved fleet that
// is at work has its own row and no second one.
func TestASavedFleetHasItsRowWithNoWork(t *testing.T) {
	f := testkit.Fleets()
	saved := []contract.SavedFleet{{Name: "billing", Purpose: "what customers pay"}, {Name: "ops portal", Purpose: "everything of the ops\x1b[31m portal"}}
	f.In.Projects[0].Saved = saved
	var rows []string
	for _, r := range Side(f.In).Rows {
		if r.Kind == contract.RowFleet {
			rows = append(rows, r.Name+"="+r.Word)
		}
	}
	if got := strings.Join(rows, " "); got != "billing= search= help pages= ops portal=saved" {
		t.Errorf("the fleets' rows are %q", got)
	}
	f.View.Saved = saved
	v := Build(f.View)
	if len(v.Saved) != 2 || !v.Saved[0].Busy || v.Saved[1].Busy || strings.ContainsRune(v.Saved[1].Purpose, 0x1b) {
		t.Errorf("status holds the saved fleets as %+v", v.Saved)
	}
	var out strings.Builder
	Text(&out, v, Options{Width: 100})
	for _, line := range []string{"saved fleet billing · at work · what customers pay", "saved fleet ops portal · no work now · everything of the ops"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("status lacks %q:\n%s", line, out.String())
		}
	}
}
