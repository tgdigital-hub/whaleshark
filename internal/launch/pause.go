package launch

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// target is one run of one project of the login.
type target struct{ root, run string }

// targets lists the runs a pause or a resume is about: this one with the
// runs of its fleets, or with everywhere every run of every project the
// login has set up.
func targets(c *contract.Call, state string, everywhere bool) ([]target, error) {
	k := c.Kit
	if !everywhere {
		if c.Run == "" {
			return nil, nil
		}
		out := []target{{c.Root, c.Run}}
		if s, err := k.Store.Read(c.Root, c.Run); err == nil {
			for _, name := range slices.Sorted(maps.Keys(s.Run.Fleets)) {
				if f := s.Run.Fleets[name]; f.EndedAt.IsZero() {
					out = append(out, target{c.Root, f.Run})
				}
			}
		}
		return out, nil
	}
	roots, err := contract.ReadProjects(k.Platform.Peek, state)
	if err != nil {
		return nil, err
	}
	here := func(root string) bool { return k.Platform.PathKey(root) == k.Platform.PathKey(c.Root) }
	if !slices.ContainsFunc(roots, here) {
		roots = append(roots, c.Root)
	}
	var out []target
	for _, root := range roots {
		runs, err := k.Store.Runs(root)
		if err != nil {
			return nil, err
		}
		for _, run := range runs {
			out = append(out, target{root, run})
		}
	}
	return out, nil
}

// pause is Stop all. It writes the login's pause mark, which every gated
// agent's own harness reads before its next step, and then marks the runs
// paused. It reaches for no terminal: nothing is typed and nothing is ended.
func pause(c *contract.Call) (any, error) {
	k := c.Kit
	if len(c.Args) > 0 {
		return nil, usage(c, "pause takes no argument.")
	}
	dirs, err := k.Platform.Dirs()
	if err != nil {
		return nil, err
	}
	all, err := targets(c, dirs.State, c.Flags["everywhere"] != nil)
	if err != nil {
		return nil, err
	}
	if c.Run != "" {
		if _, err := read(c); err != nil {
			return nil, err
		}
	}
	by := c.Caller.Where
	if mark, err := contract.Paused(k.Platform.Peek, dirs.State); err != nil {
		return nil, err
	} else if mark == nil {
		if err := contract.SetPaused(k.Platform, dirs.State, &contract.Pause{At: c.Now, By: by}); err != nil {
			return nil, err
		}
	}
	held, running, missed := 0, []string{}, []string{}
	for _, t := range all {
		err := k.Store.Change(t.root, t.run, func(s *contract.State) error {
			// A closed run is passed over for what it is: its record, whose
			// panes may be anybody's by now, decides nothing about the caller.
			var err error
			if s.Run.ClosedAt.IsZero() {
				err = k.Rules.Allowed(s, c.Caller, c.Command.Name, "")
			}
			if err == nil {
				err = k.Rules.Pause(s, by, c.Now)
			}
			if err != nil {
				return err
			}
			seen := map[string]bool{} // a leader with two groups is one agent
			for _, a := range s.Attempts {
				p := a.Place.Pane
				switch {
				case !a.State.Live() || a.State == contract.AttemptStarting || seen[p]: // a start gives up by itself
				case a.Gated:
					held, seen[p] = held+1, p != ""
				default:
					seen[p] = p != ""
					running = append(running, s.Tasks[a.Task].Name)
				}
			}
			return nil
		})
		switch r := refusal(err); {
		case err == nil || r.Code == "closed":
		case t == target{c.Root, c.Run}:
			return nil, r
		default:
			missed = append(missed, t.run+" in "+t.root+": "+r.Message)
		}
	}
	slices.Sort(running)
	fmt.Fprintf(c.Out, "Paused. %d agents stop at their next step; nothing new starts.\n", held)
	for _, name := range running {
		fmt.Fprintf(c.Out, "still running: %s (no gate: it stops at its next whaleshark command)\n", name)
	}
	for _, m := range missed {
		fmt.Fprintf(c.Out, "not marked paused: %s\n", m)
	}
	return map[string]any{"held": held, "still_running": running, "not_marked": missed}, nil
}

// woken is one agent a resume has to tell.
type woken struct {
	attempt, name, pane string
	seq                 int
}

type skipped struct {
	Name string `json:"name"`
	Why  string `json:"why"`
}

const atPrompt = "at a prompt"

