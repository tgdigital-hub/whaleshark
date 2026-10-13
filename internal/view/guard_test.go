package view

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
)

// guardedView is the afternoon with every guard on, and with the two items
// its record does not hold yet: the job held at its limit, and a stuck
// worker that reached the person.
func guardedView(by contract.CallerKind) (testkit.Guards, contract.ViewInput) {
	g := testkit.Guarded()
	g.Up.Run.Spend.Held = true
	for id, q := range map[string]*contract.Question{
		"n4": {Form: contract.FormChoice, Cause: contract.CauseBudget, Urgent: true, Text: "This job has used its 40 agent-hours",
			Set: &contract.SetAsk{Key: "spend.job", From: "40", To: "48"}}, // as Rules.Spend raises it: the grant is what its button offers
		"n5": {Form: contract.FormTodo, Cause: contract.CauseStuck, Task: "G1", Attempt: "T7.1", Fleet: "billing", Text: "billing › sign-up page looks stuck for 82m: no progress note · the same check failed 3 times. Its leader and the orchestrator were told."},
	} {
		q.ID, q.Kind, q.For, q.From, q.State, q.CreatedAt = id, contract.KindNeed, contract.ForHuman, contract.FromTool, contract.QuestionOpen, g.Now.Add(-time.Minute)
		g.Up.Questions[id] = q
	}
	return g, contract.ViewInput{Now: g.Now, Caller: by, State: g.Up, Fleets: g.Low, Person: g.Person, Limits: contract.ProjectDefaults(), Checked: g.Now,
		Swept: contract.Swept{Ran: true, At: g.Now}, Watched: true, UI: contract.UIFile{Changed: []string{"mute"}, ByAgent: []contract.SetAsk{{Key: "theme", From: "reef", To: "dusk"}}}}
}

