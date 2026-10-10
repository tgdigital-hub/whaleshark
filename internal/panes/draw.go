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

// hit is a place that can be clicked. A button lights up under the pointer;
// a card or an item does not.
type hit struct {
	x, y, w, h int
	button     bool
	do         func()
}

// under is the place under the pointer, the last one drawn there, or -1.
func (p *pane) under(button bool) int {
	for i := len(p.hits) - 1; i >= 0; i-- {
		if h := p.hits[i]; (h.button || !button) && p.px >= h.x && p.px < h.x+h.w && p.py >= h.y && p.py < h.y+h.h {
			return i
		}
	}
	return -1
}

func (p *pane) plain() bool { return len(p.scheme.Colours) == 0 || p.t.Mode == term.NoColour }

// st is the style of one of the sixteen colour names on the scheme's ground.
func (p *pane) st(name string) term.Style {
	if p.plain() {
		return term.Style{Dim: name == "dim"}
	}
	return term.Style{Fg: term.RGB(p.scheme.Colours[name]), Bg: term.RGB(p.scheme.Ground)}
}

// on is dark text on a ground of that colour: a lit button, a switch that is
// on, the title of the pane that has the keys. With no colour it is reversed.
func (p *pane) on(name string) term.Style {
	if p.plain() {
		return term.Style{Reverse: true}
	}
	return term.Style{Fg: term.RGB(p.scheme.Ground), Bg: term.RGB(p.scheme.Colours[name])}
}

func bold(st term.Style) term.Style {
	st.Bold = true
	return st
}

// glyph picks the mark for a terminal that can show it, or the plain one.
func (p *pane) glyph(mark, plain string) string {
	if p.plainMarks {
		return plain
	}
	return mark
}

func look(l contract.Look) contract.LookOf {
	i := slices.IndexFunc(contract.Looks, func(o contract.LookOf) bool { return o.Look == l })
	return contract.Looks[max(i, 0)]
}

func label(action string) string {
	i := slices.IndexFunc(contract.Actions, func(a contract.Action) bool { return a.ID == action })
	return contract.Actions[i].Label
}

