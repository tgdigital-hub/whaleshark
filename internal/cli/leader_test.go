package cli

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	fleet "github.com/tgdigital-hub/whaleshark/internal/rules"
)

func (s writing) Both(_, up, low string, fn func(up, low *contract.State) error) error {
	return fn(s.runs[up], s.runs[low])
}

// The scenes of a job with fleets: the orchestrator, the leader of billing
// in its tab and in a tab that lost its variables, and a worker of billing.
var (
	orchestrator = scene{pane: "w1:p1"}
	leader       = scene{pane: "w1:p2", attempt: "G1.1", run: "r1"}
	leaderBare   = scene{pane: "w1:p2"}
	fleetWorker  = scene{pane: "w1:p3", attempt: "T1.1", run: "r2"}
)

// fleets is a job in Fleet Mode, made by the rules: run r1 with its
// orchestrator in pane w1:p1 and the groups G1 and G2 for the fleet billing,
// whose run r2 is led from pane w1:p2 by the attempt G1.1 and has a worker
// T1.1 in pane w1:p3; and search, run r4, led from pane w1:p4 by G3.1.
func fleets(t *testing.T) (*contract.Kit, *fakeStore) {
	t.Helper()
	var r fleet.Rules
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	up := r.NewRun("r1", "a job", 20, nil, contract.Binding{Pane: "w1:p1"}, now)
	up.Run.Mode = contract.ModeFleets
	runs := map[string]*contract.State{"r1": up}
	lead := func(group, fleet, run, pane string) {
		must(r.AddTask(up, contract.Task{ID: group, Title: "group " + group, Check: "true", Fleet: fleet}, nil, now))
		id, err := r.Start(up, group, false, false, "hash", contract.Agent{Kind: "claude"}, true, now)
		must(err)
		must(r.Placed(up, id, contract.Place{Tab: "tab-" + pane, Pane: pane}, now))
		runs[run] = r.NewRun(run, fleet, 8, nil, contract.Binding{Pane: pane}, now)
		must(r.Lead(up, runs[run], id, contract.Binding{Pane: pane}, "", now))
		must(r.Working(up, id, now))
	}
	lead("G1", "billing", "r2", "w1:p2")
	lead("G3", "search", "r4", "w1:p4")
	must(r.AddTask(up, contract.Task{ID: "G2", Title: "group G2", Check: "true", Fleet: "billing"}, nil, now))
	low := runs["r2"]
	must(r.AddTask(low, contract.Task{ID: "T1", Title: "task T1", Check: "true"}, nil, now))
	id, err := r.Start(low, "T1", false, false, "hash", contract.Agent{Kind: "claude"}, true, now)
	must(err)
	must(r.Placed(low, id, contract.Place{Tab: "tab-w1:p3", Pane: "w1:p3"}, now))
	must(r.Working(low, id, now))

	store := &fakeStore{runs: runs, order: []string{"r1", "r2", "r4"}, current: "r1"}
	k := contract.NewKit()
	Plug(k)
	fleet.Plug(k)
	k.Store = writing{store}
	return k, store
}