// The items of the five causes are worded once: the view holds their text
// and their buttons, and the plain text, the view through --json and the
// left section show the same items with the same marks.
func TestTheGuardsItemsOnEveryPrinter(t *testing.T) {
	g, in := guardedView(contract.Human)
	v := Build(in)
	want := map[string]struct {
		lines   int
		text    string
		buttons []string
	}{
		"n1": {1, `Plan for "the October list" · 1 more thing for help pages`, []string{"Approve=approve", "Send back=send back:", "Open the plan>open-plan"}},
		"n2": {4, "Protected files changed in this job\ndb/migrations/0042_refunds.sql\n.env.example\nThe job cannot be landed until you approve these 2 files as they are now.",
			[]string{"Approve=approve", "Send back=send back:", "Show the changes>show-changes"}},
		"n3": {3, "Your agent wants to change a setting\nSpending guard:  on → off\nOff: a job runs until it is done.", []string{"Change it=change", "Leave it=leave"}},
		"n4": {3, "This job has used its 40 agent-hours\n6 agents are held where they stand; those with nothing in hand were closed.\nNothing is lost. Nothing more is started until you say.",
			[]string{"8 more agent-hours=more", "No limit for this job=no limit", "Leave it held=leave"}},
		"n5": {1, "billing › sign-up page looks stuck for 82m", []string{"Go there>go", "Note to its leader>note-leader"}},
	}
	body := text(v, Options{Width: 100})
	for id, w := range want {
		it := item(t, v.Items, id)
		var buttons []string
		for _, b := range it.Buttons {
			if _, ok := contract.FindAction(b.Action); b.Action != "" && !ok {
				t.Errorf("%s offers %q, which is no row of the action table", id, b.Action)
			}
			buttons = append(buttons, b.Label+strings.TrimSuffix("="+b.Answer, "=")+strings.TrimSuffix(">"+b.Action, ">"))
		}
		first, _, _ := strings.Cut(it.Text, "\n")
		if !strings.HasPrefix(it.Text, w.text) || strings.Count(it.Text, "\n")+1 != w.lines || !slices.Equal(buttons, w.buttons) || it.News != news(first) || it.Source != "from the tool itself" {
			t.Errorf("%s reads\n%s\nwith %q and the news %q", id, it.Text, buttons, it.News)
		}
		// One row in the text, with the first line and the line to type.
		if !strings.Contains(body, " ▲ "+id+" ") || !strings.Contains(body, strings.Fields(it.News)[0]) || !strings.Contains(body, strings.TrimPrefix(it.Next[0], "whaleshark ")) {
			t.Errorf("the text does not show %s with %q and %q:\n%s", id, it.News, it.Next, body)
		}
	}
	// A worker of the job's own run can be stopped from its item.
	in.State.Questions["n5"].Fleet = ""
	if it := item(t, Build(in).Items, "n5"); len(it.Buttons) != 3 || it.Buttons[1].Action != "stop" {
		t.Errorf("a stuck worker of the job itself has the buttons %+v", it.Buttons)
	}
	if it := item(t, v.Items, "n4"); !it.Urgent || it.Next[1] != `whaleshark answer n4 "no limit" --human` {
		t.Errorf("the budget's item: %+v", it)
	}
	// A fleet held alone counts its own agents, not the job's.
	in.State.Questions["n4"].Task = "G1"
	if it := item(t, Build(in).Items, "n4"); !strings.Contains(it.Text, "\n1 agent is held where they stand") {
		t.Errorf("the item of a fleet held alone: %q", it.Text)
	}
	in.State.Questions["n4"].Task = ""
	if it := item(t, v.Items, "n3"); !strings.HasSuffix(it.Text, must(t, "spend.guard").Help) {
		t.Errorf("a setting's item ends with the setting's own description: %q", it.Text)
	}
	back := new(contract.View)
	if err := json.Unmarshal([]byte(indent(t, v)), back); err != nil || indent(t, back) != indent(t, v) {
		t.Fatalf("the view does not come back from --json as it went (%v)", err)
	}
	if v.Spent != 33 || v.Limit != 40 {
		t.Errorf("the figures of the spending guard: %v of %d", v.Spent, v.Limit)
	}
	if !slices.Contains(v.Lines, "spent: 33 of 40 agent-hours · held") || !slices.Contains(v.Alerts, "setting changed by your agent: theme") || !slices.Contains(v.Alerts, "setting changed: mute") ||
		!strings.Contains(body, "spent: 33 of 40 agent-hours · held") || !strings.Contains(body, "setting changed by your agent: theme") {
		t.Errorf("the lines %q and the alerts %q, and the text:\n%s", v.Lines, v.Alerts, body)
	}
	// The held job's item is urgent and stands first, older ones under it.
	if got, want := printed(t, v), append([]string{"▲ n4", "▲ n1", "▲ n2", "▲ n3", "▲ n5"}, marked(v)...); !slices.Equal(got, want) {
		t.Errorf("status prints\n%v\nthe view holds\n%v", got, want)
	}
	side := Side(contract.SideInput{Now: g.Now, Person: g.Person, Checked: g.Now, UI: in.UI, Changed: []string{"tools.hermes.run"}, NewVersion: "version 1.3 is out\x1b[2J", Projects: []contract.SideProject{{Root: "shop", Runs: []*contract.State{g.Up, g.Low["billing"], g.Low["search"]}}}})
	if side.Warn != "agent tool changed: hermes · setting changed by your agent: theme" || side.NewVersion != clean("version 1.3 is out\x1b[2J") || strings.Contains(side.NewVersion, "\x1b") {
		t.Errorf("the left section warns %q and says of a version %q", side.Warn, side.NewVersion)
	}
	for _, r := range side.Rows {
		if c := cardOf(v, r.Task); r.Kind == contract.RowFleet && (c == nil || r.Look != c.Look) {
			t.Errorf("the bar's row %+v is not the view's card %+v", r, c)
		}
	}

	// To an agent the items are the person's; a tool that tells nothing of
	// what was asked for still gets an item in the tool's own words.
	in.Caller = contract.Orchestrator
	g.Up.Questions["n3"].Set.From = "on\x1b]0;x\a"
	for _, it := range Build(in).Items {
		if len(it.Next) != 1 || it.Next[0] != forHuman || strings.ContainsRune(it.Text, '\x1b') {
			t.Errorf("%s for an agent: %q %q", it.ID, it.Next, it.Text)
		}
	}
}

