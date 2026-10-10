package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/pick"
)

const (
	openCmd = "whaleshark open"
	restart = "whaleshark engine stop, then whaleshark open"
)

// socket is where the keeper listens: where the variable says, else in the
// state folder. The contract's own function for this may be named in the
// four folders that open the socket, and this is none of them.
func (e *exam) socket() string {
	if p := os.Getenv(contract.EnvSocket); p != "" {
		return p
	}
	return filepath.Join(e.dirs.State, "engine.sock")
}

// engine says whether the keeper runs and is this program, whether its
// socket is the login's alone, and what of the tabs belongs to no attempt
// or lost its agent at a restart.
func (e *exam) engine() {
	sock := e.socket()
	// A socket moved elsewhere lies in a folder that is not ours to change:
	// it is held to the keeper's own rule and never mended.
	if dir := filepath.Dir(sock); dir != e.dirs.State {
		if others, _ := e.k.Platform.WritableByOthers(dir); others {
			e.say(problem, "", "the folder of the engine's socket, %s, can be written by another login: the engine does not start there", dir)
		}
	}
	e.private("the engine's socket", sock)
	if _, err := os.Stat(filepath.Join(e.dirs.State, contract.LayoutBad)); err == nil {
		e.say(note, "", "the engine could not use its layout file at a start and kept it as layout.json.bad in %s: the tabs it held did not come back", e.dirs.State)
	}
	version, err := e.k.Terms.Version()
	switch {
	case errors.Is(err, contract.ErrWireVersion), err == nil && version != contract.Version:
		e.say(problem, restart, "restart needed: the engine that is running is not this version (%s)", contract.Version)
		return
	case err != nil:
		e.say(note, openCmd, "the engine is not running")
		return
	}
	e.say(ok, "", "the engine is running, version %s", version)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	snap, err := e.k.Terms.Snapshot(ctx)
	if err != nil {
		e.say(problem, restart, "the engine does not show its panes: %v", err)
		return
	}
	panes, owned := map[string]contract.Pane{}, map[string]bool{}
	for _, p := range snap.Panes {
		panes[p.ID] = p
	}
	mine := e.k.Platform.PathKey(e.c.Root)
	for root, runs := range e.runs {
		for _, s := range runs {
			for _, a := range s.Attempts {
				owned[a.Agent.Name] = owned[a.Agent.Name] || a.State.Live()
				p, there := panes[a.Place.Pane]
				if root == mine && there && p.Agent == "" && a.State.Live() && a.State != contract.AttemptStarting {
					e.say(problem, "whaleshark start "+a.Task+" --retry --resume", "the agent of %s did not come back in its pane %s", a.ID, p.ID)
				}
			}
		}
	}
	for _, p := range snap.Panes {
		if p.Name != "" && !owned[p.Name] {
			e.say(note, "", "the tab %q (pane %s) holds an agent we started that no attempt owns: close the tab when you no longer need it", p.Label, p.ID)
		}
	}
}

// nudge says whether the pop-up is switched on, and with --notify sends one
// test nudge the way a real one goes: the pop-up, then the next channel.
func (e *exam) nudge() {
	p := e.k.Platform
	cfg, err := contract.ReadPerson(p.Peek, e.dirs.Config)
	if err != nil {
		e.say(problem, "", "your %s cannot be read: %v", contract.ConfigFile, err)
		return
	}
	var ui contract.UIFile
	contract.ReadVersioned(p.Peek, filepath.Join(e.dirs.State, contract.UIFileName), contract.FileVersion, &ui)
	sound := cfg.Nudge.Sound && !ui.Mute
	switch {
	case !cfg.Nudge.Popup:
		e.say(note, "whaleshark set nudge.popup on", "the pop-up is switched off")
	case ui.DND && (ui.DNDUntil.IsZero() || e.c.Now.Before(ui.DNDUntil)):
		e.say(note, "whaleshark set dnd off", "the pop-up is switched on, and Do not disturb holds every nudge back")
	case sound:
		e.say(ok, "", "the pop-up and its sound are switched on")
	default:
		e.say(ok, "", "the pop-up is switched on, without its sound")
	}
	hooks := filepath.Join(e.dirs.Config, "hooks", "nudge.d")
	for _, dir := range []string{e.dirs.Config, filepath.Dir(hooks), hooks} {
		if others, _ := p.WritableByOthers(dir); others {
			e.say(note, "", "the nudge hooks in %s are not run: another login can write to %s", hooks, dir)
		}
	}
	if cfg.Notify.URL != "" && !cfg.Nudge.Phone {
		e.say(note, "whaleshark set nudge.phone on", "an address for your phone is set and nudge.phone is off: nothing is sent there")
	} else if cfg.Notify.URL == "" && cfg.Nudge.Phone {
		e.say(note, "", "nudge.phone is on and no address is set under [notify] in your %s: only a phone paired with the page is nudged", contract.ConfigFile)
	}
	if e.c.Flags["notify"] == nil {
		return
	}
	title, body := "WhaleShark", "This is the test nudge of doctor --notify."
	if cfg.Nudge.Popup {
		reason, delivery, err := e.k.Terms.Notify(title, body, sound)
		switch {
		case err != nil:
			e.say(problem, openCmd, "the test nudge could not be shown: %v", err)
		case reason == contract.NotifyShown && delivery != contract.NotifyOff:
			e.say(ok, "", "the test nudge was shown in your window: you should have seen it")
			return
		case reason == contract.NotifyNoWindow:
			e.say(note, openCmd, "no window is open to show the test nudge in")
		default:
			e.say(note, "", "the engine did not show the test nudge: %s", reason)
		}
	}
	if err := e.k.Notifier.Nudge(contract.Nudge{Root: e.c.Root, Run: e.c.Run, Title: title, Body: body, Urgent: true}); err != nil {
		e.say(problem, "", "the test nudge could not be sent on: %v", err)
	} else {
		e.say(note, "", "the test nudge was handed to your other channels")
	}
}

// paste tries the person's clipboard by the route a copy in a pane takes
// from where they sit, and asks them to paste: nothing else can tell whether
// an outer terminal obeyed.
func (e *exam) paste() {
	c := e.c
	if c.Caller.Kind != contract.Human || c.Caller.Where != contract.WhereTyped {
		e.say(note, "", "the clipboard is tried only when you run doctor yourself at a terminal")
		return
	}
	b := pick.Board{System: os.Getenv(contract.EnvSystem)}
	if b.Connect = b.System != ""; !b.Connect {
		b.System, b.Remote = e.k.Platform.System(), os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != ""
	}
	word := fmt.Sprintf("whaleshark-%04d", c.Now.Nanosecond()/1000%10000)
	mark, seq := b.Copy(word)
	c.Err.Write(seq)
	if mark == pick.Sent {
		e.say(note, "", "%s: the word %s. Paste it somewhere: if another text comes, your terminal does not let a program write the clipboard; switch that on in its settings", mark, word)
	} else {
		e.say(note, "", "%s: the word %s. Paste it somewhere to see that it arrived", mark, word)
	}
}
