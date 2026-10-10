package panes

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/theme"
)

const (
	// Two letters closer together than burstGap are a person typing a
	// sentence, not giving commands. A click is refused for clickWait after
	// the rows under it moved by themselves.
	burstGap  = 300 * time.Millisecond
	clickWait = 400 * time.Millisecond
	hereEvery = time.Minute

	typingSaid = "it looks like you are typing: the keys are in this pane · esc goes back to the conversation"
	movedSaid  = "the list moved; click again"
)

// queue has a child started once those before it have ended, so that an
// answer and its undo reach the record in the order they were given.
func (p *pane) queue(job func()) bool {
	select {
	case p.jobs <- job:
		return true
	default:
		p.hint = "too much at once: try again"
		return false
	}
}

// cmd is a command line of ours as a pane starts it: marked as the
// person's, for this project and the run the pane shows.
func (p *pane) cmd(args ...string) []string {
	if !slices.Contains(args, "--human") {
		args = append(args, "--human")
	}
	if args = append(args, "--root", p.root); p.run != "" {
		args = append(args, "--run", p.run)
	}
	return args
}

// child starts the program itself with one command line, which is all a
// button or a key ever does, and tells the loop how it ended. Free text
// rides in a private file, in the place of "{file}", never on the line.
func (p *pane) child(argv []string, text string, then func(out []byte, said string, ok bool)) {
	if p.self == "" {
		return
	}
	p.waiting++
	queued := p.queue(func() {
		var said bytes.Buffer
		var out []byte
		dir, err := "", error(nil)
		if at := slices.Index(argv, "{file}"); at >= 0 {
			if dir, err = p.k.Platform.PrivateTemp(); err == nil {
				defer os.RemoveAll(dir)
				argv[at] = filepath.Join(dir, "text")
				err = os.WriteFile(argv[at], []byte(text), 0o600)
			}
		}
		if err == nil {
			cmd := exec.Command(p.self, argv...) // #nosec G204 -- this program's own file, with a row of the action table
			cmd.Dir, cmd.Env, cmd.Stderr = p.root, append(os.Environ(), contract.EnvFrom+"="+contract.WherePane), &said
			out, err = cmd.Output()
		}
		if err != nil && said.Len() == 0 {
			said.WriteString(err.Error())
		}
		lines := strings.Join(strings.Split(strings.TrimSpace(said.String()), "\n"), " · ")
		p.tell(news{do: func() {
			p.waiting--
			then(out, lines, err == nil)
		}})
	})
	if !queued {
		p.waiting--
	}
}

// quiet is how a child of the pane's own housekeeping ends: only a failure is said.
func (p *pane) quiet(_ []byte, said string, ok bool) {
	if !ok {
		p.hint = said
	}
}

// result reads what a command answered under --json.
func result(out []byte, v any) bool {
	return json.Unmarshal(out, &struct{ Result any }{v}) == nil
}

// sweep starts the command that looks for stalled agents. The pane never
// looks itself and never writes: it only starts the looking, as a child.
func (p *pane) sweep() {
	if p.run == "" || len(p.jobs) > 0 {
		return
	}
	p.child(p.cmd("status", "--sweep", "--quiet", "--json"), "", func(out []byte, _ string, ok bool) {
		if ok && result(out, &p.swept) {
			p.sweptAt = p.now()
		}
	})
}

// present notes that the person is here: a key or a click in this pane, or
// the keeper saying a pane has the keys. It is written at most once a minute.
func (p *pane) present() {
	if now := p.now(); now.Sub(p.here) >= hereEvery && now.Sub(p.ui.LastHere) >= hereEvery {
		p.here = now
		p.child(p.cmd("set", "here", "now"), "", p.quiet)
	}
}

// picked is the selected item of the action pane, and cardOf the card of a
// task; nil with none.
func (p *pane) picked() *contract.Item {
	if at := slices.Index(p.ids(), p.sel); at >= 0 && p.kind != fleet {
		return &p.items[at]
	}
	return nil
}

