package rules

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
)

var (
	relayed = contract.Origin{Caller: contract.Orchestrator, Where: contract.WhereRelayed}
	agents  = []contract.Origin{orch, relayed, {Caller: contract.Leader}, {Caller: contract.Worker}, {Caller: contract.Unbound}, {Caller: contract.Human, Where: contract.WhereRelayed}}
)

// the one item of a cause that is in view, or nil.
func itemOf(s *contract.State, cause string) *contract.Question {
	var found *contract.Question
	for _, q := range questions(s) {
		if q.Cause == cause && q.State != contract.QuestionClosed {
			found = q
		}
	}
	return found
}

func told(s *contract.State) string {
	var out []string
	for _, e := range s.Inbox.Events {
		out = append(out, e.Kind)
	}
	return strings.Join(out, " ")
}

// decide answers as the person and lets the answer settle and be used.
func settle(t *testing.T, s *contract.State, id, text string, at time.Time) string {
	t.Helper()
	must(t, r.Answer(s, id, text, human, 4*time.Second, at))
	if _, ok, _ := r.Use(s, id, at.Add(3*time.Second)); ok {
		t.Fatalf("%s was used before it settled", id)
	}
	answer, ok, err := r.Use(s, id, at.Add(4*time.Second))
	if must(t, err); !ok {
		t.Fatalf("%s did not settle", id)
	}
	return answer
}

// noAgent holds that no caller but the person answers, passes on or closes
// an item, and that nothing changed by the trying.
func noAgent(t *testing.T, s *contract.State, id, text string) {
	t.Helper()
	before := snapshot(s)
	for _, by := range agents {
		if got := code(r.Answer(s, id, text, by, 0, t0)); got != contract.RefusedPersonOnly && got != "not_yours" {
			t.Errorf("%s answered by %+v: %q", id, by, got)
		}
	}
	if got := code(r.CloseQuestion(s, id, t0)); got != contract.RefusedPersonOnly {
		t.Errorf("closing %s: %q", id, got)
	}
	if got := code(r.Undo(s, id, orch, t0)); got == "" {
		t.Errorf("an agent took an answer of %s back", id)
	}
	if _, ok, _ := r.Use(s, id, t0.Add(time.Hour)); ok || snapshot(s) != before {
		t.Errorf("%s changed under an agent's hands", id)
	}
}

