package panes

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/term/termtest"
	"github.com/tgdigital-hub/whaleshark/internal/theme"
	"github.com/tgdigital-hub/whaleshark/internal/view"
)

// noticed stands for file notices that work: a pane without them says so on its age line.
type noticed struct{ contract.Watcher }

func (noticed) Slow() bool { return false }

// bar is the left section without a loop: the morning of three fleets as the
// real builder makes it of the fixture, drawn once at one size, two seconds
// after everything was last checked.
func bar(t *testing.T, w, h int) *pane {
	f := testkit.Fleets()
	k := contract.NewKit()
	k.Side, k.Platform = view.Side, system(t.TempDir(), nil)
	p := newPane(side, termtest.New(w, h).Term, k, "", "", func() time.Time { return f.In.Now.Add(2 * time.Second) })
	p.plainMarks, p.me, p.in, p.picture, p.cfg, p.shut = false, "", f.In, f.In.Terms, f.In.Person, f.In.Folded
	p.filesOK, p.termsOK, p.watch = f.In.Now, f.In.Now, noticed{}
	p.ui.FirstSteps = contract.Off // a login whose first steps are long done
	p.draw()
	return p
}

// screenOf is the settings screen without a loop, on a login that has set
// nothing, with the section and the row of the design's pictures in front.
func screenOf(t *testing.T, w, h int) *pane {
	k := contract.NewKit()
	k.Platform = system(t.TempDir(), map[string]string{"PATH": ""})
	p := newPane(settings, termtest.New(w, h).Term, k, "", "", time.Now)
	p.plainMarks, p.me = false, ""
	p.load()
	p.sect, p.saw, p.pick = 5, 5, 1
	p.draw()
	return p
}

// fleetOf is the fleet pane without a loop: the evening of twelve workers, or
// with fleets the morning of three as the real builder makes it of the fixture.
func fleetOf(t *testing.T, w, h int, fleets bool, place string) *pane {
	p, _, _ := still(t, fleet, w, h)
	if f := testkit.Fleets(); fleets {
		v := *view.Build(f.View)
		p.now, p.make = func() time.Time { return f.View.Now.Add(2 * time.Second) }, func(contract.ViewInput) contract.View { return v }
	}
	p.cfg.Panes.Fleet, p.watch, p.stale = place, noticed{}, true
	p.filesOK, p.termsOK = p.now().Add(-2*time.Second), p.now().Add(-2*time.Second)
	p.draw()
	return p
}

// grid is a pane's rows as text, each without the spaces it ends in.
func grid(p *pane) string {
	var rows []string
	for y := range p.t.H {
		rows = append(rows, strings.TrimRight(p.t.Row(y), " "))
	}
	return strings.Join(rows, "\n")
}

// shot saves a screen as plain text for somebody to read, where SHOTS names
// a folder: go test -run Pictures ./internal/panes with SHOTS set.
func shot(name string, p *pane) {
	if dir := os.Getenv("SHOTS"); dir != "" {
		os.WriteFile(filepath.Join(dir, name+".txt"), []byte(grid(p)+"\n"), 0o600)
	}
}