// Section 7 and 19.1, "Who may do what", a scenario for each row of the
// table, each trying to break it as an agent would: the leader, the worker
// of a fleet, the orchestrator, and the person, who is fenced in nowhere.
// What is refused runs no handler and leaves the record as it was.
func TestTheCallerTableWithFleets(t *testing.T) {
	const never = contract.RefusedLeader
	for _, tc := range []struct {
		name string
		w    scene
		line string
		kind contract.CallerKind
		code string
		run  string
	}{
		// The leader: three commands go up as a worker's, to the run of its attempt.
		{"a leader's report goes up", leader, "report done ok", contract.Worker, "", "r1"},
		{"a leader's report of one group goes up", leader, "report done ok --task G1", contract.Worker, "", "r1"},
		{"a leader's progress goes up", leader, "progress 40 x --task G1", contract.Worker, "", "r1"},
		{"a leader's mail comes from above", leader, "mail", contract.Worker, "", "r1"},
		{"a leader's gate is a worker's, so Stop all holds it", leader, "hook gate", contract.Worker, "", "r1"},
		{"in a tab that lost its variables", leaderBare, "mail", contract.Worker, "", "r1"},
		// Everything else is its fleet's.
		{"a leader starts its workers", leader, "start T1", contract.Leader, "", "r2"},
		{"a leader plans", leader, "task add T9 x --brief b --check none", contract.Leader, "", "r2"},
		{"a leader waits on its fleet", leader, "wait", contract.Leader, "", "r2"},
		{"a leader accepts", leader, "accept T1", contract.Leader, "", "r2"},
		{"a leader answers its worker", leader, "answer q1 yes", contract.Leader, "", "r2"},
		{"a leader hands a question up", leader, "escalate q1", contract.Leader, "", "r2"},
		{"a leader puts an item to the orchestrator", leader, "need question x", contract.Leader, "", "r2"},
		{"a leader looks at its fleet", leader, "status", contract.Leader, "", "r2"},
		{"a leader names its own fleet's run", leader, "start T1 --run r2", contract.Leader, "", "r2"},
		{"in a tab that lost its variables it leads still", leaderBare, "start T1", contract.Leader, "", "r2"},
		// The eight things it never may.
		{"a leader lands", leader, "land --local", "", never, ""},
		{"a leader makes a run", leader, "run new x", "", never, ""},
		{"a leader closes its fleet's run", leader, "run close", "", never, ""},
		{"a leader closes it as abandoned", leader, "run close --abandon", "", never, ""},
		{"a leader removes a run", leader, "run rm r2", "", never, ""},
		{"a leader takes a run over", leader, "run takeover", "", never, ""},
		{"a leader hands work to a fleet of its own", leader, "task add T9 x --brief b --check none --fleet deeper", "", never, ""},
		{"a leader answers in the person's name", leader, "answer q1 yes --relayed", "", never, ""},
		{"a leader asks", leader, "ask which", "", never, ""},
		{"in a tab that lost its variables it lands", leaderBare, "land --local", "", never, ""},
		// What an agent would try next.
		{"a leader starts a group of the orchestrator's run", leader, "start G2 --run r1", "", "not_for_worker", ""},
		{"a leader accepts its own group", leader, "accept G1 --run r1", "", "not_for_worker", ""},
		{"a leader answers a question of the orchestrator's run", leader, "answer q1 yes --run r1", "", "not_for_worker", ""},
		{"a leader takes the orchestrator's run over", leader, "run takeover --run r1", "", "not_for_worker", ""},
		{"a leader makes a run under the orchestrator's name", leader, "run new x --run r1", "", "not_for_worker", ""},
		{"a leader stops a worker of another fleet", leader, "stop T1 --run r4", "", "not_for_worker", ""},
		{"a leader is the person by the flag", leader, "accept T1 --human", "", "not_human", ""},
		{"a leader is the person by the flag, in a bare tab", leaderBare, "pause --human", "", "not_human", ""},
		{"a leader is the person by the page's mark", scene{pane: "w1:p2", attempt: "G1.1", run: "r1", from: "page"}, "pause", "", "human_only", ""},
		{"a leader presses Stop all", leader, "pause", "", "human_only", ""},
		{"a leader raises its own limit", leader, "set fleets.total 400", "", contract.RefusedLeader, ""},
		{"a leader writes to the orchestrator as the person", leader, "tell lead x", "", "human_only", ""},
		{"a leader undoes an answer", leader, "answer q1 --undo", "", "human_only", ""},
		{"a leader approves a project's settings", leader, "trust", "", "human_only", ""},
		// A worker of a fleet: its four commands, to its leader's run, and nothing else.
		{"a fleet's worker reports to its leader's run", fleetWorker, "report done ok", contract.Worker, "", "r2"},
		{"a fleet's worker asks its leader", fleetWorker, "ask which", contract.Worker, "", "r2"},
		{"a fleet's worker in a restored tab", scene{pane: "w1:p3"}, "progress 10 x", contract.Worker, "", "r2"},
		{"a fleet's worker says which group", fleetWorker, "report done ok --task G1", "", "not_leader", ""},
		{"a fleet's worker starts a worker", fleetWorker, "start T1", "", "not_for_worker", ""},
		{"a fleet's worker hands work to a fleet", fleetWorker, "task add T9 x --fleet deeper", "", "not_for_worker", ""},
		{"a fleet's worker looks at the orchestrator's run", fleetWorker, "status --run r1", "", "not_for_worker", ""},
		{"a fleet's worker under its leader's variables", scene{pane: "w1:p3", attempt: "G1.1", run: "r1"}, "start T1", "", "not_for_worker", ""},
		{"a fleet's worker under its leader's variables lands", scene{pane: "w1:p3", attempt: "G1.1", run: "r1"}, "land --local", "", "not_for_worker", ""},
		{"a fleet's worker with no pane and its leader's variables", scene{attempt: "G1.1", run: "r1"}, "accept T1", "", "not_for_worker", ""},
		{"the leader of another fleet in this one", scene{pane: "w1:p4", attempt: "G3.1", run: "r1"}, "accept T1 --run r2", "", "not_for_worker", ""},
		{"another fleet's leader under this one's attempt", scene{pane: "w1:p4", attempt: "G1.1", run: "r1"}, "accept T1", "", "not_for_worker", ""},
		// The orchestrator: its own run, and a fleet's only to read.
		{"the orchestrator hands a group to a fleet", orchestrator, "task add G7 x --brief b --check none --fleet billing", contract.Orchestrator, "", "r1"},
		{"the orchestrator starts a group", orchestrator, "start G2", contract.Orchestrator, "", "r1"},
		{"the orchestrator reads a fleet", orchestrator, "status --run r2", contract.Unbound, "", "r2"},
		{"the orchestrator starts a fleet's worker", orchestrator, "start T1 --run r2", "", "not_bound", ""},
		{"the orchestrator answers inside a fleet", orchestrator, "answer q1 yes --run r2", "", "not_bound", ""},
		{"the orchestrator stops a fleet's worker", orchestrator, "stop T1 --run r2", "", "not_bound", ""},
		{"the orchestrator says which group", orchestrator, "report done ok --task G1", "", "not_leader", ""},
		{"the orchestrator asks for a limit to be raised", orchestrator, "set fleets.leaders 9", contract.Orchestrator, "", "r1"},
		{"a fleet's name of four words", orchestrator, "task add G7 x --fleet a,b --fleet one-two-three-four-five-six", "", "invalid_value", ""},
		{"a fleet's name that reads as an option", orchestrator, "task add G7 x --fleet=-rf", "", "invalid_value", ""},
		// The person: everything, anywhere.
		{"the person stops a fleet's worker", person, "stop T1 --run r2", contract.Human, "", "r2"},
		{"the person writes to a fleet's worker", person, "tell T1 x --run r2", contract.Human, "", "r2"},
		{"the person from a tab of his own", scene{pane: "w9:p9"}, "accept T1 --run r2 --human", contract.Human, "", "r2"},
	} {
		k, store := fleets(t)
		line := strings.Fields(tc.line)
		got, before := seen(k, line[0]), records(store)
		r, exit := refusal(t, k, tc.w, line...)
		want := contract.ExitRefused
		if tc.code == "invalid_value" {
			want = contract.ExitUsage
		}
		switch {
		case r.Code != tc.code || tc.code != "" && exit != want || tc.code == "" && exit != 0:
			t.Errorf("%s: exit %d, code %q, want %q", tc.name, exit, r.Code, tc.code)
		case tc.code == "" && (got.Caller.Kind != tc.kind || got.Run != tc.run || got.Caller.Pane != tc.w.pane):
			t.Errorf("%s: %+v of run %q, want %s of %q", tc.name, got.Caller, got.Run, tc.kind, tc.run)
		case tc.code != "" && (got.Command != nil || records(store) != before):
			t.Errorf("%s: refused, and a handler ran or the record changed", tc.name)
		case strings.Contains(r.Message+strings.Join(r.Next, " "), "--human") && tc.code != "not_human":
			t.Errorf("%s: --human is shown to an agent: %+v", tc.name, r)
		}
		if tc.code == never && tc.line == "ask which" != slices.Equal(r.Next, []string{`whaleshark need question "<text>"`}) {
			t.Errorf("%s: next %q", tc.name, r.Next)
		}
	}
}

