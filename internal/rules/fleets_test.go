package rules

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

var leaderIs = contract.Origin{Caller: contract.Leader}

// job is an orchestrator's run with three groups, two of them for one fleet.
func job(t *testing.T) *contract.State {
	t.Helper()
	up := newRun()
	up.Run.Mode = contract.ModeFleets
	for id, fleet := range map[string]string{"G1": "billing", "G2": "billing", "G3": "search"} {
		must(t, r.AddTask(up, contract.Task{ID: id, Title: "group " + id, Check: "true", Fleet: fleet}, nil, t0))
	}
	return up
}

func fleetRun(id string) *contract.State {
	return r.NewRun(id, "a fleet", 8, nil, contract.Binding{Pane: "w1:p1"}, t0)
}

// led starts a group and hands it to a leader: a new one in the pane, or
// the one that sits where pane is empty.
func led(t *testing.T, up, low *contract.State, group, pane string) string {
	t.Helper()
	id, err := r.Start(up, group, len(up.Tasks[group].Attempts) > 0, false, token, contract.Agent{Kind: "codex", Model: "m1"}, true, t0)
	must(t, err)
	if pane != "" {
		must(t, r.Placed(up, id, contract.Place{Tab: "tab-" + pane, Pane: pane, Terminal: "term"}, t0))
	}
	must(t, r.Lead(up, low, id, contract.Binding{Pane: pane}, "whaleshark/"+low.Run.ID, t0))
	must(t, r.Working(up, id, t0))
	return id
}

// worker gives a fleet's run a task with a live attempt in a tab of its own.
func worker(t *testing.T, low *contract.State, id string) {
	t.Helper()
	add(t, low, id)
	a, err := r.Start(low, id, false, false, token, agent, true, t0)
	must(t, err)
	must(t, r.Placed(low, a, contract.Place{Tab: "tab-" + id, Pane: "pane-" + id}, t0))
	must(t, r.Working(low, a, t0))
}

// sound says what is wrong between an orchestrator's run and its fleets'.
func sound(up *contract.State, lows ...*contract.State) string {
	for _, low := range lows {
		p := low.Run.Parent
		if p == nil || p.Run != up.Run.ID || up.Run.Fleets[p.Fleet] == nil || up.Run.Fleets[p.Fleet].Run != low.Run.ID {
			return "run " + low.Run.ID + " and its fleet's entry do not name each other"
		}
		f := up.Run.Fleets[p.Fleet]
		for _, a := range up.Attempts {
			switch {
			case a.Leads != low.Run.ID:
			case up.Tasks[a.Task].Fleet != p.Fleet || !slices.Contains(f.Groups, a.Task):
				return a.ID + " leads a fleet its group is not in"
			case a.State.Live() && (f.Pane == "" || a.Place.Pane != f.Pane || low.Run.Orchestrator.Pane != f.Pane):
				return a.ID + " is live and not in the pane its fleet's run is bound to"
			}
		}
		for _, q := range low.Questions {
			if q.Up == nil {
				continue
			}
			if twin := up.Questions[q.Up.ID]; twin == nil || twin.Down == nil || twin.Down.ID != q.ID || twin.Down.Run != low.Run.ID || q.For != contract.ForOrchestrator {
				return q.ID + " and its twin do not name each other"
			}
		}
	}
	return ""
}

// 19.1: a group is a task with a fleet, in a run of fleets only, where
// every task is one. Its fleet's name is held as a task's name is.
func TestAGroupIsPlaced(t *testing.T) {
	plain := newRun()
	if err := r.AddTask(plain, contract.Task{ID: "T1", Title: "one", Check: "true", Name: " -rf"}, nil, t0); code(err) != "bad_name" || len(plain.Tasks) != 0 {
		t.Errorf("a task given a name that is an option behind a space: %v", err)
	}
	err := r.AddTask(plain, contract.Task{ID: "G1", Title: "one", Check: "true", Fleet: "billing"}, nil, t0)
	if code(err) != contract.RefusedNotFleets || exit(err) != contract.ExitRefused || len(plain.Tasks) != 0 {
		t.Errorf("a fleet in a run without fleets: %v", err)
	}
	up := job(t)
	if g := up.Tasks["G1"]; g.Fleet != "billing" || g.Placement != contract.PlaceFleet || g.Status != contract.TaskReady {
		t.Errorf("a group: %+v", g)
	}
	for _, tc := range []struct {
		fleet string
		taken []string
		code  string
		is    string
	}{
		{"", nil, "no_fleet", ""}, {"   ", nil, "no_fleet", ""}, {"one two three four", nil, "no_fleet", ""},
		{strings.Repeat("x", 25), nil, "no_fleet", ""}, {strings.Repeat("é", 24), nil, "", strings.Repeat("é", 24)},
		{"  BILLING ", []string{"billing"}, "", "billing"}, {"help  pages", nil, "", "help pages"},
		{"docs", []string{"Docs"}, "name_taken", ""}, {"Lead", nil, "name_taken", ""},
		// Found by the fuzz test: the validator saw a space in front, and the name that was kept began with the dash.
		{" -rf", nil, "no_fleet", ""}, {" - ", nil, "no_fleet", ""}, {"up-to date", nil, "", "up-to date"},
	} {
		s := job(t)
		err := r.AddTask(s, contract.Task{ID: "G9", Title: "nine", Check: "true", Fleet: tc.fleet}, tc.taken, t0)
		if code(err) != tc.code || tc.code == "" && s.Tasks["G9"].Fleet != tc.is || tc.code == "no_fleet" && exit(err) != contract.ExitUsage {
			t.Errorf("fleet %q: %v, %+v", tc.fleet, err, s.Tasks["G9"])
		}
		if tc.code != "" && s.Tasks["G9"] != nil {
			t.Errorf("fleet %q: refused and written", tc.fleet)
		}
	}
}

