package panes

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/term/termtest"
	"github.com/tgdigital-hub/whaleshark/internal/theme"
	"github.com/tgdigital-hub/whaleshark/internal/view"
)

// guardsOf is the settings screen with Guardrails in front and Protected
// files chosen, as the design draws it.
func guardsOf(t *testing.T, w, h int) *pane {
	p := screenOf(t, w, h)
	p.sect, p.saw, p.pick = 6, 6, 1
	p.draw()
	return p
}

// asksOf is the settings screen the moment unlimited was chosen for the
// agents in all: nothing is set yet.
func asksOf(t *testing.T, w, h int) *pane {
	p := screenOf(t, w, h)
	p.pick = 3
	p.change(p.settingsRows()[3], "unlimited")
	p.draw()
	return p
}

// lookOf is the settings screen with Look in front: the first steps' switch
// and the two rows of a saved profile.
func lookOf(t *testing.T, w, h int) *pane {
	p := screenOf(t, w, h)
	p.sect, p.saw, p.pick = 0, 0, 2
	p.draw()
	return p
}

// guarded is the afternoon with every guard on, with the two items its
// record does not hold: the job held at its limit, and a stuck worker whose
// climb reached the person. The lead agent has changed the colour scheme.
func guarded(t *testing.T, kind string, w, h int) *pane {
	g := testkit.Guarded()
	g.Up.Run.Spend.Held = true
	add := func(q contract.Question) {
		q.Kind, q.For, q.From, q.State, q.CreatedAt = contract.KindNeed, contract.ForHuman, contract.FromTool, contract.QuestionOpen, g.Now.Add(-time.Minute)
		g.Up.Questions[q.ID] = &q
	}
	add(contract.Question{ID: "n4", Form: contract.FormChoice, Cause: contract.CauseBudget, Urgent: true, Text: "This job has used its 40 agent-hours",
		Set: &contract.SetAsk{Key: "spend.job", From: "40", To: "48"}})
	add(contract.Question{ID: "n5", Form: contract.FormTodo, Cause: contract.CauseStuck, Task: "G1", Attempt: "T7.1", Fleet: "billing",
		Text: "billing › sign-up page looks stuck for 82m: no progress note · the same check failed 3 times. Its leader and the orchestrator were told."})
	k := contract.NewKit()
	k.View, k.Platform = view.Build, system(t.TempDir(), nil)
	p := newPane(kind, termtest.New(w, h).Term, k, "", "", func() time.Time { return g.Now.Add(2 * time.Second) })
	p.plainMarks, p.me, p.state, p.fleets, p.cfg, p.limits, p.watch = false, "", g.Up, g.Low, g.Person, contract.ProjectDefaults(), noticed{}
	p.picture = &contract.Snapshot{}
	p.ui.ByAgent = []contract.SetAsk{{Key: "theme", From: "reef", To: "kelp"}}
	p.ui.FirstSteps = contract.Off
	p.filesOK, p.termsOK, p.swept, p.sweptAt = g.Now, g.Now, contract.Swept{Ran: true, At: g.Now}, g.Now
	p.draw()
	return p
}

func itemsOf(t *testing.T, w, h int) *pane { return guarded(t, actions, w, h) }

// stuckOf is a fleet's own pane that afternoon: billing, whose worker is stuck.
func stuckOf(t *testing.T, w, h int) *pane {
	p := guarded(t, fleet, w, h)
	p.state, p.fleets, p.stale = p.fleets["billing"], nil, true
	p.draw()
	return p
}

// newcomerOf is the left section of somebody's first minute: one project,
// no job yet, an agent started in a tab; a newer version is out and the
// agent has changed a setting.
func newcomerOf(t *testing.T, w, h int) *pane {
	p := bar(t, w, h)
	p.in.Projects = []contract.SideProject{{Root: p.in.Projects[0].Root, Branch: "main"}}
	p.picture.Panes = p.picture.Panes[:1]
	p.in.Terms = p.picture
	p.ui = contract.UIFile{ByAgent: []contract.SetAsk{{Key: "theme", From: "reef", To: "kelp"}}}
	p.in.NewVersion = contract.VersionLine("v1.3", "1.2")
	p.stale = true
	p.draw()
	return p
}