func records(s *fakeStore) string {
	var b strings.Builder
	for _, id := range s.order {
		b.WriteString(id + " " + string(s.runs[id].Run.Orchestrator.Pane))
		for _, e := range s.runs[id].Inbox.Events {
			b.WriteString(" " + e.Kind)
		}
	}
	return b.String()
}

// Every entry of contract.LeaderNever is refused from a leader's tab, in the
// form the list gives it, and nothing else of an orchestrator's is.
func TestLeaderNever(t *testing.T) {
	for i := range contract.Commands {
		cmd := &contract.Commands[i]
		forms := []string{""}
		for _, s := range cmd.Subs {
			forms = append(forms, s.Name)
		}
		for _, sub := range forms {
			for _, f := range append([]contract.Flag{{}}, cmd.Flags...) {
				line := []string{cmd.Name}
				if sub != "" {
					line = append(line, sub)
				}
				if f.Value != "" {
					line = append(line, "--"+f.Name+"=1")
				} else if f.Name != "" {
					line = append(line, "--"+f.Name)
				}
				k, _ := fleets(t)
				got := seen(k, cmd.Name)
				r, exit := refusal(t, k, leader, line...)
				// A flag that is the person's alone is no leader's either.
				may := f.Who == 0 && cmd.LeaderMay(sub, func(flag string) bool { return flag == f.Name })
				listed := slices.ContainsFunc(contract.LeaderNever, func(n string) bool {
					return n == cmd.Name || n == cmd.Name+" "+sub || f.Name != "" && n == cmd.Name+" --"+f.Name
				})
				switch {
				case exit == contract.ExitUsage: // a value the flag does not take
				case may != (got.Command != nil) || may != (exit == 0):
					t.Errorf("%v as a leader: ran %v, exit %d, the table says %v", line, got.Command != nil, exit, may)
				case listed != (r.Code == contract.RefusedLeader):
					t.Errorf("%v as a leader: %q, and the list says %v", line, r.Code, listed)
				}
			}
		}
	}
}

