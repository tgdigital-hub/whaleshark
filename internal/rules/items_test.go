package rules

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// The four forms of an item and what each takes as an answer (6.6, 8n).
func TestForms(t *testing.T) {
	rows := []struct {
		form    string
		options []string
		text    string
		stored  string
		ok      bool
	}{
		{contract.FormQuestion, nil, "the blue logo", "the blue logo", true},
		{contract.FormQuestion, nil, "   ", "", false},
		{contract.FormChoice, nil, "Yes", "yes", true},
		{contract.FormChoice, nil, "no", "no", true},
		{contract.FormChoice, nil, "maybe", "", false},
		{contract.FormChoice, nil, "other: ask me on Monday", "other: ask me on Monday", true},
		{contract.FormChoice, nil, "other:", "", false},
		{contract.FormChoice, []string{"main", "release"}, "RELEASE", "release", true},
		{contract.FormChoice, []string{"main", "release"}, "yes", "", false},
		{contract.FormSignoff, nil, "approve", "approve", true},
		{contract.FormSignoff, nil, "send back: no tests", "send back: no tests", true},
		{contract.FormSignoff, nil, "send back:", "", false},
		{contract.FormSignoff, nil, "yes", "", false},
		{contract.FormTodo, nil, "done", "done", true},
		{contract.FormTodo, nil, "cannot: no access", "cannot: no access", true},
		{contract.FormTodo, nil, "cannot: ", "", false},
		{contract.FormTodo, nil, "approve", "", false},
	}
	for _, row := range rows {
		s := newRun()
		id, err := r.Need(s, contract.Question{Form: row.form, Text: "?", Options: row.options}, t0)
		must(t, err)
		was := snapshot(s)
		err = r.Answer(s, id, row.text, human, 4*time.Second, t0)
		q := s.Questions[id]
		switch {
		case row.ok && (err != nil || q.Answer != row.stored || q.State != contract.QuestionAnswered || q.SettlesAt != after(4*time.Second) || *q.AnsweredBy != human):
			t.Errorf("%s answered %q: %v, stored %+v", row.form, row.text, err, q)
		case !row.ok && (code(err) != "bad_answer" || exit(err) != contract.ExitUsage || snapshot(s) != was):
			t.Errorf("%s answered %q: %v", row.form, row.text, err)
		}
	}
	s := newRun()
	if _, err := r.Need(s, contract.Question{Form: "poll", Text: "?"}, t0); code(err) != "bad_form" {
		t.Errorf("an unknown form: %v", err)
	}
	if _, err := r.Need(s, contract.Question{Form: contract.FormTodo, Text: "?", Holds: true}, t0); code(err) != "no_task" {
		t.Errorf("holding no task: %v", err)
	}
	if _, err := r.Need(s, contract.Question{Form: contract.FormTodo, Text: "?", Task: "T9"}, t0); exit(err) != contract.ExitMissing {
		t.Errorf("about no task: %v", err)
	}
	id, err := r.Need(s, contract.Question{ID: "x", Kind: contract.KindAsk, For: contract.ForOrchestrator, From: "T1.1", State: contract.QuestionUsed, Answer: "a", Form: contract.FormChoice, Text: "go?"}, t0)
	must(t, err)
	if q := s.Questions[id]; id != "n1" || q.Kind != contract.KindNeed || q.For != contract.ForHuman || q.From != contract.FromLead ||
		q.State != contract.QuestionOpen || q.Answer != "" || !slices.Equal(q.Options, []string{"yes", "no"}) {
		t.Errorf("a need is stored as %+v", q)
	}
}

