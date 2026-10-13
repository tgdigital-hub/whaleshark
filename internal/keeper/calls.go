package keeper

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/layout"
	"github.com/tgdigital-hub/whaleshark/internal/overlay"
	"github.com/tgdigital-hub/whaleshark/internal/restore"
)

// onPane are the calls that are about one pane, which must be there.
var onPane = []string{contract.OpRun, contract.OpScreen, contract.OpSplit, contract.OpSwap, contract.OpResize,
	contract.OpPaneClose, contract.OpSize, contract.OpPaneFocus, contract.OpAgentStart, contract.OpPoint, contract.OpSay, contract.OpHook}

// own says a fenced call is one its caller may make all the same: one of
// contract.OwnTab about a tab that this same pane opened, and that nobody
// else has run a program or started an agent in since. A pane drawn over
// such a tab is the person's.
func (k *Keeper) own(c contract.WireCall) bool {
	tab := c.Tab
	if c.Op != contract.OpTabClose {
		p := k.panes[c.Pane]
		if c.Op == contract.OpPrompt {
			p = k.named(c.Name)
		}
		if p == nil || p == k.over {
			return false
		}
		tab = p.rec.Tab
	}
	return slices.Contains(contract.OwnTab, c.Op) && tab != "" && k.opened[tab] == c.From
}

// disown forgets what a pane opened: its program was replaced, or it is gone.
func (k *Keeper) disown(pane string) {
	maps.DeleteFunc(k.opened, func(_, by string) bool { return by == pane })
}

// call answers one call. The terminals it ended are closed after the lock
// is given back and before the answer goes out; and an agent is waited for,
// or typed at, by its Watcher with no lock held.
func (k *Keeper) call(c contract.WireCall) (contract.WireReply, error) {
	k.mu.Lock()
	if q := k.panes[c.From]; q != nil && q.rec.Name != "" && slices.Contains(contract.Fenced, c.Op) && !k.own(c) {
		k.mu.Unlock()
		return contract.WireReply{}, contract.Fence(c.Op)
	} else if k.walled(c) {
		k.mu.Unlock()
		return contract.WireReply{}, contract.ErrSpace
	}
	r, gone, err := k.do(c)
	p := k.panes[c.Pane]
	if c.Op == contract.OpPrompt {
		p = k.named(c.Name)
	}
	k.mu.Unlock()
	end(gone)
	if err != nil || p == nil {
		return r, err
	}
	switch wait := time.Duration(c.Millis) * time.Millisecond; c.Op {
	case contract.OpAgentStart:
		err = p.watch.Ready(wait)
	case contract.OpPrompt:
		err = p.watch.Prompt(c.Text, wait)
	case contract.OpPoint:
		err = p.watch.Point(c.Pointer, c.Text)
	case contract.OpSay:
		err = p.watch.Say(c.Kind, c.Text)
	}
	return r, err
}

// walled says a call comes from a pane that holds an agent and names a
// pane, a tab or a workspace that is not of that pane's own workspace: the
// wall of contract.Walled. The dock is of no workspace and behind no wall.
func (k *Keeper) walled(c contract.WireCall) bool {
	from := k.panes[c.From]
	if from == nil || from.rec.Agent == "" || !slices.Contains(contract.Walled, c.Op) {
		return false
	}
	p, own := k.panes[c.Pane], from.rec.Workspace
	if c.Op == contract.OpPrompt {
		p = k.named(c.Name)
	}
	for _, q := range []*pane{p, k.panes[c.Target]} {
		if q != nil && !q.rec.Dock && q.rec.Workspace != own {
			return true
		}
	}
	t := k.lay.Tab(c.Tab)
	return t != nil && t.Space != own || c.Space != "" && c.Space != own
}

