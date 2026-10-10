package launch

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// target is one run of one project of the login.
type target struct{ root, run string }

// targets lists the runs a pause or a resume is about: this one, or with
// everywhere every run of every project the login has set up.
func targets(c *contract.Call, state string, everywhere bool) ([]target, error) {
	k := c.Kit
	if !everywhere {
		if c.Run == "" {
			return nil, nil
		}
		return []target{{c.Root, c.Run}}, nil
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
	if mark, err := contract.Paused(dirs.State); err != nil {
		return nil, err
	} else if mark == nil {
		if err := contract.SetPaused(dirs.State, &contract.Pause{At: c.Now, By: by}); err != nil {
			return nil, err
		}
	}
	held, running, missed := 0, []string{}, []string{}
	for _, t := range all {
		err := k.Store.Change(t.root, t.run, func(s *contract.State) error {
			err := k.Rules.Allowed(s, c.Caller, c.Command.Name, "")
			if err == nil {
				err = k.Rules.Pause(s, by, c.Now)
			}
			if err != nil {
				return err
			}
			for _, a := range s.Attempts {
				switch {
				case !a.State.Live() || a.State == contract.AttemptStarting: // a start gives up by itself
				case a.Gated:
					held++
				default:
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
	var todo []target
	states := map[target]*contract.State{}
	others := false
	for _, t := range all {
		s, err := k.Store.Read(t.root, t.run)
		switch {
		case err != nil || s.Run.Paused == nil:
		case everywhere || t == target{c.Root, c.Run}:
			if err := k.Rules.Allowed(s, c.Caller, c.Command.Name, ""); err != nil {
				return nil, err
			}
			todo, states[t] = append(todo, t), s
		default:
			others = true
		}
	}
	mark, err := contract.Paused(dirs.State)
	if err != nil {
		return nil, err
	}
	switch {
	case len(todo) == 0 && (mark == nil || others):
		return nil, refuse(contract.ExitRefused, "not_paused", "Nothing here is paused.")
	case others:
		// The one mark holds every agent of the login: with another run
		// still paused, these workers would be told to carry on and be refused.
		r := refuse(contract.ExitRefused, "paused_elsewhere", "Another run of yours is paused too, and one mark holds the agents of all of them.")
		r.Next = []string{line(c, "resume --everywhere")}
		return nil, r
	}
	if err := contract.SetPaused(dirs.State, nil); err != nil {
		return nil, err
	}
	snap, snapErr := k.Terms.Snapshot(context.Background())
	told, skips := []string{}, []skipped{}
	var next time.Time
	for _, t := range todo {
		limits, _ := contract.ReadProjectFile(t.root)
		gap := time.Duration(0)
		if n := limits.Limits.ResumePerSecond; n > 0 {
			gap = time.Second / time.Duration(n)
		}
		wake, lead, err := wakeUp(c, t, states[t])
		if err != nil {
			return nil, refusal(err)
		}
		var pointed []woken
		for _, w := range wake {
			err := snapErr
			if p := paneOf(snap, w.pane); err == nil && p != nil && p.Status == contract.StatusBlocked {
				err = contract.ErrAgentBlocked
			}
			if err == nil {
				time.Sleep(time.Until(next))
				next = time.Now().Add(gap)
				err = k.Terms.Point(w.pane, contract.PointResumed, "")
			}
			switch {
			case err == nil:
				told, pointed = append(told, w.name), append(pointed, w)
			case errors.Is(err, contract.ErrAgentBlocked):
				skips = append(skips, skipped{w.name, atPrompt})
			default:
				skips = append(skips, skipped{w.name, err.Error()})
			}
		}
		if lead != "" && snapErr == nil {
			if err := k.Terms.Point(lead, contract.PointLeadOn, ""); err != nil {
				skips = append(skips, skipped{"the lead agent", err.Error()})
			}
		}
		if len(pointed) > 0 {
			err = k.Store.Change(t.root, t.run, func(s *contract.State) error {
				for _, w := range pointed {
					k.Rules.Pointed(s, w.attempt, w.seq, contract.Now())
				}
				return nil
			})
			if err != nil {
				return nil, refusal(err)
			}
		}
	}
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

// wakeUp clears one run's pause and puts the message into the mail of every
// worker that was held. seen is the run as it was read a moment ago: from it
// comes which worker has an answer waiting that no ask of its own will fetch.
func wakeUp(c *contract.Call, t target, seen *contract.State) (wake []woken, lead string, err error) {
	k := c.Kit
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
		err := k.Rules.Allowed(s, c.Caller, c.Command.Name, "")
		if err != nil || s.Run.Paused == nil {
			return err
		}
		since := s.Run.Paused.At
		if err := k.Rules.Resume(s, c.Now); err != nil {
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
				since.Local().Format("15:04"), c.Now.Local().Format("15:04"))
			if q := answered[id]; q != "" {
				text += " Your question " + q + " has its answer: run `whaleshark ask --resume " + q + "`."
			}
			seq, err := k.Rules.Tell(s, a.Task, text, "", c.Now)
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
