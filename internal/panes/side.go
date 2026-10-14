package panes

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/theme"
)

// loadSide reads the left section's side of the files: every open run of
// every project of the login's list, and each project's branch. A run that
// was closed is not read again. The notices follow the runs that are open.
func (p *pane) loadSide(dirs contract.Dirs) {
	r, peek := p.k.Reader(), p.k.Platform.Peek
	roots, _ := contract.ReadProjects(peek, dirs.State)
	paths := []string{dirs.State, filepath.Join(dirs.Config, contract.ConfigFile)}
	p.in.Projects, p.in.NewVersion = nil, contract.VersionLine(contract.ReadVersion(peek, dirs.State).Seen, contract.Version)
	for _, root := range roots {
		// A folder that is not on the disk, as one of a drive that is not
		// plugged in, costs one look: it is not read, not watched and not
		// shown, and it stays on the list for when it is back.
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			continue
		}
		pr := contract.SideProject{Root: root}
		head, _ := peek(filepath.Join(root, ".git", "HEAD"))
		pr.Branch, _ = strings.CutPrefix(strings.TrimSpace(string(head)), "ref: refs/heads/")
		pr.Saved, _ = contract.ReadFleets(peek, root)
		runs, _ := r.Runs(root)
		// A removed run's number is given again: its mark goes with the run.
		p.closed = slices.DeleteFunc(p.closed, func(mark string) bool {
			at, id, _ := strings.Cut(mark, "\n")
			return at == root && !slices.Contains(runs, id)
		})
		for _, id := range runs {
			if slices.Contains(p.closed, root+"\n"+id) {
				continue
			}
			if s, err := r.Read(root, id); err == nil && s.Run.ClosedAt.IsZero() {
				pr.Runs = append(pr.Runs, s)
				paths = append(paths, filepath.Join(r.Dir(root, id), contract.StateFile))
			} else if err == nil {
				p.closed = append(p.closed, root+"\n"+id)
			}
		}
		paths = append(paths, r.Dir(root, ""), filepath.Dir(r.Dir(root, "r1")))
		p.in.Projects = append(p.in.Projects, pr)
	}
	// A menu is fixed when it opens: it reads the list once and watches
	// none of its folders, so that its rows show at once however long the
	// list is. The notices of thousands of folders take half a minute.
	if p.kind == offer {
		return
	}
	if watched := strings.Join(paths, "\n"); watched != p.watched || p.watch == nil {
		if p.watch != nil {
			p.watch.Close()
		}
		p.watch, _ = p.k.Platform.Watch(paths...)
		p.watched = watched
	}
}

// rowID names a row of the left section for the bar and the click guard.
func rowID(r contract.SideRow) string {
	if r.Kind == "ws" {
		return "ws/" + r.Task // a workspace, by its id
	}
	return r.Kind + "/" + r.Run + "/" + r.Task + "/" + r.Name
}

// short cuts a name to w cells as the left section does: its last cell says so.
func (p *pane) short(s string, w int) string {
	if r := []rune(s); term.Width(s) > w && w > 0 {
		return string(r[:min(w-1, len(r))]) + p.glyph("…", ">")
	}
	return s
}