// 20.1, its security table. The approval is the person's alone and covers
// what was shown; the text is data; with the setting off nothing is asked.
func TestPlanFirst(t *testing.T) {
	s := newRun()
	add(t, s, "T1")
	add(t, s, "T2")
	off := snapshot(s)
	if lack := contract.Unplanned(s, []string{"T1"}); len(lack) != 1 || snapshot(s) != off {
		t.Fatalf("nothing is planned yet: %v", lack)
	}
	hostile := "Plan for \"a%s\x1b[2Jb\" · 2 tasks\n  T1 $(touch x)"
	kept := r.Planned(s, []string{"T1", "T2"}, hostile, nil, t0)
	q := itemOf(s, contract.CausePlan)
	if code(kept) != contract.RefusedPlanFirst || exit(kept) != contract.ExitRefused || q == nil || q.ID != s.Run.Plan.Item {
		t.Fatalf("a start with no approval: %v, %+v", kept, q)
	}
	if q.Text != hostile || q.Form != contract.FormSignoff || q.From != contract.FromTool || q.For != contract.ForHuman || q.Holds || q.Urgent || !slices.Equal(q.Tasks, []string{"T1", "T2"}) {
		t.Errorf("the plan's item: %+v", q)
	}
	// The row "the approval": the lead agent wants to start.
	noAgent(t, s, q.ID, contract.AnswerApprove)
	if r.Planned(s, []string{"T1", "T2"}, hostile, &orch, t0) == nil || r.Planned(s, []string{"T1", "T2"}, hostile, &relayed, t0) == nil {
		t.Error("an agent's origin approved a plan")
	}
	// Asked again with nothing new: the same item, nothing nudged again.
	before := snapshot(s)
	if r.Planned(s, []string{"T1", "T2"}, hostile, nil, after(time.Minute)); snapshot(s) != before || len(s.Questions) != 1 {
		t.Error("the same plan asked twice changed the record")
	}
	// The person approves while the agent adds a task: the yes is taken
	// back, for it was given to another plan.
	must(t, r.Answer(s, q.ID, contract.AnswerApprove, human, 4*time.Second, t0))
	add(t, s, "T3")
	r.Planned(s, []string{"T1", "T2", "T3"}, "three", nil, after(time.Second))
	if _, ok, _ := r.Use(s, q.ID, after(time.Minute)); ok || q.State != contract.QuestionOpen || len(q.Earlier) != 1 || len(s.Run.Plan.Approved) != 0 {
		t.Fatalf("a yes to two tasks covered three: %+v", q)
	}
	if got := settle(t, s, q.ID, contract.AnswerApprove, after(time.Minute)); got != contract.AnswerApprove || !slices.Equal(s.Run.Plan.Approved, []string{"T1", "T2", "T3"}) || s.Run.Plan.By.Where != contract.WherePane {
		t.Fatalf("approved: %+v", s.Run.Plan)
	}
	if !strings.HasSuffix(told(s), "answered") || r.Planned(s, []string{"T1", "T3"}, "", nil, after(time.Hour)) != nil {
		t.Errorf("an approved plan does not start, or the lead agent was not told: %s", told(s))
	}
	// A task added later is shown alone, in a new item; sent back writes nothing.
	add(t, s, "T4")
	if r.Planned(s, []string{"T2", "T4"}, "one more", nil, after(time.Hour)) == nil {
		t.Fatal("a task added after the approval started")
	}
	more := s.Questions[s.Run.Plan.Item]
	if more == q || !slices.Equal(more.Tasks, []string{"T4"}) {
		t.Fatalf("the item of what is new: %+v", more)
	}
	settle(t, s, more.ID, contract.AnswerSendBack+" not that one", after(time.Hour))
	if slices.Contains(s.Run.Plan.Approved, "T4") || more.State != contract.QuestionUsed {
		t.Errorf("sent back, and yet: %+v", s.Run.Plan)
	}
	// The person's own start is their yes; a fleet's run is never asked.
	if r.Planned(s, []string{"T4"}, "", &human, after(2*time.Hour)) != nil || !slices.Contains(s.Run.Plan.Approved, "T4") {
		t.Error("the person's own start was held")
	}
	low := fleetRun("r2")
	low.Run.Parent = &contract.Twin{Run: "r1", Fleet: "billing"}
	add(t, low, "T1")
	if r.Planned(low, []string{"T1"}, "", nil, t0) != nil || low.Run.Plan != nil {
		t.Error("a leader's start in its fleet was held")
	}
}

