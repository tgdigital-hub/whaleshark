package keeper

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// How long a notice stays on the tab row.
const noteFor = 5 * time.Second

// onPane are the calls that are about one pane, which must be there.
var onPane = []string{contract.OpRun, contract.OpScreen, contract.OpSplit, contract.OpSwap, contract.OpResize,
	contract.OpPaneClose, contract.OpSize, contract.OpPaneFocus}

// call answers one call. The terminals it ended are closed after the lock
// is given back and before the answer goes out.
func (k *Keeper) call(c contract.WireCall) (contract.WireReply, error) {
	k.mu.Lock()
	r, gone, err := k.do(c)
	k.mu.Unlock()
	end(gone)
	return r, err
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
	case contract.OpRun:
		if len(c.Argv) == 0 {
			err = errors.New("no program to run")
			break
		}
		old, was := p.tty, p.argv
		p.argv = c.Argv
		if err = k.start(p); err != nil {
			p.argv = was
			break
		}
		gone = append(gone, old)
		k.emit(contract.EvState, p.rec)
		k.wake()
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
			k.wake()
		case contract.OpTabFocus:
			k.lay.Show(c.Tab)
			k.changed()
		case contract.OpTabClose:
			for _, id := range tab.Panes() {
				gone = append(gone, k.panes[id].tty)
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
	case contract.OpAgentStart, contract.OpPrompt, contract.OpPoint, contract.OpHook, contract.OpOverlay:
		err = contract.ErrNotBuilt
	default:
		err = fmt.Errorf("the keeper has no call %q", c.Op)
	}
	return r, gone, err
}

// notify puts a notice on the tab row of every window for a few seconds,
// with the bell when a sound is asked for, and says honestly whether a
// window was there to show it.
func (k *Keeper) notify(c contract.WireCall) (reason, delivery string) {
	if cfg, err := contract.ReadPerson(k.kit.Platform.Peek, k.dirs.Config); err == nil && !cfg.Nudge.Popup {
		return contract.NotifyOff, contract.NotifyOff
	}
	if len(k.wins) == 0 {
		return contract.NotifyNoWindow, "on"
	}
	k.note, k.noteAt = term.Clean(c.Title+": "+c.Text), time.Now()
	for _, w := range k.wins {
		if c.Sound {
			w.bell.Store(true)
		}
	}
	k.wake()
	time.AfterFunc(noteFor, func() {
		k.mu.Lock()
		k.wake()
		k.mu.Unlock()
	})
	return contract.NotifyShown, "on"
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
