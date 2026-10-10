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

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/theme"
)

const (
	fleet, actions, menu = "fleet", "actions", "menu"

	second = time.Second
	// Every compareEvery seconds the picture the events built is held against
	// a snapshot. A status that differs is looked at again after secondLook,
	// because a status can come late; a lost line is said for sayLost.
	compareEvery = 5
	secondLook   = time.Second / 2
	sayLost      = time.Minute
	// Every lookEvery seconds a pane looks whether its program file was replaced.
	lookEvery = 60
)

// news is what a helper tells the loop: a snapshot (look 0 from the event
// connection, 1 from the comparison, 2 from its second look), one event, an
// error, a line for the person, or something for the loop itself to do.
type news struct {
	snap *contract.Snapshot
	ev   contract.TermEvent
	err  error
	look int
	said string
	do   func()
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

	command string             // the key that starts a shortcut of the engine's, as the person set it
	picture *contract.Snapshot // the terminals as the events built it; nil while it cannot be reached
	termsOK time.Time
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

	// self is the program a child is started from, born the time of its file
	// when the pane began, me this pane's own id; jobs are the children,
	// started one after another, and waiting counts those not yet ended.
	self, me   string
	born       time.Time
	jobs       chan func()
	waiting    int
	again      bool // the program file was replaced: the pane starts itself again
	swept      contract.Swept
	sweptAt    time.Time
	here       time.Time
	all, above bool // the done cards are shown; the action pane lies above the conversation

	// items is what waits, with an answer that is still settling kept where
	// its item stood. The typing line belongs to the item lineFor, or with
	// lineDo to a row of the action table that needs words.
	items            []contract.Item
	line             *term.Line
	lineFor, linePre string
	lineDo           func(string)
	lineDrawn        bool
	over             string // the list that lies over the pane, if any
	pick             int
	query            term.Line
	catch            contract.Catchup
	away             time.Time // when the person left, kept until they have been caught up
	shown            contract.Card
	sure, asked      string // the row that asked "sure?", and the one a second press confirms

	// The typing guard: the last letter, what it did, and whether a burst runs.
	letter  time.Time
	lastKey string
	undo    func()
	burst   bool
	// The click guard: what each row shows, and when that last changed by itself.
	rows    []string
	movedAt []time.Time
	own     bool // the person's own key or click is the reason for the next draw
}

func newPane(kind string, t *term.Term, k *contract.Kit, root, run string, now func() time.Time) *pane {
	build := func(in contract.ViewInput) contract.View { return *k.View(in) }
	me, _ := contract.PaneOf(os.Getenv)
	return &pane{kind: kind, t: t, k: k, root: root, run: run, now: now, make: build, heard: make(chan news), me: me,
		scheme: theme.Schemes[0], plainMarks: contract.PlainMarks(os.Getenv), px: -1, py: -1, lit: -1, stale: true, dirty: true}
}

// loop is the loop: draw, then wait for a key or a click, a changed file, a
// line from the terminals or the next second. It reports true when the pane is to
// start itself again from a new program file.
func (p *pane) loop() bool {
	ctx, stop := context.WithCancel(context.Background())
	p.bg, p.jobs = ctx, make(chan func(), 32)
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		for job := range p.jobs {
			job()
		}
	}()
	p.helper(p.listen)
	p.load()
	if p.folded = p.ui.Folded; p.kind == menu {
		p.over = menu
	}
	if st, err := os.Stat(p.self); err == nil {
		p.born = st.ModTime()
	}
	defer func() {
		if p.watch != nil {
			p.watch.Close()
		}
		// A child that was started is let end: it may be saving a draft.
		stop()
		close(p.jobs)
		<-ended
	}()
	tick := time.NewTicker(second)
	defer tick.Stop()
	for n := 1; ; {
		if p.again || p.kind == menu && p.over == "" && p.line == nil && p.waiting == 0 {
			return p.again
		}
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
				p.leave()
				return false
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
			p.terms(m)
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
		snap, events, err := p.k.Terms.Events(p.bg)
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
		snap, err := p.k.Terms.Snapshot(p.bg)
		p.tell(news{snap: snap, err: err, look: look})
	})
}

// terms takes one piece of news into the picture.
func (p *pane) terms(m news) {
	p.stale, p.dirty = true, true
	switch {
	case m.do != nil:
		m.do()
	case m.said != "":
		p.hint = m.said
	case m.err != nil:
		if m.look == 0 {
			p.picture = nil
		}
	case m.ev.Kind != "":
		if p.picture != nil {
			contract.Apply(p.picture, m.ev)
		}
		if m.ev.Kind == contract.EvFocus {
			p.present()
		}
	case m.look == 0:
		p.picture, p.termsOK = m.snap, p.now()
	case p.picture != nil:
		p.compare(m.snap, m.look)
	}
}

// compare holds the picture against a snapshot. A pane's folder and an
// agent's session are taken over without a word. A status alone gets a second look half a second later. Anything else,
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
		p.picture, p.termsOK = snap, now
	case same && look == 1:
		time.AfterFunc(secondLook, func() { p.snapshot(2) })
	default:
		p.picture, p.termsOK, p.lost, p.lostAt = snap, now, p.lost+1, now
	}
}