// drawSide draws the left section in the dock, at whatever width it is
// given: a rail of marks and counts, names cut, names, or names with their
// state words. The workspaces stand above, a thin line between every two,
// and the agents of the one in front begin under the heavier line, which
// stands where the model says. It prints contract.Side and works nothing
// out of it.
func (p *pane) drawSide(now time.Time) {
	t, b, w, h := p.t, p.bar, p.t.W, p.t.H
	rail, tight, wide := w <= contract.SideRail, w <= contract.SideCut, w > contract.SideNames
	dim, text := p.st("dim"), p.st("text")
	// The model fits its rows to the panel, so it is built for the rows there are.
	gone := map[bool]int{true: 1}
	fits := h - 4 - gone[b.Warn != ""] - gone[b.NewVersion != ""] - gone[b.StepsShown]
	if fits -= b.Split; rail {
		fits -= len(b.Workspaces)
	}
	if fits != p.fits {
		p.fits = fits
		p.build()
		b = p.bar
	}
	need := ""
	if b.NeedYou > 0 {
		need = p.glyph(contract.Looks[0].Mark, contract.Looks[0].Plain) + strconv.Itoa(b.NeedYou)
	}
	nx := w - 1 - term.Width(need)
	if rail {
		nx = 1
	} else {
		p.title(contract.SideTitle, "", nx-1)
	}
	t.Put(nx, 0, w-nx, need, bold(p.st("needs")))
	if b.First != nil {
		p.hits = append(p.hits, hit{nx, 0, w - nx, 1, false, func() { p.goTo(b.First.Tab) }})
	}
	end, y := h-2, 1
	if b.Warn != "" && !rail {
		end--
		t.Put(1, end, w-2, p.short(b.Warn, w-2), bold(p.st("blocked")))
	}
	if b.NewVersion != "" && !rail {
		end--
		t.Put(1, end, w-2, p.short(b.NewVersion, w-2), dim)
	}
	if !tight {
		y++
	}
	if !rail {
		t.Put(1, y, w-2, "WORKSPACES", text)
		y++
	}
	if wide {
		p.button(w-4, y-1, "[+]", dim, func() { p.act("workspace", nil) })
	}
	// The upper panel: the rows the model gave it, or unfolded over the agents all of them.
	spaces, last := b.Workspaces[:len(b.Workspaces)-b.More], b.Split
	if p.unfold = p.unfold && b.More > 0; p.unfold {
		p.wtop = max(min(p.wtop, len(b.Workspaces)-1), 0)
		spaces, last = b.Workspaces[p.wtop:], end
	} else if p.wtop = max(min(p.wtop, b.More), 0); rail {
		spaces, last = b.Workspaces, end
	} else if p.wtop > 0 {
		spaces = b.Workspaces[p.wtop:] // the wheel moved the panel: as many from there as it holds
	}
	thin := strings.Repeat(p.glyph(theme.Thin[0], theme.Thin[1]), max(w-2, 0))
	for i, ws := range spaces {
		if i > 0 && !rail && y < last-1 {
			t.Put(1, y, w-2, thin, p.st("frame"))
			y++
		}
		if y < last {
			p.spaceRow(y, ws)
			y++
		}
	}
	if b.More > 0 && !p.unfold && p.wtop == 0 && !rail && y < last-1 {
		t.Put(1, y, w-2, thin, p.st("frame"))
		t.Put(2, y+1, w-3, p.short(fmt.Sprintf("%s %d more", contract.SideMarks[contract.RowMore][0], b.More), w-3), dim)
		p.hits = append(p.hits, hit{0, y + 1, w, 1, false, func() { p.unfold, p.stale = true, true }})
	}
	if y++; !rail && !p.unfold {
		y = p.heavy(b.Split) + 1
		if t.Put(1, y, w-2, "AGENTS", text); !tight && b.Count != "" && term.Width(b.Count) <= w-11 {
			t.Put(8, y, w-9, "· "+b.Count, text)
		} else if !tight {
			t.Put(8, y, w-9, "· "+p.short(b.Title, w-11), text)
		}
		y++
	}
	// The first steps stand where the hint of a workspace with no job stood.
	// With a job, and where the section is too narrow to read them, they are
	// one line at the panel's foot, which a click opens.
	open := b.StepsShown && !rail && (p.steps || b.Hint != "" && !tight)
	if p.unfold {
		y = h // the workspaces lie over the agents until one is chosen
	} else if open {
		y = p.firstSteps(y, end)
	} else if b.StepsShown && !rail {
		n := 0
		for i := range contract.FirstSteps {
			n += b.Steps >> i & 1
		}
		end--
		p.button(1, end, p.short(fmt.Sprintf("first steps: %d of %d", n, len(contract.FirstSteps)), w-2), dim, func() { p.steps = true })
	}
	for _, l := range wrap(cmp.Or(b.Hint, b.Job), w-3) {
		if l != "" && !rail && !open && y < end {
			t.Put(2, y, w-3, l, dim)
			y++
		}
	}
	if p.top = max(min(p.top, len(b.Rows)-1), 0); !p.all {
		p.top = 0
	}
	done := fmt.Sprintf("%s %d done", p.glyph(look(contract.LookDone).Mark, look(contract.LookDone).Plain), b.Done)
	for i, r := range b.Rows[p.top:] {
		if y >= end {
			break
		}
		if r.Kind == contract.RowMore && r.Depth == 0 && p.top+i == len(b.Rows)-1 && b.Done > 0 {
			r.Name, done = r.Name+" · "+done, ""
		}
		p.sideRow(y, r, func() { p.choose(r) })
		y++
	}
	if b.Done > 0 && done != "" && y < end && !rail {
		t.Put(2, y, w-3, done, dim)
	}

	// The foot: the two buttons, or while the keys are here where they go and
	// what was typed; under it the age.
	labels, gap, x := [2]string{"[Menu]", "[Settings]"}, 1, 1
	switch {
	case wide:
		labels, gap = [2]string{"[ Menu ]", "[ Settings ]"}, 2
	case rail:
		labels, x = [2]string{p.glyph("[≡]", "[=]")}, 0
	case tight:
		labels = [2]string{p.glyph("[≡]", "[=]"), "[Set]"}
	}
	if y = h - 2; p.line != nil || p.hint != "" || p.focused {
		p.hintLine(y, w-1, cmp.Or(p.query.String(), "type to find")+" · esc back", "")
	} else {
		x += p.button(x, y, labels[0], text, func() { p.act(menu, nil) }) + gap
		p.button(x, y, labels[1], text, func() { p.act(settings, nil) })
	}
	age, ageSt := p.age(now, !wide)
	if rail {
		age = strings.TrimPrefix(age, "live · ")
	}
	t.Put(1, h-1, w-2, p.short(age, w-2), ageSt)
}