// A group started, handed to a living leader, reported, sent back, accepted,
// and its fleet taken over after the leader's program ended.
func TestAFleetAndItsLeader(t *testing.T) {
	up, low := job(t), fleetRun("r2")
	refusals := func(by string, want string, change func(up, low *contract.State) string) {
		t.Helper()
		u, l := job(t), fleetRun("r2")
		id := change(u, l)
		before := snapshot(u) + snapshot(l)
		if err := r.Lead(u, l, id, contract.Binding{Pane: by}, "", t0); code(err) != want || snapshot(u)+snapshot(l) != before {
			t.Errorf("Lead in %q: %v, want %s and nothing written", by, err, want)
		}
	}
	start := func(u *contract.State, g string) string {
		id, err := r.Start(u, g, false, false, token, agent, true, t0)
		must(t, err)
		return id
	}
	refusals("", "leader", func(u, _ *contract.State) string { return start(u, "G1") })
	refusals("w1:p3", "leader", func(u, l *contract.State) string { led(t, u, l, "G1", "w1:p2"); return start(u, "G2") })
	refusals("w1:p3", "other_fleet", func(u, l *contract.State) string { led(t, u, l, "G1", "w1:p2"); return start(u, "G3") })
	refusals("w1:p3", "other_fleet", func(u, l *contract.State) string { add(t, l, "T1"); return start(u, "G1") })
	refusals("w1:p3", "closed", func(u, l *contract.State) string { must(t, r.CloseRun(l, false, t0)); return start(u, "G1") })
	refusals("w1:p3", contract.RefusedNotFleets, func(u, l *contract.State) string {
		id := start(u, "G1")
		must(t, r.StartFailed(u, id, "no tab", t0))
		return id
	})
	refusals("w1:p3", "no_such_attempt", func(*contract.State, *contract.State) string { return "G7.1" })

	one := led(t, up, low, "G1", "w1:p2")
	f := up.Run.Fleets["billing"]
	if f == nil || f.Run != "r2" || f.Pane != "w1:p2" || f.Tab != "tab-w1:p2" || f.Tool != "codex" || f.Model != "m1" || f.Branch != "whaleshark/r2" ||
		!slices.Equal(f.Groups, []string{"G1"}) || f.Since != t0 || up.Attempts[one].Leads != "r2" {
		t.Fatalf("the fleet's entry: %+v", f)
	}
	if o := low.Run.Orchestrator; o.Pane != "w1:p2" || low.Run.Gen != 1 || *low.Run.Parent != (contract.Twin{Run: "r1", Fleet: "billing"}) {
		t.Fatalf("the fleet's run: %+v, gen %d, %+v", o, low.Run.Gen, low.Run.Parent)
	}
	two := led(t, up, low, "G2", "")
	if a := up.Attempts[two]; a.Place != up.Attempts[one].Place || a.Leads != "r2" || !slices.Equal(f.Groups, []string{"G1", "G2"}) || low.Run.Gen != 1 {
		t.Fatalf("a second group for a living leader: %+v, %v", a, f.Groups)
	}
	if why := sound(up, low); why != "" {
		t.Fatal(why)
	}

	// Reported, sent back, reported again and accepted, as any task is.
	_, err := report(r.Report(up, one, token, outDone, "done", nil, t0))
	must(t, err)
	if g := up.Tasks["G1"]; g.Status != contract.TaskReview || !up.Run.Scan.Due {
		t.Errorf("a group reported: %s, scan due %v", g.Status, up.Run.Scan.Due)
	}
	must(t, r.Reject(up, "G1", "the totals are off", contract.Live, t0))
	if a := up.Attempts[one]; a.State != contract.AttemptWorking || len(a.Mail) != 1 || up.Tasks["G1"].Status != contract.TaskRunning {
		t.Errorf("a group sent back: %+v", a)
	}
	_, err = report(r.Report(up, one, token, outDone, "done", nil, t0))
	must(t, err)
	must(t, r.Checking(up, "G1", contract.Checked{}, t0))
	_, err = r.Checked(up, "G1", contract.CheckResult{OK: true}, contract.Accepted{How: howCheck, Commit: "c1"}, t0)
	must(t, err)
	// The leader reports its other group failed: it sits in its pane still,
	// and the next group goes to it.
	_, err = report(r.Report(up, two, token, outFailed, "cannot", nil, t0))
	must(t, err)
	if f.Pane != "w1:p2" || up.Tasks["G2"].Status != contract.TaskReady {
		t.Fatalf("a leader with no open group has left its pane: %+v", f)
	}
	three := led(t, up, low, "G2", "")
	if a := up.Attempts[three]; a.Place.Pane != "w1:p2" || a.Place.Tab != "tab-w1:p2" || a.Agent.Kind != "codex" || a.Leads != "r2" {
		t.Fatalf("a group for a leader that held none: %+v", a)
	}

	// Its program ends: the sweep proves it gone, the entry loses its pane,
	// the fleet's run stays open, and a new leader takes it over.
	worker(t, low, "T1")
	r.Seen(up, three, nil, false, limits, t0)
	r.Seen(up, three, nil, false, limits, after(goneAfter))
	if a := up.Attempts[three]; a.State != contract.AttemptExited || f.Pane != "" || f.Tab != "" || open(low) != nil ||
		!low.Attempts["T1.1"].State.Live() || up.Tasks["G2"].Failures != 2 {
		t.Fatalf("a leader's program ended: %s, %+v", a.State, f)
	}
	four := led(t, up, low, "G2", "w1:p7")
	if o := low.Run.Orchestrator; o.Pane != "w1:p7" || low.Run.Gen != 2 || f.Pane != "w1:p7" || up.Attempts[four].Leads != "r2" || f.Since != t0 {
		t.Fatalf("a fleet taken over: %+v, %+v", o, f)
	}
	if why := sound(up, low); why != "" {
		t.Fatal(why)
	}
	if r.LeadSeen(low, pane(contract.StatusIdle), false, after(time.Hour)) || len(low.Questions) != 0 {
		t.Error("the person is told that a leader is not listening")
	}
}

