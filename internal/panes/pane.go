package panes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/herdr"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/theme"
)

const (
	fleet, actions = "fleet", "actions"

	second = time.Second
	// Every compareEvery seconds the picture the events built is held against
	// a snapshot. A status that differs is looked at again after secondLook,
	// because herdr's status lines come late; a lost line is said for sayLost.
	compareEvery = 5
	secondLook   = time.Second / 2
	sayLost      = time.Minute
)

// news is what a helper tells the loop: a snapshot (look 0 from the event
// connection, 1 from the comparison, 2 from its second look), one event, an
// error, or a line for the person.
type news struct {
	snap *contract.Snapshot
	ev   contract.HerdrEvent
	err  error
	look int
	said string
}

// pane is one pane program. Everything in it belongs to its loop,
// which is also the only goroutine that draws: a helper only calls outside and
// reports on the channel.
type pane struct {
	kind      string
	t         *term.Term
	k         *contract.Kit
	root, run string
	now       func() time.Time
	make      func(contract.ViewInput) contract.View
	bg        context.Context
	heard     chan news

	// What was read, and when each side was last in order.
	state   *contract.State
	readErr error
	ui      contract.UIFile
	figures map[string]contract.CtxFile
	limits  contract.ProjectFile
	chosen  string // the scheme the settings file names
	watch   contract.Watcher
	filesOK time.Time

	picture *contract.Snapshot // herdr as the events built it; nil while it cannot be reached
	herdrOK time.Time
	lost    int
	lostAt  time.Time

	view    contract.View
	cards   []contract.Card
	holding map[string]bool // the items that hold work up, as last built
	stale   bool            // the view model must be built again
	dirty   bool            // the screen must be drawn again

	scheme          theme.Scheme
	plainMarks      bool
	sel             string // the selected card's task or item's id; never a row
	top             int    // the first entry of the list that is drawn
	follow          bool   // bring the selection into view at the next draw
	focused, folded bool
	old             bool // the age line says STALE
	px, py          int
	hits            []hit
	lit             int
	hint            string
}

func newPane(kind string, t *term.Term, k *contract.Kit, root, run string, now func() time.Time) *pane {
	build := func(in contract.ViewInput) contract.View { return *k.View(in) }
	return &pane{kind: kind, t: t, k: k, root: root, run: run, now: now, make: build, heard: make(chan news),
		scheme: theme.Schemes[0], plainMarks: contract.PlainMarks(os.Getenv), px: -1, py: -1, lit: -1, stale: true, dirty: true}
}

// loop is the loop: draw, then wait for a key or a click, a changed file, a
// line from herdr or the next second.
func (p *pane) loop() {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	p.bg = ctx
	p.helper(p.listen)
	p.load()
	p.folded = p.ui.Folded
	defer func() {
		if p.watch != nil {
			p.watch.Close()
		}
	}()
	tick := time.NewTicker(second)
	defer tick.Stop()
	for n := 1; ; {
		if p.dirty {
			p.draw()
		}
		var changes <-chan string
		if p.watch != nil {
			changes = p.watch.Changes()
		}
		select {
		case ev := <-p.t.Events:
			if !p.event(ev) {
				return
			}
		case _, open := <-changes:
			for more := open; more; {
				select {
				case _, more = <-changes:
				default:
					more = false
				}
			}
			if !open {
				p.watch = nil
			}
			p.load()
		case m := <-p.heard:
			p.herdr(m)
		case <-tick.C:
			p.tick(n)
			n++
		}
	}
}

// helper runs fn beside the loop. Should it fail badly, the loop is told and
// the program lives on to put the terminal back.
func (p *pane) helper(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				p.tell(news{err: fmt.Errorf("%v", r), look: 1})
			}
		}()
		fn()
	}()
}

func (p *pane) tell(m news) {
	select {
	case p.heard <- m:
	case <-p.bg.Done():
	}
}

// listen holds the event connection open and opens it again when it ends.
func (p *pane) listen() {
	for p.bg.Err() == nil {
		snap, events, err := p.k.Herdr.Events(p.bg)
		p.tell(news{snap: snap, err: err})
		if err != nil {
			select {
			case <-time.After(second):
			case <-p.bg.Done():
			}
			continue
		}
		for e := range events {
			p.tell(news{ev: e})
		}
	}
}

func (p *pane) snapshot(look int) {
	p.helper(func() {
		snap, err := p.k.Herdr.Snapshot(p.bg)
		p.tell(news{snap: snap, err: err, look: look})
	})
}

