// Package dashweb is the screens of the page: one template file over the
// view model, one style sheet, one script, and the two small files of a
// phone. It prints what the view says and keeps no state. Nothing in it is
// inline and nothing names another address, so the page's server can forbid
// both by header.
package dashweb

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/theme"
)

//go:embed page.html page.css page.js notice.js home.webmanifest
var files embed.FS

var pages = template.Must(template.New("page.html").Funcs(template.FuncMap{
	"do": do, "press": press, "line": line, "act": act, "strip": strip, "look": look, "age": age, "clock": clock,
	"fresh": fresh, "word": word, "news": news, "clash": clash, "ctx": ctx, "by": by, "left": left, "plan": plan,
	"job": job, "catch": catch, "check": check, "moments": moments, "counts": counts, "foryou": foryou,
	"scheme": scheme, "each": each, "cards": cards, "join": strings.Join, "base": filepath.Base,
	"elsewhere": func(n int) string { return plural(n, "needs you in another job", "need you in other jobs") },
	"needs":     func() contract.LookOf { return contract.Looks[0] },
	"open":      func(c contract.Card) string { return contract.RouteTask + c.Task },
	"shot":      func(t contract.Try, i int) string { return contract.RouteEvidence + t.Attempt + "/" + fmt.Sprint(i) },
}).ParseFS(files, "page.html"))

// Plug puts the screens and their fixed files into the kit; it binds nothing.
func Plug(k *contract.Kit) { k.Page, k.Asset = Page, Asset }

// Page draws one screen.
func Page(w io.Writer, p *contract.Page) error { return pages.Execute(w, p) }

// sheet is the style sheet: each scheme of the panes' own table as
// variables, a class for each of the sixteen names, then the fixed rules.
var sheet = func() []byte {
	var b strings.Builder
	for _, s := range theme.Schemes {
		if s.Name == theme.None {
			continue // the fixed rules give it the browser's own colours
		}
		fmt.Fprintf(&b, ".t-%s{--ground:#%06x;--pale:#%06x", s.Name, s.Ground, contract.Pale(s.Colours["building"], s.Ground))
		for _, name := range contract.Colours {
			fmt.Fprintf(&b, ";--%s:#%06x", name, s.Colours[name])
		}
		b.WriteString("}\n")
	}
	for _, name := range contract.Colours {
		fmt.Fprintf(&b, ".c-%s{color:var(--%[1]s)}\n", name)
	}
	fixed, _ := files.ReadFile("page.css")
	return append([]byte(b.String()), fixed...)
}()

// Asset hands out a fixed file by its name after contract.RouteAsset.
func Asset(name string) (body []byte, kind string, ok bool) {
	kind, ok = map[string]string{"page.css": "text/css; charset=utf-8", "page.js": "text/javascript; charset=utf-8",
		"notice.js": "text/javascript; charset=utf-8", "home.webmanifest": "application/manifest+json"}[name]
	if !ok {
		return nil, "", false
	}
	if name == "page.css" {
		return sheet, kind, true
	}
	body, err := files.ReadFile(name)
	return body, kind, err == nil
}

// scheme is the class of the person's colour scheme.
func scheme(name string) string { return "t-" + theme.Get(name).Name }

// form is one button: an ordinary form that posts one row of the action
// table. Line names the field typed in, Pre is what that line begins with,
// Open leaves the line in view, Sure asks once and On is a switch that is on.
type form struct {
	ID, Action, Path, Label, Key, Line, Pre string
	Fields                                  [][2]string
	Open, Sure, On                          bool
}