// The life of an item: open, answered, settling, undone, used, closed (6.6, 8n).
func TestSettlingUndoUsed(t *testing.T) {
	settle := 4 * time.Second
	need := func(s *contract.State) string {
		id, err := r.Need(s, contract.Question{Form: contract.FormChoice, Text: "keep the old link?"}, t0)
		must(t, err)
		return id
	}
	t.Run("nothing uses an answer before it has settled", func(t *testing.T) {
		s := newRun()
		id := need(s)
		if _, ok, err := r.Use(s, id, t0); ok || err != nil {
			t.Errorf("an open item was used: %v", err)
		}
		must(t, r.Answer(s, id, "yes", human, settle, t0))
		if _, ok, _ := r.Use(s, id, after(settle-time.Millisecond)); ok {
			t.Error("used while settling")
		}
		if b, _, _, _ := r.Batch(s, nil, after(settle-time.Millisecond)); b != nil {
			t.Error("handed to the lead agent while settling")
		}
		got, ok, err := r.Use(s, id, after(settle))
		if !ok || err != nil || got != "yes" || s.Questions[id].UsedAt != after(settle) || s.Questions[id].State != contract.QuestionUsed {
			t.Errorf("after settling: %q %v %v", got, ok, err)
		}
		if got, ok, _ := r.Use(s, id, after(time.Hour)); !ok || got != "yes" || s.Questions[id].UsedAt != after(settle) {
			t.Error("a used answer is not handed out again as it was")
		}
		if e := s.Inbox.Events[0]; e.Kind != "answered" || e.Text != "yes" || e.Data["id"] != id {
			t.Errorf("the lead agent is told %+v", e)
		}
	})
	t.Run("a typed line settles at once", func(t *testing.T) {
		s := newRun()
		id := need(s)
		must(t, r.Answer(s, id, "other: ask Ana", contract.Origin{Caller: contract.Human, Where: contract.WhereTyped}, 0, t0))
		if _, ok, _ := r.Use(s, id, t0); !ok {
			t.Error("not usable at once")
		}
	})
	t.Run("undone while settling, after settling, and after it was used", func(t *testing.T) {
		s := newRun()
		id := need(s)
		if err := r.Undo(s, id, human, t0); code(err) != "not_answered" {
			t.Errorf("undo of an open item: %v", err)
		}
		must(t, r.Answer(s, id, "yes", human, settle, t0))
		if err := r.Undo(s, id, orch, t0); code(err) != "not_human" {
			t.Errorf("undo by the lead agent: %v", err)
		}
		must(t, r.Undo(s, id, human, after(time.Second)))
		q := s.Questions[id]
		if q.State != contract.QuestionOpen || q.Answer != "" || q.AnsweredBy != nil || !q.SettlesAt.IsZero() || len(q.Earlier) != 1 ||
			q.Earlier[0].Answer != "yes" || q.Earlier[0].At != t0 || q.Earlier[0].UndoneAt != after(time.Second) {
			t.Fatalf("after undo: %+v", q)
		}
		must(t, r.Answer(s, id, "no", human, settle, after(time.Minute)))
		must(t, r.Undo(s, id, human, after(time.Hour)))
		must(t, r.Answer(s, id, "yes", human, 0, after(time.Hour)))
		if _, ok, _ := r.Use(s, id, after(time.Hour)); !ok {
			t.Fatal("not used")
		}
		err := r.Undo(s, id, human, after(2*time.Hour))
		if code(err) != "used" || err.Error() != "Already acted on; tell the lead agent." || len(q.Earlier) != 2 || q.Answer != "yes" {
			t.Errorf("undo after use: %v, %+v", err, q)
		}
		if err := r.Answer(s, id, "no", human, 0, after(2*time.Hour)); code(err) != "used" {
			t.Errorf("another answer after use: %v", err)
		}
	})
	t.Run("two answers never race", func(t *testing.T) {
		s := newRun()
		id := need(s)
		must(t, r.Answer(s, id, "yes", human, settle, t0))
		was := snapshot(s)
		must(t, r.Answer(s, id, "YES", human, settle, after(time.Second)))
		if snapshot(s) != was {
			t.Error("the same answer twice changed the record")
		}
		if err := r.Answer(s, id, "no", human, settle, t0); code(err) != "answered" || exit(err) != contract.ExitRefused {
			t.Errorf("a different answer: %v", err)
		}
	})
	t.Run("the lead agent answers for the person only with --relayed", func(t *testing.T) {
		s := newRun()
		id := need(s)
		if err := r.Answer(s, id, "yes", orch, 0, t0); code(err) != "for_human" {
			t.Errorf("not relayed: %v", err)
		}
		if err := r.Answer(s, id, "yes", contract.Origin{Caller: contract.Worker}, 0, t0); code(err) != "not_yours" {
			t.Errorf("a worker: %v", err)
		}
		relayed := contract.Origin{Caller: contract.Orchestrator, Where: contract.WhereRelayed}
		must(t, r.Answer(s, id, "yes", relayed, 0, t0))
		if *s.Questions[id].AnsweredBy != relayed {
			t.Error("the record does not say it was relayed")
		}
		if err := r.Answer(s, "n9", "yes", human, 0, t0); exit(err) != contract.ExitMissing {
			t.Errorf("no such item: %v", err)
		}
	})
	t.Run("the lead agent's wait hands the answer over, and that makes it final", func(t *testing.T) {
		s := newRun()
		id := need(s)
		must(t, r.Answer(s, id, "no", human, settle, t0))
		b, events, _, err := r.Batch(s, []string{"answered"}, after(settle))
		if err != nil || b == nil || len(events) != 1 || events[0].Kind != "answered" || s.Questions[id].State != contract.QuestionUsed {
			t.Fatalf("batch %+v %v %v", b, events, err)
		}
		if err := r.Undo(s, id, human, after(time.Minute)); code(err) != "used" {
			t.Errorf("undo after the hand-over: %v", err)
		}
	})
	t.Run("withdrawn", func(t *testing.T) {
		s := in(t, contract.AttemptAsked)
		id := need(s)
		must(t, r.CloseQuestion(s, id, t0))
		must(t, r.CloseQuestion(s, id, t0))
		if err := r.Answer(s, id, "yes", human, 0, t0); code(err) != "closed" {
			t.Errorf("an answer to a withdrawn item: %v", err)
		}
		if _, _, err := r.Use(s, id, t0); code(err) != "closed" {
			t.Errorf("use of a withdrawn item: %v", err)
		}
		if err := r.CloseQuestion(s, "q1", t0); code(err) != "not_an_item" {
			t.Errorf("need close on a worker's question: %v", err)
		}
		id = need(s)
		must(t, r.Answer(s, id, "yes", human, 0, t0))
		r.Use(s, id, t0)
		if err := r.CloseQuestion(s, id, t0); code(err) != "used" {
			t.Errorf("need close after use: %v", err)
		}
	})
}

