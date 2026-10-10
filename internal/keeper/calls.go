package keeper

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/overlay"
)

// onPane are the calls that are about one pane, which must be there.
var onPane = []string{contract.OpRun, contract.OpScreen, contract.OpSplit, contract.OpSwap, contract.OpResize,
	contract.OpPaneClose, contract.OpSize, contract.OpPaneFocus, contract.OpAgentStart, contract.OpPoint, contract.OpHook}

// fenced are the calls that are not for an agent the tool started to make
// from its own pane: they type what the caller likes, start a program, move
// the person's keys or close something. A fence against mistakes, no wall:
// whoever runs as the login can leave the pane's name out.
var fenced = []string{contract.OpPrompt, contract.OpRun, contract.OpAgentStart, contract.OpTabFocus, contract.OpPaneFocus,
	contract.OpPaneClose, contract.OpTabClose, contract.OpSetKeys}

// call answers one call. The terminals it ended are closed after the lock
// is given back and before the answer goes out; and an agent is waited for,
// or typed at, by its Watcher with no lock held.
func (k *Keeper) call(c contract.WireCall) (contract.WireReply, error) {
	k.mu.Lock()
	if q := k.panes[c.From]; q != nil && q.rec.Name != "" && slices.Contains(fenced, c.Op) {
		k.mu.Unlock()
		return contract.WireReply{}, fmt.Errorf("%s is not a call for an agent to make from its pane", c.Op)
	}
	r, gone, err := k.do(c)
	p := k.panes[c.Pane]
	if c.Op == contract.OpPrompt {
		p = k.named(c.Name)
	}
	k.mu.Unlock()
	end(gone)
	if wait := time.Duration(c.Millis) * time.Millisecond; err != nil || p == nil {
	} else if c.Op == contract.OpAgentStart {
		err = p.watch.Ready(wait)
	} else if c.Op == contract.OpPrompt {
		err = p.watch.Prompt(c.Text, wait)
	} else if c.Op == contract.OpPoint {
		err = p.watch.Point(c.Pointer, c.Text)
	}
	return r, err
}

// named is the pane of the agent the tool gave a name.
func (k *Keeper) named(name string) *pane {
	for _, p := range k.panes {
		if p.rec.Name == name && name != "" {
			return p
		}
	}
	return nil
}