// firstSteps draws the five first steps from row y as far as end: those that
// have happened ticked, the first that has not lit. It returns the row after.
func (p *pane) firstSteps(y, end int) int {
	t, w, done, lit := p.t, p.t.W, look(contract.LookDone), true
	t.Put(1, y, w-2, "FIRST STEPS", p.st("text"))
	p.button(w-9, y, "[ hide ]", p.st("dim"), func() { p.child(p.cmd("set", "ui.first_steps", contract.Off), "", p.quiet) })
	for i, words := range contract.FirstSteps {
		mark, st := " ", p.st("dim")
		if p.bar.Steps>>i&1 == 1 {
			mark = p.glyph(done.Mark, done.Plain)
		} else if lit {
			mark, st, lit = p.glyph("›", ">"), bold(p.st("text")), false
		}
		for j, l := range textLines(words, w-6) {
			if y++; y >= end {
				return y
			} else if j == 0 {
				t.Put(1, y, 3, mark+" "+strconv.Itoa(i+1), st)
			}
			t.Put(5, y, w-6, l, st)
		}
	}
	return y + 1
}

// spaceRow is a workspace's row of the upper panel: the bar for the one in
// front, its name, its branch where the whole of both has room, and at its
// right what waits for the person there and how many agents it has, or how
// many tabs where it has no job. On the rail it is its mark and its count.
func (p *pane) spaceRow(y int, ws contract.Workspace) {
	t, w, id := p.t, p.t.W, "ws/"+ws.ID
	name, count := p.st("text"), strconv.Itoa(cmp.Or(ws.Agents, ws.Tabs))
	if ws.Other || ws.Gone || p.old {
		name = p.st("dim")
	}
	p.mark(y, 1, id)
	p.hits = append(p.hits, hit{0, y, w, 1, false, func() { p.front(ws) }})
	p.menus[id] = func(x, y int) {
		p.offerAt(p.me, contract.OfferSpace, ws.ID, contract.SideRow{Root: ws.Root}, false, x, y)
	}
	if w <= contract.SideRail {
		sign, st := contract.SideMarks[contract.RowTab], p.st("dim")
		if lk := look(ws.Look); ws.Look != "" && !p.old {
			sign, st = [2]string{lk.Mark, lk.Plain}, p.st(lk.Colour)
		}
		if ws.Front {
			t.Put(0, y, 1, p.glyph("▌", "|"), p.st("accent"))
		}
		t.Put(1, y, 1, p.glyph(sign[0], sign[1]), st)
		t.Put(2, y, 2, count, name)
		return
	}
	if ws.Front || id == p.sel {
		t.Put(1, y, 1, p.glyph("▌", "|"), p.st("accent"))
		if name = bold(name); w > contract.SideCut {
			w = p.asks(y, id)
		}
	}
	x := w - 1 - term.Width(count)
	t.Put(x, y, w-x, count, name)
	if ws.Need > 0 {
		need := p.glyph(contract.Looks[0].Mark, contract.Looks[0].Plain) + strconv.Itoa(ws.Need)
		x -= term.Width(need) + 1
		t.Put(x, y, w-x, need, p.st("needs"))
	}
	room := x - 3
	at := 2 + t.Put(2, y, room, p.short(ws.Name, room), name)
	if t.W > contract.SideNames && ws.Branch != "" && at+2+term.Width(ws.Branch) <= 2+room {
		t.Put(at+2, y, room, ws.Branch, p.st("dim"))
	}
}