// resume ends the pause: the mark goes, each run's mark goes, every paused
// worker gets one message and the one-line pointer to it, a few a second,
// and the lead agent its own. An agent at a prompt is skipped and named:
// nothing is typed at a question only the person may answer.
func resume(c *contract.Call) (any, error) {
	k := c.Kit
	if len(c.Args) > 0 {
		return nil, usage(c, "resume takes no argument.")
	}
	dirs, err := k.Platform.Dirs()
	if err != nil {
		return nil, err
	}
	all, err := targets(c, dirs.State, true)
	if err != nil {
		return nil, err
	}
	everywhere := c.Flags["everywhere"] != nil
	here, _ := targets(c, dirs.State, false)
	var todo []target
	states := map[target]*contract.State{}
	others, over := false, false
	for _, t := range all {
		s, err := k.Store.Read(t.root, t.run)
		switch in := everywhere || slices.Contains(here, t); {
		case err != nil || s.Run.Paused == nil:
		case guarded(s):
			// The guard's hold is no mark's: it stays, whatever else is resumed.
			over = over || in
		case in:
			if err := k.Rules.Allowed(s, c.Caller, c.Command.Name, ""); err != nil {
				return nil, err
			}
			todo, states[t] = append(todo, t), s
		default:
			others = true
		}
	}
	mark, err := contract.Paused(k.Platform.Peek, dirs.State)
	if err != nil {
		return nil, err
	}
	switch {
	case len(todo) == 0 && mark == nil && over:
		job, _ := k.Store.Read(c.Root, c.Run)
		return nil, overBudget(c, job, "Nothing is resumed")
	case len(todo) == 0 && (mark == nil || others):
		return nil, refuse(contract.ExitRefused, "not_paused", "Nothing here is paused.")
	case others:
		// The one mark holds every agent of the login: with another run
		// still paused, these workers would be told to carry on and be refused.
		r := refuse(contract.ExitRefused, "paused_elsewhere", "Another run of yours is paused too, and one mark holds the agents of all of them.")
		r.Next = []string{line(c, "resume --everywhere")}
		return nil, r
	}
	if err := contract.SetPaused(k.Platform, dirs.State, nil); err != nil {
		return nil, err
	}
	w := waker{k: k, typed: map[string]bool{}}
	w.snap, w.err = k.Terms.Snapshot(context.Background())
	for _, t := range todo {
		wake, lead, err := wakeUp(k, t, states[t], c.Now, func(s *contract.State) (since time.Time, err error) {
			if err = k.Rules.Allowed(s, c.Caller, c.Command.Name, ""); err == nil && s.Run.Paused != nil {
				since = s.Run.Paused.At
				err = k.Rules.Resume(s, c.Now)
			}
			return since, err
		})
		if err == nil {
			err = w.tell(t, wake, lead)
		}
		if err != nil {
			return nil, refusal(err)
		}
	}
	told, skips := w.told, w.skips
	fmt.Fprintf(c.Out, "Resumed. %d agents were told to read their mail and carry on.\n", len(told))
	for _, s := range skips {
		if s.Why == atPrompt {
			fmt.Fprintf(c.Out, "at a prompt, not resumed: %s\n", s.Name)
		} else {
			fmt.Fprintf(c.Out, "not told: %s (%s)\n", s.Name, s.Why)
		}
	}
	return map[string]any{"told": told, "skipped": skips}, nil
}

// waker types the pointer to its mail into the tab of each agent that is
// woken, a few a second, over one run after another.
type waker struct {
	k     *contract.Kit
	snap  *contract.Snapshot
	err   error           // the terminals could not be asked
	typed map[string]bool // a leader's tab is the place of each of its groups and its fleet's lead: one line is enough
	next  time.Time
	told  []string
	skips []skipped
}

func (w *waker) tell(t target, wake []woken, lead string) error {
	k := w.k
	limits, _ := contract.ReadProjectFile(t.root)
	gap := time.Duration(0)
	if n := limits.Limits.ResumePerSecond; n > 0 {
		gap = time.Second / time.Duration(n)
	}
	var pointed []woken
	for _, to := range wake {
		err := w.err
		if p := paneOf(w.snap, to.pane); err == nil && p != nil && p.Status == contract.StatusBlocked {
			err = contract.ErrAgentBlocked
		}
		if err == nil && !w.typed[to.pane] {
			time.Sleep(time.Until(w.next))
			w.next = time.Now().Add(gap)
			err = k.Terms.Point(to.pane, contract.PointResumed, "")
			w.typed[to.pane] = err == nil
		}
		switch {
		case err == nil:
			w.told, pointed = append(w.told, to.name), append(pointed, to)
		case errors.Is(err, contract.ErrAgentBlocked):
			w.skips = append(w.skips, skipped{to.name, atPrompt})
		default:
			w.skips = append(w.skips, skipped{to.name, err.Error()})
		}
	}
	if lead != "" && w.err == nil && !w.typed[lead] {
		if err := k.Terms.Point(lead, contract.PointLeadOn, ""); err != nil {
			w.skips = append(w.skips, skipped{"the lead agent", err.Error()})
		}
	}
	if len(pointed) == 0 {
		return nil
	}
	return k.Store.Change(t.root, t.run, func(s *contract.State) error {
		for _, to := range pointed {
			k.Rules.Pointed(s, to.attempt, to.seq, contract.Now())
		}
		return nil
	})
}

// guarded reports whether the spending guard holds a run.
func guarded(s *contract.State) bool {
	return s != nil && s.Run.Paused != nil && s.Run.Paused.By == contract.HeldBy
}

