package panes

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/term/termtest"
	"github.com/tgdigital-hub/whaleshark/internal/theme"
	"github.com/tgdigital-hub/whaleshark/test/fakeengine"
)

// reader is the record of the fixture, read as the panes read it.
type reader struct {
	contract.NoStore
	dir   string
	state *contract.State
	reads *atomic.Int32
	fails *atomic.Bool
}

func (r reader) Read(string, string) (*contract.State, error) {
	if r.reads.Add(1); r.fails.Load() {
		return nil, errors.New("the disk is gone")
	}
	return r.state, nil
}
func (r reader) Current(string) (string, error) { return "r3", nil }

// Dir lays the folders out as the store does: the project's own folder of
// ours, and each run's under its runs.
func (r reader) Dir(_, run string) string {
	if run == "" {
		return filepath.Join(r.dir, contract.ProjectDir)
	}
	return filepath.Join(r.dir, contract.ProjectDir, "runs", run)
}

// system is this system with a home of its own and env for its environment.
// Windows keeps a program's files in two folders its environment names and
// has no rule without them, so there they lie inside the home too.
func system(home string, env map[string]string) *platform.System {
	sys := platform.New(runtime.GOOS)
	sys.Home, sys.Env = home, func(name string) string {
		switch name {
		case "APPDATA":
			return filepath.Join(home, "roaming")
		case "LOCALAPPDATA":
			return filepath.Join(home, "local")
		}
		return env[name]
	}
	return sys
}

// world is one pane on a pretended terminal, drawing the view model of the
// evening fixture, with the engine's double holding the fixture's picture and real
// file notices on a folder of its own.
type world struct {
	t        *testing.T
	s        *termtest.Screen
	p        *pane
	double   *fakeengine.Fake
	fx       *testkit.Fixture
	sys      *platform.System
	file     string // the run's state.json
	home     string
	h        int
	clock    atomic.Int64
	reads    atomic.Int32 // how often the pane has read the record
	fails    atomic.Bool  // the record cannot be read
	in       contract.ViewInput
	next     int // the children and the calls to the terminals a test has looked at
	nextCall int
	began    bool // the pane's loop runs and can be asked

	mu   sync.Mutex
	view contract.View
	seen *contract.Snapshot // the terminals' picture as the builder was last handed it
}

func start(t *testing.T, kind string, w, h int, env map[string]string) *world {
	return startWith(t, kind, w, h, env, func(*world) {})
}

// startWith is start with something done to the world before the pane begins.
func startWith(t *testing.T, kind string, w, h int, env map[string]string, prepare func(*world)) *world {
	t.Parallel()
	fx, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	wd := &world{t: t, s: termtest.New(w, h), h: h, fx: fx, view: *fx.Views[contract.Human]}
	home := t.TempDir()
	wd.home = home
	k := contract.NewKit()
	wd.sys = system(home, env)
	k.Platform, k.Store = wd.sys, reader{dir: home, state: &fx.State, reads: &wd.reads, fails: &wd.fails}
	wd.file = filepath.Join(k.Store.Dir(home, "r3"), "state.json")
	os.MkdirAll(filepath.Dir(wd.file), 0o700)
	wd.touch()
	wd.double = fakeengine.New()
	wd.double.Load(fx.Terms)
	k.Terms = wd.double

	wd.p = newPane(kind, wd.s.Term, k, home, "", func() time.Time { return fx.Now.Add(time.Duration(wd.clock.Load())) })
	wd.p.plainMarks, wd.p.me = false, ""
	wd.p.self, _ = os.Executable()
	wd.p.make = func(in contract.ViewInput) contract.View {
		wd.mu.Lock()
		defer wd.mu.Unlock()
		// A copy of its own: the pane reads what it was given while the test
		// goes on changing the cards and items of its world.
		v := wd.view
		v.Items, v.Sections = slices.Clone(v.Items), slices.Clone(v.Sections)
		for i := range v.Sections {
			v.Sections[i].Cards = slices.Clone(v.Sections[i].Cards)
		}
		v.Fresh, wd.seen, wd.in = contract.Fresh{Checked: in.Checked, Notes: in.Notes}, in.Terms, in
		return v
	}
	prepare(wd)
	done := make(chan bool)
	go func() {
		wd.p.loop()
		close(done)
	}()
	t.Cleanup(func() {
		wd.s.Close()
		<-done
		wd.double.Close()
	})
	wd.shows(map[bool]string{true: "saved as you go", false: "live · "}[kind == settings])
	wd.began = true
	return wd
}

