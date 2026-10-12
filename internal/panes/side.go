package panes

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// loadSide reads the left section's side of the files: every open run of
// every project of the login's list, and each project's branch. A run that
// was closed is not read again. The notices follow the runs that are open.
func (p *pane) loadSide(dirs contract.Dirs) {
	r, peek := p.k.Reader(), p.k.Platform.Peek
	roots, _ := contract.ReadProjects(peek, dirs.State)
	paths := []string{dirs.State, filepath.Join(dirs.Config, contract.ConfigFile)}
	p.in.Projects = nil
	for _, root := range roots {
		pr := contract.SideProject{Root: root}
		head, _ := peek(filepath.Join(root, ".git", "HEAD"))
		pr.Branch, _ = strings.CutPrefix(strings.TrimSpace(string(head)), "ref: refs/heads/")
		runs, _ := r.Runs(root)
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
	if watched := strings.Join(paths, "\n"); watched != p.watched || p.watch == nil {
		if p.watch != nil {
			p.watch.Close()
		}
		p.watch, _ = p.k.Platform.Watch(paths...)
		p.watched = watched
	}
}

// rowID names a row of the left section for the bar and the click guard.
func rowID(r contract.SideRow) string { return r.Kind + "/" + r.Run + "/" + r.Task + "/" + r.Name }

// short cuts a name to w cells as the left section does: its last cell says so.
func (p *pane) short(s string, w int) string {
	if r := []rune(s); term.Width(s) > w && w > 0 {
		return string(r[:min(w-1, len(r))]) + p.glyph("…", ">")
	}
	return s
}

// drawSide draws the left section in the dock, at whatever width it is
// given: a rail of marks and counts, names cut, names, or names with their
// state words. It prints contract.Side and works nothing out of it.
func (p *pane) drawSide(now time.Time) {
	t, b, w, h := p.t, p.bar, p.t.W, p.t.H
	rail, tight, wide := w <= contract.SideRail, w <= contract.SideCut, w > contract.SideNames
	dim, text := p.st("dim"), p.st("text")
	// The model fits its rows to the panel, so it is built for the rows there are.
	gone := map[bool]int{true: 1}
	if fits := h - 7 - len(b.Workspaces) + 2*gone[tight] + gone[rail] - gone[b.Warn != ""]; fits != p.fits {
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
	y := 1
	panel := func(title string) {
		if rail && y > 1 || !tight {
			y++
		}
		if !rail {
			t.Put(1, y, w-2, title, text)
			y++
		}
	}
	panel("WORKSPACES")
	if wide {
		p.button(w-4, y-1, "[+]", dim, func() { p.act("workspace", nil) })
	}
	for _, ws := range b.Workspaces {
		p.sideRow(y, contract.SideRow{Kind: "ws", Look: ws.Look, Name: ws.Name, Front: ws.Front, Agents: ws.Agents}, ws.Branch, func() { p.goTo(p.tabOf(ws)) })
		y++
	}
	if panel("AGENTS"); !tight && b.Count != "" && term.Width(b.Count) <= w-11 {
		t.Put(8, y-1, w-9, "· "+b.Count, text)
	} else if !tight {
		t.Put(8, y-1, w-9, "· "+p.short(b.Title, w-11), text)
	}
	end := h - 2
	if b.Warn != "" && !rail {
		end--
		t.Put(1, end, w-2, p.short(b.Warn, w-2), bold(p.st("blocked")))
	}
	for _, l := range wrap(b.Hint, w-3) {
		if l != "" && !rail && y < end {
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
		p.sideRow(y, r, "", func() { p.choose(r) })
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

// sideRow is one row of the left section: the bar where the keys or the tab
// in front are, the mark of its state, its name, and at its right what the
// width has room for: a workspace's agents, a fleet's tool and how full it
// is, an agent's state word beside its whole name.
func (p *pane) sideRow(y int, r contract.SideRow, branch string, do func()) {
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
	case r.Kind == "ws":
		if rights, rst = []string{strconv.Itoa(r.Agents)}, name; r.Look == "" {
			sign = contract.SideMarks[contract.RowTab]
		}
	case r.Parked:
		rights = []string{p.glyph("■", "#") + " parked"}
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
		rights = []string{cmp.Or(r.Word, string(r.Look))}
	}
	if r.Unseen {
		name = bold(name)
	}
	if r.Front && (r.Kind == "ws" || p.sel == "") || id == p.sel {
		t.Put(x-1-r.Depth, y, 1, p.glyph("▌", "|"), p.st("accent"))
		name = bold(name)
	}
	if p.old {
		st, name, rst = p.st("dim"), p.st("dim"), p.st("dim")
	}
	p.mark(y, 1, id)
	p.hits = append(p.hits, hit{0, y, w, 1, false, do})
	t.Put(x, y, 1, p.glyph(sign[0], sign[1]), st)
	offer := func() { p.offerRow(r) }
	if r.Kind == contract.RowFleet {
		p.hits = append(p.hits, hit{x, y, 1, 1, false, func() { p.shutFleet(r) }})
	} else {
		p.hits = append(p.hits, hit{x, y, 1, 1, false, offer})
	}
	if rail && r.Kind == "ws" {
		t.Put(2, y, 2, rights[0], rst)
	}
	if rail {
		return
	}
	room, right := w-x-3, ""
	for _, c := range rights {
		if right = c; term.Width(r.Name)+1+term.Width(c) <= room || r.Kind == "ws" {
			break
		}
		right = ""
	}
	rw := term.Width(right)
	if rw > 0 {
		room -= rw + 1
		t.Put(w-1-rw, y, rw, right, rst)
	}
	if rw > 0 && r.Kind != "ws" {
		p.hits = append(p.hits, hit{w - 1 - rw, y, rw, 1, false, offer})
	}
	at := x + 2 + t.Put(x+2, y, room, p.short(r.Name, room), name)
	if wide && branch != "" && at+2+term.Width(branch) <= x+2+room {
		t.Put(at+2, y, room, branch, p.st("dim"))
	}
}

// offerRow opens what can be done with a row. A fleet is parked and
// written to through its group, in the run above its own.
func (p *pane) offerRow(r contract.SideRow) {
	if len(r.Actions) == 0 {
		return
	}
	if p.at = r; r.Kind == contract.RowFleet {
		p.at.Run = p.bar.Rows[0].Run
	}
	p.shown, p.over, p.pick = contract.Card{Task: r.Task, Name: r.Name, Item: r.Item, Actions: r.Actions}, "card", 0
}

// tabOf is a tab to go to for a workspace: its lead agent's, else the first
// that is open in its folder, and for the tabs of no project the first of them.
func (p *pane) tabOf(ws contract.Workspace) string {
	in := func(q contract.Pane, root string) bool {
		return q.Cwd == root || strings.HasPrefix(q.Cwd, root+string(filepath.Separator))
	}
	lead, tab := "", ""
	for _, pr := range p.in.Projects {
		if pr.Root == ws.Root && len(pr.Runs) > 0 && pr.Runs[0].Run.Orchestrator != nil {
			lead = pr.Runs[0].Run.Orchestrator.Pane
		}
	}
	for _, q := range cmp.Or(p.picture, &contract.Snapshot{}).Panes {
		listed := slices.ContainsFunc(p.in.Projects, func(pr contract.SideProject) bool { return in(q, pr.Root) })
		if q.ID == lead && lead != "" {
			return q.Tab
		} else if tab == "" && !q.Dock && !q.Ours && (ws.Other && !listed || !ws.Other && in(q, ws.Root)) {
			tab = q.Tab
		}
	}
	return tab
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
	rows := p.bar.Rows
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
	case key == "esc":
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
	case at >= 0 && key == "enter":
		p.choose(rows[at])
	case at >= 0 && key == ".":
		p.offerRow(rows[at])
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
