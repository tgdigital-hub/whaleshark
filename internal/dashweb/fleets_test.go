package dashweb

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/theme"
	"github.com/tgdigital-hub/whaleshark/internal/view"
)

// morning is Home of the orchestrator's run of the morning with three
// fleets, as its view must be: the sections are the looks in the one order,
// a card is a fleet, and the one item names the fleet it climbed from.
func morning() (*contract.Page, *testkit.ThreeFleets) {
	f := testkit.Fleets()
	v := &contract.View{Version: contract.ViewVersion, At: f.View.Now, Caller: contract.Human, Run: f.View.State.Run.ID, Mode: contract.ModeFleets,
		Items: []contract.Item{f.Item}, Fresh: contract.Fresh{Checked: f.View.Now}}
	for _, l := range contract.Looks {
		for _, c := range f.Cards {
			if c.Look != l.Look {
				continue
			}
			if n := len(v.Sections); n == 0 || v.Sections[n-1].Name != l.Section {
				v.Sections = append(v.Sections, contract.Section{Name: l.Section})
			}
			last := &v.Sections[len(v.Sections)-1]
			last.Cards = append(last.Cards, c)
		}
	}
	v.Counts.Agents, v.Counts.Waiting, v.Counts.Holding, v.Counts.NeedYou = 15, 1, 1, 1
	return &contract.Page{Screen: contract.ScreenTeam, Login: "robin", Host: "harbour", Theme: "reef", Token: "tok-1", View: v}, f
}

// Home prints the fleets of an orchestrator's run on a laptop and on a
// phone: one card a fleet under the looks' own headings, with the fixture's
// mark, name, word and colour, its leader's tool, its agents and how far
// its tasks are; the item that climbed is titled with its fleet.
func TestHomePrintsTheFleets(t *testing.T) {
	wide, f := morning()
	narrow := *wide
	narrow.Phone = true
	laptop, phone := draw(t, wide), draw(t, &narrow)
	order := regexp.MustCompile(` id="(top|fresh|main|you|foot|[ic]-[^"]+)"|<h2>([A-Z ]+)</h2>`)
	rows := fmt.Sprint(order.FindAllStringSubmatch(laptop, -1))
	if got := fmt.Sprint(order.FindAllStringSubmatch(phone, -1)); got != rows {
		t.Errorf("the phone's rows %s\nthe laptop's %s", got, rows)
	}
	var ids []string
	for _, m := range order.FindAllStringSubmatch(laptop, -1) {
		ids = append(ids, m[1]+m[2])
	}
	if got := strings.Join(ids, " "); got != "top fresh main you i-q1 NEEDS YOU c-G1 BUILDING c-G2 c-G3 foot" {
		t.Errorf("the rows of Home: %s", got)
	}
	colours := theme.Get(wide.Theme).Colours
	for name, out := range map[string]string{"laptop": laptop, "phone": phone} {
		cards := pageRows(t, out, "card", colours)
		if len(cards) != len(f.Cards) {
			t.Fatalf("%s: %d cards for %d fleets", name, len(cards), len(f.Cards))
		}
		// The fixture's cards are in the fleets' order; billing alone needs the person, so it is first in both.
		for i, c := range f.Cards {
			l := look(c.Look)
			if want := (shown{l.Mark, c.Name, string(c.Look), term.RGB(colours[c.Colour])}); cards[i] != want {
				t.Errorf("%s, fleet %d: the page shows %v, the fixture says %v", name, i, cards[i], want)
			}
			for _, want := range []string{
				fmt.Sprintf(`href="%s%s">%s</a> <span class="c-dim">· %s</span>`, contract.RouteTask, c.Task, c.Name, c.Agent),
				fmt.Sprintf(`<span class="ctx c-dim">%s</span>`, c.Fleet.Working()),
			} {
				if !strings.Contains(out, want) {
					t.Errorf("%s: the fleet %s has no %s", name, c.Name, want)
				}
			}
		}
		if items := pageRows(t, out, "item", colours); len(items) != 1 || items[0].name != "billing › refund flow" || items[0].mark != contract.Looks[0].Mark {
			t.Errorf("%s: the item that climbed reads %v", name, items)
		}
		for _, want := range []string{`data-title="billing › refund flow — needs you"`, "passed up by the orchestrator · 2m",
			`<span class="news c-dim">0 of 5 done · 1 to check</span>`, `<span class="news c-dim">0 of 6 done</span>`, "15 agents · 1 needs you · 0 done today"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: Home does not hold %s", name, want)
			}
		}
		// A phone hides the news of a card that only builds: a fleet's line is never that.
		if strings.Contains(out, " plain") || strings.Contains(out, "ctx unknown") || strings.Contains(out, "ctx <progress") {
			t.Errorf("%s: a fleet's card is drawn as one agent's", name)
		}
	}
}

