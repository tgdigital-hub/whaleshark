package rules

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
)

// invariants returns what is wrong with a record, or "" when nothing is.
func invariants(s *contract.State) string {
	var bad []string
	fail := func(format string, a ...any) { bad = append(bad, fmt.Sprintf(format, a...)) }
	lives, names := 0, map[string]string{}
	for id, t := range s.Tasks {
		for _, n := range []string{strings.ToLower(id), strings.ToLower(t.Name)} {
			if other, taken := names[n]; taken && other != id {
				fail("%s and %s share the name or id %q", id, other, n)
			}
			names[n] = id
		}
		if words := strings.Fields(t.Name); len(words) == 0 || len(words) > 3 || len([]rune(t.Name)) > 24 {
			fail("%s is named %q", id, t.Name)
		}
		if id != t.ID || t.Check == "" {
			fail("%s has the id %q and the check %q", id, t.ID, t.Check)
		}
		waiting := false
		for _, dep := range t.After {
			if s.Tasks[dep] == nil {
				fail("%s waits for %s, which does not exist", id, dep)
				return strings.Join(bad, "; ")
			}
			waiting = waiting || s.Tasks[dep].Status != contract.TaskDone
		}
		if loops(s, id, t.After) {
			fail("%s waits for itself", id)
		}
		if (t.Status == contract.TaskPending && !waiting) || (t.Status == contract.TaskReady && waiting) {
			fail("%s is %s with waiting %v", id, t.Status, waiting)
		}
		for i, aid := range t.Attempts {
			a := s.Attempts[aid]
			if a == nil || a.Task != id || a.N != i+1 || aid != fmt.Sprintf("%s.%d", id, i+1) {
				fail("%s lists the attempt %s wrongly", id, aid)
			} else if a.State.Live() && i != len(t.Attempts)-1 {
				fail("%s is live and is not the last attempt", aid)
			}
		}
		state := contract.AttemptState("")
		if a := current(s, t); a != nil {
			state = a.State
		}
		var ok bool
		switch t.Status {
		case contract.TaskRunning:
			ok = state == contract.AttemptStarting || state == contract.AttemptWorking || state == contract.AttemptAsked
		case contract.TaskReview:
			ok = state == contract.AttemptReported || state == contract.AttemptChecking
		case contract.TaskDone:
			ok = state == contract.AttemptAccepted && t.Accepted != nil
		case contract.TaskFailed:
			ok = !state.Live() && t.Failures >= contract.MaxFailures
		default:
			ok = !state.Live() && state != contract.AttemptAccepted
		}
		if !ok || (t.Status != contract.TaskFailed && t.Status != contract.TaskCancelled && t.Failures >= contract.MaxFailures) {
			fail("%s is %s with its attempt %q and %d failures", id, t.Status, state, t.Failures)
		}
	}
	asks := map[string]int{}
	for id, q := range s.Questions {
		answered := q.Answer != "" && q.AnsweredBy != nil && !q.AnsweredAt.IsZero()
		switch q.State {
		case contract.QuestionOpen:
			ok := q.Answer == "" && q.AnsweredBy == nil && q.UsedAt.IsZero()
			if !ok {
				fail("%s is open and carries an answer", id)
			}
		case contract.QuestionAnswered:
			if !answered || !q.UsedAt.IsZero() {
				fail("%s is answered: %+v", id, q)
			}
		case contract.QuestionUsed:
			if !answered || q.UsedAt.IsZero() {
				fail("%s is used: %+v", id, q)
			}
		case contract.QuestionClosed:
		default:
			fail("%s is %q", id, q.State)
		}
		if id != q.ID || (q.Kind == contract.KindAsk) != (id[0] == 'q') || (q.Kind == contract.KindNeed) != (id[0] == 'n') {
			fail("%s is a %s with the id %s", id, q.Kind, q.ID)
		}
		if q.Kind == contract.KindNeed && q.For != contract.ForHuman || !slices.Contains(forms, q.Form) {
			fail("%s is for %s, as a %s", id, q.For, q.Form)
		}
		if (q.Task != "" && s.Tasks[q.Task] == nil) || (q.Holds && q.Task == "") || (q.Kind == contract.KindAsk && s.Attempts[q.Attempt] == nil) {
			fail("%s is about %q and %q", id, q.Task, q.Attempt)
		}
		if q.Kind == contract.KindAsk && (q.State == contract.QuestionOpen || q.State == contract.QuestionAnswered) {
			asks[q.Attempt]++
		}
	}
	for id, a := range s.Attempts {
		if a.State.Live() {
			lives++
		} else if a.EndedAt.IsZero() || a.Exit == nil {
			fail("%s is %s with no end", id, a.State)
		}
		if a.State.Live() && (!a.EndedAt.IsZero() || a.Exit != nil) {
			fail("%s is %s and has ended", id, a.State)
		}
		if (a.State == contract.AttemptAsked) != (asks[id] == 1) || asks[id] > 1 {
			fail("%s is %s with %d open questions", id, a.State, asks[id])
		}
		if (a.State == contract.AttemptChecking) != (a.Accept != nil) {
			fail("%s is %s with accept %v", id, a.State, a.Accept)
		}
		reported := a.Report != nil && a.Report.Outcome == outDone
		if (a.State == contract.AttemptReported || a.State == contract.AttemptChecking) && !reported {
			fail("%s is %s with no report", id, a.State)
		}
		if a.State == contract.AttemptFailed && (a.Report == nil || a.Report.Outcome != outFailed) {
			fail("%s failed with no report", id)
		}
		if t := s.Tasks[a.Task]; t == nil || !slices.Contains(t.Attempts, id) {
			fail("%s is not an attempt of its task", id)
		}
		if a.State.Live() && a.Seen.Liveness == "" {
			fail("%s has no liveness", id)
		}
	}
	if lives > s.Run.Limit || (!s.Run.ClosedAt.IsZero() && lives > 0) {
		fail("%d live attempts, limit %d, closed %v", lives, s.Run.Limit, !s.Run.ClosedAt.IsZero())
	}
	in, last := s.Inbox, s.Inbox.Acked
	for _, e := range in.Events {
		if e.Seq <= last || e.Seq > s.Counters.Seq || !slices.Contains(contract.EventKinds, e.Kind) || e.At.IsZero() {
			fail("event %d (%s) is out of order or unknown", e.Seq, e.Kind)
		}
		last = e.Seq
	}
	if b := in.Outstanding; b != nil {
		n := len(delivered(&in, b))
		if n == 0 || n > contract.MaxBatch || in.Events[0].Seq != b.From || in.Events[n-1].Seq != b.To || b.ID != fmt.Sprintf("d%d", s.Counters.Delivery) {
			fail("the batch %+v does not match the inbox", b)
		}
	}
	return strings.Join(bad, "; ")
}