// 19.1, a parked fleet: its leader and every agent of it stopped, the tasks
// ready again, and the run open.
func TestParkAndEnd(t *testing.T) {
	up, low := job(t), fleetRun("r2")
	led(t, up, low, "G1", "w1:p2")
	led(t, up, low, "G2", "")
	worker(t, low, "T1")
	worker(t, low, "T2")
	before := snapshot(up) + snapshot(low)
	for _, tc := range []struct{ group, code string }{{"G1", "holds_more"}, {"G3", "no_live_attempt"}, {"G8", "no_such_task"}} {
		if _, err := r.Park(up, low, tc.group, t0); code(err) != tc.code || snapshot(up)+snapshot(low) != before {
			t.Errorf("Park %s: %v, want %s and nothing written", tc.group, err, tc.code)
		}
	}
	must(t, r.Stop(up, "G2", true, t0))
	if f := up.Run.Fleets["billing"]; f.Pane != "w1:p2" {
		t.Fatalf("one group of two stopped, and the leader has left: %+v", f)
	}
	_, err := report(r.Report(low, "T2.1", token, outDone, "done", nil, t0))
	must(t, err)
	must(t, r.Checking(low, "T2", contract.Checked{}, t0))
	before = snapshot(up) + snapshot(low)
	if _, err := r.Park(up, low, "G1", t0); code(err) != "checking" || snapshot(up)+snapshot(low) != before {
		t.Errorf("Park while a task is accepted: %v", err)
	}
	must(t, r.Unchecked(low, "T2", t0))
	if err := r.Ended(up, low, true, t0); code(err) != "open_group" || open(low) != nil {
		t.Errorf("Ended with a group open: %v", err)
	}

	tabs, err := r.Park(up, low, "G1", t0)
	must(t, err)
	f := up.Run.Fleets["billing"]
	if !slices.Equal(tabs, []string{"tab-T1", "tab-T2", "tab-w1:p2"}) || f.Pane != "" || f.Tab != "" || !f.EndedAt.IsZero() || open(low) != nil {
		t.Fatalf("parked: tabs %v, %+v", tabs, f)
	}
	for _, s := range []*contract.State{up, low} {
		for _, a := range s.Attempts {
			if a.State.Live() || a.State == contract.AttemptStopped && a.Exit.Cause != contract.ExitStopped && a.Exit.Cause != contract.ExitClosedByUs {
				t.Errorf("%s of %s after the park: %s", a.ID, s.Run.ID, a.State)
			}
		}
	}
	if up.Tasks["G1"].Status != contract.TaskReady || up.Tasks["G1"].Failures != 0 || low.Tasks["T1"].Status != contract.TaskReady {
		t.Errorf("after the park: %s, %s", up.Tasks["G1"].Status, low.Tasks["T1"].Status)
	}

	// Brought back, finished and ended. A fleet has nothing to land.
	g := led(t, up, low, "G1", "w1:p8")
	worker2, err := r.Start(low, "T1", true, false, token, agent, true, t0)
	must(t, err)
	must(t, r.Working(low, worker2, t0))
	low.Run.Integration = &contract.Integration{Branch: "whaleshark/r2", Tip: "c0"}
	accept := func(s *contract.State, task, attempt string) {
		t.Helper()
		_, err := report(r.Report(s, attempt, token, outDone, "done", nil, t0))
		must(t, err)
		must(t, r.Checking(s, task, contract.Checked{}, t0))
		_, err = r.Checked(s, task, contract.CheckResult{OK: true}, contract.Accepted{How: howCheck, Commit: "c1"}, t0)
		must(t, err)
	}
	if err := r.Ended(up, low, true, t0); code(err) != "open_group" {
		t.Errorf("Ended with its group running: %v", err)
	}
	must(t, r.CancelTask(up, "G2", t0))
	_, err = report(r.Report(up, g, token, outDone, "done", nil, t0))
	must(t, err)
	must(t, r.Checking(up, "G1", contract.Checked{}, t0))
	_, err = r.Checked(up, "G1", contract.CheckResult{OK: true}, contract.Accepted{How: howCheck}, t0)
	must(t, err)
	if err := r.Ended(up, low, false, t0); code(err) != "still_live" || open(low) != nil {
		t.Errorf("Ended with an agent of the fleet live: %v", err)
	}
	accept(low, "T1", worker2)
	if err := r.Ended(up, fleetRun("r5"), false, t0); code(err) != contract.RefusedNotFleets {
		t.Errorf("Ended on a run that is no fleet's: %v", err)
	}
	// What the run used is written into the job's record as it ends.
	low.Attempts[worker2].StartedAt = low.Attempts[worker2].EndedAt.Add(-2 * time.Hour)
	must(t, r.Ended(up, low, false, after(time.Minute)))
	if f.EndedAt != after(time.Minute) || f.Pane != "" || open(low) == nil || f.Used != 2*time.Hour {
		t.Errorf("ended: %+v", f)
	}
	if err := r.Lead(up, low, g, contract.Binding{Pane: "w1:p9"}, "", t0); code(err) != "closed" {
		t.Errorf("a fleet that ended is led again: %v", err)
	}
	// More work for a fleet that ended: the same name, a run of its own.
	must(t, r.AddTask(up, contract.Task{ID: "G4", Title: "group G4", Check: "true", Fleet: "Billing"}, nil, t0))
	if err := r.Room(up, map[string]*contract.State{"billing": low}, "billing", "", contract.PersonDefaults(), 0); err != nil {
		t.Errorf("room for a fleet that begins again: %v", err)
	}
	again := fleetRun("r6")
	led(t, up, again, "G4", "w1:p9")
	if f := up.Run.Fleets["billing"]; f.Run != "r6" || f.Pane != "w1:p9" || !f.EndedAt.IsZero() || !slices.Equal(f.Groups, []string{"G4"}) || sound(up, again) != "" || f.Used != 2*time.Hour {
		t.Errorf("a fleet that began again: %+v, %s", f, sound(up, again))
	}
	if err := r.Ended(up, low, true, t0); code(err) != contract.RefusedNotFleets {
		t.Errorf("the run of a fleet that began again ends it: %v", err)
	}
	// A run that is nobody's fleet still has to land.
	plain := in(t, contract.AttemptAccepted)
	plain.Tasks["T1"].Accepted.Commit, plain.Run.Integration = "c1", &contract.Integration{Tip: "c1"}
	if err := r.CloseRun(plain, false, t0); code(err) != "not_landed" {
		t.Errorf("a run of its own closes with work not landed: %v", err)
	}
}