// until looks every few milliseconds whether a thing is so, and gives up
// when it was not for the time of limit. That time is counted as the pane
// counts it: it begins again for as long as a child the pane started has
// still to end. The pane starts its children one after another, and the
// sweep, which it starts by itself every five seconds, is one of them: what a
// test asked for waits behind it, and how long a program takes to start and
// to end is the machine's own affair. A pane whose child does not end is
// given up on after a minute.
func (wd *world) until(limit time.Duration, so func() bool) bool {
	first := time.Now()
	for end := first.Add(limit); !so(); time.Sleep(5 * time.Millisecond) {
		if now := time.Now(); now.Before(end) {
			continue
		} else if now.Sub(first) > time.Minute || !wd.began || wd.unended() == 0 {
			return so()
		} else {
			end = now.Add(limit)
		}
	}
	return true
}

// unended asks the pane's loop how many children it started have still to
// end. A loop that has ended, or gives no answer, has none.
func (wd *world) unended() int {
	n := make(chan int, 1)
	go wd.p.tell(news{do: func() { n <- wd.p.waiting }})
	select {
	case waiting := <-n:
		return waiting
	case <-wd.p.bg.Done():
	case <-time.After(10 * time.Second):
	}
	return 0
}

// touch replaces the record's file, as a command of ours does when it ends:
// a finished file is moved over it. Written in place it would be three
// changes on Windows, cut short, written and closed, each with its notice.
func (wd *world) touch() {
	err := os.WriteFile(wd.file+".tmp", []byte(time.Now().String()), 0o600)
	if err == nil {
		err = wd.sys.Replace(wd.file+".tmp", wd.file)
	}
	if err != nil {
		wd.t.Fatal(err)
	}
}

// change alters the view model the pane is handed next, and wakes the pane.
func (wd *world) change(fn func(v *contract.View)) {
	wd.mu.Lock()
	fn(&wd.view)
	wd.mu.Unlock()
	wd.touch()
}

func (wd *world) within(d time.Duration, text string) bool {
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if _, _, ok := wd.s.Find(text); ok {
			return true
		}
	}
	return false
}

func (wd *world) shows(texts ...string) {
	wd.t.Helper()
	for _, text := range texts {
		if !wd.until(3*time.Second, func() bool { _, _, ok := wd.s.Find(text); return ok }) {
			wd.t.Fatalf("the pane never showed %q; it shows:\n%s", text, wd.screen())
		}
	}
}

func (wd *world) screen() string {
	var rows []string
	for y := range wd.h {
		rows = append(rows, wd.s.Row(y))
	}
	return strings.Join(rows, "\n")
}

// row is the first row that shows the text.
func (wd *world) row(text string) int {
	wd.t.Helper()
	wd.shows(text)
	_, y, _ := wd.s.Find(text)
	return y
}

func (wd *world) last() string { return wd.s.Row(wd.h - 1) }

func (wd *world) resize(w, h int) {
	wd.h = h
	wd.s.Resize(w, h)
}

// still is a pane that draws the evening once and has no loop, so a test can
// read how each cell is drawn.
func still(t *testing.T, kind string, w, h int) (*pane, *contract.View, *time.Time) {
	fx, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	now, view := fx.Now, fx.Views[contract.Human]
	p := newPane(kind, termtest.New(w, h).Term, contract.NewKit(), "", "", func() time.Time { return now })
	p.plainMarks, p.me, p.state, p.make = false, "", &fx.State, func(contract.ViewInput) contract.View { return *view }
	p.draw()
	return p, view, &now
}

// press gives a pane without a loop keys, each long enough after the last
// that the typing guard takes it for a command.
func press(p *pane, keys ...string) {
	for _, key := range keys {
		time.Sleep(burstGap + 20*time.Millisecond)
		p.event(term.Event{Kind: term.KeyPress, Key: key, Rune: []rune(key)[0]})
	}
}

func (p *pane) find(t *testing.T, text string) (x, y int) {
	t.Helper()
	x, y, ok := p.t.Find(text)
	if !ok {
		t.Fatalf("the pane does not show %q", text)
	}
	return x, y
}

// wentTo reports whether the terminals were asked to bring that tab to the front.
func (wd *world) wentTo(tab string) bool {
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if slices.ContainsFunc(wd.double.Calls(), func(c contract.WireCall) bool { return c.Op == contract.OpTabFocus && c.Tab == tab }) {
			return true
		}
	}
	return false
}

