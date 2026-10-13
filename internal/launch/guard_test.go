package launch

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/rules"
	"github.com/tgdigital-hub/whaleshark/internal/view"
	"github.com/tgdigital-hub/whaleshark/test/fakeengine"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func plainTask(id string) *contract.Task {
	return &contract.Task{ID: id, Name: "task " + id, Title: "a task of work", Brief: "briefs/" + id + ".md",
		Check: contract.CheckNone, Placement: contract.PlaceShared, Status: contract.TaskReady, CreatedAt: contract.Now()}
}

var hang = map[string]string{"T1.1": "hang", "T2.1": "hang", "T3.1": "hang", "G1.1": "hang", "G2.1": "hang"}

// item is the one open item of a cause in the job's record.
func (m *morning) item(cause string) *contract.Question {
	m.t.Helper()
	var open []*contract.Question
	for _, q := range m.read("r1").Questions {
		if q.Cause == cause && q.State == contract.QuestionOpen {
			open = append(open, q)
		}
	}
	if len(open) != 1 {
		m.t.Fatalf("%d items of cause %s are open, expected one", len(open), cause)
	}
	return open[0]
}

// answer is the person's answer to an item, settled, and used as the sweep
// uses it at its next look: the sweep is another package's.
func (m *morning) answer(id, text string) {
	m.t.Helper()
	m.do(scenario.Human, 0, nil, "", "answer", id, text)
	m.p.Clock(time.Minute)
	err := m.p.Kit.Store.Change(m.p.Root, "r1", func(s *contract.State) error {
		if _, ok, err := (rules.Rules{}).Use(s, id, contract.Now()); err != nil || !ok {
			return fmt.Errorf("the answer to %s was not used: %v", id, err)
		}
		return nil
	})
	if err != nil {
		m.t.Fatal(err)
	}
}

// plan adds to the log what the person approved and what the plan's item holds.
func (m *morning) plan() []string {
	s := m.read("r1")
	m.log = append(m.log, fmt.Sprintf("approved %v", s.Run.Plan.Approved))
	for _, id := range slices.Sorted(func(yield func(string) bool) {
		for id := range s.Questions {
			if !yield(id) {
				return
			}
		}
	}) {
		q := s.Questions[id]
		m.log = append(m.log, fmt.Sprintf("item %s %s %s %v: %s", id, q.Cause, q.State, q.Tasks, q.Text))
	}
	return m.record()
}

// Plan first with Fleet Mode off: nothing starts until the person has
// approved the tasks they were shown, and a task added later is shown again.
func planFirst(t *testing.T, on scenario.Backend) []string {
	m := job(t, on, "[plan]\nfirst = true\n", "", hang, plainTask("T1"), plainTask("T2"))
	p := m.p
	m.do(scenario.Orch, 5, []string{contract.RefusedPlanFirst, "Plan first is on", "whaleshark wait"}, "", "start", "T1", "--json")
	q := m.item(contract.CausePlan)
	if q.Form != contract.FormSignoff || q.From != contract.FromTool || !slices.Equal(q.Tasks, []string{"T1"}) || !strings.HasPrefix(q.Text, `Plan for "a morning with fleets" · 1 task · 1 agent (claude)`) {
		t.Fatalf("the plan's item: %+v", q)
	}
	// The same item, brought up to date; and nothing was started or opened.
	m.do(scenario.Orch, 5, []string{"Plan first is on"}, "", "start", "--ready")
	if again := m.item(contract.CausePlan); again.ID != q.ID || !slices.Equal(again.Tasks, []string{"T1", "T2"}) || !strings.Contains(again.Text, "2 tasks") ||
		len(m.read("r1").Attempts) != 0 || m.tabs("task T1")+m.tabs("task T2") != 0 {
		t.Fatalf("the plan's item after a second start: %+v, attempts %v", again, m.read("r1").Attempts)
	}
	// The yes is the person's own; then the same start passes.
	m.do(scenario.Orch, 5, []string{contract.RefusedPersonOnly}, "", "answer", q.ID, contract.AnswerApprove, "--json")
	m.answer(q.ID, contract.AnswerApprove)
	m.do(scenario.Orch, 0, []string{"(T1.1)", "(T2.1)"}, "", "start", "T1", "T2")
	// A task added after the approval is shown before it starts, and the
	// person's own start of it is their yes.
	if err := p.Kit.Store.Change(p.Root, "r1", func(s *contract.State) error { s.Tasks["T3"] = plainTask("T3"); return nil }); err != nil {
		t.Fatal(err)
	}
	m.do(scenario.Orch, 5, []string{"Plan first is on"}, "", "start", "T3")
	if more := m.item(contract.CausePlan); more.ID == q.ID || !slices.Equal(more.Tasks, []string{"T3"}) || !strings.Contains(more.Text, "1 more task") {
		t.Fatalf("the item for what was added: %+v", more)
	}
	m.do(scenario.Human, 0, []string{"(T3.1)"}, "", "start", "T3")
	if plan := m.read("r1").Run.Plan; !slices.Equal(plan.Approved, []string{"T1", "T2", "T3"}) || plan.By == nil || plan.By.Caller != contract.Human {
		t.Fatalf("the person's own start did not approve: %+v", plan)
	}
	return m.plan()
}

