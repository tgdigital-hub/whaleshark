package layout

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// morning is the design's morning with three fleets in one workspace, and a
// second workspace behind it: the Orchestrator, each fleet's leader and its
// workers, in the order they were opened.
func morning(w, h, dock int) *Layout {
	l := &Layout{Dock: Dock{On: dock > 0, Width: dock}}
	l.Fit(w, h)
	n := 0
	add := func(s *Space, group, label string, head bool) {
		n++
		t := l.AddIn(s, fmt.Sprintf("t%d", n), label, fmt.Sprintf("p%d", n))
		t.Group, t.Head = group, head
	}
	sleeps, portal := l.Open("/work/tomsleeps"), l.Open("/work/ops-portal")
	add(sleeps, "", "Orchestrator", false)
	add(portal, "", "Orchestrator", false)
	add(sleeps, "front desk", "front desk", true)
	add(sleeps, "front desk", "sign-up page", false)
	add(sleeps, "back office", "back office", true)
	add(sleeps, "front desk", "login page", false)
	add(portal, "", "invoices", false)
	add(sleeps, "front desk", "help pages", false)
	add(sleeps, "back office", "exports", false)
	add(sleeps, "back office", "audit", false)
	add(sleeps, "misc", "misc", true)
	add(sleeps, "misc", "site map", false)
	l.Marks = map[string]int{"t4": 1}
	l.Show("t6")
	return l
}

