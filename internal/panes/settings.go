package panes

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// setting is one row of the settings screen: a key of contract.Keys under
// the name `set` takes, with its value as its file holds it, or with do a
// button of its section. to is the value a row asks about, or u puts back.
type setting struct {
	key                    *contract.Key
	name, label, value, to string
	do                     func()
}

// settingsRows is the rows of the section in front, drawn from the table of
// every setting, or of every section those whose name holds what was typed.
func (p *pane) settingsRows() (out []setting) {
	section, query := contract.Sections[p.sect], strings.ToLower(p.query.String())
	server, _ := contract.ReadServer(p.k.Platform.Peek)
	// A key's place is in the one file it lies in, so that file answers it.
	files := []any{&p.cfg, &p.engine, &p.ui, &p.limits, server}
	var names []string
	for _, t := range p.tools {
		names = append(names, t.Name)
	}
	p.tool = cmp.Or(p.tool, names[0])
	tool, _ := contract.FindTool(p.tools, p.tool)
	tool.Name = p.tool
	add := func(k *contract.Key, name, label, value string) {
		if value == "" || value == "0" && strings.HasPrefix(name, "fleet.") {
			value = k.Default // nothing was said: what holds
		}
		if p.finding && strings.Contains(strings.ToLower(label+" "+name), query) || !p.finding && k.Section == section {
			out = append(out, setting{key: k, name: name, label: label, value: value})
		}
	}
	add(&contract.Key{Section: contract.SecTools, Kind: contract.KindChoice, Values: strings.Join(names, "|") + "|NEW",
		Help: "The tool the rows below describe; NEW wires one in under the name you type."}, "", "Agent tool", p.tool)
	for i := range contract.Keys {
		switch k := &contract.Keys[i]; {
		case k.Section == contract.SecTools && strings.HasSuffix(k.Name, ">"):
			for _, v := range slices.Sorted(maps.Keys(tool.Env)) {
				add(k, "tools."+p.tool+".env."+v, k.Label+": "+v, contract.Dots)
			}
			add(k, "tools."+p.tool+".env.", k.Label, "")
		case k.Section == contract.SecTools:
			add(k, strings.Replace(k.Name, "<name>", p.tool, 1), k.Label, k.Of(&tool))
		case strings.Contains(k.Name, "<"):
			for _, e := range p.engine.Shortcuts {
				add(k, "shortcut."+e.Key, k.Label+": "+e.Key, strings.Join(e.Argv, " "))
			}
			add(k, "shortcut.", k.Label, "")
		case k.Section != "":
			value := ""
			for _, file := range files {
				value = cmp.Or(value, k.Of(file))
			}
			add(k, k.Name, k.Label, value)
		}
	}
	press := func(label string, do func()) {
		if !p.finding {
			out = append(out, setting{key: &contract.Key{}, label: label, do: do})
		}
	}
	if section == contract.SecTools {
		// Until a command runs the one test of a tool, this is doctor's line of it.
		press("[ Check it ]", func() {
			p.child(p.cmd("doctor", "--json"), "", func(out []byte, said string, _ bool) {
				var got struct{ Lines []struct{ Text string } }
				result(out, &got)
				p.hint = cmp.Or(said, "doctor says nothing of "+p.tool)
				for _, l := range got.Lines {
					if strings.Contains(l.Text, "agent tool "+p.tool+" ") {
						p.hint = l.Text
					}
				}
			})
		})
		press("[ Take this tool out ]", func() { p.apply(setting{key: &contract.Key{}, name: "tools." + p.tool}, "remove", false) })
	}
	if section == contract.SecProject && len(p.limits.Held) > 0 {
		press("[ Approve… ]", func() {
			p.hint = "nothing is approved by a click · in the new tab type: whaleshark trust"
			p.newTab(p.root, "trust")
		})
	}
	if section == contract.SecPage {
		press(fmt.Sprintf("Phones may reach the page: %s · paired: %d · the page itself: whaleshark dash",
			map[bool]string{true: contract.On, false: contract.Off}[p.phones.On], len(p.phones.Paired)), nil)
		for _, ph := range p.phones.Paired {
			press("[ Forget "+ph.Name+" ]", func() { p.act("forget-phone", map[string]string{"id": ph.ID}) })
		}
	}
	rows := slices.Clone(out)
	if !slices.ContainsFunc(rows, func(s setting) bool { return s.key.Default != "" && s.key.Asks == "" }) {
		return out
	}
	press("[ Put this section back as it came ]", func() {
		for _, s := range rows {
			if s.key.Asks == "" && s.key.Default != "" && s.value != s.key.Default && s.name != "" {
				p.apply(s, s.key.Default, true)
			}
		}
	})
	return out
}