func TestTheFleetDrawsTheEvening(t *testing.T) {
	wd := start(t, fleet, 44, 46, nil)
	wd.shows("FLEET  12 agents · 2 need you", "[<] [>] [x]", "5 done today  [show]", "enter go there · f hide · [ ] width")
	if got := wd.s.Row(wd.row("▲ sign-up page")); got != " ▲ sign-up page                    needs you" {
		t.Errorf("the first card's first line is %q", got)
	}
	y := wd.row("▲ sign-up page")
	if got := wd.s.Row(y + 1); got != "   ████████░░░░░░░░░░░  40%    ctx ▮▮▯▯▯ 48%" {
		t.Errorf("its bars are %q", got)
	}
	if got := wd.s.Row(y + 2); got != "   must the old sign-up link keep working?" {
		t.Errorf("its news is %q", got)
	}
	wd.shows("● price list ▲", "to check", "● email sender", "check failed", "ctx unknown", "asking",
		"◌ admin page", "idle 12m", "said 14m ago: half the screens done")
	if !strings.HasPrefix(wd.last(), " live · checked ") {
		t.Errorf("the last line is %q", wd.last())
	}
	// The one order: the cards stand as the view model lists them.
	var order []int
	for _, name := range []string{"sign-up page", "photo upload", "email sender", "price list", "login page", "admin page"} {
		order = append(order, wd.row(name))
	}
	if !slices.IsSorted(order) {
		t.Errorf("the cards stand in rows %v", order)
	}

}

func TestHowTheFleetIsColoured(t *testing.T) {
	p, view, now := still(t, fleet, 44, 46)
	reef := theme.Get("reef")
	fg := func(text string, dx, dy int) term.Colour {
		x, y := p.find(t, text)
		return p.t.At(x+dx, y+dy).Style.Fg
	}
	// The bar of the card whose figure is old is the scheme's building colour
	// half-way to its ground; with no colour it is the lighter block.
	if got, want := fg("◌ admin page", 2, 1), term.RGB(contract.Pale(reef.Colours["building"], reef.Ground)); got != want {
		t.Errorf("the pale bar is drawn in %x, want %x", got, want)
	}
	for text, name := range map[string]string{"◐ login page": "building", "▲ sign-up page": "needs", "● email sender": "failed",
		"● price list": "check", "◌ admin page": "idle"} {
		if got := fg(text, 0, 0); got != term.RGB(reef.Colours[name]) {
			t.Errorf("the mark of %q is drawn in %x, want the scheme's %s", text, got, name)
		}
	}
	if got := fg("◐ login page", 2, 1); got != term.RGB(reef.Colours["building"]) {
		t.Errorf("a fresh bar is drawn in %x", got)
	}
	for text, name := range map[string]string{"▮▮▯▯▯ 48%": "ctx_low", "▮▮▮▯▯ 61%": "ctx_mid", "▮▮▮▮▯ 88%": "ctx_high"} {
		if got := fg(text, 0, 0); got != term.RGB(reef.Colours[name]) {
			t.Errorf("the context bar %q is drawn in %x, want the scheme's %s", text, got, name)
		}
	}
	if st := p.t.At(5, 5).Style; st.Bg != term.RGB(reef.Ground) {
		t.Errorf("the pane's ground is drawn %+v", st)
	}

	// Past ten seconds the cards go dim and the last line warns.
	view.Fresh.Checked = *now
	*now = now.Add(11 * time.Second)
	p.draw()
	if !strings.HasPrefix(p.t.Row(45), " STALE since ") || p.t.At(1, 45).Style.Fg != term.RGB(reef.Colours["blocked"]) {
		t.Errorf("the last line of a stale pane is %q, drawn %+v", p.t.Row(45), p.t.At(1, 45).Style)
	}
	if got := fg("▲ sign-up page", 0, 0); got != term.RGB(reef.Colours["dim"]) {
		t.Errorf("a stale card's mark is drawn in %x", got)
	}

	p.scheme = theme.Get(theme.None)
	p.draw()
	p.find(t, "▓▓▓▓▓▓▓▓▓▓░░░░░░░░░  50%")
	if st := p.t.At(5, 5).Style; st.Fg != 0 || st.Bg != 0 {
		t.Errorf("with no colour a cell is drawn %+v", st)
	}
}

