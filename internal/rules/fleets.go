package rules

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// place holds a new task to the mode of its run: in a run of fleets every
// task is a group placed in a fleet, and no other run has one. A fleet the
// run has is joined, in the spelling it has; a new one is on no open tab,
// for it names its leader's.
func place(s *contract.State, t *contract.Task, taken []string) error {
	words := strings.Fields(t.Fleet)
	switch fleets := s.Run.Mode == contract.ModeFleets; {
	case !fleets && t.Fleet == "":
		return nil
	case !fleets:
		return next(refused(contract.RefusedNotFleets, "This job has no fleets: Fleet Mode was off when it began. Switched on, it holds from the next job."), "whaleshark set fleets.mode on")
	case len(words) == 0 || len(words) > maxWords || utf8.RuneCountInString(strings.Join(words, " ")) > maxName || words[0][0] == '-':
		return next(refuse(contract.ExitUsage, "no_fleet", "In Fleet Mode every task is a group of work for a fleet, named in at most three words and %d characters, with no dash in front: %q is not.", maxName, t.Fleet),
			"whaleshark task add "+t.ID+` "<title>" --brief FILE --check CMD --fleet "<name>"`)
	}
	t.Fleet, t.Placement = strings.Join(words, " "), contract.PlaceFleet
	for _, other := range s.Tasks {
		if strings.EqualFold(other.Fleet, t.Fleet) {
			t.Fleet = other.Fleet
			return nil
		}
	}
	if slices.ContainsFunc(slices.Concat(taken, []string{lead}), func(n string) bool { return strings.EqualFold(n, t.Fleet) }) {
		return refused("name_taken", "The name %q is on an open tab. Give the fleet another.", t.Fleet)
	}
	return nil
}

// leader is a live attempt of up that leads the run, other than not.
func leader(up *contract.State, run string, not *contract.Attempt) *contract.Attempt {
	for _, a := range up.Attempts {
		if a != not && a.Leads == run && a.State.Live() {
			return a
		}
	}
	return nil
}

// left takes the leader's pane from its fleet's entry when the last attempt
// that led it ends, but not by its own report: that leader sits there still.
func left(s *contract.State, a *contract.Attempt) {
	f := s.Run.Fleets[s.Tasks[a.Task].Fleet]
	if f != nil && a.Leads != "" && a.State != contract.AttemptAccepted && a.State != contract.AttemptFailed && leader(s, a.Leads, nil) == nil {
		f.Pane, f.Tab = "", ""
	}
}

// group is the fleet that low is in up, with the group its leader is at:
// the first handed to it that is running, else the last.
func group(up, low *contract.State) (name string, t *contract.Task) {
	p := low.Run.Parent
	if p == nil || p.Run != up.Run.ID || up.Run.Fleets[p.Fleet] == nil || up.Run.Fleets[p.Fleet].Run != low.Run.ID {
		return "", nil
	}
	for _, id := range up.Run.Fleets[p.Fleet].Groups {
		if t = up.Tasks[id]; t.Status == contract.TaskRunning {
			break
		}
	}
	return p.Fleet, t
}

func notFleet(up, low *contract.State) error {
	return refused(contract.RefusedNotFleets, "Run %s is no fleet of run %s.", low.Run.ID, up.Run.ID)
}

// Room counts a leader that sits in its pane, with a group or without.
func (Rules) Room(up *contract.State, fleets map[string]*contract.State, fleet, worker string, limits contract.PersonConfig, cap int) error {
	l, leaders, total, server := limits.Fleets, 0, 0, ""
	for name, f := range up.Run.Fleets {
		if f.Pane != "" {
			leaders++
		}
		if low := fleets[name]; low != nil {
			total += live(low)
		}
	}
	if total += leaders; cap > 0 && (l.Total == contract.Unlimited || cap < l.Total) {
		l.Total, server = cap, " This server allows you "+strconv.Itoa(cap)+" agents, which is under your own limit."
	}
	full := func(n, limit int) bool { return limit != contract.Unlimited && n >= limit }
	low, f := fleets[fleet], up.Run.Fleets[fleet]
	var r *contract.Refusal
	switch {
	case worker == "" && f != nil && f.Pane != "":
		return nil
	case worker == "" && full(leaders, l.Leaders):
		r = refused(contract.LimitLeaders, "%d fleets are running, which is your limit (Settings › Fleet Mode). Give it to a fleet with room, or wait.", leaders)
	case worker != "" && low == nil:
		return refuse(contract.ExitMissing, "no_such_fleet", "Run %s has no fleet %q.", up.Run.ID, fleet)
	case worker != "" && full(live(low), l.Agents):
		r = refused(contract.LimitFleet, "%d agents are live in %s, which is your limit for one fleet (Settings › Fleet Mode): %s waits.", live(low), fleet, worker)
	case full(total, l.Total):
		r = refused(contract.LimitTotal, "%d agents are live in all, which is your limit (Settings › Fleet Mode).%s", total, server)
	default:
		return nil
	}
	return next(r, "whaleshark status")
}

