// Package notify reaches the person when they are not looking: the pop-up
// and its sound through the terminals, held back by Do not disturb, silenced
// by Mute, and never taken as seen on herdr's word alone.
package notify

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const (
	// perTask is the least time between two nudges about one task.
	perTask = 30 * time.Second
	// snapshotWait is how long the pass waits for the terminals' picture.
	snapshotWait = 3 * time.Second
)

// Nudger is the kit's Notifier. Its Items is the pass a watcher runs after
// each sweep; a package that cannot import this one reaches it through the
// kit as interface{ Items(root, run string, now time.Time) error }.
type Nudger struct {
	k *contract.Kit
	// next are the channels after the pop-up, tried in order until one says
	// it reached the person. None is built yet: the phone's is the first.
	next []func(title, body string, sound bool) bool
}

// Plug puts the nudger into the kit. The package binds no command.
func Plug(k *contract.Kit) { k.Notifier = Nudger{k: k} }

// sentFile is nudged.json in the login's state folder: when a task was last
// nudged about, kept for as long as the limit lasts.
type sentFile struct {
	contract.Versioned
	At map[string]time.Time `json:"at"`
}

// settings reads the person's switches and what ui.json says of Do not
// disturb and Mute. An end time that has passed counts as off.
func (n Nudger) settings() (cfg contract.PersonConfig, ui contract.UIFile, file string, err error) {
	cfg = contract.PersonDefaults()
	dirs, err := n.k.Platform.Dirs()
	if err != nil {
		return cfg, ui, "", err
	}
	data, err := n.k.Platform.Peek(filepath.Join(dirs.Config, "config.toml"))
	if err == nil {
		err = toml.Unmarshal(data, &cfg)
	} else if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	if err != nil {
		return cfg, ui, "", err
	}
	file = filepath.Join(dirs.State, "ui.json")
	// Read, not Peek: a file missed at the moment it is replaced would read
	// as Do not disturb switched off.
	err = contract.ReadVersioned(n.k.Platform.Read, file, contract.FileVersion, &ui)
	return cfg, ui, file, err
}

// over reports whether Do not disturb has an end time and it has passed.
func over(ui contract.UIFile, now time.Time) bool {
	return ui.DND && !ui.DNDUntil.IsZero() && !now.Before(ui.DNDUntil)
}

// send goes through the channels until one has reached the person. The
// pop-up counts only when herdr's own setting has it switched on: with it
// off herdr still answers that it was shown.
func (n Nudger) send(cfg contract.PersonConfig, ui contract.UIFile, title, body string) {
	sound := cfg.Nudge.Sound && !ui.Mute
	if cfg.Nudge.Popup {
		reason, delivery, err := n.k.Terms.Notify(title, body, sound)
		if err == nil && delivery != contract.NotifyOff && reason == contract.NotifyShown {
			return
		}
	}
	for _, channel := range n.next {
		if channel(title, body, sound) {
			return
		}
	}
}

// Nudge is one call about something that is no item: a task that failed for
// good, the whole job finished, the terminals out of reach. Under Do not
// disturb only an urgent one is sent, and a task is nudged about at most
// once in 30 seconds.
func (n Nudger) Nudge(c contract.Nudge) error {
	now := contract.Now()
	cfg, ui, file, err := n.settings()
	if err != nil || ui.DND && !over(ui, now) && !c.Urgent {
		return err
	}
	if c.Task != "" {
		file = filepath.Join(filepath.Dir(file), "nudged.json")
		sent := sentFile{}
		if err := contract.ReadVersioned(n.k.Platform.Read, file, contract.FileVersion, &sent); err != nil {
			return err
		}
		if now.Sub(sent.At[c.Task]) < perTask {
			return nil
		}
		fresh := sentFile{contract.Versioned{Version: contract.FileVersion}, map[string]time.Time{c.Task: now}}
		for task, at := range sent.At {
			if task != c.Task && now.Sub(at) < perTask {
				fresh.At[task] = at
			}
		}
		if err := contract.WriteVersioned(n.k.Platform, file, contract.FileVersion, fresh); err != nil {
			return err
		}
	}
	n.send(cfg, ui, c.Title, c.Body)
	return nil
}