// 20.2, its security table: the sign-off is the person's alone and is for
// exact contents; names from a repository are data.
func TestProtectedFiles(t *testing.T) {
	s := in(t, contract.AttemptAccepted)
	off := snapshot(s)
	if r.Protect(s, "T1", "", nil, t0) || r.Landable(s, "", nil) != nil || snapshot(s) != off {
		t.Fatal("no protected file changed, and something was written")
	}
	files := []string{"db/migrations/0042 %d\x1b[31m.sql", ".env", "deploy/a", "deploy/b", "deploy/c"}
	if !r.Protect(s, "T1", "f1", files, t0) {
		t.Fatal("a protected file changed and nobody was asked")
	}
	p := s.Run.Protect
	q := s.Questions[p.Item]
	if q == nil || q.Cause != contract.CauseProtect || q.Form != contract.FormSignoff || q.Holds || q.Task != "T1" || q.Text != "Protected files changed in this job" || len(p.Files) != len(files) {
		t.Fatalf("the item: %+v", q)
	}
	if e := s.Inbox.Events[len(s.Inbox.Events)-1]; e.Kind != "protected" || e.Task != "T1" || !strings.Contains(e.Text, "Carry on") {
		t.Errorf("the event: %+v", e)
	}
	if got := r.Landable(s, "f1", files); code(got) != contract.RefusedProtected || exit(got) != contract.ExitRefused {
		t.Fatalf("landed with no approval: %v", got)
	}
	// The row "the sign-off".
	noAgent(t, s, q.ID, contract.AnswerApprove)
	// The same fingerprint collected again asks nothing new.
	before := snapshot(s)
	if r.Protect(s, "T1", "f1", files, after(time.Minute)) || snapshot(s) != before {
		t.Error("the same fingerprint was asked twice")
	}
	// The row "a change slipped in after the yes", while the yes settles...
	must(t, r.Answer(s, q.ID, contract.AnswerApprove, human, 4*time.Second, t0))
	if !r.Protect(s, "T1", "f2", files, after(time.Second)) {
		t.Fatal("a new fingerprint was not asked")
	}
	if _, ok, _ := r.Use(s, q.ID, after(time.Minute)); ok || p.Print != "" || q.State != contract.QuestionOpen || len(q.Earlier) != 1 {
		t.Fatalf("a yes to f1 approved f2: %+v", p)
	}
	settle(t, s, q.ID, contract.AnswerApprove, after(time.Minute))
	if p.Print != "f2" || r.Landable(s, "f2", files) != nil {
		t.Fatalf("approved, and not landable: %+v", p)
	}
	// ... and after it: the tip is worked out again and is another.
	if code(r.Landable(s, "f3", files)) != contract.RefusedProtected {
		t.Error("another fingerprint landed on the yes to f2")
	}
	if !r.Protect(s, "T1", "f3", files[:1], after(time.Hour)) || s.Questions[p.Item] == q || p.Print != "f2" {
		t.Errorf("a later change did not ask again: %+v", p)
	}
	// Back at what was approved, or at the base: the item closes by itself.
	again := s.Questions[p.Item]
	if r.Protect(s, "T1", "f2", files, after(2*time.Hour)) || again.State != contract.QuestionClosed || r.Landable(s, "f2", files) != nil {
		t.Errorf("the approved fingerprint left its item open: %+v", again)
	}
	r.Protect(s, "T1", "f4", files, after(3*time.Hour))
	if last := s.Questions[p.Item]; r.Protect(s, "T1", "", nil, after(4*time.Hour)) || last.State != contract.QuestionClosed || r.Landable(s, "", nil) != nil {
		t.Errorf("files as at the base left the item open: %+v", last)
	}
	// An item taken out of view is put back by the next collect; a fleet's run asks nothing.
	r.Protect(s, "T1", "f5", files, after(5*time.Hour))
	s.Questions[p.Item].State = contract.QuestionClosed
	if !r.Protect(s, "T1", "f5", files, after(6*time.Hour)) || itemOf(s, contract.CauseProtect) == nil {
		t.Error("the sign-off was lost with its item")
	}
	s.Run.Parent, s.Run.Protect = &contract.Twin{Run: "r0", Fleet: "billing"}, nil
	if r.Protect(s, "T1", "f9", files, t0) || s.Run.Protect != nil {
		t.Error("a leader's accept raised a sign-off")
	}
}

func spendRun(t *testing.T) *contract.State {
	s := in(t, contract.AttemptWorking)
	s.Attempts["T1.1"].Seen.At = t0 // so that the snapshot compares
	return s
}

