package panes

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

const (
	offer = "offer"
	// twice is the most that lies between the two presses of a double-click:
	// the half second the keeper's routing counts for its own lines.
	twice = 500 * time.Millisecond
	// A refusal stands in a menu's box for saidFor, and then the box closes.
	saidFor = 2 * time.Second
	// The least a menu's box is wide, and the box of a typing line.
	menuWide, lineWide = 24, 30
)

// thing is what a menu was opened for: a tab, an agent, a fleet or a
// workspace, by its id and never by where it stood. row is its project, its
// run, its task, its item and its tab; first and work are the menu's two
// blocks, fixed when it opens. name is cleaned before it is shown, and it
// stands in a question and in the typing line only.
type thing struct {
	kind, id    string
	row         contract.SideRow
	first, work []string
	name        string
	agents      int         // how many agents a question counts
	lead, held  bool        // the lead agent's tab of a job; an agent's program is in the tab
	jobs        [][2]string // the open jobs a workspace shows, each its project and its run
}

// whole is the model with nothing folded and nothing cut: a menu is for a
// thing by its name, wherever its row stands and whether it has one.
func (p *pane) whole() *contract.Side {
	in := p.last
	in.Folded, in.Rows, in.Find, in.Height = nil, 0, "", 0
	return p.k.Side(in)
}

// thingOf finds what a menu is asked for, in the model and in the keeper's
// picture, and reports false where it is not there or offers nothing. full
// says the menu opens on a row of the left section: what its work offers
// follows under a line. A tab that is no row is a tab a person made.
func (p *pane) thingOf(kind, id string, about contract.SideRow, full bool) (th thing, ok bool) {
	b, snap := p.bar, cmp.Or(p.picture, &contract.Snapshot{})
	if p.kind == side {
		b = p.whole()
	}
	th = thing{kind: kind, id: id, row: about}
	at := slices.IndexFunc(b.Rows, func(r contract.SideRow) bool {
		switch kind {
		case contract.OfferAgent:
			return r.Kind == contract.RowAgent && r.Root == about.Root && r.Run == about.Run && r.Task == about.Task
		case contract.OfferFleet:
			return r.Kind == contract.RowFleet && r.Name == id
		}
		return kind == contract.OfferTab && r.Tab == id && (r.Kind == contract.RowLead || r.Kind == contract.RowTab)
	})
	switch ws := slices.IndexFunc(b.Workspaces, func(ws contract.Workspace) bool { return ws.ID == id }); {
	case kind == contract.OfferSpace && ws >= 0:
		th.first, th.name, th.agents, th.jobs = b.Workspaces[ws].Offer, b.Workspaces[ws].Name, b.Workspaces[ws].Agents, p.jobsOf(b.Workspaces[ws])
	case kind != contract.OfferSpace && at >= 0:
		r := b.Rows[at]
		th.row, th.first, th.name, th.lead = r, r.Offer, r.Name, r.Kind == contract.RowLead && r.Run != ""
		if th.row.Run = cmp.Or(about.Run, r.Run); full {
			th.work = r.Actions
		}
		for _, r := range b.Rows {
			if r.Kind == contract.RowAgent {
				th.agents++
			}
		}
	case kind == contract.OfferTab:
		th.row.Tab, th.first = id, contract.OfferRows(kind, false, false, true)
	default:
		return th, false
	}
	there := kind != contract.OfferTab
	for _, q := range snap.Panes {
		if q.Tab != th.row.Tab || q.Tab == "" {
			continue
		}
		if th.held = th.held || q.Agent != ""; kind == contract.OfferTab {
			th.name, there = q.Label, true
		}
	}
	return th, there && len(th.first)+len(th.work) > 0
}

// jobsOf are the open jobs a workspace shows: those whose lead agent's tab
// is of it, and those with no such tab whose project's folder is its own. A
// fleet's own run is stopped through the job above it.
func (p *pane) jobsOf(ws contract.Workspace) (jobs [][2]string) {
	snap := cmp.Or(p.picture, &contract.Snapshot{})
	for _, pr := range p.in.Projects {
		var below []string
		for _, s := range pr.Runs {
			for _, f := range s.Run.Fleets {
				below = append(below, f.Run)
			}
		}
		for _, s := range pr.Runs {
			at := ""
			for _, q := range snap.Panes {
				if o := s.Run.Orchestrator; o != nil && q.ID == o.Pane {
					at = q.Workspace
				}
			}
			if !slices.Contains(below, s.Run.ID) && (at == ws.ID || at == "" && pr.Root == ws.Root && ws.Root != "") {
				jobs = append(jobs, [2]string{pr.Root, s.Run.ID})
			}
		}
	}
	return jobs
}