// drawSettings draws the settings screen, which lies over the tab and so has
// the one box there is: the sections, the rows of the one in front, and in
// a wide window the help for the row in front. Narrow, it is one column.
func (p *pane) drawSettings() {
	t, w, h := p.t, p.t.W, p.t.H
	frame, dim, text := p.st("frame"), p.st("dim"), p.st("text")
	box, lt, gt := []rune(p.glyph("┌─┐│└┘", "+-+|++")), p.glyph("‹", "<"), p.glyph("›", ">")
	narrow, a, b := w < 90, 22, w-1
	if w >= 150 {
		a, b = 24, w-56
	}
	if narrow {
		a = 1
	}
	line := strings.Repeat(string(box[1]), w)
	t.Put(0, 0, w, string(box[0])+line, frame)
	t.Put(0, h-1, w, string(box[4])+line, frame)
	t.Put(w-1, 0, 1, string(box[2]), frame)
	t.Put(w-1, h-1, 1, string(box[5]), frame)
	t.Put(2, 0, w-4, " settings ", bold(text))
	for y := 1; y < h-1; y++ {
		for _, x := range []int{0, a - 1, b, w - 1} {
			if y < h-2 || x == 0 || x == w-1 {
				t.Put(x, y, 1, string(box[3]), frame)
			}
		}
	}
	rows := p.settingsRows()
	p.pick = max(min(p.pick, len(rows)-1), 0)
	// The warning of a setting that runs a program ends when its section is seen again.
	if changed := func(s setting) bool { return slices.Contains(p.ui.Changed, s.name) }; p.saw != p.sect && slices.ContainsFunc(rows, changed) {
		p.child(p.cmd("set", "seen", contract.Sections[p.sect]), "", p.quiet)
	}
	p.saw = p.sect
	turn := func(to int) func() {
		return func() { p.sect, p.pick, p.finding = (to+len(contract.Sections))%len(contract.Sections), 0, false }
	}
	title := strings.ToUpper(contract.Sections[p.sect])
	whose := cmp.Or(map[string]string{contract.SecProject: "this project's, for everyone who works in it",
		contract.SecServer: "this server's, the administrator's"}[contract.Sections[p.sect]], "yours, on this computer")
	if p.finding {
		title, whose = "FIND  "+p.query.String()+"_", ""
	}
	y := 4
	if narrow {
		n := len(contract.Sections)
		x := 2 + p.button(2, 1, lt+" "+contract.Sections[(p.sect+n-1)%n], dim, turn(p.sect-1))
		x += 4 + t.Put(x+4, 1, w-x-5, title, bold(text))
		x += 4 + p.button(x+4, 1, contract.Sections[(p.sect+1)%n]+" "+gt, dim, turn(p.sect+1))
		t.Put(x+3, 1, w-x-4, fmt.Sprintf("(%d of %d)", p.sect+1, n), dim)
		y = 3
	} else {
		for i, name := range contract.Sections {
			st := text
			if i == p.sect {
				t.Put(2, 2+i, 1, p.glyph("▌", "|"), p.st("accent"))
				st = bold(st)
			}
			if 2+i < h-2 {
				t.Put(4, 2+i, a-6, name, st)
				p.hits = append(p.hits, hit{1, 2 + i, a - 2, 1, false, turn(i)})
			}
		}
		t.Put(a+1, 2, b-a-2, title, bold(text))
		t.Put(b-1-term.Width(whose), 2, b, whose, dim)
	}
	edge := b - 2
	if narrow {
		edge = b - 1
	}
	for i := max(p.pick-(h-4-y), 0); i < len(rows) && y < h-2; i, y = i+1, y+1 {
		s, st, help := rows[i], text, rows[i].key.Help
		if s.key.Asks == contract.AsksAdmin {
			help = contract.AdminLine
		}
		if s.key.Kind == "" && i > 0 && rows[i-1].key.Kind != "" && y+1 < h-2 {
			y++ // a section's buttons stand apart from its settings
		}
		p.mark(y, 1, s.name+s.label)
		p.hits = append(p.hits, hit{a, y, b - a, 1, false, func() { p.pick = i }})
		if i == p.pick {
			t.Put(a+1, y, 1, gt, p.st("needs"))
			st = bold(st)
		}
		if s.do != nil {
			p.button(a+3, y, s.label, st, s.do)
		} else if t.Put(a+3, y, b-a-4, p.short(s.label, b-a-4), st); s.key.Kind != "" {
			p.value(s, y, edge, i == p.pick || !narrow)
		}
		if i == p.pick && b == w-1 && help != "" && y+1 < h-2 {
			y++
			t.Put(a+5, y, b-a-6, p.short(help, b-a-6), dim)
		}
	}
	if s := rows[p.pick]; b < w-1 {
		lines := append([]string{s.label, ""}, wrap(s.key.Help, 52)...)
		if s.key.Default != "" {
			lines = append(lines, "", "It came as "+s.key.Default+".")
		}
		for i, l := range lines[:min(len(lines), h-4)] {
			t.Put(b+2, 2+i, 52, l, map[bool]term.Style{true: bold(text), false: dim}[i == 0])
		}
	}
	keys, saved := "/ find a setting · j k choose · ← → or a click change · enter type a value · u undo · esc close", "saved as you go"
	if narrow {
		keys = "/ find · j k · ← → change · u undo · esc close"
	}
	keys = p.glyph(keys, strings.Replace(keys, "← →", "< >", 1))
	switch y, room := h-2, w-20; {
	case p.pending != nil:
		x := 3 + t.Put(2, y, w-34, p.short("this runs a program, exactly: "+p.pending.to, w-34), bold(p.st("blocked")))
		x += 1 + p.button(x, y, "[ Use it ]", text, func() { p.settingsKey(term.Event{Key: "y"}) })
		p.button(x, y, "[ Leave it ]", text, func() { p.pending = nil })
	case p.line != nil || p.hint != "":
		p.hintLine(y, w-2, "", "")
	default:
		t.Put(2, y, room, p.short(keys, room), dim)
		t.Put(w-2-len(saved), y, len(saved), saved, dim)
	}
}

