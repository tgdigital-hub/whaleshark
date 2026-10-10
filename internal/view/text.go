package view

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/rivo/uniseg"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Options is how a view is printed as plain text.
type Options struct {
	Width      int
	PlainMarks bool // the terminal cannot show the marks (contract.PlainMarks)
	Items      bool // only what waits for the person
	// Colours is the scheme's colours by name where the text goes to a
	// terminal that takes them: empty for one that wants no colour, which
	// still shows what is unseen in bold; nil for anything else.
	Colours map[string]uint32
}

// row is one line of the team list or of what waits for the person, before
// its columns are measured.
type row struct {
	mark, id, name, word, age, text, trail string
	next                                   []string
	colour                                 string // of the mark and the word
	bold                                   bool   // the name: unseen
}

type printer struct {
	out   *strings.Builder
	o     Options
	v     *contract.View
	forms map[string]string
	// the widths of the id, name, word and age columns
	idw, namew, wordw, agew int
}

// Text prints a view as plain text: what waits for the person, then the team
// by section, then how fresh it is. It computes no order, count or word of
// its own, and everything a person or an agent wrote is made harmless first.
func Text(w io.Writer, v *contract.View, o Options) {
	p := newPrinter(v, o)
	var items []row
	for _, it := range v.Items {
		items = append(items, p.item(it))
	}
	for _, it := range v.Held {
		p.forms[it.ID] = formWord(it.Form)
	}
	sections := make([][]row, len(v.Sections))
	for i, s := range v.Sections {
		for _, c := range s.Cards {
			sections[i] = append(sections[i], p.measure(p.card(c)))
		}
	}

	p.alerts()
	if !o.Items {
		c := v.Counts
		parts := []string{fmt.Sprintf("run %s %q", clean(v.Run), clean(v.Objective)), plural(c.Agents, "agent", "agents")}
		for _, n := range []struct {
			n         int
			one, many string
		}{{c.NeedYou, "needs you", "need you"}, {c.CheckFailed, "check failed", "check failed"}, {c.ToCheck, "to check", "to check"}} {
			if n.n > 0 {
				parts = append(parts, plural(n.n, n.one, n.many))
			}
		}
		p.line("TEAM   ", strings.Join(parts, " · "), v.At.Format("2 Jan 15:04"))
	}
	if len(items) > 0 || len(v.Held) > 0 || o.Items {
		parts := []string{fmt.Sprint(v.Counts.Waiting, " waiting"), plural(v.Counts.Holding, "holds work up", "hold work up")}
		if n := len(v.Held); n > 0 {
			parts = append(parts, fmt.Sprint(n, " held"))
		}
		p.block("FOR YOU   "+strings.Join(parts, " · "), items)
	}
	if !o.Items {
		for i, s := range v.Sections {
			p.block(clean(s.Name), sections[i])
		}
		c := v.Counts
		parts := []string{plural(c.Agents, "agent", "agents"), fmt.Sprint(c.DoneToday, " done today (whaleshark status --all)")}
		if v.Strip.DND {
			parts = append(parts, "do not disturb")
		}
		if v.Strip.Mute {
			parts = append(parts, "muted")
		}
		trail := ""
		if c.Elsewhere > 0 {
			trail = plural(c.Elsewhere, "needs you in another job", "need you in other jobs") + " (--everywhere)"
		}
		p.out.WriteByte('\n')
		for _, line := range v.Lines {
			p.line("", clean(line), "")
		}
		p.line("", strings.Join(parts, " · "), trail)
	}
	p.fresh(w)
}

func newPrinter(v *contract.View, o Options) *printer {
	return &printer{out: new(strings.Builder), o: o, v: v, forms: map[string]string{}, idw: 5, namew: 19, wordw: 12, agew: 3}
}

// alerts prints the lines that stand on top of every screen.
func (p *printer) alerts() {
	for _, line := range p.v.Alerts {
		p.line("", p.paint("blocked", clean(line)), "")
	}
}

// fresh ends a screen with how fresh it is, and hands it over.
func (p *printer) fresh(w io.Writer) {
	v := p.v
	fresh := "live · checked " + seconds(v.At.Sub(v.Fresh.Checked)) + " ago"
	if v.At.Sub(v.Fresh.Checked) > contract.StaleAfter {
		fresh = "STALE since " + v.Fresh.Checked.Format("15:04")
	}
	for _, note := range v.Fresh.Notes {
		fresh += " · " + clean(note)
	}
	p.line("", fresh, "")
	io.WriteString(w, p.out.String())
}