// tick is the slow clock: the ages are drawn again, the files' side is
// checked, and every fifth second the keeper is asked for a snapshot.
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
		p.sweep()
	}
	if st, err := os.Stat(p.self); n%lookEvery == 0 && err == nil && !st.ModTime().Equal(p.born) {
		p.leave()
		p.again = true
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
		paths := []string{filepath.Join(dirs.State, "ctx"), filepath.Join(dirs.State, contract.UIFileName),
			filepath.Join(dirs.State, "paused"), filepath.Join(dirs.Config, contract.ConfigFile)}
		if p.run != "" {
			paths = append(paths, filepath.Join(r.Dir(p.root, p.run), contract.StateFile))
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
	if contract.ReadVersioned(p.k.Platform.Peek, filepath.Join(dirs.State, contract.UIFileName), contract.FileVersion, &ui) == nil {
		p.ui = ui
	}
	if p.figures = nil; p.state != nil {
		p.figures = contract.ReadCtx(p.k.Platform.Peek, dirs.State, p.state)
	}
	p.limits, _ = contract.ReadProjectFile(p.root)
	// The engine's command key, for the hint that names it.
	keys, _ := contract.ReadSettings(p.k.Platform.Peek, dirs)
	p.command = keys.Keys.Command
	cfg := person(p.k, dirs)
	if p.above = cfg.UI.Actions == "top"; cfg.UI.Theme != p.chosen {
		p.chosen, p.scheme = cfg.UI.Theme, theme.Get(cfg.UI.Theme)
	}
}

// person reads the login's own settings over what holds when they say nothing.
func person(k *contract.Kit, dirs contract.Dirs) contract.PersonConfig {
	cfg, _ := contract.ReadPerson(k.Platform.Peek, dirs.Config)
	return cfg
}

// build has the view model made again. The selection stays on the item it
// was on, whatever arrives above it, and is kept in view; an item that has
// left takes its selection with it, and no other item inherits it.
func (p *pane) build() {
	now := p.now()
	order := p.ids()
	was := slices.Index(order, p.sel)
	checked := p.filesOK
	if p.termsOK.Before(checked) {
		checked = p.termsOK
	}
	// What only this pane knows about its two sources goes on the age line.
	var notes []string
	if p.watch == nil || p.watch.Slow() {
		notes = append(notes, "slow updates")
	}
	if !p.lostAt.IsZero() && now.Sub(p.lostAt) < sayLost {
		notes = append(notes, "terminals: checking every 5 s")
	}
	switch {
	case p.state != nil:
		p.view = p.make(contract.ViewInput{Now: now, Caller: contract.Human, State: p.state, Terms: p.picture,
			Ctx: p.figures, UI: p.ui, Limits: p.limits, Checked: checked, Notes: notes,
			Swept: p.swept, Watched: !p.sweptAt.IsZero() && now.Sub(p.sweptAt) < 3*compareEvery*second, All: p.all})
		if a := p.view.Strip.Away; !a.IsZero() {
			p.away = a
		}
	case p.readErr != nil && !errors.Is(p.readErr, contract.ErrNoRun):
		p.view = contract.View{Alerts: []string{"the record cannot be read: " + p.readErr.Error()}, Fresh: contract.Fresh{Notes: notes}}
	default:
		p.view = contract.View{Alerts: []string{"no run in this folder yet"}, Fresh: contract.Fresh{Checked: checked, Notes: notes}}
	}
	p.cards = p.cards[:0]
	for _, s := range p.view.Sections {
		p.cards = append(p.cards, s.Cards...)
	}
	// An answer that may still be taken back stays where its item stood.
	p.items = slices.Clone(p.view.Items)
	for _, it := range p.view.Answered {
		if at := slices.Index(order, it.ID); p.kind != fleet && now.Before(it.Settles) && it.Used.IsZero() {
			p.items = slices.Insert(p.items, min(max(at, 0), len(p.items)), it)
		}
	}
	ids := p.ids()
	if at := slices.Index(ids, p.sel); at < 0 {
		p.sel = ""
	} else {
		p.follow = p.follow || at != was
	}
	if p.lineFor != "" && !slices.Contains(ids, p.lineFor) {
		p.leave()
	}
	holding := map[string]bool{}
	for _, it := range p.view.Items {
		if holding[it.ID] = it.Holds; it.Holds && p.holding != nil && !p.holding[it.ID] && !p.view.Strip.DND {
			p.fold(false)
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
	for _, it := range p.items {
		ids = append(ids, it.ID)
	}
	return ids
}

// event takes a key, a click or a report from the terminal, and reports
// false when the pane is to end.
func (p *pane) event(ev term.Event) bool {
	p.dirty, p.own = true, ev.Kind != term.Focus
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
	case term.KeyPress, term.Paste:
		return p.key(ev)
	}
	return true
}

// mouse: a click presses what is under it and the wheel moves the list.
// The pointer is reported for every cell it crosses, so the screen is drawn
// again only when another button is under it. A click on a row that has just
// moved by itself is refused: the button slid under the finger.
func (p *pane) mouse(ev term.Event) {
	p.px, p.py = ev.X, ev.Y
	by := map[term.Button]int{term.WheelUp: -1, term.WheelDown: 1}[ev.Button]
	switch {
	case by != 0 && p.over != "":
		p.pick += by
	case by != 0:
		p.top += by
	case ev.Button == term.Left && ev.Action == term.Press:
		p.hint, p.asked, p.sure = "", p.sure, ""
		p.present()
		if ev.Y < len(p.movedAt) && time.Since(p.movedAt[ev.Y]) < clickWait {
			p.hint = movedSaid
		} else if i := p.under(false); i >= 0 {
			p.hits[i].do()
		}
	default:
		p.dirty = p.under(true) != p.lit
	}
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

func (p *pane) goTo(tab string) {
	if tab == "" {
		p.hint = "this one has no tab"
		return
	}
	p.helper(func() {
		if err := p.k.Terms.TabFocus(tab); err != nil {
			p.tell(news{said: "cannot go there: " + err.Error()})
		}
	})
}