func must(t *testing.T, name string) *contract.Key {
	t.Helper()
	key, _ := contract.FindKey(name)
	if key == nil {
		t.Fatalf("no key %s", name)
	}
	return key
}

// With every guard as it comes nothing of phase 6 shows but the stuck line;
// with the detector off not that either.
func TestGuardsOffChangeNothing(t *testing.T) {
	g, in := guardedView(contract.Human)
	in.Person, in.UI = contract.PersonDefaults(), contract.UIFile{}
	if v := Build(in); len(v.Lines) != 0 || strings.Contains(strings.Join(v.Alerts, " "), "your agent") {
		t.Errorf("with the guard off: lines %q, alerts %q", v.Lines, v.Alerts)
	}
	lin := in
	lin.State, lin.Fleets, lin.Caller = g.Low["billing"], nil, contract.Leader
	c, _ := card(t, Build(lin), "T7")
	if c.News != "stuck 132m: no progress note · the same check failed 3 times" || !c.Stuck || c.Look != contract.LookBuilding || len(c.Next) != 3 || c.Next[0] != "whaleshark show T7 --screen" {
		t.Errorf("the stuck card: %q, %s, %q", c.News, c.Look, c.Next)
	}
	if body := text(Build(lin), Options{Width: 120}); !strings.Contains(body, c.News) || strings.Contains(body, "last said") && strings.Contains(body, `"stuck`) {
		t.Errorf("the text of a stuck card:\n%s", body)
	}
	if v := Build(lin); len(v.Lines) != 0 {
		t.Errorf("a fleet's own run has no line of the job's spending: %q", v.Lines)
	}
	lin.Person.Stuck.Watch = false
	if c, _ := card(t, Build(lin), "T7"); c.News != "wiring the form" || c.Stuck {
		t.Errorf("with the detector off the card says %q", c.News)
	}
}

// What looked stuck while the person was away is a line of the catch-up,
// as the guides promise: a fleet's worker by its fleet, and what became of it.
func TestCatchupSaysWhatLookedStuck(t *testing.T) {
	g, in := guardedView(contract.Human)
	block := func() string {
		c := away(Build(in), g.Up, g.Low, g.Up.Inbox.Events, in.Limits, g.Now.Add(-2*time.Hour))
		var out strings.Builder
		catchText(&out, c, 120)
		return out.String()
	}
	if got := block(); !strings.Contains(got, " Looked stuck          1   billing › sign-up page; still stuck\n") {
		t.Errorf("a worker still stuck:\n%s", got)
	}
	g.Low["billing"].Attempts["T7.1"].Seen.StuckSince = time.Time{}
	if got := block(); !strings.Contains(got, "billing › sign-up page; no longer stuck\n") {
		t.Errorf("a worker that went on:\n%s", got)
	}
	g.Up.Inbox.Events = nil
	if got := block(); strings.Contains(got, "Looked stuck") {
		t.Errorf("nothing was stuck, and the block has a row for it:\n%s", got)
	}
}

