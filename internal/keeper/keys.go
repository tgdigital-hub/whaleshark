package keeper

import (
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"os/exec"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/input"
	"github.com/tgdigital-hub/whaleshark/internal/layout"
	"github.com/tgdigital-hub/whaleshark/internal/restore"
	"github.com/tgdigital-hub/whaleshark/internal/screen"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// input takes what a window's terminal reported, until its input ends. The
// input package says what a report means and the keeper does it; a key goes
// first to the view of the pane with the keys, which has the copy key and
// selecting by keys. Whatever writes to a terminal, starts a program or
// waits for one is done after the lock is given back.
func (k *Keeper) input(w *window) {
	for ev := range w.t.Events {
		if ev.Kind == term.End {
			return
		}
		var gone []contract.Pty
		var after []func()
		typed := func(p *pane, text string) {
			if tty := p.tty; text != "" {
				after = append(after, func() { io.WriteString(tty, text) })
			}
		}
		k.mu.Lock()
		if ev.Kind != term.Focus && (ev.Kind != term.Mouse || ev.Action == term.Press) {
			k.use(w)
		}
		v := input.View{Lay: &k.lay, Modes: k.modes}
		keys := k.panes[k.focus]
		if k.over != nil {
			v.Over, keys = k.over.rec.ID, k.over
		}
		var acts []input.Act
		used, moved := false, false
		if ev.Kind == term.KeyPress && keys != nil && !w.in.Armed {
			keys.mu.Lock()
			d := keys.pick.Key(ev, keys.scr, w.board.CtrlC(k.set.Keys.Style))
			keys.mu.Unlock()
			if used = d.Used; d.Copy != "" {
				after = append(after, func() { k.copy(w, d.Copy) })
			}
		}
		if !used {
			acts = w.in.Take(ev, v)
		}
		for _, a := range acts {
			moved = moved || a.Kind == input.Row || a.Kind == input.Drag || a.Kind == input.Zoom
			p := k.panes[a.Pane]
			switch {
			case a.Kind == input.Call:
				if q := keys; a.Call.Op == contract.OpTabCreate && q != nil {
					a.Call.Cwd = q.rec.Cwd
				}
				r, g, _ := k.do(a.Call)
				if gone = append(gone, g...); r.Pane != nil {
					// A pane the person made with a key gets the keys.
					k.do(contract.WireCall{Op: contract.OpPaneFocus, Pane: r.Pane.ID})
				}
			case a.Kind == input.Row:
				k.lay.Wheel(a.Step)
			case a.Kind == input.Drag:
				k.lay.Drag(a.Hit, a.X, a.Y)
			case a.Kind == input.Run && len(a.Argv) > 0:
				// #nosec G204 -- the person's own shortcut, from their own settings file
				cmd := exec.Command(a.Argv[0], a.Argv[1:]...)
				cmd.Env = append(os.Environ(), contract.EnvTermActivePane+"="+a.Pane)
				if cmd.Start() == nil {
					go cmd.Wait()
				}
			case a.Kind == input.Popup:
				k.do(contract.WireCall{Op: contract.OpOverlay, Argv: a.Argv, From: a.Pane})
			case p == nil:
			case a.Kind == input.Zoom:
				k.lay.Zoom(a.Pane)
			case a.Kind == input.Allow:
				if text := p.wish; text != "" {
					p.wish = ""
					after = append(after, func() { k.copy(w, text) })
				}
			case a.Kind == input.Redraw:
				// A program draws again when its terminal changes size: by
				// one row and back, with a moment for it to see each.
				tty, cols, rows := p.tty, p.w, p.h
				after = append(after, func() {
					tty.Resize(cols, max(rows-1, 1))
					time.Sleep(50 * time.Millisecond)
					tty.Resize(cols, rows)
				})
			default:
				p.mu.Lock()
				switch a.Kind {
				case input.Type:
					// A key or a paste for the program: back to the live picture.
					p.pick.Live()
					typed(p, a.Text)
				case input.Scroll:
					p.pick.Back -= a.Step
				case input.Mark:
					p.pick.Select(p.scr)
				case input.Select:
					d := p.pick.Mouse(term.Event{Kind: term.Mouse, Button: term.Left, Action: a.Action, X: a.X, Y: a.Y}, k.place(p), p.scr, time.Now())
					if typed(p, d.Type); d.Copy != "" {
						after = append(after, func() { k.copy(w, d.Copy) })
					}
				}
				p.mu.Unlock()
			}
		}
		if moved {
			k.changed()
		}
		k.wake()
		k.mu.Unlock()
		end(gone)
		for _, do := range after {
			do()
		}
	}
}

// place is where a pane shows in the window used last.
func (k *Keeper) place(p *pane) (at layout.Rect) {
	if p == k.over {
		return k.lay.OverAt
	}
	if t := k.lay.Of(p.rec.ID); t != nil {
		for _, pl := range k.lay.Places(t) {
			if pl.Pane == p.rec.ID {
				at = pl.Rect
			}
		}
	}
	return at
}

// modes is what a pane's program asked of its terminal.
func (k *Keeper) modes(pane string) (m screen.Modes) {
	if p := k.panes[pane]; p != nil {
		p.mu.Lock()
		m = p.scr.Modes()
		p.mu.Unlock()
	}
	return m
}

// notice reads the person's settings again whenever their file changes:
// every window's keys, and the scroll-back of the panes started after.
func (k *Keeper) notice() {
	w, err := k.kit.Platform.Watch(contract.SettingsPath(k.dirs))
	if err != nil {
		return
	}
	defer w.Close()
	for {
		select {
		case <-w.Changes():
		case <-k.done:
			return
		}
		k.mu.Lock()
		k.set = k.settings()
		for _, win := range k.wins {
			win.in.Keys = input.New(k.set, win.sys)
		}
		k.mu.Unlock()
	}
}

// back brings back what the layout file holds, each pane under its old id
// and over its old lines, and starts the saving. A pane whose program does
// not start comes back as a shell, and its record says so.
func (k *Keeper) back() {
	f, lay, err := restore.Load(k.kit.Platform, k.dirs)
	if k.lost = err; !errors.Is(err, contract.ErrNewer) {
		k.saver = restore.Start(k.kit.Platform, k.dirs, k.pack)
	}
	if f == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.lay, k.tabs, k.nTab, k.nPane = *lay, f.Vars, f.NTab, f.NPane
	for i := range f.Panes {
		sp := &f.Panes[i]
		p := &pane{rec: sp.Rec, argv: sp.Argv, from: sp}
		k.panes[p.rec.ID] = p
		if k.start(p) != nil {
			sp.Shell()
			if p.rec, p.argv = sp.Rec, nil; k.start(p) != nil {
				k.drop(p)
			}
		}
	}
	k.changed()
}

// pack is the layout file as it is now.
func (k *Keeper) pack() *restore.File {
	k.mu.Lock()
	defer k.mu.Unlock()
	f := &restore.File{W: k.lay.W, H: k.lay.H, NTab: k.nTab, NPane: k.nPane, Vars: maps.Clone(k.tabs)}
	f.Layout, _ = json.Marshal(&k.lay)
	for _, p := range k.panes {
		p.mu.Lock()
		f.Panes = append(f.Panes, restore.Pane{Rec: p.rec, Argv: p.argv, Lines: restore.Text(p.scr, k.set.Scrollback.Keep)})
		p.mu.Unlock()
	}
	return f
}
