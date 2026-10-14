package panes

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/term/termtest"
	"github.com/tgdigital-hub/whaleshark/internal/theme"
	"github.com/tgdigital-hub/whaleshark/internal/view"
)

// afternoon is the left section without a loop: the afternoon of three
// workspaces as the real builder makes it of the fixture, drawn once at one
// size, two seconds after everything was last checked, for a login whose
// first steps are long done.
func afternoon(t *testing.T, w, h int, change func(*contract.SideInput)) *pane {
	f := testkit.Spaces()
	if change != nil {
		change(&f.In)
	}
	k := contract.NewKit()
	k.Side, k.Platform = view.Side, system(t.TempDir(), nil)
	p := newPane(side, termtest.New(w, h).Term, k, "", "", func() time.Time { return f.In.Now.Add(2 * time.Second) })
	p.plainMarks, p.me, p.in, p.picture, p.cfg = false, "dock", f.In, f.In.Terms, f.In.Person
	p.filesOK, p.termsOK, p.watch = f.In.Now, f.In.Now, noticed{}
	p.ui.FirstSteps = contract.Off
	p.draw()
	return p
}

// more gives the afternoon n more workspaces, each of one tab.
func more(n int) func(*contract.SideInput) {
	return func(in *contract.SideInput) {
		for i := range n {
			id := fmt.Sprint("x", i)
			in.Terms.Spaces = append(in.Terms.Spaces, contract.Space{ID: id, Root: "/w/" + id, Name: "project " + id, Tab: "t-" + id})
			in.Terms.Panes = append(in.Terms.Panes, contract.Pane{ID: "p-" + id, Tab: "t-" + id, Label: contract.MainTab, Cwd: "/w/" + id, Workspace: id})
		}
	}
}

// The left section as the design draws it for a window of 160, of 120 and
// of 80 columns, thirty rows high: 28, 22 and 14 cells wide. A row a
// workspace with a thin line between every two, and the agents of the one
// in front from the half-way mark, under the heavier line. The counts are
// the fixture's, which counts a workspace's lead agent, and the words the
// model's, which gives an idle agent no time.
var frames = []struct {
	w    int
	want string
}{
	{28, `
 WHALESHARK              ▲2

 WORKSPACES             [+]
 ▌tomsleeps  main     ▲1 4 ≡
 ──────────────────────────
  TestRunFoldersAndPoint… 1
 ──────────────────────────
  ops-portal  main     ▲1 5







━━━━━━━━━━━━━━━━━━━━━━━━━━━━
 AGENTS · tomsleeps
  ● orchestrator
  ▲ sign-up page  needs you
 ▌◐ login page    building ≡
  ◌ help pages     idle 12m







 [ Menu ]  [ Settings ]
 live · checked 2s ago`},
	{22, `
 WHALESHARK        ▲2

 WORKSPACES
 ▌tomsleeps     ▲1 4 ≡
 ────────────────────
  TestRunFoldersAn… 1
 ────────────────────
  ops-portal     ▲1 5







━━━━━━━━━━━━━━━━━━━━━━
 AGENTS · tomsleeps
  ● orchestrator
  ▲ sign-up page
 ▌◐ login page       ≡
  ◌ help pages







 [Menu] [Settings]
 live · 2s`},
	{14, `
 WHALESHAR ▲2
 WORKSPACES
 ▌tomsl… ▲1 4
 ────────────
  TestRunF… 1
 ────────────
  ops-p… ▲1 5








━━━━━━━━━━━━━━
 AGENTS
  ● orchestr…
  ▲ sign-up …
 ▌◐ login pa…
  ◌ help pag…







 [≡] [Set]
 live · 2s`},
}

func TestTheLeftSectionIsTheFramesInEverySchemeAndInNone(t *testing.T) {
	for _, f := range frames {
		for _, s := range theme.Schemes {
			p := afternoon(t, f.w, 30, nil)
			p.scheme = s
			p.draw()
			shot(fmt.Sprint("halves-", f.w), p)
			if got := grid(p); got != strings.TrimPrefix(f.want, "\n") {
				t.Errorf("%d cells wide in the scheme %s the left section is\n%s\nwant%s", f.w, s.Name, got, f.want)
			}
		}
	}
}

// One workspace has its row, no line, and blank rows to the heavier line,
// which stands where it stood; more than the upper panel holds end in a row
// that counts the rest, the one in front keeps its row, and a click on that
// row unfolds them all over the agents until one is chosen.
func TestOneWorkspaceAndMoreThanFit(t *testing.T) {
	one := afternoon(t, 28, 30, func(in *contract.SideInput) {
		in.Terms.Spaces, in.Terms.Panes = in.Terms.Spaces[:1], in.Terms.Panes[:4]
	})
	if got := grid(one); strings.Contains(got, "──") || !strings.HasPrefix(one.t.Row(15), "━━━━") || strings.TrimSpace(one.t.Row(4)) != "" {
		t.Errorf("one workspace is drawn\n%s", got)
	}
	p := afternoon(t, 28, 30, more(12))
	got := grid(p)
	if !strings.Contains(got, "· 10 more") || !strings.Contains(got, "▌tomsleeps") || !strings.HasPrefix(p.t.Row(15), "━━━━") || strings.Count(got, "project x") != 2 {
		t.Fatalf("fifteen workspaces are drawn\n%s", got)
	}
	_, y := p.find(t, "· 10 more")
	p.event(term.Event{Kind: term.Mouse, Button: term.Left, Action: term.Press, X: 3, Y: y})
	p.draw()
	if got := grid(p); strings.Count(got, "project x") != 10 || strings.Contains(got, "━") || strings.Contains(got, "AGENTS") {
		t.Errorf("unfolded, the workspaces are drawn\n%s", got)
	}
	// The wheel moves the panel, and a workspace chosen folds it again.
	mouse(p, term.WheelDown, term.Press, 3, 5)
	mouse(p, term.WheelDown, term.Press, 3, 5)
	if got := grid(p); strings.Count(got, "project x") != 12 || strings.Contains(got, "tomsleeps") {
		t.Errorf("unfolded and moved by the wheel, the workspaces are drawn\n%s", got)
	}
	over, cancel := context.WithCancel(context.Background())
	cancel() // no loop hears what the keeper answers
	p.bg = over
	p.front(contract.Workspace{})
	if p.draw(); p.unfold || !strings.Contains(grid(p), "AGENTS · tomsleeps") {
		t.Errorf("after a workspace was chosen the section is\n%s", grid(p))
	}
}