// Plan first with Fleet Mode on: the person approves the orchestrator's
// groups, and a leader's own start in its fleet's run is not held.
func planFirstFleets(t *testing.T, on scenario.Backend) []string {
	m := job(t, on, fleetsOn+"[plan]\nfirst = true\n", contract.ModeFleets, hang, group("G1", "billing"), group("G2", "search"), group("G3", "billing"))
	m.do(scenario.Orch, 5, []string{contract.RefusedPlanFirst}, "", "start", "G1", "G2", "--json")
	q := m.item(contract.CausePlan)
	if !slices.Equal(q.Tasks, []string{"G1", "G2"}) || !strings.Contains(q.Text, "2 fleets · 2 things · up to 6 agents (your limit: 6)") || len(m.read("r1").Run.Fleets) != 0 {
		t.Fatalf("the orchestrator's plan: %+v", q)
	}
	m.answer(q.ID, contract.AnswerApprove)
	m.do(scenario.Orch, 0, []string{"(G1.1)"}, "", "start", "G1")
	m.do(scenario.Orch, 0, []string{"(G2.1)"}, "", "start", "G2")
	m.do("G1.1", 0, nil, asLeader, "task", "add", "T1", "bill T1", "--brief", "brief.md", "--check", "none")
	m.do("G1.1", 0, []string{"(T1.1)"}, asLeader, "start", "T1")
	if low := m.read("r2"); low.Run.Plan != nil || low.Attempts["T1.1"].State != contract.AttemptWorking {
		t.Fatalf("the leader's start in its fleet's run was held: %+v", low.Run.Plan)
	}
	// One more group, for a fleet whose leader sits, is shown all the same.
	m.do(scenario.Orch, 5, []string{"Plan first is on"}, "", "start", "G3")
	if more := m.item(contract.CausePlan); !slices.Equal(more.Tasks, []string{"G3"}) || !strings.Contains(more.Text, "1 more thing for billing") || m.read("r1").Attempts["G3.1"] != nil {
		t.Fatalf("the item for the group that was added: %+v", more)
	}
	return m.plan()
}

func TestPlanFirstThenStart(t *testing.T)            { onBoth(t, planFirst) }
func TestPlanFirstInAJobOfFleets(t *testing.T)       { onBoth(t, planFirstFleets) }
func TestAnAgentHourReachedHoldsTheJob(t *testing.T) { onBoth(t, oneAgentHour) }

// With the setting as it comes a start asks nobody and writes no plan.
func TestPlanFirstOffChangesNothing(t *testing.T) {
	m := job(t, scenario.Double, "", "", hang, plainTask("T1"))
	m.do(scenario.Orch, 0, []string{"(T1.1)"}, "", "start", "T1")
	if s := m.read("r1"); s.Run.Plan != nil || len(s.Questions) != 0 {
		t.Errorf("a start with plan first off wrote %+v and %d items", s.Run.Plan, len(s.Questions))
	}
}

// sweep is the sweep's look at what the job has spent, as its own package
// will make it: the rule on the job's record, then the kit's Hold.
func (m *morning) sweep() string {
	m.t.Helper()
	p, did := m.p, ""
	dirs, err := p.Kit.Platform.Dirs()
	if err == nil {
		var person contract.PersonConfig
		if person, err = contract.ReadPerson(p.Kit.Platform.Peek, dirs.Config); err == nil {
			err = p.Kit.Store.Change(p.Root, "r1", func(s *contract.State) error {
				now := contract.Now()
				did = rules.Rules{}.Spend(s, "", contract.Spent(now, s), person.SpendLimit(s.Run.Spend, false), now)
				return nil
			})
		}
	}
	if err == nil {
		err = p.Kit.Hold(p.Kit, p.Root, "r1")
	}
	if err != nil {
		m.t.Fatal(err)
	}
	return did
}