func (p *pane) cardOf(task string) *contract.Card {
	if at := slices.IndexFunc(p.cards, func(c contract.Card) bool { return c.Task == task }); at >= 0 {
		return &p.cards[at]
	}
	return nil
}

// typing catches what was meant for the lead agent. A letter that follows
// another within burstGap is ignored with all that follow as closely, the
// Enter that ends the sentence too, and what the first letter did is taken
// back. Stepping through the list with j and k is not a sentence. The two
// guards count on the machine's own clock, whatever clock the record is on.
func (p *pane) typing(ev term.Event) bool {
	now, step := time.Now(), func(k string) bool { return k == "j" || k == "k" }
	quick := now.Sub(p.letter) < burstGap
	switch {
	case ev.Rune == 0:
		p.burst = p.burst && quick && ev.Key == "enter"
		return p.burst
	case quick && !(step(ev.Key) && step(p.lastKey)):
		if p.burst = true; p.undo != nil {
			p.undo()
		}
		p.hint, p.sure = typingSaid, ""
	default:
		p.burst = false
	}
	p.undo, p.letter, p.lastKey = nil, now, ev.Key
	return p.burst
}

// key takes a key press or a paste and reports false when the pane is to end.
func (p *pane) key(ev term.Event) bool {
	key := ev.Key
	p.present()
	switch {
	case key == "ctrl+c":
		return false
	case p.over == "" && (p.line == nil || p.undo != nil) && p.typing(ev):
	case p.line != nil:
		p.typed(ev)
	case ev.Kind == term.Paste:
		// Pasted text is only ever put into an open typing line.
	case p.over != "":
		p.overKey(ev)
	case key == "q":
		return false
	case key == "j" || key == "down":
		p.move(1)
	case key == "k" || key == "up":
		p.move(-1)
	case key == "tab":
		p.hand(map[string]string{fleet: actions, actions: fleet}[p.kind])
	case key == "esc":
		p.hand("")
	case key == "o" && p.cardOf(p.sel) != nil:
		p.offer(*p.cardOf(p.sel))
	default:
		p.hint, p.asked, p.sure = "", p.sure, ""
		if it := p.picked(); it != nil {
			for _, b := range it.Buttons {
				if b.Key == key || key == "enter" && b.Action == "go" {
					p.press(*it, b)
					return true
				}
			}
			if it.Line && it.Answer == "" && (key == "o" || key == "enter") {
				p.open(it.ID, "")
				return true
			}
		}
		for _, a := range contract.Actions {
			if a.Key == key {
				p.act(a.ID, nil)
			}
		}
	}
	return true
}

// offer opens what can be done with a card.
func (p *pane) offer(c contract.Card) {
	if len(c.Actions) > 0 {
		p.sel, p.shown, p.over, p.pick = c.Task, c, "card", 0
	}
}

// press is a button of an item: an answer given at once, the typing line
// for an answer that needs words, or the row of the action table it names.
func (p *pane) press(it contract.Item, b contract.Button) {
	fill := map[string]string{"id": it.ID, "task": it.Task}
	switch p.sel = it.ID; {
	case b.Action != "":
		p.act(b.Action, fill)
	case strings.HasSuffix(b.Answer, ":"):
		p.open(it.ID, b.Answer)
	default:
		fill["text"] = b.Answer
		p.act("answer", fill)
		p.undo = func() { p.act("undo", map[string]string{"id": it.ID}) }
	}
}