// A leader that is no leader any more: its fleet taken over from another
// pane, its fleet's run closed, or a run that names another as its parent.
func TestALeaderNoMore(t *testing.T) {
	for name, change := range map[string]func(up, low *contract.State){
		"taken over":     func(_, low *contract.State) { low.Run.Orchestrator.Pane = "w1:p9" },
		"closed":         func(_, low *contract.State) { low.Run.ClosedAt = time.Now() },
		"no parent":      func(_, low *contract.State) { low.Run.Parent = nil },
		"another parent": func(_, low *contract.State) { low.Run.Parent.Run = "r4" },
		"leads nothing":  func(up, _ *contract.State) { up.Attempts["G1.1"].Leads = "" },
		"leads another":  func(up, _ *contract.State) { up.Attempts["G1.1"].Leads = "r4" },
	} {
		for _, line := range []string{"start T1", "accept T1", "task add T9 x", "status"} {
			k, store := fleets(t)
			change(store.runs["r1"], store.runs["r2"])
			got := seen(k, strings.Fields(line)[0])
			if r, exit := refusal(t, k, leader, strings.Fields(line)...); exit != contract.ExitRefused || r.Code != "not_for_worker" || got.Command != nil {
				t.Errorf("%s, then %s: exit %d, %q", name, line, exit, r.Code)
			}
		}
		k, store := fleets(t)
		change(store.runs["r1"], store.runs["r2"])
		got := seen(k, "mail")
		if _, _, exit := call(t, k, leader, "mail"); exit != 0 || got.Caller.Kind != contract.Worker || got.Run != "r1" {
			t.Errorf("%s, then mail: %+v of %q", name, got.Caller, got.Run)
		}
	}
}

// 19.1: what the person does in a fleet, past the orchestrator, both lead
// agents are told; what he does in the orchestrator's run, that one alone.
func TestThePersonInAFleetIsToldToBoth(t *testing.T) {
	k, store := fleets(t)
	seen(k, "stop")
	kinds := func(run string) (n int, last contract.Event) {
		for _, e := range store.runs[run].Inbox.Events {
			if e.Kind == "human" {
				n, last = n+1, e
			}
		}
		return n, last
	}
	if _, errw, exit := call(t, k, person, "stop", "T1", "--run", "r2"); exit != 0 {
		t.Fatal(errw)
	}
	for _, run := range []string{"r1", "r2"} {
		if n, e := kinds(run); n != 1 || e.Data["what"] != "stop" || e.Data["on"] != "T1" || e.Data["fleet"] != "billing" || e.Data["where"] != contract.WhereTyped {
			t.Errorf("run %s: %d human events, %+v", run, n, e)
		}
	}
	if n, _ := kinds("r4"); n != 0 {
		t.Error("another fleet is told")
	}
	call(t, k, person, "stop", "G1", "--run", "r1")
	if up, _ := kinds("r1"); up != 2 {
		t.Errorf("the orchestrator's run: %d human events", up)
	}
	if low, _ := kinds("r2"); low != 1 {
		t.Errorf("a fleet is told what was done above it: %d", low)
	}
}

