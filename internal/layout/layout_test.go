package layout

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

var update = flag.Bool("update", false, "rewrite the picture files")

// The five window sizes every layout is drawn at: the design's wide and
// narrow screens, the classic terminal, a small one, and one too small for
// most layouts.
var sizes = [][2]int{{150, 44}, {100, 29}, {80, 24}, {50, 15}, {24, 8}}

var look = Look{
	Text:   term.Style{Fg: term.RGB(1)},
	Dim:    term.Style{Fg: term.RGB(2)},
	Frame:  term.Style{Fg: term.RGB(3)},
	Accent: term.Style{Fg: term.RGB(4)},
}

// shop is the design's evening with thirteen tabs.
const shop = `tab t1 p1 Lead
tab t2 q2 sign-up page
tab t3 q3 photo upload
tab t4 q4 email sender
tab t5 q5 price list
tab t6 q6 login page
tab t7 q7 search box
tab t8 q8 search results
tab t9 q9 checkout form
tab t10 q10 help pages
tab t11 q11 site map
tab t12 q12 order emails
tab t13 q13 admin page
`

// approved is the screen of the design: the fleet on the right, the action
// pane under the conversation.
const approved = shop + "split p1 right 0.7 p2\nsplit p1 down 0.72 p3\n"

// grid is one tab of cols by rows panes of equal shares.
func grid(cols, rows int) string {
	var b strings.Builder
	b.WriteString("tab t1 p1 Lead\n")
	n := 1
	for c := 1; c < cols; c++ {
		fmt.Fprintf(&b, "split p%d right %g p%d\n", n, 1/float64(cols-c+1), n+1)
		n++
	}
	for c := 1; c <= cols; c++ {
		top := c
		for r := 1; r < rows; r++ {
			n++
			fmt.Fprintf(&b, "split p%d down %g p%d\n", top, 1/float64(rows-r+1), n)
			top = n
		}
	}
	return b.String()
}

