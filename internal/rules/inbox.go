package rules

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

func (Rules) Raise(s *contract.State, e contract.Event, now time.Time) int {
	s.Counters.Seq++
	e.Seq = s.Counters.Seq
	if e.At.IsZero() {
		e.At = now
	}
	s.Inbox.Events = append(s.Inbox.Events, e)
	return e.Seq
}

// Batch is also where the lead agent is handed what the person answered: a
// settled answer to an item the lead agent raised becomes an answered event
// here, and from then on cannot be taken back.
func (Rules) Batch(s *contract.State, kinds []string, now time.Time) (*contract.Batch, []contract.Event, bool, error) {
	for _, k := range kinds {
		if !slices.Contains(contract.EventKinds, k) {
			return nil, nil, false, refuse(contract.ExitUsage, "bad_kind", "There is no event kind %q.", k)
		}
	}
	in := &s.Inbox
	if b := in.Outstanding; b != nil {
		return new(*b), delivered(in, b), true, nil
	}
	for _, q := range questions(s) {
		if q.Kind == contract.KindNeed && q.From == contract.FromLead && settled(s, q, now) {
			use(s, q, now)
		}
	}
	wanted := func(e contract.Event) bool { return len(kinds) == 0 || slices.Contains(kinds, e.Kind) }
	if !slices.ContainsFunc(in.Events, wanted) {
		return nil, nil, false, nil
	}
	n := min(len(in.Events), contract.MaxBatch)
	s.Counters.Delivery++
	in.Outstanding = &contract.Batch{ID: "d" + strconv.Itoa(s.Counters.Delivery), From: in.Events[0].Seq, To: in.Events[n-1].Seq}
	return new(*in.Outstanding), delivered(in, in.Outstanding), false, nil
}

func delivered(in *contract.Inbox, b *contract.Batch) []contract.Event {
	n := 0
	for n < len(in.Events) && in.Events[n].Seq <= b.To {
		n++
	}
	return slices.Clone(in.Events[:n])
}

// Ack of a delivery that was acknowledged before changes nothing and
// returns nothing.
func (Rules) Ack(s *contract.State, delivery string) ([]contract.Event, error) {
	in := &s.Inbox
	if b := in.Outstanding; b != nil && b.ID == delivery {
		events := delivered(in, b)
		in.Events = slices.Clone(in.Events[len(events):])
		in.Acked, in.Outstanding = b.To, nil
		return events, nil
	}
	number, ok := strings.CutPrefix(delivery, "d")
	if n, err := strconv.Atoi(number); !ok || err != nil || n < 1 || n > s.Counters.Delivery {
		return nil, refuse(contract.ExitMissing, "no_such_delivery", "There is no delivery %q.", delivery)
	}
	if b := in.Outstanding; b != nil {
		return nil, next(refused("not_outstanding", "%s is not the delivery that is out; %s is.", delivery, b.ID), "whaleshark wait --ack "+b.ID)
	}
	return nil, nil
}

// mail stores a message for an attempt. Messages and events share one count.
func mail(s *contract.State, a *contract.Attempt, text, body string, now time.Time) int {
	s.Counters.Seq++
	a.Mail = append(a.Mail, contract.Mail{Seq: s.Counters.Seq, At: now, Text: text, Body: body})
	return s.Counters.Seq
}

// Tell with the task "lead" is the person's note to the lead agent, which
// reaches it as a human event.
func (r Rules) Tell(s *contract.State, task, text, body string, now time.Time) (int, error) {
	if task == lead {
		data := map[string]any{"what": "note"}
		if body != "" {
			data["body"] = body
		}
		return r.Raise(s, contract.Event{Kind: "human", Text: text, Data: data}, now), nil
	}
	t, a, err := r.taskAttempt(s, task)
	if err != nil {
		return 0, err
	}
	if a == nil || !a.State.Live() {
		return 0, refused("no_live_attempt", "%s has no live attempt to tell.", t.ID)
	}
	return mail(s, a, text, body, now), nil
}

func (Rules) ReadMail(s *contract.State, id string, now time.Time) ([]contract.Mail, error) {
	a, _, err := attempt(s, id)
	if err != nil {
		return nil, err
	}
	var unread []contract.Mail
	for i := range a.Mail {
		if m := &a.Mail[i]; m.ReadAt.IsZero() {
			m.ReadAt = now
			unread = append(unread, *m)
		}
	}
	return unread, nil
}

func (Rules) Pointed(s *contract.State, id string, seq int, now time.Time) error {
	a, _, err := attempt(s, id)
	if err != nil {
		return err
	}
	for i := range a.Mail {
		if a.Mail[i].Seq == seq {
			a.Mail[i].NudgedAt = now
			return nil
		}
	}
	return refuse(contract.ExitMissing, "no_such_message", "%s has no message %d.", a.ID, seq)
}