// herdr takes one piece of news into the picture.
func (p *pane) herdr(m news) {
	p.stale, p.dirty = true, true
	switch {
	case m.said != "":
		p.hint = m.said
	case m.err != nil:
		if m.look == 0 {
			p.picture = nil
		}
	case m.ev.Kind != "":
		if p.picture != nil {
			herdr.Apply(p.picture, m.ev)
		}
	case m.look == 0:
		p.picture, p.herdrOK = m.snap, p.now()
	case p.picture != nil:
		p.compare(m.snap, m.look)
	}
}

// compare holds the picture against a snapshot. herdr sends no line for a
// pane's folder or an agent's session, so those are taken over without a
// word. A status alone gets a second look half a second later. Anything else,
// and a status that still differs then, is a lost line: the snapshot is
// drawn at once and the age line says so for a minute.
func (p *pane) compare(snap *contract.Snapshot, look int) {
	byID := func(a, b contract.Pane) int { return strings.Compare(a.ID, b.ID) }
	slices.SortFunc(snap.Panes, byID)
	slices.SortFunc(p.picture.Panes, byID)
	status := false
	same := slices.EqualFunc(p.picture.Panes, snap.Panes, func(a, b contract.Pane) bool {
		a.Cwd, a.Session = b.Cwd, b.Session
		if a.Status != b.Status {
			a.Status, status = b.Status, true
		}
		return a == b
	})
	switch now := p.now(); {
	case same && !status:
		p.picture, p.herdrOK = snap, now
	case same && look == 1:
		time.AfterFunc(secondLook, func() { p.snapshot(2) })
	default:
		p.picture, p.herdrOK, p.lost, p.lostAt = snap, now, p.lost+1, now
	}
}

// tick is the slow clock: the ages are drawn again, the files' side is
// checked, and every fifth second herdr is asked for a snapshot.
func (p *pane) tick(n int) {
	p.stale, p.dirty = true, true
	if p.watch == nil || p.run == "" {
		p.load()
	}
	if p.readErr == nil || errors.Is(p.readErr, contract.ErrNoRun) {
		p.filesOK = p.now()
	}
	if n%compareEvery == 0 {
		p.snapshot(1)
	}
}

// load reads everything a pane shows from files, and starts the file
// notices once it knows which run it shows.
func (p *pane) load() {
	p.stale, p.dirty = true, true
	r := p.k.Reader()
	dirs, err := p.k.Platform.Dirs()
	if p.run == "" && err == nil {
		p.run, err = r.Current(p.root)
	}
	if p.watch == nil && err == nil {
		paths := []string{filepath.Join(dirs.State, "ctx"), filepath.Join(dirs.State, "ui.json"),
			filepath.Join(dirs.State, "paused"), filepath.Join(dirs.Config, "config.toml")}
		if p.run != "" {
			paths = append(paths, filepath.Join(r.Dir(p.root, p.run), "state.json"))
			p.watch, _ = p.k.Platform.Watch(paths...)
		}
	}
	p.state = nil
	if err == nil && p.run != "" {
		p.state, err = r.Read(p.root, p.run)
	}
	if p.readErr = err; dirs.State == "" {
		return
	}
	// A file that cannot be read at this moment, as one being replaced on
	// Windows, leaves what the pane had; the next look reads it.
	var ui contract.UIFile
	if contract.ReadVersioned(p.k.Platform.Peek, filepath.Join(dirs.State, "ui.json"), contract.FileVersion, &ui) == nil {
		if ui.Folded != p.ui.Folded {
			p.folded = ui.Folded
		}
		p.ui = ui
	}
	if p.figures = nil; p.state != nil {
		p.figures = contract.ReadCtx(p.k.Platform.Peek, dirs.State, p.state)
	}
	p.limits, _ = contract.ReadProjectFile(p.root)
	cfg := contract.PersonDefaults()
	toml.DecodeFile(filepath.Join(dirs.Config, "config.toml"), &cfg)
	if cfg.UI.Theme != p.chosen {
		p.chosen, p.scheme = cfg.UI.Theme, theme.Get(cfg.UI.Theme)
	}
}