// do is the form of an action: with gives fixed values to the row's
// placeholders by name, and "label" another word for the button. A
// placeholder for text that nobody gave is the line to type on.
func do(p *contract.Page, action string, with ...string) *form {
	i := slices.IndexFunc(contract.Actions, func(a contract.Action) bool { return a.ID == action })
	if i < 0 {
		return nil
	}
	a := contract.Actions[i]
	f := &form{ID: action, Action: action, Path: contract.RouteDo + action, Label: a.Label, Key: a.Key, Sure: a.Confirm,
		Fields: [][2]string{{contract.FieldToken, p.Token}}}
	given := map[string]string{}
	for i := 0; i+1 < len(with); i += 2 {
		given[with[i]] = with[i+1]
	}
	if given["label"] != "" {
		f.Label = given["label"]
	}
	for _, arg := range a.Run {
		name := strings.Trim(arg, "{}")
		if name == arg {
			continue
		}
		if name == "file" {
			name = "text"
		}
		if v, ok := given[name]; ok {
			f.Fields, f.ID = append(f.Fields, [2]string{name, v}), f.ID+"-"+v
		} else if name == "text" {
			f.Line = name
		}
	}
	return f
}

// press is a button of an item: an answer given at once, the line for an
// answer that needs words, or the row of the action table it names. A phone
// has no tab to go to.
func press(p *contract.Page, it contract.Item, b contract.Button) *form {
	var f *form
	switch {
	case b.Action == "go" && p.Phone:
		return nil
	case b.Action != "":
		f = do(p, b.Action, "id", it.ID, "task", it.Task, "label", b.Label)
	case strings.HasSuffix(b.Answer, ":"):
		f = do(p, "answer", "id", it.ID, "label", b.Label)
		f.Pre, f.ID = b.Answer+" ", f.ID+"-"+b.Key
	default:
		f = do(p, "answer", "id", it.ID, "text", b.Answer, "label", b.Label)
	}
	if f != nil && b.Key != "" {
		f.Key = b.Key
	}
	return f
}

// line is the line a question is answered on, which stands open.
func line(p *contract.Page, it contract.Item) *form {
	f := do(p, "answer", "id", it.ID, "label", "Send")
	f.Open, f.Key = true, "o"
	return f
}

// act is one of the things a card offers, for its task.
func act(p *contract.Page, c contract.Card, action string) *form {
	if action == "go" && p.Phone {
		return nil
	}
	return do(p, action, "task", c.Task)
}

// entry is an item with the page it stands on; Count is set on the first of
// those Do not disturb keeps back. row is the same for a card.
type entry struct {
	P *contract.Page
	contract.Item
	Held  bool
	Count int
}

type row struct {
	P *contract.Page
	contract.Card
}

func each(p *contract.Page, items []contract.Item, held bool) []entry {
	out := make([]entry, len(items))
	for i, it := range items {
		out[i] = entry{P: p, Item: it, Held: held}
	}
	if held && len(out) > 0 {
		out[0].Count = len(out)
	}
	return out
}

func cards(p *contract.Page, list []contract.Card) []row {
	out := make([]row, len(list))
	for i, c := range list {
		out[i] = row{p, c}
	}
	return out
}

// strip is the four buttons that are always there.
func strip(p *contract.Page) []*form {
	if p.View == nil {
		return nil
	}
	s, onOff := p.View.Strip, map[bool]string{true: "off", false: "on"}
	stop := do(p, "stop-all")
	if s.Paused {
		stop = do(p, "resume")
	}
	four := []*form{stop, do(p, "catchup"), do(p, "dnd", "value", onOff[s.DND]), do(p, "mute", "value", onOff[s.Mute])}
	four[2].On, four[3].On = s.DND, s.Mute
	if p.Phone {
		four[1].Label, four[2].Label = "Catch up", "DND"
	}
	return four
}

func look(l contract.Look) contract.LookOf {
	return contract.Looks[max(slices.IndexFunc(contract.Looks, func(o contract.LookOf) bool { return o.Look == l }), 0)]
}

// now is the moment a screen was built at.
func now(p *contract.Page) time.Time {
	if p.View != nil {
		return p.View.At
	}
	return contract.Now()
}