// 19.1, how the three limits are held.
func TestRoom(t *testing.T) {
	up := job(t)
	lows := map[string]*contract.State{"billing": fleetRun("r2"), "search": fleetRun("r3")}
	led(t, up, lows["billing"], "G1", "w1:p2")
	for _, id := range []string{"T1", "T2", "T3"} {
		worker(t, lows["billing"], id)
	}
	add(t, lows["billing"], "T4")
	set := func(leaders, agents, total int) contract.PersonConfig {
		c := contract.PersonDefaults()
		c.Fleets.Leaders, c.Fleets.Agents, c.Fleets.Total = leaders, agents, total
		return c
	}
	const none = contract.Unlimited
	for _, tc := range []struct {
		name          string
		fleet, worker string
		limits        contract.PersonConfig
		cap           int
		code, says    string
	}{
		{"a group for a leader that sits counts nothing", "billing", "", set(1, 1, 1), 1, "", ""},
		{"a second leader of two", "search", "", set(2, 8, 20), 0, "", ""},
		{"a second leader of one", "search", "", set(1, 8, 20), 0, contract.LimitLeaders, "1 fleets are running"},
		{"a fleet nobody has heard of needs a leader too", "docs", "", set(1, 8, 20), 0, contract.LimitLeaders, ""},
		{"leaders unlimited", "search", "", set(none, 8, 20), 0, "", ""},
		{"a leader is one of the total", "search", "", set(4, 8, 4), 0, contract.LimitTotal, "4 agents are live in all"},
		{"a leader under the total", "search", "", set(4, 8, 5), 0, "", ""},
		{"a fourth worker of eight", "billing", "T4", set(4, 8, 20), 0, "", ""},
		{"a fourth worker of three: the leader is not counted", "billing", "T4", set(4, 3, 20), 0, contract.LimitFleet, "T4 waits"},
		{"a fourth worker of four", "billing", "T4", set(4, 4, 20), 0, "", ""},
		{"a worker over the total, which the fleet's limit let through", "billing", "T4", set(4, 8, 4), 0, contract.LimitTotal, ""},
		{"everything unlimited", "billing", "T4", set(none, none, none), 0, "", ""},
		{"a server's figure under the person's", "billing", "T4", set(4, 8, 20), 4, contract.LimitTotal, "This server allows you 4 agents"},
		{"a server's figure under unlimited", "search", "", set(none, none, none), 4, contract.LimitTotal, "This server allows you 4 agents"},
		{"a server's figure over the person's", "billing", "T4", set(4, 8, 4), 12, contract.LimitTotal, "which is your limit (Settings › Fleet Mode)."},
		{"a server with room", "billing", "T4", set(4, 8, 20), 5, "", ""},
		{"a worker of a fleet with no run", "docs", "T1", set(4, 8, 20), 0, "no_such_fleet", ""},
	} {
		before := snapshot(up)
		err := r.Room(up, lows, tc.fleet, tc.worker, tc.limits, tc.cap)
		if code(err) != tc.code || tc.code != "" && tc.code != "no_such_fleet" && exit(err) != contract.ExitRefused || err != nil && !strings.Contains(err.Error(), tc.says) || snapshot(up) != before {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.code)
		}
		if tc.says == "which is your limit (Settings › Fleet Mode)." && strings.Contains(err.Error(), "server") {
			t.Errorf("%s: the server is named where it cut nothing: %v", tc.name, err)
		}
	}
	// A leader whose groups are all settled sits there still and is counted.
	_, err := report(r.Report(up, "G1.1", token, outFailed, "cannot", nil, t0))
	must(t, err)
	if err := r.Room(up, lows, "search", "", set(1, 8, 20), 0); code(err) != contract.LimitLeaders {
		t.Errorf("a leader with no open group is not counted: %v", err)
	}
	if err := r.Room(up, lows, "billing", "", set(1, 8, 4), 0); err != nil {
		t.Errorf("a group for the leader that sits: %v", err)
	}
}

