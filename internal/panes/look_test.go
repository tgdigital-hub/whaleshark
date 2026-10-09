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