// asks is the question a row asks before it does anything, in the
// contract's words, the button that does it, and whether the box opens on
// the button that keeps; with no question the row is done at once.
func (th thing) asks(id string) (text, doing string, keep bool) {
	name := contract.Label(th.name)
	keep = slices.Contains(contract.KeepFirst, id) && (id != "close-tab" || th.lead)
	switch {
	case id == "close-tab" && th.lead:
		return fmt.Sprintf(contract.AskCloseLead, th.agents), contract.ButtonClose, keep
	case id == "close-tab" && th.held:
		return contract.AskCloseAgent, contract.ButtonClose, keep
	case id == "stop-close":
		return fmt.Sprintf(contract.AskStopClose, name), contract.ButtonStop, keep
	case id == "remove-space" && len(th.jobs) > 0:
		return fmt.Sprintf(contract.AskRemove, name, th.agents), contract.ButtonRemove, keep
	case id == "remove-space":
		return fmt.Sprintf(contract.AskRemoveBare, name), contract.ButtonRemove, keep
	case id == "park":
		return label(id) + " " + name + "?", "[ Park ]", keep
	}
	return "", "", false
}

// askWide is how wide the box of a row's question is, and how wide its
// words run in it: as the design draws the two it draws.
func askWide(id string) (box, words int) {
	if id == "remove-space" {
		return 36, 33
	}
	return 34, 30
}

// menu is the size of the menu's own box: a row each, a line between its
// two blocks, and the dim line under "Remove from list".
func (th thing) menu() (w, h int) {
	w, h = menuWide, len(th.first)+len(th.work)+2
	for _, id := range slices.Concat(th.first, th.work) {
		w = max(w, term.Width(label(id))+5)
	}
	if len(th.first) > 0 && len(th.work) > 0 {
		h++
	}
	if slices.Contains(th.first, "remove-space") {
		h++
	}
	return w, h
}

// box is the size of the pane a menu is opened in: its own box, or the
// largest of what a row of it turns the box into, since a pane over the tab
// keeps the size it was opened with.
func (th thing) box() (w, h int) {
	w, h = th.menu()
	for _, id := range slices.Concat(th.first, th.work) {
		a, _ := contract.FindAction(id)
		if text, _, _ := th.asks(id); text != "" {
			box, words := askWide(id)
			w, h = max(w, box), max(h, len(wrap(text, words))+3)
		} else if strings.HasPrefix(id, "rename") || slices.Contains(a.Run, "{file}") {
			w, h = max(w, lineWide), max(h, 4)
		}
	}
	return w, h
}

// offerAt opens the menu of a thing with its corner at a cell of pane, or
// of the window with none named: the keeper lays the program over the tab,
// in a pane as large as the menu and what its rows turn it into.
func (p *pane) offerAt(pane, kind, id string, about contract.SideRow, full bool, x, y int) {
	th, ok := p.thingOf(kind, id, about, full)
	if !ok {
		return
	}
	argv := []string{cmp.Or(p.self, "whaleshark"), "ui", "run", offer, kind, cmp.Or(id, "-"), cmp.Or(about.Root, "-"), cmp.Or(about.Run, "-"), cmp.Or(about.Task, "-")}
	if full {
		argv = append(argv, "row")
	}
	w, h := th.box()
	p.keeper(func() error { return p.k.Terms.OverlayAt(argv, float64(w), float64(h), pane, max(x, 0), max(y, 0)) })
}