// Lead comes after Placed for a new leader, whose tab the attempt has. A run
// that is another fleet's, or has tasks and was never a fleet's, is not led;
// a fleet that ended begins again with a run of its own.
func (r Rules) Lead(up, low *contract.State, id string, by contract.Binding, branch string, now time.Time) error {
	a, t, err := attempt(up, id)
	if err == nil {
		err = open(low)
	}
	if err != nil {
		return err
	}
	f, p := up.Run.Fleets[t.Fleet], low.Run.Parent
	// What the fleet's ended runs spent stays with it, and with that what
	// the person granted it beyond the setting.
	var was contract.Fleet
	if f != nil && !f.EndedAt.IsZero() {
		was, f = *f, nil
	}
	switch sits := f != nil && f.Pane != ""; {
	case t.Fleet == "" || low == up || !a.State.Live():
		return refused(contract.RefusedNotFleets, "%s is no live attempt of a group.", a.ID)
	case f != nil && f.Run != low.Run.ID, p != nil && (p.Run != up.Run.ID || p.Fleet != t.Fleet), p == nil && len(low.Tasks) > 0:
		return refused("other_fleet", "Run %s is not the run of fleet %q.", low.Run.ID, t.Fleet)
	case sits == (by.Pane != ""):
		return refused("leader", "%s goes to the leader of %q where one sits, and to a new one only where none does.", t.ID, t.Fleet)
	}
	if f == nil {
		if up.Run.Fleets == nil {
			up.Run.Fleets = map[string]*contract.Fleet{}
		}
		f = &contract.Fleet{Run: low.Run.ID, Since: now, Used: was.Used, Spend: contract.Spend{More: was.Spend.More, Off: was.Spend.Off}}
		up.Run.Fleets[t.Fleet] = f
	}
	if by.Pane != "" {
		r.Takeover(low, by, now)
		f.Pane, f.Tab, f.Tool, f.Model = by.Pane, a.Place.Tab, a.Agent.Kind, a.Agent.Model
	} else if other := leader(up, low.Run.ID, a); other != nil {
		a.Place, a.Agent = other.Place, other.Agent
	} else {
		a.Place.Pane, a.Place.Tab, a.Agent.Kind, a.Agent.Model = f.Pane, f.Tab, f.Tool, f.Model
	}
	if branch != "" {
		f.Branch = branch
	}
	if !slices.Contains(f.Groups, t.ID) {
		f.Groups = append(f.Groups, t.ID)
	}
	a.Leads, low.Run.Parent = low.Run.ID, &contract.Twin{Run: up.Run.ID, Fleet: t.Fleet}
	return nil
}

// Park refuses as holds_more while the leader has another open group: that
// group alone is stopped then, with Stop, and the fleet goes on.
func (r Rules) Park(up, low *contract.State, group string, now time.Time) ([]string, error) {
	t, a, err := r.taskAttempt(up, group)
	if err != nil {
		return nil, err
	}
	checking := func(w *contract.Attempt) bool { return w.State == contract.AttemptChecking }
	switch {
	case a == nil || !a.State.Live() || a.Leads != low.Run.ID:
		return nil, refused("no_live_attempt", "%s is with no leader of run %s.", t.ID, low.Run.ID)
	case leader(up, a.Leads, a) != nil:
		return nil, refused("holds_more", "The leader of %q holds another open group, so the fleet is not parked.", t.Fleet)
	case checking(a) || slices.ContainsFunc(slices.Collect(maps.Values(low.Attempts)), checking):
		return nil, refused("checking", "Work of %q is being accepted. Wait for it.", t.Fleet)
	}
	tabs := []string{a.Place.Tab}
	for _, w := range low.Attempts {
		if w.State.Live() {
			end(low, w, contract.AttemptStopped, contract.ExitClosedByUs, now)
			back(low.Tasks[w.Task], false, now)
			tabs = append(tabs, w.Place.Tab)
		}
	}
	end(up, a, contract.AttemptStopped, contract.ExitClosedByUs, now)
	back(t, false, now)
	slices.Sort(tabs)
	return tabs, nil
}