// asks puts the menu's mark in the last cell of the chosen row: a plain
// click on it opens the row's menu, for a terminal that reports no right
// button. It is called once the row's own click is in place, and returns the
// width left to the row, one cell less, so a blank stands before the mark.
func (p *pane) asks(y int, id string) int {
	x := p.t.W - 1
	p.t.Put(x, y, 1, p.glyph("≡", "="), p.st("accent"))
	p.hits = append(p.hits, hit{x, y, 1, 1, false, func() { p.menus[id](x, y+1) }})
	return x
}

// heavy draws the heavier line the agents begin under, from edge to edge on
// the row the model says, and returns that row. Under the pointer it is in
// the accent; held, or waiting for the press that puts it, it is the held
// line's mark with its label: the rows over it and the limit it stands at.
func (p *pane) heavy(row int) int {
	w, mark, st, label := p.t.W, theme.Heavy, p.st("frame"), ""
	switch {
	case p.waits:
		mark, label = theme.Held, "click where it should go · esc"
	case p.grip:
		mark = theme.Held
		arrows := p.glyph("▲▼", "^v") + " " + strconv.Itoa(row)
		if label = arrows + " rows"; w <= contract.SideCut {
			label = arrows
		} else if p.py != row && p.moved {
			label += " · the " + map[bool]string{true: "least", false: "most"}[p.py < row]
		}
	}
	if p.py == row || label != "" {
		st = bold(p.st("blocked")) // as the keeper lights a line of its own
	}
	p.t.Put(0, row, w, strings.Repeat(p.glyph(mark[0], mark[1]), w), st)
	if label != "" {
		p.t.Put(1, row, w-2, " "+p.short(label, w-4)+" ", bold(st))
	}
	return row
}

// lineMouse is the pointer on the heavier line, and reports that the report
// was the line's: a press holds it, it follows the pointer while the button
// is down, and is stored when the button is let go. A press let go where it
// was leaves the line waiting for the press that puts it; a second press on
// it within twice, the double-click, puts it back where it comes.
func (p *pane) lineMouse(ev term.Event) bool {
	on := p.bar != nil && !p.unfold && p.t.W > contract.SideRail && ev.Y == p.bar.Split
	press := ev.Button == term.Left && ev.Action == term.Press
	to := func(row int) {
		p.split = min(max((row*100+p.t.H-1)/p.t.H, contract.SideSplitMin), contract.SideSplitMax)
	}
	switch {
	case press && p.waits:
		if p.waits = false; !on {
			to(ev.Y)
		} else if time.Since(p.pressed) < twice {
			p.split = contract.SideSplit
		}
		p.keepSplit()
	case press && on:
		p.grip, p.moved, p.pressed = true, false, time.Now()
		p.split = cmp.Or(p.split, p.ui.SideSplit, contract.SideSplit)
	case p.grip && ev.Action == term.Move:
		p.moved = true
		to(ev.Y)
	case p.grip && ev.Action == term.Release:
		if p.grip, p.waits = false, !p.moved; p.moved {
			p.keepSplit()
		}
	default:
		return false
	}
	p.stale = true
	return true
}

// keepSplit has the line's place written, as every setting is.
func (p *pane) keepSplit() {
	v := p.split
	p.child(p.cmd("set", "side.split", strconv.Itoa(v)), "", func(_ []byte, said string, ok bool) {
		if ok {
			p.ui.SideSplit = v
		} else {
			p.hint = said
		}
		if p.split == v && !p.grip && !p.waits {
			p.split, p.stale = 0, true
		}
	})
}

// front is a click on a workspace's row, or Enter: it comes to the front
// with the tab it showed last, and the keys stay where they are.
func (p *pane) front(ws contract.Workspace) {
	p.unfold, p.stale = false, true
	p.keeper(func() error {
		_, err := p.k.Terms.SpaceOpen(ws.Root, ws.Root)
		if err != nil && ws.Tab != "" {
			err = p.k.Terms.TabFocus(ws.Tab) // its folder is gone: its tab is still there
		}
		return err
	})
}