// help set lists every setting of the table that has a row of the screen.
func TestHelpSetListsTheKeys(t *testing.T) {
	k, _ := world(t)
	out, _, exit := call(t, k, person, "help", "set")
	for _, key := range contract.Keys {
		if listed := strings.Contains(out, "\n  "+key.Name+" "); exit != 0 || listed != (key.Section != "") {
			t.Errorf("help set and the key %s: listed %v", key.Name, listed)
		}
	}
	// And with --json, each under the names a program reads.
	var said struct{ Result struct{ Keys []contract.Key } }
	if out, _, _ = call(t, k, person, "help", "set", "--json"); json.Unmarshal([]byte(out), &said) != nil || len(said.Result.Keys) < 50 ||
		!strings.Contains(out, `"name":"fleets.mode"`) || strings.Contains(out, `"Section"`) {
		t.Errorf("help set --json has %d keys:\n%s", len(said.Result.Keys), out)
	}
	if other, _, _ := call(t, k, person, "help", "start"); strings.Contains(other, "fleets.mode") {
		t.Error("the keys are listed under another command")
	}
}

// The fuzz test on the validator with fleet names: --fleet takes what a
// task's name takes and nothing else, and what then passes the rules as a
// new fleet can be the label of a leader's tab: one line of one to three
// words and 24 characters at most, no option, no control character, not
// the lead agent's word and on no tab that is open.
func FuzzFleetName(f *testing.F) {
	for _, seed := range []string{"billing", "help pages", "a b c d", "-rf", "--fleet=x", "lead", "LEAD", "Docs", " x ", "x\ny", "\x1b[2J", "\x1b]0;t\a",
		"é\u202e", strings.Repeat("x", 25), strings.Repeat("é", 24), "", "\t", "a  b", "\xff\xfe", "a\u2028b", "$(x)", "`x`", ";x", "a\u00a0b\u00a0c\u00a0d"} {
		f.Add(seed)
	}
	task, now := contract.Find("task"), time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, v string) {
		passes := check(task, parsed{flags: map[string][]string{"fleet": {v}}}, "") == nil
		if passes != (Valid("name", v, "") == nil) {
			t.Fatalf("--fleet %q passes: %v, and as a name it does not do the same", v, passes)
		}
		var r fleet.Rules
		up := r.NewRun("r1", "a job", 20, nil, contract.Binding{Pane: "w1:p1"}, now)
		up.Run.Mode = contract.ModeFleets
		err := r.AddTask(up, contract.Task{ID: "G1", Title: "one", Check: "true", Fleet: v}, []string{"docs"}, now)
		if !passes || err != nil {
			if err != nil && len(up.Tasks) != 0 {
				t.Errorf("fleet %q: refused and written", v)
			}
			return
		}
		name := up.Tasks["G1"].Fleet
		if n := len(strings.Fields(name)); n < 1 || n > 3 || utf8.RuneCountInString(name) > 24 || name[0] == '-' || Plain(name) != name || Line(name) != name ||
			strings.EqualFold(name, contract.Lead) || strings.EqualFold(name, "docs") || up.Tasks["G1"].Placement != contract.PlaceFleet {
			t.Errorf("fleet %q is placed as %q", v, name)
		}
		// A second group of the same words joins it, whatever its case.
		again := strings.ToUpper(name)
		if err := r.AddTask(up, contract.Task{ID: "G2", Title: "two", Check: "true", Fleet: " " + again}, []string{name}, now); strings.EqualFold(again, name) && (err != nil || up.Tasks["G2"].Fleet != name) {
			t.Errorf("fleet %q a second time: %v", name, err)
		}
	})
}