// 19.1, questions through three levels: a worker's question climbs to the
// orchestrator and to the person, and the answer goes down the same way.
func TestATwinBothWays(t *testing.T) {
	scene := func() (up, low *contract.State, q string) {
		up, low = job(t), fleetRun("r2")
		led(t, up, low, "G1", "w1:p2")
		worker(t, low, "T1")
		q, err := r.Ask(low, "T1.1", "which base?", []string{"main", "next"}, t0)
		must(t, err)
		return up, low, q
	}
	up, low, q := scene()
	if _, err := r.Twin(up, fleetRun("r9"), q, t0); code(err) != "no_such_question" {
		t.Errorf("a question no run has: %v", err)
	}
	stray := fleetRun("r9")
	worker(t, stray, "T1")
	sq, _ := r.Ask(stray, "T1.1", "x?", nil, t0)
	if _, err := r.Twin(up, stray, sq, t0); code(err) != contract.RefusedNotFleets || len(up.Questions) != 0 || stray.Questions[sq].Up != nil {
		t.Errorf("a question of a run that is no fleet's climbs: %v", err)
	}
	item(low, contract.CausePrompt, low.Tasks["T1"], low.Attempts["T1.1"], "%s is at a prompt", false, t0)
	prompt := find(low, contract.CausePrompt, "T1.1")
	if _, err := r.Twin(up, low, prompt.ID, t0); code(err) != "not_open" || len(up.Questions) != 0 || prompt.For != contract.ForHuman {
		t.Errorf("an item only the person can settle climbs: %v", err)
	}
	must(t, r.Answer(low, q, "main", leaderIs, 0, t0)) // the leader knows: nothing climbs
	if _, err := r.Twin(up, low, q, t0); code(err) != "not_open" || exit(err) != contract.ExitMissing {
		t.Errorf("an answered question climbs: %v", err)
	}

	up, low, q = scene()
	events := len(up.Inbox.Events)
	twin, err := r.Twin(up, low, q, t0)
	must(t, err)
	tw, first := up.Questions[twin], low.Questions[q]
	if tw == nil || tw.Kind != contract.KindAsk || tw.Form != contract.FormChoice || tw.For != contract.ForOrchestrator || tw.Task != "G1" ||
		tw.Attempt != "G1.1" || tw.Text != "which base?" || !slices.Equal(tw.Options, []string{"main", "next"}) || tw.Holds || tw.Urgent ||
		*tw.Down != (contract.Twin{Run: "r2", ID: q}) || *first.Up != (contract.Twin{Run: "r1", ID: twin}) || first.For != contract.ForOrchestrator {
		t.Fatalf("the twin: %+v of %+v", tw, first)
	}
	if e := up.Inbox.Events[len(up.Inbox.Events)-1]; len(up.Inbox.Events) != events+1 || e.Kind != "question" || e.Task != "G1" ||
		e.Data["fleet"] != "billing" || e.Data["task"] != "T1" || e.Data["id"] != twin {
		t.Errorf("the orchestrator is told: %+v", e)
	}
	if why := sound(up, low); why != "" {
		t.Fatal(why)
	}
	// A kill wrote the upper half only: the same twin, and nobody told twice.
	first.Up, first.For = nil, contract.ForOrchestrator
	if again, err := r.Twin(up, low, q, t0); err != nil || again != twin || len(up.Questions) != 1 || len(up.Inbox.Events) != events+1 || first.Up == nil {
		t.Errorf("the twin made again: %q, %v", again, err)
	}
	if err := r.Answer(low, q, "main", leaderIs, 0, t0); code(err) != "handed_up" {
		t.Errorf("the leader answers what it handed up: %v", err)
	}
	if err := r.Answer(low, q, "main", contract.Origin{Caller: contract.Worker}, 0, t0); code(err) != "not_yours" {
		t.Errorf("a worker answers: %v", err)
	}
	if done, err := r.Down(up, low, twin, t0); done || err != nil || first.Answer != "" {
		t.Errorf("Down with nothing answered: %v, %v", done, err)
	}
	if _, err := r.Down(up, fleetRun("r9"), twin, t0); code(err) != contract.RefusedNotFleets {
		t.Errorf("Down into another run: %v", err)
	}

	// The orchestrator does not know either: the person answers, takes it
	// back, answers again, and only what has settled goes down.
	must(t, r.Escalate(up, twin, t0))
	must(t, r.Answer(up, twin, "next", human, 4*time.Second, t0))
	if done, _ := r.Down(up, low, twin, after(time.Second)); done || first.Answer != "" || tw.State != contract.QuestionAnswered {
		t.Fatal("an answer went down before it settled")
	}
	must(t, r.Undo(up, twin, human, after(2*time.Second)))
	must(t, r.Answer(up, twin, "main", human, 4*time.Second, after(3*time.Second)))
	halfLow := snapshot(low)
	done, err := r.Down(up, low, twin, after(8*time.Second))
	if !done || err != nil || tw.State != contract.QuestionUsed || first.Answer != "main" || *first.AnsweredBy != human || first.State != contract.QuestionAnswered {
		t.Fatalf("Down: %v, %v, %+v", done, err, first)
	}
	if e := up.Inbox.Events[len(up.Inbox.Events)-1]; e.Kind != "answered" || e.Text != "main" {
		t.Errorf("the orchestrator is not told what the person said: %+v", e)
	}
	if err := r.Undo(up, twin, human, after(9*time.Second)); code(err) != "used" {
		t.Errorf("an answer that went down is taken back: %v", err)
	}
	if answer, ok, err := r.Use(low, q, after(9*time.Second)); !ok || err != nil || answer != "main" || low.Attempts["T1.1"].State != contract.AttemptWorking {
		t.Errorf("the worker's ask: %q, %v, %v", answer, ok, err)
	}
	if e := low.Inbox.Events[len(low.Inbox.Events)-1]; e.Kind != "answered" || e.Text != "main" {
		t.Errorf("the leader is not told what was answered above it: %+v", e)
	}
	if done, err := r.Down(up, low, twin, after(10*time.Second)); done || err != nil {
		t.Errorf("Down a second time: %v, %v", done, err)
	}
	// A command killed between the two writes: the upper run was saved and
	// the lower not. The next sweep copies the answer down.
	var killedUp, killedLow contract.State
	restore(t, snapshot(up), &killedUp)
	restore(t, halfLow, &killedLow)
	if done, err := r.Down(&killedUp, &killedLow, twin, after(time.Minute)); !done || err != nil || killedLow.Questions[q].Answer != "main" {
		t.Errorf("a pair a kill left apart: %v, %v", done, err)
	}

	// The leader's own item: for the orchestrator, holding the group as it
	// held its task, urgent as it was; the leader's wait hands it the answer.
	up, low, _ = scene()
	n, err := r.Need(low, contract.Question{Form: contract.FormSignoff, Text: "ship it?", Task: "T1", Holds: true, Urgent: true}, t0)
	must(t, err)
	twin, err = r.Twin(up, low, n, t0)
	must(t, err)
	if tw := up.Questions[twin]; !tw.Holds || !tw.Urgent || tw.Form != contract.FormSignoff || tw.Task != "G1" || low.Questions[n].For != contract.ForOrchestrator {
		t.Fatalf("the twin of an item: %+v", tw)
	}
	if err := r.Answer(up, twin, "perhaps", orch, 0, t0); code(err) != "bad_answer" {
		t.Errorf("the twin takes an answer its form does not: %v", err)
	}
	must(t, r.Answer(up, twin, "approve", orch, 0, t0))
	if done, err := r.Down(up, low, twin, t0); !done || err != nil || *low.Questions[n].AnsweredBy != orch || len(up.Tasks["G1"].Decisions) != 1 {
		t.Fatalf("an item answered by the orchestrator: %v, %v", done, err)
	}
	_, events2, _, err := r.Batch(low, nil, t0)
	must(t, err)
	if !slices.ContainsFunc(events2, func(e contract.Event) bool { return e.Kind == "answered" && e.Text == "approve" && e.Data["id"] == n }) {
		t.Errorf("the leader's wait is not handed the answer: %+v", events2)
	}

	// The leader's attempt ends with its question open: the twin stays, for
	// the worker below still waits; a next leader that hands the question up
	// again finds the same twin, and nobody is told twice.
	up, low, q = scene()
	twin, _ = r.Twin(up, low, q, t0)
	must(t, r.Stop(up, "G1", false, t0))
	if up.Questions[twin].State != contract.QuestionOpen {
		t.Fatalf("the twin of a leader that ended: %s", up.Questions[twin].State)
	}
	led(t, up, low, "G1", "w1:p6")
	told := len(up.Inbox.Events)
	if again, err := r.Twin(up, low, q, t0); err != nil || again != twin || low.Questions[q].Up.ID != again || len(up.Inbox.Events) != told {
		t.Errorf("handed up again: %q, %v", again, err)
	}
}