// 20.3, its security table: more hours are the person's alone, a hold that
// is taken away comes back, and the count is the record's own times.
func TestSpendingGuard(t *testing.T) {
	s, hours := spendRun(t), func(h float64) time.Duration { return time.Duration(h * float64(time.Hour)) }
	off := snapshot(s)
	if got := r.Spend(s, "", hours(500), contract.Unlimited, t0); got != "" || snapshot(s) != off {
		t.Fatal("the guard is off, and something was written")
	}
	if got := r.Spend(s, "", hours(31.9), 40, t0); got != "" || snapshot(s) != off {
		t.Fatal("under four fifths, and something was written")
	}
	if got := r.Spend(s, "", hours(32), 40, t0); got != contract.SpendWarned || !s.Run.Spend.Warned || s.Inbox.Events[0].Text != "32 of 40 agent-hours used" || s.Run.Paused != nil {
		t.Fatalf("four fifths: %q %+v", got, s.Inbox.Events)
	}
	if got := r.Spend(s, "", hours(39.96), 40, t0); got != "" || told(s) != "budget" {
		t.Fatalf("told twice: %q %s", got, told(s))
	}
	held := after(2 * time.Hour)
	if got := r.Spend(s, "", hours(40.26), 40, held); got != contract.SpendHeld || !s.Run.Spend.Held || s.Run.Paused == nil || s.Run.Paused.By != contract.HeldBy || told(s) != "budget budget paused" {
		t.Fatalf("the limit: %q %+v %s", got, s.Run, told(s))
	}
	q := itemOf(s, contract.CauseBudget)
	if q == nil || !q.Urgent || q.Form != contract.FormChoice || q.Set == nil || *q.Set != (contract.SetAsk{Key: "spend.job", From: "40", To: "48"}) || s.Inbox.Events[1].Text != "40.3 of 40 agent-hours used: held" {
		t.Fatalf("the choice: %+v", q)
	}
	before := snapshot(s)
	if got := r.Spend(s, "", hours(40.26), 40, held.Add(time.Minute)); got != "" || snapshot(s) != before {
		t.Fatal("held twice")
	}
	// The row "more hours for a job": the lead agent wants to go on.
	noAgent(t, s, q.ID, contract.AnswerMore)
	noAgent(t, s, q.ID, contract.AnswerNoLimit)
	if _, err := r.Start(s, "T1", true, false, token, agent, true, held); code(err) == "" {
		t.Error("a start in a held job")
	}
	// The row "the hold": resumed past the guard, it is held again at the next look.
	must(t, r.Resume(s, held.Add(10*time.Minute)))
	if got := r.Spend(s, "", hours(40.3), 40, held.Add(11*time.Minute)); got != "" || s.Run.Paused == nil || s.Run.Paused.By != contract.HeldBy || itemOf(s, contract.CauseBudget) != q {
		t.Fatalf("a hold that was taken away stayed away: %+v", s.Run.Paused)
	}
	q.State = contract.QuestionClosed
	if r.Spend(s, "", hours(40.3), 40, held.Add(12*time.Minute)); itemOf(s, contract.CauseBudget) == nil {
		t.Fatal("the choice was lost with its item")
	}
	q = itemOf(s, contract.CauseBudget)
	// Leave it held: nothing is written, and the answer can be taken back.
	if got := settle(t, s, q.ID, contract.AnswerLeave, held.Add(time.Hour)); got != contract.AnswerLeave || !s.Run.Spend.Held || s.Run.Paused == nil || q.State != contract.QuestionAnswered {
		t.Fatalf("left held: %+v", q)
	}
	before = snapshot(s)
	if got := r.Spend(s, "", hours(40.3), 40, held.Add(time.Hour)); got != "" || snapshot(s) != before {
		t.Fatal("a job left held was asked again")
	}
	must(t, r.Undo(s, q.ID, human, held.Add(2*time.Hour)))
	// More: a fifth, the hold ends as a pause does, and the rest is not counted.
	a := s.Attempts["T1.1"]
	rested, end := a.Rested, held.Add(3*time.Hour)
	if got := settle(t, s, q.ID, contract.AnswerMore, end.Add(-4*time.Second)); got != contract.AnswerMore || s.Run.Spend != (contract.Spend{More: 8, Warned: true}) || s.Run.Paused != nil || q.State != contract.QuestionUsed {
		t.Fatalf("more: %+v", s.Run.Spend)
	}
	if a.Rested-rested != 3*time.Hour-11*time.Minute || !strings.HasSuffix(told(s), "resumed answered") {
		t.Errorf("rested %v, told %s", a.Rested-rested, told(s))
	}
	if got := r.Spend(s, "", hours(40.3), 48, end); got != "" || s.Run.Paused != nil {
		t.Fatal("held again under the new limit")
	}
	// At the new limit a new choice; no limit lifts it for this job.
	if got := r.Spend(s, "", hours(48), 48, end.Add(8*time.Hour)); got != contract.SpendHeld {
		t.Fatalf("the second limit: %q", got)
	}
	q = itemOf(s, contract.CauseBudget)
	if q.Set.To != "58" || settle(t, s, q.ID, contract.AnswerNoLimit, end.Add(9*time.Hour)) != contract.AnswerNoLimit || !s.Run.Spend.Off || s.Run.Spend.Held || s.Run.Paused != nil {
		t.Fatalf("no limit: %+v %+v", q.Set, s.Run.Spend)
	}
	// The person's settings no longer ask for a hold: it goes, with its item.
	s = spendRun(t)
	r.Spend(s, "", hours(1), 1, t0)
	if q = itemOf(s, contract.CauseBudget); q.Set.To != "2" || s.Run.Paused == nil {
		t.Fatalf("a limit of one: %+v", q.Set)
	}
	if r.Spend(s, "", hours(1), contract.Unlimited, after(time.Minute)); s.Run.Spend.Held || s.Run.Paused != nil || q.State != contract.QuestionClosed {
		t.Fatalf("the guard switched off left the job held: %+v", s.Run)
	}
	// The person's own pause is not the guard's to end, and the choice settles under it.
	must(t, r.Pause(s, "the person", after(time.Hour)))
	r.Spend(s, "", hours(2), 1, after(time.Hour))
	q = itemOf(s, contract.CauseBudget)
	if settle(t, s, q.ID, contract.AnswerNoLimit, after(2*time.Hour)); s.Run.Spend.Held || s.Run.Paused == nil || s.Run.Paused.By != "the person" {
		t.Errorf("the guard ended the person's pause: %+v", s.Run.Paused)
	}
}