// open is the workspace of a project's folder, made where there is none.
// The folder is the caller's word and is checked to be one; which project
// it is of is the caller's knowledge, and no file is read for it.
func (k *Keeper) open(root string) (*layout.Space, error) {
	if root != "" {
		root = filepath.Clean(root)
	}
	known := slices.ContainsFunc(k.lay.Spaces, func(s *layout.Space) bool { return s.Root == root })
	if st, err := os.Stat(root); root != "" && !known && (err != nil || !st.IsDir()) {
		return nil, fmt.Errorf("%s is no folder to open a workspace of", root)
	}
	return k.lay.Open(root), nil
}

// create opens a tab of one pane in a workspace, without showing it, and
// starts the pane's program. A tab that does not start leaves nothing.
func (k *Keeper) create(sp *layout.Space, c contract.WireCall) (*pane, error) {
	k.nTab++
	id := "t" + strconv.Itoa(k.nTab)
	p := k.fresh(id, sp.ID, c.Label, c.Cwd)
	if k.tabs[id] = c.Env; k.panes[c.From] != nil {
		k.opened[id] = c.From
	}
	k.lay.AddIn(sp, id, c.Label, p.rec.ID).Group = c.Group
	if err := k.start(p); err != nil {
		k.lay.Remove(id)
		delete(k.tabs, id)
		delete(k.opened, id)
		k.nTab--
		k.forget(p)
		return nil, err
	}
	k.emit(contract.EvOpened, p.rec)
	return p, nil
}

