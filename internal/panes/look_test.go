package panes

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// frame is a pane's grid as text, with a line round it.
func frame(p *pane) string {
	edge := "+" + strings.Repeat("-", p.t.W) + "+\n"
	out := edge
	for y := range p.t.H {
		row := p.t.Row(y)
		out += "|" + row + strings.Repeat(" ", max(p.t.W-term.Width(row), 0)) + "|\n"
	}
	return out + edge
}

// TestLook prints both panes at the sizes a person can drag them to, for
// somebody to look at: go test -run TestLook -v with LOOK=1.
func TestLook(t *testing.T) {
	if os.Getenv("LOOK") == "" {
		t.Skip("set LOOK=1 to print the panes")
	}
	for _, w := range []int{24, 32, 40, 56} {
		for _, n := range []int{12, 0, 20} {
			p, v, _ := still(t, fleet, w, 30)
			var cards []contract.Card
			for _, s := range v.Sections {
				cards = append(cards, s.Cards...)
			}
			for i := len(cards); i < n; i++ {
				c := cards[i%12]
				c.Task = fmt.Sprint("X", i)
				cards = append(cards, c)
			}
			view := *v
			view.Sections = []contract.Section{{Name: "ALL", Cards: cards[:n]}}
			view.Counts.Agents = n
			p.make = func(contract.ViewInput) contract.View { return view }
			p.stale, p.focused = true, n == 20
			p.draw()
			fmt.Printf("fleet %dx30, %d agents\n%s", w, n, frame(p))
		}
	}
	for _, h := range []int{2, 3, 8, 15} {
		for _, w := range []int{60, 85, 122} {
			p, _, _ := still(t, actions, w, h)
			fmt.Printf("actions %dx%d\n%s", w, h, frame(p))
		}
	}
}

// What look round 0 found: at every size a pane says as much as fits, and
// what it cuts it marks.
func TestAPaneFitsWhatItSaysToItsSize(t *testing.T) {
	shows := func(p *pane, texts ...string) {
		t.Helper()
		for _, text := range texts {
			if _, _, ok := p.t.Find(text); !ok {
				t.Errorf("a %s pane of %dx%d does not show %q:\n%s", p.kind, p.t.W, p.t.H, text, frame(p))
			}
		}
	}
	p, _, _ := still(t, fleet, 24, 30)
	shows(p, "FLEET  12 · ▲2   [<][>]", "▲ sign-up pa… needs you", "● price list ▲ to check")
	p, _, _ = still(t, fleet, 32, 30)
	shows(p, "FLEET  12 · 2 need you   [<][>]")
	p, _, _ = still(t, fleet, 40, 48)
	shows(p, "FLEET  12 · 2 need you      [<] [>] [x]", "tests written · also changes search.…", "clashes with search box: search.js")
	// The bars are one length, whatever the context bar says.
	x1, _ := p.find(t, " 20%      ctx unknown")
	x2, _ := p.find(t, " 30%    ctx ▮")
	if x1 != x2 {
		t.Errorf("the figures of two bars stand at %d and %d:\n%s", x1, x2, frame(p))
	}
	p, v, _ := still(t, fleet, 40, 30)
	none := *v
	none.Sections, none.Counts = nil, contract.Counts{}
	p.make, p.stale = func(contract.ViewInput) contract.View { return none }, true
	p.draw()
	shows(p, "FLEET  0 · 0 need you       [<] [>] [x]", "no agents yet")

	// The action pane: the strip in three lengths, and what does not fit
	// whole on a line of its own.
	p, _, _ = still(t, actions, 122, 8)
	shows(p, "3 waiting · 2 hold work up", "[ Catch me up ]", "[ Yes ]", "▲ photo upload · Which sizes should a photo be kept in?", "● price list · Prices load")
	p, _, _ = still(t, actions, 85, 6)
	shows(p, "3 waiting · 2 hold work up", "[Catch up]", "[ Yes ]", "+2 more · j k")
	p, _, _ = still(t, actions, 60, 3)
	shows(p, "ACTIONS  3 · ▲2", "[Stop all]", "▲ sign-up page · Must the old sign-up link keep working?")
	p, _, _ = still(t, actions, 60, 4)
	shows(p, "▲ sign-up page · Must", "+2 more · j k")
}
