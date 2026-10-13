package inbox

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/rules"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// played is evening with fake agents playing scripts in the attempts' tabs.
func played(t *testing.T, edit func(f *testkit.Fixture), scripts map[string]string) *world {
	t.Helper()
	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	edit(f)
	w := &world{T: t, p: scenario.Prepare(t, f, scripts)}
	_, w.dir = w.p.Record()
	return w
}

// person writes the person's own settings file.
func (w *world) person(text string) {
	w.Helper()
	d, err := w.p.Kit.Platform.Dirs()
	if err == nil {
		err = os.MkdirAll(d.Config, 0o700)
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(d.Config, contract.ConfigFile), []byte(text), 0o600)
	}
	if err != nil {
		w.Fatal(err)
	}
}

// stuckEvents are the stuck events about one attempt or, in a job's run,
// about one task of a fleet.
func stuckEvents(s *contract.State, attempt, task string) (out []contract.Event) {
	for _, e := range s.Inbox.Events {
		if below, _ := e.Data["task"].(string); e.Kind == "stuck" && (attempt != "" && e.Attempt == attempt || task != "" && below == task) {
			out = append(out, e)
		}
	}
	return out
}

// The row's first check: each of the seven signs is made to happen, by an
// agent that plays a script in its tab and by what the tool's own commands
// count, and each alone is the verdict in the end: a soft one after twice
// its span, a hard one at once. The lead agent is told once an episode.
func TestEachSignOfAnAgentGoingNowhere(t *testing.T) {
	const same = `progress 50 "wiring the form"`
	notes := func(w *world, n int, every time.Duration) {
		for i := range n {
			w.run("W0.1", "progress", fmt.Sprint(10+i), fmt.Sprint("step ", i, " is done"))
			w.after(every)
		}
	}
	for _, sign := range []struct {
		name, script string
		happen       func(w *world)
	}{
		{"stale", "sleep 600", func(w *world) {
			if w.after(31 * time.Minute); w.state().Attempts["W0.1"].Seen.StuckWhy != "" {
				t.Fatal("one missed note is stale, and not stuck")
			}
			w.after(30 * time.Minute)
		}},
		{"quiet", "hang", func(w *world) {
			w.until("the agent to rest", func() bool {
				return slices.ContainsFunc(w.picture().Panes, func(p contract.Pane) bool { return p.ID == w.pane("W0.1") && p.Status == contract.StatusIdle })
			})
			if w.after(13 * time.Minute); w.state().Attempts["W0.1"].Seen.StuckWhy != "" {
				t.Fatal("at rest for thirteen minutes is quiet, and not stuck")
			}
			w.after(12 * time.Minute)
		}},
		{"overtime", "sleep 600", func(w *world) {
			if notes(w, 3, 20*time.Minute); w.state().Attempts["W0.1"].Seen.StuckWhy != "" {
				t.Fatal("an hour of a task with 45 minutes is over time, and not stuck")
			}
			notes(w, 2, 20*time.Minute)
		}},
		{"same", strings.Repeat(same+" | ", 6) + "sleep 600", func(w *world) {
			w.until("the sixth note", func() bool { p := w.state().Attempts["W0.1"].Progress; return p != nil && p.Same == 5 })
			w.after(5 * time.Second)
		}},
		{"forgot", "sleep 600", func(w *world) {
			d, _ := w.p.Kit.Platform.Dirs()
			for i := range 8 {
				ctx := contract.CtxFile{Pct: 80 - 60*(i%2), Known: true, At: contract.Now()}
				if err := contract.WriteVersioned(w.p.Kit.Platform, contract.CtxPath(d.State, w.pane("W0.1")), contract.FileVersion, ctx); err != nil {
					t.Fatal(err)
				}
				if w.after(5 * time.Second); i == 3 && w.state().Attempts["W0.1"].Seen.StuckWhy != "" {
					t.Fatal("a memory emptied twice is a sign, and alone not stuck")
				}
			}
		}},
		// The two hard ones are counted where the fact happens: by a check
		// that failed, and by a result sent back.
		{"check", "sleep 600", func(w *world) {
			w.change(func(s *contract.State, _ time.Time) error { s.Tasks["W0"].CheckFails = contract.StuckTimes; return nil })
			w.after(5 * time.Second)
		}},
		{"back", "sleep 600", func(w *world) {
			w.change(func(s *contract.State, _ time.Time) error { s.Attempts["W0.1"].Round = contract.StuckTimes; return nil })
			w.after(5 * time.Second)
		}},
	} {
		t.Run(sign.name, func(t *testing.T) {
			w := played(t, crew(2), map[string]string{"W0.1": sign.script, "W1.1": "sleep 600"})
			w.T = t
			// Nothing else is to hold beside the sign.
			limits := map[string]string{"stale": "stale_minutes = 30", "overtime": "stale_minutes = 30\nmax_minutes = 45"}[sign.name]
			if err := os.WriteFile(filepath.Join(w.p.Root, "whaleshark.toml"), []byte("[limits]\n"+cmp.Or(limits, "stale_minutes = 600")+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if w.sweep(); len(stuckEvents(w.state(), "W0.1", "")) != 0 {
				t.Fatal("stuck before anything happened")
			}
			sign.happen(w)
			s := w.state()
			a, told := s.Attempts["W0.1"], stuckEvents(s, "W0.1", "")
			if a.Seen.StuckWhy != sign.name || a.Seen.StuckTold != contract.ToldLeader || len(told) != 1 || told[0].Data["why"] != sign.name ||
				!strings.Contains(told[0].Text, contract.StuckWhy(sign.name)) || a.State != contract.AttemptWorking {
				t.Fatalf("the sign %s: why %q, told %d, %+v", sign.name, a.Seen.StuckWhy, a.Seen.StuckTold, told)
			}
			w.after(5 * time.Second)
			w.after(5 * time.Second)
			if s := w.state(); len(stuckEvents(s, "W0.1", "")) != 1 || s.Attempts["W0.1"].Seen != a.Seen {
				t.Fatalf("a verdict that stands was told or written again: %+v", s.Attempts["W0.1"].Seen)
			}
		})
	}
}

// With the detector off nothing is judged: the record of an agent with a
// hard sign is as it was before phase 6.
func TestTheDetectorOffJudgesNobody(t *testing.T) {
	w := evening(t, crew(2))
	w.person("[stuck]\nwatch = false\n")
	w.change(func(s *contract.State, _ time.Time) error { s.Tasks["W0"].CheckFails = contract.StuckTimes; return nil })
	w.sweep()
	w.after(time.Minute)
	if s := w.state(); s.Attempts["W0.1"].Seen.StuckWhy != "" || len(stuckEvents(s, "W0.1", "")) != 0 {
		t.Fatalf("judged with the detector off: %+v", s.Attempts["W0.1"].Seen)
	}
}

// The row's second check. A worker of billing is stuck: its leader is told
// in the fleet's run; ten minutes later the orchestrator, in the job's run
// and by the same write; ten minutes later the person, by a to-do there. A
// tell in between is somebody dealing with it, and the climb waits its ten
// minutes again. When the verdict clears, the person's item is withdrawn.
func TestAClimbThroughThreeLevelsAndOneCutShortByATell(t *testing.T) {
	w := morning(t)
	k := w.p.Kit
	seen := func() (a *contract.Attempt, above []contract.Event, todo *contract.Question) {
		up := w.state()
		for _, q := range up.Questions {
			if q.Cause == contract.CauseStuck && q.Fleet == "billing" && q.Attempt == "T3.1" {
				todo = q
			}
		}
		return w.of("r2").Attempts["T3.1"], stuckEvents(up, "", "T3"), todo
	}
	w.in("r2", func(s *contract.State, _ time.Time) { s.Tasks["T3"].CheckFails = contract.StuckTimes })
	w.low("r2", 5*time.Second)
	a, above, todo := seen()
	if below := stuckEvents(w.of("r2"), "T3.1", ""); a.Seen.StuckTold != contract.ToldLeader || len(below) != 1 || len(above) != 0 || todo != nil {
		t.Fatalf("the verdict falls: told %d, %d below, %d above, %+v", a.Seen.StuckTold, len(below), len(above), todo)
	}
	if w.low("r2", 9*time.Minute); w.of("r2").Attempts["T3.1"].Seen.StuckTold != contract.ToldLeader {
		t.Fatal("it climbed before its ten minutes")
	}
	// One sweep writes the level below and the event above.
	w.low("r2", time.Minute)
	a, above, todo = seen()
	if a.Seen.StuckTold != contract.ToldOrchestrator || len(above) != 1 || above[0].Data["fleet"] != "billing" || above[0].Task != "G1" || todo != nil {
		t.Fatalf("one level up: told %d, %+v, %+v", a.Seen.StuckTold, above, todo)
	}
	// The leader tells its worker something nine minutes later: the next
	// level hears ten minutes after that, and not one minute after.
	w.p.Clock(9 * time.Minute)
	w.in("r2", func(s *contract.State, now time.Time) {
		if _, err := k.Rules.Tell(s, "T3", "the check wants port 8081", "", now); err != nil {
			t.Fatal(err)
		}
	})
	w.low("r2", 2*time.Minute)
	if a, above, todo = seen(); a.Seen.StuckTold != contract.ToldOrchestrator || len(above) != 1 || todo != nil {
		t.Fatalf("a tell did not cut the climb short: told %d, %d above, %+v", a.Seen.StuckTold, len(above), todo)
	}
	w.low("r2", 9*time.Minute)
	a, above, todo = seen()
	if a.Seen.StuckTold != contract.ToldPerson || todo == nil || todo.State != contract.QuestionOpen || todo.Urgent || todo.Task != "G1" ||
		!strings.Contains(todo.Text, "billing") || len(above) != 1 {
		t.Fatalf("the person: told %d, %+v, %d above", a.Seen.StuckTold, todo, len(above))
	}
	// Nothing climbs further, and nothing is written while it stands.
	before, below := w.file(contract.StateFile), w.of("r2").Counters.Seq
	if w.low("r2", 30*time.Minute); string(before) != string(w.file(contract.StateFile)) || len(stuckEvents(w.of("r2"), "T3.1", "")) != 1 {
		t.Fatalf("a verdict the person holds was told again (%d events below then, %d now)", below, w.of("r2").Counters.Seq)
	}
	// The check passes and the worker says so: the episode ends below and
	// the item goes above.
	w.in("r2", func(s *contract.State, now time.Time) {
		if s.Tasks["T3"].CheckFails = 0; k.Rules.Progress(s, "T3.1", 90, "the check passes on port 8081", now) != nil {
			t.Fatal("no progress note taken")
		}
	})
	w.low("r2", 5*time.Second)
	if a, _, todo = seen(); !a.Seen.StuckSince.IsZero() || a.Seen.StuckTold != 0 || todo.State != contract.QuestionClosed {
		t.Fatalf("the verdict cleared: %+v, the item is %s", a.Seen, todo.State)
	}
}

// afternoon is the test kit's afternoon with every guard on, as a project on
// disk: the job r7 is the current run, billing (r8) and search (r9) lie
// beside it, and every agent that is open has a pane of the engine's double.
// held lists the runs the kit's Hold was called for.
func afternoon(t *testing.T, config string) (w *world, held *[]string) {
	t.Helper()
	g := testkit.Guarded()
	snap := &contract.Snapshot{At: g.Now, Instance: "k1", Panes: []contract.Pane{{ID: "w1:p1", Tab: "w1:t1", Agent: "claude", Status: contract.StatusWorking}}}
	for _, s := range []*contract.State{g.Up, g.Low["billing"], g.Low["search"]} {
		for _, id := range slices.Sorted(func(yield func(string) bool) {
			for id := range s.Attempts {
				if !yield(id) {
					return
				}
			}
		}) {
			if a := s.Attempts[id]; a.State.Live() {
				a.Seen.LastWorking = g.Now
				snap.Panes = append(snap.Panes, contract.Pane{ID: a.Place.Pane, Tab: a.Place.Tab, Agent: a.Agent.Kind, Status: contract.StatusWorking})
			}
		}
	}
	f := &testkit.Fixture{Version: testkit.FixtureVersion, Name: "every guard on", Now: g.Now, Checked: g.Now, State: *g.Up, Terms: snap}
	w = &world{T: t, p: scenario.Prepare(t, f, nil)}
	for _, low := range g.Low {
		if err := w.p.Kit.Store.Create(w.p.Root, low); err != nil {
			t.Fatal(err)
		}
	}
	rules.Plug(w.p.Kit)
	_, w.dir = w.p.Record()
	held = new([]string)
	w.p.Kit.Hold = func(_ *contract.Kit, _, run string) error { *held = append(*held, run); return nil }
	if config != "" {
		w.person(config)
	}
	return w, held
}

// job sweeps the job's run in this process, where the kit's Hold is the test's.
func (w *world) job(by time.Duration) contract.Swept {
	w.Helper()
	w.p.Clock(by)
	swept, _, err := (sweeper{w.p.Kit}).sweep(w.p.Root, "r7", contract.Now(), true)
	if err != nil {
		w.Fatal(err)
	}
	return swept
}

func heldBy(s *contract.State) bool { return s.Run.Paused != nil && s.Run.Paused.By == contract.HeldBy }

// At four fifths the orchestrator is told to wrap up, and one that rests
// without waiting has that pointed out by a line in its pane: the warning
// is of no use unread until the limit.
func TestTheWarningAtFourFifthsIsPointedOut(t *testing.T) {
	w, _ := afternoon(t, "[spend]\nguard = true\njob = 41\n")
	w.p.Double.Drop(contract.StatusIdle, w.p.Lead)
	// The afternoon as it was before anybody was warned.
	w.in("r7", func(s *contract.State, _ time.Time) {
		s.Run.Spend.Warned = false
		s.Inbox.Events = slices.DeleteFunc(s.Inbox.Events, func(e contract.Event) bool { return e.Kind == "budget" })
	})
	w.job(time.Minute)
	s := w.state()
	if !s.Run.Spend.Warned || heldBy(s) || !slices.Contains(kinds(s.Inbox.Events, ""), "budget") {
		t.Fatalf("at 33 of 41 agent-hours: %+v, %v", s.Run.Spend, kinds(s.Inbox.Events, ""))
	}
	if n := w.typed(w.p.Lead, fmt.Sprintf(string(contract.PointEvents), fmt.Sprint(len(s.Inbox.Events)))); n != 1 {
		t.Fatalf("the pointer to the warning was typed %d times", n)
	}
}

// The afternoon has used 33 of its 40 agent-hours with six agents open. At
// the limit the job and both fleets are held, the hold is handed to the
// kit for each run, and the person is asked; every look at the held job
// hands the hold on again and writes nothing; and the person's "more",
// once settled, lets everything go.
func TestTheSpendingGuardHoldsAJobAndLetsItGo(t *testing.T) {
	w, held := afternoon(t, "[spend]\nguard = true\njob = 40\n")
	// The kit looks at each run's hold file at every look, held or not: one
	// left behind is taken away.
	if w.job(time.Hour); heldBy(w.state()) || len(*held) != 3 || item(w.state(), contract.CauseBudget) != nil {
		t.Fatalf("held at 39 of 40 agent-hours: %+v, %v", w.state().Run.Spend, *held)
	}
	*held = nil
	w.job(15 * time.Minute)
	s, q := w.state(), item(w.state(), contract.CauseBudget)
	if !s.Run.Spend.Held || !heldBy(s) || !heldBy(w.of("r8")) || !heldBy(w.of("r9")) || q == nil || !q.Urgent || q.Form != contract.FormChoice {
		t.Fatalf("at the limit: %+v, paused %+v, billing %+v, search %+v, %+v", s.Run.Spend, s.Run.Paused, w.of("r8").Run.Paused, w.of("r9").Run.Paused, q)
	}
	if last := s.Inbox.Events[len(s.Inbox.Events)-1]; !slices.Contains(kinds(s.Inbox.Events, ""), "budget") || slices.Sorted(slices.Values(*held))[0] != "r7" || len(*held) != 3 {
		t.Fatalf("the lead agent was handed %+v and the kit %v", last, *held)
	}
	// A hold file somebody removed is the kit's to put back, at every look;
	// the record is not written for it.
	before, lowBefore := w.file(contract.StateFile), w.of("r8")
	*held = nil
	if swept := w.job(5 * time.Second); swept.Changed || string(before) != string(w.file(contract.StateFile)) || len(*held) != 3 || !lowBefore.Run.Paused.At.Equal(w.of("r8").Run.Paused.At) {
		t.Fatalf("a look at a held job: changed %v, the kit %v", swept.Changed, *held)
	}
	// Leave it held: final for nobody, and no write at any sweep after it.
	person := contract.Origin{Caller: contract.Human, Where: contract.WherePane}
	answer := func(text string) {
		w.in("r7", func(s *contract.State, now time.Time) {
			if err := w.p.Kit.Rules.Answer(s, q.ID, text, person, 4*time.Second, now); err != nil {
				t.Fatal(err)
			}
		})
	}
	answer(contract.AnswerLeave)
	w.job(5 * time.Second)
	before = w.file(contract.StateFile)
	if swept := w.job(5 * time.Second); swept.Changed || string(before) != string(w.file(contract.StateFile)) || !heldBy(w.state()) {
		t.Fatal("a hold that was left standing was written again, or let go")
	}
	// More hours: not before the answer has settled, and then at once.
	w.in("r7", func(s *contract.State, now time.Time) {
		if err := w.p.Kit.Rules.Undo(s, q.ID, person, now); err != nil {
			t.Fatal(err)
		}
	})
	answer(contract.AnswerMore)
	if w.job(time.Second); !heldBy(w.state()) {
		t.Fatal("the hold went before the answer had settled")
	}
	*held = nil
	w.job(5 * time.Second)
	s = w.state()
	if s.Run.Spend.Held || s.Run.Spend.More != 8 || s.Run.Paused != nil || w.of("r8").Run.Paused != nil || w.of("r9").Run.Paused != nil ||
		s.Questions[q.ID].State != contract.QuestionUsed || len(*held) != 3 {
		t.Fatalf("eight more agent-hours: %+v, paused %+v, the item %s, the kit %v", s.Run.Spend, s.Run.Paused, s.Questions[q.ID].State, *held)
	}
	if rested := w.of("r9").Attempts["T1.1"].Rested; rested <= 0 {
		t.Fatalf("the time under the hold is not left out of what was spent: rested %v", rested)
	}
}

// One fleet at its own limit: only its run is held, the job and the other
// fleet go on, and the choice names the fleet's group.
func TestOneFleetAtItsOwnLimit(t *testing.T) {
	w, held := afternoon(t, "[spend]\nguard = true\njob = 100\nfleet = 14\n")
	if w.job(5 * time.Second); w.of("r9").Run.Paused != nil || w.state().Run.Fleets["search"].Spend.Held {
		t.Fatalf("search held at 13.8 of 14 agent-hours: %v", *held)
	}
	w.job(30 * time.Minute)
	s, q := w.state(), item(w.state(), contract.CauseBudget)
	if !s.Run.Fleets["search"].Spend.Held || s.Run.Fleets["billing"].Spend.Held || s.Run.Paused != nil || !heldBy(w.of("r9")) || w.of("r8").Run.Paused != nil ||
		q == nil || q.Task != "G2" || !slices.Contains(*held, "r9") {
		t.Fatalf("search at its limit: %+v, the job %+v, billing %+v, %+v, the kit %v", s.Run.Fleets["search"].Spend, s.Run.Paused, w.of("r8").Run.Paused, q, *held)
	}
	// The person switches the guard off: the hold goes without a word, and
	// the fleet's run with it.
	w.person("[spend]\nguard = false\n")
	*held = nil
	w.job(5 * time.Second)
	if s = w.state(); s.Run.Fleets["search"].Spend.Held || w.of("r9").Run.Paused != nil || !slices.Contains(*held, "r9") || s.Questions[q.ID].State != contract.QuestionClosed {
		t.Fatalf("the guard switched off: %+v, search %+v, the kit %v", s.Run.Fleets["search"].Spend, w.of("r9").Run.Paused, *held)
	}
}

// A guard that is off changes nothing: a job far beyond every limit is
// swept as before phase 6, with no word of spending anywhere.
func TestTheSpendingGuardOffChangesNothing(t *testing.T) {
	w, held := afternoon(t, "")
	was := w.state()
	w.job(5 * time.Second)
	w.job(100 * time.Hour)
	s := w.state()
	budget := func(s *contract.State) (n int) {
		for _, kind := range kinds(s.Inbox.Events, "") {
			if kind == "budget" || kind == "paused" {
				n++
			}
		}
		return n
	}
	// The kit is asked to look at each run's hold file all the same: with no
	// hold and no file that is one read, and a file somebody left is gone.
	if s.Run.Spend != was.Run.Spend || s.Run.Paused != nil || w.of("r8").Run.Paused != nil || len(*held) != 6 || item(s, contract.CauseBudget) != nil || budget(s) != budget(was) {
		t.Fatalf("the guard is off: %+v, paused %+v, the kit %v, events %v", s.Run.Spend, s.Run.Paused, *held, kinds(s.Inbox.Events, ""))
	}
}

// A setting an agent asked for is changed when the person's "change" has
// settled, by the sweep and as the person; "leave" changes nothing.
func TestASettingIsChangedOnceThePersonSaysSo(t *testing.T) {
	w, _ := afternoon(t, "[spend]\nguard = true\njob = 100\n")
	guard := func() bool {
		d, _ := w.p.Kit.Platform.Dirs()
		c, err := contract.ReadPerson(w.p.Kit.Platform.Peek, d.Config)
		if err != nil {
			t.Fatal(err)
		}
		return c.Spend.Guard
	}
	w.in("r7", func(s *contract.State, now time.Time) {
		if err := w.p.Kit.Rules.Answer(s, "n3", contract.AnswerChange, contract.Origin{Caller: contract.Human, Where: contract.WherePane}, 4*time.Second, now); err != nil {
			t.Fatal(err)
		}
	})
	if w.job(time.Second); !guard() || w.state().Questions["n3"].State != contract.QuestionAnswered {
		t.Fatal("the setting changed while the answer could still be taken back")
	}
	if w.job(5 * time.Second); guard() || w.state().Questions["n3"].State != contract.QuestionUsed {
		t.Fatalf("the person said change: the guard is %v and the item %s", guard(), w.state().Questions["n3"].State)
	}
}

// A yes whose change cannot be made is said to the lead agent: the item is
// used and the setting is as it was, and nobody is left to guess.
func TestAGrantedChangeThatFailsIsSaid(t *testing.T) {
	w, _ := afternoon(t, "[spend]\nguard = true\njob = 100\n")
	w.in("r7", func(s *contract.State, now time.Time) {
		if err := w.p.Kit.Rules.Answer(s, "n3", contract.AnswerChange, contract.Origin{Caller: contract.Human, Where: contract.WherePane}, 4*time.Second, now); err != nil {
			t.Fatal(err)
		}
	})
	d, _ := w.p.Kit.Platform.Dirs()
	if err := os.WriteFile(filepath.Join(d.Config, contract.ConfigFile), []byte("[spend\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.job(5 * time.Second)
	s := w.state()
	if e := s.Inbox.Events[len(s.Inbox.Events)-1]; s.Questions["n3"].State != contract.QuestionUsed || e.Kind != "answered" || !strings.HasPrefix(e.Text, "spend.guard was not changed, though the person said yes: ") {
		t.Fatalf("the item is %s and the last event %+v", s.Questions["n3"].State, e)
	}
}

// clash is a scan that finds what it is told to.
type clash []contract.Finding

func (c clash) Scan(context.Context, string, *contract.State, []string, bool) ([]contract.Finding, error) {
	return c, nil
}

// A new clash across fleets is one event in each of the two fleets' runs,
// beside the orchestrator's own, and never a second one.
func TestBothLeadersAreToldOfAClashAcrossFleets(t *testing.T) {
	w := morning(t)
	k := w.p.Kit
	k.Overlap = clash{{Kind: "overlap", Tasks: []string{"G1", "G2"}, Files: []string{"cart.js", "api.js", "tax.js"}, How: "billing's T3 and search: content", Work: []string{"T3", ""}},
		{Kind: "overlap", Tasks: []string{"G1"}, Files: []string{"cart.js"}, How: "one fleet alone"}}
	told := func(run string) (out []contract.Event) {
		for _, e := range w.of(run).Inbox.Events {
			if e.Kind == "overlap" {
				out = append(out, e)
			}
		}
		return out
	}
	for range 2 {
		w.in("r1", func(s *contract.State, _ time.Time) { s.Run.Scan.Due = true })
		scan(k, w.p.Root, "r1", contract.Now())
	}
	billing, search := told("r2"), told("r3")
	if len(told("r1")) != 2 || len(billing) != 1 || len(search) != 1 || len(told("r4")) != 0 {
		t.Fatalf("told: %d above, %d billing, %d search, %d help pages", len(told("r1")), len(billing), len(search), len(told("r4")))
	}
	if want := "your T3 and search's collected work both change cart.js and 2 more; search's leader has been told too"; billing[0].Text != want || billing[0].Task != "T3" {
		t.Fatalf("billing's leader reads %q about %q", billing[0].Text, billing[0].Task)
	}
	if want := "your collected work and billing's T3 both change cart.js and 2 more; billing's leader has been told too"; search[0].Text != want || search[0].Task != "" {
		t.Fatalf("search's leader reads %q about %q", search[0].Text, search[0].Task)
	}
}