// 8b and 8d: a worker's question, escalating it, and an item that holds a task.
func TestAskAndHold(t *testing.T) {
	t.Run("ask, answer, use", func(t *testing.T) {
		s := in(t, contract.AttemptWorking)
		id, err := r.Ask(s, "T1.1", "Which base branch?", []string{"main", "release"}, t0)
		must(t, err)
		q, a := s.Questions[id], s.Attempts["T1.1"]
		if id != "q1" || q.Form != contract.FormChoice || q.For != contract.ForOrchestrator || q.From != "T1.1" || q.Task != "T1" ||
			a.State != contract.AttemptAsked || s.Inbox.Events[0].Kind != "question" {
			t.Fatalf("asked %+v", q)
		}
		must(t, r.Answer(s, id, "main", orch, 0, after(time.Minute)))
		if a.State != contract.AttemptAsked {
			t.Error("the attempt works again before the answer reached it")
		}
		got, ok, err := r.Use(s, id, after(time.Minute))
		if got != "main" || !ok || err != nil || a.State != contract.AttemptWorking || len(s.Inbox.Events) != 1 {
			t.Errorf("use: %q %v %v, attempt %s", got, ok, err, a.State)
		}
	})
	t.Run("escalated: the person answers, the worker gets it and the lead agent is told", func(t *testing.T) {
		s := in(t, contract.AttemptAsked)
		must(t, r.Escalate(s, "q1", t0))
		must(t, r.Escalate(s, "q1", t0))
		if err := r.Answer(s, "q1", "main", orch, 0, t0); code(err) != "for_human" {
			t.Errorf("the lead agent answers an escalated question: %v", err)
		}
		must(t, r.Answer(s, "q1", "main", human, 4*time.Second, t0))
		if b, _, _, _ := r.Batch(s, []string{"answered"}, after(time.Minute)); b != nil {
			t.Error("the lead agent's wait took the worker's answer")
		}
		if _, ok, _ := r.Use(s, "q1", after(time.Minute)); !ok {
			t.Fatal("not used")
		}
		if e := s.Inbox.Events[len(s.Inbox.Events)-1]; e.Kind != "answered" || e.Attempt != "T1.1" || e.Text != "main" {
			t.Errorf("last event %+v", e)
		}
		if err := r.Escalate(s, "q1", t0); exit(err) != contract.ExitMissing {
			t.Errorf("escalating a used question: %v", err)
		}
		if err := r.Escalate(s, "q9", t0); exit(err) != contract.ExitMissing {
			t.Errorf("escalating nothing: %v", err)
		}
	})
	t.Run("the attempt ends: the question closes, and a late answer is kept for the next prompt", func(t *testing.T) {
		for _, state := range []string{"open", "answered"} {
			s := in(t, contract.AttemptAsked)
			if state == "answered" {
				must(t, r.Answer(s, "q1", "main", orch, 0, t0))
			}
			must(t, r.Stop(s, "T1", false, t0))
			if s.Questions["q1"].State != contract.QuestionClosed {
				t.Fatalf("question is %s", s.Questions["q1"].State)
			}
			if _, _, err := r.Use(s, "q1", t0); code(err) != "closed" || err.Error() != "Your attempt has ended." {
				t.Errorf("a blocked ask: %v", err)
			}
			must(t, r.Answer(s, "q1", "main", orch, 0, after(time.Minute)))
			must(t, r.Answer(s, "q1", "main", orch, 0, after(time.Hour)))
			d := s.Tasks["T1"].Decisions
			if len(d) != 1 || d[0].Question != "which base?" || d[0].Answer != "main" || s.Questions["q1"].State != contract.QuestionClosed {
				t.Errorf("%s: decisions %+v", state, d)
			}
			if err := r.Undo(s, "q1", human, after(time.Hour)); code(err) != "used" {
				t.Errorf("undo of a kept answer: %v", err)
			}
		}
	})
	t.Run("an item that holds a task", func(t *testing.T) {
		s := newRun()
		add(t, s, "T1")
		id, err := r.Need(s, contract.Question{Form: contract.FormChoice, Text: "keep the old API?", Task: "task T1", Holds: true}, t0)
		must(t, err)
		start := func(at time.Time) error {
			_, err := r.Start(s, "T1", false, false, token, agent, true, at)
			return err
		}
		if err := start(t0); code(err) != "held" {
			t.Errorf("start while held: %v", err)
		}
		must(t, r.Answer(s, id, "yes", human, 4*time.Second, t0))
		if err := start(after(time.Second)); code(err) != "held" {
			t.Errorf("start while the answer settles: %v", err)
		}
		must(t, start(after(4*time.Second)))
		q, task := s.Questions[id], s.Tasks["T1"]
		if q.State != contract.QuestionUsed || len(task.Decisions) != 1 || task.Decisions[0].Answer != "yes" {
			t.Errorf("item %+v, decisions %+v", q, task.Decisions)
		}
		if err := r.Undo(s, id, human, after(time.Minute)); code(err) != "used" {
			t.Errorf("undo after the start went past: %v", err)
		}

		// The same in review: the accept is held, and the answer reaches the live attempt as mail.
		s = in(t, contract.AttemptReported)
		id, _ = r.Need(s, contract.Question{Form: contract.FormSignoff, Text: "ship it?", Task: "T1", Holds: true}, t0)
		if err := r.Checking(s, "T1", contract.Checked{}, t0); code(err) != "held" || s.Attempts["T1.1"].State != contract.AttemptReported {
			t.Errorf("accept while held: %v", err)
		}
		must(t, r.Answer(s, id, "approve", human, 0, t0))
		must(t, r.Checking(s, "T1", contract.Checked{}, t0))
		if mail := s.Attempts["T1.1"].Mail; len(mail) != 1 || mail[0].Text != "Decided: ship it? Answer: approve" {
			t.Errorf("mail %+v", mail)
		}
		// Cancelling a task closes what was open about it.
		s = newRun()
		add(t, s, "T1")
		id, _ = r.Need(s, contract.Question{Form: contract.FormTodo, Text: "check the bill", Task: "T1"}, t0)
		must(t, r.CancelTask(s, "T1", t0))
		if s.Questions[id].State != contract.QuestionClosed {
			t.Error("the item of a cancelled task stays open")
		}
	})
}