// The row "the count": what an attempt rested is what Spent leaves out.
func TestRestedIsNotSpent(t *testing.T) {
	s := in(t, contract.AttemptWorking)
	add(t, s, "T2")
	must(t, r.Pause(s, "the person", after(2*time.Hour)))
	if got := contract.Spent(after(3*time.Hour), s); got != 2*time.Hour {
		t.Fatalf("spent under a pause: %v", got)
	}
	must(t, r.Stop(s, "T1", false, after(150*time.Minute)))
	must(t, r.Resume(s, after(3*time.Hour)))
	_, err := r.Start(s, "T2", false, false, token, agent, true, after(3*time.Hour))
	must(t, err)
	must(t, r.Pause(s, contract.HeldBy, after(4*time.Hour)))
	must(t, r.Resume(s, after(6*time.Hour)))
	if got := contract.Spent(after(7*time.Hour), s); got != 4*time.Hour || s.Attempts["T1.1"].Rested != 30*time.Minute || s.Attempts["T2.1"].Rested != 2*time.Hour {
		t.Fatalf("spent %v: two hours of T1, and of T2 one before its hold and one after", got)
	}
}

// 20.3 point 7: one fleet at its own limit is held alone.
func TestAFleetIsHeld(t *testing.T) {
	up, low, other := job(t), fleetRun("r2"), fleetRun("r3")
	led(t, up, low, "G1", "w1:p5")
	led(t, up, other, "G3", "w1:p6")
	worker(t, low, "T1")
	if r.Hold(up, low, t0) || r.Hold(up, fleetRun("r9"), t0) || r.Spend(up, "nobody", 99*time.Hour, 1, t0) != "" {
		t.Fatal("nothing is held yet")
	}
	if got := r.Spend(up, "billing", 16*time.Hour, 16, after(time.Hour)); got != contract.SpendHeld || up.Run.Paused != nil || !up.Run.Fleets["billing"].Spend.Held || up.Run.Spend.Held {
		t.Fatalf("a fleet at its limit: %q %+v", got, up.Run)
	}
	q := itemOf(up, contract.CauseBudget)
	if q.Task != "G1" || *q.Set != (contract.SetAsk{Key: "spend.fleet", From: "16", To: "19"}) || !strings.HasPrefix(q.Text, "billing has used") {
		t.Fatalf("the fleet's choice: %+v", q)
	}
	if e := up.Inbox.Events[len(up.Inbox.Events)-1]; e.Kind != "budget" || e.Data["fleet"] != "billing" || e.Data["held"] != true {
		t.Errorf("the orchestrator was told: %+v", e)
	}
	if !r.Hold(up, low, after(time.Hour)) || low.Run.Paused == nil || low.Run.Paused.By != contract.HeldBy || r.Hold(up, low, after(time.Hour)) || r.Hold(up, other, after(time.Hour)) {
		t.Fatalf("the fleet's run: %+v, the other's: %+v", low.Run.Paused, other.Run.Paused)
	}
	// Resumed past the guard, a fleet's run is held again.
	must(t, r.Resume(low, after(61*time.Minute)))
	if !r.Hold(up, low, after(62*time.Minute)) || low.Run.Paused == nil {
		t.Fatal("a fleet's hold that was taken away stayed away")
	}
	noAgent(t, up, q.ID, contract.AnswerMore)
	settle(t, up, q.ID, contract.AnswerMore, after(2*time.Hour))
	if sp := up.Run.Fleets["billing"].Spend; sp != (contract.Spend{More: 3, Warned: true}) || up.Run.Spend != (contract.Spend{}) {
		t.Fatalf("more for one fleet: %+v, the job: %+v", sp, up.Run.Spend)
	}
	if !r.Hold(up, low, after(3*time.Hour)) || low.Run.Paused != nil || low.Attempts["T1.1"].Rested != 2*time.Hour-time.Minute {
		t.Fatalf("the hold ended: %+v, rested %v", low.Run.Paused, low.Attempts["T1.1"].Rested)
	}
	// The job held holds every fleet; the person's own pause of a fleet is not the guard's.
	r.Spend(up, "", 40*time.Hour, 40, after(4*time.Hour))
	must(t, r.Pause(other, "the person", after(4*time.Hour)))
	if !r.Hold(up, low, after(4*time.Hour)) || r.Hold(up, other, after(4*time.Hour)) {
		t.Fatal("the job is held, and its fleets are not brought in line")
	}
	settle(t, up, itemOf(up, contract.CauseBudget).ID, contract.AnswerNoLimit, after(5*time.Hour))
	if !r.Hold(up, low, after(6*time.Hour)) || r.Hold(up, other, after(6*time.Hour)) || other.Run.Paused == nil {
		t.Fatal("the hold ended, and the fleets are not as they should be")
	}
}