var layouts = []struct{ name, script string }{
	{"one-pane", "tab t1 p1 Lead"},
	{"right-half", "tab t1 p1 Lead\nsplit p1 right 0.5 p2"},
	{"down-half", "tab t1 p1 Lead\nsplit p1 down 0 p2"},
	{"left-third", "tab t1 p1 Lead\nsplit p1 left 0.67 p2"},
	{"up-quarter", "tab t1 p1 Lead\nsplit p1 up 0.75 p2"},
	{"approved-screen", approved},
	{"fleet-36-columns", shop + "split p1 right 36 p2\nsplit p1 down 0.72 p3"},
	{"actions-folded", shop + "split p1 right 0.7 p2\nsplit p1 down 2 p3"},
	{"actions-on-top", shop + "split p1 right 0.7 p2\nsplit p1 up 0.72 p3"},
	{"actions-on-top-folded", shop + "split p1 right 36 p2\nsplit p1 up 2 p3"},
	{"fleet-on-the-left", shop + "split p1 left 36 p2\nsplit p1 down 0.72 p3"},
	{"three-columns", grid(3, 1)},
	{"three-rows", grid(1, 3)},
	{"two-by-two", grid(2, 2)},
	{"three-by-three", grid(3, 3)},
	{"twenty-panes", grid(4, 5)},
	{"spiral", "tab t1 p1 Lead\nsplit p1 right 0.4 p2\nsplit p2 down 0.4 p3\nsplit p3 left 0.4 p4\nsplit p4 up 0.4 p5\nsplit p5 right 0.4 p6"},
	{"thin-shares", "tab t1 p1 Lead\nsplit p1 right 0.98 p2\nsplit p1 down 0.02 p3"},
	{"fixed-wider-than-the-window", "tab t1 p1 Lead\nsplit p1 right 500 p2\nsplit p1 down 500 p3"},
	{"fixed-on-both-sides", "tab t1 p1 Lead\nsplit p1 right 20 p2\nsplit p1 left 20 p3"},
	{"fixed-above-and-below", "tab t1 p1 Lead\nsplit p1 up 3 p2\nsplit p1 down 3 p3"},
	{"zoomed", approved + "zoom p2"},
	{"zoom-undone", approved + "zoom p2\nzoom p2"},
	{"zoom-ended-by-focus", approved + "zoom p2\nfocus p3"},
	{"keys-in-the-middle", grid(3, 3) + "focus p6"},
	{"keys-in-a-corner", grid(3, 3) + "focus p9"},
	{"swapped", approved + "swap p1 p3"},
	{"closed-in-the-middle", grid(3, 1) + "close p2"},
	{"closed-with-the-keys", approved + "focus p3\nclose p3"},
	{"closed-to-one", approved + "close p1\nclose p3"},
	{"resized-right", approved + "resize p1 right 0.1"},
	{"resized-at-the-edge", approved + "resize p2 right 0.1\nresize p3 down 0.1"},
	{"resized-to-the-least", approved + "resize p1 right 1\nresize p1 down 1"},
	{"dragged-across", "tab t1 p1 Lead\nsplit p1 right 0.5 p2\ndrag 0.5 0.5 0.25 0.5"},
	{"dragged-down", "tab t1 p1 Lead\nsplit p1 down 0.5 p2\ndrag 0.5 0.5 0.5 0.8"},
	{"dragged-fixed", shop + "split p1 right 36 p2\ndrag -37 5 -50 5"},
	{"tab-row-wheeled", approved + "wheel 3"},
	{"last-tab-shown", approved + "show t13"},
	{"tab-clicked", approved + "click 10 0"},
	{"names-cleaned", "tab t1 p1 \x1b[31mred\x1b[0m alert\ntab t2 p2 海の家\ntab t3 p3\ntab t4 p4 a name far longer than any tab row should give to one tab\ntab t5 p5 tab\there"},
	{"plain-marks", approved + "plain\nfocus p3"},
	{"second-tab", "tab t1 p1 Lead\ntab t2 p2 sign-up page\nsplit p2 down 0.6 p3\nsplit p1 right 0.5 p4\nshow t2"},
	{"tab-removed", approved + "remove t1"},
	{"built-wide-shown-here", "fit 150 44\n" + grid(4, 5)},
	{"no-tabs", ""},
}

// play runs a script on a layout of a window's size and returns what was
// refused. A drag or a click names a cell as Split names a size: under 1 a
// share of the window, else cells, and below 0 cells from the far edge.
func play(t *testing.T, l *Layout, k *Look, script string, w, h int) (refused []string) {
	t.Helper()
	l.Fit(w, h)
	num := func(s string) float64 {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatalf("%q in a script: %v", s, err)
		}
		return v
	}
	one := func(s string, all int) int {
		switch v := num(s); {
		case v < 0:
			return all + int(v)
		case v < 1:
			return int(v * float64(all))
		default:
			return int(v)
		}
	}
	cell := func(x, y string) (int, int) { return one(x, l.W), one(y, l.H) }
	for _, line := range strings.Split(strings.TrimSpace(script), "\n") {
		f := strings.SplitN(line, " ", 4)
		var err error
		switch f[0] {
		case "":
		case "tab":
			l.Add(f[1], strings.Join(f[3:], " "), f[2])
		case "split":
			g := strings.Fields(f[3])
			err = l.Split(f[1], f[2], num(g[0]), g[1])
		case "close":
			l.Close(f[1])
		case "swap":
			err = l.Swap(f[1], f[2])
		case "zoom":
			l.Zoom(f[1])
		case "focus":
			l.Focus(f[1])
		case "show":
			l.Show(f[1])
		case "remove":
			l.Remove(f[1])
		case "resize":
			err = l.Resize(f[1], f[2], num(f[3]))
		case "wheel":
			l.Wheel(int(num(f[1])))
		case "fit":
			l.Fit(int(num(f[1])), int(num(f[2])))
		case "plain":
			k.Plain = true
		case "drag":
			g := strings.Fields(f[3])
			x, y := cell(f[1], f[2])
			tx, ty := cell(g[0], g[1])
			l.Drag(l.At(x, y), tx, ty)
		case "click":
			switch hit := l.At(cell(f[1], f[2])); hit.Kind {
			case OnTab:
				l.Show(hit.Tab)
			case OnMore:
				l.Wheel(hit.Step)
			case OnPane:
				l.Focus(hit.Pane)
			}
		default:
			t.Fatalf("no such step %q", line)
		}
		if err != nil {
			refused = append(refused, line+": "+err.Error())
		}
	}
	l.Fit(w, h)
	return refused
}

