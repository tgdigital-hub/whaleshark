package panes

import (
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/theme"
)

// What Do not disturb keeps back is still listed and can still be answered:
// dim, after what waits, under the line that counts it. The strip counts it
// too, and when Do not disturb ends the pane unfolds.
func TestWhatDoNotDisturbKeepsBack(t *testing.T) {
	p, view, _ := still(t, actions, 110, 30)
	dim := term.RGB(theme.Get("reef").Colours["dim"])
	all := view.Items
	view.Items, view.Held, view.Strip.DND, p.stale = all[:2], all[2:], true, true
	p.draw()
	p.find(t, "3 waiting · 2 hold work up · 1 held")
	_, y := p.find(t, "held: 1 · do not disturb is on")
	x, at := p.find(t, "● price list")
	if at != y+1 || p.t.At(x, at).Style.Fg != dim || p.t.At(x+2, at).Style.Fg != dim {
		t.Errorf("the held item stands in row %d under the line in row %d, drawn %+v", at, y, p.t.At(x, at).Style)
	}
	if x, y := p.find(t, "▲ photo upload"); p.t.At(x, y).Style.Fg == dim {
		t.Errorf("an item that waits is drawn dim")
	}
	p.find(t, "[ Approve ]")
	press(p, "j", "j", "j")
	if p.sel != all[2].ID {
		t.Errorf("the keys chose %q, want the held item %s", p.sel, all[2].ID)
	}
	// One row each: the held item is dim there too, and no line counts it.
	p.folded, p.dirty = true, true
	p.t.Resize(110, 6)
	p.draw()
	if _, _, ok := p.t.Find("held: 1 · do not"); ok {
		t.Errorf("a folded pane spends a row on the count:\n%s", frame(p))
	}
	view.Items, view.Held, view.Strip.DND, p.stale = all, nil, false, true
	if p.draw(); p.folded {
		t.Error("the pane stayed folded when Do not disturb ended with something held")
	}
}

// t goes through every scheme, and the first is the default.
func TestTheSchemeKeyGoesRound(t *testing.T) {
	p, _, _ := still(t, fleet, 40, 20)
	var got []string
	for range theme.Schemes {
		press(p, "t")
		got = append(got, p.scheme.Name)
	}
	if want := "paper kelp ember mono none reef"; strings.Join(got, " ") != want {
		t.Errorf("t went through %v, want %s", got, want)
	}
}

// The person's settings are lines of the list of every action, the colour
// schemes among them: reached by typing and Enter, and by clicks alone.
func TestTheSettingsAreLinesOfTheActionList(t *testing.T) {
	wd := startWith(t, actions, 110, 48, nil, ours)
	wd.s.Type("\x1b[I")
	wd.shows("KEYS GO HERE")
	wd.key("/")
	wd.shows("42 actions and settings · type to search")
	wd.s.Type("scheme: kelp")
	wd.shows("1 actions and settings")
	wd.s.Key("enter")
	wd.ran("set theme kelp --human")
	// The pane saw its settings file change: the list says which scheme it is now.
	wd.key("/")
	wd.s.Type("colour scheme")
	now := func(name string) bool {
		_, y, ok := wd.s.Find("Colour scheme: " + name)
		return ok && strings.HasSuffix(wd.s.Row(y), " now")
	}
	for end := time.Now().Add(3 * time.Second); !now("kelp") || now("reef"); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("the list does not say that kelp is the scheme now:\n%s", wd.screen())
		}
	}
	wd.s.Key("esc")

	for _, row := range [][3]string{{"A sound when you are needed", "set nudge.sound toggle --human", "nudge.sound: off"},
		{"A message to the phone when you are needed", "set nudge.phone toggle --human", "nudge.phone: off"},
		{"The action pane at the top", "set actions top --human", "actions: top"}} {
		wd.shows("ACTIONS ")
		wd.settle()
		wd.s.Click(3, wd.h-1)
		wd.shows("type to search")
		wd.click(row[0])
		wd.ran(row[1])
		wd.shows(row[2])
	}
	wd.key("/")
	wd.s.Type("action pane at")
	wd.shows("The action pane at the bottom")
	wd.s.Key("esc")
	wd.key("/")
	wd.s.Type("when you are")
	if y := wd.row("A sound when you are needed"); !strings.HasSuffix(wd.s.Row(y), " off") || !strings.HasSuffix(wd.s.Row(y+1), " on") {
		t.Errorf("the list does not say which way the switches stand:\n%s", wd.screen())
	}
}