// choices is what a row can be set to by a click: the two of a switch, a
// choice's own values, the tools that are wired in.
func (p *pane) choices(s setting) []string {
	switch s.key.Kind {
	case contract.KindSwitch:
		return []string{contract.Off, contract.On}
	case contract.KindChoice:
		return strings.Split(s.key.Values, "|")
	}
	var names []string
	for _, t := range p.tools {
		if t.Found() && s.key.Kind == contract.KindTool {
			names = append(names, t.Name)
		}
	}
	return append(names, strings.Fields(map[bool]string{true: s.key.Values}[s.key.Kind == contract.KindTool])...)
}

// value draws a row's value ending at edge, as what can be clicked: a choice
// shows every value with the one in force in round brackets, a number its
// minus and plus, anything else what a click types over; or the value alone.
func (p *pane) value(s setting, y, edge int, whole bool) {
	type part struct {
		text string
		do   func()
	}
	k, show, c := s.key, s.value, p.choices(s)
	if k.Kind == contract.KindTool && show == contract.FleetWorkerLeader {
		show = "the leader's"
	}
	var parts []part
	if k.Asks == contract.AsksApproval && len(p.limits.Held) > 0 {
		parts = []part{{"held · not approved yet  ", nil}}
	}
	switch {
	case k.Asks == contract.AsksAdmin:
		parts = []part{{cmp.Or(show, "the administrator's"), nil}}
	case !whole:
		parts = []part{{p.glyph("‹ ", "< ") + cmp.Or(show, "not set") + p.glyph(" ›", " >"), nil}}
	case k.Kind == contract.KindTool:
		parts = append(parts, part{"[ " + show + p.glyph(" ▾ ]", " v ]"), func() { p.step(s, 0) }})
	case len(c) > 0:
		for i, v := range c {
			if i > 0 {
				parts = append(parts, part{" ", nil})
			}
			if v == s.value {
				parts = append(parts, part{"( " + v + " )", nil})
			} else {
				parts = append(parts, part{"[ " + v + " ]", func() { p.take(s, v) }})
			}
		}
	case k.Kind == contract.KindNumber:
		parts = append(parts, part{"[ - ]", func() { p.step(s, -1) }}, part{fmt.Sprintf(" %2s  ", show), nil}, part{"[ + ]", func() { p.step(s, 1) }})
		if strings.HasSuffix(k.Values, "|unlimited") {
			parts = append(parts, part{"  ", nil}, part{"[ unlimited ]", func() { p.change(s, "unlimited") }})
		}
	default:
		parts = append(parts, part{"[ " + cmp.Or(p.short(show, 40), "not set") + " ]", func() { p.step(s, 0) }})
	}
	x := edge
	for _, q := range parts {
		x -= term.Width(q.text)
	}
	for _, q := range parts {
		switch st := p.st("text"); {
		case q.do != nil:
			x += p.button(x, y, q.text, st, q.do)
		case q.text[0] == '(':
			x += p.t.Put(x, y, edge-x, q.text, bold(p.st("accent")))
		default:
			x += p.t.Put(x, y, edge-x, q.text, st)
		}
	}
}