// world drives one run with random commands and remembers what must never
// change afterwards.
type world struct {
	*rand.Rand
	s      *contract.State
	now    time.Time
	seqs   map[int]bool // every event and message ever seen, by seq
	acked  int
	done   map[string]contract.Accepted
	used   map[string]string
	closed map[string]bool
}

var (
	taskIDs  = []string{"T1", "T2", "T3", "T4", "T5", "t1", "T9"}
	titles   = []string{"login page", "sign-up page", "Login Page", "search box", "a title of more than three words", "help pages", "site map", "lead", "price list", "admin page", "order emails"}
	statuses = []string{contract.StatusWorking, contract.StatusWorking, contract.StatusIdle, contract.StatusDone, contract.StatusBlocked, contract.StatusUnknown, ""}
	answers  = []string{"yes", "no", "approve", "done", "send back: no", "cannot: no", "other: later", "main", ""}
	origins  = []contract.Origin{human, human, orch, {Caller: contract.Orchestrator, Where: contract.WhereRelayed}, {Caller: contract.Worker}}
)

func pick[T any](w *world, of []T) T { return of[w.IntN(len(of))] }

// task picks a task, most often one that is in one of the given states, so
// that the sequences get past the first steps of a task's life.
func (w *world) task(in ...contract.TaskStatus) string {
	var ids []string
	for id, t := range w.s.Tasks {
		if len(in) == 0 || slices.Contains(in, t.Status) {
			ids = append(ids, id)
		}
	}
	if w.IntN(6) == 0 || len(ids) == 0 {
		return pick(w, taskIDs)
	}
	slices.Sort(ids)
	return pick(w, ids)
}