// act runs one row of the action table on what is selected. It is the only
// way a click or a key changes anything.
func (p *pane) act(id string, fill map[string]string) {
	a := contract.Actions[slices.IndexFunc(contract.Actions, func(a contract.Action) bool { return a.ID == id })]
	if fill == nil {
		fill = map[string]string{}
	}
	given := func(name, value string) {
		if _, ok := fill[name]; !ok && (value != "" || name == "value") {
			fill[name] = value
		}
	}
	toggle := map[bool]string{true: "off", false: "on"}
	switch id {
	case "undo":
		if len(p.view.Answered) > 0 {
			given("id", p.view.Answered[0].ID)
		}
	case "dnd":
		given("value", toggle[p.view.Strip.DND])
	case "mute":
		given("value", toggle[p.view.Strip.Mute])
	case "theme":
		p.scheme = theme.Get(theme.Next(p.scheme.Name))
		given("value", p.scheme.Name)
		p.hint = "colour scheme: " + p.scheme.Name
	case "fleet", "actions":
		given("value", "")
	}
	if it := p.picked(); it != nil {
		given("id", it.ID)
		given("task", it.Task)
	} else if c := p.cardOf(p.sel); c != nil {
		given("id", c.Item)
		given("task", c.Task)
	}
	c := p.cardOf(fill["task"])
	if a.Confirm && p.asked != id {
		what := a.Label
		if id == "stop-all" {
			what = fmt.Sprintf("Stop all %d agents", p.view.Counts.Agents)
		} else if c != nil {
			what += " " + c.Name
		}
		p.sure, p.hint = id, fmt.Sprintf("%s? press %s again", what, cmp.Or(a.Key, "it"))
		return
	}
	switch {
	case id == "go" && c != nil:
		p.goTo(c.Tab)
	case id == "menu" || id == "keys":
		p.fold(false)
		p.over, p.pick = id, 0
		p.query.Set("")
	case id == "fold":
		p.fold(!p.folded)
	case id == "show" && p.kind == fleet:
		p.all, p.stale = !p.all, true
	case id == "show":
		p.over, p.pick = "answered", 0
	case id == "team" && fill["name"] == "":
		p.child(p.cmd("team", "list", "--json"), "", func(out []byte, said string, ok bool) {
			if p.teams = nil; !ok || !result(out, &p.teams) {
				p.hint = cmp.Or(said, "the saved teams could not be read")
			} else if p.over, p.pick = "teams", 0; len(p.teams) == 0 {
				p.over, p.hint = "", "no team is saved yet: whaleshark team save <name>"
			}
		})
	case a.How == contract.HowResize:
		p.widen(id == "wider")
	case a.How == contract.HowPoint && p.state != nil && p.state.Run.Orchestrator != nil:
		lead := p.state.Run.Orchestrator.Pane
		p.helper(func() {
			said := "the lead agent was told"
			if err := p.k.Terms.Point(lead, contract.Pointer(a.Run[0]), ""); err != nil {
				said = "the lead agent could not be told: " + err.Error()
			}
			p.tell(news{said: said})
		})
	case a.How == contract.HowPoint:
		p.hint = "no lead agent holds this run"
	default:
		p.launch(a, fill)
	}
}