// sideRow is one row of the agents panel: the bar where the keys or the tab
// in front are, the mark of its state, its name, and at its right what the
// width has room for: a fleet's tool and how full it is, an agent's state
// word beside its whole name.
func (p *pane) sideRow(y int, r contract.SideRow, do func()) {
	t, w, id := p.t, p.t.W, rowID(r)
	rail, tight, wide := w <= contract.SideRail, w <= contract.SideCut, w > contract.SideNames
	x := 2 + 2*r.Depth
	if tight {
		x = 2 + r.Depth
	}
	if rail {
		x, r.Depth = 1, 0
	}
	kind := r.Kind
	if kind == contract.RowFleet {
		kind = map[bool]string{true: "open", false: "folded"}[r.Open]
	}
	sign, st, name, rst := contract.SideMarks[kind], p.st("dim"), p.st("text"), p.st("dim")
	if lk := look(r.Look); r.Look != "" {
		if st = p.st(lk.Colour); r.Kind != contract.RowFleet {
			sign, rst = [2]string{lk.Mark, lk.Plain}, st
		}
	}
	var rights []string
	switch {
	case r.Parked:
		rights = []string{p.glyph("■", "#") + " parked"}
	case r.Word == contract.WordSaved:
		rights = []string{contract.WordSaved}
	case r.Kind == contract.RowFleet:
		n := strconv.Itoa(r.Agents)
		if r.Limit != contract.Unlimited {
			n += "/" + strconv.Itoa(r.Limit)
		}
		if rights = []string{n}; wide {
			rights = []string{r.Tool + " " + n, n}
		}
	case r.Kind == contract.RowMore:
		name = p.st("dim")
	case wide:
		if rights = []string{cmp.Or(r.Word, string(r.Look))}; r.Look == contract.LookIdle && r.Word != "" {
			rights = append(rights, string(r.Look)) // without its time where the name leaves that no room
		}
	}
	if r.Unseen {
		name = bold(name)
	}
	chosen := r.Front && p.sel == "" || id == p.sel
	if chosen {
		t.Put(x-1-r.Depth, y, 1, p.glyph("▌", "|"), p.st("accent"))
		name = bold(name)
	}
	if p.old {
		st, name, rst = p.st("dim"), p.st("dim"), p.st("dim")
	}
	p.mark(y, 1, id)
	p.hits = append(p.hits, hit{0, y, w, 1, false, do})
	t.Put(x, y, 1, p.glyph(sign[0], sign[1]), st)
	// A row's menu: its first block and, under a line, what its work offers.
	// A fleet is parked and written to through its group, in the run above its own.
	kind, of, about := contract.OfferTab, r.Tab, r
	switch r.Kind {
	case contract.RowAgent:
		kind = contract.OfferAgent
	case contract.RowFleet:
		kind, of, about.Run = contract.OfferFleet, r.Name, p.bar.Rows[0].Run
	}
	p.menus[id] = func(x, y int) { p.offerAt(p.me, kind, of, about, true, x, y) }
	offer := func() { p.menus[id](p.px+1, p.py+1) }
	if r.Kind == contract.RowFleet {
		p.hits = append(p.hits, hit{x, y, 1, 1, false, func() { p.shutFleet(r) }})
	} else {
		p.hits = append(p.hits, hit{x, y, 1, 1, false, offer})
	}
	if rail {
		return
	}
	if chosen && r.Kind != contract.RowMore && !tight {
		w = p.asks(y, id)
	}
	room, right := w-x-3, ""
	for _, c := range rights {
		if right = c; term.Width(r.Name)+1+term.Width(c) <= room {
			break
		}
		// A fleet keeps how full it is while four cells are left of its
		// name, which is cut for it.
		if r.Kind != contract.RowFleet || r.Parked || room-term.Width(c)-1 < 4 {
			right = ""
		}
	}
	rw := term.Width(right)
	if rw > 0 {
		room -= rw + 1
		t.Put(w-1-rw, y, rw, right, rst)
	}
	if rw > 0 {
		p.hits = append(p.hits, hit{w - 1 - rw, y, rw, 1, false, offer})
	}
	t.Put(x+2, y, room, p.short(r.Name, room), name)
}