func ago(d time.Duration) string {
	switch d = max(d, 0); {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

// wrap breaks a text into lines of at most w cells, at its spaces.
func wrap(s string, w int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(s) {
		if line != "" && term.Width(line)+1+term.Width(word) > w {
			lines, line = append(lines, line), ""
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	return append(lines, line)
}

// cut shortens a text to w cells and says so in its last cell.
func (p *pane) cut(s string, w int) string {
	if term.Width(s) <= w || w < 1 {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && term.Width(string(r)) >= w {
		r = r[:len(r)-1]
	}
	return strings.TrimRight(string(r), " ") + p.glyph("…", ">")
}

// button draws a thing to click and returns the cells it takes.
func (p *pane) button(x, y int, text string, st term.Style, do func()) int {
	w := term.Width(text)
	if x < 0 || x+w > p.t.W {
		return w
	}
	p.hits = append(p.hits, hit{x, y, w, 1, true, do})
	if p.py == y && p.px >= x && p.px < x+w {
		st = bold(p.on("accent"))
	}
	return p.t.Put(x, y, w, text, st)
}

// draw is the one place that writes to the terminal.
func (p *pane) draw() {
	if p.stale {
		p.build()
	}
	now, t := p.now(), p.t
	checked := p.view.Fresh.Checked
	p.old = !checked.IsZero() && now.Sub(checked) > contract.StaleAfter
	p.hits, p.dirty, p.lineDrawn = p.hits[:0], false, false
	was := p.rows
	p.rows = make([]string, t.H)
	for y := range t.H {
		t.Put(0, y, t.W, strings.Repeat(" ", t.W), p.st("text"))
	}
	switch {
	case p.over != "":
		p.drawOver()
	case p.kind == fleet:
		p.drawFleet(now)
	default:
		p.drawActions(now)
	}
	// The click guard's book: a row that shows another thing than before,
	// and not because the person scrolled or chose, has moved by itself.
	if len(p.movedAt) != t.H {
		p.movedAt = make([]time.Time, t.H)
	}
	for y, id := range p.rows {
		if y < len(was) && was[y] != id && !p.own {
			p.movedAt[y] = time.Now()
		}
	}
	p.own = false
	p.lit = p.under(true)
	t.Flush()
}

// mark notes which card or item a block of rows shows.
func (p *pane) mark(y, rows int, id string) {
	for i := y; i < min(y+rows, len(p.rows)); i++ {
		p.rows[i] = id
	}
}

// drawOver draws the list that lies over the pane: its name, the line that
// narrows it, the entries round the chosen one, and what the keys do.
func (p *pane) drawOver() {
	t, w, h := p.t, p.t.W, p.t.H
	title, list, foot := p.entries()
	t.Put(0, 0, w, " "+title+" ", bold(p.on("accent")))
	y := 1
	if p.over == menu {
		x := 1 + t.Put(1, y, w-1, "> ", p.st("accent"))
		p.query.Draw(t.Grid, x, y, w-x-1, p.st("text"), true)
		y++
	}
	p.pick = max(min(p.pick, len(list)-1), 0)
	room := h - y - 1
	for i := max(min(p.pick-room/2, len(list)-room), 0); i < len(list) && y < h-1; i, y = i+1, y+1 {
		e, st := list[i], p.st("text")
		if e.do != nil && i == p.pick {
			st = bold(st)
			t.Put(0, y, 1, p.glyph("▌", "|"), p.st("accent"))
		}
		rw := term.Width(e.right)
		t.Put(2, y, w-rw-4, p.cut(e.text, w-rw-4), st)
		t.Put(w-rw-1, y, rw, e.right, p.st("dim"))
		if e.do != nil {
			p.hits = append(p.hits, hit{0, y, w, 1, false, func() {
				p.pick = i
				e.do()
			}})
		}
	}
	if st := p.st("dim"); p.hint != "" {
		t.Put(1, h-1, w-1, p.cut(p.hint, w-1), p.st("accent"))
	} else {
		t.Put(1, h-1, w-1, p.cut(foot, w-1), st)
	}
}

// age is the line that cannot lie: how long ago both sources of news last
// agreed with the screen, counted on the pane's own clock, and what the pane
// knows to be wrong with either.
func (p *pane) age(now time.Time, short bool) (string, term.Style) {
	checked, st := p.view.Fresh.Checked, p.st("dim")
	text := "live · checked " + ago(now.Sub(checked)) + " ago"
	switch {
	case checked.IsZero():
		text = "not checked yet"
	case p.old:
		text, st = "STALE since "+checked.Local().Format("15:04"), bold(p.st("blocked"))
	case short:
		text = "live · " + ago(now.Sub(checked))
	}
	for _, n := range p.view.Fresh.Notes {
		if !strings.Contains(text, n) {
			text += " · " + n
		}
	}
	return text, st
}

// title is the first cells of a pane's first row: its name, lit while the
// pane has the keys, and its counts. A click on the name, as one on the line
// that names the keys, opens the list of every action.
func (p *pane) title(name, counts string, limit int) {
	st := bold(p.st("text"))
	if p.focused {
		st = bold(p.on("accent"))
	}
	x := p.t.Put(0, 0, limit, " "+name+" ", st) + 1
	p.hits = append(p.hits, hit{0, 0, x, 1, false, func() { p.act(menu, nil) }})
	p.t.Put(x, 0, limit-x, counts, p.st("text"))
}

// hintLine names the keys that matter here, as many as fit whole, or says
// what the last key did.
func (p *pane) hintLine(y, limit int, keys, away string) {
	x := 1
	p.hits = append(p.hits, hit{0, y, limit, 1, false, func() { p.act(menu, nil) }})
	switch {
	case p.line != nil && !p.lineDrawn:
		x += p.t.Put(x, y, limit-x, p.linePre+" ", bold(p.st("accent")))
		p.line.Draw(p.t.Grid, x, y, limit-x, p.st("text"), true)
		return
	case p.hint != "":
		p.t.Put(x, y, limit-x, p.hint, p.st("accent"))
		return
	case p.focused:
		x += p.t.Put(x, y, limit-x, "KEYS GO HERE", bold(p.st("accent")))
		away = " · " + keys
	}
	for term.Width(away) > limit-x && strings.Contains(away, " · ") {
		away = away[:strings.LastIndex(away, " · ")]
	}
	p.t.Put(x, y, limit-x, away, p.st("dim"))
}

// window settles which entries of a list are drawn in room rows, entry i
// taking height(i) of them, and returns how many are, from p.top on.
func (p *pane) window(n, room int, height func(int) int) int {
	fits := func(top int) (fit int) {
		for used := 0; top+fit < n && used+height(top+fit) <= room; fit++ {
			used += height(top + fit)
		}
		return fit
	}
	sel := slices.Index(p.ids(), p.sel)
	p.top = max(min(p.top, n-1), 0)
	if p.follow && sel >= 0 && sel < p.top {
		p.top = sel
	}
	for p.follow && sel >= p.top+max(fits(p.top), 1) {
		p.top++
	}
	for p.top > 0 && fits(p.top-1) == n-p.top+1 {
		p.top--
	}
	p.follow = false
	return fits(p.top)
}

func (p *pane) drawFleet(now time.Time) {
	t, v, w, h := p.t, p.view, p.t.W, p.t.H
	narrow := w < 38
	dim := p.st("dim")
	age, ageSt := p.age(now, narrow)
	done := fmt.Sprintf("%d done today", v.Counts.DoneToday)
	foot := 3
	if narrow {
		done = fmt.Sprintf("%d done", v.Counts.DoneToday)
		if foot = 1; term.Width(done+" [show] · "+age) >= w {
			foot = 2
		}
		if p.focused || p.hint != "" {
			foot++
		}
	}
	y := 1
	for _, a := range v.Alerts {
		if y < h-foot {
			t.Put(1, y, w-1, p.cut(a, w-1), bold(p.st("blocked")))
			y++
		}
	}
	room := h - foot - y
	if !narrow && h > 12 {
		y, room = y+1, room-2
	} else if len(p.cards)*3 < room {
		y, room = y+1, room-1
	}
	if len(p.cards) == 0 && len(v.Alerts) == 0 && room > 0 {
		t.Put(3, y, w-3, "no agents yet", dim)
	}
	rows := 3
	if len(p.cards)*3 > room {
		rows = 2
	}
	fit := p.window(len(p.cards), room, func(int) int { return rows })
	for i := range fit {
		p.card(p.cards[p.top+i], y+i*rows, rows, narrow, now)
	}

	// The header says as much as fits: the counts in three lengths, and the
	// three buttons, two of them or none.
	need := p.glyph(contract.Looks[0].Mark, contract.Looks[0].Plain)
	up, down := p.glyph(" ↑%d", " -%d"), p.glyph(" ↓%d", " +%d")
	forms := []struct {
		counts, above, below string
		buttons, gap         int
	}{
		{fmt.Sprintf("%d agents · %d need you", v.Counts.Agents, v.Counts.NeedYou), " · %d above", " · %d below", 3, 1},
		{fmt.Sprintf("%d · %d need you", v.Counts.Agents, v.Counts.NeedYou), up, down, 3, 1},
		{fmt.Sprintf("%d · %d need you", v.Counts.Agents, v.Counts.NeedYou), up, down, 2, 0},
		{fmt.Sprintf("%d · %s%d", v.Counts.Agents, need, v.Counts.NeedYou), up, down, 2, 0},
		{fmt.Sprintf("%d · %s%d", v.Counts.Agents, need, v.Counts.NeedYou), up, down, 0, 0},
	}
	for i, f := range forms {
		if p.top > 0 {
			f.counts += fmt.Sprintf(f.above, p.top)
		}
		if rest := len(p.cards) - p.top - fit; rest > 0 {
			f.counts += fmt.Sprintf(f.below, rest)
		}
		x := w - f.buttons*(3+f.gap) + f.gap
		if 9+term.Width(f.counts) > x && i < len(forms)-1 {
			continue
		}
		p.title("FLEET", f.counts, x-1)
		for i, b := range []string{"narrower", "wider", "fleet"}[:f.buttons] {
			x += p.button(x, 0, [...]string{"[<]", "[>]", "[x]"}[i], dim, func() { p.act(b, nil) }) + f.gap
		}
		break
	}

	show := func(x, y int) int { return x + p.button(x, y, "[show]", dim, func() { p.act("show", nil) }) }
	keys := "enter go there · f hide · [ ] width"
	switch y = h - foot; {
	case !narrow:
		show(3+t.Put(1, y, w-1, done, dim), y)
		p.hintLine(y+1, w, keys, keys)
		t.Put(1, y+2, w-1, p.cut(age, w-1), ageSt)
	case term.Width(done+" [show] · "+age) < w:
		if foot == 2 {
			p.hintLine(y, w, keys, keys)
		}
		x := show(2+t.Put(1, h-1, w-1, done, dim), h-1)
		t.Put(x, h-1, w-x, " · "+age, ageSt)
	default:
		if foot == 3 {
			p.hintLine(y, w, keys, keys)
		}
		show(2+t.Put(1, h-2, w-1, done, dim), h-2)
		t.Put(1, h-1, w-1, p.cut(age, w-1), ageSt)
	}
}

// card is one agent: its mark, name and state, its two bars, its news.
func (p *pane) card(c contract.Card, y, rows int, narrow bool, now time.Time) {
	t, w := p.t, p.t.W
	st := func(name string) term.Style {
		if p.old {
			name = "dim"
		}
		s := p.st(name)
		s.Dim = s.Dim || p.old
		return s
	}
	p.mark(y, rows, c.Task)
	p.hits = append(p.hits, hit{0, y, w, rows, false, func() {
		p.sel = c.Task
		p.goTo(c.Tab)
	}})
	name := st("text")
	if c.Task == p.sel {
		name = bold(name)
		for i := range rows {
			t.Put(0, y+i, 1, p.glyph("▌", "|"), p.st("accent"))
		}
	}
	lk, word := look(c.Look), c.Word
	if word == "" {
		if word = string(c.Look); c.Look == contract.LookIdle {
			word += " " + ago(now.Sub(c.Since))
		}
	}
	ww := term.Width(word)
	t.Put(1, y, 1, p.glyph(lk.Mark, lk.Plain), st(c.Colour))
	room := w - ww - 4
	if c.Flag {
		room -= 2
	}
	x := 3 + t.Put(3, y, room, p.cut(c.Name, room), name)
	if c.Flag {
		t.Put(x+1, y, w-ww-x-2, p.glyph(contract.Looks[0].Mark, contract.Looks[0].Plain), st("needs"))
	}
	t.Put(w-ww, y, ww, word, st(c.Colour))
	// The state word opens what can be done with this agent.
	if len(c.Actions) > 0 {
		p.hits = append(p.hits, hit{w - ww, y, ww, 1, false, func() { p.sel, p.shown, p.over, p.pick = c.Task, c, "card", 0 }})
	}

	ctx, cells := "  ctx unknown", (c.Ctx+10)/20
	switch {
	case narrow && c.CtxKnown:
		ctx = fmt.Sprintf("ctx %2d%%", c.Ctx)
	case narrow:
		ctx = "ctx   ?"
	case c.CtxKnown:
		ctx = fmt.Sprintf("ctx %s %2d%%", strings.Repeat("▮", 5), c.Ctx)
	}
	cw := term.Width(ctx)
	bw := w - 12 - cw
	if bw < 4 {
		bw, cw = w-8, 0
	}
	full, bar, block := (c.Progress*bw+50)/100, st("building"), contract.BarFull
	if c.Pale && p.plain() {
		block = contract.BarPale
	} else if c.Pale && !p.old {
		bar.Fg = term.RGB(contract.Pale(p.scheme.Colours["building"], p.scheme.Ground))
	}
	t.Put(3, y+1, bw, strings.Repeat(block, full), bar)
	t.Put(3+full, y+1, bw-full, strings.Repeat("░", bw-full), st("bar_empty"))
	t.Put(3+bw, y+1, 5, fmt.Sprintf(" %3d%%", c.Progress), st("text"))
	if cw > 0 {
		t.Put(w-cw, y+1, cw, ctx, st("dim"))
	}
	if cw > 0 && c.CtxKnown && !narrow {
		level := "ctx_low"
		if c.Ctx > contract.CtxHigh {
			level = "ctx_high"
		} else if c.Ctx >= contract.CtxMid {
			level = "ctx_mid"
		}
		t.Put(w-cw+4, y+1, cells, strings.Repeat("▮", cells), st(level))
		t.Put(w-cw+4+cells, y+1, 5-cells, strings.Repeat("▯", 5-cells), st("bar_empty"))
	}
	if rows > 2 {
		news := c.News
		if c.Pale && !c.Said.IsZero() {
			news = "said " + ago(now.Sub(c.Said)) + " ago: " + news
		}
		// A change that would conflict is the news; one that only meets
		// another follows it.
		if x := c.Clash; x != nil && x.Conflicts {
			news = "clashes with " + x.Name + ": " + x.File
		} else if x != nil {
			news += " · also changes " + x.File
		}
		t.Put(3, y+2, w-3, p.cut(news, w-3), st("dim"))
	}
}

func (p *pane) drawActions(now time.Time) {
	t, items, w, h := p.t, p.items, p.t.W, p.t.H
	dim := p.st("dim")
	p.strip(now)
	if h < 2 {
		return
	}
	age, ageSt := p.age(now, true)
	age = p.cut(age, w-2)
	aw := term.Width(age)
	t.Put(max(w-aw-1, 0), h-1, w, age, ageSt)
	defer func() {
		p.hintLine(h-1, w-aw-3, "j k choose · y n o answer · u undo · x fold · / all actions · ? keys",
			cmp.Or(p.command, "ctrl+space")+" a to answer · / all actions · ? keys")
	}()

	y, room, end := 1, h-2, h-1
	for _, a := range p.view.Alerts {
		if room > 0 {
			t.Put(1, y, w-1, a, bold(p.st("blocked")))
			y, room = y+1, room-1
		}
	}
	// A draft whose item was answered somewhere else stays in view until it
	// is dismissed; the last answers are one row, opened with [show].
	for _, it := range p.view.Answered {
		if d := p.ui.Drafts[it.ID]; d != "" && room > 0 && it.By != nil {
			where := cmp.Or(map[string]string{contract.WherePage: "on the page", contract.WherePhone: "on the phone",
				contract.WherePane: "here"}[it.By.Where], "elsewhere")
			x := 2 + t.Put(1, y, w-14, p.cut(fmt.Sprintf("answered %s: %s · your draft: %s", where, it.Answer, d), w-14), p.st("accent"))
			p.button(x, y, "[ dismiss ]", dim, func() { p.draft(it.ID, "") })
			y, room = y+1, room-1
		}
	}
	if n := len(p.view.Answered); n > 0 && !p.folded && room > 3 {
		room, end = room-1, end-1
		x := 5 + t.Put(3, end, w-3, fmt.Sprintf("answered: %d", n), dim)
		p.button(x, end, "[show]", dim, func() { p.act("show", nil) })
	}
	if len(items) == 0 && room > 0 {
		t.Put(3, y, w-3, "nothing waits for you", dim)
	}
	height := func(i int) int {
		if p.folded || room < 3 {
			return 1
		}
		return 2 + min(len(wrap(items[i].Text, w-6)), max(room-3, 1))
	}
	fit, used := p.window(len(items), room, height), 0
	for i := range len(items) {
		used += height(i)
	}
	if used < room && len(items) > 0 && height(0) > 1 {
		y, room = y+1, room-1
	} else if used > room && room > 1 {
		room--
		fit = p.window(len(items), room, height)
	}
	for i := p.top; i < p.top+fit; i++ {
		p.item(items[i], y, height(i), now)
		y += height(i)
	}
	// What does not fit whole is shown on one line each, as far as the rows
	// go, and a last row counts what is still out of view.
	rest, left := items[p.top+fit:], end-y
	lines := min(len(rest), left)
	hidden := p.top + len(rest) - lines
	if hidden > 0 && lines == left && left > 0 {
		lines, hidden = lines-1, hidden+1
	}
	for i := range lines {
		p.item(rest[i], y+i, 1, now)
	}
	if y += lines; hidden > 0 && y < end {
		t.Put(5, y, w-5, fmt.Sprintf("+%d more · j k", hidden), dim)
	}
}

// item is one thing that waits for the person, in one of its four forms:
// who asks, the text as it was asked, and the buttons or the line to type on.
// In one row it is its name and its text.
func (p *pane) item(it contract.Item, y, rows int, now time.Time) {
	t, w, lk := p.t, p.t.W, look(it.Look)
	p.mark(y, rows, it.ID)
	p.hits = append(p.hits, hit{0, y, w, rows, false, func() {
		p.sel, p.follow = it.ID, true
		p.fold(false)
	}})
	name := p.st("text")
	if it.ID == p.sel {
		name = bold(name)
		t.Put(1, y, 1, p.glyph("›", ">"), p.st("needs"))
	}
	t.Put(3, y, 1, p.glyph(lk.Mark, lk.Plain), p.st(lk.Colour))
	x := 5 + t.Put(5, y, w-5, it.Name, name)
	if x > 5 {
		x += t.Put(x, y, w-x, " · ", p.st("dim"))
	}
	if rows == 1 {
		t.Put(x, y, w-x, p.cut(it.Text, w-x-1), p.st("text"))
		return
	}
	since := "now"
	if d := now.Sub(it.Since); d >= time.Minute {
		since = ago(d)
	}
	t.Put(x, y, w-x, p.cut(it.Source+" · "+since, w-x-1), p.st("dim"))
	lines := wrap(it.Text, w-6)
	if n := rows - 2; len(lines) > n {
		lines[n-1] = p.cut(lines[n-1]+" "+lines[n], w-6)
	}
	for i, l := range lines[:min(len(lines), rows-2)] {
		t.Put(5, y+1+i, w-6, l, p.st("text"))
	}
	x, y = 5, y+rows-1
	draft := p.ui.Drafts[it.ID]
	switch {
	case p.line != nil && p.lineFor == it.ID:
		// The line being typed on takes the row, after what it is sent with.
		x += t.Put(x, y, w-x, strings.TrimSpace(p.linePre+" >")+" ", p.st("accent"))
		p.line.Draw(t.Grid, x, y, w-x-1, p.st("text"), true)
		p.hits = append(p.hits, hit{x, y, w - x, 1, false, func() { p.line.Click(p.px - x) }})
		p.lineDrawn = true
		return
	case it.Answer != "":
		left := (it.Settles.Sub(now) + time.Second - 1) / time.Second
		x += t.Put(x, y, w-x, fmt.Sprintf("%s · u to undo · %d", it.Answer, left), p.st("accent")) + 2
	case it.Line && len(it.Buttons) == 0:
		t.Put(x, y, 2, ">", p.st("accent"))
		t.Put(x+2, y, w-x-3, cmp.Or(p.cut(draft, w-x-3), "_"), p.st("dim"))
		p.hits = append(p.hits, hit{x, y, w - x, 1, false, func() { p.open(it.ID, "") }})
	}
	for _, b := range it.Buttons {
		x += p.button(x, y, "[ "+b.Label+" ]", p.st("text"), func() { p.press(it, b) }) + 2
	}
	if draft != "" && len(it.Buttons) > 0 && it.Answer == "" {
		t.Put(x, y, w-x-1, p.cut("· typed: "+draft, w-x-1), p.st("dim"))
	}
}

// strip is the first row of the action pane and all of it that is always
// there: what waits, the four buttons, and the arrow that folds the pane.
func (p *pane) strip(now time.Time) {
	v, s, w := p.view, p.view.Strip, p.t.W
	ids := []string{"stop-all", "catchup", "dnd", "mute"}
	short := []string{"Stop all", "Catch up", "DND", "Mute"}
	styles := []term.Style{p.st("failed"), p.st("text"), p.st("text"), p.st("text")}
	if s.Paused {
		ids[0], short[0], styles[0] = "resume", "Resume", p.st("done")
	}
	if s.DND {
		styles[2] = p.on("blocked")
	}
	if s.Mute {
		styles[3] = p.on("blocked")
	}
	var more []string // what else the strip says, what matters most first
	if s.Paused {
		more = append(more, " · paused")
	}
	if s.StillRunning > 0 {
		more = append(more, fmt.Sprintf(" · %d still running", s.StillRunning))
	}
	if !s.Away.IsZero() {
		more = append(more, " · away "+ago(now.Sub(s.Away))+" · press c")
	}
	if s.DoneAsYou > 0 {
		more = append(more, fmt.Sprintf(" · %d done as you", s.DoneAsYou))
	}
	// As much as fits: the full words, then the short buttons, then the
	// short counts too, with less and less of what else there is to say.
	long := fmt.Sprintf("%d waiting · %d hold work up", v.Counts.Waiting, v.Counts.Holding)
	brief := fmt.Sprintf("%d · %s%d", v.Counts.Waiting, p.glyph(contract.Looks[0].Mark, contract.Looks[0].Plain), v.Counts.Holding)
	counts, texts, total := "", make([]string, len(ids)), 0
	for form := 0; form <= 2+len(more); form++ {
		counts, total = long+strings.Join(more, ""), 4-min(form, 1)
		if form > 1 {
			counts = brief + strings.Join(more[:len(more)+2-form], "")
		}
		for i, id := range ids {
			if texts[i] = "[ " + label(id) + " ]"; form > 0 {
				texts[i] = "[" + short[i] + "]"
			}
			total += term.Width(texts[i]) + 1
		}
		if 10+term.Width(counts)+total <= w {
			break
		}
	}
	if 10+term.Width(counts)+total > w {
		// Too narrow for both: the counts stand where the name stood.
		p.title(brief, "", w-total-1)
	} else {
		p.title("ACTIONS", counts, w-total-1)
	}
	x := w - total
	for i, id := range ids {
		x += p.button(x, 0, texts[i], styles[i], func() { p.act(id, nil) }) + 1
	}
	arrow := "[v]"
	if p.folded {
		arrow = "[^]"
	}
	p.button(w-3, 0, arrow, p.st("dim"), func() { p.fold(!p.folded) })
}