// pictures are the left section and the settings screen as the design draws
// them for a window of 200, of 120 and of 80 columns, and the rail of a
// window under 72: the left section 32, 22, 14 and 4 cells wide with the
// morning of three fleets, the settings screen at the sizes of its three
// pictures with Fleet Mode in front. The words are the contract's and the
// rows the builder's; the cells are the pictures'.
var pictures = []struct {
	kind string
	w, h int
	want string
}{
	{side, 32, 23, `
 WHALESHARK                  ▲1

 WORKSPACES                 [+]
 ▌▲ shop-site  main          19
  ◐ blog  drafts              3
  · other tabs                2

 AGENTS · shop-site
  ● orchestrator
  ▾ billing           codex 5/8
    ▲ refund flow     needs you
    ● invoice pdf      to check
    ◐ tax rounding     building
    ◐ card retries     building
    ◐ receipt mail     building
  ▾ search           claude 6/8
  ▌ ◐ search box       building
    ◐ search results   building
    ◐ filters          building
    · 3 more
  ▸ help pages       hermes 4/8
 [ Menu ]  [ Settings ]
 live · checked 2s ago
`},
	{side, 22, 22, `
 WHALESHARK        ▲1

 WORKSPACES
 ▌▲ shop-site      19
  ◐ blog            3
  · other tabs      2

 AGENTS · shop-site
  ● orchestrator
  ▾ billing       5/8
    ▲ refund flow
    ● invoice pdf
    ◐ tax rounding
    ◐ card retries
    ◐ receipt mail
  ▾ search        6/8
  ▌ ◐ search box
    ◐ search results
    · 4 more
  ▸ help pages    4/8
 [Menu] [Settings]
 live · 2s
`},
	{side, 14, 20, `
 WHALESHAR ▲1
 WORKSPACES
 ▌▲ shop-… 19
  ◐ blog    3
  · other … 2
 AGENTS
  ● orchestr…
  ▾ billing
   ▲ refund …
   ● invoice…
   ◐ tax rou…
   ◐ card re…
   ◐ receipt…
  ▾ search
 ▌ ◐ search …
   ◐ search …
   · 4 more
  ▸ help pag…
 [≡] [Set]
 live · 2s
`},
	{side, 4, 14, `
 ▲1
▌▲19
 ◐3
 ·2

 ●
 ▸
 ▲
 ▾
▌◐
 ·
 ▸
[≡]
 2s
`},
	{settings, 180, 18, `
┌─ settings ───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
│                      │                                                                                                    │                                                      │
│   Look               │ FLEET MODE                                                                 yours, on this computer │ Fleet leaders at once                                │
│   Layout             │                                                                                                    │                                                      │
│   Nudges             │   Fleet Mode                                                                       ( off ) [ on ]  │ How many fleets may run side by side. Takes hold at  │
│   Keys               │ › Fleet leaders at once                                            [ - ]  4  [ + ]  [ unlimited ]  │ the next start; nothing running is stopped.          │
│   Scrolling & copy   │   Agents in one fleet                                              [ - ]  8  [ + ]  [ unlimited ]  │                                                      │
│ ▌ Fleet Mode         │   Agents in all                                                    [ - ] 20  [ + ]  [ unlimited ]  │ It came as 4.                                        │
│   Guardrails         │   A leader's tool                                                                    [ claude ▾ ]  │                                                      │
│   Agent tools        │   A worker's tool                                                              [ the leader's ▾ ]  │                                                      │
│   This project       │                                                                                                    │                                                      │
│   Page & phone       │   Agents running now: 0 of 20                                                                      │                                                      │
│   This server        │   [ Put this section back as it came ]                                                             │                                                      │
│                      │                                                                                                    │                                                      │
│                      │                                                                                                    │                                                      │
│                      │                                                                                                    │                                                      │
│ / find a setting · j k choose · ← → or a click change · enter type a value · u undo · esc close                                                                  saved as you go │
└──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
`},
	{settings, 108, 15, `
┌─ settings ───────────────────────────────────────────────────────────────────────────────────────────────┐
│                    │                                                                                     │
│   Look             │ FLEET MODE                                                  yours, on this computer │
│   Layout           │                                                                                     │
│   Nudges           │   Fleet Mode                                                        ( off ) [ on ]  │
│   Keys             │ › Fleet leaders at once                             [ - ]  4  [ + ]  [ unlimited ]  │
│   Scrolling & copy │   Agents in one fleet                               [ - ]  8  [ + ]  [ unlimited ]  │
│ ▌ Fleet Mode       │   Agents in all                                     [ - ] 20  [ + ]  [ unlimited ]  │
│   Guardrails       │   A leader's tool                                                     [ claude ▾ ]  │
│   Agent tools      │   A worker's tool                                               [ the leader's ▾ ]  │
│   This project     │ ─────────────────────────────────────────────────────────────────────────────────── │
│   Page & phone     │ How many fleets may run side by side. Takes hold at the next start; nothing running │
│   This server      │ is stopped.                                                                         │
│ / find a setting · j k choose · ← → or a click change · enter type a value · u undo · e… saved as you go │
└──────────────────────────────────────────────────────────────────────────────────────────────────────────┘
`},
	{settings, 76, 13, `
┌─ settings ───────────────────────────────────────────────────────────────┐
│ ‹ Scrolling & copy    FLEET MODE    Guardrails ›   (6 of 11)             │
│                                                                          │
│   Fleet Mode                                                     ‹ off › │
│ › Fleet leaders at once                   [ - ]  4  [ + ]  [ unlimited ] │
│   Agents in one fleet                                              ‹ 8 › │
│   Agents in all                                                   ‹ 20 › │
│   A leader's tool                                             ‹ claude › │
│ ──────────────────────────────────────────────────────────────────────── │
│ How many fleets may run side by side. Takes hold at the next start;      │
│ nothing running is stopped.                                              │
│ / find · j k · ← → change · u undo · esc close           saved as you go │
└──────────────────────────────────────────────────────────────────────────┘
`},
}