// rows is the tab row as drawn, from the column the tabs begin in, with the
// names in the accent between brackets and those in the dim ink in braces.
func rows(l *Layout, k Look) string {
	g := term.NewGrid(l.W, l.H)
	l.Draw(g, k)
	var b strings.Builder
	for y := range l.Head() {
		was := ""
		for x := l.area().X; x < l.W; x++ {
			c, now := g.At(x, y), ""
			if st := c.Style; st.Bold && st.Fg == k.Accent.Fg {
				now = "["
			} else if st == k.Dim && c.Text != " " {
				now = "{"
			} else if st == k.Need && c.Text != " " {
				now = "<"
			} else if c.Text == " " && was != "" && x+1 < l.W && g.At(x+1, y).Style == g.At(x-1, y).Style && g.At(x+1, y).Text != " " {
				now = was // a blank inside a name
			}
			if now != was {
				b.WriteString(map[string]string{"[": "]", "{": "}", "<": ">"}[was] + now)
			}
			b.WriteString(c.Text)
			was = now
		}
		b.WriteString(map[string]string{"[": "]", "{": "}", "<": ">"}[was])
		b.WriteString("\n")
	}
	lines := strings.Split(b.String(), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.Join(lines, "\n")
}

// The tab row with fleets, against the frames of the design at its three
// widths: the first line has the Orchestrator and each fleet with the count
// of its workers' tabs and what waits in them, the second the chosen fleet's
// leader and workers under a corner; the name that shows is in the accent on
// both. Fleets that do not fit are counted.
func TestTwoTabLines(t *testing.T) {
	k := look
	k.Need = term.Style{Fg: term.RGB(5)}
	for _, c := range []struct {
		w, dock int
		want    string
	}{
		{160, 28, " Orchestrator │ [front desk · 3] <▲1> │ back office · 2 │ misc · 1\n └ leader │ sign-up page │ [login page] │ help pages\n"},
		{120, 22, " Orchestrator │ [front desk · 3] <▲1> │ back office · 2 │ misc · 1\n └ leader │ sign-up page │ [login page] │ help pages\n"},
		{80, 14, " Orchestrator │ [front desk · 3] <▲1> │ back office · 2 │ misc · 1\n └ leader │ sign-up page │ [login page] │ help pages\n"},
		{72, 14, " Orchestrator │ [front desk · 3] <▲1> │ back office · 2 │ {+1}\n └ leader │ sign-up page │ [login page] │ help pages\n"},
	} {
		l := morning(c.w, 30, c.dock)
		if got := rows(l, k); got != c.want {
			t.Errorf("at %d columns the tab row is\n%s\nwant\n%s", c.w, got, c.want)
		}
		a := l.area()
		if l.Head() != 2 || a.Y != 3 || a.X != c.dock+1 || l.Places(l.Tab("t6"))[1].Y != 3 || l.Places(l.Tab("t2"))[1].Y != 2 {
			t.Errorf("at %d columns the tab begins at %+v", c.w, a)
		}
		g := term.NewGrid(l.W, l.H)
		l.Draw(g, k)
		if got := g.At(a.X+4, 2).Text + g.At(a.X+4, 3).Text; got != "─ " {
			t.Errorf("at %d columns the line under the tab row and the pane's first row are %q", c.w, got)
		}
	}
	plain := k
	plain.Plain = true
	if got, want := rows(morning(100, 30, 0), plain), " Orchestrator | [front desk - 3] <!1> | back office - 2 | misc - 1\n + leader | sign-up page | [login page] | help pages\n"; got != want {
		t.Errorf("in plain marks the tab row is\n%s\nwant\n%s", got, want)
	}
	sound(t, morning(100, 30, 0))

	// What a cell of either line is: a fleet's name is its group, and a click
	// goes to the tab of it that showed last, its leader's the first time.
	l := morning(100, 30, 0)
	at := func(text string, y int) Hit {
		t.Helper()
		g := term.NewGrid(l.W, l.H)
		l.Draw(g, k)
		x := strings.Index(g.Row(y), text)
		if x < 0 {
			t.Fatalf("no %q on line %d: %q", text, y, g.Row(y))
		}
		return l.At(len([]rune(g.Row(y)[:x]))+1, y)
	}
	for _, c := range []struct {
		text string
		y    int
		want Hit
	}{
		{"Orchestrator", 0, Hit{Kind: OnTab, Tab: "t1"}}, {"front desk", 0, Hit{Kind: OnGroup, Tab: "t6"}}, {"▲1", 0, Hit{Kind: OnGroup, Tab: "t6"}},
		{"back office", 0, Hit{Kind: OnGroup, Tab: "t5"}}, {"misc", 0, Hit{Kind: OnGroup, Tab: "t11"}},
		{"leader", 1, Hit{Kind: OnTab, Tab: "t3"}}, {"help pages", 1, Hit{Kind: OnTab, Tab: "t8"}}, {"└", 1, Hit{Kind: OnRow}},
	} {
		if got := at(c.text, c.y); got != c.want {
			t.Errorf("%q on line %d is %+v, want %+v", c.text, c.y, got, c.want)
		}
	}
	if h := l.At(4, 2); h.Kind != Nothing {
		t.Errorf("the line under two tab lines is %+v", h)
	}
	if h := l.At(4, 3); h.Kind != OnPane || h.Pane != "p6" || h.Y != 0 {
		t.Errorf("the first row under it is %+v", h)
	}
	// The Orchestrator shows: the second line is the fleet looked at last, dim.
	l.Show("t10")
	l.Show("t1")
	if got, want := rows(l, k), " [Orchestrator] │ front desk · 3 <▲1> │ back office · 2 │ misc · 1\n └ {leader} │ {exports} │ {audit}\n"; got != want {
		t.Errorf("with the Orchestrator in front the tab row is\n%s\nwant\n%s", got, want)
	}
	if got := at("back office", 0); got.Tab != "t10" {
		t.Errorf("a fleet's name goes to %s, not to the tab of it that showed last", got.Tab)
	}
	// The keys walk the line that holds the tab that shows; a digit is a
	// tab of the second line, also while that line is dim.
	walk := func(digit bool) string {
		tabs, at := l.Walk(digit)
		var ids []string
		for _, tab := range tabs {
			ids = append(ids, tab.ID)
		}
		if on := tabs[at] == l.shown(); on == (digit && l.shown().ID == "t1") {
			t.Errorf("the tab that shows is not where the keys are: %s at %d, a digit %v", tabs[at].ID, at, digit)
		}
		return strings.Join(ids, " ")
	}
	if got, digits := walk(false), walk(true); got != "t1 t6 t10 t11" || digits != "t5 t9 t10" {
		t.Errorf("from the Orchestrator the keys walk %s and the digits are %s", got, digits)
	}
	if l.Show("t8"); walk(false) != "t3 t4 t6 t8" || walk(true) != "t3 t4 t6 t8" {
		t.Errorf("in a fleet the keys walk %s and the digits are %s", walk(false), walk(true))
	}
	// Tabs opened by hand are one more group, the last; and a window of
	// fewer than sixteen rows has one line with every tab on it.
	l.AddIn(l.Space("s1"), "t13", "notes", "p13")
	l.AddIn(l.Space("s1"), "t14", "a shell", "p14")
	l.Show("t14")
	if got, want := rows(l, k), " Orchestrator │ front desk · 3 <▲1> │ back office · 2 │ misc · 1 │ [other · 2]\n └ notes │ [a shell]\n"; got != want {
		t.Errorf("with tabs opened by hand the tab row is\n%s\nwant\n%s", got, want)
	}
	l.Fit(100, 15)
	if got := rows(l, k); l.Head() != 1 || l.area().Y != 2 || got != " {+3} │ back office │ login page │ help pages │ exports │ audit │ misc │ site map │ notes │ [a shell]\n" {
		t.Errorf("in fifteen rows the tab row is\n%s", got)
	}
	// The wheel moves the line it is given; a count on the second line says so.
	l = morning(40, 30, 0)
	if got, want := rows(l, k), " Orchestrator │ [front desk · 3] <▲1> │ {+2}\n └ {+1} │ sign-up page │ [login page] │ {+1}\n"; got != want {
		t.Fatalf("in forty columns the tab row is\n%s\nwant\n%s", got, want)
	}
	if h := at("+1", 1); h.Kind != OnMore || h.Step != -Lower {
		t.Errorf("the second line's count of the names before is %+v", h)
	}
	l.Wheel(Lower)
	l.Wheel(1)
	if got, want := rows(l, k), " {+1} │ [front desk · 3] <▲1> │ {+2}\n └ {+2} │ [login page] │ help pages\n"; got != want {
		t.Errorf("wheeled on by one on each line the tab row is\n%s\nwant\n%s", got, want)
	}
}

// Each workspace has its own tabs: the tab row is the one in front's, a tab
// shown brings its workspace along, each remembers the tab it showed, a
// closed tab gives way to the next of its own workspace, a workspace ends
// with its last tab, and while a tab is left one shows.
func TestEachWorkspaceItsOwnTabs(t *testing.T) {
	l := morning(120, 30, 0)
	said := func() string {
		sound(t, l)
		s := l.Front + " shows " + l.shown().ID + ":"
		for _, sp := range l.Spaces {
			s += " " + sp.ID + "=" + sp.Active
		}
		return s
	}
	if got := said(); got != "s1 shows t6: s1=t6 s2=t2" {
		t.Fatal(got)
	}
	if l.Show("t7"); said() != "s2 shows t7: s1=t6 s2=t7" || rows(l, look) != " Orchestrator │ [invoices]\n" {
		t.Errorf("a tab of the second workspace shown: %s\n%s", said(), rows(l, look))
	}
	if l.Remove("t6"); said() != "s2 shows t7: s1=t8 s2=t7" {
		t.Errorf("a tab closed behind: %s", said())
	}
	if l.Remove("t7"); said() != "s2 shows t2: s1=t8 s2=t2" {
		t.Errorf("the tab that showed closed, and none after it in its workspace: %s", said())
	}
	if l.Remove("t2"); said() != "s1 shows t8: s1=t8" || l.Space("s2") != nil {
		t.Errorf("a workspace's last tab closed: %s", said())
	}
	if s := l.Open("/work/ops-portal"); s.ID != "s3" || l.Front != "s1" {
		t.Errorf("opened again the workspace is %+v, and %s is in front", s, l.Front)
	}
	l.Remove("t12") // a workspace that got no tab is gone at the next close
	if l.Add("t20", "by hand", "p20"); said() != "s1 shows t8: s1=t8" || l.Tab("t20").Space != "s1" {
		t.Errorf("a tab added to the workspace in front: %s", said())
	}
	for len(l.Tabs) > 0 {
		l.Remove(l.Tabs[len(l.Tabs)-1].ID)
		if !l.Sound() || len(l.Tabs) > 0 && (l.shown() == nil || l.shown().Space != l.Front) {
			t.Fatalf("with %d tabs left the layout is %+v", len(l.Tabs), l)
		}
	}
	if len(l.Spaces) != 0 || l.Front != "" || l.Add("t30", "anew", "p30").Space != "s4" || !l.Sound() {
		t.Errorf("after the last tab: %+v", l)
	}
}