// choose is a click on a row or Enter: the rows that were cut unfold, and
// any other row's tab comes to the front, which gives the keys back.
func (p *pane) choose(r contract.SideRow) {
	if p.stale = true; r.Kind == contract.RowMore {
		p.all = true
		return
	}
	p.sel, p.focused = "", false
	p.query.Set("")
	p.ticked(1 << 2) // the third first step: a row was clicked
	p.goTo(r.Tab)
}

// shutFleet folds a fleet to its one line, or opens it again.
func (p *pane) shutFleet(r contract.SideRow) {
	id := p.bar.Rows[0].Run + "/" + r.Name
	if at := slices.Index(p.shut, id); at >= 0 {
		p.shut = slices.Delete(p.shut, at, at+1)
	} else if r.Open {
		p.shut = append(p.shut, id)
	} else {
		return
	}
	p.stale = true
	p.child(p.cmd("set", "shut", id), "", p.quiet) // kept for the next start of the bar
}

// sideKey is a key while the left section has the keys. Letters narrow the
// rows to what is typed, so it has no letter keys of its own.
func (p *pane) sideKey(ev term.Event) {
	// The workspaces that have a row and then the agents are one list to walk.
	var rows []contract.SideRow
	shown := p.bar.Workspaces
	if !p.unfold {
		shown = shown[:len(shown)-p.bar.More]
	}
	for _, ws := range shown {
		rows = append(rows, contract.SideRow{Kind: "ws", Task: ws.ID})
	}
	rows = append(rows, p.bar.Rows...)
	at := slices.IndexFunc(rows, func(r contract.SideRow) bool { return rowID(r) == p.sel || p.sel == "" && r.Front })
	step := func(by int, want func(contract.SideRow) bool) {
		for i := at + by; i >= 0 && i < len(rows); i += by {
			if want(rows[i]) {
				p.sel = rowID(rows[i])
				return
			}
		}
	}
	p.focused, p.stale, p.hint = true, true, ""
	switch key := ev.Key; {
	case key == "esc" && p.waits:
		p.waits, p.split = false, 0 // the line stays where it was
	case key == "esc":
		if p.bar.StepsShown { // Esc ends the first steps, as [ hide ] does
			p.child(p.cmd("set", "ui.first_steps", contract.Off), "", p.quiet)
		}
		p.sel, p.focused = "", false
		p.query.Set("")
		p.keeper(func() error { return p.k.Terms.Dock(contract.DockBack) })
	case key == "up" || key == "down":
		step(map[bool]int{true: -1, false: 1}[key == "up"], func(contract.SideRow) bool { return true })
	case key == "tab":
		step(1, func(r contract.SideRow) bool {
			return r.Look == contract.LookNeedsYou || r.Look == contract.LookAtPrompt
		})
	case key == "[" || key == "]":
		wider := map[bool]int{true: 2, false: -2}[key == "]"]
		p.child([]string{"set", "side.width", strconv.Itoa(cmp.Or(p.engine.Side.Width, contract.SideWidth) + wider), "--human"}, "", p.quiet)
	case at >= 0 && key == "enter" && rows[at].Kind == "ws":
		p.front(p.bar.Workspaces[slices.IndexFunc(p.bar.Workspaces, func(ws contract.Workspace) bool { return ws.ID == rows[at].Task })])
	case at >= 0 && key == "enter":
		p.choose(rows[at])
	case at >= 0 && key == "." && p.menus[rowID(rows[at])] != nil:
		// The menu opens at the row, as it does under a right press on it.
		p.menus[rowID(rows[at])](6, slices.Index(p.rows, rowID(rows[at])))
	case key == ".":
	case at >= 0 && (key == "left" || key == "right") && rows[at].Kind == contract.RowFleet && rows[at].Open == (key == "left"):
		p.shutFleet(rows[at])
	default:
		p.query.Key(ev)
		p.top = 0
	}
}

// keeper asks the keeper for one thing beside the loop, and says a refusal.
func (p *pane) keeper(ask func() error) {
	p.helper(func() {
		if err := ask(); err != nil {
			p.tell(news{said: err.Error()})
		}
	})
}

// lay runs one of our screens over the tab that shows: the whole of a narrow window.
func (p *pane) lay(what ...string) {
	size := map[bool]float64{true: 1}[p.kind == side && p.t.W <= contract.SideCut]
	p.keeper(func() error { return p.k.Terms.Overlay(append([]string{p.self, "ui"}, what...), size, size) })
}