// A fleet's news of its own stands in the place of its counts, and the
// counts are the view's figures as they are.
func TestAFleetsThirdLine(t *testing.T) {
	p, _ := morning()
	card := p.View.Sections[1].Cards[0]
	fleet := *card.Fleet
	fleet.Done, fleet.Of, fleet.ToCheck = 4, 7, 1
	card.Fleet = &fleet
	if got := news(p, card); got != "4 of 7 done · 1 to check" {
		t.Errorf("the fleet's counts read %q", got)
	}
	card.News = "9 of 12 pages written"
	if got := news(p, card); got != card.News {
		t.Errorf("the fleet's own news reads %q", got)
	}
	card.Fleet.Agents, card.Fleet.Led = 1, false
	p.View.Sections = []contract.Section{{Name: "BUILDING", Cards: []contract.Card{card}}}
	if out := draw(t, p); !strings.Contains(out, `<span class="ctx c-dim">1 agent</span>`) || !strings.Contains(out, `<span class="news c-dim">9 of 12 pages written</span>`) {
		t.Errorf("a fleet of one agent with news of its own:\n%s", out)
	}
	// Its leader is one of its agents, as the header above the cards counts.
	card.Fleet.Led = true
	if out := draw(t, p); !strings.Contains(out, `<span class="ctx c-dim">2 agents</span>`) {
		t.Errorf("a fleet of a leader and one worker:\n%s", out)
	}
}

// The afternoon with every guard on. Home of the job says what was spent of
// the limit, in the view's own words; in the fleet whose worker looks stuck
// the card's third line says so in the warning colour, on a phone too, where
// a building card with nothing to say has no third line; and the catch-up
// has a row for what looked stuck only when something did.
func TestHomeSaysWhatWasSpentAndACardThatItLooksStuck(t *testing.T) {
	g := testkit.Guarded()
	in := contract.ViewInput{Now: g.Now, Caller: contract.Human, State: g.Up, Fleets: g.Low, Person: g.Person, Limits: contract.ProjectDefaults(), Checked: g.Now}
	p := &contract.Page{Screen: contract.ScreenTeam, Theme: "reef", Token: "tok-1", View: view.Build(in)}
	if out := draw(t, p); p.View.Spent != 33 || p.View.Limit != 40 || !strings.Contains(out, "<p>spent: 33 of 40 agent-hours</p>") || strings.Contains(out, "c-blocked\">stuck") {
		t.Errorf("Home of the job, spent %v of %v:\n%s", p.View.Spent, p.View.Limit, out)
	}
	in.State, in.Fleets = g.Low["billing"], nil
	p.View, p.Phone = view.Build(in), true
	out := draw(t, p)
	_, card, _ := strings.Cut(out, ` id="c-T7">`)
	card, _, _ = strings.Cut(card, "</article>")
	if !strings.Contains(card, `<span class="news c-blocked">stuck 132m: no progress note · the same check failed 3 times</span>`) ||
		strings.Contains(out, `plain" id="c-T7"`) || strings.Count(out, `class="news c-blocked"`) != 1 {
		t.Errorf("the card of the worker that looks stuck:\n%s", card)
	}
	p.Catchup = &contract.Catchup{From: g.Now.Add(-time.Hour), To: g.Now}
	if out := draw(t, p); strings.Contains(out, "Looked stuck") {
		t.Errorf("nothing looked stuck, and the catch-up has the row:\n%s", out)
	}
	p.Catchup.Stuck = contract.CatchLine{N: 1, Text: "sign-up page, still so"}
	if out := draw(t, p); !strings.Contains(out, "<tr><th>Looked stuck</th><td>1</td><td>sign-up page, still so</td></tr>") {
		t.Errorf("the catch-up of an hour in which a worker looked stuck:\n%s", out)
	}
}