// 6.5: the batch, the replay and the acknowledgement.
func TestInbox(t *testing.T) {
	s := newRun()
	if b, _, _, err := r.Batch(s, nil, t0); b != nil || err != nil {
		t.Fatalf("a batch from nothing: %+v %v", b, err)
	}
	for i := 1; i <= 60; i++ {
		kind := "done"
		if i == 55 {
			kind = "question"
		}
		if seq := r.Raise(s, contract.Event{Kind: kind}, t0); seq != i {
			t.Fatalf("seq %d, want %d", seq, i)
		}
	}
	if _, _, _, err := r.Batch(s, []string{"nonsense"}, t0); code(err) != "bad_kind" || exit(err) != contract.ExitUsage {
		t.Errorf("an unknown kind: %v", err)
	}
	if b, _, _, _ := r.Batch(s, []string{"failed", "exited"}, t0); b != nil || s.Inbox.Outstanding != nil {
		t.Error("a batch for kinds that are not there")
	}
	// A wanted kind far back hands out everything older first, up to 50.
	b, events, replayed, err := r.Batch(s, []string{"question"}, t0)
	if err != nil || replayed || b.ID != "d1" || b.From != 1 || b.To != 50 || len(events) != contract.MaxBatch || events[49].Seq != 50 {
		t.Fatalf("first batch %+v, %d events, %v", b, len(events), err)
	}
	r.Raise(s, contract.Event{Kind: "failed"}, t0)
	again, events, replayed, _ := r.Batch(s, []string{"exited"}, after(time.Hour))
	if !replayed || *again != *b || len(events) != 50 || s.Counters.Delivery != 1 {
		t.Errorf("replay %+v, %d events, replayed %v", again, len(events), replayed)
	}
	if _, err := r.Ack(s, "d7"); exit(err) != contract.ExitMissing {
		t.Errorf("ack of a delivery that never was: %v", err)
	}
	if _, err := r.Ack(s, "17"); exit(err) != contract.ExitMissing {
		t.Errorf("ack of nonsense: %v", err)
	}
	acked, err := r.Ack(s, "d1")
	if err != nil || len(acked) != 50 || s.Inbox.Acked != 50 || s.Inbox.Outstanding != nil || len(s.Inbox.Events) != 11 || s.Inbox.Events[0].Seq != 51 {
		t.Fatalf("ack: %d events, %v, inbox %+v", len(acked), err, s.Inbox.Outstanding)
	}
	was := snapshot(s)
	if acked, err := r.Ack(s, "d1"); err != nil || len(acked) != 0 || snapshot(s) != was {
		t.Errorf("acknowledging twice: %v, %d events", err, len(acked))
	}
	b, events, _, _ = r.Batch(s, nil, t0)
	if b.ID != "d2" || b.From != 51 || b.To != 61 || len(events) != 11 {
		t.Errorf("second batch %+v", b)
	}
	if _, err := r.Ack(s, "d1"); code(err) != "not_outstanding" {
		t.Errorf("an old ack while another delivery is out: %v", err)
	}
	r.Ack(s, "d2")
	if len(s.Inbox.Events) != 0 || s.Inbox.Acked != 61 {
		t.Errorf("inbox %+v", s.Inbox)
	}
}

