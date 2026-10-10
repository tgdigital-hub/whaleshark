package dashweb

import (
	"bytes"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/panes"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/term/termtest"
	"github.com/tgdigital-hub/whaleshark/internal/theme"
	"github.com/tgdigital-hub/whaleshark/internal/view"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func TestMain(m *testing.M) { scenario.Main(m) }

// fixtures is every prepared moment the testkit holds.
var fixtures = []string{testkit.Evening}

func load(t testing.TB, name string) *testkit.Fixture {
	t.Helper()
	f, err := testkit.Load(name)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func draw(t testing.TB, p *contract.Page) string {
	t.Helper()
	var b bytes.Buffer
	if err := Page(&b, p); err != nil {
		t.Fatalf("the %s screen: %v", p.Screen, err)
	}
	return b.String()
}

// team is a fixture's page as the page's server fills it.
func team(f *testkit.Fixture) *contract.Page {
	return &contract.Page{Screen: contract.ScreenTeam, Login: "robin", Host: "harbour", Theme: "reef", Token: "tok-1",
		View: view.Build(f.Input(contract.Human))}
}

// screens is one page of every kind, drawn from a fixture, with everything
// a screen can carry set on some of them.
func screens(t testing.TB, f *testkit.Fixture) []*contract.Page {
	base, at := team(f), f.Now
	v := *base.View
	card := v.Sections[1].Cards[0]
	card.Actions = []string{"go", "accept", "accept-by-hand", "send-back", "stop", "note", "retry", "start", "close"}
	settling := v.Items[0]
	settling.Answer, settling.Settles, settling.By = "yes", at.Add(3500*time.Millisecond), &contract.Origin{Caller: contract.Human, Where: contract.WherePage}
	settling.Buttons = []contract.Button{{Label: "Undo", Key: "u", Action: "undo"}}
	used := settling
	used.ID, used.Used, used.Buttons = "q1", at.Add(-time.Minute), nil
	prompt := contract.Item{ID: "n9", Form: contract.FormTodo, Look: contract.LookAtPrompt, Task: card.Task, Name: card.Name,
		Source: "the tool", Since: at, Text: "waiting at a permission prompt in its tab", Buttons: []contract.Button{
			{Label: "Done", Key: "y", Answer: contract.AnswerDone}, {Label: "Cannot", Key: "n", Answer: contract.AnswerCannot}, {Label: "Go there", Action: "go"}}}
	v.Items = append([]contract.Item{settling}, append(v.Items[1:], prompt)...)
	v.Held, v.Answered = []contract.Item{base.View.Items[1]}, []contract.Item{used}
	v.Alerts, v.Lines = []string{"the lead agent is not listening · 3 events waiting"}, []string{"to check: 7, about 21 min one by one, about 3 min together"}
	v.Strip = contract.Strip{Paused: true, StillRunning: 2, DND: true, Mute: true, Away: at.Add(-time.Hour), DoneAsYou: 1}
	v.Counts.Elsewhere = 1
	full := *base
	full.View, full.Lead, full.Notes = &v, "Twelve workers are running.\nTwo of them need a word from you.", []string{"slow updates"}
	full.Said = &contract.Refusal{Code: "paused", Message: "All work is paused.", Next: []string{"whaleshark resume --everywhere --human"}}
	full.Catchup = &contract.Catchup{From: at.Add(-64 * time.Minute), To: at, Waiting: contract.CatchLine{N: 1, Text: "a question from checkout form"}, StillBuilding: 4}
	with := func(screen string, set func(p *contract.Page)) *contract.Page {
		p := full
		if p.Screen = screen; set != nil {
			set(&p)
		}
		return &p
	}
	phone := with(contract.ScreenTeam, func(p *contract.Page) { p.Phone = true })
	return []*contract.Page{base, &full, phone,
		with(contract.ScreenPlan, func(p *contract.Page) {
			p.Plan = &contract.Plan{Chain: []string{"T2", "T13"}, Holding: "Nothing new can start until T1 is done.", Loop: true, Rows: []contract.PlanRow{
				{Card: card, After: []string{"P1"}}, {Card: v.Sections[0].Cards[0], After: []string{"T1", "T2", "T3", "T4"}, Waits: 4, HeldBy: "q7"}}}
		}),
		with(contract.ScreenMessages, func(p *contract.Page) {
			p.Messages = []contract.Message{{At: at, Dir: "<", Look: contract.LookNeedsYou, Task: "T2", Kind: "question", Text: "must it?", New: true},
				{At: at, Dir: ">", Task: "T11", Kind: "answer", Text: "the old one", Struck: true, By: &contract.Origin{Caller: contract.Human}},
				{At: at, Dir: "<", Task: "T7", Kind: "80%", Text: "templates done", Dim: true}}
		}),
		with(contract.ScreenTask, func(p *contract.Page) {
			p.Sites = map[string]int{card.Task: 20143}
			p.Task = &contract.TaskView{Card: card, Run: v.Run, Says: "all sending paths covered", Check: "npm test -- email", Changed: "9 files, +410 -12",
				Result: &contract.CheckResult{At: at, Tail: "2 of 41 tests fail"}, ByHand: "looked at it", Owns: []string{"src/email/**"}, Brief: "briefs/T7.md",
				Decisions: []contract.Decision{{Question: "Which sender?", Answer: "orders"}}, Items: []contract.Item{prompt}, Screen: "$ npm test\n<b>2 failing</b>",
				Tries: []contract.Try{{Attempt: "T7.1", State: "reported", Result: "attempts/T7.1/result.md", CheckLog: "attempts/T7.1/check.log",
					Evidence: []contract.EvidenceFile{{Name: "home.png", Kind: "png"}, {Name: "trace.zip"}}, History: []contract.Event{{At: at, Text: "started"}}}}}
		}),
		with(contract.ScreenJobs, func(p *contract.Page) {
			p.Jobs = []contract.Job{{Root: "/work/shop", View: &v, Next: "whaleshark status --root /work/shop --run r3"},
				{Root: "/work/docs", View: &contract.View{Run: "r1", Objective: "tidy the docs"}}}
		}),
		with(contract.ScreenLead, nil),
		{Screen: contract.ScreenPair, Phone: true, Pair: "4821", Notes: []string{"the road names nobody: this phone's session stands alone"}},
		{Screen: contract.ScreenRefused, Said: &contract.Refusal{Message: "This address answers nobody without a login."}},
	}
}

var fixed = []string{"page.css", "page.js", "notice.js", "home.webmanifest"}

// The page asks nothing of any other address, carries nothing inline, and
// all of it together is small. The one address written out is the link to a
// task's own running site on this machine.
func TestThePageNamesNoOtherAddressAndNothingIsInline(t *testing.T) {
	address := regexp.MustCompile(`(?i)(https?:|wss?:|ftp:)?//[a-z0-9.-]+|@import|(?-i:url\()`)
	inline := regexp.MustCompile(`(?i)<style|\sstyle=|\son[a-z]+=|<script(?:\s+(?:src="/a/[a-z.]+"|defer))*\s*>[^<]|javascript:`)
	site := "http://localhost:20143/"
	size := 0
	for _, name := range fixed {
		body, kind, ok := Asset(name)
		if !ok || kind == "" || len(body) == 0 {
			t.Fatalf("no file %s", name)
		}
		if size += len(body); address.Match(body) {
			t.Errorf("%s names an address: %q", name, address.Find(body))
		}
	}
	if _, _, ok := Asset("../commands.go"); ok {
		t.Error("a file that is not the page's was handed out")
	}
	f := load(t, testkit.Evening)
	for _, p := range screens(t, f) {
		out := draw(t, p)
		if got := address.FindAllString(strings.ReplaceAll(out, site, ""), -1); got != nil {
			t.Errorf("the %s screen names an address: %q", p.Screen, got)
		}
		if got := inline.FindAllString(out, -1); got != nil {
			t.Errorf("the %s screen carries something inline: %q", p.Screen, got)
		}
		for _, m := range regexp.MustCompile(`(?:src|href|action)="([^"]*)"`).FindAllStringSubmatch(out, -1) {
			if !strings.HasPrefix(m[1], "/") && m[1] != site {
				t.Errorf("the %s screen points at %q", p.Screen, m[1])
			}
		}
	}
	raw, _ := files.ReadFile("page.html")
	if size += len(raw); size > 100_000 {
		t.Errorf("the page is %d bytes", size)
	}
	t.Logf("the page is %d bytes", size)
}

// The addresses the templates and the script write out are the contract's.
func TestTheRoutesAreTheContracts(t *testing.T) {
	f := load(t, testkit.Evening)
	all := ""
	for _, p := range screens(t, f) {
		all += draw(t, p)
	}
	js, _, _ := Asset("page.js")
	for _, want := range []string{`href="` + contract.RouteTeam + `"`, `href="` + contract.RoutePlan + `"`, `href="` + contract.RouteMessages + `"`,
		`href="` + contract.RouteJobs + `"`, `href="` + contract.RouteLead + `"`, `href="` + contract.RouteTask + "T7", `action="` + contract.RouteDo + "answer",
		`src="` + contract.RouteEvidence + "T7.1/0", `href="` + contract.RouteEvidence + "T7.1/1", `name="` + contract.FieldToken + `" value="tok-1"`,
		`src="` + contract.RouteAsset + `page.js"`, `href="` + contract.RouteAsset + `page.css"`, `href="` + contract.RouteAsset + `home.webmanifest"`} {
		if !strings.Contains(all, want) {
			t.Errorf("no screen holds %s", want)
		}
	}
	for _, want := range []string{"'" + contract.RouteLive + "'", "'" + contract.RoutePush + "'", "'" + contract.RouteAsset + "notice.js'", "[name=" + contract.FieldToken + "]"} {
		if !bytes.Contains(js, []byte(want)) {
			t.Errorf("the script does not hold %s", want)
		}
	}
}

var (
	formRE  = regexp.MustCompile(`(?s)<form method="post" action="([^"]*)">(.*?)</form>`)
	fieldRE = regexp.MustCompile(`<input(?: type="hidden")? name="([^"]*)"`)
)

// With scripts off every button is an ordinary form: it posts one row of
// the action table, never one the page may not run, with the session's
// token and one field for each placeholder of the row. Nothing that can be
// pressed stands outside a form but what only opens one, and every answer
// the view offers has its form.
func TestEveryButtonIsAnOrdinaryForm(t *testing.T) {
	f := load(t, testkit.Evening)
	for _, p := range screens(t, f) {
		out := draw(t, p)
		forms := formRE.FindAllStringSubmatch(out, -1)
		for _, m := range forms {
			id := strings.TrimPrefix(m[1], contract.RouteDo)
			i := slices.IndexFunc(contract.Actions, func(a contract.Action) bool { return a.ID == id })
			if i < 0 || id == m[1] {
				t.Fatalf("the %s screen posts to %s", p.Screen, m[1])
			}
			a := contract.Actions[i]
			if slices.Contains(contract.Never, a.Run[0]) || slices.Contains(contract.Never, strings.Join(a.Run[:min(2, len(a.Run))], " ")) {
				t.Errorf("the %s screen offers %s", p.Screen, id)
			}
			want := []string{contract.FieldToken}
			for _, arg := range a.Run {
				if name := strings.Trim(arg, "{}"); name != arg {
					want = append(want, strings.Replace(name, "file", "text", 1))
				}
			}
			var got []string
			for _, in := range fieldRE.FindAllStringSubmatch(m[2], -1) {
				got = append(got, in[1])
			}
			if !slices.Equal(got, want) || strings.Count(m[2], "<button") != 1 {
				t.Errorf("the %s screen's form for %s has the fields %v, want %v, and %d buttons", p.Screen, id, got, want, strings.Count(m[2], "<button"))
			}
		}
		rest := formRE.ReplaceAllString(out, "")
		if n := strings.Count(rest, "<button"); n > 1 || n == 1 && !strings.Contains(rest, `<button class="b" id="notice" hidden>`) {
			t.Errorf("the %s screen has a button outside a form", p.Screen)
		}
		if p.View == nil || p.Screen != contract.ScreenTeam {
			continue
		}
		for _, it := range p.View.Items {
			for _, b := range it.Buttons {
				action, value := "answer", html.EscapeString(b.Answer)
				if b.Action != "" {
					action, value = b.Action, ""
				}
				has := slices.ContainsFunc(forms, func(m []string) bool {
					return m[1] == contract.RouteDo+action && (strings.Contains(m[2], `name="id" value="`+it.ID+`"`) || strings.Contains(m[2], `name="task" value="`+it.Task+`"`)) && strings.Contains(m[2], `value="`+value)
				})
				if gone := p.Phone && b.Action == "go"; has == gone {
					t.Errorf("the %s screen (phone %v): the form of %s for %s is there: %v", p.Screen, p.Phone, b.Label, it.ID, has)
				}
			}
			if it.Line && len(it.Buttons) == 0 && !strings.Contains(out, `id="l-answer-`+it.ID+`"`) {
				t.Errorf("no line to answer %s on", it.ID)
			}
		}
	}
}

// What the page shows of the screens' other parts, on the full page of the
// fixture: the settling answer with its count and Undo, a used answer with
// no Undo, the held items, the four buttons as they stand, the catch-up
// block, both bars, the lines of the view, and what somebody wrote, made
// harmless.
func TestTheScreensShowWhatTheViewSays(t *testing.T) {
	f := load(t, testkit.Evening)
	all := screens(t, f)
	full, phone, task, pair := draw(t, all[1]), draw(t, all[2]), draw(t, all[5]), draw(t, all[8])
	if base := draw(t, all[0]); !strings.Contains(base, `id="l-answer-q7-o" value="other: "`) || !strings.Contains(base, `name="text" value="yes"`) {
		t.Errorf("the first item has no Yes and no line for Other")
	}
	for _, want := range []string{
		`yes · Undo · <b class="left">4</b>`, `action="/do/undo"><input type="hidden" name="t" value="tok-1"><input type="hidden" name="id" value="q7">`,
		"yes · used at 21:13", "by you, from the page", "held: 1 · do not disturb is on", "answered: 1",
		`a-resume" data-key="r">Resume</button>`, `class="b on a-dnd" data-key="d">Do not disturb</button>`, `class="b on a-mute" data-key="m">Mute</button>`,
		`name="value" value="off"`, "· paused · 2 still running · 1 held · away 1h 00m · press c · 1 done as you",
		"WHILE YOU WERE AWAY", "1h 04m  (20:10 to 21:14)", "<th>Waiting for you now</th><td>1</td><td>a question from checkout form</td>",
		"<td>Stopped  0   Failed  0</td>", "the lead agent is not listening · 3 events waiting", "slow updates", "All work is paused.",
		"&gt; whaleshark resume --everywhere --human", "to check: 7, about 21 min", "1 needs you in another job",
		`<progress max="100" value="40"></progress> 40%`, `<progress class="pale" max="100" value="50">`, `<progress class="c-ctx_mid" max="100" value="61">`,
		"ctx unknown", "said 14m ago: half the screens done", "clashes with search box: search.js", "· also changes search.js", "idle 12m",
		`live · checked <b>0</b>s ago`, `<html lang="en" class="t-reef">`, `value="send back: "`, `value="cannot: "`,
	} {
		if !strings.Contains(full, want) {
			t.Errorf("the full team screen does not hold %s", want)
		}
	}
	for _, want := range []string{`class="t-reef phone"`, "at a prompt: needs a terminal", ">Catch up</button>", ">DND</button>"} {
		if !strings.Contains(phone, want) {
			t.Errorf("the phone's screen does not hold %s", want)
		}
	}
	for _, want := range []string{"$ npm test\n&lt;b&gt;2 failing&lt;/b&gt;", "npm test -- email · FAILED 21:14 · 2 of 41 tests fail", `alt="home.png"`, `download>trace.zip</a>`,
		"answering on port 20143", `<span class="c-blocked">sure?</span> <button class="b a-stop">Stop</button>`, "Accept by hand", "21:14 started"} {
		if !strings.Contains(task, want) {
			t.Errorf("the task screen does not hold %s", want)
		}
	}
	if !strings.Contains(pair, `<p class="number">4821</p>`) || strings.Contains(pair, "<script") || strings.Contains(pair, "<form") || strings.Contains(pair, "robin") {
		t.Errorf("an unpaired phone is shown more than a number:\n%s", pair)
	}
	old := *all[0]
	v := *old.View
	v.Fresh.Checked, v.Items = v.At.Add(-time.Minute), []contract.Item{{ID: "q1", Look: contract.LookNeedsYou, Name: `<img src=x onerror=alert(1)>`, Text: `"><script>alert(1)</script>`}}
	old.View, old.Theme = &v, "no such scheme"
	out := draw(t, &old)
	if !strings.Contains(out, "STALE since 21:13") || !strings.Contains(out, `id="main" class="old"`) || !strings.Contains(out, `class="t-reef"`) {
		t.Errorf("a screen checked a minute ago does not say so, or a scheme nobody has is not the default")
	}
	if strings.Contains(out, "<img src=x") || strings.Contains(out, "<script>alert") {
		t.Errorf("what somebody wrote is drawn as it stands")
	}
}

// The style sheet's colours are the scheme table's, every scheme and every
// name, and the pale bar is the contract's.
func TestTheSchemesAreThePanesOwnTable(t *testing.T) {
	css, _, _ := Asset("page.css")
	for _, s := range theme.Schemes {
		block := regexp.MustCompile(`(?s)\.t-` + s.Name + ` ?\{(.*?)\}`).FindSubmatch(css)
		if block == nil {
			t.Fatalf("no variables for the scheme %s", s.Name)
		}
		for _, name := range contract.Colours {
			want := fmt.Sprintf("--%s:#%06x", name, s.Colours[name])
			if s.Name == theme.None {
				want = "--" + name + ": " // the browser's own
			}
			if !bytes.Contains(block[1], []byte(want)) {
				t.Errorf("%s: no %s", s.Name, want)
			}
		}
		if want := fmt.Sprintf("--pale:#%06x", contract.Pale(s.Colours["building"], s.Ground)); s.Name != theme.None && !bytes.Contains(block[1], []byte(want)) {
			t.Errorf("%s: no %s", s.Name, want)
		}
	}
	for _, name := range contract.Colours {
		if !bytes.Contains(css, []byte(".c-"+name+"{color:var(--"+name+")}")) {
			t.Errorf("no class for the colour %s", name)
		}
	}
}

// shown is one row as a screen shows it: the mark, the name, the word
// beside it and the colour of the mark.
type shown struct {
	mark, name, word string
	colour           term.Colour
}

func pageRows(t *testing.T, out, kind string, colours map[string]uint32) (rows []shown) {
	re := regexp.MustCompile(`(?s)<article class="` + kind + `[^>]*>.*?<span class="mark c-(\w+)">(.)</span>.*?class="name"[^>]*>([^<]*)<(?:.*?<span class="word[^>]*>([^<]*)<)?`)
	if kind == "item" {
		re = regexp.MustCompile(`(?s)<article class="item[^>]*>.*?<span class="mark c-(\w+)">(.)</span> <b class="name">([^<]*)<()`)
	}
	for _, m := range re.FindAllStringSubmatch(out, -1) {
		rows = append(rows, shown{m[2], html.UnescapeString(m[3]), m[4], term.RGB(colours[m[1]])})
	}
	return rows
}

var (
	paint    = regexp.MustCompile("\x1b\\[38;2;(\\d+);(\\d+);(\\d+)m(.)\x1b\\[39m")
	switches = regexp.MustCompile("\x1b\\[[0-9;]*m")
)

func textRows(t *testing.T, v *contract.View, id string, colours map[string]uint32) (rows []shown) {
	var b strings.Builder
	view.Text(&b, v, view.Options{Width: 200, Colours: colours})
	for _, line := range strings.Split(b.String(), "\n") {
		m := regexp.MustCompile(`^ (\S) ` + id + ` +(.+?)  +(.+?)  `).FindStringSubmatch(switches.ReplaceAllString(line, ""))
		c := paint.FindStringSubmatch(line)
		if m == nil || c == nil {
			continue
		}
		var r, g, bl uint32
		fmt.Sscan(c[1]+" "+c[2]+" "+c[3], &r, &g, &bl)
		rows = append(rows, shown{m[1], strings.TrimSuffix(m[2], " ▲"), m[3], term.RGB(r<<16 | g<<8 | bl)})
	}
	return rows
}

func paneRows(s *termtest.Screen, re string) (rows []shown) {
	marks := ""
	for _, l := range contract.Looks {
		marks += l.Mark
	}
	for y := range s.Term.H {
		m := regexp.MustCompile(re).FindStringSubmatch(s.Row(y))
		if m == nil || !strings.Contains(marks, m[1]) {
			continue
		}
		x, _, _ := s.Find(m[1] + " " + m[2])
		rows = append(rows, shown{m[1], strings.TrimSuffix(strings.TrimSpace(m[2]), " ▲"), strings.TrimSpace(m[3]), s.Term.At(x, y).Style.Fg})
	}
	return rows
}

// Each fixture drawn as page, as pane and as plain text gives the same
// rows, in the same order, each with the same mark, name and colour. The
// word beside a card is the same on the page and in the pane; plain text
// puts a building agent's figure in its place, which the page and the pane
// show beside the bar.
func TestPagePaneAndPlainTextShowTheSameRows(t *testing.T) {
	// The marks themselves are compared, so the pane is told to draw them:
	// left alone it asks the locale, and a machine that names none gets the
	// plain ones, which the page never draws.
	t.Setenv("WHALESHARK_MARKS", "full")
	for _, name := range fixtures {
		f := load(t, name)
		p := scenario.Prepare(t, f, nil)
		view.Plug(p.Kit)
		for _, kv := range p.Env(p.Own) {
			k, v, _ := strings.Cut(kv, "=")
			t.Setenv(k, v)
		}
		pg := team(f)
		pg.View = p.Kit.View(f.Input(contract.Human))
		colours := theme.Get(pg.Theme).Colours
		out := draw(t, pg)
		for kind, pane := range map[string]struct{ name, text, grid string }{
			"card": {"fleet", `T\d+`, `^.(\S) (.+?)   +(\S.*)$`},
			"item": {"actions", `[qn]\d+`, `^ [ ›] (\S) (.+?) · ()`},
		} {
			s := termtest.New(70, 60)
			done := make(chan struct{})
			go func() {
				defer close(done)
				panes.Run(pane.name, s.Term, p.Kit, p.Root, "")
			}()
			if !s.Wait("live · ") {
				t.Fatalf("the %s pane drew nothing", pane.name)
			}
			s.Still(100 * time.Millisecond)
			// The pane has left before its grid is read: nothing draws on it now.
			s.Close()
			<-done
			page, grid, text := pageRows(t, out, kind, colours), paneRows(s, pane.grid), textRows(t, pg.View, pane.text, colours)
			if len(page) == 0 || len(page) != len(grid) || len(page) != len(text) {
				t.Fatalf("%s, %ss: the page shows %d, the pane %d, plain text %d\n%v\n%v\n%v", name, kind, len(page), len(grid), len(text), page, grid, text)
			}
			for i, row := range page {
				g, x := grid[i], text[i]
				if kind == "item" {
					row.word, g.word, x.word = "", "", ""
				} else if strings.HasSuffix(x.word, "%") && strings.Contains(out, `value="`+strings.TrimSuffix(x.word, "%")+`"></progress> `+x.word) {
					x.word = row.word
				} else if strings.HasPrefix(row.word, x.word+" ") {
					x.word = row.word // "idle 12m": plain text prints the age in its own column
				}
				if row != g || row != x {
					t.Errorf("%s, %s %d: the page shows %v, the pane %v, plain text %v", name, kind, i, row, g, x)
				}
			}
		}
	}
}

// A run of 200 tasks is drawn in well under a tenth of a second.
func TestTwoHundredTasksAreDrawnFast(t *testing.T) {
	p := team(load(t, testkit.Evening))
	v := *p.View
	card := v.Sections[0].Cards[0]
	v.Sections = []contract.Section{{Name: "BUILDING"}}
	for i := range 200 {
		card.Task = fmt.Sprint("T", i)
		v.Sections[0].Cards = append(v.Sections[0].Cards, card)
	}
	p.View = &v
	draw(t, p)
	start := time.Now()
	out := draw(t, p)
	if took := time.Since(start); took > 100*time.Millisecond || strings.Count(out, `<article class="card`) != 200 {
		t.Errorf("200 cards took %v", took)
	}
}

// Plug puts both into the kit and binds nothing.
func TestPlug(t *testing.T) {
	k := contract.NewKit()
	Plug(k)
	var b bytes.Buffer
	if err := k.Page(&b, &contract.Page{Screen: contract.ScreenRefused}); err != nil || !strings.Contains(b.String(), "<h1>WhaleShark</h1>") {
		t.Errorf("the kit's page: %v\n%s", err, b.String())
	}
	if _, kind, ok := k.Asset("page.js"); !ok || !strings.HasPrefix(kind, "text/javascript") {
		t.Errorf("the kit's script is %q, %v", kind, ok)
	}
	for _, c := range contract.Commands {
		if k.Handler(c.Name) != nil {
			t.Errorf("%s is bound", c.Name)
		}
	}
}

// With WHALESHARK_PAGE_OUT naming a folder, every screen and fixed file is
// written there, to be looked at in a browser: serve the folder as it is.
func TestWriteThePageOut(t *testing.T) {
	dir := os.Getenv("WHALESHARK_PAGE_OUT")
	if dir == "" {
		t.Skip("WHALESHARK_PAGE_OUT names no folder")
	}
	if err := os.MkdirAll(filepath.Join(dir, "a"), 0o700); err != nil {
		t.Fatal(err)
	}
	for i, p := range screens(t, load(t, testkit.Evening)) {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d-%s.html", i, p.Screen)), []byte(draw(t, p)), 0o600)
	}
	for _, name := range fixed {
		body, _, _ := Asset(name)
		os.WriteFile(filepath.Join(dir, "a", name), body, 0o600)
	}
}