// The heavier line stands on the row the setting says, in hundredths of the
// section's height, held to the least of the two panels.
func TestTheHeavierLineStandsWhereTheSettingSays(t *testing.T) {
	for _, h := range []int{24, 30, 44} {
		for _, split := range []int{0, 20, 37, 80} {
			p := afternoon(t, 28, h, nil)
			p.ui.SideSplit, p.stale = split, true
			p.draw()
			want := contract.SplitRow(h, split, contract.SideHead, contract.SideFoot)
			for y := range h {
				if line := strings.HasPrefix(p.t.Row(y), "━"); line != (y == want) {
					t.Errorf("%d rows, split %d: row %d is %q, the line belongs on row %d", h, split, y, p.t.Row(y), want)
				}
			}
			if _, y := p.find(t, "AGENTS"); y != want+1 {
				t.Errorf("%d rows, split %d: the agents begin on row %d", h, split, y)
			}
		}
	}
}

// mouse gives a pane without a loop one report of the pointer.
func mouse(p *pane, b term.Button, a term.Action, x, y int) {
	p.event(term.Event{Kind: term.Mouse, Button: b, Action: a, X: x, Y: y})
	p.draw()
}

// While the heavier line is held it follows the pointer, in its own colour, and
// says the rows over it; at a limit it says which; under the pointer with no
// button down it is lit; a press let go where it was leaves it waiting.
func TestTheHeldLineSaysItsRowsAndItsLimit(t *testing.T) {
	p := afternoon(t, 28, 30, nil)
	accent, frame := bold(p.st("blocked")), p.st("frame") // the lit line's own colour
	if st := p.t.At(5, 15).Style; st != frame {
		t.Errorf("the line at rest is drawn %+v", st)
	}
	mouse(p, term.NoButton, term.Move, 5, 15)
	if st := p.t.At(5, 15).Style; st != accent {
		t.Errorf("the line under the pointer is drawn %+v", st)
	}
	mouse(p, term.Left, term.Press, 5, 15)
	mouse(p, term.Left, term.Move, 5, 11)
	if row := p.t.Row(11); !strings.HasPrefix(row, "━ ▲▼ 11 rows ━━") || strings.Contains(grid(p), "━━\n AGENTS") == false {
		t.Errorf("held on row 11 the section is\n%s", grid(p))
	}
	mouse(p, term.Left, term.Move, 5, 2)
	least := contract.SplitRow(30, contract.SideSplitMin, contract.SideHead, contract.SideFoot)
	if row := p.t.Row(least); !strings.HasPrefix(row, fmt.Sprintf("━ ▲▼ %d rows · the least ━", least)) {
		t.Errorf("held above its least the line is %q", row)
	}
	mouse(p, term.Left, term.Move, 5, 29)
	most := contract.SplitRow(30, contract.SideSplitMax, contract.SideHead, contract.SideFoot)
	if row := p.t.Row(most); !strings.HasPrefix(row, fmt.Sprintf("━ ▲▼ %d rows · the most ━", most)) {
		t.Errorf("held under its most the line is %q", row)
	}
	narrow := afternoon(t, 14, 30, nil)
	mouse(narrow, term.Left, term.Press, 5, 15)
	mouse(narrow, term.Left, term.Move, 5, 11)
	if row := narrow.t.Row(11); row != "━ ▲▼ 11 ━━━━━━" {
		t.Errorf("held in a section of 14 cells the line is %q", row)
	}
	mouse(narrow, term.Left, term.Release, 5, 11)
	mouse(narrow, term.Left, term.Press, 5, 11)
	mouse(narrow, term.Left, term.Release, 5, 11)
	if row := narrow.t.Row(11); !narrow.waits || !strings.HasPrefix(row, "━ click whe") {
		t.Errorf("clicked and not dragged the line is %q", row)
	}
	// The form of a terminal that cannot show the marks.
	narrow.plainMarks = true
	narrow.draw()
	if row := narrow.t.Row(11); !strings.HasPrefix(row, "# click") || !strings.HasSuffix(row, "#") || !strings.Contains(grid(narrow), " ------------") {
		t.Errorf("in plain marks the section is\n%s", grid(narrow))
	}
}

// No scheme paints a ground under the two halves or under a menu.
func TestTheHalvesAndAMenuStandOnTheTerminalsGround(t *testing.T) {
	menu := boxOf(t, 40, 12, contract.OfferSpace, "s3")
	for _, p := range []*pane{afternoon(t, 28, 30, nil), menu} {
		for _, s := range theme.Schemes {
			p.scheme = s
			p.draw()
			for i := range p.t.W * p.t.H {
				if st := p.t.At(i%p.t.W, i/p.t.W).Style; st.Bg != 0 {
					t.Fatalf("%s in %s: the cell at %d,%d has a ground: %+v", p.kind, s.Name, i%p.t.W, i/p.t.W, st)
				}
			}
		}
	}
}