// shut closes a tab whole and tells the readers of events; its terminals
// are the caller's to end. A workspace ends with its last tab.
func (k *Keeper) shut(tab *layout.Tab) (gone []contract.Pty) {
	for _, id := range tab.Panes() {
		gone = append(gone, k.panes[id].tty)
		k.panes[id].quit()
		delete(k.panes, id)
		k.disown(id) // what a pane of it opened is nobody's now, as when a pane is closed alone
	}
	k.lay.Remove(tab.ID)
	delete(k.tabs, tab.ID)
	delete(k.opened, tab.ID)
	k.emit(contract.EvTabClosed, contract.Pane{Tab: tab.ID})
	return gone
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
	// The dock is looked at and nothing else: not closed, split, swapped or run in.
	if q := k.panes[c.Target]; q != nil && q.rec.Dock ||
		p != nil && p.rec.Dock && slices.Contains(onPane, c.Op) && c.Op != contract.OpScreen && c.Op != contract.OpSize {
		return r, nil, contract.ErrDock
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
		if k.foreign() {
			r.Text += "; the settings file can be written by another login and is not used"
		}
		if k.saver == nil {
			r.Text += "; nothing is saved"
		} else if err := k.saver.Err(); err != nil {
			r.Text += "; the layout was not saved: " + err.Error()
		}
	case contract.OpTabCreate:
		// From a pane the tab is of that pane's workspace and its group,
		// whatever the call says; else of the workspace of the folder the call
		// names, made where there is none, or with none named of the one in
		// front. A group the call itself names begins with this tab, its
		// leader's, while the group has none.
		sp, from, led := k.lay.Space(k.lay.Front), k.panes[c.From], c.Group != ""
		if own := k.lay.Space(cmp.Or(from, &pane{}).rec.Workspace); own != nil {
			if t := k.lay.Tab(from.rec.Tab); t != nil && t.Group != "" {
				c.Group, led = t.Group, false
			}
			sp = own
		} else if c.Space != "" || sp == nil {
			sp, err = k.open(c.Space)
		}
		if sp == nil {
			err = cmp.Or(err, contract.ErrNoPane)
			break
		}
		// The label is cleaned as every label is, and one tab of a workspace
		// at most reads as the main agent's.
		if c.Label = contract.Label(c.Label); contract.IsMainTab(c.Label) && slices.ContainsFunc(k.lay.Tabs, func(t *layout.Tab) bool {
			return t.Space == sp.ID && contract.IsMainTab(t.Label)
		}) {
			err = contract.ErrMainTab
			break
		}
		led = led && !slices.ContainsFunc(k.lay.Tabs, func(t *layout.Tab) bool { return t.Space == sp.ID && t.Group == c.Group && t.Head })
		if p, err = k.create(sp, c); err != nil {
			break
		}
		k.lay.Tab(p.rec.Tab).Head = led
		k.changed()
		rec := p.rec
		r.Pane = &rec
	case contract.OpSpaceOpen:
		sp, oerr := k.open(c.Space)
		if err = oerr; err == nil && sp.Active == "" {
			c.Label, c.Group, c.Env, c.From = contract.MainTab, "", nil, ""
			if p, err = k.create(sp, c); err == nil {
				k.lay.Tab(p.rec.Tab).Chat = true // the main agent's, until somebody names it
			}
		}
		if err != nil {
			break
		}
		k.lay.Show(sp.Active)
		k.changed()
		rec := k.panes[k.focus].rec
		r.Pane = &rec
	case contract.OpSpaceRename, contract.OpSpaceClose:
		sp := k.lay.Space(c.Space)
		if sp == nil {
			return r, nil, contract.ErrNoPane
		}
		for _, t := range slices.Clone(k.lay.Tabs) {
			if t.Space == sp.ID && c.Op == contract.OpSpaceClose {
				gone = append(gone, k.shut(t)...)
			}
		}
		if c.Op == contract.OpSpaceRename {
			if sp.Name, sp.Named = contract.Label(c.Label), true; sp.Name == "" { // no name is the folder's again
				sp.Name, sp.Named = contract.SpaceName(sp.Root), false
			}
		}
		k.changed()
	case contract.OpMarks:
		// The dock's alone; a figure for a tab that is not there is dropped.
		if c.From != layout.DockPane {
			return r, nil, contract.Fence(c.Op)
		}
		k.lay.Marks = maps.Clone(c.Marks)
		maps.DeleteFunc(k.lay.Marks, func(tab string, _ int) bool { return k.lay.Tab(tab) == nil })
		k.wake()
	case contract.OpSplit:
		n := k.fresh(p.rec.Tab, p.rec.Workspace, p.rec.Label, p.rec.Cwd)
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
		// An agent is its tool's program, by that kind's description, with the
		// arguments given and its name; whatever else is run there is none.
		old, was, rec, wired := p.tty, p.argv, p.rec, true
		p.argv, p.rec.Agent, p.rec.Name, p.rec.Session = c.Argv, "", "", ""
		if c.Op == contract.OpAgentStart {
			t, ok := k.tool(c.Kind)
			p.argv, p.rec.Agent, p.rec.Name, wired = slices.Concat(t.Run, c.Argv), c.Kind, c.Name, ok
		}
		if q := k.named(c.Name); q != nil && q != p {
			// A prompt finds its agent by name: two of one name would share it.
			err = fmt.Errorf("an agent named %q is in pane %s already", c.Name, q.rec.ID)
		} else if !wired || len(p.argv) == 0 || p.argv[0] == "" {
			err = errors.New("no program to run") // none named, or a kind nobody wired in
		} else {
			err = k.start(p)
		}
		if err != nil {
			p.argv, p.rec = was, rec
			break
		}
		// Whoever opened the tab for an agent of its own has it no longer once
		// another runs something there; nor has a pane what it opened once its
		// own program is another.
		if k.disown(p.rec.ID); k.opened[p.rec.Tab] != c.From {
			delete(k.opened, p.rec.Tab)
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
		// A session that could be read as an option is nobody's session: it
		// would stand on the agent's command line after a restart.
		if !restore.Session(c.Session) {
			c.Session = ""
		}
		p.hooked = true
		p.watch.Hook(c.Kind, c.Session, c.Cwd)
	case contract.OpPrompt:
		if k.named(c.Name) == nil {
			err = contract.ErrNoPane
		}
	case contract.OpPoint:
	case contract.OpSay:
		// The person's note goes to the agent the person started by hand, the
		// one they talk to: never to an agent the tool started, a shell or a
		// pane of ours.
		if p.rec.Agent == "" || p.rec.Name != "" || p == k.over {
			err = contract.ErrAgentNotReady
		}
	case contract.OpOverlay:
		// A screen of ours never opens over a project it is not about. One
		// asked for at a cell of a pane stands at that cell of the window.
		from, at := k.panes[c.From], layout.Rect{}
		if p != nil {
			at = k.place(p)
		}
		if from != nil && from == k.over && k.placed != "" && c.Dir == contract.OverAt && len(c.Argv) == 0 {
			// The placed pane's own program asks for another size of its
			// box: the corner stays, and the box is moved to stay inside.
			k.overW, k.overH = cmp.Or(c.W, k.overW), cmp.Or(c.H, k.overH)
			k.changed()
			break
		} else if from != nil && !from.rec.Dock && from.rec.Workspace != k.lay.Front {
			err = contract.ErrNotFront
			break
		} else if len(c.Argv) == 0 || k.over != nil {
			err = errors.New("no program to draw over the tab, or one is there already")
			break
		}
		// It runs as in the pane it was asked from, or the one with the keys.
		if from == nil || from.rec.Dock {
			from = cmp.Or(k.panes[k.focus], &pane{})
		}
		p = k.fresh(from.rec.Tab, from.rec.Workspace, from.rec.Label, from.rec.Cwd)
		p.argv, k.over, k.overW, k.overH = c.Argv, p, cmp.Or(c.W, 0.8), cmp.Or(c.H, 0.8)
		if c.Dir == contract.OverAt {
			k.placed, k.overX, k.overY = k.stands(), at.X+c.X, at.Y+c.Y
		}
		if err = k.start(p); err != nil {
			k.over, k.placed = nil, ""
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
		if t := k.lay.Of(to); t != nil { // a pane in no tab, drawn over one, takes no keys this way
			k.lay.Focus(to)
			k.lay.Show(t.ID)
			k.lay.Dock.Keys, k.lay.Dock.Open = false, false
			k.changed()
		}
		r.Text = k.focus
	case contract.OpTabRename, contract.OpTabFocus, contract.OpTabClose, contract.OpTabGive:
		if tab == nil || c.Op == contract.OpTabGive && k.panes[c.Target] == nil {
			return r, nil, contract.ErrNoPane
		}
		switch c.Op {
		case contract.OpTabGive:
			k.opened[c.Tab] = c.Target
			k.saved()
		case contract.OpTabRename:
			// A name somebody gave the tab holds, cleaned as every label is;
			// the main agent's default one follows the chat's, and one tab of
			// a workspace at most reads as the main agent's.
			label := contract.Label(c.Label)
			if contract.IsMainTab(label) && slices.ContainsFunc(k.lay.Tabs, func(t *layout.Tab) bool {
				return t != tab && t.Space == tab.Space && contract.IsMainTab(t.Label)
			}) {
				return r, nil, contract.ErrMainTab
			}
			tab.Chat = label == contract.MainTab
			tab.Named = !tab.Chat
			k.rename(tab, label)
		case contract.OpTabFocus:
			k.lay.Show(c.Tab) // its workspace comes to the front with it
			k.lay.Dock.Keys, k.lay.Dock.Open = false, false
			k.changed()
		case contract.OpTabClose:
			gone = k.shut(tab)
			k.changed()
		}
	case contract.OpNotify:
		r.Text, r.Delivery = k.notify(c)
	case contract.OpSetKeys:
		err = k.setKeys(c.Keys)
	case contract.OpDock:
		// The keys to the dock or back, which opens and closes a rail; or it is folded.
		d, keys, fold := &k.lay.Dock, c.Dir == contract.DockKeys, c.Dir == contract.DockFold
		if !keys && !fold && c.Dir != contract.DockBack {
			return r, nil, fmt.Errorf("the dock is told %s, %s or %s", contract.DockKeys, contract.DockBack, contract.DockFold)
		} else if d.Fold = d.Fold != fold; !fold {
			d.Keys, d.Open = keys, keys
		}
		k.changed()
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