// six are the screens phase 6 adds, each as its picture on file for a
// window of 200, of 120 and of 80 columns: the section Guardrails with
// Protected files in front, the ask before a limit is taken away, Look with
// the first steps' switch and the rows of a saved profile, the five items
// of the guards with their buttons, a stuck worker's card, and the left
// section of somebody's first minute. SHOTS names a folder to save them in
// for a reader; PICTURES=write writes the files anew, to be read before
// they are kept.
var six = []struct {
	name string
	of   func(*testing.T, int, int) *pane
	w, h int
}{
	{"guards", guardsOf, 180, 18}, {"guards", guardsOf, 108, 21}, {"guards", guardsOf, 76, 20},
	{"unlimited", asksOf, 180, 18}, {"unlimited", asksOf, 108, 19}, {"unlimited", asksOf, 76, 17},
	{"look", lookOf, 180, 18}, {"look", lookOf, 108, 15}, {"look", lookOf, 76, 13},
	{"items", itemsOf, 120, 34}, {"items", itemsOf, 60, 40}, {"items", itemsOf, 64, 38},
	{"stuck", stuckOf, 46, 12}, {"stuck", stuckOf, 36, 12}, {"stuck", stuckOf, 97, 9},
	{"steps", newcomerOf, 32, 23}, {"steps", newcomerOf, 22, 26}, {"steps", newcomerOf, 14, 20},
}