// overBudget is what a start and a resume end with where the spending guard
// holds: more agent-hours are the person's alone to give, with the line that
// answers the job's item, and a resume without them would be held again at
// the sweep's next look.
func overBudget(c *contract.Call, job *contract.State, what string) *contract.Refusal {
	r := refuse(contract.ExitRefused, contract.RefusedOverBudget, "%s: the spending guard holds this run at its limit of agent-hours until the person answers its item.", what)
	r.Next = []string{"whaleshark wait"}
	for id, q := range cmp.Or(job, &contract.State{}).Questions {
		if c.Caller.Kind == contract.Human && q.Cause == contract.CauseBudget && q.State == contract.QuestionOpen {
			r.Next = []string{line(c, "answer "+id+` "`+contract.AnswerMore+`"`)}
		}
	}
	return r
}

// Hold brings a run's hold file and its agents in line with its record.
// Held by the spending guard, the file is written with the moment of the
// hold in it, and whoever writes it closes the tab of each agent with
// nothing in hand: one that has reported and waits to be checked, or whose
// task is done. Its work is committed and its copy of the code stays. An
// agent at work is left to its gate, and a leader sits on. Not held, the
// file goes, and whoever took it away wakes the run's agents as resume does,
// unless a pause of the person's holds them still.
func Hold(k *contract.Kit, root, run string) error {
	s, err := k.Store.Read(root, run)
	if err != nil {
		return err
	}
	file := contract.HoldPath(root, run)
	at, none := k.Platform.Peek(file)
	switch t := (target{root, run}); {
	case guarded(s) && none != nil:
		if err := os.WriteFile(file, []byte(s.Run.Paused.At.Format(time.RFC3339)), 0o600); err != nil {
			return err
		}
		snap, err := k.Terms.Snapshot(context.Background())
		for _, a := range s.Attempts {
			if task := s.Tasks[a.Task]; a.Leads == "" && (a.State == contract.AttemptReported || task.Status == contract.TaskDone) {
				shut(k, snap, tabName(task), a.Place)
			}
		}
		return err
	case !guarded(s) && none == nil && os.Remove(file) == nil && s.Run.Paused == nil:
		now := contract.Now()
		since, _ := time.Parse(time.RFC3339, string(at))
		w := waker{k: k, typed: map[string]bool{}}
		w.snap, w.err = k.Terms.Snapshot(context.Background())
		wake, lead, err := wakeUp(k, t, s, now, func(*contract.State) (time.Time, error) { return cmp.Or(since, now), nil })
		if err == nil {
			err = w.tell(t, wake, lead)
		}
		// Nobody reads what Hold prints: whom it could not wake is said to
		// the run's lead agent, as one more event.
		var not []string
		for _, s := range w.skips {
			not = append(not, s.Name+" ("+s.Why+")")
		}
		if len(not) > 0 {
			k.Store.Change(root, run, func(s *contract.State) error {
				k.Rules.Raise(s, contract.Event{Kind: "resumed", Text: "the hold has ended; not told to carry on: " + strings.Join(not, ", ")}, now)
				return nil
			})
		}
		return err
	}
	return nil
}

// wakeUp puts the message into the mail of every worker of one run that was
// held; begin, under the run's lock, ends what held them and says since
// when, or with no moment that nobody is to be woken. seen is the run as it
// was read a moment ago: from it comes which worker has an answer waiting
// that no ask of its own will fetch.
func wakeUp(k *contract.Kit, t target, seen *contract.State, now time.Time, begin func(*contract.State) (time.Time, error)) (wake []woken, lead string, err error) {
	answered := map[string]string{}
	for id, q := range seen.Questions {
		if q.Kind != contract.KindAsk || q.State != contract.QuestionAnswered {
			continue
		}
		lock := contract.AskLock(k.Store.Dir(t.root, t.run), id)
		// A lock that cannot even be tried is held by no ask either.
		unlock, free, err := k.Platform.TryLock(lock)
		if free {
			unlock()
		}
		if free || err != nil {
			answered[q.Attempt] = id
		}
	}
	err = k.Store.Change(t.root, t.run, func(s *contract.State) error {
		wake = nil
		since, err := begin(s)
		if err != nil || since.IsZero() {
			return err
		}
		ids := make([]string, 0, len(s.Attempts))
		for id, a := range s.Attempts {
			if a.State == contract.AttemptWorking || a.State == contract.AttemptAsked {
				ids = append(ids, id)
			}
		}
		slices.SortFunc(ids, byID)
		for _, id := range ids {
			a := s.Attempts[id]
			text := fmt.Sprintf("You were paused at %s and resumed at %s. Run `git status` first, then carry on from where you stopped.",
				since.Local().Format("15:04"), now.Local().Format("15:04"))
			if q := answered[id]; q != "" {
				text += " Your question " + q + " has its answer: run `whaleshark ask --resume " + q + "`."
			}
			seq, err := k.Rules.Tell(s, a.Task, text, "", now)
			if err != nil {
				return err
			}
			wake = append(wake, woken{id, s.Tasks[a.Task].Name, a.Place.Pane, seq})
		}
		if o := s.Run.Orchestrator; o != nil {
			lead = o.Pane
		}
		return nil
	})
	return wake, lead, err
}