func restore(t *testing.T, from string, to *contract.State) {
	t.Helper()
	if err := json.Unmarshal([]byte(from), to); err != nil {
		t.Fatal(err)
	}
}

// Section 7, the caller table's row "leader": in its fleet's run it may what
// an orchestrator may, but for what contract.LeaderNever lists, and it is
// bound to that run as an orchestrator is to its own.
func TestAllowedALeader(t *testing.T) {
	up, low := job(t), fleetRun("r2")
	led(t, up, low, "G1", "w1:p2")
	leader := contract.Caller{Kind: contract.Leader, Pane: "w1:p2", Attempt: "G1.1"}
	for _, row := range []struct {
		s            *contract.State
		command, sub string
		want         string
	}{
		{low, "start", "", ""}, {low, "accept", "", ""}, {low, "task", "add", ""}, {low, "wait", "", ""}, {low, "answer", "", ""},
		{low, "need", "", ""}, {low, "escalate", "", ""}, {low, "tell", "", ""}, {low, "stop", "", ""}, {low, "sync", "", ""},
		{low, "status", "", ""}, {low, "run", "list", ""}, {low, "report", "", ""}, {low, "mail", "", ""},
		{low, "land", "", contract.RefusedLeader}, {low, "run", "new", contract.RefusedLeader}, {low, "run", "close", contract.RefusedLeader},
		{low, "run", "rm", contract.RefusedLeader}, {low, "run", "takeover", contract.RefusedLeader}, {low, "ask", "", contract.RefusedLeader},
		{low, "pause", "", "human_only"}, {low, "resume", "", "human_only"}, {low, "trust", "", "human_only"}, {low, "tell", "lead", "human_only"},
		{low, "set", "", contract.RefusedLeader},
		// The orchestrator's run, and any other, it may read and not change.
		{up, "start", "", "not_bound"}, {up, "accept", "", "not_bound"}, {up, "answer", "", "not_bound"}, {up, "task", "add", "not_bound"},
		{up, "status", "", ""}, {up, "show", "", ""}, {up, "run", "takeover", contract.RefusedLeader}, {up, "land", "", contract.RefusedLeader},
		{nil, "run", "new", contract.RefusedLeader},
	} {
		err := r.Allowed(row.s, leader, row.command, row.sub)
		if code(err) != row.want || row.want != "" && exit(err) != contract.ExitRefused {
			t.Errorf("a leader runs %s %s: %v, want %q", row.command, row.sub, err, row.want)
		}
	}
	for _, never := range contract.LeaderNever {
		command, sub, _ := strings.Cut(never, " ")
		if strings.HasPrefix(sub, "--") {
			continue // a flag: the command line's, which the rules never see
		}
		if err := r.Allowed(low, leader, command, sub); code(err) != contract.RefusedLeader {
			t.Errorf("a leader runs %s: %v", never, err)
		}
	}
	if err := r.Allowed(low, leader, "ask", ""); !strings.Contains(strings.Join(err.(*contract.Refusal).Next, " "), "whaleshark need question") {
		t.Errorf("a leader that asks is not told the line to type: %v", err)
	}
	// A fleet's run takes no group: two levels, never three.
	err := r.AddTask(low, contract.Task{ID: "T1", Title: "one", Check: "true", Fleet: "deeper"}, nil, t0)
	if code(err) != contract.RefusedNotFleets {
		t.Errorf("a leader hands work to a fleet of its own: %v", err)
	}
	must(t, r.Pause(low, "you", t0))
	if err := r.Allowed(low, leader, "start", ""); code(err) != "paused" {
		t.Errorf("a leader starts while its fleet is paused: %v", err)
	}
	// After a takeover the pane that led the fleet leads it no more.
	must(t, r.Resume(low, t0))
	must(t, r.Takeover(low, contract.Binding{Pane: "w1:p9"}, t0))
	if err := r.Allowed(low, leader, "start", ""); code(err) != "not_bound" {
		t.Errorf("a leader whose fleet was taken over: %v", err)
	}
}

