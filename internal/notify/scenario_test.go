package notify

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/rules"
	"github.com/tgdigital-hub/whaleshark/internal/view"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func TestMain(m *testing.M) { scenario.Main(m) }

// evening is the fixture's project with a nudger over the fake herdr, whose
// pop-up is switched on unless a test says otherwise.
type evening struct {
	*scenario.Project
	t     *testing.T
	n     Nudger
	run   string
	now   time.Time
	dirs  contract.Dirs
	after []string // the titles that went on to the channel after the pop-up
}

// prepare loads the evening, lets change alter it, and takes the shown stamp
// off the items named, so that they are new to the person.
func prepare(t *testing.T, change func(*testkit.Fixture), unshown ...string) *evening {
	t.Helper()
	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range unshown {
		f.State.Questions[id].ShownAt = time.Time{}
	}
	if change != nil {
		change(f)
	}
	e := &evening{Project: scenario.Prepare(t, f, nil), t: t, run: f.State.Run.ID, now: f.Now}
	rules.Plug(e.Kit)
	view.Plug(e.Kit)
	e.n = Nudger{k: e.Kit, next: []func(string, string, bool) bool{func(title, _ string, _ bool) bool {
		e.after = append(e.after, title)
		return true
	}}}
	if e.dirs, err = e.Kit.Platform.Dirs(); err != nil {
		t.Fatal(err)
	}
	e.popup("herdr")
	return e
}

// popup writes herdr's own pop-up setting.
func (e *evening) popup(delivery string) {
	e.write(e.Herdr.Settings, "[ui.toast]\ndelivery = \""+delivery+"\"\n")
}

func (e *evening) write(path, text string) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

// pass moves the clock and runs the pass, as the next sweep would.
func (e *evening) pass(later time.Duration) {
	e.t.Helper()
	e.Clock(later)
	e.now = e.now.Add(later)
	if err := e.n.Items(e.Root, e.run, nil, e.now); err != nil {
		e.t.Fatal(err)
	}
}

// popups are the notifications herdr was asked for: title, body and sound.
func (e *evening) popups() [][3]string {
	var out [][3]string
	for _, c := range e.Herdr.Calls() {
		if len(c) == 7 && c[0] == "notification" {
			out = append(out, [3]string{c[2], c[4], c[6]})
		}
	}
	return out
}

func (e *evening) want(titles ...string) {
	e.t.Helper()
	var got []string
	for _, p := range e.popups() {
		got = append(got, p[0])
	}
	if !slices.Equal(got, titles) {
		e.t.Fatalf("pop-ups %q, want %q", got, titles)
	}
}

func (e *evening) shown(id string) bool {
	s, _ := e.Record()
	return !s.Questions[id].ShownAt.IsZero()
}

func (e *evening) ui() (ui contract.UIFile) {
	e.t.Helper()
	file := filepath.Join(e.dirs.State, "ui.json")
	if err := contract.ReadVersioned(e.Kit.Platform.Read, file, contract.FileVersion, &ui); err != nil {
		e.t.Fatal(err)
	}
	return ui
}

// seen is the terminals with a look at the record at the moment of a pop-up.
type seen struct {
	contract.Terminals
	e      *evening
	before []bool
}

func (s *seen) Notify(title, body string, sound bool) (string, string, error) {
	s.before = append(s.before, s.e.shown("q7"))
	return s.Terminals.Notify(title, body, sound)
}

func TestItemBeforeNudge(t *testing.T) {
	e := prepare(t, nil, "q7")
	spy := &seen{Terminals: e.Kit.Terms, e: e}
	e.Kit.Terms = spy
	e.pass(0)
	if !slices.Equal(spy.before, []bool{true}) {
		t.Fatalf("the item was in the record before its nudge: %v, want one true", spy.before)
	}
	want := [3]string{"sign-up page — needs you", "Must the old sign-up link keep working?", "request"}
	if got := e.popups(); len(got) != 1 || got[0] != want {
		t.Fatalf("pop-ups %q, want %q", got, want)
	}
	e.pass(5 * time.Second)
	e.want("sign-up page — needs you")
	if len(e.after) != 0 {
		t.Fatalf("a pop-up that was shown went on to the next channel: %q", e.after)
	}
}