// A limit of one agent-hour, reached with one agent in the middle of its
// work and one that has reported: the job is held, the hold file lies in
// its folder for the gate, the reported agent's tab is closed and the other
// stays; start and resume are refused; more hours from the person take the
// file away and wake the agent that was held.
func oneAgentHour(t *testing.T, on scenario.Backend) []string {
	m := job(t, on, "[spend]\nguard = true\njob = 1\n", "", hang, plainTask("T1"), plainTask("T2"), plainTask("T3"))
	p := m.p
	p.Plug(func(k *contract.Kit) {
		for _, plug := range []func(*contract.Kit){rules.Plug, view.Plug, Plug} {
			plug(k)
		}
	})
	hold := contract.HoldPath(p.Root, "r1")
	held := func() bool { _, err := os.Stat(hold); return err == nil }
	gate := func() string {
		cmd := p.Command("T1.1", "hook", "gate")
		cmd.Stdin = strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"echo on"}}`)
		out, _ := run(t, cmd)
		return out
	}

	m.do(scenario.Orch, 0, []string{"(T1.1)", "(T2.1)"}, "", "start", "T1", "T2")
	if err := os.WriteFile(filepath.Join(contract.AttemptDir(p.Kit.Store.Dir(p.Root, "r1"), "T2.1"), "result.md"), []byte("It is written.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.do("T2.1", 0, nil, "", "report", "done", "It is written. It was read. Nothing is left open.")
	if did := m.sweep(); did != "" || held() || gate() != "" {
		t.Fatalf("under the limit the sweep said %q, a hold file: %t, the gate: %q", did, held(), gate())
	}

	// Two agents open for thirty-one minutes each are over one agent-hour.
	p.Clock(31 * time.Minute)
	if did := m.sweep(); did != contract.SpendHeld || !held() {
		t.Fatalf("at the limit the sweep said %q, a hold file: %t", did, held())
	}
	s := m.read("r1")
	if !guarded(s) || s.Attempts["T1.1"].State != contract.AttemptWorking || s.Attempts["T2.1"].State != contract.AttemptReported || m.tabs("task T1") != 1 || m.tabs("task T2") != 0 {
		t.Fatalf("held: paused %+v, T1.1 %s with %d tabs, T2.1 %s with %d tabs", s.Run.Paused, s.Attempts["T1.1"].State, m.tabs("task T1"), s.Attempts["T2.1"].State, m.tabs("task T2"))
	}
	if out := gate(); !strings.Contains(out, `"continue":false`) || !strings.Contains(out, "agent-hours") {
		t.Fatalf("the gate of the agent at work said %q", out)
	}
	q := m.item(contract.CauseBudget)
	m.do(scenario.Orch, 5, []string{contract.RefusedOverBudget, "Nothing is started", "whaleshark wait"}, "", "start", "T3", "--json")
	m.do(scenario.Human, 5, []string{contract.RefusedOverBudget, "Nothing is resumed", "whaleshark answer " + q.ID}, "", "resume", "--json")

	// Stop all over the hold, and its resume, leave the guard's hold standing.
	m.do(scenario.Human, 0, []string{"Paused."}, "", "pause")
	m.do(scenario.Human, 0, []string{"Resumed. 0 agents"}, "", "resume")
	if !guarded(m.read("r1")) || !held() || len(m.read("r1").Attempts["T1.1"].Mail) != 0 {
		t.Fatalf("a resume of Stop all lifted the guard's hold: %+v", m.read("r1").Run.Paused)
	}

	// A hold file that was taken away is back at the sweep's next look.
	if err := os.Remove(hold); err != nil {
		t.Fatal(err)
	}
	if did := m.sweep(); did != "" || !held() {
		t.Fatalf("the sweep after the file was removed said %q, a hold file: %t", did, held())
	}

	// More hours: the hold ends in the record, the file goes, the agent
	// that was held is told to carry on, and a start passes again.
	m.answer(q.ID, contract.AnswerMore)
	if did := m.sweep(); did != "" || held() || gate() != "" {
		t.Fatalf("after more hours the sweep said %q, a hold file: %t, the gate: %q", did, held(), gate())
	}
	s = m.read("r1")
	if mail := s.Attempts["T1.1"].Mail; s.Run.Paused != nil || s.Run.Spend.More != 1 || len(mail) != 1 || !strings.HasPrefix(mail[0].Text, "You were paused at ") || (p.Double != nil && mail[0].NudgedAt.IsZero()) || len(s.Attempts["T2.1"].Mail) != 0 {
		t.Fatalf("after more hours: paused %+v, spend %+v, mail %+v", s.Run.Paused, s.Run.Spend, mail)
	}
	// (On the real keeper the line is typed too, and the scripted agent,
	// which only waits, does not take it up: the keeper says so after its wait.)
	m.sweep() // and no second time
	m.do(scenario.Orch, 0, []string{"(T3.1)"}, "", "start", "T3")
	if p.Double != nil {
		var typed []string
		for _, c := range p.Double.Calls() {
			if line := fakeengine.Typed(c); c.Op == contract.OpPoint && line != "" {
				typed = append(typed, line)
			}
		}
		if want := []string{string(contract.PointResumed), string(contract.PointLeadOn)}; !slices.Equal(typed, want) {
			t.Errorf("typed after the hold: %q, expected %q", typed, want)
		}
	}
	s = m.read("r1")
	m.log = append(m.log, fmt.Sprintf("spend %+v, item %s %s %q", s.Run.Spend, q.ID, s.Questions[q.ID].State, s.Questions[q.ID].Answer))
	return m.record()
}