// Items puts every item of a run that waits for the person in front of
// them: it stamps each as shown, which only this does, and then nudges, so
// an item is in the pane before its nudge. Do not disturb holds everything
// but the urgent back, unstamped; when its end time has passed it is
// switched off here and one nudge announces all that was held.
func (n Nudger) Items(root, run string, now time.Time) error {
	cfg, ui, file, err := n.settings()
	if err != nil {
		return err
	}
	ended := over(ui, now)
	if ended {
		ui.Version, ui.DND, ui.DNDUntil = contract.FileVersion, false, time.Time{}
		if err := contract.WriteVersioned(n.k.Platform, file, contract.FileVersion, ui); err != nil {
			return err
		}
	}
	s, err := n.k.Store.Read(root, run)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapshotWait)
	snap, _ := n.k.Terms.Snapshot(ctx)
	cancel()
	// The view model says what waits, what holds work up and what Do not
	// disturb keeps back; the nudge is one more printer of it.
	v := n.k.View(contract.ViewInput{Now: now, Caller: contract.Human, State: s,
		Limits: contract.ProjectDefaults(), Terms: snap, UI: ui, Checked: now})
	var due, loud []contract.Item
	for _, it := range v.Items {
		q := s.Questions[it.ID]
		if q == nil || !q.ShownAt.IsZero() {
			continue
		}
		due = append(due, it)
		if nudged(it, q) && !looking(s, snap, it) {
			loud = append(loud, it)
		}
	}
	if len(due) == 0 {
		return nil
	}
	err = n.k.Store.Change(root, run, func(s *contract.State) error {
		for _, it := range due {
			if err := n.k.Rules.Shown(s, it.ID, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	switch {
	case ended:
		n.send(cfg, ui, things(len(due))+" waited while you were not to be disturbed", names(due))
	case len(loud) == 1:
		look := contract.LookNeedsYou
		if loud[0].Look == contract.LookAtPrompt {
			look = contract.LookAtPrompt
		}
		n.send(cfg, ui, names(loud)+" — "+string(look), loud[0].Text)
	case len(loud) > 1:
		n.send(cfg, ui, things(len(loud))+" wait for you", names(loud))
	}
	return nil
}

// nudged is rule 1 for an item: it holds work up, it is urgent, or the tool
// raised it for an agent at a prompt, a lead agent that is not listening, or
// most workers stopping together.
func nudged(it contract.Item, q *contract.Question) bool {
	switch q.Cause {
	case contract.CausePrompt, contract.CauseLeadSilent, contract.CauseTogether:
		return true
	}
	return it.Holds || it.Urgent
}

// looking reports whether the tab the item is about is the one in front.
func looking(s *contract.State, snap *contract.Snapshot, it contract.Item) bool {
	pane := ""
	if t := s.Tasks[it.Task]; it.Task == contract.Lead && s.Run.Orchestrator != nil {
		pane = s.Run.Orchestrator.Pane
	} else if t != nil && len(t.Attempts) > 0 {
		pane = s.Attempts[t.Attempts[len(t.Attempts)-1]].Place.Pane
	}
	if snap == nil || pane == "" {
		return false
	}
	var tab, front string
	for _, p := range snap.Panes {
		if p.ID == pane {
			tab = p.Tab
		}
		if p.Focused {
			front = p.Tab
		}
	}
	return tab != "" && tab == front
}

// things is the count a nudge about several items begins with.
func things(n int) string {
	if n == 1 {
		return "1 thing"
	}
	return fmt.Sprintf("%d things", n)
}

// names lists what the items are about, each once.
func names(items []contract.Item) string {
	var out []string
	for _, it := range items {
		name := it.Name
		if name == "" {
			name = "whaleshark"
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return strings.Join(out, ", ")
}