// launch fills a row's command line in and starts it. A task or an item comes
// from what is selected; anything else that is missing is typed on a line.
// A start or a check, which can take minutes, is a child like any other: the
// command moves itself into a tab of its own and the child ends at once.
func (p *pane) launch(a contract.Action, fill map[string]string) {
	var argv []string
	for _, word := range a.Run {
		name := strings.Trim(word, "{}")
		if word == "{file}" {
			name = "text"
		}
		v, ok := fill[name]
		switch {
		case name == word:
			argv = append(argv, word)
		case ok && word == "{file}":
			argv = append(argv, word)
		case ok && v != "":
			argv = append(argv, v)
		case ok:
		case name == "id" || name == "task":
			p.hint = a.Label + ": choose " + map[string]string{"id": "an item", "task": "a card"}[name] + " first"
			return
		default:
			p.ask(a.Label, func(typed string) {
				if name == "key" {
					typed, fill["value"], _ = strings.Cut(typed, " ")
				}
				fill[name] = typed
				p.launch(a, fill)
			})
			return
		}
	}
	if goal := fill["goal"]; goal != "" {
		argv = append(argv, "--goal="+goal)
	}
	switch {
	case p.self == "":
		p.hint = a.Label + ": this pane was not started by the program"
	case a.ID == "catchup":
		// From when the person left, not from the key that asks: that key,
		// and coming back to the window before it, already said "here".
		if argv = append(argv, "--json"); !p.away.IsZero() {
			argv = append(argv, "--since", p.away.Format(time.RFC3339))
		}
		p.child(p.cmd(argv...), "", func(out []byte, said string, ok bool) {
			if p.catch = (contract.Catchup{}); ok && result(out, &p.catch) {
				p.over, p.hint, p.away = a.ID, "", time.Time{}
			} else {
				p.hint = cmp.Or(said, "catch me up: the answer could not be read")
			}
		})
	default:
		p.child(p.cmd(argv...), fill["text"], func(out []byte, said string, ok bool) {
			if lines := strings.Split(strings.TrimSpace(string(out)), "\n"); ok && a.ID == "answer" {
				said = "" // the item says it, and counts down how long it can be taken back
			} else if ok {
				said = lines[len(lines)-1]
			} else if p.kind == menu {
				p.over = menu
			}
			// What a letter of a typed sentence started, and its taking
			// back, end a moment after the warning: the warning stays.
			if ok && p.hint == typingSaid {
				return
			}
			p.hint = said
		})
	}
}

// open puts the typing line on an item, with what was left there; pre
// stands in front of what is sent.
func (p *pane) open(id, pre string) {
	p.leave()
	p.fold(false)
	p.line, p.lineFor, p.linePre, p.lineDo = &term.Line{}, id, pre, nil
	p.line.Set(p.ui.Drafts[id])
	p.sel, p.follow = id, true
	p.undo = func() { p.line = nil }
}

// ask has the person type what a row of the action table still needs.
func (p *pane) ask(label string, do func(string)) {
	p.leave()
	p.line, p.lineFor, p.linePre, p.lineDo, p.over = &term.Line{}, "", label+":", do, ""
}

// typed is a key while the typing line is open. Enter sends the line; Escape
// leaves it and keeps what was typed.
func (p *pane) typed(ev term.Event) {
	text := strings.TrimSpace(p.line.String())
	switch {
	case ev.Key == "esc":
		p.leave()
	case ev.Key != "enter":
		p.line.Key(ev)
	case text == "":
		p.hint = "nothing is typed yet · esc leaves the line"
	case p.lineDo != nil:
		p.line = nil
		p.lineDo(text)
	default:
		p.line = nil
		p.act("answer", map[string]string{"id": p.lineFor, "text": strings.TrimSpace(p.linePre + " " + text)})
		p.draft(p.lineFor, "")
	}
}

// leave closes the typing line. What was typed for an item stays its draft.
func (p *pane) leave() {
	if p.line != nil && p.lineFor != "" {
		p.draft(p.lineFor, p.line.String())
	}
	p.line = nil
}

// draft keeps a half-typed answer with its item in ui.json, or drops it.
func (p *pane) draft(id, text string) {
	if p.ui.Drafts[id] == text {
		return
	}
	drafts := map[string]string{}
	for k, v := range p.ui.Drafts {
		drafts[k] = v
	}
	if drafts[id] = text; text == "" {
		delete(drafts, id)
		p.child(p.cmd("set", "draft."+id), "", p.quiet)
	} else {
		p.child(p.cmd("set", "draft."+id, "--file", "{file}"), text, p.quiet)
	}
	p.ui.Drafts = drafts
}

// fold folds the action pane to its strip or opens it again: the pane asks
// for its own place to shrink, and the mark is written as every setting is.
func (p *pane) fold(folded bool) {
	if p.kind != actions || p.folded == folded {
		return
	}
	p.leave()
	p.folded = folded
	if me, top := p.me, p.above; me != "" {
		p.helper(func() {
			if err := shrink(p.k.Terms, me, top, folded); err != nil {
				p.tell(news{said: "the pane keeps its height: " + err.Error()})
			}
		})
	}
	p.child(p.cmd("set", "folded", map[bool]string{true: "on", false: "off"}[folded]), "", p.quiet)
}