func TestNextChannel(t *testing.T) {
	for _, c := range []struct {
		name, delivery string
		attached, on   bool
		popups, after  int
	}{
		{"off and herdr answers shown", "off", true, true, 1, 1},
		{"on and attached", "herdr", true, true, 1, 0},
		{"on and no screen", "herdr", false, true, 1, 1},
		{"our own switch off", "herdr", true, false, 0, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := prepare(t, nil, "q7")
			e.popup(c.delivery)
			if e.Herdr.Attached = c.attached; !c.on {
				e.write(filepath.Join(e.dirs.Config, "config.toml"), "[nudge]\npopup = false\n")
			}
			if reason, _, err := e.Herdr.Notify("a", "b", false); c.attached && (err != nil || reason != contract.NotifyShown) {
				t.Fatalf("the fake herdr answered %q, %v; the scenario needs %q", reason, err, contract.NotifyShown)
			}
			e.pass(0)
			if got := len(e.popups()) - 1; got != c.popups || len(e.after) != c.after {
				t.Fatalf("%d pop-ups and %d on the next channel, want %d and %d", got, len(e.after), c.popups, c.after)
			}
		})
	}
}

func TestThirtySeconds(t *testing.T) {
	e := prepare(t, nil)
	nudge := func(task string) {
		t.Helper()
		if err := e.n.Nudge(contract.Nudge{Title: task, Task: task}); err != nil {
			t.Fatal(err)
		}
	}
	nudge("T7")
	nudge("T7")
	nudge("T8")
	e.Clock(29 * time.Second)
	nudge("T7")
	e.want("T7", "T8")
	e.Clock(time.Second)
	nudge("T7")
	e.want("T7", "T8", "T7")
}

func TestFocusedTab(t *testing.T) {
	e := prepare(t, nil, "q7", "q9")
	e.Herdr.Push(testkit.PushFocused, "w1:p3")
	e.pass(0)
	e.want("photo upload — needs you")
	if !e.shown("q7") {
		t.Fatal("the item of the tab in front was not stamped as shown")
	}
}

func TestDoNotDisturb(t *testing.T) {
	e := prepare(t, func(f *testkit.Fixture) {
		f.UI.DND, f.UI.DNDUntil = true, f.Now.Add(time.Hour)
		f.State.Questions["n4"].Urgent = true
	}, "q7", "q9", "n4")
	e.pass(0)
	e.want("price list — needs you")
	if e.shown("q7") || e.shown("q9") {
		t.Fatal("an item held back was stamped as shown")
	}
	for _, urgent := range []bool{false, true} {
		if err := e.n.Nudge(contract.Nudge{Title: "herdr is not reachable", Urgent: urgent}); err != nil {
			t.Fatal(err)
		}
	}
	e.want("price list — needs you", "herdr is not reachable")

	// The end time passes while nothing runs; the next pass applies it. By
	// then a worker's question has waited for the lead agent long enough to
	// be the person's too, and was held with the others.
	e.pass(2 * time.Hour)
	if ui := e.ui(); ui.DND || !ui.DNDUntil.IsZero() || ui.FleetPane == "" {
		t.Fatalf("ui.json after the end time: %+v", ui)
	}
	if got := e.popups(); len(got) != 3 || got[2] != [3]string{"3 things waited while you were not to be disturbed", "order emails, sign-up page, photo upload", "request"} {
		t.Fatalf("pop-ups %q", got)
	}
	if !e.shown("q7") || !e.shown("q9") || !e.shown("q10") {
		t.Fatal("what was held is not stamped as shown")
	}
	e.pass(5 * time.Second)
	if len(e.popups()) != 3 {
		t.Fatalf("a second nudge for what was held: %q", e.popups())
	}
}

func TestSwitchedOffByHand(t *testing.T) {
	e := prepare(t, func(f *testkit.Fixture) { f.UI.DND = true }, "q7", "q9")
	e.pass(time.Hour)
	e.want()
	ui := e.ui()
	ui.DND = false
	if err := contract.WriteVersioned(e.Kit.Platform, filepath.Join(e.dirs.State, "ui.json"), contract.FileVersion, ui); err != nil {
		t.Fatal(err)
	}
	e.pass(time.Second)
	e.want("3 things wait for you")
}

func TestSound(t *testing.T) {
	for name, c := range map[string]struct {
		mute         bool
		config, want string
	}{
		"as it comes":      {false, "", "request"},
		"mute":             {true, "", "none"},
		"sound switch off": {false, "[nudge]\nsound = false\n", "none"},
	} {
		t.Run(name, func(t *testing.T) {
			e := prepare(t, func(f *testkit.Fixture) { f.UI.Mute = c.mute }, "q7")
			if c.config != "" {
				e.write(filepath.Join(e.dirs.Config, "config.toml"), c.config)
			}
			e.pass(0)
			if got := e.popups(); len(got) != 1 || got[0][2] != c.want {
				t.Fatalf("pop-ups %q, want one with the sound %q", got, c.want)
			}
		})
	}
}

func TestPlug(t *testing.T) {
	k := contract.NewKit()
	Plug(k)
	if _, ok := k.Notifier.(Nudger); !ok {
		t.Fatal("the nudger is not the kit's notifier")
	}
}