// age is a length of time as every row prints it.
func age(p *contract.Page, since time.Time) string {
	switch m := int(now(p).Sub(since) / time.Minute); {
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

func clock(p *contract.Page, t time.Time) string { return t.In(now(p).Location()).Format("15:04") }

// freshness is the age line. The script counts Age on from the moment the
// screen arrived, and asks again before the words could become untrue.
type freshness struct {
	Before, After string
	Age           int
	Stale         bool
}

func fresh(v *contract.View) freshness {
	d := max(v.At.Sub(v.Fresh.Checked), 0)
	f := freshness{Before: "live · checked ", After: "s ago", Age: int(d / time.Second)}
	if f.Stale = d > contract.StaleAfter; f.Stale {
		f = freshness{Before: "STALE since " + v.Fresh.Checked.In(v.At.Location()).Format("15:04"), Age: -1, Stale: true}
	}
	for _, note := range v.Fresh.Notes {
		f.After += " · " + note
	}
	return f
}

// word is what stands beside a card's mark.
func word(p *contract.Page, c contract.Card) string {
	switch {
	case c.Word != "":
		return c.Word
	case c.Look == contract.LookIdle:
		return string(c.Look) + " " + age(p, c.Since)
	}
	return string(c.Look)
}

// news is a card's third line: a change that would conflict is the news,
// one that only meets another follows it.
func news(p *contract.Page, c contract.Card) string {
	text := c.News
	if c.Pale && !c.Said.IsZero() {
		text = "said " + age(p, c.Said) + " ago: " + text
	}
	if x := c.Clash; x != nil && x.Conflicts {
		return "clashes with " + x.Name + ": " + x.File
	} else if x != nil {
		text += " · also changes " + x.File
	}
	return text
}

// clash is the note of two tasks that change the same file, by their ids.
func clash(x *contract.Clash) string {
	if x.Conflicts {
		return "clashes with " + x.Task + ": " + x.File
	}
	return "also changes " + x.File + ": " + x.Task
}

// ctx is the colour name of a context figure.
func ctx(c contract.Card) string {
	switch {
	case c.Ctx > contract.CtxHigh:
		return "ctx_high"
	case c.Ctx >= contract.CtxMid:
		return "ctx_mid"
	}
	return "ctx_low"
}

// by says where something done as the person came from.
func by(o *contract.Origin) string {
	if o == nil {
		return ""
	}
	from := map[string]string{contract.WherePane: "by you, from the pane", contract.WherePage: "by you, from the page",
		contract.WherePhone: "by you, from your phone", contract.WhereRelayed: "relayed by the lead agent"}[o.Where]
	if from == "" && o.Caller == contract.Human {
		return "by you, typed"
	}
	return from
}

// left is the whole seconds until an answer may be used.
func left(p *contract.Page, it contract.Item) int {
	return int(max(it.Settles.Sub(now(p))+time.Second-1, 0) / time.Second)
}

// plan is the word and what follows it on a row of the plan.
func plan(p *contract.Page, r contract.PlanRow) [2]string {
	c := r.Card
	w := word(p, c)
	switch {
	case r.HeldBy != "":
		w = "held by " + r.HeldBy
	case c.Item != "":
		w = contract.Looks[0].Mark + " " + c.Item
	case c.Word == "" && c.Look == contract.LookBuilding && !c.Said.IsZero():
		w = fmt.Sprint(c.Progress, "%")
	case c.Look == contract.LookIdle:
		w = string(c.Look)
	}
	after := ""
	if n := len(r.After); n > 3 {
		after = "after " + r.After[0] + " ... " + r.After[n-1]
	} else if n > 0 {
		after = "after " + strings.Join(r.After, ", ")
	}
	if r.Waits > 0 && len(r.After) > 1 {
		after += fmt.Sprint("  (waits on ", r.Waits, ")")
	}
	return [2]string{w, after}
}

// job is the look and the word of one open run in the list of jobs, and the
// address that makes it the job this browser is shown.
func job(j contract.Job) (out struct {
	contract.LookOf
	Word, To string
}) {
	n := j.View.Counts
	out.To = contract.RouteTeam + "?" + url.Values{contract.FieldRoot: {j.Root}, contract.FieldRun: {j.View.Run}}.Encode()
	out.LookOf, out.Word = look(contract.LookQueued), plural(n.Agents, "agent", "agents")
	if n.Agents > 0 {
		out.LookOf = look(contract.LookBuilding)
	}
	if n.Waiting > 0 {
		out.LookOf, out.Word = look(contract.LookNeedsYou), fmt.Sprint(n.Waiting, " waiting")
	}
	return out
}

// catch is "while you were away": its head and its lines, the same words
// every time.
func catch(c *contract.Catchup) (out struct {
	Head string
	Rows [][3]string
}) {
	from, day := c.From.In(c.To.Location()), "15:04"
	if from.Format(time.DateOnly) != c.To.Format(time.DateOnly) {
		day = "2 Jan 15:04"
	}
	out.Head = fmt.Sprintf("%s  (%s to %s)", age(&contract.Page{View: &contract.View{At: c.To}}, from), from.Format(day), c.To.Format(day))
	line := func(label string, l contract.CatchLine) [3]string { return [3]string{label, fmt.Sprint(l.N), l.Text} }
	out.Rows = [][3]string{
		line("Waiting for you now", c.Waiting), line("Finished and checked", c.Finished), line("Check failed", c.CheckFailed),
		line("Clashes", c.Clashes), line("Went idle", c.WentIdle), {"Answered by the lead", fmt.Sprint(c.AnsweredLead)},
		line("Done as you", c.DoneAsYou),
		{"Still building", fmt.Sprint(c.StillBuilding), fmt.Sprint("Stopped  ", c.Stopped, "   Failed  ", c.Failed)},
	}
	return out
}

// check is a task's check line: the command and how its last run ended.
func check(p *contract.Page, t *contract.TaskView) string {
	if r := t.Result; r != nil {
		return t.Check + " · " + map[bool]string{true: "passed ", false: "FAILED "}[r.OK] + clock(p, r.At) + " · " + r.Tail
	}
	return t.Check
}

// moments is a try's history on one line.
func moments(p *contract.Page, t contract.Try) string {
	out := make([]string, len(t.History))
	for i, e := range t.History {
		out[i] = clock(p, e.At) + " " + e.Text
	}
	return strings.Join(out, " · ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprint(n, " ", one)
	}
	return fmt.Sprint(n, " ", many)
}

// counts is the figures of the team: the agents and what waits among them.
func counts(v *contract.View) string {
	c := v.Counts
	parts := []string{plural(c.Agents, "agent", "agents")}
	if c.NeedYou > 0 {
		parts = append(parts, plural(c.NeedYou, "needs you", "need you"))
	}
	if c.CheckFailed > 0 {
		parts = append(parts, fmt.Sprint(c.CheckFailed, " check failed"))
	}
	if c.ToCheck > 0 {
		parts = append(parts, fmt.Sprint(c.ToCheck, " to check"))
	}
	return strings.Join(append(parts, fmt.Sprint(c.DoneToday, " done today")), " · ")
}

// foryou is what the strip says: what waits, and what else there is to say.
func foryou(p *contract.Page) string {
	v, s := p.View, p.View.Strip
	parts := []string{fmt.Sprint(v.Counts.Waiting, " waiting"), plural(v.Counts.Holding, "holds work up", "hold work up")}
	if s.Paused {
		parts = append(parts, "paused")
	}
	if s.StillRunning > 0 {
		parts = append(parts, fmt.Sprint(s.StillRunning, " still running"))
	}
	if n := len(v.Held); n > 0 {
		parts = append(parts, fmt.Sprint(n, " held"))
	}
	if !s.Away.IsZero() {
		parts = append(parts, "away "+age(p, s.Away)+" · press c")
	}
	if s.DoneAsYou > 0 {
		parts = append(parts, fmt.Sprint(s.DoneAsYou, " done as you"))
	}
	return strings.Join(parts, " · ")
}