// The plan as its item shows it: the picture of the design for three fleets,
// what was added since an approval, a job without fleets, and forty lines.
func TestPlanText(t *testing.T) {
	g := testkit.Guarded()
	if got, want := PlanText(g.Up, g.Low, []string{"G3"}, g.Person), g.Up.Questions["n1"].Text+"\n  help pages   claude   1 thing   up to 8 agents"; got != want {
		t.Errorf("what was added since:\n%s\nwant\n%s", got, want)
	}
	f := testkit.Fleets()
	person := f.View.Person
	person.Fleets.Agents, person.Fleets.Total = 8, 20
	up := f.View.State
	up.Run.Objective = "the October list"
	up.Tasks["G4"] = &contract.Task{ID: "G4", Name: "refunds", Fleet: "billing"}
	want := `Plan for "the October list" · 3 fleets · 4 things · up to 27 agents (your limit: 20)
  billing      codex    2 things   up to 8 agents   · codex is not held by Stop all
  search       claude   1 thing    up to 8 agents
  help pages   hermes   1 thing    up to 8 agents   · hermes is not held by Stop all`
	if got := PlanText(up, f.View.Fleets, []string{"G1", "G2", "G3", "G4", "G9"}, person); got != want {
		t.Errorf("three fleets:\n%s\nwant\n%s", got, want)
	}
	person.Fleets.Agents, person.Fleets.Total = contract.Unlimited, contract.Unlimited
	if got := PlanText(up, nil, []string{"G2"}, person); got != "Plan for \"the October list\" · 1 fleet · 1 thing · no limit on agents (your limit: unlimited)\n  search   claude   1 thing   no limit on agents" {
		t.Errorf("with no limit:\n%s", got)
	}

	job := &contract.State{Run: contract.Run{Objective: "add\x1b]0;x\a login"}, Tasks: map[string]*contract.Task{
		"T1": {ID: "T1", Name: "form", Status: contract.TaskDone}, "T2": {ID: "T2", Name: "session\nstore", Agent: "codex", Model: "big", After: []string{"T1"}},
		"T3": {ID: "T3", Name: "sign-out", After: []string{"T1", "T2"}}}}
	want = `Plan for "add]0;x login" · 3 tasks · 3 agents (claude, codex)
  form            claude
  session store   codex big
  sign-out        claude   after T2`
	if got := PlanText(job, nil, []string{"T1", "T2", "T3"}, person); got != want || strings.ContainsAny(got, "\x1b\a") {
		t.Errorf("without fleets:\n%s\nwant\n%s", got, want)
	}
	var ids []string
	for i := range 60 {
		id := fmt.Sprint("T", i+10)
		job.Tasks[id], ids = &contract.Task{ID: id, Name: "task " + id}, append(ids, id)
	}
	job.Run.Plan = &contract.Approval{Approved: []string{"T1"}}
	lines := strings.Split(PlanText(job, nil, ids, person), "\n")
	if len(lines) != 40 || lines[0] != `Plan for "add]0;x login" · 60 more tasks · 60 agents (claude)` || lines[39] != "  … and 22 more: whaleshark graph" {
		t.Errorf("sixty tasks are %d lines, from %q to %q", len(lines), lines[0], lines[len(lines)-1])
	}
	k := contract.NewKit()
	if Plug(k); k.PlanText(job, nil, ids[:1], person) != `Plan for "add]0;x login" · 1 more task · 1 agent (claude)`+"\n  task T10   claude" {
		t.Errorf("the kit's plan text is not the view's")
	}
}