func TestTheFleetNarrowAndScrolled(t *testing.T) {
	wd := start(t, fleet, 34, 20, nil)
	wd.shows("FLEET  12 · 2 need you ↓3", "[<][>]", "5 done [show] · live · ")
	y := wd.row("▲ sign-up page")
	if got := wd.s.Row(y + 1); got != "   ██████░░░░░░░░░  40%    ctx 48%" {
		t.Errorf("a narrow card's bars are %q", got)
	}
	if got := wd.s.Row(y + 2); !strings.HasPrefix(got, " ▲ photo upload") {
		t.Errorf("a narrow card has a third line: %q", got)
	}
	wd.s.Type("\x1b[<65;5;5M\x1b[<65;5;5M")
	wd.shows("↑2 ↓1")
	if _, _, ok := wd.s.Find("sign-up page"); ok {
		t.Error("the wheel did not move the list")
	}
	// The keys bring the selection into view, down to the last card and back.
	for range 12 {
		wd.s.Key("j")
	}
	wd.shows("▌◌ admin page", "ctx   ?", "↑3")
	wd.s.Key("k")
	wd.shows("▌◐ order emails")
	wd.resize(60, 50)
	wd.shows("FLEET  12 agents · 2 need you", "▌◐ order emails", "asked the lead: which sender address?")
}

func TestTheFleetGoesToATab(t *testing.T) {
	wd := start(t, fleet, 44, 46, nil)
	wd.s.Key("j")
	wd.s.Key("j")
	wd.shows("▌▲ photo upload")
	wd.s.Key("enter")
	if !wd.wentTo("w1:t7") {
		t.Fatalf("enter on photo upload did not go to its tab: %+v", wd.double.Calls())
	}
	wd.s.ClickText("login page")
	if !wd.wentTo("w1:t2") {
		t.Fatalf("a click on login page did not go to its tab: %+v", wd.double.Calls())
	}
	wd.shows("▌◐ login page")

}

func TestTheFourFormsAndTheStrip(t *testing.T) {
	wd := start(t, actions, 104, 20, nil)
	wd.change(func(v *contract.View) {
		v.Items = append(v.Items, contract.Item{ID: "n5", Form: "todo", Look: contract.LookAtPrompt, Task: "T7", Name: "email sender",
			Source: "the tool", Since: wd.fx.Now, Text: "email sender is waiting at a permission prompt in its tab",
			Buttons: []contract.Button{{Label: "Done", Key: "y"}, {Label: "Cannot", Key: "n"}, {Label: "Go there", Action: "go"}}})
	})
	wd.shows("ACTIONS  3 waiting · 2 hold work up", "[ Stop all ] [ Catch me up ] [ Do not disturb ] [ Mute ]  [v]",
		"▲ sign-up page · a question from the worker · 3m", "Must the old sign-up link keep working?", "[ Yes ]  [ No ]  [ Other ]",
		"▲ photo upload · a question from the worker · 1m", "● price list · sign-off asked by the lead agent · now",
		"[ Approve ]  [ Send back ]", "◆ email sender · the tool · now", "[ Done ]  [ Cannot ]  [ Go there ]")
	if got := wd.s.Row(wd.row("Which sizes should a photo be kept in?") + 1); got != "     > _" {
		t.Errorf("the line to type on is %q", got)
	}

	// The pane that has the keys says so, and its title is lit.
	if got := wd.last(); !strings.HasPrefix(got, " click an item to answer it · keys from any tab: whaleshark init --keys") || !strings.Contains(got, "live · ") {
		t.Errorf("without the keys the last line is %q", got)
	}
	wd.s.Type("\x1b[I")
	wd.shows("KEYS GO HERE · j k choose · y n o answer", "› ▲ sign-up page")
	// On an item answered in words the line says how: letters are keys until enter.
	wd.key("j")
	wd.shows("› ▲ photo upload", "KEYS GO HERE · enter, your answer, enter · j k choose")
	wd.key("k")
	wd.shows("› ▲ sign-up page", "y n o answer")
	wd.s.Type("\x1b[O")
	wd.shows("click an item to answer it")
	// A click presses the button it is on, of the item it is in.
	wd.click("[ Approve ]")
	wd.ran(`answer n4 --file "approve" --human`)
	wd.shows("› ● price list")
	wd.click("[ Go there ]")
	if !wd.wentTo("w1:t8") {
		t.Fatalf("Go there did not go to the tab: %+v", wd.double.Calls())
	}
	wd.click("[ Mute ]")
	wd.ran("set mute on --human")

	// What the strip says of the four buttons.
	wd.change(func(v *contract.View) {
		v.Strip = contract.Strip{Paused: true, StillRunning: 2, DND: true, Away: wd.fx.Now.Add(-64 * time.Minute)}
	})
	wd.shows("ACTIONS  3 · ▲2 · paused · 2 still running · away 1h 04m", "[Resume] [Catch up] [DND] [Mute] [v]")
	wd.resize(150, 20)
	wd.shows("hold work up · paused · 2 still running · away 1h 04m · press c", "[ Resume ] [ Catch me up ]")
	wd.resize(64, 20)
	wd.shows("ACTIONS  3 · ▲2 · paused  ", "[Resume] [Catch up] [DND] [Mute] [v]")
}