// take sets a row to one of its choices. One whose last word is in capitals
// stands for what the person types: a time of day, a new tool's name.
func (p *pane) take(s setting, c string) {
	if i := strings.LastIndex(c, " ") + 1; c[i:] == strings.ToUpper(c[i:]) {
		p.ask(s.label, func(typed string) { p.change(s, c[:i]+typed) })
		return
	}
	p.change(s, c)
}

// step changes a row by a key: left and right take the value before or
// after, Enter the next one round, a button's own doing, or the typing line.
func (p *pane) step(s setting, by int) {
	c := p.choices(s)
	at := slices.Index(c, s.value)
	n, err := strconv.Atoi(s.value)
	switch {
	case s.key.Asks == contract.AsksAdmin:
		p.hint = contract.AdminLine
	case s.key.Kind == "":
		if s.do != nil && by == 0 {
			s.do()
		}
	case len(c) > 0 && by == 0:
		p.take(s, c[(at+1)%len(c)])
	case len(c) > 0:
		p.take(s, c[min(max(at+by, 0), len(c)-1)])
	case s.key.Kind == contract.KindNumber && by != 0 && err == nil:
		p.change(s, strconv.Itoa(n+by))
	default:
		if p.ask(s.label, func(typed string) { p.change(s, typed) }); s.value != contract.Dots {
			p.line.Set(s.value)
		}
	}
}

// change sets a row. A row that stands for many has its name typed first; a
// value that runs a program is shown exactly and asked about once; the
// administrator's is never set here.
func (p *pane) change(s setting, v string) {
	if strings.HasSuffix(s.name, ".") {
		name, rest, _ := strings.Cut(v, " ")
		s.name, v = s.name+name, rest
	}
	switch s.to = v; {
	case s.name == "":
		p.tool = v
	case s.key.Asks == contract.AsksAdmin:
		p.hint = contract.AdminLine
	case s.key.Asks == contract.AsksRuns && v != "":
		p.pending = &s
	default:
		p.apply(s, v, true)
	}
}

// apply runs `set` for a row, which is how every setting is changed, with
// the value in a private file, and keeps what the row was for u.
func (p *pane) apply(s setting, v string, keep bool) {
	args := []string{"set", s.name}
	if v != "" {
		args = append(args, "--file", "{file}")
	}
	if s.key.Section == contract.SecProject {
		args = append(args, "--project")
	}
	p.child(p.cmd(args...), v, func(out []byte, said string, ok bool) {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if p.hint = said; ok && len(lines) > 1 {
			p.hint = lines[0] // what `set` said beside the value it answers
		}
		if s.to = s.value; ok && keep && s.value != contract.Dots {
			p.undone = append(p.undone, s)
		}
		p.load()
	})
}

// settingsKey is a key in the settings screen, and reports false when the
// screen is to close.
func (p *pane) settingsKey(ev term.Event) bool {
	rows, key := p.settingsRows(), ev.Key
	p.pick = max(min(p.pick, len(rows)-1), 0)
	n, by := len(contract.Sections), map[string]int{"left": -1, "h": -1, "right": 1, "l": 1}
	switch p.hint = ""; {
	case p.pending != nil:
		if s := *p.pending; key == "y" || key == "enter" {
			p.apply(s, s.to, true)
		}
		p.pending = nil
	case p.finding && key != "esc" && key != "enter" && key != "up" && key != "down" && key != "left" && key != "right":
		p.query.Key(ev)
		p.pick = 0
	case key == "esc" && p.finding:
		p.finding = false
		p.query.Set("")
	case key == "esc" || key == "q":
		return false
	case key == "/":
		p.finding = true
	case key == "j" || key == "down":
		p.pick++
	case key == "k" || key == "up":
		p.pick--
	case key == "tab" || key == "shift+tab":
		p.sect, p.pick = (p.sect+n+map[bool]int{true: 1, false: -1}[key == "tab"])%n, 0
	case key == "u" && len(p.undone) > 0:
		s := p.undone[len(p.undone)-1]
		p.undone = p.undone[:len(p.undone)-1]
		p.apply(s, s.to, false)
	case key == "enter" || by[key] != 0:
		p.step(rows[p.pick], by[key])
	}
	return true
}