// exported runs log --export on the morning of three fleets, which a person
// and the orchestrator have acted in, and returns what it wrote.
func exported(t *testing.T, by contract.CallerKind, json bool, args ...string) (*contract.Call, string, any, error) {
	t.Helper()
	c, out := morning(t, contract.On, "log", args...)
	c.Flags, c.Caller.Kind, c.JSON = map[string][]string{"export": {""}}, by, json
	c.Now = c.Now.Add(7 * time.Hour)
	at := func(m int) time.Time { return c.Now.Add(time.Duration(m-400) * time.Minute) }
	store := c.Kit.Store.(fleetStore)
	up, billing := store.runs[0], store.runs[1]
	q := up.Questions["q1"]
	q.State, q.Answer, q.AnsweredAt, q.AnsweredBy = contract.QuestionUsed, "Card", at(4), &contract.Origin{Caller: contract.Human, Where: contract.WherePane}
	up.Questions["n1"] = &contract.Question{ID: "n1", Kind: contract.KindNeed, Form: contract.FormSignoff, For: contract.ForHuman, From: contract.FromLead, Text: "Ship the\x1b[2J refunds?",
		State: contract.QuestionUsed, CreatedAt: at(20), Answer: "approve", AnsweredAt: at(25), AnsweredBy: &contract.Origin{Caller: contract.Orchestrator, Where: contract.WhereRelayed},
		Earlier: []contract.Undone{{Answer: "send back: not yet", At: at(22), By: &contract.Origin{Caller: contract.Human, Where: contract.WherePhone}}}}
	a := billing.Attempts["T2.1"]
	a.Report = &contract.Report{Outcome: contract.ReportDone, Summary: "All five refund cases pass.", At: at(30)}
	a.Check = &contract.CheckResult{OK: true, At: at(33), Tail: "SECRET-IN-A-CHECK-LOG"}
	a.Mail = []contract.Mail{{Seq: 1, At: at(10), Text: "A-MESSAGE-TO-AN-AGENT"}}
	a.Progress = &contract.Progress{Pct: 40, Note: "A-PROGRESS-NOTE", At: at(12)}
	billing.Tasks["T2"].Brief = "A-BRIEF"
	billing.Attempts["T3.1"].State, billing.Attempts["T3.1"].EndedAt = contract.AttemptAccepted, at(100)
	// What the front writes when a person runs a command: the deed, on what, from where.
	store.past = []contract.Event{
		{Seq: 1, At: at(1), Kind: "human", Text: "The person ran run new themselves.", Data: map[string]any{"what": "run new", "where": contract.WhereTyped}},
		{Seq: 2, At: at(40), Kind: "human", Text: "The person ran accept themselves.", Data: map[string]any{"what": "accept", "where": contract.WherePage, "on": "G1"}},
		{Seq: 3, At: at(41), Kind: "protected", Task: "G1", Text: "G1 changed protected files: db/migrations/0042_refunds.sql"},
	}
	c.Kit.Store, c.Kit.Platform = jobPast{store}, private{c.Kit.Platform.(home)}
	result, err := c.Kit.Handler("log")(c)
	return c, out.String(), result, err
}

// A leader that holds two groups is one agent in one tab: the log lists and
// counts it once. Seen with real agents, where one leader stood eight times.
func TestExportListsALeaderOnce(t *testing.T) {
	c, out := morning(t, contract.On, "log", "-")
	c.Flags, c.Caller.Kind = map[string][]string{"export": {""}}, contract.Human
	up := c.Kit.Store.(fleetStore).runs[0]
	for id, a := range up.Attempts {
		if t := up.Tasks[a.Task]; a.Leads != "" && t.Fleet == "billing" {
			second, task := *a, *t
			second.ID, second.Task, task.ID = "G9.1", "G9", "G9"
			up.Attempts["G9.1"], up.Tasks["G9"] = &second, &task
			_ = id
			break
		}
	}
	if up.Attempts["G9.1"] == nil {
		t.Fatal("the morning has no leader of billing")
	}
	if _, err := c.Kit.Handler("log")(c); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); strings.Count(got, "  billing           fleet leader") != 1 || !strings.Contains(got, " · 18 agents · ") {
		t.Errorf("a leader with two groups in the log:\n%s", got)
	}
}