// menuAt is the keeper's word that a person asked for the menu of a tab, at
// a cell of the window: the box stands one cell right of and below it, and
// holds the first block alone. A tab of an agent or of a fleet's leader is
// that agent's or that fleet's menu.
func (p *pane) menuAt(tab string, x, y int) {
	kind, id, about := contract.OfferTab, tab, contract.SideRow{}
	b := p.whole()
	if at := slices.IndexFunc(b.Rows, func(r contract.SideRow) bool { return r.Tab == tab && tab != "" }); at >= 0 {
		switch about = b.Rows[at]; about.Kind {
		case contract.RowAgent:
			kind = contract.OfferAgent
		case contract.RowFleet:
			kind, id, about.Run = contract.OfferFleet, about.Name, b.Rows[0].Run
		}
	}
	p.offerAt("", kind, id, about, false, x+1, y+1)
}

// drawOffer draws the menu at the pointer, or what a taken row turned its
// box into: a line to type on, a question with its two buttons, or the one
// sentence a refusal is said with.
func (p *pane) drawOffer() {
	if p.th == nil && p.picture != nil {
		word := func(i int) string { return strings.TrimPrefix(append(p.want, "", "", "", "", "", "")[i], "-") }
		th, ok := p.thingOf(word(0), word(1), contract.SideRow{Root: word(2), Run: word(3), Task: word(4)}, word(5) == "row")
		if p.th, p.done = &th, !ok; !ok {
			return // it changed before the menu stood: nothing is drawn of it
		}
	}
	if p.th == nil {
		return
	}
	t, th := p.t, p.th
	frame, text, dim, accent := p.st("frame"), p.st("text"), p.st("dim"), p.st("accent")
	c := []rune(p.glyph("┌─┐│└┘├┤›", "+-+|++++>"))
	box := func(w, h int, title string) {
		line := strings.Repeat(string(c[1]), max(w-2, 0))
		t.Put(0, 0, w, string(c[0])+line+string(c[2]), frame)
		t.Put(0, h-1, w, string(c[4])+line+string(c[5]), frame)
		for y := 1; y < h-1; y++ {
			t.Put(0, y, 1, string(c[3]), frame)
			t.Put(w-1, y, 1, string(c[3]), frame)
		}
		if title != "" {
			t.Put(1, 0, w-2, " "+title+" ", bold(text))
		}
	}
	switch W := t.W; {
	case !p.until.IsZero():
		lines := wrap(p.hint, W-3)
		box(W, len(lines)+2, "")
		for i, l := range lines {
			t.Put(2, 1+i, W-3, l, text)
		}
	case p.line != nil:
		W = min(W, lineWide)
		box(W, 4, strings.TrimSuffix(p.linePre, ":"))
		p.line.Draw(t.Grid, 2, 1, W-4, text, true)
		t.Put(2, 2, W-4, p.cut(cmp.Or(p.hint, contract.RenameKeys), W-4), dim)
	case p.asking != "":
		wide, words := askWide(p.asking)
		W = min(W, wide)
		ask, doing, _ := th.asks(p.asking)
		lines := wrap(ask, min(words, W-3))
		box(W, len(lines)+3, "")
		for i, l := range lines {
			t.Put(2, 1+i, W-3, l, text)
		}
		x, y := 2, len(lines)+1
		for i, words := range []string{doing, contract.ButtonKeep} {
			st := text
			if (i == 1) == p.keep {
				t.Put(x, y, 1, string(c[8]), accent)
				st = bold(text)
			}
			p.hits = append(p.hits, hit{x + 1, y, term.Width(words), 1, true, func() {
				p.keep = i == 1
				p.answer()
			}})
			x += 2 + t.Put(x+1, y, W-x-2, words, st)
		}
	default:
		W, H := th.menu()
		box(W, H, "")
		y := 1
		for i, id := range slices.Concat(th.first, th.work) {
			if i == len(th.first) && i > 0 {
				t.Put(0, y, W, string(c[6])+strings.Repeat(string(c[1]), W-2)+string(c[7]), frame)
				y++
			}
			st := text
			if i == p.pick {
				t.Put(1, y, 1, string(c[8]), accent)
				st = bold(text)
			}
			t.Put(2, y, W-3, label(id), st)
			p.hits = append(p.hits, hit{1, y, W - 2, 1, false, func() {
				p.pick = i
				p.taken(id)
			}})
			if y++; id == "remove-space" {
				t.Put(3, y, W-4, contract.FilesStay, dim)
				y++
			}
		}
	}
}

