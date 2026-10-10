package corpus

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/rivo/uniseg"
)

// The synthetic streams stand where a recording of a coding agent cannot be
// published: an agent's interface text is its maker's. They are written
// here, around words of our own, from the kinds of sequence such an agent
// was counted sending and in about those proportions:
//
//   - the classic frame, on the first screen: the "one go" mark, down to the
//     bottom of what was drawn, carriage return, up n lines, words placed
//     with "go to column" instead of spaces, erase to the end of the line,
//     lines ended CR CR LF, the cursor parked in the typing line, the mark
//     closed; the title set at each change of state;
//   - the full-screen frame, on the second screen: home, then down and
//     forward to each row that changed, almost no line feed, the mouse asked
//     for, the cursor placed by row and column at the end;
//   - the questions asked at the start, and the three sequences that end in
//     "m" and are no style.

// Synth returns every synthetic stream. The same streams every time.
func Synth() []*Recording {
	var recs []*Recording
	for _, size := range [][2]int{{120, 40}, {50, 15}, {80, 24}} {
		w, h := size[0], size[1]
		recs = append(recs,
			classic(fmt.Sprintf("synth-classic-%dx%d", w, h), w, h, true),
			fullscreen(fmt.Sprintf("synth-fullscreen-%dx%d", w, h), w, h))
	}
	return append(recs, classic("synth-classic-plain-80x24", 80, 24, false))
}

// look is a style as the stream switches it on and off again.
type look struct{ on, off string }

var (
	plain  = look{}
	accent = look{"\x1b[38;2;64;160;208m", "\x1b[39m"}
	glow   = look{"\x1b[38;2;140;205;235m", "\x1b[39m"}
	grey   = look{"\x1b[38;2;150;150;150m", "\x1b[39m"}
	faint  = look{"\x1b[38;2;120;130;140m", "\x1b[39m"}
	white  = look{"\x1b[38;2;255;255;255m", "\x1b[39m"}
	block  = look{"\x1b[48;2;10;30;50m", "\x1b[49m"}
	banded = look{"\x1b[48;2;50;55;60m\x1b[38;2;255;255;255m", "\x1b[39m\x1b[49m"}
	bold   = look{"\x1b[1m", "\x1b[22m"}
	rev    = look{"\x1b[7m", "\x1b[27m"}
	blue   = look{"\x1b[34m", "\x1b[39m"}
	red    = look{"\x1b[31m", "\x1b[39m"}
)

// word is a piece of a line in one look.
type word struct {
	look look
	text string
}

// line is a row of words. The words of a tight line are separated by a
// real space; those of any other by a move to the next word's column.
type line struct {
	words  []word
	indent int
	tight  bool
	joined bool // no room at all between the words: one word in several looks
}

func row(indent int, tight bool, parts ...any) line {
	l := line{indent: indent, tight: tight}
	lk := plain
	for _, p := range parts {
		switch p := p.(type) {
		case look:
			lk = p
		case string:
			l.words = append(l.words, word{lk, p})
		}
	}
	return l
}

func (l line) width() int {
	n := l.indent
	if !l.joined {
		n += max(len(l.words)-1, 0)
	}
	for _, w := range l.words {
		n += uniseg.StringWidth(w.text)
	}
	return n
}

// gen writes one stream.
type gen struct {
	w, h  int
	rnd   *rand.Rand
	buf   bytes.Buffer
	out   [][]byte
	marks bool
	want  map[int]string
	top   int // how many rows of the page have scrolled away
}

func newGen(w, h int, marks bool) *gen {
	return &gen{w: w, h: h, marks: marks, want: map[int]string{}, rnd: rand.New(rand.NewPCG(uint64(w), uint64(h)))} // #nosec G115 G404 -- a seed, so that the streams are the same every time
}