// heavy is how a picture file shows a line in the accent.
var heavy = strings.NewReplacer("│", "┃", "─", "━", "┼", "╋", "├", "┣", "┤", "┫", "┬", "┳", "┴", "┻", "|", "!", "-", "=", "+", "#")

// picture draws a layout as text, each pane's place named by its id and size.
func picture(l *Layout, k Look) string {
	g := term.NewGrid(l.W, l.H)
	l.Draw(g, k)
	if t := l.shown(); t != nil {
		for _, p := range l.Places(t) {
			if p.H > 0 {
				g.Put(p.X, p.Y, p.W, fmt.Sprintf("%s %dx%d", p.Pane, p.W, p.H), k.Text)
			}
		}
	}
	var b strings.Builder
	for y := range g.H {
		var row strings.Builder
		for x := range g.W {
			c := g.At(x, y)
			if c.Style == k.Accent {
				c.Text = heavy.Replace(c.Text)
			}
			row.WriteString(c.Text)
		}
		b.WriteString(strings.TrimRight(row.String(), " ") + "\n")
	}
	return b.String()
}

// TestPictures draws every layout at every size and compares each with its file.
func TestPictures(t *testing.T) {
	if len(layouts) < 40 {
		t.Fatalf("%d layouts, and the check asks for forty", len(layouts))
	}
	for i, c := range layouts {
		var got strings.Builder
		for _, s := range sizes {
			l, k := &Layout{}, look
			refused := play(t, l, &k, c.script, s[0], s[1])
			sound(t, l)
			keys, zoom := "", ""
			if tab := l.shown(); tab != nil {
				keys, zoom = tab.Focus, tab.Zoom
			}
			fmt.Fprintf(&got, "== %dx%d keys=%s zoom=%s\n", s[0], s[1], keys, zoom)
			for _, r := range refused {
				fmt.Fprintf(&got, "refused: %s\n", r)
			}
			got.WriteString(picture(l, k))
		}
		path := filepath.Join("testdata", fmt.Sprintf("%02d-%s.txt", i+1, c.name))
		if *update {
			if err := os.WriteFile(path, []byte(got.String()), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got.String() != strings.ReplaceAll(string(want), "\r\n", "\n") {
			t.Errorf("%s differs from its file:\n%s", c.name, got.String())
		}
	}
}

// sound fails the test unless the layout holds everything a layout must: no
// pane with a negative side, outside the window or on another's cells; every
// pane at least the smallest size when the window has the room; every cell
// under the tab row a pane's or a line's, and At and Draw agreeing which.
func sound(t *testing.T, l *Layout) {
	t.Helper()
	if !l.Sound() {
		t.Fatalf("not sound: %+v", l)
	}
	area := l.area()
	for _, tab := range l.Tabs {
		places := l.Places(tab)
		fits := tab.Root.least(false) <= area.W && tab.Root.least(true) <= area.H
		if tab.Zoom != "" {
			fits = area.W >= LeastW && area.H >= LeastH
		}
		for i, p := range places {
			if p.W < 0 || p.H < 0 || p.meet(area) != p.W*p.H {
				t.Fatalf("%s has the place %+v in %+v", p.Pane, p.Rect, area)
			}
			if fits && (p.W < LeastW || p.H < LeastH) {
				t.Fatalf("%s is %dx%d though the window has room", p.Pane, p.W, p.H)
			}
			for _, o := range places[:i] {
				if p.meet(o.Rect) != 0 {
					t.Fatalf("%s at %+v and %s at %+v overlap", p.Pane, p.Rect, o.Pane, o.Rect)
				}
			}
		}
	}
	g := term.NewGrid(l.W, l.H)
	l.Draw(g, look)
	tab := l.shown()
	if tab == nil {
		return
	}
	cells := map[string]int{}
	for y := area.Y; y < area.Y+area.H; y++ {
		for x := range area.W {
			drawn := g.At(x, y).Text != " "
			switch h := l.At(x, y); {
			case h.Kind == OnPane && !drawn:
				cells[h.Pane]++
			case h.Kind != OnLine || !drawn:
				t.Fatalf("cell %d,%d is %+v and drawn is %v", x, y, h, drawn)
			}
		}
	}
	for _, p := range l.Places(tab) {
		if h := l.At(p.X+p.W-1, p.Y+p.H-1); p.W*p.H > 0 && (h.Pane != p.Pane || h.X != p.W-1 || h.Y != p.H-1) {
			t.Fatalf("the last cell of %s at %+v is %+v", p.Pane, p.Rect, h)
		}
		if cells[p.Pane] != p.W*p.H {
			t.Fatalf("%s has %d cells and the place %+v", p.Pane, cells[p.Pane], p.Rect)
		}
	}
	l.Draw(term.NewGrid(l.W/2, l.H/2), look)
	l.Draw(term.NewGrid(l.W+3, l.H+3), look)
}

// drive does steps things to a layout, each chosen by next, and checks the
// layout after every one. next(n) is a number from 0 to n-1.
func drive(t *testing.T, next func(n int) int, steps int) {
	l := &Layout{}
	ids := 0
	id := func() string { ids++; return "p" + strconv.Itoa(ids) }
	pane := func() string { return "p" + strconv.Itoa(next(ids+2)) }
	dir := func() string {
		return []string{contract.Right, contract.Down, contract.Left, contract.Up, "round"}[next(5)]
	}
	amount := func() float64 {
		return []float64{0, 0.02, 0.3, 0.5, 0.9, 1, 2, 7, 36, 4000, -1, math.NaN(), math.Inf(1)}[next(13)]
	}
	for range steps {
		switch next(14) {
		case 0:
			l.Fit(next(130)-2, next(44)-2)
		case 1:
			if len(l.Tabs) < 6 {
				l.Add("t"+id(), "tab "+strconv.Itoa(ids), id())
			}
		case 2, 3, 4:
			err := l.Split(pane(), dir(), amount(), id())
			if err != nil && !errors.Is(err, contract.ErrNoPane) && err != ErrNoRoom && err != ErrDirection {
				t.Fatal(err)
			}
		case 5:
			l.Close(pane())
		case 6:
			l.Swap(pane(), pane())
		case 7:
			l.Zoom(pane())
		case 8:
			l.Focus(pane())
		case 9:
			l.Resize(pane(), dir(), amount()-0.4)
		case 10:
			l.Drag(l.At(next(l.W+1), next(l.H+1)), next(l.W+1), next(l.H+1))
		case 11:
			l.Wheel(next(7) - 3)
		case 12:
			l.Show("tp" + strconv.Itoa(next(ids+1)))
		case 13:
			l.Remove("tp" + strconv.Itoa(next(ids+1)))
		}
		sound(t, l)
	}
}

// TestNoPlaceNegativeOrShared is the property: whatever is done to a layout,
// at whatever window size, no pane gets a negative or an overlapping place.
func TestNoPlaceNegativeOrShared(t *testing.T) {
	for seed := range int64(200) {
		r := rand.New(rand.NewSource(seed))
		drive(t, r.Intn, 100)
	}
}

func FuzzLayout(f *testing.F) {
	f.Add([]byte{1, 0, 150, 44, 2, 0, 0, 3, 3, 1, 1, 2, 10, 80, 20, 40, 20, 7, 1, 0, 9, 9})
	f.Add([]byte("a layout, driven by any bytes at all, stays sound"))
	f.Fuzz(func(t *testing.T, data []byte) {
		i := 0
		drive(t, func(n int) int {
			i++
			return int(data[(i-1)%len(data)]+byte(i/len(data))) % n
		}, min(len(data), 200))
	})
}

// TestFixedKeepsItsCells: a pane given a size in cells has it at every window
// size that has the room, and a share follows the window.
func TestFixedKeepsItsCells(t *testing.T) {
	l := &Layout{}
	l.Add("t1", "Lead", "p1")
	if err := l.Split("p1", contract.Right, 36, "p2"); err != nil {
		t.Fatal(err)
	}
	if err := l.Split("p1", contract.Up, 0.75, "p3"); err != nil {
		t.Fatal(err)
	}
	for _, s := range [][2]int{{150, 44}, {100, 29}, {60, 12}, {300, 90}, {150, 44}} {
		l.Fit(s[0], s[1])
		for _, p := range l.Places(l.Tabs[0]) {
			switch {
			case p.Pane == "p2" && (p.W != 36 || p.H != s[1]-2 || p.X != s[0]-36):
				t.Errorf("at %v the fixed pane is at %+v", s, p.Rect)
			case p.Pane == "p1" && (p.W != s[0]-37 || p.H != s[1]-3-int(0.25*float64(s[1]-3)+0.5)):
				t.Errorf("at %v the shared pane is at %+v", s, p.Rect)
			}
		}
	}
	l.Fit(40, 20)
	if got := l.Places(l.Tabs[0]); got[2].W != 40-1-LeastW || l.Tabs[0].Root.Cells != 36 {
		t.Errorf("in a window of 40 columns the fixed pane is %+v and the tree asks for %d", got[2].Rect, l.Tabs[0].Root.Cells)
	}
	if err := l.Resize("p2", contract.Left, 0.25); err != nil || l.Tabs[0].Root.Cells != 31 {
		t.Errorf("a fixed pane resized is %d cells, %v", l.Tabs[0].Root.Cells, err)
	}
}

// TestRefusals: what each call refuses, and that a refusal changes nothing.
func TestRefusals(t *testing.T) {
	l := &Layout{}
	l.Add("t1", "Lead", "p1")
	l.Add("t2", "other", "p2")
	before, _ := json.Marshal(l)
	for name, c := range map[string]struct {
		got  error
		want error
	}{
		"split of no pane":    {l.Split("p9", contract.Right, 0.5, "p3"), contract.ErrNoPane},
		"split round":         {l.Split("p1", "round", 0.5, "p3"), ErrDirection},
		"swap with no pane":   {l.Swap("p1", "p9"), contract.ErrNoPane},
		"swap across tabs":    {l.Swap("p1", "p2"), ErrOtherTab},
		"resize of no pane":   {l.Resize("p9", contract.Left, 0.1), contract.ErrNoPane},
		"resize round":        {l.Resize("p1", "round", 0.1), ErrDirection},
		"resize with no line": {l.Resize("p1", contract.Left, 0.1), ErrNoLine},
	} {
		if !errors.Is(c.got, c.want) {
			t.Errorf("%s: %v, want %v", name, c.got, c.want)
		}
	}
	if l.Focus("p9") || l.Zoom("p9") || l.Show("t9") || l.Close("p9") {
		t.Error("a pane or tab nobody has was found")
	}
	l.Fit(40, 6)
	if err := l.Split("p1", contract.Down, 0.5, "p3"); err != ErrNoRoom {
		t.Errorf("a split of four rows: %v", err)
	}
	if err := l.Split("p1", contract.Right, 0.5, "p3"); err != nil {
		t.Errorf("a split of forty columns: %v", err)
	} else if err := l.Split("p1", contract.Right, 0.5, "p4"); err != nil {
		t.Errorf("a split of nineteen columns: %v", err)
	} else if err := l.Split("p1", contract.Right, 0.5, "p5"); err != ErrNoRoom {
		t.Errorf("a split of nine columns: %v", err)
	}
	l.Close("p3")
	l.Close("p4")
	l.Fit(0, 0)
	if after, _ := json.Marshal(l); string(after) != string(before) {
		t.Errorf("the refusals left\n%s\nwhere there was\n%s", after, before)
	}
}

// TestLook: which colour of the scheme each thing is drawn in.
func TestLook(t *testing.T) {
	l, k := &Layout{}, look
	play(t, l, &k, approved, 100, 29)
	g := term.NewGrid(100, 29)
	l.Draw(g, k)
	bold := k.Accent
	bold.Bold = true
	for _, c := range []struct {
		text string
		dx   int
		want term.Style
	}{
		{"Lead", 0, bold}, {"sign-up", 0, k.Text}, {"Lead │", 5, k.Frame}, {"+6", 0, k.Dim},
	} {
		x, y, ok := g.Find(c.text)
		if got := g.At(x+c.dx, y).Style; !ok || got != c.want {
			t.Errorf("%q is drawn as %+v, want %+v", c.text, got, c.want)
		}
	}
	places := l.Places(l.Tabs[0])
	lead, fleet := places[0], places[2]
	for name, c := range map[string]struct {
		x, y int
		want term.Cell
	}{
		"above the keys":     {lead.X, 1, term.Cell{Text: "─", Style: k.Accent}},
		"right of the keys":  {lead.W, lead.Y, term.Cell{Text: "│", Style: k.Accent}},
		"its upper corner":   {lead.W, 1, term.Cell{Text: "┬", Style: k.Accent}},
		"its lower corner":   {lead.W, lead.Y + lead.H, term.Cell{Text: "┤", Style: k.Accent}},
		"under the keys":     {lead.X, lead.Y + lead.H, term.Cell{Text: "─", Style: k.Accent}},
		"above the fleet":    {fleet.X, 1, term.Cell{Text: "─", Style: k.Frame}},
		"left of the fleet":  {fleet.X - 1, fleet.H, term.Cell{Text: "│", Style: k.Frame}},
		"inside the fleet":   {fleet.X, fleet.Y, term.Cell{Text: " "}},
		"the window's start": {0, 1, term.Cell{Text: "─", Style: k.Accent}},
	} {
		if got := g.At(c.x, c.y); got != c.want {
			t.Errorf("%s: %+v, want %+v", name, got, c.want)
		}
	}
}

// TestTabRow: clicks, the wheel, and the tab that shows always being on the row.
func TestTabRow(t *testing.T) {
	l, k := &Layout{}, look
	play(t, l, &k, approved, 100, 29)
	var g *term.Grid
	row := func() string {
		g = term.NewGrid(l.W, l.H)
		l.Draw(g, k)
		return g.Row(0)
	}
	if got, want := row(), " Lead │ sign-up page │ photo upload │ email sender │ price list │ login page │ search box │ +6"; got != want {
		t.Fatalf("the row is\n%q, want\n%q", got, want)
	}
	if h := l.At(0, 0); h.Kind != OnRow {
		t.Errorf("the row's first cell is %+v", h)
	}
	if h := l.At(9, 0); h.Kind != OnTab || h.Tab != "t2" {
		t.Errorf("a click on the second name is %+v", h)
	}
	x, _, _ := g.Find("+6")
	more := l.At(x+1, 0)
	if more.Kind != OnMore || more.Step != 1 {
		t.Fatalf("a click on the count is %+v", more)
	}
	l.Wheel(more.Step)
	if got := row(); !strings.HasPrefix(got, " +1 │ sign-up page │") || !strings.HasSuffix(got, "│ +6") {
		t.Errorf("one tab on, the row is %q", got)
	}
	if h := l.At(1, 0); h.Kind != OnMore || h.Step != -1 {
		t.Errorf("a click on the first count is %+v", h)
	}
	l.Wheel(100)
	if got := row(); !strings.HasSuffix(got, "│ admin page") || strings.Count(got, "│") < 4 {
		t.Errorf("wheeled to the end, the row is %q", got)
	}
	l.Wheel(-100)
	for _, tab := range []string{"t13", "t7", "t1", "t10"} {
		l.Show(tab)
		if got := row(); !strings.Contains(got, l.Tab(tab).Label) {
			t.Errorf("%s shows and the row is %q", tab, got)
		}
	}
	l.Fit(14, 5)
	if got := row(); got != " +9 │ hel │ +3" {
		t.Errorf("in fourteen columns the row is %q", got)
	}
}

// TestBeside: the pane across a line, and none at the window's edge.
func TestBeside(t *testing.T) {
	l, k := &Layout{}, look
	play(t, l, &k, approved, 150, 44)
	for _, c := range [][3]string{
		{"p1", contract.Right, "p2"}, {"p1", contract.Down, "p3"}, {"p1", contract.Left, ""}, {"p1", contract.Up, ""},
		{"p2", contract.Left, "p1"}, {"p3", contract.Up, "p1"}, {"p3", contract.Right, "p2"}, {"p2", "round", ""},
		{"p9", contract.Left, ""},
	} {
		if got := l.Beside(c[0], c[1]); got != c[2] {
			t.Errorf("%s of %s is %q, want %q", c[1], c[0], got, c[2])
		}
	}
	l.Zoom("p1")
	if got := l.Beside("p1", contract.Right); got != "" {
		t.Errorf("beside a zoomed pane is %q", got)
	}
}

// TestFromAFile: a layout written and read back is the same, and what a
// damaged file could hold is not taken.
func TestFromAFile(t *testing.T) {
	l, k := &Layout{}, look
	play(t, l, &k, approved+"split p3 left 20 p4\nzoom p4\nshow t3", 150, 44)
	data, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	back := &Layout{}
	if err := json.Unmarshal(data, back); err != nil || !back.Sound() {
		t.Fatalf("read back: %v, sound %v", err, back.Sound())
	}
	back.Fit(150, 44)
	if got, want := picture(back, k), picture(l, k); got != want {
		t.Errorf("read back it shows\n%s\nand it was\n%s", got, want)
	}
	for name, text := range map[string]string{
		"a split with one side": `{"tabs":[{"id":"t1","focus":"p1","root":{"a":{"pane":"p1"}}}]}`,
		"a pane with sides":     `{"tabs":[{"id":"t1","focus":"p1","root":{"pane":"p1","a":{"pane":"p2"},"b":{"pane":"p3"}}}]}`,
		"a pane with no id":     `{"tabs":[{"id":"t1","focus":"","root":{}}]}`,
		"a pane twice":          `{"tabs":[{"id":"t1","focus":"p1","root":{"share":0.5,"a":{"pane":"p1"},"b":{"pane":"p1"}}}]}`,
		"a pane in two tabs":    `{"tabs":[{"id":"t1","focus":"p1","root":{"pane":"p1"}},{"id":"t2","focus":"p1","root":{"pane":"p1"}}]}`,
		"a tab twice":           `{"tabs":[{"id":"t1","focus":"p1","root":{"pane":"p1"}},{"id":"t1","focus":"p2","root":{"pane":"p2"}}]}`,
		"a share of two":        `{"tabs":[{"id":"t1","focus":"p1","root":{"share":2,"a":{"pane":"p1"},"b":{"pane":"p2"}}}]}`,
		"cells below none":      `{"tabs":[{"id":"t1","focus":"p1","root":{"cells":-4,"a":{"pane":"p1"},"b":{"pane":"p2"}}}]}`,
		"keys in no pane":       `{"tabs":[{"id":"t1","focus":"p7","root":{"pane":"p1"}}]}`,
		"a zoom of no pane":     `{"tabs":[{"id":"t1","focus":"p1","zoom":"p7","root":{"pane":"p1"}}]}`,
		"a tab with no tree":    `{"tabs":[{"id":"t1","focus":"p1"}]}`,
		"no tab at all":         `{"tabs":[null]}`,
		"a tab that is not":     `{"tabs":[{"id":"t1","focus":"p1","root":{"pane":"p1"}}],"active":3}`,
	} {
		bad := &Layout{}
		if err := json.Unmarshal([]byte(text), bad); err != nil || bad.Sound() {
			t.Errorf("%s: read with %v and taken as sound", name, err)
		}
	}
}