// build has the view model made again. The selection stays on the item it
// was on, whatever arrives above it, and is kept in view; an item that has
// left takes its selection with it, and no other item inherits it.
func (p *pane) build() {
	now := p.now()
	was := slices.Index(p.ids(), p.sel)
	checked := p.filesOK
	if p.herdrOK.Before(checked) {
		checked = p.herdrOK
	}
	// What only this pane knows about its two sources goes on the age line.
	var notes []string
	if p.watch == nil || p.watch.Slow() {
		notes = append(notes, "slow updates")
	}
	if !p.lostAt.IsZero() && now.Sub(p.lostAt) < sayLost {
		notes = append(notes, "herdr: checking every 5 s")
	}
	switch {
	case p.state != nil:
		p.view = p.make(contract.ViewInput{Now: now, Caller: contract.Human, State: p.state, Herdr: p.picture,
			Ctx: p.figures, UI: p.ui, Limits: p.limits, Checked: checked, Notes: notes})
	case p.readErr != nil && !errors.Is(p.readErr, contract.ErrNoRun):
		p.view = contract.View{Alerts: []string{"the record cannot be read: " + p.readErr.Error()}, Fresh: contract.Fresh{Notes: notes}}
	default:
		p.view = contract.View{Alerts: []string{"no run in this folder yet"}, Fresh: contract.Fresh{Checked: checked, Notes: notes}}
	}
	p.cards = p.cards[:0]
	for _, s := range p.view.Sections {
		p.cards = append(p.cards, s.Cards...)
	}
	if at := slices.Index(p.ids(), p.sel); at < 0 {
		p.sel = ""
	} else {
		p.follow = p.follow || at != was
	}
	holding := map[string]bool{}
	for _, it := range p.view.Items {
		if holding[it.ID] = it.Holds; it.Holds && p.holding != nil && !p.holding[it.ID] && !p.view.Strip.DND {
			p.folded = false
		}
	}
	p.holding, p.stale = holding, false
}

// ids is the list a pane chooses from: the cards by task, or the items.
func (p *pane) ids() []string {
	var ids []string
	if p.kind == fleet {
		for _, c := range p.cards {
			ids = append(ids, c.Task)
		}
		return ids
	}
	for _, it := range p.view.Items {
		ids = append(ids, it.ID)
	}
	return ids
}

// event takes a key, a click or a report from the terminal, and reports
// false when the pane is to end.
func (p *pane) event(ev term.Event) bool {
	p.dirty = true
	switch ev.Kind {
	case term.End:
		return false
	case term.Resize:
		p.t.Resize(ev.W, ev.H)
	case term.Focus:
		if p.focused = ev.Focused; p.focused && p.kind == actions && p.sel == "" {
			p.move(1)
		}
	case term.Mouse:
		p.mouse(ev)
	case term.KeyPress:
		return p.key(ev.Key)
	}
	return true
}

// mouse: a click presses what is under it and the wheel moves the list.
// The pointer is reported for every cell it crosses, so the screen is drawn
// again only when another button is under it.
func (p *pane) mouse(ev term.Event) {
	p.px, p.py = ev.X, ev.Y
	switch {
	case ev.Button == term.WheelUp:
		p.top--
	case ev.Button == term.WheelDown:
		p.top++
	case ev.Button == term.Left && ev.Action == term.Press:
		p.hint = ""
		if i := p.under(false); i >= 0 {
			p.hits[i].do()
		}
	default:
		p.dirty = p.under(true) != p.lit
	}
}

func (p *pane) key(key string) bool {
	p.hint = ""
	switch key {
	case "q", "ctrl+c":
		return false
	case "j", "down":
		p.move(1)
	case "k", "up":
		p.move(-1)
	case "t":
		p.scheme = theme.Get(theme.Next(p.scheme.Name))
		p.hint = "colour scheme: " + p.scheme.Name
	case "x":
		p.folded = p.kind == actions && !p.folded
	default:
		if at := slices.Index(p.ids(), p.sel); at >= 0 && p.kind == fleet && key == "enter" {
			p.goTo(p.cards[at].Tab)
		} else if at >= 0 && p.kind == actions {
			for _, b := range p.view.Items[at].Buttons {
				if b.Key == key || key == "enter" && b.Action == "go" {
					p.press(p.view.Items[at], b)
					return true
				}
			}
		}
		for _, a := range contract.Actions {
			if a.Key == key && key != "enter" {
				p.notYet(a.Label)
			}
		}
	}
	return true
}

// move takes the selection one entry on or back; with none, to the first or
// the last.
func (p *pane) move(by int) {
	ids := p.ids()
	if len(ids) == 0 {
		return
	}
	at := slices.Index(ids, p.sel)
	if at < 0 && by < 0 {
		at = len(ids)
	}
	p.sel, p.follow = ids[min(max(at+by, 0), len(ids)-1)], true
}

// press is a button of an item. Going to a tab is the one thing a pane does
// yet; every other button says so.
func (p *pane) press(it contract.Item, b contract.Button) {
	if p.sel = it.ID; b.Action != "go" {
		p.notYet(b.Label)
		return
	}
	for _, c := range p.cards {
		if c.Task == it.Task {
			p.goTo(c.Tab)
		}
	}
}

func (p *pane) notYet(label string) { p.hint = label + ": " + contract.ErrNotBuilt.Error() }

func (p *pane) goTo(tab string) {
	if tab == "" {
		p.hint = "this one has no tab"
		return
	}
	p.helper(func() {
		if err := p.k.Herdr.TabFocus(tab); err != nil {
			p.tell(news{said: "cannot go there: " + err.Error()})
		}
	})
}