// offerKey is a key while a menu stands: the arrows or j and k choose, Enter
// takes, a row's first letter takes it where only one row begins with it,
// and Escape closes; in a question the arrows or Tab go between the two
// buttons and Escape keeps.
func (p *pane) offerKey(ev term.Event) bool {
	th, key := p.th, ev.Key
	switch {
	case key == "ctrl+c":
		return false
	case th == nil || p.busy && p.line == nil:
	case key == "esc" || !p.until.IsZero():
		p.done = true
	case p.line != nil && key == "enter" && th.kind == contract.OfferSpace && strings.TrimSpace(p.line.String()) == "":
		p.line = nil
		p.does("rename-space", "") // an empty line gives the folder's name back
	case p.line != nil:
		p.typed(ev)
	case ev.Kind != term.KeyPress:
	case p.asking != "" && (key == "left" || key == "right" || key == "tab"):
		p.keep = !p.keep
	case p.asking != "":
		if key == "enter" {
			p.answer()
		}
	default:
		rows, with := slices.Concat(th.first, th.work), []string(nil)
		for _, id := range rows {
			if strings.EqualFold(string([]rune(label(id))[:1]), key) {
				with = append(with, id)
			}
		}
		switch {
		case key == "up" || key == "k":
			p.pick = max(p.pick-1, 0)
		case key == "down" || key == "j":
			p.pick = min(p.pick+1, len(rows)-1)
		case key == "enter":
			p.taken(rows[p.pick])
		case len(with) == 1:
			p.pick = slices.Index(rows, with[0])
			p.taken(with[0])
		}
	}
	return true
}

// taken is a row taken: its question first where it has one, the typing line
// with the name as it is for a rename, and anything else done at once.
func (p *pane) taken(id string) {
	switch text, _, keep := p.th.asks(id); {
	case text != "":
		p.asking, p.keep = id, keep
	case strings.HasPrefix(id, "rename"):
		p.line, p.lineFor, p.linePre = &term.Line{}, "", contract.RenameTitle
		p.line.Set(contract.Label(p.th.name))
		p.lineDo = func(typed string) { p.does(id, typed) }
	default:
		p.does(id, "")
	}
}

// answer is a question answered: the button that keeps closes the box.
func (p *pane) answer() {
	id := p.asking
	if p.asking, p.done = "", p.keep; !p.keep {
		p.does(id, "")
	}
}

// does does what a row of a menu is for: one call of the keeper, or the
// row's line of the action table as a child, about the one thing the menu
// was opened for. A workspace is removed by stopping every agent of each
// open job it shows and, when each of those ended well, closing its tabs.
func (p *pane) does(id, text string) {
	th, t := p.th, p.k.Terms
	p.busy = true // no key is taken until it has ended
	call := func(ask func() error) {
		p.helper(func() {
			err := ask()
			p.tell(news{do: func() { p.ended(fmt.Sprint(err), err == nil) }})
		})
	}
	switch id {
	case "close-tab":
		call(func() error { return t.TabClose(th.row.Tab) })
	case "rename-tab":
		call(func() error { return t.TabRename(th.row.Tab, text) })
	case "rename-space":
		call(func() error { return t.SpaceRename(th.id, text) })
	case "remove-space":
		var next func(jobs [][2]string)
		next = func(jobs [][2]string) {
			if len(jobs) == 0 {
				call(func() error { return t.SpaceClose(th.id) })
				return
			}
			p.child([]string{"stop", "--all", "--human", "--root", jobs[0][0], "--run", jobs[0][1]}, "", func(_ []byte, said string, ok bool) {
				if !ok {
					p.ended(said, false) // the workspace stays, and the box says which task
				} else {
					next(jobs[1:])
				}
			})
		}
		next(th.jobs)
	default:
		fill := map[string]string{"task": th.row.Task, "id": th.row.Item}
		if text != "" {
			fill["text"] = text
		}
		p.at, p.asked = th.row, id
		p.act(id, fill)
	}
}

// ended is how what a row did ended: well, and the box closes; refused, and
// the box shows that one sentence for two seconds and then closes.
func (p *pane) ended(said string, ok bool) {
	if p.done = ok; ok {
		return
	}
	p.hint, p.until, p.asking, p.line, p.busy = said, time.Now().Add(saidFor), "", nil, false
	time.AfterFunc(saidFor, func() { p.tell(news{do: func() { p.done = true }}) })
}