// widen moves the fleet's edge one step and has the new width remembered.
func (p *pane) widen(wider bool) {
	pane := p.ui.FleetPane
	if p.kind == fleet && p.me != "" {
		pane = p.me
	}
	if pane == "" {
		p.hint = "the fleet is closed · f opens it"
		return
	}
	p.helper(func() {
		w, err := widen(p.k.Terms, pane, wider)
		p.tell(news{do: func() {
			if err != nil {
				p.hint = "the width stays: " + err.Error()
			} else {
				p.child(p.cmd("set", "fleet.width", strconv.Itoa(w)), "", p.quiet)
			}
		}})
	})
}

// hand gives the keys to the other pane of ours, or with "" back to the
// conversation.
func (p *pane) hand(to string) {
	ui := p.ui
	if to == fleet && ui.FleetPane == "" || to == actions && ui.ActionsPane == "" {
		p.hint = "the " + map[string]string{fleet: "fleet", actions: "action pane"}[to] + " is closed"
		return
	}
	p.helper(func() {
		if err := hand(p.k.Terms, ui, to); err != nil {
			p.tell(news{said: "the keys stay here: " + err.Error()})
		}
	})
}

// saved is a team as `team list` answers it. One whose words hold {goal}, or
// which has briefs that may, is run with a goal.
type saved struct {
	Name, From, About string
	Held              bool
	Tasks             []struct{ Title, Brief string }
}

// entry is one line of a list drawn over the pane: what it says, what
// stands at its right, and what Enter or a click on it does.
type entry struct {
	text, right string
	do          func()
}