func TestTheNewScreensAreThePicturesInEverySchemeAndInNone(t *testing.T) {
	for _, pic := range pictures {
		p := map[string]func(*testing.T, int, int) *pane{side: bar, settings: screenOf}[pic.kind](t, pic.w, pic.h)
		name, want := fmt.Sprintf("%s-%dx%d", pic.kind, pic.w, pic.h), strings.Trim(pic.want, "\n")
		shot(name, p)
		for _, s := range theme.Schemes {
			p.scheme = s
			if p.draw(); grid(p) != want {
				t.Errorf("%s in the scheme %s is\n%s\nwant\n%s", name, s.Name, grid(p), want)
			}
			// With no scheme no cell names a colour: bold, dim and reverse say it all.
			for y := 0; s.Name == theme.None && y < pic.h; y++ {
				for x := range pic.w {
					if st := p.t.At(x, y).Style; st.Fg != (term.Style{}).Fg || st.Bg != (term.Style{}).Bg {
						t.Fatalf("%s with no colour: the cell %d,%d has one: %+v", name, x, y, st)
					}
				}
			}
		}
		// A terminal that cannot show the marks gets none of them.
		p.plainMarks = true
		if p.draw(); strings.ContainsAny(grid(p), "▌▲◐●◌◆▾▸…≡‹›┌─│└┘▮") {
			t.Errorf("%s with plain marks still draws one:\n%s", name, grid(p))
		}
		shot(name+"-plain", p)
	}
}

// The fleet where it is no pane beside the conversation, and a fleet's card:
// the strip of one line with the agents by their marks, the cards in columns
// at the bottom, and for an orchestrator one card a fleet.
func TestTheFleetAsAStripAtTheBottomAndWithFleets(t *testing.T) {
	shows := func(name string, p *pane, texts ...string) {
		t.Helper()
		shot(name, p)
		for _, text := range texts {
			if _, _, ok := p.t.Find(text); !ok {
				t.Errorf("%s does not show %q:\n%s", name, text, frame(p))
			}
		}
	}
	shows("fleet-strip-65", fleetOf(t, 65, 2, false, contract.Right), "FLEET  12 agents · ▲2 · ●2 · ◐7 · ◌1                   [ open ]", "live · 0s")
	shows("fleets-strip-65", fleetOf(t, 65, 2, true, contract.Right), "FLEETS  3 · 18 agents · ▲1 · ◐2                        [ open ]")
	low := fleetOf(t, 97, 9, false, contract.Bottom)
	shows("fleet-bottom-97", low, "FLEET  12 agents · 2 need you · page 1 of 2                          live · checked 0s ago  [x]",
		"▲ sign-up page            needs you   ▲ photo upload            needs you",
		"● email sender         check failed   ● price list ▲             to check", "ctx 48%     █████████░░░░░░░░  55%    ctx 61%")
	// The keys go through every card, page by page.
	low.focused = true
	for range 9 {
		low.event(term.Event{Kind: term.KeyPress, Key: "down"})
	}
	low.draw()
	shows("fleet-bottom-97-page-2", low, "page 2 of 2", "site map")
	three := fleetOf(t, 46, 20, true, contract.Right)
	shows("fleets-46", three, "FLEETS  3 · 18 agents · 1 needs…  [<] [>] [x]", "▲ billing · codex                   needs you", "6 agents", // five workers and their leader: the cards sum to the header's 18
		"refund to the card, or to store credit?", "◐ search · claude                    building", "7 agents", "0 of 6 done", "◐ help pages · hermes                building",
		"0 fleets finished today  [show]", "live · checked 2s ago")
	shows("fleets-36", fleetOf(t, 36, 20, true, contract.Right), "FLEETS  3 · 18 agents…  [<] [>] [x]", "0 of 4 done")
	shows("fleet-36", fleetOf(t, 36, 30, false, contract.Right), "FLEET  12 agents · 2 …  [<] [>] [x]")
	shows("fleets-bottom-97", fleetOf(t, 97, 9, true, contract.Bottom), "FLEETS  3 · 18 agents · 1 needs you", "▲ billing · codex         needs you   ◐ search · claude          building")
	// The mark of plain marks is the setting's too, where the variable says nothing.
	three.cfg.UI.Marks = "plain"
	if three.draw(); strings.ContainsAny(grid(three), "▲◐█") != (os.Getenv("WHALESHARK_MARKS") != "") {
		t.Errorf("ui.marks plain draws\n%s", frame(three))
	}
}

// listed is the fixture's record for the left section: one open run.
type listed struct{ reader }

