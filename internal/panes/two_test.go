package panes

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/theme"
)

// What Do not disturb keeps back is still listed and can still be answered:
// dim, after what waits, under the line that counts it. The strip counts it
// too, and when Do not disturb ends the pane unfolds.
func TestWhatDoNotDisturbKeepsBack(t *testing.T) {
	p, view, _ := still(t, actions, 110, 30)
	dim := theme.Get("reef").Ink("dim")
	all := view.Items
	view.Items, view.Held, view.Strip.DND, p.stale = all[:2], all[2:], true, true
	p.draw()
	p.find(t, "3 waiting · 2 hold work up · 1 held")
	_, y := p.find(t, "held: 1 · do not disturb is on")
	x, at := p.find(t, "● price list")
	if at != y+1 || p.t.At(x, at).Style != dim || p.t.At(x+2, at).Style != dim {
		t.Errorf("the held item stands in row %d under the line in row %d, drawn %+v", at, y, p.t.At(x, at).Style)
	}
	if x, y := p.find(t, "▲ photo upload"); p.t.At(x, y).Style == dim {
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

// The settings are a screen of their own, and that screen is a row of the
// list of every action beside the left section's own rows: chosen, it is
// laid over the tab.
func TestTheSettingsScreenIsARowOfTheActionList(t *testing.T) {
	wd := startWith(t, actions, 110, 48, nil, ours)
	wd.s.Type("\x1b[I")
	wd.shows("KEYS GO HERE")
	wd.key("/")
	wd.shows(fmt.Sprint(len(contract.Actions)+len(contract.SideActions)-2, " actions · type to search"))
	wd.s.Type("settings screen")
	wd.shows("1 actions")
	wd.s.Key("enter")
	wd.asked("ui run settings")
	wd.key("/")
	wd.s.Type("fold the left")
	wd.shows("1 actions")
	wd.s.Key("enter")
	wd.asked("dock " + contract.DockFold)
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
	wd.ran(`team run site-review --goal --file "the checkout pages" --human`)
	pick(1)
	wd.s.Key("enter")
	wd.ran(`team run lint-all --goal --file "" --human`)
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

// A line a command answered is read whole at the
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
func TestSetActionsIsAnotherNameOfTheLayoutsKey(t *testing.T) {
	d := newDesk(t)
	d.double.Width, d.k.Panes = 200, Panes
	o, err := d.ui(d.me)
	if err != nil {
		t.Fatal(err)
	}
	d.did()
	var out strings.Builder
	if _, err := set(&contract.Call{Kit: d.k, Command: contract.Find("set"), Args: []string{actions, "top"}, Caller: contract.Caller{Pane: d.me}, Out: &out}); err != nil {
		t.Fatal(err)
	}
	if got := d.did(); !strings.Contains(got, "pane-close "+o.Actions) || !strings.Contains(got, "split "+d.me+" up 0.75") || !strings.Contains(out.String(), "actions: top") {
		t.Errorf("set actions top: %q\n%s", out.String(), got)
	}
	// Opened anew, the pane is not folded: it has its whole share again.
	if _, ui, _, _ := screen(d.k); ui.Folded || only(ui).Actions == "" || only(ui).Actions == o.Actions {
		t.Errorf("after the action pane moved ui.json holds %+v", ui)
	}
	// With no engine to ask, the setting is kept and the pane opens there next time.
	d.k.Terms = contract.NoTerminals{}
	if _, err := set(&contract.Call{Kit: d.k, Command: contract.Find("set"), Args: []string{actions, "bottom"}, Out: &out}); err != nil {
		t.Errorf("set actions bottom with no engine: %v", err)
	}
}

// The fleet prints the view's plain sentences under the alerts, and a card
// the person has not looked at since it changed has its name in bold.
func TestTheFleetShowsTheLinesAndWhatIsUnseen(t *testing.T) {
	p, _, _ := still(t, fleet, 60, 40)
	p.view.Lines = []string{"to check: 3, about 6 min one by one, about 2 min together"}
	p.sel = ""
	for i := range p.cards {
		p.cards[i].Unseen = i == 0
	}
	p.dirty = true
	p.draw()
	p.find(t, "to check: 3, about 6 min one by one")
	for i, c := range p.cards[:2] {
		x, y := p.find(t, c.Name)
		if got := p.t.At(x, y).Style.Bold; got != (i == 0) {
			t.Errorf("%s is bold: %v\n%s", c.Name, got, frame(p))
		}
	}
}

// A phone that asks to be paired is an item of the login, not of a run: the
// pane sees the phones' file change without being told, hands it to the
// builder, and its two buttons are rows of the action table that carry the
// item's id to `dash phone`.
func TestAPhoneThatAsksInTheActionPane(t *testing.T) {
	wd := startWith(t, actions, 110, 30, nil, ours)
	wd.s.Type("\x1b[I")
	wd.shows("KEYS GO HERE")
	asking := &contract.Pairing{Number: "493 201", Identity: "robin@example.com", Name: "iPhone", At: wd.fx.Now}
	if err := contract.ChangePhones(wd.sys, func(list *contract.Phones) error { list.Asking, list.Shut = asking, wd.fx.Now; return nil }); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		wd.mu.Lock()
		got := wd.in.Phones.Asking
		if got != nil && got.Number == asking.Number {
			// What the builder makes of it is the first item from now on.
			wd.view.Items = append(wd.in.Phones.Items(wd.fx.Now, true), wd.view.Items...)
			wd.mu.Unlock()
			break
		}
		wd.mu.Unlock()
		if time.Now().After(end) {
			t.Fatal("the pane never handed the phone that asks to the builder")
		}
	}
	wd.touch()
	wd.shows("A phone wants to connect: 493 201 · robin@example.com · iPhone", "Requests without a valid pass are reaching your page")
	for range 6 { // up to the first item, wherever the selection stood
		wd.key("k")
	}
	// The door's warning has no button: a key on it starts nothing.
	wd.key("y")
	wd.key("j")
	wd.key("y")
	wd.ran("dash phone approve pair493201")
	wd.key("n")
	wd.ran("dash phone refuse pair493201")
}