// expect notes what the screen must show once the pieces so far are read,
// as contract.Picture.Text gives it: the rows of page from the first that
// has not scrolled away. A page that grows shorter scrolls nothing back.
func (g *gen) expect(page []line) {
	g.top = max(g.top, len(page)-g.h)
	rows := make([]string, 0, g.h)
	for _, l := range page[g.top:] {
		var b strings.Builder
		for _, c := range g.cells(l) {
			if c.text == "" && !c.half {
				c.text = " "
			}
			b.WriteString(c.text)
		}
		rows = append(rows, strings.TrimRight(b.String(), " "))
	}
	g.want[len(g.out)] = strings.TrimRight(strings.Join(rows, "\n"), "\n")
}

func (g *gen) f(format string, a ...any) { fmt.Fprintf(&g.buf, format, a...) }

// cut ends a piece of the stream. A long piece is cut where a read of a
// terminal would cut it, which is anywhere.
func (g *gen) cut() {
	for b := g.buf.Bytes(); len(b) > 0; {
		n := min(len(b), 1021)
		g.out = append(g.out, bytes.Clone(b[:n]))
		b = b[n:]
	}
	g.buf.Reset()
}

func (g *gen) mark(open bool) {
	if g.marks {
		g.f("\x1b[?2026%c", "lh"[b2i(open)])
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// cell is one place of a row as the generator means it: a character in
// a look, the second half of a wide one, or nothing at all.
type cell struct {
	text string
	look look
	half bool
}

// cells lays a line out in a row of the screen.
func (g *gen) cells(l line) []cell {
	row := make([]cell, g.w)
	x := l.indent
	for i, w := range l.words {
		if i > 0 && l.tight {
			row[x] = cell{text: " ", look: l.words[i-1].look}
		}
		if i > 0 && !l.joined {
			x++
		}
		for gr := uniseg.NewGraphemes(w.text); gr.Next(); x += gr.Width() {
			if row[x] = (cell{text: gr.Str(), look: w.look}); gr.Width() == 2 {
				row[x+1].half = true
			}
		}
	}
	return row
}

// draw changes a row that shows old into l, with the cursor at its start.
// Only the cells that differ are written, with a jump over the others: so
// on an empty row every word is placed by a move and no space is printed.
// It says whether anything was written.
func (g *gen) draw(old, l line) bool {
	was, now := g.cells(old), g.cells(l)
	at, cur, end, wrote := 0, plain, 0, false
	for x, c := range now {
		if c.text != "" {
			end = x + 1 + b2i(x+1 < g.w && now[x+1].half)
		}
	}
	jump := func(x int) {
		if x != at && at == 0 {
			g.f("\x1b[%dC", x)
		} else if x != at {
			g.f("\x1b[%dG", x+1)
		}
		at = x
	}
	for x := 0; x < end; x++ {
		c := now[x]
		if c == was[x] || c.half {
			continue
		}
		if c.text == "" {
			c.text = " "
		}
		jump(x)
		if c.look != cur {
			g.f("%s%s", cur.off, c.look.on)
			cur = c.look
		}
		g.f("%s", c.text)
		at, wrote = at+uniseg.StringWidth(c.text), true
	}
	g.f("%s", cur.off)
	for x := end; x < g.w; x++ {
		if was[x] != (cell{}) {
			jump(end)
			g.f("\x1b[K")
			return true
		}
	}
	return wrote
}

var vocabulary = strings.Fields(`the whale shark drifts under a small boat and filters what it needs
from warm water while nobody on deck is looking it keeps a slow steady course past the reef where
smaller fish follow in its shadow and the light comes down in long pale bars a diver counts the spots
along its back writes the number on a slate then rises to tell the others what was seen today`)

var wide = []string{"日本語", "鯨", "🙂", "né", "海の魚", "🦈", "café", "한국어"}

// sentence is n words of ours, with now and then one that is wide or joined.
func (g *gen) sentence(n int) []string {
	s := make([]string, n)
	for i := range s {
		if s[i] = vocabulary[g.rnd.IntN(len(vocabulary))]; g.rnd.IntN(40) == 0 {
			s[i] = wide[g.rnd.IntN(len(wide))]
		}
	}
	return s
}

// fill makes lines of words in one look that fit the width.
func (g *gen) fill(lk look, indent int, words []string) []line {
	var lines []line
	l := line{indent: indent}
	for _, w := range words {
		if next := (line{words: append(l.words[:len(l.words):len(l.words)], word{lk, w}), indent: indent}); next.width() < g.w-1 {
			l = next
			continue
		}
		lines = append(lines, l)
		l = line{indent: indent, words: []word{{lk, w}}}
	}
	return append(lines, l)
}

func (g *gen) rule(label string) line {
	n := g.w
	if label == "" {
		return row(0, true, faint, strings.Repeat("─", n))
	}
	// The label sits near the right end and the last column is written too,
	// which leaves the wrap held when the line ends.
	return row(0, false, faint, strings.Repeat("─", n-len(label)-3), label, "─")
}

func (g *gen) status(busy bool) line {
	hint := "?"
	if busy {
		hint = "esc"
	}
	l := row(2, false, grey, "▸▸", "plain", "mode", "on", "·", hint, "for", "help")
	if g.w >= 70 {
		l.words = append(l.words, word{grey, "·"}, word{grey, "◐"}, word{grey, "steady"})
	}
	return l
}

func (g *gen) prompt(typed []string) line {
	l := row(0, len(typed) == 0, "❯")
	for _, t := range typed {
		l.words = append(l.words, word{plain, t})
	}
	l.words = append(l.words, word{rev, " "})
	return l
}

func (g *gen) title(glyph string) {
	g.f("\x1b]0;%s pool\a", glyph)
	g.cut()
}

// questions is what the agent asks a terminal at its start, with the three
// sequences that end in "m" and are no style.
func (g *gen) questions() {
	for _, q := range []string{"\x1b[>0q", "\x1b[?u\x1b]7501;?\a\x1b[c", "\x1b[<u\x1b[>5u\x1b[>4;2m",
		"\x1b[?2026$p", "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\", "\x1b[16t", "\x1b[?1016$p", "\x1b[?4m\x1b[0%m", "\x1b[c"} {
		g.f("%s", q)
		g.cut()
	}
}

func (g *gen) banner() []line {
	return []line{
		row(0, false, accent, " ▗▄▄▄▖ ", block, "▛███▜", plain, bold, "Pool", "0.1", grey, "sample"),
		row(0, false, accent, "▝▜", block, "█████", accent, "█▀", grey, "a", "made-up", "agent"),
		row(0, false, accent, "  ▘▘ ▝▝", grey, "~/pool"),
	}
}

var spinner = []string{"✶", "✻", "✽", "✢", "·", "✳"}

// spin is the line that shows work going on: a mark that changes and a
// word whose letters light up one after another.
func (g *gen) spin(step int) line {
	verb, k := "Drifting…", step%8
	l := row(0, false, accent, spinner[step%len(spinner)]+" "+verb[:k], glow, verb[k:k+1], accent, verb[k+1:])
	l.joined = true
	return l
}

// answer is what a turn leaves on the page: text of about so many words,
// most of its lines with real spaces if tight, and by turns a piece of code
// in the eight colours or a table drawn with lines.
func (g *gen) answer(turn, words int, tight bool) []line {
	lines := g.fill(plain, 2, g.sentence(words+g.rnd.IntN(30)))
	for i := range lines {
		lines[i].tight = tight && i%4 != 3
	}
	lines[0].indent = 0
	lines[0].words = append([]word{{white, "⏺"}}, lines[0].words...)
	switch turn % 3 {
	case 1:
		lines = append(lines, line{},
			row(2, true, blue, "func", plain, "count(spots", blue, "int", plain, ") {"),
			row(2, true, "    report(", red, `"seen"`, plain, ", spots)"),
			row(2, true, "}"))
	case 2:
		lines = append(lines, line{},
			row(2, true, "┌───────┬────────┬───────┐"),
			row(2, true, "│ Name  │ Colour │ Count │"),
			row(2, true, "├───────┼────────┼───────┤"),
			row(2, true, "│ shark │ grey   │ 3     │"),
			row(2, true, "│ 鯨    │ blue   │ 12    │"),
			row(2, true, "└───────┴────────┴───────┘"))
	}
	return append(lines, line{})
}

// classic writes a session in the classic drawing mode. live is the part
// redrawn in place: it ends one row above the cursor's resting row, and
// the cursor is parked three rows up, in the typing line.
func classic(name string, w, h int, marks bool) *Recording {
	g := newGen(w, h, marks)
	var page, live []line // page is everything above the part redrawn in place
	park := 0
	frame := func(static, next []line) {
		g.mark(true)
		if park > 0 {
			g.f("\x1b[%dD", park)
		}
		g.f("\x1b[3B\r")
		first := 0
		for len(static) == 0 && first < len(live) && first < len(next) && slices.Equal(g.cells(live[first]), g.cells(next[first])) {
			first++
		}
		if up := len(live) - first; up > 0 {
			g.f("\x1b[%dA", up)
		}
		all := append(append([]line{}, static...), next[first:]...)
		for i, l := range all {
			old := line{}
			if j := first + i; j < len(live) {
				old = live[j]
			}
			if !g.draw(old, l) {
				g.f("\r\n")
			} else if i+1 < len(all) && first+i+1 < len(live) && g.rnd.IntN(2) == 0 {
				g.f("\r\x1b[1B")
			} else {
				g.f("\r\r\n")
			}
		}
		if gone := len(live) - first - len(all); gone > 0 {
			for range gone {
				g.f("\x1b[2K\r\n")
			}
			g.f("\x1b[%dA", gone)
		}
		live = next
		park = 2
		if p := live[len(live)-3]; len(p.words) > 2 {
			park = p.width() - 1
		}
		g.f("\x1b[%dC\x1b[3A", park)
		g.mark(false)
		g.cut()
		page = append(page, static...)
		g.expect(append(append(page[:len(page):len(page)], live...), line{}))
	}
	idle := func(typed []string) []line {
		return []line{g.rule("pool"), g.prompt(typed), g.rule(""), g.status(false)}
	}

	g.f("\x1b7\x1b[r\x1b8\x1b[?25h")
	g.cut()
	g.f("\x1b[?25l")
	g.cut()
	g.f("\x1b[?2004h\x1b[?2031h\x1b[?1004h")
	g.cut()
	g.title("✳")
	// The first paint has nothing above it and no mark round it.
	for _, l := range append(append(g.banner(), line{}, line{}), idle(nil)...) {
		g.draw(line{}, l)
		g.f("\r\r\n")
	}
	page, live, park = append(g.banner(), line{}, line{}), idle(nil), 2
	g.f("\x1b[2C\x1b[3A")
	g.cut()
	g.questions()

	for turn := range 12 {
		typed := g.sentence(2 + g.rnd.IntN(5))
		for i := range typed {
			frame(nil, idle(typed[:i+1]))
		}
		g.title("◐")
		busy := func(step int) []line {
			return []line{g.spin(step), {}, g.rule("pool"), g.prompt(nil), g.rule(""), g.status(true)}
		}
		echo := row(0, true, banded, "❯", strings.Join(typed, " "))
		frame([]line{echo, {}}, busy(0))
		for step := 1; step < 4+g.rnd.IntN(5); step++ {
			if frame(nil, busy(step)); step%5 == 0 {
				g.title("◑")
			}
		}
		g.title("✳")
		// The work line goes first, which leaves two rows to erase whole.
		frame(nil, idle(nil))
		frame(g.answer(turn, 150, true), idle(nil))
	}

	g.f("\x1b[?1006l\x1b[?1003l\x1b[?1002l\x1b[?1000l")
	g.cut()
	g.f("\x1b[%dD\x1b[3B", park)
	g.cut()
	g.f("\x1b[?1004l\x1b[>4m\x1b[<u\x1b[?2031l\x1b[?2004l\x1b(B\x0f\x1b[?1016l\x1b[?1006l\x1b[?1003l\x1b[?1002l\x1b[?1000l\x1b[?25h\x1b7\x1b[r\x1b8")
	g.cut()
	g.f("\x1b]0;\a")
	g.cut()
	g.f("\x1b[2m\x1b[22m\r\n\x1b[2mThe pool is closed.\x1b[22m\r\n\x1b[2mCome back with: pool --again\x1b[22m\r\n\x1b[2m\x1b[22m")
	g.cut()
	return &Recording{Name: name, W: w, H: h, Out: g.out, Want: g.want}
}

// fullscreen writes a session in the full-screen drawing mode: every frame
// starts at home and goes down and forward to the rows that changed.
func fullscreen(name string, w, h int) *Recording {
	g := newGen(w, h, true)
	shown := make([]line, h)
	var page []line // everything said so far; its end is what shows
	typed := []string(nil)
	frame := func(spin *line) {
		next := make([]line, h)
		body := h - 5
		copy(next, page[max(len(page)-body, 0):])
		if spin != nil {
			next[body] = *spin
		}
		next[h-4], next[h-3], next[h-2], next[h-1] = g.rule(""), g.prompt(typed), g.rule(""), g.status(spin != nil)
		g.mark(true)
		g.f("\x1b[H")
		at := 0
		for y, l := range next {
			if slices.Equal(g.cells(shown[y]), g.cells(l)) {
				continue
			}
			g.f("\r")
			if y > at {
				g.f("\x1b[%dB", y-at)
			}
			at = y
			g.draw(shown[y], l)
		}
		shown = next
		defer g.expect(next)
		g.f("\x1b[%d;1H\x1b[%d;%dH", h, h-2, g.prompt(typed).width())
		g.mark(false)
		g.cut()
	}

	g.f("\x1b7\x1b[r\x1b8\x1b[?25h")
	g.cut()
	g.f("\x1b[?2004h\x1b[?2031h\x1b[?1004h")
	g.cut()
	g.title("✳")
	g.f("\x1b[?1049h\x1b[2J\x1b[H\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1006h\x1b[?25l\x1b[?25l")
	page = append(g.banner(), line{})
	frame(nil)
	g.questions()

	for turn := range 9 {
		words := g.sentence(2 + g.rnd.IntN(5))
		for i := range words {
			typed = words[:i+1]
			frame(nil)
		}
		typed = nil
		g.title("◐")
		page = append(page, row(0, true, banded, "❯", strings.Join(words, " ")), line{})
		for step := range 7 + g.rnd.IntN(8) {
			l := g.spin(step)
			if frame(&l); step%5 == 4 {
				g.title([]string{"◑", "◐"}[step/5%2])
			}
		}
		g.title("✳")
		// The answer arrives a line at a time.
		for _, l := range g.answer(turn, 8, false) {
			page = append(page, l)
			frame(nil)
		}
	}

	g.f("\x1b[?1006l\x1b[?1003l\x1b[?1002l\x1b[?1000l")
	g.cut()
	g.mark(true)
	g.f("\x1b[?25h")
	g.mark(false)
	g.cut()
	g.f("\x1b[?1004l\x1b[<u\x1b[?1049l\x1b[>4m\x1b[>4m\x1b[<u\x1b[?2031l\x1b[?2004l\x1b(B\x0f\x1b[?1016l\x1b[?1006l\x1b[?1003l\x1b[?1002l\x1b[?1000l\x1b[?25h\x1b7\x1b[r\x1b8")
	g.cut()
	g.f("\x1b]0;\a")
	g.cut()
	g.f("\x1b[2m\x1b[22m\r\n\x1b[2mThe pool is closed.\x1b[22m\r\n\x1b[2mCome back with: pool --again\x1b[22m\r\n\x1b[2m\x1b[22m\r\n")
	g.cut()
	return &Recording{Name: name, W: w, H: h, Out: g.out, Want: g.want}
}