func (l listed) Runs(root string) ([]string, error) {
	return map[bool][]string{true: {"r3"}}[root == l.dir], nil
}

// sideWorld is the left section in its loop: the evening's run in a project
// of the login's list, the real builder, and tweak for what a test changes
// in the model before it is drawn.
func sideWorld(t *testing.T, w, h int) (*world, func(func(*contract.Side))) {
	var mu sync.Mutex
	tweak := func(*contract.Side) {}
	wd := startWith(t, side, w, h, nil, func(wd *world) {
		dirs, _ := wd.sys.Dirs()
		if err := contract.WriteProjects(wd.sys, dirs.State, []string{wd.home, filepath.Join(wd.home, "elsewhere")}); err != nil {
			t.Fatal(err)
		}
		k := wd.p.k
		k.Store = listed{k.Store.(reader)}
		k.Side = func(in contract.SideInput) *contract.Side {
			mu.Lock()
			defer mu.Unlock()
			s := view.Side(in)
			tweak(s)
			return s
		}
	})
	return wd, func(fn func(*contract.Side)) {
		mu.Lock()
		tweak = fn
		mu.Unlock()
		wd.touch()
	}
}

// The timing scenario with the bar open: a change of the record is on the
// left section within 200 ms.
func TestTheBarShowsAChangeWithin200ms(t *testing.T) {
	wd, change := sideWorld(t, 32, 40)
	wd.shows("AGENTS · ", "sign-up page")
	late := 0
	for i := range 20 {
		name, at := fmt.Sprint("step ", i), time.Now()
		change(func(s *contract.Side) { s.Rows[1].Name = name })
		if !wd.within(200*time.Millisecond-time.Since(at), name) {
			late++
			wd.shows(name)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if late > 1 {
		t.Errorf("%d of 20 changes took longer than 200 ms to be on the bar", late)
	}
}

// The click guard on the bar: a row that moved by itself under the pointer
// is not acted on; the click after it is.
func TestAClickOnARowOfTheBarThatMovedIsRefused(t *testing.T) {
	wd, change := sideWorld(t, 32, 40)
	x, y, _ := wd.s.Find("photo upload")
	wd.settle()
	change(func(s *contract.Side) {
		s.Rows = append([]contract.SideRow{{Kind: contract.RowAgent, Look: contract.LookNeedsYou, Name: "came first", Tab: "t-new"}}, s.Rows...)
	})
	wd.shows("came first")
	wd.s.Click(x, y)
	wd.shows(movedSaid)
	if wd.settle(); slices.ContainsFunc(wd.double.Calls(), func(c contract.WireCall) bool { return c.Op == contract.OpTabFocus }) {
		t.Fatalf("the refused click went to a tab: %+v", wd.double.Calls())
	}
	wd.s.Click(x, y)
	wd.asked("tab-focus")
}

// The left section's clicks and keys: a row goes to its tab, a mark opens
// what can be done with it, in the row's own run; the foot's buttons lay the
// list of every action and the settings screen over the tab; typed letters
// narrow the rows; esc gives the keys back.
func TestTheBarsClicksAndKeys(t *testing.T) {
	wd, change := sideWorld(t, 32, 40)
	wd.shows(" WHALESHARK ", "▲2", "[ Menu ]  [ Settings ]", "[+]")
	wd.click("photo upload")
	wd.asked("tab-focus")
	wd.click("[ Settings ]")
	// On the job of the tab that shows: its log is what the screen exports.
	wd.asked("ui run settings --root " + wd.p.root + " --run r3")
	wd.click("[ Menu ]")
	wd.asked("ui menu")
	// [+] lists the projects of the login that no workspace shows.
	wd.click("[+]")
	wd.over("open a project")
	wd.shows("elsewhere")
	wd.s.Key("enter")
	wd.asked("tab-create")
	// A fleet's row: its mark folds it, its right side offers what can be
	// done, and that goes to the run above the fleet's own.
	change(func(s *contract.Side) {
		s.Rows = []contract.SideRow{{Kind: contract.RowLead, Name: "orchestrator", Root: wd.home, Run: "r1", Tab: "t1"},
			{Kind: contract.RowFleet, Look: contract.LookBuilding, Name: "billing", Tool: "codex", Agents: 5, Limit: 8, Open: !slices.Contains(wd.p.shut, "r1/billing"),
				Root: wd.home, Run: "r2", Task: "G1", Actions: []string{"park", "note-leader"}},
			{Kind: contract.RowAgent, Depth: 1, Look: contract.LookNeedsYou, Name: "refund flow", Root: wd.home, Run: "r2", Task: "T1", Tab: "t9",
				Item: "q1", ItemRun: "r1", Actions: []string{"answer", "stop", "note"}}}
	})
	wd.shows("▾ billing           codex 5/8", "▲ refund flow     needs you")
	wd.click("codex 5/8")
	wd.over("billing")
	wd.shows("Park the fleet", "Send its leader a note")
	wd.s.Key("enter")
	wd.shows("Park the fleet? press it again")
	wd.s.Key("enter")
	if got := wd.ran("stop G1"); !strings.Contains(got, "--human") {
		t.Errorf("park ran %q", got)
	}
	if log, _ := os.ReadFile(filepath.Join(wd.home, "children.log")); !strings.Contains(string(log), "stop G1 --human") {
		t.Errorf("the children were:\n%s", log)
	}
	wd.click("▾")
	wd.shows("▸ billing")
	if got := wd.ran("set shut "); !strings.HasPrefix(got, "set shut r1/billing --human") {
		t.Errorf("a folded fleet is kept by %q", got)
	}
	// The keys: down and up choose, a point opens what a row offers, and an
	// answer goes to the run its question climbed to.
	wd.s.Key("down")
	wd.s.Key("down")
	wd.shows("KEYS GO HERE")
	wd.s.Key("tab")
	wd.s.Key(".")
	wd.over("refund flow")
	wd.s.Key("enter")
	wd.shows("Answer:")
	wd.s.Type("card")
	wd.s.Key("enter")
	wd.ran(`answer q1 --file "card" --human`)
	if p := wd.p; p.at.ItemRun != "r1" || !slices.Contains(p.cmd("answer", "q1"), "r1") || !slices.Contains(p.cmd("stop", "T1"), "r2") {
		t.Errorf("the row's commands go to %v and %v", p.cmd("answer", "q1"), p.cmd("stop", "T1"))
	}
	wd.s.Type("refu")
	wd.shows("refu · esc back")
	if in := wd.p.query.String(); in != "refu" {
		t.Errorf("what was typed to narrow the rows is %q", in)
	}
	wd.s.Key("enter")
	wd.asked("tab-focus t9")
	wd.s.Key("x")
	wd.s.Key("esc")
	wd.asked("dock " + contract.DockBack)
	wd.shows("[ Menu ]")
}

// Every setting of the contract's table is a row of the settings screen, in
// its section, and is changed there by a key and by a click: each change is
// one `set`, a value that runs a program is asked about first, and the
// administrator's is shown with the line to type and never set.
func TestEverySettingIsARowChangedByAKeyAndByAClick(t *testing.T) {
	wd := startWith(t, settings, 120, 44, map[string]string{"PATH": ""}, func(*world) {})
	ask := func() (rows []setting, ys []int, typing, asks bool) {
		got := make(chan struct{})
		go wd.p.tell(news{do: func() {
			p := wd.p
			if p.stale || p.dirty {
				p.draw()
			}
			rows, typing, asks = p.settingsRows(), p.line != nil, p.pending != nil
			for _, s := range rows {
				ys = append(ys, slices.Index(p.rows, s.name+s.label))
			}
			close(got)
		}})
		<-got
		return rows, ys, typing, asks
	}
	// finish answers what a change still asks, the typing line and whether a
	// value that runs a program is to be used, until its `set` has run or,
	// for the administrator's, the line to type is shown.
	finish := func(s setting, act func()) {
		typed, used, at := false, false, time.Now()
		act()
		if !wd.until(6*time.Second, func() bool {
			switch _, _, typing, asks := ask(); {
			case !typing && !asks && !typed && (strings.Contains(wd.screen(), movedSaid) ||
				!strings.HasSuffix(s.key.Values, "|unlimited") && time.Since(at) > 700*time.Millisecond && len(wd.children("")) == wd.next):
				// The rows had just moved by themselves, and the click was refused.
				// A row whose click asks first is clicked again only where the pane
				// says so: a slow pane's question is no refusal.
				wd.settle()
				at = time.Now()
				act()
			case typing && !typed:
				typed = true
				wd.s.Type("9:00")
				wd.s.Key("enter")
			case asks && !used:
				if used = true; !strings.Contains(wd.screen(), "[ Use unlimited ]") && (!strings.Contains(wd.screen(), "this runs a program, exactly: ") || !strings.Contains(wd.screen(), "[ Use it ]")) {
					t.Errorf("%s: a value that runs a program is not shown first:\n%s", s.name, wd.screen())
				}
				wd.s.Key("y")
			}
			if s.key.Asks == contract.AsksAdmin {
				return strings.Contains(wd.screen(), contract.AdminLine)
			}
			all := wd.children("set " + s.name)
			return len(all) > 0 && len(wd.children("")) > wd.next
		}) {
			t.Fatalf("%s was not changed; the pane started:\n%s\nand shows:\n%s", s.name, strings.Join(wd.children(""), "\n"), wd.screen())
		}
		wd.next = len(wd.children(""))
	}
	drawn, changed := map[*contract.Key]bool{}, 0
	for i, section := range contract.Sections {
		wd.shows(" " + strings.ToUpper(section) + " ")
		rows, _, _, _ := ask()
		for range len(rows) - 1 {
			wd.s.Key("j")
		}
		for at := len(rows) - 1; at >= 0; at-- {
			s := rows[at]
			if _, _, ok := wd.s.Find(s.label); !ok {
				t.Errorf("%s: the row %q is not drawn:\n%s", section, s.label, wd.screen())
			}
			if drawn[s.key] = true; s.key.Kind != "" && s.name != "" {
				// By a key, then by a click on its value.
				for _, how := range []string{"key", "click"} {
					finish(s, func() {
						switch _, ys, _, _ := ask(); {
						case how == "key" && s.key.Kind == contract.KindNumber:
							wd.s.Key("right")
						case how == "key":
							wd.s.Key("enter")
						default:
							// The last button of the row's value.
							x := 0
							for i, r := range []rune(wd.s.Row(ys[at])) {
								if r == '[' {
									x = i + 1
								}
							}
							wd.s.Click(x, ys[at])
						}
					})
					changed++
				}
			}
			wd.s.Key("k")
		}
		if wd.s.Key("tab"); i == len(contract.Sections)-1 {
			wd.shows(" " + strings.ToUpper(contract.Sections[0]) + " ")
		}
	}
	rows := 0
	for i := range contract.Keys {
		if k := &contract.Keys[i]; k.Section != "" {
			if rows++; !drawn[k] {
				t.Errorf("the setting %s is no row of the settings screen", k.Name)
			}
		}
	}
	if rows < 60 || changed < 2*(rows-5) {
		t.Errorf("%d settings are rows and %d changes were made by a key or a click", rows, changed)
	}
	// What a change made stands in the files, and u takes the last one back.
	dirs, _ := wd.sys.Dirs()
	if cfg := person(wd.p.k, dirs); cfg.Fleets.Mode != contract.On && cfg.Panes.Fleet == contract.Right {
		t.Errorf("nothing was set: %+v", cfg)
	}
	wd.s.Key("u")
	wd.ran("set ")
}

// "Try it" under Agent tools runs the one test of the tool shown, which is
// doctor's: one child, with the tool's name.
func TestTryItRunsTheOneTestOfTheTool(t *testing.T) {
	wd := startWith(t, settings, 120, 44, map[string]string{"PATH": ""}, func(*world) {})
	for range slices.Index(contract.Sections, contract.SecTools) {
		wd.s.Key("tab")
	}
	wd.shows(" " + strings.ToUpper(contract.SecTools) + " ")
	wd.shows("[ Try it ]")
	wd.settle()
	wd.s.ClickText("[ Try it ]")
	if line := wd.ran("doctor --try "); !strings.HasPrefix(line, "doctor --try "+contract.DefaultTool+" --human") {
		t.Errorf("the button started %q", line)
	}
}

// What is done at a terminal and is no value of a file has its row all the
// same: the button opens a tab and names the line, and runs nothing; whether
// the page runs is asked of the page's own command.
func TestTheRowsOfWhatIsDoneAtATerminal(t *testing.T) {
	wd := startWith(t, settings, 120, 44, map[string]string{"PATH": ""}, func(*world) {})
	at := 0
	to := func(section string) {
		for ; contract.Sections[at%len(contract.Sections)] != section; at++ {
			wd.s.Key("tab")
			wd.settle()
		}
		wd.shows(" " + strings.ToUpper(section) + " ")
	}
	to(contract.SecNudges)
	wd.shows("Your own nudge programs: 0 · in hooks/nudge.d of your settings folder")
	// In the screen's own order: the row of teams looked at after the loop
	// is one of This project, the section the loop must end in.
	for _, row := range [][3]string{{contract.SecKeys, "[ Put whaleshark's own shortcuts in… ]", "init --keys"},
		{contract.SecProject, "[ Take this project out… ]", "init --remove"}} {
		section, row := row[0], row[1:]
		to(section)
		wd.shows(row[0])
		wd.settle()
		wd.s.ClickText(row[0])
		wd.shows("in the new tab type: whaleshark " + row[1])
		if !wd.until(5*time.Second, func() bool {
			return slices.ContainsFunc(wd.double.Calls(), func(c contract.WireCall) bool { return c.Op == contract.OpTabCreate && c.Label == row[1] })
		}) {
			t.Errorf("%s opened no tab", row[0])
		}
	}
	wd.shows("Teams this project brings: 0 in " + contract.TeamsDir)
	if n := len(wd.children("init")); n != 0 {
		t.Errorf("a click ran init %d times", n)
	}
	to(contract.SecPage)
	wd.settle()
	wd.s.ClickText("[ Is the page running? ]")
	wd.ran("dash status")
	// With phone access on, the code an app pairs with has its row: the
	// line that makes a new one, as typed.
	if strings.Contains(wd.screen(), "pairing code") {
		t.Error("a pairing code is offered while phone access is off")
	}
	if err := contract.ChangePhones(wd.sys, func(list *contract.Phones) error { list.On = true; return nil }); err != nil {
		t.Fatal(err)
	}
	wd.shows("[ A pairing code for an app ]")
	wd.settle()
	wd.s.ClickText("[ A pairing code for an app ]")
	wd.ran("dash phone on --human")
	// A tool's short rules are written by init's own step for that tool.
	to(contract.SecTools)
	wd.settle()
	wd.s.ClickText("[ Write its rules ]")
	wd.ran("init --agent claude")
}

// What `set` does of its own for phase 5: the layout's keys move the open
// panes at once, a limit of all agents under a fleet's is taken and said,
// and a section seen again ends its warning.
func TestSetMovesThePanesAndSaysWhatALimitMeans(t *testing.T) {
	d := newDesk(t)
	d.double.Width, d.k.Panes = 200, Panes
	run := func(args ...string) string {
		var out strings.Builder
		if _, err := set(&contract.Call{Kit: d.k, Command: contract.Find("set"), Args: args, Caller: contract.Caller{Pane: d.me, Kind: contract.Human}, Out: &out}); err != nil {
			t.Fatalf("set %v: %v", args, err)
		}
		return out.String()
	}
	if err := Panes(d.k, d.me); err != nil {
		t.Fatal(err)
	}
	if got := d.did(); !strings.Contains(got, "split "+d.me+" right 46") || !strings.Contains(got, "split "+d.me+" down 0.75") {
		t.Errorf("the layout as it comes opened\n%s", got)
	}
	for _, step := range []struct{ key, value, want, not string }{
		{"panes.fleet", "left", "split " + d.me + " left 46", ""},
		{"panes.fleet", "bottom", "split " + d.me + " down 9", " right "},
		{"panes.actions", "top", "split " + d.me + " up 0.75", ""},
		{"panes.actions", "off", "pane-close", " up "},
		{"panes.fleet", "off", "pane-close", "split"},
	} {
		run(step.key, step.value)
		if got := d.did(); !strings.Contains(got, step.want) || step.not != "" && strings.Contains(got, step.not) {
			t.Errorf("set %s %s asked the terminals for\n%s", step.key, step.value, got)
		}
	}
	if left := d.panes(); len(left) != 1 {
		t.Errorf("with both panes off there are %+v", left)
	}
	// With no pane named the layout is about the tab in front; a keeper out of reach is no error.
	run("panes.fleet", "right")
	if got := d.did(); !strings.Contains(got, "split "+d.me+" right 46") {
		t.Errorf("set panes.fleet right opened\n%s", got)
	}
	keeper := d.k.Terms
	if d.k.Terms = (contract.NoTerminals{}); Panes(d.k, "") != nil {
		t.Error("the layout with no keeper is an error")
	}
	d.k.Terms = keeper
	// In a narrow window the fleet is its strip, under the action pane's.
	d.double.Width = 70
	if Panes(d.k, d.me); !strings.Contains(d.did(), "split "+d.me+" down 2") {
		t.Error("in a narrow window the fleet did not open as its strip")
	}
	if said := run("fleets.total", "6"); !strings.Contains(said, "one fleet could use all 6") || !strings.Contains(said, "fleets.total: 6") {
		t.Errorf("a limit of all under a fleet's said %q", said)
	}
	if said := run("fleets.total", "20"); strings.Contains(said, "could use") {
		t.Errorf("a limit of all over a fleet's said %q", said)
	}
	// A fleet folded in the left section is in the file, and out of it when opened.
	run("shut", "r1/help", "pages")
	run("shut", "r1/billing")
	run("shut", "r1/help pages")
	if _, ui, _, _ := screen(d.k); !slices.Equal(ui.Shut, []string{"r1/billing"}) {
		t.Errorf("the folded fleets on file: %q", ui.Shut)
	}
	run("shell", "/bin/sh")
	if _, ui, _, _ := screen(d.k); !slices.Contains(ui.Changed, "shell") {
		t.Fatalf("a setting that runs a program is not marked: %+v", ui)
	}
	run("seen", contract.SecKeys)
	run("seen", contract.SecScroll)
	if _, ui, _, _ := screen(d.k); len(ui.Changed) != 0 {
		t.Errorf("after its section was seen again the warning stays: %+v", ui.Changed)
	}
}

// The left section rests. Every read of a record takes the record's lock,
// which changes a file in the folder the left section watches: that is no
// news and brings no new read, or the section would read for ever. A file
// that says something still does.
func TestALockFilesChangeIsNoNews(t *testing.T) {
	wd, _ := sideWorld(t, 32, 40)
	wd.shows("AGENTS · ", "sign-up page")
	wd.settle()
	ours := filepath.Join(wd.home, contract.ProjectDir)
	touch := func(name string) {
		if err := os.WriteFile(filepath.Join(ours, name), []byte(time.Now().String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	touch("lock") // the files are there before they are counted
	touch("turn")
	touch("current")
	time.Sleep(300 * time.Millisecond)
	before := wd.reads.Load()
	for range 10 {
		touch("lock")
		touch("turn")
		touch("worktrees.lock")
		time.Sleep(30 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if n := wd.reads.Load() - before; n != 0 {
		t.Errorf("the locks' changes brought %d reads of the record", n)
	}
	touch("current")
	if !wd.until(3*time.Second, func() bool { return wd.reads.Load() > before }) {
		t.Error("a file that says something brought no read")
	}
	for path, want := range map[string]bool{"/p/.whaleshark/lock": true, "/p/.whaleshark/turn": true, "/p/.whaleshark/runs/r1/wait.lock": true,
		"/p/.whaleshark/current": false, "/p/.whaleshark/runs/r1/state.json": false, "/p/locksmith.txt": false} {
		if lockFile(path) != want {
			t.Errorf("lockFile(%s) = %v", path, !want)
		}
	}
}

// A click a person makes on a button must take, the first time: the
// question about a value that runs a program, clicked the moment it shows.
func TestTheFirstClickOnUseItTakes(t *testing.T) {
	wd := startWith(t, settings, 120, 44, map[string]string{"PATH": ""}, func(*world) {})
	for range slices.Index(contract.Sections, contract.SecScroll) {
		wd.s.Key("tab")
	}
	wd.shows(" " + strings.ToUpper(contract.SecScroll) + " ")
	wd.key("j", "j", "j", "enter")
	wd.s.Type("zsh -l")
	wd.s.Key("enter")
	wd.shows("[ Use it ]")
	wd.s.ClickText("[ Use it ]")
	wd.ran("set shell")
}

// The same of a section's own button, clicked the moment its section shows.
func TestTheFirstClickOnTryItTakes(t *testing.T) {
	wd := startWith(t, settings, 120, 44, map[string]string{"PATH": ""}, func(*world) {})
	for range slices.Index(contract.Sections, contract.SecTools) {
		wd.s.Key("tab")
	}
	wd.shows("[ Try it ]")
	wd.s.ClickText("[ Try it ]")
	wd.ran("doctor --try ")
}

// At every width a setting's label stands whole beside its value, as the
// picture has it: 94 columns is the settings screen in a window of 120.
func TestASettingsLabelIsNotCutByItsValues(t *testing.T) {
	for _, w := range []int{94, 120, 170} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			wd := startWith(t, settings, w, 44, map[string]string{"PATH": ""}, func(*world) {})
			for i, section := range contract.Sections {
				wd.shows(" " + strings.ToUpper(section) + " ")
				got := make(chan []setting)
				go wd.p.tell(news{do: func() { got <- wd.p.settingsRows() }})
				for _, s := range <-got {
					if s.key.Kind != "" && term.Width(s.label) < w-40 {
						wd.shows(" " + s.label + " ")
					}
				}
				if section == contract.SecLook && w == 94 {
					wd.shows("Colour scheme")
					wd.shows(wd.p.glyph("‹ reef ›", "< reef >"))
				}
				if i < len(contract.Sections)-1 {
					wd.s.Key("tab")
				}
			}
		})
	}
}