// entries is the list that lies over the pane: every action, all keys, the
// catch-up, the last answers, or what can be done with one card.
func (p *pane) entries() (title string, list []entry, foot string) {
	close := func(do func()) func() {
		return func() {
			was := p.over
			p.over = ""
			if do(); p.sure != "" {
				p.over = was // it asked "sure?": the same line again says yes
			}
		}
	}
	switch p.over {
	case "menu", "keys":
		for _, a := range contract.Actions {
			right := a.Key
			if run := strings.Join(a.Run, " "); right == "" && strings.Contains(run, "{task}") {
				right = "(card)"
			} else if right == "" && strings.Contains(run, "{id}") {
				right = "(item)"
			}
			if p.over == "keys" && a.Key != "" || p.over == "menu" && strings.Contains(strings.ToLower(a.Label), strings.ToLower(p.query.String())) {
				list = append(list, entry{a.Label, right, close(func() { p.act(a.ID, nil) })})
			}
		}
		if p.over == "keys" {
			for _, k := range [][2]string{{"Choose an item or a card", "j k"}, {"The first button, the second, the typing line", "y n o"},
				{"Pick an option", "1-9"}, {"What can be done with a card", "o"}, {"Send the typed line; leave it", "enter esc"}, {"Between the two panes", "tab"},
				{"Back to the conversation", "esc"}, {"Leave this pane", "q"}} {
				list = append(list, entry{text: k[0], right: k[1]})
			}
			return "all keys", list, "any key closes"
		}
		// The person's own settings are lines of this list: each says what
		// it is now, or what choosing it makes of it.
		on, n := map[bool]string{true: "on", false: "off"}, p.cfg.Nudge
		side := map[bool]string{true: "bottom", false: "top"}[p.above]
		rows := [][4]string{{"The action pane at the " + side, "", "actions", side}, {"A sound when you are needed", on[n.Sound], "nudge.sound", "toggle"},
			{"A pop-up when you are needed", on[n.Popup], "nudge.popup", "toggle"}, {"A message to the phone when you are needed", on[n.Phone], "nudge.phone", "toggle"}}
		for _, s := range theme.Schemes {
			rows = append(rows, [4]string{"Colour scheme: " + s.Name, map[bool]string{true: "now"}[s.Name == p.scheme.Name], "theme", s.Name})
		}
		for _, r := range rows {
			if strings.Contains(strings.ToLower(r[0]), strings.ToLower(p.query.String())) {
				list = append(list, entry{r[0], r[1], close(func() { p.act("set", map[string]string{"key": r[2], "value": r[3]}) })})
			}
		}
		return "every action", list, fmt.Sprintf("%d actions and settings · type to search · enter runs it · esc closes", len(list))
	case "teams":
		for _, t := range p.teams {
			run := func(goal string) { p.act("team", map[string]string{"name": t.Name, "goal": goal}) }
			e := entry{strings.TrimSuffix(t.Name+" · "+t.About, " · "), fmt.Sprintf("%d tasks", len(t.Tasks)), close(func() { run("") })}
			if t.Held {
				e.right = "not approved"
			}
			if slices.ContainsFunc(t.Tasks, func(k struct{ Title, Brief string }) bool {
				return k.Brief != "" || strings.Contains(k.Title, "{goal}")
			}) {
				e.do = close(func() { p.ask("Goal for "+t.Name, run) })
			}
			list = append(list, e)
		}
		return "saved teams", list, "enter adds its tasks to the run and starts nothing · esc closes"
	case "catchup":
		c := p.catch
		row := func(label string, n int, text string) {
			list = append(list, entry{text: fmt.Sprintf("%-21s%3d   %s", label, n, text)})
		}
		row("Waiting for you now", c.Waiting.N, c.Waiting.Text)
		row("Finished and checked", c.Finished.N, c.Finished.Text)
		row("Check failed", c.CheckFailed.N, c.CheckFailed.Text)
		row("Clashes", c.Clashes.N, c.Clashes.Text)
		row("Went idle", c.WentIdle.N, c.WentIdle.Text)
		row("Answered by the lead", c.AnsweredLead, "")
		row("Done as you", c.DoneAsYou.N, c.DoneAsYou.Text)
		row("Still building", c.StillBuilding, fmt.Sprintf("Stopped %d   Failed %d", c.Stopped, c.Failed))
		return fmt.Sprintf("WHILE YOU WERE AWAY  %s  (%s to %s)", ago(c.To.Sub(c.From)), c.From.Local().Format("15:04"),
			c.To.Local().Format("15:04")), list, "any key closes"
	case "answered":
		for _, it := range p.view.Answered {
			e := entry{text: strings.TrimPrefix(it.Name+" · ", " · ") + it.Text + " → " + it.Answer, right: "used at " + it.Used.Local().Format("15:04")}
			if it.Used.IsZero() {
				e.right, e.do = "[ Undo ]", func() { p.act("undo", map[string]string{"id": it.ID}) }
			}
			list = append(list, e)
		}
		return fmt.Sprintf("answered: %d", len(list)), list, "enter or a click takes an answer back · esc closes"
	}
	for _, id := range p.shown.Actions {
		list = append(list, entry{text: label(id), do: close(func() { p.act(id, map[string]string{"task": p.shown.Task, "id": p.shown.Item}) })})
	}
	return p.shown.Name, list, "enter runs it · esc closes"
}

// overKey is a key while a list lies over the pane.
func (p *pane) overKey(ev term.Event) {
	_, list, _ := p.entries()
	p.asked, p.sure = p.sure, ""
	switch {
	case p.over == "keys" || p.over == "catchup" || ev.Key == "esc":
		p.over = ""
	case ev.Key == "enter":
		if p.pick < len(list) && list[p.pick].do != nil {
			list[p.pick].do()
		}
	case ev.Key == "up" || ev.Key == "k" && p.over != "menu":
		p.pick--
	case ev.Key == "down" || ev.Key == "j" && p.over != "menu":
		p.pick++
	case p.over == "menu":
		p.query.Key(ev)
		p.pick = 0
	}
}