// paint gives a text one of the scheme's colours, where there is colour.
func (p *printer) paint(colour, s string) string {
	rgb, ok := p.o.Colours[colour]
	if !ok || s == "" {
		return s
	}
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm%s\x1b[39m", rgb>>16&0xff, rgb>>8&0xff, rgb&0xff, s)
}

// styled puts a text between two of a terminal's own switches, such as
// bold on and off, where the text goes to a terminal.
func (p *printer) styled(on bool, start, end, s string) string {
	if !on || p.o.Colours == nil {
		return s
	}
	return "\x1b[" + start + "m" + s + "\x1b[" + end + "m"
}

// item is what waits for the person as a row, a choice with its options.
func (p *printer) item(it contract.Item) row {
	p.forms[it.ID] = formWord(it.Form)
	text := clean(it.News)
	if it.Form == contract.FormChoice {
		var options []string
		for _, b := range it.Buttons {
			if b.Answer != contract.AnswerOther {
				options = append(options, clean(b.Answer))
			}
		}
		text += "  (" + strings.Join(options, " / ") + ")"
	}
	return p.measure(row{mark: p.mark(it.Look), id: it.ID, name: it.Name, word: formWord(it.Form), age: age(p.v.At.Sub(it.Since)), text: text, next: it.Next,
		colour: contract.Looks[order[it.Look]].Colour})
}

// card is a card as a row: its word is the figure while it builds, and what
// stands after its news is what it clashes with, what waits about it, or how
// far it had got.
func (p *printer) card(c contract.Card) row {
	r := row{mark: p.mark(c.Look), id: c.Task, name: c.Name, word: string(c.Look), age: age(p.v.At.Sub(c.Since)), text: clean(c.News), next: c.Next,
		colour: c.Colour, bold: c.Unseen}
	pct := fmt.Sprint(c.Progress, "%")
	figure := c.Look == contract.LookBuilding && !c.Said.IsZero()
	switch {
	case c.Word != "":
		r.word = c.Word
	case figure:
		r.word = pct
	}
	if c.Flag {
		r.name += " " + p.mark(contract.LookNeedsYou)
	}
	if c.Pale {
		r.text = fmt.Sprintf("last said %s ago: %q", age(p.v.At.Sub(c.Said)), r.text)
		if !figure {
			r.text += " (" + pct + ")"
		}
	}
	var trail []string
	if c.Clash != nil {
		trail = append(trail, clash(c.Clash))
	}
	switch {
	case c.Flag:
		trail = append(trail, strings.TrimSpace(p.forms[c.Item]+" "+clean(c.Item))+" waits for you")
	case !c.Said.IsZero() && (c.Look == contract.LookNeedsYou || c.Look == contract.LookAtPrompt || c.Look == contract.LookPaused):
		trail = append(trail, pct)
	}
	r.trail = strings.Join(trail, " · ")
	return r
}

// clash is the note of two tasks that change the same file.
func clash(x *contract.Clash) string {
	if x.Conflicts {
		return "clashes with " + clean(x.Task) + ": " + clean(x.File)
	}
	return "also changes " + clean(x.File) + ": " + clean(x.Task)
}

func (p *printer) mark(l contract.Look) string {
	of := contract.Looks[order[l]]
	if p.o.PlainMarks {
		return of.Plain
	}
	return of.Mark
}

func (p *printer) measure(r row) row {
	r.id, r.name, r.word = clean(r.id), clean(r.name), clean(r.word)
	p.idw, p.namew = max(p.idw, cells(r.id)+2), max(p.namew, cells(r.name)+2)
	p.wordw, p.agew = max(p.wordw, cells(r.word)), max(p.agew, cells(r.age))
	return r
}

// block prints a heading and its rows, each with the lines its reader may
// run under it.
func (p *printer) block(heading string, rows []row) {
	if p.out.WriteByte('\n'); heading != "" {
		p.out.WriteString(heading + "\n")
	}
	for _, r := range rows {
		prefix := " " + p.paint(r.colour, r.mark) + " " + pad(r.id, p.idw) + pad(p.styled(r.bold, "1", "22", r.name), p.namew) +
			pad(p.paint(r.colour, r.word), p.wordw) + strings.Repeat(" ", 2+p.agew-cells(r.age)) + r.age + "  "
		p.line(prefix, r.text, r.trail)
		p.steps(strings.Repeat(" ", 3+p.idw), r.next)
	}
}

// line prints text after a prefix, wrapped under itself, and then trail: at
// the right edge where the last line has room, else on a line of its own.
// Where the screen is too narrow for the text beside its row, it goes under
// the row. The words come in the same order at every width.
func (p *printer) line(prefix, text, trail string) {
	if p.o.Width-cells(prefix) < narrow && text+trail != "" {
		p.out.WriteString(strings.TrimRight(prefix, " ") + "\n")
		prefix = strings.Repeat(" ", 3+p.idw)
	}
	room := max(p.o.Width-cells(prefix), narrow)
	lines := wrap(text, room)
	if last := lines[len(lines)-1]; trail != "" && last == "" {
		lines[len(lines)-1] = trail
	} else if gap := room - cells(last) - cells(trail); trail != "" && gap >= 2 {
		lines[len(lines)-1] += strings.Repeat(" ", gap) + trail
	} else if trail != "" {
		lines = append(lines, trail)
	}
	for i, l := range lines {
		if i > 0 {
			prefix = strings.Repeat(" ", cells(prefix))
			if strings.HasPrefix(l, ">") {
				l = "· " + l // what somebody wrote never starts a line as our own ">" lines do
			}
		}
		p.out.WriteString(strings.TrimRight(prefix+l, " ") + "\n")
	}
}

// steps prints the lines a reader may run after an indent: side by side where they fit, else
// one under the other, joined by the contract's word; after "or" the
// program's name is not said again.
func (p *printer) steps(indent string, next []string) {
	if len(next) == 0 {
		return
	}
	parts := make([]string, len(next))
	for i, s := range next {
		if parts[i] = clean(s); i == 0 {
			continue
		}
		if join := contract.NextJoin(clean(next[i-1])); join == "or" {
			parts[i] = "or        " + strings.TrimPrefix(parts[i], "whaleshark ")
		} else {
			parts[i] = join + "        " + parts[i]
		}
	}
	if one := indent + "> " + strings.Join(parts, "        "); cells(one) <= p.o.Width {
		p.out.WriteString(one + "\n")
		return
	}
	for i, part := range parts {
		lead := "> "
		if i > 0 {
			lead = "  "
		}
		p.out.WriteString(indent + lead + strings.Join(strings.Fields(part), " ") + "\n")
	}
}

// clean makes a text one harmless line: nothing in it can recolour, retitle
// or rewrite a terminal, and no line break in it can pass for a row.
func clean(s string) string {
	return cli.Line(s)
}

// narrow is the least room a text is wrapped in.
const narrow = 20

// switches are the colour and bold sequences this printer writes itself;
// everything else of that kind was taken out of the text before.
var switches = regexp.MustCompile("\x1b\\[[0-9;]*m")

// cells is how wide a text stands on the screen.
func cells(s string) int { return uniseg.StringWidth(switches.ReplaceAllString(s, "")) }

func pad(s string, w int) string {
	return s + strings.Repeat(" ", max(w-cells(s), 0))
}

// wrap breaks a text at its spaces into lines of at most w cells. A word
// wider than that stands on a line of its own.
func wrap(s string, w int) []string {
	lines := []string{""}
	for _, word := range strings.Fields(s) {
		last := &lines[len(lines)-1]
		switch {
		case *last == "":
			*last = word
		case cells(*last)+1+cells(word) <= w:
			*last += " " + word
		default:
			lines = append(lines, word)
		}
	}
	return lines
}

func formWord(form string) string {
	switch form {
	case contract.FormSignoff:
		return "sign-off"
	case contract.FormTodo:
		return "to do"
	}
	return "question"
}

// age is a length of time as every row prints it.
func age(d time.Duration) string {
	switch m := int(d / time.Minute); {
	case m < 1:
		return "now"
	case m < 60:
		return fmt.Sprint(m, "m")
	case m < 24*60:
		return fmt.Sprintf("%dh %02dm", m/60, m%60)
	default:
		return fmt.Sprint(m/(24*60), "d")
	}
}

// seconds is the age of the last check, which is counted in seconds while it is young.
func seconds(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprint(int(max(d, 0)/time.Second), "s")
	}
	return age(d)
}