// 20.18, its security table: an agent's wish for a setting that holds it.
func TestAnAgentAsksForASetting(t *testing.T) {
	s, help := newRun(), "Spending guard: Off: a job runs until it is done."
	id, err := r.SetAsked(s, contract.SetAsk{Key: "spend.guard", From: "on", To: "off"}, help, t0)
	must(t, err)
	q := s.Questions[id]
	if q.Cause != contract.CauseSetting || q.From != contract.FromTool || q.Urgent || q.Text != help || q.Set.To != "off" {
		t.Fatalf("the item: %+v", q)
	}
	// The rows "an agent switching off what holds it" and "many items".
	noAgent(t, s, id, contract.AnswerChange)
	if again, _ := r.SetAsked(s, contract.SetAsk{Key: "spend.guard", From: "on", To: "off"}, help, t0); again != id || len(s.Questions) != 1 {
		t.Fatal("one key, two items")
	}
	if other, _ := r.SetAsked(s, contract.SetAsk{Key: "plan.first", From: "on", To: "off"}, "Plan first", t0); other == id {
		t.Fatal("two keys, one item")
	}
	// A yes to one value is no yes to another.
	must(t, r.Answer(s, id, "Change", human, 4*time.Second, t0))
	if q.Answer != contract.AnswerChange {
		t.Fatalf("stored as %q", q.Answer)
	}
	r.SetAsked(s, contract.SetAsk{Key: "spend.guard", From: "on", To: "unlimited"}, help, after(time.Second))
	if _, ok, _ := r.Use(s, id, after(time.Minute)); ok || q.State != contract.QuestionOpen || q.Set.To != "unlimited" {
		t.Fatalf("a yes to off changed it to %s", q.Set.To)
	}
	// Change is handed back for the caller to make; nothing of the record changes but the item.
	if got := settle(t, s, id, contract.AnswerChange, after(time.Minute)); got != contract.AnswerChange || q.State != contract.QuestionUsed || !strings.HasSuffix(told(s), "answered") {
		t.Fatalf("change: %q %+v", got, q)
	}
	// Left as it was: not asked again in this job.
	id, err = r.SetAsked(s, contract.SetAsk{Key: "spend.guard", From: "unlimited", To: "off"}, help, after(time.Hour))
	must(t, err)
	settle(t, s, id, contract.AnswerLeave, after(time.Hour))
	before := snapshot(s)
	if _, err := r.SetAsked(s, contract.SetAsk{Key: "spend.guard", From: "unlimited", To: "off"}, help, after(2*time.Hour)); code(err) != contract.RefusedPersonOnly || snapshot(s) != before {
		t.Fatalf("asked again after leave: %v", err)
	}
	must(t, r.CloseRun(s, false, after(3*time.Hour)))
	if _, err := r.SetAsked(s, contract.SetAsk{Key: "mute", To: "on"}, "Mute", after(4*time.Hour)); code(err) != "closed" {
		t.Errorf("asked in a closed run: %v", err)
	}
}