// The export of the morning of three fleets, part by part as the design's
// picture has it, and line for line as the file kept here.
func TestExportOfThreeFleets(t *testing.T) {
	_, got, _, err := exported(t, contract.Human, false, "-")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "export-three-fleets.txt", got)
	lines := strings.Split(got, "\n")
	picture := []string{`WhaleShark log · project shop-site · job r1 "" · 12 Oct 2026, `, "Exported 12 Oct 2026 17:20. This file holds what your agents wrote: read it before you send it on.", "", "WHO", "  orchestrator "}
	for i, want := range picture {
		if !strings.HasPrefix(lines[i], want) || want == "" && lines[i] != "" {
			t.Errorf("line %d is %q, the picture's begins %q", i+1, lines[i], want)
		}
	}
	// The four parts in their order, every line of a part indented under it.
	var parts []string
	for _, l := range lines[3:] {
		if l != "" && !strings.HasPrefix(l, "  ") {
			parts = append(parts, l)
		}
	}
	if !slices.Equal(parts, []string{"WHO", "WHAT HAPPENED", "WHAT YOU APPROVED", "WHAT IT USED"}) {
		t.Errorf("the parts are %q", parts)
	}
	for _, want := range []string{
		`  billing/T2.1      worker · codex · tab "t-`, "  billing           fleet leader · codex · tab", "  10:41  you (typed)                  run new\n", `you (pane)`, `answered G1: "Card"`,
		`billing/T2`, `reported done: "All five refund cases pass."`, "  11:20  you (page)                   accept G1\n", `whaleshark`, `protected G1: "G1 changed protected files: db/migrations/0042_refunds.sql"`,
		`orchestrator, in your name`, `answered: "approve"`, `you (phone)`, `answered: "send back: not yet" (taken back)`, "check passed billing/T2\n", `  11:00  orchestrator                 sign-off: "ship the[2J refunds?"`,
		"(5: 4 by you, 1 passed on in your name)", "  139 agent-hours · 18 agents · longest: billing, 8h 00m\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the export lacks %q:\n%s", want, got)
		}
	}
	// What agents were told and what a check printed are not in it, nor anything that moves a terminal.
	for _, never := range []string{"SECRET-IN-A-CHECK-LOG", "A-MESSAGE-TO-AN-AGENT", "A-PROGRESS-NOTE", "A-BRIEF", "\x1b"} {
		if strings.Contains(got, never) {
			t.Errorf("the export holds %q", never)
		}
	}
}

// The file: a new one in the login's own state folder, the login's alone,
// its path printed; one the person names, never replaced; the same as one
// document with --json; every job with --all, a fleet's run inside its job.
func TestExportFiles(t *testing.T) {
	c, printed, result, err := exported(t, contract.Human, false)
	dirs, _ := c.Kit.Platform.Dirs()
	path := strings.TrimSpace(printed)
	if err != nil || filepath.Dir(path) != filepath.Join(dirs.State, "export") || filepath.Base(path) != filepath.Base(c.Root)+"-r1-2026-10-12-172000.txt" || result.(map[string]any)["path"] != path {
		t.Fatalf("exported to %q (%v)", printed, err)
	}
	body, err := os.ReadFile(path)
	_, screen, _, _ := exported(t, contract.Human, false, "-")
	if err != nil || string(body) != screen {
		t.Errorf("the file is not what the screen gets (%v)", err)
	}
	if info, err := os.Stat(path); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("the file's mode is %v (%v)", info.Mode(), err)
	}

	named := filepath.Join(t.TempDir(), "log.json")
	if _, _, _, err := exported(t, contract.Human, true, named); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Jobs []struct {
			Run      string
			Who      []entry
			Happened []entry
			Approved []entry
			Relayed  int
			Agents   int
			Hours    float64 `json:"agent_hours"`
		}
	}
	body, _ = os.ReadFile(named)
	if err := json.Unmarshal(body, &doc); err != nil || len(doc.Jobs) != 1 || doc.Jobs[0].Run != "r1" || len(doc.Jobs[0].Who) != 19 || len(doc.Jobs[0].Approved) != 5 ||
		doc.Jobs[0].Relayed != 1 || doc.Jobs[0].Agents != 18 || doc.Jobs[0].Hours <= 0 || len(doc.Jobs[0].Happened) < 20 {
		t.Errorf("the document (%v): %.300s", err, body)
	}
	_, _, _, err = exported(t, contract.Human, false, named)
	var r *contract.Refusal
	if after, _ := os.ReadFile(named); !errors.As(err, &r) || r.Exit != contract.ExitRefused || r.Code != "not_written" || string(after) != string(body) {
		t.Errorf("a file that is there: %v", err)
	}
	if _, _, _, err = exported(t, contract.Human, false, "one", "two"); !errors.As(err, &r) || r.Exit != contract.ExitUsage {
		t.Errorf("two files: %v", err)
	}

	// Every job of the project: the fleets' runs are in their job and are no jobs of their own.
	c, out := morning(t, contract.On, "log", "-")
	c.Flags = map[string][]string{"export": {""}, "all": {""}}
	c.Kit.Store = everyRun{c.Kit.Store.(fleetStore)}
	c.Kit.Platform = private{c.Kit.Platform.(home)}
	if run(t, c); strings.Count(out.String(), "WhaleShark log · ") != 1 || !strings.Contains(out.String(), "  search/T1.1 ") {
		t.Errorf("every job:\n%s", out)
	}
}