// "Run a team..." shows the saved teams. One whose words hold a goal asks
// for it on one line; one that holds none is run at once.
func TestRunATeam(t *testing.T) {
	wd := startWith(t, actions, 110, 30, nil, ours)
	wd.s.Type("\x1b[I")
	wd.shows("KEYS GO HERE")
	pick := func(downs int) {
		wd.key("/")
		wd.s.Type("run a team")
		wd.s.Key("enter")
		wd.ran("team list --json --human")
		wd.over("saved teams")
		for range downs {
			wd.s.Key("down")
		}
	}
	pick(0)
	wd.shows("site-review · Three reviewers and one writer", "2 tasks", "not approved")
	wd.s.Key("enter")
	wd.shows(" Goal for site-review:")
	wd.s.Type("the checkout pages")
	wd.s.Key("enter")
	wd.ran("team run site-review --human --goal=the checkout pages")
	pick(1)
	wd.s.Key("enter")
	wd.ran("team run lint-all --human")
}

// o, or a click on the state word, opens what can be done with a card.
func TestAKeyOpensWhatACardOffers(t *testing.T) {
	wd := startWith(t, fleet, 60, 46, nil, ours)
	wd.s.Type("\x1b[I")
	wd.key("j", "o")
	wd.over("sign-up page")
	wd.shows("Go there", "Stop", "enter runs it")
	wd.s.Key("esc")
	wd.key("?")
	wd.shows("What can be done with a card")
}

// What look round 1 found: a line a command answered is read whole at the
// fleet's usual width, and so is the catch-up when it opens there.
func TestTheFleetCutsNoAnswerShort(t *testing.T) {
	p, _, _ := still(t, fleet, 40, 30)
	p.hint, p.dirty = "Resumed. 2 agents were told to read their mail and carry on.", true
	p.draw()
	_, y := p.find(t, "Resumed. 2 agents were told to read")
	if !strings.Contains(p.t.Row(y+1), "their mail and carry on.") || !strings.Contains(p.t.Row(y+2), "live · ") {
		t.Errorf("the answer is not read whole above the age line:\n%s", frame(p))
	}
	p.hint, p.dirty = "", true
	p.draw()
	p.find(t, "5 done today")

	p.catch.Finished = contract.CatchLine{N: 5, Text: "login page, search box, search results, help pages, site map"}
	p.over, p.dirty = "catchup", true
	p.draw()
	x, y := p.find(t, "Finished and checked   5   login")
	if _, _, ok := p.t.Find("help pages, site map"); !ok || x != 2 || strings.TrimSpace(p.t.Row(y+1)) == "" {
		t.Errorf("the catch-up is cut in the fleet:\n%s", frame(p))
	}
}

// The line that says how long an answer can be taken back does not stay:
// the item counts the seconds itself.
func TestAnAnswerLeavesNoLineBehind(t *testing.T) {
	wd := startWith(t, actions, 150, 20, nil, ours)
	wd.click("[ Yes ]")
	wd.ran(`answer q7 --file "yes" --human`)
	if wd.within(700*time.Millisecond, "taken back") {
		t.Errorf("the pane says:\n%s", wd.screen())
	}
}

// set actions moves an open action pane to the other side at once: it
// changes places with the conversation and takes its own height again.
func TestSetMovesAnOpenActionPane(t *testing.T) {
	d := newDesk(t)
	o, err := d.ui(d.me)
	if err != nil {
		t.Fatal(err)
	}
	run := func(from string, args ...string) string {
		var out strings.Builder
		if _, err := set(&contract.Call{Kit: d.k, Command: contract.Find("set"), Args: args, Caller: contract.Caller{Pane: from}, Out: &out}); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	d.did()
	said := run(d.me, actions, "top")
	if got := d.did(); !strings.Contains(got, "swap "+d.me+" "+o.Actions+"\nresize "+o.Actions+" up 0.5") || strings.Count(got, "resize") != 1 || strings.Contains(said, "next opened") {
		t.Errorf("set actions top: %q\n%s", said, got)
	}
	if run(d.me, actions, "top"); strings.Contains(d.did(), "swap") {
		t.Error("a pane that is on top already was moved")
	}
	// Folded, and asked from the pane itself: down to its strip, and the keys stay.
	run(d.me, "folded", "on")
	run(o.Actions, actions, "bottom")
	if got := d.did(); strings.Count(got, "resize "+o.Actions+" down 0.5") != 2 || d.focus() != o.Actions {
		t.Errorf("set actions bottom from the folded pane: the keys are in %s\n%s", d.focus(), got)
	}
	// Opened anew, the pane is not folded: it has its whole share again.
	d.ui(d.me, "close")
	d.ui(d.me)
	if _, ui, _, _ := screen(d.k); ui.Folded || ui.ActionsPane == "" {
		t.Errorf("after opening the panes again ui.json holds %+v", ui)
	}
	// With no engine to ask, the pane moves when it is next opened, and that is said.
	d.k.Terms = contract.NoTerminals{}
	var out strings.Builder
	set(&contract.Call{Kit: d.k, Command: contract.Find("set"), Args: []string{actions, "top"}, Out: &out})
	if !strings.Contains(out.String(), "next opened") {
		t.Errorf("set actions top with no engine said %q", out.String())
	}
}