func TestAnItemInsertedAboveTheSelectionDoesNotMoveIt(t *testing.T) {
	wd := start(t, actions, 104, 24, nil)
	wd.s.Type("\x1b[I")
	wd.shows("› ▲ sign-up page")
	wd.s.Key("j")
	wd.s.Key("j")
	wd.shows("› ● price list")
	wd.change(func(v *contract.View) {
		v.Items = append([]contract.Item{{ID: "n9", Form: "choice", Look: contract.LookNeedsYou, Name: "help pages",
			Source: "a question from the worker", Since: wd.fx.Now, Text: "Is the old help text still right?",
			Buttons: []contract.Button{{Label: "Yes", Key: "y"}, {Label: "No", Key: "n"}}}}, v.Items...)
	})
	wd.shows("Is the old help text still right?")
	above, selected := wd.row("help pages"), wd.row("› ● price list")
	if above >= selected {
		t.Fatalf("the new item stands in row %d, the selected one in row %d", above, selected)
	}
	if strings.Count(wd.screen(), "›") != 1 {
		t.Fatalf("more than one item is selected:\n%s", wd.screen())
	}
	// The key acts on the item, not on the row the item used to be in.
	wd.key("y")
	wd.ran(`answer n4 --file "approve" --human`)

	// An item that leaves takes its selection with it; no other inherits it.
	wd.change(func(v *contract.View) { v.Items = v.Items[:3] })
	if !wd.until(3*time.Second, func() bool { return !strings.Contains(wd.screen(), "price list") }) {
		t.Fatal("the item never left")
	}
	wd.key("y")
	wd.shows("KEYS GO HERE")
	time.Sleep(200 * time.Millisecond)
	if rows := wd.screen(); strings.Contains(rows, "›") || len(wd.children("answer")) != 1 {
		t.Fatalf("a key acted with nothing selected: %v\n%s", wd.children("answer"), rows)
	}
}

func TestFoldAndUnfold(t *testing.T) {
	wd := start(t, actions, 104, 20, nil)
	wd.key("x")
	wd.resize(104, 6)
	wd.shows("[^]", "▲ sign-up page · Must the old sign-up link keep working?", "▲ photo upload · Which sizes",
		"● price list · Prices load from the sheet")
	if got := wd.s.Row(0); !strings.HasPrefix(got, " ACTIONS  3 waiting · 2 hold work up") {
		t.Errorf("the folded strip is %q", got)
	}
	// Folded, the strip fills what it is left: two rows at the least.
	wd.resize(104, 2)
	wd.shows("click an item to answer it")
	if rows := wd.screen(); strings.Contains(rows, "sign-up page") || !strings.Contains(wd.s.Row(0), "[ Stop all ]") || !strings.Contains(wd.last(), "live · ") {
		t.Errorf("two rows of a folded pane show:\n%s", rows)
	}
	wd.resize(104, 4)
	wd.shows("▲ sign-up page · Must", "+2 more · j k")

	// Something that holds work up unfolds it; with Do not disturb it stays.
	wd.resize(104, 20)
	wd.shows("[^]", "● price list · Prices")
	holds := contract.Item{ID: "q12", Form: "question", Look: contract.LookNeedsYou, Name: "site map", Source: "a question from the worker",
		Since: wd.fx.Now, Text: "Which pages are left out of the map?", Line: true, Holds: true}
	wd.change(func(v *contract.View) { v.Items = append(v.Items, holds) })
	wd.shows("[v]", "site map · a question from the worker · now", "[ Approve ]")
	wd.click("[v]")
	wd.shows("[^]")
	holds.ID = "q13"
	wd.change(func(v *contract.View) {
		v.Strip.DND = true
		v.Items = append(v.Items, holds)
	})
	// The second item of the kind is drawn. (This waited for a "+", which
	// the last line's own "ctrl+space" held from the start.)
	if !wd.until(3*time.Second, func() bool { return strings.Contains(wd.s.Row(5), "site map") }) {
		t.Fatalf("the second item that holds work up was not drawn:\n%s", wd.s.Row(5))
	}
	if wd.within(time.Second, "[v]") {
		t.Fatal("the pane unfolded itself while it was not to disturb")
	}
	wd.click("▲ photo upload")
	wd.shows("[v]", "› ▲ photo upload")
}