// jobPast is the morning's record with its history in the job's run alone;
// private is its login, where a file can be made the login's own.
type jobPast struct{ fleetStore }

func (j jobPast) History(_, run string) ([]contract.Event, error) {
	if run != "r1" {
		return nil, nil
	}
	return j.past, nil
}

type private struct{ home }

func (private) Private(string) error { return nil }

// everyRun lists the runs of the morning's project.
type everyRun struct{ fleetStore }

func (e everyRun) Runs(string) (ids []string, err error) {
	for _, s := range e.runs {
		ids = append(ids, s.Run.ID)
	}
	return append(ids, "gone"), nil
}

// The first steps tick by what has happened, and what ui.json holds stays.
func TestFirstSteps(t *testing.T) {
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	in := contract.SideInput{Now: now, Terms: &contract.Snapshot{Panes: []contract.Pane{{ID: "p1"}}}}
	job := &contract.State{Run: contract.Run{ID: "r1", CreatedAt: now}, Attempts: map[string]*contract.Attempt{}, Questions: map[string]*contract.Question{}}
	step := func(want int, show bool, ui contract.UIFile, what string) {
		t.Helper()
		in.UI = ui
		if out := Side(in); out.Steps != want || out.StepsShown != show {
			got, shown := out.Steps, out.StepsShown
			t.Errorf("%s: steps %05b shown %v, want %05b %v", what, got, shown, want, show)
		}
	}
	step(0, true, contract.UIFile{}, "a new window")
	in.Terms.Panes[0].Agent = "claude"
	step(1, true, contract.UIFile{}, "an agent runs")
	in.Projects = []contract.SideProject{{Root: "shop", Runs: []*contract.State{job}}}
	step(3, true, contract.UIFile{}, "a job")
	job.Attempts["T1.1"] = &contract.Attempt{ID: "T1.1"}
	step(7, true, contract.UIFile{}, "a worker")
	job.Questions["q1"] = &contract.Question{ID: "q1", AnsweredBy: &contract.Origin{Caller: contract.Orchestrator}}
	step(7, true, contract.UIFile{}, "the lead agent answered")
	job.Questions["q1"].AnsweredBy.Caller = contract.Human
	step(15, true, contract.UIFile{}, "the person answered")
	delete(job.Questions, "q1")
	in.Now = now.Add(10 * time.Minute)
	step(15, true, contract.UIFile{}, "ten minutes with a job")
	step(15, false, contract.UIFile{FirstSteps: contract.Off}, "hidden")
	step(31, false, contract.UIFile{Steps: 16}, "the settings were opened")
	if len(contract.FirstSteps) != 5 {
		t.Errorf("the steps are %d", len(contract.FirstSteps))
	}
}