// 8e, row by row: what one sweep does with what herdr shows for a pane.
func TestSeen(t *testing.T) {
	kinds := func(s *contract.State) (out []string) {
		for _, e := range s.Inbox.Events {
			out = append(out, e.Kind)
		}
		return out
	}
	type step struct {
		at   time.Duration
		pane *contract.Pane
		want bool
	}
	gone, shell := (*contract.Pane)(nil), pane("")
	restarted := pane(contract.StatusWorking)
	restarted.Terminal = "term2"
	bare := pane("")
	bare.Terminal = "term2"
	rows := []struct {
		name     string
		state    contract.AttemptState
		change   func(s *contract.State, l *contract.ProjectFile)
		steps    []step
		events   []string
		attempt  contract.AttemptState
		liveness contract.Liveness
	}{
		{"working and nothing new is no write", "working", nil,
			[]step{{5 * time.Second, pane("working"), false}, {10 * time.Minute, pane("working"), false}}, nil, "working", contract.Live},
		{"working with no progress note for 30 minutes is stale, once", "working", nil,
			[]step{{29 * time.Minute, pane("working"), false}, {30 * time.Minute, pane("working"), true}, {50 * time.Minute, pane("working"), false}},
			[]string{"stale"}, "working", contract.Live},
		{"a progress note ends the stale episode", "working",
			func(s *contract.State, _ *contract.ProjectFile) {
				r.Progress(s, "T1.1", 10, "reading", after(20*time.Minute))
			},
			[]step{{30 * time.Minute, pane("working"), false}, {50 * time.Minute, pane("working"), true}}, []string{"stale"}, "working", contract.Live},
		{"past its time limit is overtime, once, and nothing is stopped", "working",
			func(_ *contract.State, l *contract.ProjectFile) { l.Limits.MaxMinutes = 10 },
			[]step{{9 * time.Minute, pane("working"), false}, {10 * time.Minute, pane("working"), true}, {11 * time.Minute, pane("working"), false}},
			[]string{"overtime"}, "working", contract.Live},
		{"at a prompt is blocked, once, with an item for the person", "working", nil,
			[]step{{time.Minute, pane("blocked"), true}, {2 * time.Minute, pane("blocked"), false}}, []string{"blocked"}, "working", contract.Live},
		{"idle with no report for 120 s is quiet, once", "working", nil,
			[]step{{time.Minute, pane("idle"), true}, {2 * time.Minute, pane("done"), true}, {3*time.Minute + time.Second, pane("idle"), true}, {9 * time.Minute, pane("idle"), false}},
			[]string{"quiet"}, "working", contract.Unverifiable},
		{"working again clears the quiet flag", "working", nil,
			[]step{{time.Minute, pane("idle"), true}, {4 * time.Minute, pane("idle"), true}, {5 * time.Minute, pane("working"), true}},
			[]string{"quiet"}, "working", contract.Live},
		{"idle while asked is waiting as told", "asked", nil,
			[]step{{time.Minute, pane("idle"), true}, {time.Hour, pane("idle"), false}}, nil, "asked", contract.Live},
		{"idle after its report is no news", "reported", nil,
			[]step{{time.Minute, pane("done"), true}, {time.Hour, pane("done"), false}}, nil, "reported", contract.Live},
		{"unknown is unverifiable and nothing else", "working", nil,
			[]step{{time.Minute, pane("unknown"), true}, {time.Hour, pane("unknown"), false}}, nil, "working", contract.Unverifiable},
		{"a shell prompt, seen again 30 s later, is an exit", "working", nil,
			[]step{{time.Minute, shell, true}, {time.Minute + 29*time.Second, shell, false}, {time.Minute + 30*time.Second, shell, true}},
			[]string{"exited"}, "exited", contract.Gone},
		{"a pane that is not in the picture, the same", "asked", nil,
			[]step{{time.Minute, gone, true}, {2 * time.Minute, gone, true}}, []string{"exited"}, "exited", contract.Gone},
		{"twice within one second proves nothing", "working", nil,
			[]step{{time.Minute, gone, true}, {time.Minute + time.Second, gone, false}}, nil, "working", contract.Live},
		{"missing for 5 s and back", "working", nil,
			[]step{{time.Minute, gone, true}, {time.Minute + 5*time.Second, pane("working"), true}, {2 * time.Minute, gone, true}, {2*time.Minute + 5*time.Second, gone, false}},
			nil, "working", contract.Live},
		{"gone after its report changes no state", "reported", nil,
			[]step{{time.Minute, gone, true}, {2 * time.Minute, gone, true}, {3 * time.Minute, gone, false}}, nil, "reported", contract.Gone},
		{"starting with its start lock held is left alone", "starting", nil, []step{{time.Hour, gone, false}}, nil, "starting", contract.Unverifiable},
		{"herdr restarted: nobody is declared gone for 120 s", "working", nil,
			[]step{{time.Minute, restarted, true}, {time.Minute + 5*time.Second, bare, false}, {time.Minute + 119*time.Second, bare, false},
				{3 * time.Minute, bare, true}, {3*time.Minute + 29*time.Second, bare, false}},
			nil, "working", contract.Unverifiable},
		{"herdr restarted and the agent came back", "working", nil,
			[]step{{time.Minute, restarted, true}, {time.Minute + 5*time.Second, restarted, true}, {2 * time.Minute, restarted, false}},
			nil, "working", contract.Live},
		{"paused: no quiet, stale or overtime", "working",
			func(s *contract.State, l *contract.ProjectFile) { l.Limits.MaxMinutes = 1; r.Pause(s, "you", t0) },
			[]step{{time.Minute, pane("idle"), true}, {time.Hour, pane("idle"), false}}, []string{"paused"}, "working", contract.Live},
		{"paused and working two minutes later is still running, once", "working",
			func(s *contract.State, l *contract.ProjectFile) { r.Pause(s, "you", t0) },
			[]step{{time.Minute, pane("working"), false}, {2 * time.Minute, pane("working"), true}, {time.Hour, pane("working"), false}},
			[]string{"paused", "still_running"}, "working", contract.Live},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			s, l := in(t, row.state), limits
			s.Inbox.Events = nil
			if row.change != nil {
				row.change(s, &l)
			}
			for i, st := range row.steps {
				was := snapshot(s)
				got := r.Seen(s, "T1.1", st.pane, true, l, after(st.at))
				if got != st.want {
					t.Errorf("step %d at %v: changed %v, want %v", i, st.at, got, st.want)
				}
				s.Attempts["T1.1"].Herdr.LastWorking = time.Time{}
				cmp := snapshot(s)
				var before contract.State
				json.Unmarshal([]byte(was), &before)
				before.Attempts["T1.1"].Herdr.LastWorking = time.Time{}
				if !got && cmp != snapshot(&before) {
					t.Errorf("step %d said nothing changed, and something did", i)
				}
				if msg := invariants(s); msg != "" {
					t.Error(msg)
				}
			}
			a := s.Attempts["T1.1"]
			if got := kinds(s); !slices.Equal(got, row.events) || a.State != row.attempt || a.Herdr.Liveness != row.liveness {
				t.Errorf("events %v, attempt %s, liveness %s; want %v, %s, %s", got, a.State, a.Herdr.Liveness, row.events, row.attempt, row.liveness)
			}
		})
	}

	t.Run("an exit counts as a failure and frees the task", func(t *testing.T) {
		s := in(t, contract.AttemptExited)
		a, task := s.Attempts["T1.1"], s.Tasks["T1"]
		if task.Status != contract.TaskReady || task.Failures != 1 || a.Exit.Cause != contract.ExitExited || a.EndedAt.IsZero() {
			t.Errorf("task %s with %d failures, exit %+v", task.Status, task.Failures, a.Exit)
		}
	})
	t.Run("a start that died lists what exists", func(t *testing.T) {
		s := in(t, contract.AttemptStarting)
		must(t, r.Placed(s, "T1.1", contract.Place{Tab: "w1:t2", Pane: "w1:p2"}, t0))
		if !r.Seen(s, "T1.1", nil, false, limits, t0) {
			t.Fatal("no change")
		}
		e := s.Inbox.Events[0]
		if e.Kind != "start_failed" || e.Data["created"].(map[string]any)["tab"] != "w1:t2" || s.Tasks["T1"].Failures != 0 || s.Tasks["T1"].Status != contract.TaskReady {
			t.Errorf("event %+v", e)
		}
	})
	t.Run("the prompt item clears when the prompt is answered", func(t *testing.T) {
		s := in(t, contract.AttemptWorking)
		r.Seen(s, "T1.1", pane("blocked"), true, limits, after(time.Minute))
		q := s.Questions["n1"]
		if q == nil || q.From != contract.FromTool || q.Cause != contract.CausePrompt || q.Form != contract.FormTodo || q.Attempt != "T1.1" ||
			q.Text != "task T1 is waiting at a prompt in its tab." || q.State != contract.QuestionOpen {
			t.Fatalf("item %+v", q)
		}
		if !r.Seen(s, "T1.1", pane("working"), true, limits, after(2*time.Minute)) || q.State != contract.QuestionClosed {
			t.Errorf("item is %s", q.State)
		}
		r.Seen(s, "T1.1", pane("blocked"), true, limits, after(3*time.Minute))
		must(t, r.Stop(s, "T1", false, after(4*time.Minute)))
		if s.Questions["n2"].State != contract.QuestionClosed {
			t.Error("the item outlived its attempt")
		}
	})
	t.Run("herdr's new terminal id and the agent's session are recorded", func(t *testing.T) {
		s := in(t, contract.AttemptWorking)
		p := pane("working")
		p.Terminal, p.Session = "term2", "session-9"
		r.Seen(s, "T1.1", p, true, limits, after(time.Minute))
		r.Seen(s, "T1.1", p, true, limits, after(2*time.Minute))
		if a := s.Attempts["T1.1"]; a.Place.Terminal != "term2" || a.Agent.Session != "session-9" {
			t.Errorf("place %+v, agent %+v", a.Place, a.Agent)
		}
	})
	t.Run("most workers stopped together", func(t *testing.T) {
		s := newRun()
		s.Run.Limit = 5
		for _, id := range []string{"T1", "T2", "T3", "T4"} {
			add(t, s, id)
			a, err := r.Start(s, id, false, false, token, agent, true, t0)
			must(t, err)
			must(t, r.Working(s, a, t0))
		}
		r.Seen(s, "T1.1", pane("idle"), true, limits, after(time.Minute))
		r.Seen(s, "T2.1", pane("idle"), true, limits, after(time.Minute+30*time.Second))
		if len(s.Inbox.Events) != 0 {
			t.Fatalf("half is not most: %v", kinds(s))
		}
		r.Seen(s, "T3.1", pane("done"), true, limits, after(2*time.Minute))
		r.Seen(s, "T4.1", pane("working"), true, limits, after(2*time.Minute))
		r.Seen(s, "T4.1", pane("idle"), true, limits, after(2*time.Minute+5*time.Second))
		q := s.Questions["n1"]
		if got := kinds(s); !slices.Equal(got, []string{"together"}) || q == nil || !q.Urgent || q.Cause != contract.CauseTogether || q.State != contract.QuestionOpen {
			t.Fatalf("events %v, item %+v", got, q)
		}
		if !r.Seen(s, "T2.1", pane("working"), true, limits, after(3*time.Minute)) || q.State != contract.QuestionClosed {
			t.Errorf("the item stays when a worker carries on: %s", q.State)
		}
	})
}