func TestTheScreensOfPhaseSixAreThePictures(t *testing.T) {
	for _, pic := range six {
		name := fmt.Sprintf("%s-%dx%d", pic.name, pic.w, pic.h)
		p, file := pic.of(t, pic.w, pic.h), filepath.Join("testdata", "six", name+".txt")
		if shot(name, p); os.Getenv("PICTURES") == "write" {
			os.MkdirAll(filepath.Dir(file), 0o755)
			os.WriteFile(file, []byte(grid(p)+"\n"), 0o644)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.TrimRight(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		for _, s := range theme.Schemes {
			p.scheme = s
			// With no colour a pale bar is told by its own block.
			if p.draw(); strings.ReplaceAll(grid(p), contract.BarPale, contract.BarFull) != want {
				t.Errorf("%s in the scheme %s is\n%s\nwant\n%s", name, s.Name, grid(p), want)
			}
			for y := 0; s.Name == theme.None && y < pic.h; y++ {
				for x := range pic.w {
					if st := p.t.At(x, y).Style; st.Fg != (term.Style{}).Fg || st.Bg != (term.Style{}).Bg {
						t.Fatalf("%s with no colour: the cell %d,%d has one: %+v", name, x, y, st)
					}
				}
			}
		}
		// A terminal that cannot show the marks gets none of them. (The view's
		// own words keep theirs: "billing › sign-up page", "a file…".)
		p.plainMarks = true
		if p.draw(); strings.ContainsAny(grid(p), "▌▲◐●◌◆▾▸≡‹┌─│└┘▮⚠✔") {
			t.Errorf("%s with plain marks still draws one:\n%s", name, grid(p))
		}
		shot(name+"-plain", p)
	}
}

// Every row of Guardrails says what it does in the words the person signed
// off, whole at every width: under the rows in four lines at most, or in
// the wide screen's own column. A limit that is away begins with the first
// two sentences of its warning.
func TestEveryGuardIsDescribedWordForWord(t *testing.T) {
	const export = "Who did what, what you approved and what it used, as one plain file you can keep or send."
	for _, size := range [][2]int{{180, 24}, {108, 24}, {76, 24}} {
		p := guardsOf(t, size[0], size[1])
		rows := p.settingsRows()
		for i, s := range rows[:9] {
			p.pick = i
			p.draw()
			want := s.key.Help
			switch s.label {
			case "The log of this job":
				want = export
			case "Agent-hours one fleet may use":
				want = "No limit on agent-hours. The guard will not hold this job, whatever it uses. " + want
			}
			said, _ := p.says(s, size[0])
			if got := strings.Join(said, " "); got != want || want == "" {
				t.Errorf("%s says %q, want %q", s.label, got, want)
			}
			// As drawn: every word of it, in the order written.
			var drawn []string
			for y := range p.t.H {
				row := []rune(p.t.Row(y))
				if size[0] >= 150 {
					row = row[size[0]-55:] // the wide screen's column for it
				}
				drawn = append(drawn, strings.Fields(strings.NewReplacer("│", " ", "─", " ").Replace(string(row)))...)
			}
			if !strings.Contains(strings.Join(drawn, " "), want) {
				t.Errorf("%d columns: %s is not described whole:\n%s", size[0], s.label, grid(p))
			}
			if lines, _ := p.says(s, size[0]-4); size[0] < 150 && len(lines) > 4 {
				t.Errorf("%d columns: %s takes %d lines", size[0], s.label, len(lines))
			}
		}
		if _, _, ok := p.t.Find("unlimited ⚠"); !ok {
			t.Errorf("a limit that is away has no mark:\n%s", grid(p))
		}
	}
}

// Choosing unlimited asks once before anything is set, in the words of the
// contract; Keep leaves the limit; Use sets it, and from then on the row
// carries its mark and its description begins with the warning.
func TestChoosingUnlimitedAsksFirst(t *testing.T) {
	wd := startWith(t, settings, 120, 44, map[string]string{"PATH": ""}, func(*world) {})
	for range slices.Index(contract.Sections, contract.SecFleets) {
		wd.s.Key("tab")
	}
	wd.shows(" FLEET MODE ")
	wd.key("j", "j", "j")
	choose := func() {
		t.Helper()
		y := wd.row("› Agents in all")
		wd.settle()
		wd.s.Click(len([]rune(wd.s.Row(y)[:strings.LastIndex(wd.s.Row(y), "[ unlimited ]")]))+2, y)
		wd.shows("No limit on agents in all.", "WhaleShark will start as many agents as it is asked for.", "The spending guard is off.", "[ Use unlimited ]  [ Keep 20 ]")
	}
	choose()
	wd.s.ClickText("[ Keep 20 ]")
	wd.shows("saved as you go")
	if n := wd.children("set fleets.total"); len(n) != 0 {
		t.Fatalf("before the yes, and after Keep, the pane ran %q", n)
	}
	choose()
	wd.s.ClickText("[ Use unlimited ]") // the first click takes
	wd.ran(`set fleets.total --file "unlimited"`)
	wd.shows("unlimited ⚠", "No limit on agents in all. WhaleShark will start as many agents as it is asked for.")
}

// The items of the guards in the action pane: each button runs its row of
// the action table or gives its answer, and one that slid under the pointer
// because the list moved by itself is not pressed.
func TestTheGuardsItemsAndTheirButtons(t *testing.T) {
	items := itemsOf(t, 120, 34).view.Items
	wd := startWith(t, actions, 120, 44, nil, func(wd *world) { wd.view.Items = items })
	wd.shows("Protected files changed in this job", "db/migrations/0042_refunds.sql", "Spending guard:  on → off")
	x, y, _ := wd.s.Find("[ Open the plan ]")
	wd.settle()
	wd.change(func(v *contract.View) {
		v.Items = append([]contract.Item{{ID: "n9", Form: "choice", Look: contract.LookNeedsYou, Name: "help pages", Source: "a question from the worker",
			Since: wd.fx.Now, Text: "Drop the old pages?", Buttons: []contract.Button{{Label: "Yes", Key: "y", Answer: "yes"}}}}, v.Items...)
	})
	wd.shows("Drop the old pages?")
	wd.s.Click(x, y)
	wd.shows(movedSaid)
	if wd.settle(); len(wd.children("graph"))+len(wd.children("answer")) != 0 {
		t.Fatalf("the refused click acted: %v", wd.children(""))
	}
	for _, press := range [][3]string{{"[ Show the changes ]", "", "show G1 --diff --human"}, {"[ Open the plan ]", "", "graph --human"},
		{"[ Note to its leader ]", "look at the failing check", `tell G1 --file "look at the failing check" --human`},
		{"[ Send back ]", "smaller groups", `answer n1 --file "send back: smaller groups" --human`},
		{"[ Change it ]", "", `answer n3 --file "change" --human`}, {"[ No limit for this job ]", "", `answer n4 --file "no limit" --human`}} {
		if wd.click(press[0]); press[1] != "" {
			wd.shows(":")
			wd.s.Type(press[1])
			wd.s.Key("enter")
		}
		// What the two that show something print is laid over the pane as
		// a list to read, not cut to the last line of it.
		if wd.ran(press[2]); strings.HasPrefix(press[2], "show ") || press[2] == "graph --human" {
			wd.shows(strings.Trim(press[0], "[] "), "ran ", "j k scroll · esc closes")
			wd.s.Key("esc")
			wd.shows("Protected files changed in this job")
		}
	}
}

// What the strip, a card and the catch-up say of the guards: what is spent,
// the stuck line in the warning colour, and the row of what looked stuck,
// which is there only while something did.
func TestSpentTheStuckLineAndTheCatchUp(t *testing.T) {
	ac := itemsOf(t, 120, 34)
	if row := ac.t.Row(0); !strings.Contains(row, "· spent: 33 of 40 agent-hours · held") {
		t.Errorf("the strip is %q", row)
	}
	fl := stuckOf(t, 46, 12)
	x, y := fl.find(t, "stuck 132m: no progress note")
	if st := fl.t.At(x, y).Style; st.Fg != term.RGB(fl.scheme.Colours["blocked"]) {
		t.Errorf("the stuck line is drawn %+v", st)
	}
	ac.catch, ac.over = contract.Catchup{}, "catchup"
	if ac.draw(); strings.Contains(grid(ac), "Looked stuck") {
		t.Errorf("nothing looked stuck, and the list says\n%s", grid(ac))
	}
	ac.catch.Stuck = contract.CatchLine{N: 1, Text: "sign-up page, still"}
	ac.draw()
	ac.find(t, "Looked stuck           1   sign-up page, still")
}

// A caller that is not the person has none of set's own ways: the table
// decides. A free key is set and flagged until its section is seen; a key
// that is how the person is called is not changed; a pane's marks, which
// would end a warning, are no settings to it at all.
func TestAnAgentsSetIsTheTablesToDecide(t *testing.T) {
	d := newDesk(t)
	dirs, _ := d.k.Platform.Dirs()
	run := func(kind contract.CallerKind, args ...string) (string, error) {
		var out strings.Builder
		_, err := set(&contract.Call{Kit: d.k, Command: contract.Find("set"), Args: args, Flags: map[string][]string{}, Caller: contract.Caller{Kind: kind, Pane: d.me}, Now: time.Now(), Out: &out})
		return out.String(), err
	}
	code := func(err error) string {
		var r *contract.Refusal
		errors.As(err, &r)
		return cmp.Or(r, &contract.Refusal{}).Code
	}
	ui := func() contract.UIFile {
		_, ui, _, _ := screen(d.k)
		return ui
	}
	for _, kind := range []contract.CallerKind{contract.Orchestrator, contract.Unbound} {
		if _, err := run(kind, "theme", "kelp"); err != nil || person(d.k, dirs).UI.Theme != "kelp" || !slices.Equal(ui().ByAgent, []contract.SetAsk{{Key: "theme", From: "reef", To: "kelp"}}) {
			t.Fatalf("%s set theme kelp: %v, and ui.json holds %+v", kind, err, ui().ByAgent)
		}
		if _, err := run(kind, "dnd", "on"); code(err) != contract.RefusedPersonOnly || ui().DND {
			t.Errorf("%s set dnd on: %v, dnd %v", kind, err, ui().DND)
		}
		if _, err := run(kind, "dnd", "until", "9:00"); code(err) != "human_only" || !strings.Contains(err.Error(), "the person's to set") || ui().DND {
			t.Errorf("%s set dnd until 9:00: %v", kind, err)
		}
		for _, args := range [][]string{{"seen", contract.SecLook}, {"here", "now"}, {"shut", "r1/billing"}, {"steps", "31"}, {"draft.q1", "x"}} {
			if _, err := run(kind, args...); code(err) != "usage" {
				t.Errorf("%s set %v: %v", kind, args, err)
			}
		}
		if got := ui(); len(got.ByAgent) != 1 || got.Steps != 0 || len(got.Drafts)+len(got.Shut) != 0 || !got.LastHere.IsZero() {
			t.Errorf("%s changed a pane's own marks: %+v", kind, got)
		}
		run(contract.Human, "theme", "reef")
	}
	// The person's: a section seen ends its warning, and the first steps are noted.
	if out, err := run(contract.Human, "seen", contract.SecLook); err != nil || out != "" || len(ui().ByAgent) != 0 {
		t.Errorf("set seen: %q, %v, %+v", out, err, ui().ByAgent)
	}
	run(contract.Human, "steps", "5")
	if out, err := run(contract.Human, "steps", "16"); err != nil || out != "" || ui().Steps != 21 {
		t.Errorf("set steps: %q, %v, %d", out, err, ui().Steps)
	}
	// Hidden, then switched on again, the first steps begin again.
	run(contract.Human, "ui.first_steps", "off")
	if out, err := run(contract.Human, "ui.first_steps", "on"); err != nil || !strings.Contains(out, "ui.first_steps: on") || ui().Steps != 0 || ui().FirstSteps != contract.On {
		t.Errorf("set ui.first_steps on: %q, %v, %+v", out, err, ui())
	}
}

// What an agent changed is said once more where its section is seen, the
// warning ends there, and u takes the change back.
func TestAnAgentsChangeIsSeenAndTakenBack(t *testing.T) {
	wd := startWith(t, settings, 120, 44, map[string]string{"PATH": ""}, func(wd *world) {
		dirs, _ := wd.sys.Dirs()
		ui := contract.UIFile{Versioned: contract.Versioned{Version: contract.FileVersion}, ByAgent: []contract.SetAsk{{Key: "side.place", From: "left", To: "right"}}}
		os.MkdirAll(dirs.State, 0o700)
		if err := contract.WriteVersioned(wd.sys, filepath.Join(dirs.State, contract.UIFileName), contract.FileVersion, ui); err != nil {
			t.Fatal(err)
		}
	})
	wd.s.Key("tab")
	wd.shows("your agent changed The left section: left → right · u takes it back")
	wd.ran("set seen " + contract.SecLayout)
	wd.key("u")
	wd.ran(`set side.place --file "left"`)
}

// A profile saved on one login and loaded on another: no secret is in the
// file and what was left out is named; every difference is shown before
// anything is set, a limit taken away and a guard switched off with their
// warning; one typed yes sets the plain ones; each line that runs a program
// is asked about by itself; and nothing replaces a file that is there.
func TestAProfileSavedAndLoaded(t *testing.T) {
	t.Parallel()
	login := func() *contract.Kit {
		k := contract.NewKit()
		k.Platform = system(t.TempDir(), nil)
		return k
	}
	run := func(k *contract.Kit, typed string, flag, file string, args ...string) (string, error) {
		var out strings.Builder
		flags := map[string][]string{}
		if flag != "" {
			flags[flag] = []string{file}
		}
		_, err := set(&contract.Call{Kit: k, Command: contract.Find("set"), Args: args, Flags: flags, Caller: contract.Caller{Kind: contract.Human}, Now: time.Now(), Stdin: strings.NewReader(typed), Out: &out})
		return out.String(), err
	}
	old, fresh := login(), login()
	hermit := filepath.Join(t.TempDir(), "bin", "hermit") // a full path, on whatever system this is
	for _, args := range [][]string{{"theme", "paper"}, {"fleets.total", "unlimited"}, {"stuck.watch", "off"}, {"shell", "zsh", "-l"}, {"notify.url", "http://localhost/nudge"},
		{"tools.hermit.run", hermit, "chat"}, {"tools.hermit.suits", "long reading"}, {"tools.hermit.env.KEY", "a-secret-of-mine"}} {
		if _, err := run(old, "", "", "", args...); err != nil {
			t.Fatalf("set %v: %v", args, err)
		}
	}
	file := filepath.Join(t.TempDir(), "mine.toml")
	out, err := run(old, "", "save", file)
	data, _ := os.ReadFile(file)
	if err != nil || !strings.Contains(out, "saved: "+file) || !strings.Contains(out, "left out: the nudge address, 1 variables of hermit") ||
		strings.Contains(string(data), "a-secret-of-mine") || strings.Contains(string(data), "localhost") || !strings.Contains(string(data), "KEY") {
		t.Fatalf("set --save: %v\n%s\n%s", err, out, data)
	}
	if dir := os.Getenv("SHOTS"); dir != "" {
		os.WriteFile(filepath.Join(dir, "profile-save.txt"), []byte(out), 0o600)
	}
	if _, err := run(old, "", "save", file); err == nil || !strings.Contains(err.Error(), "nothing was replaced") {
		t.Errorf("a second save to the same file: %v", err)
	}
	// Loaded where nothing was set: shown whole, and with no yes nothing changes.
	out, err = run(fresh, "no\nno\nno\n", "load", file)
	for _, want := range []string{"Colour scheme (theme): reef → paper", "Agents in all (fleets.total): 20 → unlimited", "No limit on agents in all.", "The spending guard is off.",
		"Stuck detector (stuck.watch): on → off", "This switches a guard off.", "What it suits (tools.hermit.suits): not set → long reading", "Make these 4 changes? Type yes: ",
		"shell runs: zsh -l", "tools.hermit.run runs: " + hermit + " chat · that program is not on this computer · use exactly this? Type yes: ", "0 settings changed."} {
		if !strings.Contains(out, want) {
			t.Errorf("set --load does not say %q:\n%s", want, out)
		}
	}
	dirs, _ := fresh.Platform.Dirs()
	if cfg := person(fresh, dirs); err != nil || cfg != contract.PersonDefaults() {
		t.Errorf("with no yes typed: %v, and the settings are %+v", err, cfg)
	}
	if dir := os.Getenv("SHOTS"); dir != "" {
		os.WriteFile(filepath.Join(dir, "profile-load.txt"), []byte(out), 0o600)
	}
	// Yes to the plain ones and to the shell, no to the tool's program: the
	// tool is not wired in, and what else the file says of it is not set.
	out, err = run(fresh, "yes\nyes\nno\n", "load", file)
	cfg, engine := person(fresh, dirs), contract.Settings{}
	engine, _ = contract.ReadSettings(fresh.Platform.Read, dirs)
	tools, _ := contract.Tools(fresh.Platform, dirs)
	_, wired := contract.FindTool(tools, "hermit")
	_, ui, _, _ := screen(fresh)
	if err != nil || cfg.UI.Theme != "paper" || cfg.Fleets.Total != contract.Unlimited || cfg.Stuck.Watch || cfg.Notify.URL != "" || !slices.Equal(engine.Shell, []string{"zsh", "-l"}) ||
		wired || !slices.Contains(ui.Changed, "shell") || !strings.Contains(out, "not set: tools.hermit.suits") || !strings.Contains(out, "4 settings changed.") {
		t.Errorf("set --load with yes, yes, no: %v\n%s\n%+v %+v wired %v, flagged %v", err, out, cfg, engine.Shell, wired, ui.Changed)
	}
	// Loaded again, only the tool is left, and yes to its program wires it in.
	out, err = run(fresh, "yes\nyes\n", "load", file)
	tools, _ = contract.Tools(fresh.Platform, dirs)
	if tool, wired := contract.FindTool(tools, "hermit"); err != nil || !wired || tool.Suits != "long reading" || len(tool.Env) != 0 || strings.Contains(out, "theme") {
		t.Errorf("the second load: %v\n%s\n%+v", err, out, tool)
	}
	if out, _ = run(fresh, "", "load", file); !strings.Contains(out, "Nothing here differs from that file.") {
		t.Errorf("a third load says\n%s", out)
	}
	// A file from somebody else cannot steer the terminal it is shown on.
	os.WriteFile(file+".evil", []byte(strings.Replace(string(data), `"zsh"`, `"sh\u001b[2Jx"`, 1)), 0o600)
	if out, _ = run(login(), "no\nno\nno\n", "load", file+".evil"); strings.Contains(out, "\x1b") || !strings.Contains(out, "shell runs: shx -l") {
		t.Errorf("a value with an escape sequence is shown as %q", out)
	}
	// What is no profile changes nothing, and a key nobody knows is named and skipped.
	os.WriteFile(file+".bad", []byte("version = 1\nconfig = 7\n"), 0o600)
	if _, err = run(fresh, "yes\n", "load", file+".bad"); err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Errorf("a file that is no profile: %v", err)
	}
	os.WriteFile(file+".odd", append(data, []byte("\n[tomorrow]\nkey = 1\n")...), 0o600)
	if out, _ = run(fresh, "", "load", file+".odd"); !strings.Contains(out, "not known here, and skipped: tomorrow") {
		t.Errorf("a key nobody knows: %s", out)
	}
}

// The first steps in the left section: one line at the panel's foot while a
// job fills it, opened by a click; the ticks are noted in ui.json by `set`;
// [ hide ] ends them, and so does Esc while the section has the keys.
func TestTheFirstStepsAreTickedOpenedAndHidden(t *testing.T) {
	wd, change := sideWorld(t, 32, 40)
	wd.shows("first steps: ")
	wd.ran("set steps 15 --human") // an agent runs, a job is open, a worker has its row, the job is ten minutes old
	wd.click("first steps: ")
	wd.shows("FIRST STEPS", "[ hide ]", "✔ 1 start your agent: type", "5 ctrl+space s settings · S")
	wd.click("[ hide ]")
	wd.ran("set ui.first_steps off")
	if !wd.until(3*time.Second, func() bool { return !strings.Contains(wd.screen(), "FIRST STEPS") }) {
		t.Fatalf("hidden, the first steps are still drawn:\n%s", wd.screen())
	}
	change(func(s *contract.Side) { s.StepsShown = true })
	wd.shows("FIRST STEPS")
	wd.s.Key("down")
	wd.shows("KEYS GO HERE")
	wd.s.Key("esc")
	wd.ran("set ui.first_steps off")
}

// A search that finds no setting says so, at every width, and Enter on it
// does nothing.
func TestASearchThatFindsNoSetting(t *testing.T) {
	for _, w := range []int{180, 108, 76} {
		p := screenOf(t, w, 18)
		p.finding = true
		p.query.Set("zzzz")
		p.draw()
		p.find(t, "no setting is called that")
		p.settingsKey(term.Event{Kind: term.KeyPress, Key: "enter"})
	}
}

// Opening the settings screen ticks the fifth step. Its new rows: Export
// runs the log's own command and says where the file is, the profile is
// saved to the file that is typed, and loading one is a line for a terminal.
// A row that only says something is no button.
func TestTheSettingsScreensNewRows(t *testing.T) {
	wd := startWith(t, settings, 120, 30, map[string]string{"PATH": ""}, func(*world) {})
	wd.ran("set steps 16")
	// A click on a value chooses its row: the right side then speaks of it.
	wd.shows("› Colour scheme")
	wd.click("[ off ]")
	wd.ran(`set ui.first_steps --file "off" --human`)
	wd.shows("› First steps")
	wd.click("[ Save my settings to a file ]")
	wd.shows("Save to the file:")
	wd.s.Type("mine.toml")
	wd.s.Key("enter")
	wd.ran("set --save mine.toml --human")
	wd.click("[ Load settings from a file… ]")
	wd.shows("in the new tab type: whaleshark set --load FILE")
	for range slices.Index(contract.Sections, contract.SecFleets) {
		wd.s.Key("tab")
	}
	wd.click("Agents running now: ")
	wd.s.Key("tab")
	wd.click("[ Export ]")
	wd.ran("log --export --human")
	wd.shows("ran log")
}