// Ended has nothing to land: a fleet's work lands with the orchestrator's.
func (r Rules) Ended(up, low *contract.State, abandon bool, now time.Time) error {
	name, _ := group(up, low)
	if name == "" {
		return notFleet(up, low)
	}
	for _, t := range up.Tasks {
		if t.Fleet == name && t.Status != contract.TaskDone && t.Status != contract.TaskCancelled {
			return next(refused("open_group", "%s of fleet %q is %s; a fleet ends with its last group settled.", t.ID, name, t.Status), "whaleshark show "+t.ID)
		}
	}
	if err := r.CloseRun(low, abandon, now); err != nil {
		return err
	}
	for _, q := range questions(up) {
		if q.Down != nil && q.Down.Run == low.Run.ID && q.State == contract.QuestionOpen {
			q.State = contract.QuestionClosed // nobody is left below to hear the answer
		}
	}
	f := up.Run.Fleets[name]
	f.EndedAt, f.Pane, f.Tab, f.Used = now, "", "", f.Used+contract.Spent(now, low)
	return nil
}

// Twin run again finds the twin that is there and not closed, which mends
// a pair of which a kill wrote the upper half only. The twin is the
// leader's question to the orchestrator and ends with the leader's attempt.
func (Rules) Twin(up, low *contract.State, id string, now time.Time) (string, error) {
	q, err := question(low, id)
	if err == nil {
		err = open(up)
	}
	name, t := group(up, low)
	switch {
	case err != nil:
		return "", err
	case t == nil:
		return "", notFleet(up, low)
	case q.State != contract.QuestionOpen || q.From == contract.FromTool:
		return "", refuse(contract.ExitMissing, "not_open", "%s is %s, and the tool's own items are the person's; there is nothing to hand to the orchestrator.", q.ID, q.State)
	}
	var twin *contract.Question
	for _, x := range questions(up) {
		if x.Down != nil && x.Down.Run == low.Run.ID && x.Down.ID == q.ID && x.State != contract.QuestionClosed {
			twin = x
		}
	}
	if twin == nil {
		a := current(up, t)
		up.Counters.Question++
		twin = &contract.Question{
			ID: "q" + strconv.Itoa(up.Counters.Question), Kind: contract.KindAsk, Form: q.Form, For: contract.ForOrchestrator,
			From: a.ID, Task: t.ID, Attempt: a.ID, Holds: q.Holds, Urgent: q.Urgent, Text: q.Text, Options: q.Options,
			State: contract.QuestionOpen, CreatedAt: now, Down: &contract.Twin{Run: low.Run.ID, ID: q.ID},
		}
		up.Questions[twin.ID] = twin
		raise(up, "question", t, a, q.Text, map[string]any{"id": twin.ID, "fleet": name, "task": q.Task}, now)
	}
	q.For, q.Up = contract.ForOrchestrator, &contract.Twin{Run: up.Run.ID, ID: twin.ID}
	return twin.ID, nil
}

// Down copies once, and only what has settled; a twin that was used while
// its other half has no answer is the pair a kill left apart.
func (r Rules) Down(up, low *contract.State, twin string, now time.Time) (bool, error) {
	q, err := question(up, twin)
	switch {
	case err != nil:
		return false, err
	case q.Down == nil || q.Down.Run != low.Run.ID || low.Questions[q.Down.ID] == nil:
		return false, refused(contract.RefusedNotFleets, "%s is no twin of a question in run %s.", q.ID, low.Run.ID)
	case q.State != contract.QuestionUsed && !settled(up, q, now):
		return false, nil
	}
	other := low.Questions[q.Down.ID]
	done := other.Answer == "" && r.Answer(low, other.ID, q.Answer, *q.AnsweredBy, 0, now) == nil
	if q.State != contract.QuestionUsed {
		use(up, q, now)
	}
	return done, nil
}