// What the counters count, where the fact happens.
func TestCounters(t *testing.T) {
	s := in(t, contract.AttemptWorking)
	a, task := s.Attempts["T1.1"], s.Tasks["T1"]
	a.Seen.Emptied = 2
	for i, want := range []int{0, 1, 2, 0, 0, 1} {
		note := []string{"a", "a", "a", "b", "b", "b"}[i]
		must(t, r.Progress(s, "T1.1", 50+i/4*10, note, after(time.Duration(i)*time.Minute)))
		if a.Progress.Same != want || i == 0 && a.Seen.Emptied != 0 {
			t.Fatalf("note %d: same %d, emptied %d", i, a.Progress.Same, a.Seen.Emptied)
		}
	}
	for n := 1; n <= 2; n++ {
		report(r.Report(s, "T1.1", token, outDone, "done", nil, t0))
		must(t, r.Checking(s, "T1", contract.Checked{}, t0))
		if _, err := r.Checked(s, "T1", contract.CheckResult{}, contract.Accepted{How: howCheck}, t0); err != nil || task.CheckFails != n {
			t.Fatalf("failed checks: %d, %v", task.CheckFails, err)
		}
		must(t, r.Reject(s, "T1", "again", contract.Live, t0))
	}
	report(r.Report(s, "T1.1", token, outDone, "done", nil, t0))
	must(t, r.Checking(s, "T1", contract.Checked{}, t0))
	if _, err := r.Checked(s, "T1", contract.CheckResult{OK: true}, contract.Accepted{How: howCheck}, t0); err != nil || task.CheckFails != 0 {
		t.Fatalf("a pass left %d failed checks", task.CheckFails)
	}
	// A group's syncs are counted, with no brief; a task with no copy is still refused.
	up := job(t)
	if n, err := r.Synced(up, "G1", "msg/brief.md", t0); err != nil || n != 1 || up.Tasks["G1"].SyncBrief != "" {
		t.Errorf("a group synced: %d %v", n, err)
	}
	if _, err := r.Synced(newRunWith(t, "T9"), "T9", "", t0); code(err) != "no_worktree" {
		t.Errorf("a task with no copy: %v", err)
	}
}

func newRunWith(t *testing.T, id string) *contract.State {
	s := newRun()
	add(t, s, id)
	return s
}