// 8e, the two rows about the lead agent's own pane.
func TestLeadSeen(t *testing.T) {
	lead := func(status string) *contract.Pane {
		return &contract.Pane{ID: "w1:p1", Agent: "claude", Status: status}
	}
	item := func(s *contract.State, cause string) *contract.Question {
		for _, q := range s.Questions {
			if q.Cause == cause {
				return q
			}
		}
		return nil
	}
	shown := func(s *contract.State, cause string) bool {
		q := item(s, cause)
		return q != nil && q.State == contract.QuestionOpen
	}
	s := newRun()
	steps := []struct {
		at      time.Duration
		status  string
		waiting bool
		changed bool
		unread  bool
	}{
		{0, contract.StatusWorking, true, false, false},
		{time.Minute, contract.StatusDone, true, true, false},
		{time.Minute + 59*time.Second, contract.StatusDone, true, false, false},
		{2 * time.Minute, contract.StatusDone, true, true, true},
		{3 * time.Minute, contract.StatusDone, true, false, true},
		{4 * time.Minute, contract.StatusIdle, true, true, false},
		{5 * time.Minute, contract.StatusIdle, true, false, false},
		{6 * time.Minute, contract.StatusDone, true, true, false},
		{6*time.Minute + 30*time.Second, contract.StatusWorking, true, true, false},
		{7 * time.Minute, contract.StatusDone, true, true, false},
		{7*time.Minute + 59*time.Second, contract.StatusDone, true, false, false},
		{8 * time.Minute, contract.StatusDone, true, true, true},
	}
	for i, st := range steps {
		if got := r.LeadSeen(s, lead(st.status), st.waiting, after(st.at)); got != st.changed || shown(s, contract.CauseLeadUnread) != st.unread {
			t.Errorf("step %d at %v: changed %v, shown %v", i, st.at, got, shown(s, contract.CauseLeadUnread))
		}
	}
	q := item(s, contract.CauseLeadUnread)
	if len(s.Questions) != 1 || q.Text != textUnread || q.From != contract.FromTool || q.Form != contract.FormTodo || q.CreatedAt != after(7*time.Minute) {
		t.Errorf("one item is kept for the cause: %+v", q)
	}
	must(t, r.Answer(s, q.ID, "done", human, 0, after(9*time.Minute)))
	if r.LeadSeen(s, lead(contract.StatusDone), true, after(10*time.Minute)) {
		t.Error("an item the person has dealt with was raised again in the same episode")
	}
	r.LeadSeen(s, lead(contract.StatusIdle), true, after(11*time.Minute))
	r.LeadSeen(s, lead(contract.StatusDone), true, after(12*time.Minute))
	if q.Answer != "" || len(q.Earlier) != 1 || q.Earlier[0].Answer != "done" {
		t.Errorf("the next episode starts clean and keeps what the person did: %+v", q)
	}

	// Not listening: events unread, no wait, the lead agent idle, for 60 s.
	s = newRun()
	if r.LeadSeen(s, lead(contract.StatusIdle), false, t0) {
		t.Error("nothing to listen for")
	}
	r.Raise(s, contract.Event{Kind: "done"}, t0)
	if r.LeadSeen(s, lead(contract.StatusWorking), false, t0) || r.LeadSeen(s, lead(contract.StatusIdle), true, t0) {
		t.Error("a lead agent that is working, or waiting, is listening")
	}
	r.LeadSeen(s, lead(contract.StatusIdle), false, after(time.Second))
	r.LeadSeen(s, lead(contract.StatusIdle), false, after(30*time.Second))
	if shown(s, contract.CauseLeadSilent) {
		t.Error("raised before 60 s")
	}
	r.LeadSeen(s, lead(contract.StatusIdle), false, after(61*time.Second))
	if !shown(s, contract.CauseLeadSilent) || item(s, contract.CauseLeadSilent).Text != textSilent || len(s.Inbox.Events) != 1 {
		t.Errorf("not raised: %+v", item(s, contract.CauseLeadSilent))
	}
	if !r.LeadSeen(s, lead(contract.StatusIdle), true, after(2*time.Minute)) || shown(s, contract.CauseLeadSilent) {
		t.Error("it stays when a wait starts")
	}
	if r.LeadSeen(s, nil, false, after(3*time.Minute)) {
		t.Error("a lead pane that is not in the picture")
	}
	if msg := invariants(s); msg != "" {
		t.Error(msg)
	}
}