func (w *world) attempt(in ...contract.AttemptState) string {
	var ids []string
	for id, a := range w.s.Attempts {
		if len(in) == 0 || slices.Contains(in, a.State) {
			ids = append(ids, id)
		}
	}
	if w.IntN(8) == 0 || len(ids) == 0 {
		return "T9.1"
	}
	slices.Sort(ids)
	return pick(w, ids)
}

func (w *world) question() string {
	if w.IntN(20) == 0 || len(w.s.Questions) == 0 {
		return "q99"
	}
	ids := make([]string, 0, len(w.s.Questions))
	for id := range w.s.Questions {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return pick(w, ids)
}

func (w *world) pane(a *contract.Attempt) *contract.Pane {
	if w.IntN(8) == 0 {
		return nil
	}
	p := pane(pick(w, statuses))
	if a != nil && w.IntN(25) != 0 {
		p.Terminal = a.Place.Terminal
	}
	return p
}

// step runs one random command. It reports the error and whether a refusal
// is allowed to have changed the record, which only a report is.
func (w *world) step() (name string, err error, mayChange bool) {
	s, now := w.s, w.now
	some := func(n int) bool { return w.IntN(n) == 0 }
	switch w.IntN(34) {
	case 0, 1:
		t := contract.Task{ID: w.task(), Title: pick(w, titles), Check: "true"}
		if some(3) {
			t.Name = pick(w, titles)
		}
		t.ID = pick(w, taskIDs)
		for range w.IntN(3) {
			t.After = append(t.After, w.task())
		}
		return "task add", r.AddTask(s, t, []string{"site map"}, now), false
	case 2:
		after := []string{w.task()}
		_, err = r.EditTask(s, w.task(contract.TaskPending, contract.TaskReady, contract.TaskFailed), contract.TaskEdit{After: &after}, now)
		return "task edit", err, false
	case 3:
		if some(3) {
			return "task cancel", r.CancelTask(s, w.task(), now), false
		}
		return "task reset", r.ResetTask(s, w.task(contract.TaskFailed), now), false
	case 4, 5, 6:
		_, err = r.Start(s, w.task(contract.TaskReady), !some(4), some(10), token, agent, some(2), now)
		return "start", err, false
	case 7:
		return "placed", r.Placed(s, w.attempt(contract.AttemptStarting), contract.Place{Tab: "t", Pane: "w1:p2", Terminal: "term1"}, now), false
	case 8, 9, 10:
		return "working", r.Working(s, w.attempt(contract.AttemptStarting), now), false
	case 11:
		if some(4) {
			return "start failed", r.StartFailed(s, w.attempt(contract.AttemptStarting), "no tab", now), false
		}
		return "progress", r.Progress(s, w.attempt(), w.IntN(120)-10, "note", now), false
	case 12, 13, 14:
		outcome, tok := outDone, token
		id := w.attempt(contract.AttemptWorking, contract.AttemptAsked, contract.AttemptReported)
		// A task that failed once tends to fail again, so that some reach the third failure.
		if a := s.Attempts[id]; some(3) || a != nil && s.Tasks[a.Task].Failures > 0 && !some(3) {
			outcome = outFailed
		}
		if some(15) {
			tok = "wrong"
		}
		// Only a refusal the record keeps may change it.
		_, kept, err := r.Report(s, id, tok, outcome, "summary", nil, now)
		if kept != nil {
			return "report", kept, true
		}
		return "report", err, false
	case 15:
		_, err = r.Ask(s, w.attempt(contract.AttemptWorking), "which?", nil, now)
		return "ask", err, false
	case 16, 17:
		return "checking", r.Checking(s, w.task(contract.TaskReview), contract.Checked{OID: "abc"}, now), false
	case 18, 19:
		how := contract.Accepted{How: howCheck}
		if some(10) {
			how.How = howByHand
		}
		_, err = r.Checked(s, w.task(contract.TaskReview), contract.CheckResult{OK: !some(3), Tail: "tail"}, how, now)
		return "checked", err, false
	case 20:
		return "reject", r.Reject(s, w.task(contract.TaskReview), "why", pick(w, []contract.Liveness{contract.Live, contract.Gone, contract.Unverifiable}), now), false
	case 21:
		return "stop", r.Stop(s, w.task(contract.TaskRunning, contract.TaskReview), some(2), now), false
	case 22, 23, 24:
		id := w.attempt()
		r.Seen(s, id, w.pane(s.Attempts[id]), !some(4), limits, now)
		return "seen", nil, false
	case 25:
		r.LeadSeen(s, w.pane(nil), some(2), now)
		return "lead seen", nil, false
	case 26:
		var kinds []string
		if some(2) {
			kinds = []string{"done", "question", "answered"}
		}
		_, events, _, err := r.Batch(s, kinds, now)
		for _, e := range events {
			w.seqs[e.Seq] = true
		}
		return "wait", err, false
	case 27:
		id := "d1"
		if b := s.Inbox.Outstanding; b != nil && !some(6) {
			id = b.ID
		}
		events, err := r.Ack(s, id)
		w.acked += len(events)
		return "ack", err, false
	case 28:
		q := contract.Question{Form: pick(w, append(forms, "poll")), Text: "?", Holds: some(3)}
		if some(2) {
			q.Task = w.task()
		}
		_, err = r.Need(s, q, now)
		return "need", err, false
	case 29, 30:
		return "answer", r.Answer(s, w.question(), pick(w, answers), pick(w, origins), time.Duration(w.IntN(2))*4*time.Second, now), false
	case 31:
		switch w.IntN(4) {
		case 0:
			return "undo", r.Undo(s, w.question(), pick(w, origins), now), false
		case 1:
			return "escalate", r.Escalate(s, w.question(), now), false
		case 2:
			return "need close", r.CloseQuestion(s, w.question(), now), false
		}
		_, _, err = r.Use(s, w.question(), now)
		return "use", err, false
	case 32:
		switch w.IntN(3) {
		case 0:
			_, err = r.Tell(s, pick(w, append(taskIDs, lead)), "note", "", now)
			return "tell", err, false
		case 1:
			_, err = r.ReadMail(s, w.attempt(), now)
			return "mail", err, false
		}
		id, seq := w.attempt(), w.IntN(s.Counters.Seq+2)
		if a := s.Attempts[id]; a != nil && len(a.Mail) > 0 {
			seq = pick(w, a.Mail).Seq
		}
		return "pointed", r.Pointed(s, id, seq, now), false
	}
	switch w.IntN(12) {
	case 0, 1:
		return "pause", r.Pause(s, "you", now), false
	case 2, 3, 4, 5:
		return "resume", r.Resume(s, now), false
	case 6:
		return "takeover", r.Takeover(s, contract.Binding{Pane: pick(w, []string{"w1:p1", "w1:p7"})}, now), false
	case 7:
		if some(4) {
			return "run close", r.CloseRun(s, some(2), now), false
		}
		return "resume", r.Resume(s, now), false
	case 8, 9:
		return "worktree", r.SetWorktree(s, w.task(), &contract.Worktree{Path: "wt"}, now), false
	}
	return "allowed", r.Allowed(s, contract.Caller{Kind: pick(w, []contract.CallerKind{contract.Worker, contract.Orchestrator, contract.Human, contract.Unbound}),
		Pane: "w1:p1", Attempt: w.attempt()}, pick(w, contract.Commands).Name, pick(w, []string{"", "close", "add", "lead"})), false
}

// check holds the record to its invariants and to what can never be undone.
func (w *world) check() string {
	s := w.s
	if msg := invariants(s); msg != "" {
		return msg
	}
	seen := len(w.seqs)
	for _, e := range s.Inbox.Events {
		if !w.seqs[e.Seq] {
			seen++
		}
	}
	mails := 0
	for _, a := range s.Attempts {
		mails += len(a.Mail)
	}
	if got := w.acked + len(s.Inbox.Events) + mails; got != s.Counters.Seq {
		return fmt.Sprintf("%d events and messages were counted and %d exist: one was lost or doubled", s.Counters.Seq, got)
	}
	for id, t := range s.Tasks {
		if t.Status == contract.TaskDone {
			if was, ok := w.done[id]; ok && was != *t.Accepted {
				return id + " was accepted a second time"
			}
			w.done[id] = *t.Accepted
		} else if _, was := w.done[id]; was {
			return id + " was done and is " + string(t.Status)
		}
	}
	for id, q := range s.Questions {
		if was, ok := w.used[id]; ok && (q.State != contract.QuestionUsed || q.Answer != was) {
			return id + " was used and has changed"
		}
		if q.State == contract.QuestionUsed {
			w.used[id] = q.Answer
		}
		if w.closed[id] && q.From != contract.FromTool && q.State != contract.QuestionClosed {
			return id + " was closed and is " + q.State
		}
		w.closed[id] = q.State == contract.QuestionClosed
	}
	return ""
}

func (w *world) run(t *testing.T, seed uint64, steps int, strict bool) map[string]int {
	legal := map[string]int{}
	var log []string
	for i := range steps {
		w.now = w.now.Add(time.Duration(w.IntN(40)) * time.Second)
		if w.IntN(30) == 0 {
			w.now = w.now.Add(time.Duration(w.IntN(40)) * time.Minute)
		}
		was := ""
		if strict {
			was = snapshot(w.s)
		}
		name, err, mayChange := w.step()
		log = append(log, fmt.Sprintf("%s -> %s", name, code(err)))
		switch {
		case err == nil:
			legal[name]++
		case code(err) == "?":
			t.Fatalf("seed %d, step %d: %s gave an error that is no refusal: %v", seed, i, name, err)
		case strict && !mayChange && snapshot(w.s) != was:
			t.Fatalf("seed %d, step %d: the refused %s changed the record\n%s", seed, i, name, strings.Join(log, "\n"))
		}
		if msg := w.check(); msg != "" {
			t.Fatalf("seed %d, step %d, after %s: %s\n%s", seed, i, name, msg, strings.Join(log, "\n"))
		}
	}
	return legal
}

func newWorld(seed uint64, s *contract.State, now time.Time) *world {
	w := &world{Rand: rand.New(rand.NewPCG(seed, 7)), s: s, now: now, seqs: map[int]bool{}, done: map[string]contract.Accepted{}, used: map[string]string{}, closed: map[string]bool{}}
	for _, a := range s.Attempts {
		a.TokenHash = token
	}
	w.acked = s.Inbox.Acked
	for _, a := range s.Attempts {
		w.acked -= len(a.Mail)
	}
	return w
}

// 10,000 random command sequences keep the invariants. Every tenth also
// proves, command by command, that a refusal changes nothing.
func TestRandomSequences(t *testing.T) {
	sequences, steps := 10000, 200
	if testing.Short() {
		sequences = 500
	}
	legal := map[string]int{}
	for seed := range uint64(sequences) {
		w := newWorld(seed, newRun(), t0)
		for name, n := range w.run(t, seed, steps, seed%10 == 0) {
			legal[name] += n
		}
	}
	for _, name := range []string{"task add", "task edit", "task cancel", "task reset", "start", "placed", "working", "start failed", "progress", "report",
		"ask", "checking", "checked", "reject", "stop", "wait", "ack", "need", "answer", "undo", "escalate", "need close", "use", "tell", "mail", "pointed",
		"pause", "resume", "takeover", "run close", "worktree", "allowed"} {
		if legal[name] < sequences/100 {
			t.Errorf("%s was allowed only %d times: the sequences do not reach it", name, legal[name])
		}
	}
}

// The same from the evening fixture: a record this package did not write.
func TestRandomFromFixture(t *testing.T) {
	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	if msg := invariants(&f.State); msg != "" {
		t.Fatalf("the fixture breaks the invariants: %s", msg)
	}
	sequences := 500
	if testing.Short() {
		sequences = 50
	}
	base := snapshot(&f.State)
	for seed := range uint64(sequences) {
		var s contract.State
		if err := json.Unmarshal([]byte(base), &s); err != nil {
			t.Fatal(err)
		}
		newWorld(seed, &s, f.Now).run(t, seed, 80, seed%10 == 0)
	}
}

func TestPlug(t *testing.T) {
	k := contract.NewKit()
	Plug(k)
	if _, ok := k.Rules.(Rules); !ok {
		t.Error("the kit has no rules")
	}
}