// A pane reads the record when a file of its own changed and at no other
// time, with notices and without: the lock is shared among readers, and one
// that read in a loop would have every command wait for its turn behind it.
func TestAPaneReadsOnlyWhenAFileChanged(t *testing.T) {
	for name, env := range map[string]map[string]string{"notices": nil, "no notices": {contract.EnvNotices: contract.NoticesOff}} {
		t.Run(name, func(t *testing.T) {
			wd := start(t, fleet, 60, 20, env)
			time.Sleep(time.Second) // what starting up reads
			before := wd.reads.Load()
			time.Sleep(3 * time.Second)
			if n := wd.reads.Load() - before; n != 0 {
				t.Fatalf("left alone for three seconds the pane read the record %d times", n)
			}
			wd.touch()
			time.Sleep(3 * time.Second)
			if n := wd.reads.Load() - before; n < 1 || n > 2 {
				t.Fatalf("after one change the pane read the record %d times", n)
			}
		})
	}
}

// The watcher finds a change no notice told of at its next look, within a
// second, gives the notice a quarter of a second more, and the pane says so
// when it next draws the ages, within another second: two and a quarter
// seconds at the most, and the test changes the file at the worst moment,
// just after the pane and its watcher began.
func TestSlowUpdatesAreSaidWithinThreeSeconds(t *testing.T) {
	for _, kind := range []string{fleet, actions} {
		t.Run(kind, func(t *testing.T) {
			wd := start(t, kind, 100, 30, map[string]string{contract.EnvNotices: contract.NoticesOff})
			if strings.Contains(wd.last(), "slow updates") {
				t.Fatalf("the pane says %q before any notice was lost", wd.last())
			}
			wd.touch()
			at := time.Now()
			for !strings.Contains(wd.last(), "slow updates") {
				if time.Since(at) > 3*time.Second {
					t.Fatalf("three seconds after a change without a notice the last line is %q", wd.last())
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
}

// The line is dropped just after the pane began, the worst moment: the
// comparison that finds it is five seconds away, and then takes a snapshot
// and a draw of its own, which the half second on top is for.
func TestALostLineIsSaidWithinFiveSeconds(t *testing.T) {
	for _, kind := range []string{fleet, actions} {
		t.Run(kind, func(t *testing.T) {
			wd := start(t, kind, 100, 30, nil)
			wd.double.Drop(testkit.PushClosed, "w1:p2")
			at := time.Now()
			for !strings.Contains(wd.last(), "terminals: checking every 5 s") {
				if time.Since(at) > 5*time.Second+time.Second/2 {
					t.Fatalf("five seconds after a line was dropped the last line is %q", wd.last())
				}
				time.Sleep(5 * time.Millisecond)
			}
			wd.mu.Lock()
			defer wd.mu.Unlock()
			if slices.ContainsFunc(wd.seen.Panes, func(p contract.Pane) bool { return p.ID == "w1:p2" }) {
				t.Error("the picture still holds the pane that was closed")
			}
		})
	}
}

func TestAnotherFolderOrSessionCountsNoLostLine(t *testing.T) {
	wd := start(t, fleet, 100, 30, nil)
	moved := *wd.fx.Terms
	moved.Panes = slices.Clone(moved.Panes)
	login(&moved).Cwd, login(&moved).Session = "/work/shop/site", "s-other"
	wd.double.Load(&moved)
	took := func() bool {
		wd.mu.Lock()
		defer wd.mu.Unlock()
		return wd.seen != nil && login(wd.seen).Cwd == "/work/shop/site" && login(wd.seen).Session == "s-other"
	}
	for at := time.Now(); !took(); time.Sleep(20 * time.Millisecond) {
		if time.Since(at) > 7*time.Second {
			t.Fatal("the pane never took the new folder and session over")
		}
	}
	if wd.within(time.Second, "terminals: checking") {
		t.Fatalf("a folder and a session alone were counted as a lost line: %q", wd.last())
	}
}

// login is the pane of the login page's agent in a picture.
func login(s *contract.Snapshot) *contract.Pane {
	return &s.Panes[slices.IndexFunc(s.Panes, func(p contract.Pane) bool { return p.ID == "w1:p2" })]
}

// The comparison itself, step by step, with no loop and no clock.
func TestTheComparisonLooksTwiceAtAStatus(t *testing.T) {
	fx, _ := testkit.Load(testkit.Evening)
	gone, stop := context.WithCancel(context.Background())
	stop()
	p := newPane(fleet, termtest.New(40, 10).Term, contract.NewKit(), "", "", func() time.Time { return fx.Now })
	p.bg = gone
	with := func(change func(p *contract.Pane)) *contract.Snapshot {
		s := &contract.Snapshot{Panes: slices.Clone(fx.Terms.Panes)}
		change(login(s))
		return s
	}
	p.picture = with(func(*contract.Pane) {})

	p.compare(with(func(q *contract.Pane) { q.Cwd, q.Session = "/work/shop/site", "s-other" }), 1)
	if p.lost != 0 || login(p.picture).Cwd != "/work/shop/site" || login(p.picture).Session != "s-other" || !p.termsOK.Equal(fx.Now) {
		t.Fatalf("a folder and a session: lost %d, picture %+v", p.lost, login(p.picture))
	}
	p.termsOK = time.Time{}
	idle := with(func(q *contract.Pane) { q.Status = contract.StatusIdle })
	p.compare(idle, 1)
	if p.lost != 0 || login(p.picture).Status != contract.StatusWorking || !p.termsOK.IsZero() {
		t.Fatalf("a status at the first look: lost %d, picture %+v, checked %v", p.lost, login(p.picture), p.termsOK)
	}
	// The line came in the half second between the two looks: nothing was lost.
	login(p.picture).Status = contract.StatusIdle
	p.compare(idle, 2)
	if p.lost != 0 || p.termsOK.IsZero() {
		t.Fatalf("a status line that was only late was counted: lost %d", p.lost)
	}
	p.compare(with(func(q *contract.Pane) { q.Status = contract.StatusBlocked }), 2)
	if p.lost != 1 || login(p.picture).Status != contract.StatusBlocked || !p.lostAt.Equal(fx.Now) {
		t.Fatalf("a status that still differs at the second look: lost %d, picture %+v", p.lost, login(p.picture))
	}
	p.compare(with(func(q *contract.Pane) { q.Label = "another name" }), 1)
	if p.lost != 2 || login(p.picture).Label != "another name" {
		t.Fatalf("anything else at the first look: lost %d", p.lost)
	}
}

func TestTheAgeGrowsAndThenSaysStale(t *testing.T) {
	wd := start(t, fleet, 44, 46, nil)
	wd.double.Close()
	wd.clock.Store(int64(7 * time.Second))
	wd.shows("live · checked 7s ago")
	wd.clock.Store(int64(11 * time.Second))
	wd.shows("STALE since ")
}

// Pointer movement is reported for every cell: the screen is drawn again only
// when what is lit changes. A lit button is dark on the accent colour, and
// reversed where there is no colour; so are the title of the pane that has
// the keys and a switch that is on.
func TestWhatIsLit(t *testing.T) {
	p, view, _ := still(t, actions, 150, 20)
	reef := theme.Get("reef")
	lit := term.Style{Fg: term.RGB(reef.Ground), Bg: term.RGB(reef.Colours["accent"]), Bold: true}
	x, y := p.find(t, "[ Yes ]")
	move := func(x, y int) bool {
		p.event(term.Event{Kind: term.Mouse, Action: term.Move, X: x, Y: y})
		dirty := p.dirty
		if dirty {
			p.draw()
		}
		return dirty
	}
	for _, step := range []struct {
		x, y int
		want bool
	}{{40, 10, false}, {41, 10, false}, {x, y, true}, {x + 3, y, false}, {x + 12, y, true}, {x + 13, y, false}, {60, 12, true}, {61, 12, false}} {
		if got := move(step.x, step.y); got != step.want {
			t.Errorf("the pointer at %d,%d: drawn again %v, want %v", step.x, step.y, got, step.want)
		}
	}
	move(x+2, y)
	if st := p.t.At(x+2, y).Style; st != lit {
		t.Errorf("the button under the pointer is drawn %+v", st)
	}
	if st := p.t.At(x+12, y).Style; st.Bg != term.RGB(reef.Ground) {
		t.Errorf("the button beside it is drawn %+v", st)
	}

	if st := p.t.At(1, 0).Style; st.Bg != term.RGB(reef.Ground) {
		t.Errorf("the title of a pane without the keys is drawn %+v", st)
	}
	p.event(term.Event{Kind: term.Focus, Focused: true})
	view.Strip.DND, p.stale = true, true
	p.draw()
	if st := p.t.At(1, 0).Style; st != lit {
		t.Errorf("the title of the pane that has the keys is drawn %+v", st)
	}
	dnd, _ := p.find(t, "[ Do not disturb ]")
	mute, _ := p.find(t, "[ Mute ]")
	if st := p.t.At(dnd, 0).Style; st.Bg != term.RGB(reef.Colours["blocked"]) {
		t.Errorf("Do not disturb, switched on, is drawn %+v", st)
	}
	if st := p.t.At(mute, 0).Style; st.Bg != term.RGB(reef.Ground) {
		t.Errorf("Mute, switched off, is drawn %+v", st)
	}

	p.scheme = theme.Get(theme.None)
	p.draw()
	for name, at := range map[string]int{"the lit button": x + 2, "the title": 1, "the switch that is on": dnd} {
		row := map[string]int{"the lit button": y}[name]
		if st := p.t.At(at, row).Style; !st.Reverse {
			t.Errorf("with no colour %s is drawn %+v", name, st)
		}
	}
	if st := p.t.At(mute, 0).Style; st.Reverse {
		t.Errorf("with no colour a switch that is off is drawn %+v", st)
	}
}

// several is a project with more than one run, each with its own record.
type several struct {
	reader
	runs    map[string]*contract.State
	current string
}

func (r several) Runs(string) ([]string, error) { return slices.Sorted(maps.Keys(r.runs)), nil }
func (r several) Current(string) (string, error) {
	return r.current, nil
}
func (r several) Read(_, run string) (*contract.State, error) {
	r.reads.Add(1)
	return r.runs[run], nil
}

// A pane that was started with no run named shows the run of the lead agent
// in its own tab, the newest, whichever run the person's commands go to; so
// a pane opened beside a lead agent shows its run, parked or not, and its
// next. With no lead agent in its tab it follows the person's pointer. It
// looks the runs through only when told that something changed.
func TestAPaneShowsTheRunOfItsTabsLeadAgent(t *testing.T) {
	led := func(pane string, closed bool) *contract.State {
		s := &contract.State{Run: contract.Run{Orchestrator: &contract.Binding{Pane: pane}}}
		if closed {
			s.Run.ClosedAt = time.Unix(1, 0)
		}
		return s
	}
	var reads atomic.Int32
	r := several{reader{reads: &reads}, map[string]*contract.State{"r1": led("old", false), "r2": led("lead", false), "r3": led("lead", true)}, "r1"}
	k := contract.NewKit()
	k.Store = r
	p := newPane(fleet, nil, k, "/project", "", time.Now)
	p.me = "mine"
	shows := func(want string) {
		t.Helper()
		if got, err := p.showing(r); err != nil || got != want {
			t.Fatalf("the pane shows %q, %v; want %q", got, err, want)
		}
		p.run = want
	}
	shows("r1") // no picture yet: the person's pointer
	p.picture = &contract.Snapshot{Panes: []contract.Pane{{ID: "old", Tab: "t1"}, {ID: "lead", Tab: "t2"}, {ID: "mine", Tab: "t2"}}}
	shows("r2") // the open run led from its tab, not the closed one and not the pointer's
	before := reads.Load()
	r.runs["r4"] = led("lead", false)
	shows("r2") // nothing said that anything changed: no record is read
	if reads.Load() != before {
		t.Fatal("the pane read the records with nothing changed")
	}
	p.seek = true
	shows("r4") // the lead agent's next run
	p.me, p.seek = "elsewhere", true
	shows("r1") // a tab with no lead agent: the pointer
	// A run that was named is held to.
	if q := newPane(fleet, nil, k, "/project", "r1", time.Now); q.anyRun {
		t.Fatal("a pane started with a run named would leave it")
	}
}

// The action pane names the answer shortcut only where the shortcuts are
// set: until `init --keys` has written them, the key it would name does
// nothing, and the line says what does.
func TestTheAnswerShortcutIsNamedOnlyWhereItIsSet(t *testing.T) {
	wd := startWith(t, actions, 104, 20, nil, func(w *world) {
		d, err := w.sys.Dirs()
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(d.Config, 0o700); err == nil {
			err = os.WriteFile(contract.SettingsPath(d), []byte("[[shortcut]]\nkey = \"a\"\ntype = \"shell\"\nargv = [\"whaleshark\", \"ui\", \"answer\"]\n"), 0o600)
		}
		if err != nil {
			t.Fatal(err)
		}
	})
	wd.shows("ctrl+space a to answer · / all actions · ? keys")
}