// The afternoon of the contract's fixture, answered by the person.
func TestTheGuardedAfternoon(t *testing.T) {
	g := testkit.Guarded()
	up, now := g.Up, g.Now
	for _, id := range []string{"n1", "n2", "n3"} {
		noAgent(t, up, id, contract.AnswerApprove)
	}
	if r.Planned(up, []string{"G1", "G2"}, "", nil, now) != nil || code(r.Planned(up, []string{"G3"}, up.Questions["n1"].Text, nil, now)) != contract.RefusedPlanFirst || len(up.Questions) != 3 {
		t.Fatal("the plan as the fixture has it")
	}
	if code(r.Landable(up, "f2", up.Run.Protect.Files)) != contract.RefusedProtected || r.Landable(up, "f1", nil) != nil {
		t.Fatal("f1 is approved and f2 is not")
	}
	settle(t, up, "n1", contract.AnswerApprove, now)
	settle(t, up, "n2", contract.AnswerApprove, now)
	if r.Planned(up, []string{"G3"}, "", nil, now.Add(time.Minute)) != nil || r.Landable(up, "f2", nil) != nil || up.Run.Protect.Print != "f2" {
		t.Fatalf("approved: %+v %+v", up.Run.Plan, up.Run.Protect)
	}
	if got := settle(t, up, "n3", contract.AnswerChange, now); got != contract.AnswerChange || up.Questions["n3"].Set.To != contract.Off {
		t.Fatalf("the setting's change handed back: %q", got)
	}
	spent := contract.Spent(now, up, g.Low["billing"], g.Low["search"])
	if got := r.Spend(up, "", spent, g.Person.SpendLimit(up.Run.Spend, false), now); got != "" || spent != 33*time.Hour {
		t.Fatalf("33 of 40, told already: %q %v", got, spent)
	}
	if got := r.Spend(up, "", spent+7*time.Hour, 40, now); got != contract.SpendHeld || !r.Hold(up, g.Low["billing"], now) || !r.Hold(up, g.Low["search"], now) {
		t.Fatalf("at 40 the job and both fleets are held: %q", got)
	}
}

// The rows "the setting itself", "the limits" and "the list" of the three
// tables: every guard's key lies in the person's own file, waits for their
// click when an agent sets it, and a project's file only adds to the list,
// through a matcher that takes a pattern as nothing but a pattern.
func TestTheGuardsAreThePersons(t *testing.T) {
	n := 0
	for _, key := range contract.Keys {
		name, _, _ := strings.Cut(key.Name, ".")
		if !slices.Contains([]string{"plan", "protect", "spend", "stuck"}, name) {
			continue
		}
		// The project's additions are its file's, and no agent's to set at all.
		want := [2]string{contract.SetAsks, contract.StoreConfig}
		if key.Name == "protect.also" {
			want = [2]string{contract.SetPerson, contract.StoreProject}
		}
		if n++; [2]string{key.Agent(), key.Store} != want || key.Help == "" {
			t.Errorf("%s: an agent %s it, it lies in %s", key.Name, key.Agent(), key.Store)
		}
	}
	if n != 9 {
		t.Errorf("%d keys guard, the design has 9", n)
	}
	person, project := contract.PersonDefaults(), contract.ProjectDefaults()
	project.Protect.Also = []string{"$(touch owned)", "-rf", "", "docs/**"}
	if contract.Protected(person, project) != nil {
		t.Fatal("protected files are off, and a project switched them on")
	}
	person.Protect.Files = true
	list := contract.Protected(person, project)
	for file, want := range map[string]bool{".env.local": true, "db/migrations/1.sql": true, "docs/a/b.md": true, "main.go": false, "touch": false, "owned": false} {
		if contract.Owned(list, file) != want {
			t.Errorf("%s protected: %v", file, !want)
		}
	}
	person.Protect.Paths = ""
	if got := contract.Protected(person, project); len(got) != 3 || contract.Owned(got, ".env") {
		t.Errorf("the project's additions alone: %q", got)
	}
}