// A leader's program ends while a worker's question it passed up waits for
// the person: the question stays in front of the person, the answer goes
// down to the worker as if nothing had happened, and a group that is
// accepted still takes its own questions with it.
func TestAQuestionPassedUpOutlivesItsLeader(t *testing.T) {
	up, low := job(t), fleetRun("r2")
	led(t, up, low, "G1", "w1:p2")
	worker(t, low, "T1")
	q, err := r.Ask(low, "T1.1", "which word?", nil, t0)
	must(t, err)
	twin, err := r.Twin(up, low, q, t0)
	must(t, err)
	must(t, r.Escalate(up, twin, t0))
	must(t, r.Stop(up, "G1", false, after(time.Minute)))
	tw := up.Questions[twin]
	if tw.State != contract.QuestionOpen || tw.For != contract.ForHuman {
		t.Fatalf("the leader's end took the question from the person: %+v", tw)
	}
	// It holds the worker below, not the start of the leader that takes over.
	tw.Holds = true
	if _, err := r.Start(up, "G1", true, false, token, agent, true, after(90*time.Second)); err != nil {
		t.Fatalf("the group is not started again while its question waits: %v", err)
	}
	must(t, r.Answer(up, twin, "alpha", human, 0, after(2*time.Minute)))
	if done, err := r.Down(up, low, twin, after(3*time.Minute)); !done || err != nil || low.Questions[q].Answer != "alpha" {
		t.Fatalf("the answer did not go down after the leader's end: %v, %v, %+v", done, err, low.Questions[q])
	}

	up, low = job(t), fleetRun("r2")
	led(t, up, low, "G1", "w1:p2")
	worker(t, low, "T1")
	q, _ = r.Ask(low, "T1.1", "which word?", nil, t0)
	twin, err = r.Twin(up, low, q, t0)
	must(t, err)
	end(up, up.Attempts["G1.1"], contract.AttemptAccepted, contract.ExitReported, after(time.Minute))
	if up.Questions[twin].State != contract.QuestionClosed {
		t.Errorf("a group that is accepted left its question open: %+v", up.Questions[twin])
	}
}