func (k *Keeper) do(c contract.WireCall) (r contract.WireReply, gone []contract.Pty, err error) {
	select {
	case <-k.done:
		return r, nil, contract.ErrEngineUnreachable
	default:
	}
	p, tab := k.panes[c.Pane], k.lay.Tab(c.Tab)
	if p == nil && slices.Contains(onPane, c.Op) {
		return r, nil, contract.ErrNoPane
	}
	switch c.Op {
	case contract.OpVersion:
		r.Text = k.version
	case contract.OpSnapshot:
		r.Snapshot = k.snapshot()
	case contract.OpStatus:
		r.Snapshot = k.snapshot()
		r.Text = fmt.Sprintf("pid %d, %d tabs, %d panes, %d windows", os.Getpid(), len(k.lay.Tabs), len(k.panes), len(k.wins))
		if k.lost != nil {
			r.Text += "; the layout file: " + k.lost.Error()
		}
		if k.saver == nil {
			r.Text += "; nothing is saved"
		} else if err := k.saver.Err(); err != nil {
			r.Text += "; the layout was not saved: " + err.Error()
		}
	case contract.OpTabCreate:
		k.nTab++
		id := "t" + strconv.Itoa(k.nTab)
		p = k.fresh(id, c.Label, c.Cwd)
		k.tabs[id] = c.Env
		k.lay.Add(id, c.Label, p.rec.ID)
		if err = k.start(p); err != nil {
			k.lay.Remove(id)
			delete(k.tabs, id)
			k.nTab--
			k.forget(p)
			break
		}
		k.emit(contract.EvOpened, p.rec)
		k.changed()
		rec := p.rec
		r.Pane = &rec
	case contract.OpSplit:
		n := k.fresh(p.rec.Tab, p.rec.Label, p.rec.Cwd)
		if err = k.lay.Split(c.Pane, c.Dir, c.Ratio, n.rec.ID); err == nil {
			if err = k.start(n); err != nil {
				k.lay.Close(n.rec.ID)
			}
		}
		if err != nil {
			k.forget(n)
			break
		}
		k.emit(contract.EvOpened, n.rec)
		k.changed()
		rec := n.rec
		r.Pane = &rec
	case contract.OpRun, contract.OpAgentStart:
		// An agent is its kind's program with the arguments given, and the
		// name the tool knows it by; whatever else is run there is none.
		old, was, rec := p.tty, p.argv, p.rec
		p.argv, p.rec.Agent, p.rec.Name, p.rec.Session = c.Argv, "", "", ""
		if c.Op == contract.OpAgentStart {
			p.argv, p.rec.Agent, p.rec.Name = append([]string{c.Kind}, c.Argv...), c.Kind, c.Name
		}
		if q := k.named(c.Name); q != nil && q != p {
			// A prompt finds its agent by name: two of one name would share it.
			err = fmt.Errorf("an agent named %q is in pane %s already", c.Name, q.rec.ID)
		} else if len(p.argv) == 0 || p.argv[0] == "" {
			err = errors.New("no program to run")
		} else {
			err = k.start(p)
		}
		if err != nil {
			p.argv, p.rec = was, rec
			break
		}
		if gone = append(gone, old); c.Op == contract.OpAgentStart {
			// The agent is what its pane was opened for, not a new start of
			// it: the terminal keeps the id its caller wrote down.
			p.rec.Terminal = rec.Terminal
		}
		k.emit(contract.EvState, p.rec)
		k.saved()
		k.wake()
	case contract.OpHook:
		p.hooked = true
		p.watch.Hook(c.Kind, c.Session, c.Cwd)
	case contract.OpPrompt:
		if k.named(c.Name) == nil {
			err = contract.ErrNoPane
		}
	case contract.OpPoint:
	case contract.OpOverlay:
		if len(c.Argv) == 0 || k.over != nil {
			err = errors.New("no program to draw over the tab, or one is there already")
			break
		}
		// It runs as in the pane it was asked from, or the one with the keys.
		from := cmp.Or(k.panes[c.From], k.panes[k.focus], &pane{})
		p = k.fresh(from.rec.Tab, from.rec.Label, from.rec.Cwd)
		p.argv, k.over, k.overW, k.overH = c.Argv, p, cmp.Or(c.W, 0.8), cmp.Or(c.H, 0.8)
		if err = k.start(p); err != nil {
			k.over = nil
			k.forget(p)
			break
		}
		k.emit(contract.EvOpened, p.rec)
		k.changed()
	case contract.OpScreen:
		r.Text = p.text()
	case contract.OpSize:
		p.mu.Lock()
		r.W, r.H = p.w, p.h
		p.mu.Unlock()
	case contract.OpSwap:
		if err = k.lay.Swap(c.Pane, c.Target); err == nil {
			k.changed()
		}
	case contract.OpResize:
		if err = k.lay.Resize(c.Pane, c.Dir, c.Ratio); err == nil {
			k.changed()
		}
	case contract.OpPaneClose:
		gone = append(gone, p.tty)
		k.drop(p)
		k.changed()
	case contract.OpPaneFocus:
		to := c.Pane
		if c.Dir != "" {
			to = k.lay.Beside(c.Pane, c.Dir)
		}
		if to != "" {
			k.lay.Focus(to)
			k.lay.Show(k.lay.Of(to).ID)
			k.changed()
		}
		r.Text = k.focus
	case contract.OpTabRename, contract.OpTabFocus, contract.OpTabClose:
		if tab == nil {
			return r, nil, contract.ErrNoPane
		}
		switch c.Op {
		case contract.OpTabRename:
			tab.Label = c.Label
			for _, id := range tab.Panes() {
				k.panes[id].rec.Label = c.Label
			}
			k.emit(contract.EvRenamed, contract.Pane{Tab: c.Tab, Label: c.Label})
			k.saved()
			k.wake()
		case contract.OpTabFocus:
			k.lay.Show(c.Tab)
			k.changed()
		case contract.OpTabClose:
			for _, id := range tab.Panes() {
				gone = append(gone, k.panes[id].tty)
				k.panes[id].quit()
				delete(k.panes, id)
			}
			k.lay.Remove(c.Tab)
			delete(k.tabs, c.Tab)
			k.emit(contract.EvTabClosed, contract.Pane{Tab: c.Tab})
			k.changed()
		}
	case contract.OpNotify:
		r.Text, r.Delivery = k.notify(c)
	case contract.OpSetKeys:
		err = k.setKeys(c.Keys)
	default:
		err = fmt.Errorf("the keeper has no call %q", c.Op)
	}
	return r, gone, err
}

// notify shows the pop-up in every window, with each window's own notice
// sequence and the bell when a sound is asked for, and says honestly
// whether a window was there to show it.
func (k *Keeper) notify(c contract.WireCall) (reason, delivery string) {
	cfg, err := contract.ReadPerson(k.kit.Platform.Peek, k.dirs.Config)
	reason, delivery = k.pop.Show(c.Title, c.Text, err != nil || cfg.Nudge.Popup, len(k.wins), time.Now())
	if reason == contract.NotifyShown {
		k.popup(c.Title, c.Text)
		for _, w := range k.wins {
			w.later(overlay.Signal(c.Title, c.Text, c.Sound, w.env))
		}
	}
	return reason, delivery
}

// setKeys replaces the shortcuts in the person's settings file.
func (k *Keeper) setKeys(keys []contract.KeyEntry) error {
	s, err := contract.ReadSettings(k.kit.Platform.Read, k.dirs)
	if err != nil {
		return err
	}
	s.Shortcuts = keys
	path := contract.SettingsPath(k.dirs)
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	err = toml.NewEncoder(tmp).Encode(s)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return k.kit.Platform.Replace(tmp.Name(), path)
}
