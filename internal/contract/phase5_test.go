package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

// home is a login with a settings folder and a state folder of its own;
// shared names what another login can write.
type home struct {
	desk
	config string
	shared *[]string
}

func (h home) Dirs() (Dirs, error) { return Dirs{Config: h.config, State: h.dir}, nil }
func (h home) WritableByOthers(path string) (bool, error) {
	return slices.Contains(*h.shared, path), nil
}

func login(t *testing.T) (home, *Call) {
	t.Helper()
	h := home{desk{dir: t.TempDir(), mu: new(sync.Mutex)}, t.TempDir(), new([]string)}
	k := NewKit()
	k.Platform = h
	return h, &Call{Kit: k, Caller: Caller{Kind: Human}, Flags: map[string][]string{}}
}

// leaves lists every setting a type can hold, by the names its file uses.
func leaves(t reflect.Type, tag, prefix string) (out []string) {
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get(tag), ",")
		switch {
		case name == "-" || name == "version" || f.Anonymous:
		case f.Type.Kind() == reflect.Struct:
			out = append(out, leaves(f.Type, tag, prefix+name+".")...)
		default:
			out = append(out, prefix+name)
		}
	}
	return out
}

// Every setting a file is read into has its row in the table, so that the
// settings screen, which draws the table, has every setting. The one field
// without a row is the place of the action pane as older files name it,
// which is read and never written.
func TestEverySettingHasARow(t *testing.T) {
	for store, typ := range map[string]reflect.Type{StoreConfig: reflect.TypeFor[PersonConfig](), StoreEngine: reflect.TypeFor[Settings](),
		StoreProject: reflect.TypeFor[ProjectFile](), StoreServer: reflect.TypeFor[Server](), StoreTools: reflect.TypeFor[Tool]()} {
		for _, path := range leaves(typ, "toml", "") {
			has := slices.ContainsFunc(Keys, func(k Key) bool {
				at := k.Path
				if at == "" {
					at = k.Name
				}
				return k.Store == store && at == path
			})
			if !has && !(store == StoreConfig && path == "ui.actions") {
				t.Errorf("%s holds %s, and no row of Keys is that setting", store, path)
			}
		}
	}
}

// A row is whole, stands in a section of the screen, is found by its name,
// and says of a setting what the program itself does when nothing is said.
func TestKeysAreWhole(t *testing.T) {
	person, engine, project := PersonDefaults(), Settings{}, ProjectDefaults()
	engine, _ = ReadSettings(func(string) ([]byte, error) { return nil, os.ErrNotExist }, Dirs{})
	files := map[string]any{StoreConfig: &person, StoreEngine: &engine, StoreProject: &project}
	kinds := []string{KindChoice, KindSwitch, KindNumber, KindText, KindWords, KindList, KindTool}
	seen := map[string]bool{}
	for i := range Keys {
		k := &Keys[i]
		if seen[k.Name] || !slices.Contains(kinds, k.Kind) || k.Store == "" || k.Section != "" && (k.Label == "" || !slices.Contains(Sections, k.Section)) {
			t.Errorf("%s: twice in the table, or a kind, a store, a label or a section is missing", k.Name)
		}
		seen[k.Name] = true
		found, given := FindKey(strings.NewReplacer("<name>", "hermes", "<VAR>", "API_KEY", "<key>", "x").Replace(k.Name))
		if found != k || len(given) != strings.Count(k.Name, "<") {
			t.Errorf("%s is not found by its own name: %v %v", k.Name, found, given)
		}
		if file := files[k.Store]; file != nil && !strings.Contains(k.Name, "<") {
			if got := k.Of(file); got != k.Default {
				t.Errorf("%s comes as %q by the table and as %q by the program", k.Name, k.Default, got)
			}
			if _, err := k.Check(k.Default, BuiltIn()); err != nil && k.Kind != KindText && k.Kind != KindList && k.Default != "" {
				t.Errorf("%s: its own default is refused: %v", k.Name, err)
			}
		}
	}
	if k, _ := FindKey("tools.hermes.nothing"); k != nil {
		t.Error("a key nobody has was found")
	}
}

func TestValuesAreHeldToTheirKey(t *testing.T) {
	for _, tc := range []struct {
		key, value, want string
		ok               bool
	}{
		{"fleets.leaders", "2", "2", true}, {"fleets.leaders", "1", "", false}, {"fleets.leaders", "unlimited", "0", true},
		{"fleets.total", "400", "400", true}, {"side.width", "15", "", false}, {"side.width", "60", "60", true}, {"side.width", "unlimited", "", false},
		{"panes.fleet", "bottom", "bottom", true}, {"panes.fleet", "top", "", false}, {"side.place", "off", "", false},
		{"fleets.mode", "on", "on", true}, {"fleets.mode", "yes", "", false}, {"dnd", "until 9:00", "until 9:00", true}, {"dnd", "until ", "", false},
		{"fleets.leader", "codex", "codex", true}, {"fleets.leader", "hermes", "", false}, {"fleets.leader", "", "", false},
		{"fleets.worker", "leader", "leader", true}, {"notify.link", "a\nb", "", false},
	} {
		k, _ := FindKey(tc.key)
		if got, err := k.Check(tc.value, BuiltIn()); (err == nil) != tc.ok || got != tc.want {
			t.Errorf("%s %q: %q, %v", tc.key, tc.value, got, err)
		}
	}
}

// set writes each store's file, keeps what else is in it, refuses an agent a
// key that runs a program, the administrator's key and a key on the wrong
// side of --project, and notes a key that runs a program for every screen.
func TestSetWritesEveryStore(t *testing.T) {
	h, c := login(t)
	must := func(key, value, want string) {
		t.Helper()
		if got, err := Set(c, key, value); err != nil || got != want {
			t.Fatalf("set %s %s: %q, %v", key, value, got, err)
		}
	}
	must("fleets.mode", "on", "on")
	must("fleets.agents", "unlimited", "unlimited")
	must("panes.actions", "off", "off")
	must("nudge.sound", "toggle", "off")
	must("side.width", "34", "34")
	must("shell", "/bin/zsh -l", "/bin/zsh -l")
	must("shortcut.x", "/usr/bin/true now", "/usr/bin/true now")
	must("fleet.height", "12", "12")
	person, err := ReadPerson(os.ReadFile, h.config)
	if f := person.Fleets; err != nil || f.Mode != On || f.Agents != Unlimited || f.Leaders != 4 || person.Panes.Actions != Off || person.Nudge.Sound || person.UI.Theme != "reef" {
		t.Errorf("config.toml: %+v, %v", person, err)
	}
	engine, err := ReadSettings(os.ReadFile, Dirs{Config: h.config})
	if err != nil || engine.Side.Width != 34 || engine.Side.Place != Left || !slices.Equal(engine.Shell, []string{"/bin/zsh", "-l"}) ||
		len(engine.Shortcuts) != 1 || engine.Shortcuts[0].Key != "x" || engine.Keys.Command != "ctrl+space" {
		t.Errorf("settings.toml: %+v, %v", engine, err)
	}
	must("shortcut.x", "", "")
	if engine, _ = ReadSettings(os.ReadFile, Dirs{Config: h.config}); len(engine.Shortcuts) != 0 {
		t.Errorf("a shortcut set to nothing stays: %+v", engine.Shortcuts)
	}
	var ui UIFile
	if err := ReadVersioned(os.ReadFile, filepath.Join(h.dir, UIFileName), FileVersion, &ui); err != nil || ui.FleetHeight != 12 ||
		!slices.Equal(ui.Changed, []string{"shell", "shortcut.x"}) {
		t.Errorf("ui.json: %+v, %v", ui, err)
	}

	refused := func(key, value, code string, exit int) {
		t.Helper()
		var r *Refusal
		if _, err := Set(c, key, value); !errors.As(err, &r) || r.Code != code || r.Exit != exit {
			t.Errorf("set %s %s: %v, want %s", key, value, err, code)
		}
	}
	refused("server.agents", "99", "admin_only", ExitRefused)
	refused("no.such", "1", "usage", ExitUsage)
	refused("fleets.leaders", "1", "usage", ExitUsage)
	refused("limits.agents", "30", "usage", ExitUsage) // the project's, without --project
	c.Flags["project"] = []string{""}
	refused("theme", "kelp", "usage", ExitUsage)
	if _, err := Set(c, "limits.agents", "30"); !errors.Is(err, ErrNotBuilt) {
		t.Errorf("a project's key until its writer is built: %v", err)
	}
	c.Caller.Kind = Unbound
	refused("limits.agents", "30", "human_only", ExitRefused)
	delete(c.Flags, "project")
	refused("shell", "/bin/sh", "human_only", ExitRefused)
	refused("tools.hermes.run", "/opt/hermes/bin/hermes", "human_only", ExitRefused)
	must("fleets.total", "30", "30") // a plain key is anybody's of the login
}

// A tool is wired in by a description from two places and no third; a
// description that must not be used is named and left out; and a built-in is
// what the three tables of launch, watch and hook say of it today.
func TestToolsFromDescriptions(t *testing.T) {
	h, c := login(t)
	// A full path is the system's own: on Windows it begins at a drive, and
	// a person types it with that system's strokes.
	full := func(path string) string {
		if runtime.GOOS == "windows" {
			path = "C:" + path
		}
		return filepath.FromSlash(path)
	}
	program := full("/opt/hermes/bin/hermes")
	if err := WriteProjects(h, h.dir, []string{full("/work/shop-site")}); err != nil {
		t.Fatal(err)
	}
	tools, faults := Tools(h, Dirs{Config: h.config, State: h.dir})
	claude, _ := FindTool(tools, "")
	codex, _ := FindTool(tools, "codex")
	if len(tools) != 2 || len(faults) != 0 || claude.Name != DefaultTool || !claude.Proven || !claude.Exact() || !claude.Held() || codex.Proven || !codex.Exact() || codex.Held() {
		t.Fatalf("built in: %+v %v", tools, faults)
	}
	if got := slices.Concat(claude.Folders("/a", "/b"), Fill(claude.Resume, "session", "s1"), Fill(claude.Model, "model", "opus")); !slices.Equal(got,
		[]string{"--add-dir", "/a", "/b", "--resume", "s1", "--model", "opus"}) {
		t.Errorf("claude's arguments: %v", got)
	}
	if got := slices.Concat(codex.Folders("/a", "/b"), Fill(codex.Resume, "session", "s1")); !slices.Equal(got, []string{"--add-dir", "/a", "--add-dir", "/b", "resume", "s1"}) {
		t.Errorf("codex's arguments: %v", got)
	}

	if _, err := Set(c, "tools.hermes.label", "Hermes"); err == nil {
		t.Fatal("a tool with no program was stored")
	}
	if _, err := Set(c, "tools.hermes.run", program+" chat"); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"label": "Hermes", "model": "--model {model}", "env.API_KEY": "s3cret", "screen.question": "Allow,Deny"} {
		got, err := Set(c, "tools.hermes."+key, value)
		if err != nil || (got == value) == strings.HasPrefix(key, "env.") {
			t.Fatalf("set tools.hermes.%s: %q, %v", key, got, err)
		}
	}
	tools, faults = Tools(h, Dirs{Config: h.config, State: h.dir})
	hermes, wired := FindTool(tools, "hermes")
	if !wired || len(faults) != 0 || !hermes.Mine || hermes.Proven || hermes.Exact() || hermes.Held() || hermes.Label != "Hermes" || hermes.Env["API_KEY"] != "s3cret" ||
		!slices.Equal(hermes.Run, []string{program, "chat"}) || !slices.Equal(hermes.Screen.Question, []string{"Allow", "Deny"}) {
		t.Fatalf("hermes: %+v %v", hermes, faults)
	}
	file := filepath.Join(h.config, ToolsDir, "hermes.toml")
	// Windows has no such bits to look at: a file there is as private as the
	// profile folder it lies in, which is where the settings folder is.
	if info, err := os.Stat(file); err != nil || runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Errorf("the description holds a key and is not the login's alone: %v", err)
	}

	// What is refused when it is stored, and not used when it lies there anyway.
	for key, value := range map[string]string{"tools.hermes.run": "hermes chat", "tools.grok.run": full("/work/shop-site/bin/agent"), "tools.Bad_Name.run": full("/bin/x"),
		"tools.hermes.hooks": "mine", "tools.-x.run": full("/bin/x")} {
		if _, err := Set(c, key, value); err == nil {
			t.Errorf("set %s %s was taken", key, value)
		}
	}
	plant := func(name, text string) {
		if err := os.WriteFile(filepath.Join(h.config, ToolsDir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	plant("inside.toml", "run = ['"+full("/work/shop-site/tools/agent")+"']\nhooks = 'none'\n")
	plant("relative.toml", "run = ['agent']\nhooks = 'none'\n")
	plant("claude.toml", "suits = 'everything but tests'\n")
	plant("codex.toml", "run = ['"+full("/opt/codex/codex")+"']\n")
	tools, faults = Tools(h, Dirs{Config: h.config, State: h.dir})
	claude, _ = FindTool(tools, "claude")
	codex, _ = FindTool(tools, "codex")
	if len(tools) != 3 || len(faults) != 2 || claude.Suits != "everything but tests" || !claude.Proven || claude.Run[0] != "claude" || codex.Run[0] != full("/opt/codex/codex") || codex.Hooks != HooksCodex {
		t.Errorf("a built-in changed field by field, two files left out: %d tools, %v, %+v", len(tools), faults, claude)
	}
	*h.shared = []string{file}
	if tools, faults = Tools(h, Dirs{Config: h.config, State: h.dir}); len(tools) != 2 || len(faults) != 3 {
		t.Errorf("a file another login can write was used: %d tools, %v", len(tools), faults)
	}
	*h.shared = []string{filepath.Join(h.config, ToolsDir)}
	if tools, faults = Tools(h, Dirs{Config: h.config, State: h.dir}); len(tools) != 2 || len(faults) != 1 || tools[0].Suits == "everything but tests" {
		t.Errorf("a folder another login can write was used: %d tools, %v", len(tools), faults)
	}
	*h.shared = nil
	if _, err := Set(c, "tools.hermes", "remove"); err != nil {
		t.Error(err)
	}
	if _, wired := FindTool(firstOf(Tools(h, Dirs{Config: h.config, State: h.dir})), "hermes"); wired {
		t.Error("a tool that was removed is still wired in")
	}
}

func firstOf(tools []Tool, _ []string) []Tool { return tools }

// An older config.toml names the action pane's place under [ui]: it is read
// as the new setting, and the next write keeps it under its new name alone.
func TestTheActionPanesOlderName(t *testing.T) {
	h, c := login(t)
	if err := os.WriteFile(filepath.Join(h.config, ConfigFile), []byte("[ui]\nactions = \"top\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if p, err := ReadPerson(os.ReadFile, h.config); err != nil || p.Panes.Actions != Top || p.Panes.Fleet != Right || p.Fleets.Total != 20 {
		t.Fatalf("read: %+v, %v", p, err)
	}
	if _, err := Set(c, "theme", "kelp"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(h.config, ConfigFile))
	if p, _ := ReadPerson(os.ReadFile, h.config); p.Panes.Actions != Top || bytes.Count(data, []byte("actions")) != 1 {
		t.Errorf("after a write: %+v\n%s", p.Panes, data)
	}
}

// Fit at every width: no window of 72 columns or more leaves the
// conversation under fifty, every region grows with the window and never
// shrinks, a form is a function of the width alone, and the widths at which
// the forms change are the ones the pictures are drawn at.
func TestFitAtEveryWidth(t *testing.T) {
	order := []string{FormOff, FormStrip, FormRail, FormCut, FormNames, FormCards2, FormCards3, FormFull}
	rank := func(form string) int { return slices.Index(order, form) }
	for _, l := range []Layout{
		{Side: SideWidth, Fleet: Right, Actions: Bottom}, {Side: SideMax, Fleet: Left, FleetWide: 120, Actions: Top}, {Side: SideMin, Fleet: Right, Actions: Off},
		{Side: SideWidth, Fleet: Bottom, Actions: Bottom, Holding: true}, {Side: 0, Fleet: Off, Actions: Bottom}, {Side: 99, Fleet: Right, FleetWide: 1, Actions: Top},
	} {
		was := Fit(20, l)
		for cols := 20; cols <= 240; cols++ {
			f := Fit(cols, l)
			if cols >= SideRailFrom && f.Talk < TalkMin {
				t.Fatalf("%d columns, %+v: the conversation has %d", cols, l, f.Talk)
			}
			used := f.Side + 1 + f.Talk
			if f.Fleet > 0 {
				used += f.Fleet + 1
			}
			if used != cols || f.Side < was.Side || f.Fleet < was.Fleet || rank(f.SideForm) < rank(was.SideForm) || rank(f.FleetForm) < rank(was.FleetForm) || rank(f.Actions) < rank(was.Actions) {
				t.Fatalf("%d columns, %+v: %+v after %+v", cols, l, f, was)
			}
			if (l.Fleet == Off) != (f.FleetForm == FormOff) && cols >= FleetOffUnder || (l.Actions == Off) != (f.Actions == FormOff) {
				t.Fatalf("%d columns, %+v: a pane switched off shows, or one switched on is gone: %+v", cols, l, f)
			}
			was = f
		}
	}
	as := Layout{Side: SideWidth, Fleet: Right, Actions: Bottom}
	for cols, want := range map[int]Form{
		200: {Side: 28, Fleet: 46, Talk: 124, SideForm: FormFull, FleetForm: FormCards3, Actions: FormFull},
		120: {Side: 22, Fleet: 36, Talk: 60, SideForm: FormNames, FleetForm: FormCards2, Actions: FormStrip},
		109: {Side: 22, Talk: 86, SideForm: FormNames, FleetForm: FormStrip, Actions: FormStrip},
		80:  {Side: 14, Talk: 65, SideForm: FormCut, FleetForm: FormStrip, Actions: FormStrip},
		60:  {Side: 4, Talk: 55, SideForm: FormRail, FleetForm: FormStrip, Actions: FormStrip},
		30:  {Side: 4, Talk: 25, SideForm: FormRail, FleetForm: FormOff, Actions: FormStrip},
	} {
		if got := Fit(cols, as); got != want {
			t.Errorf("%d columns: %+v, want %+v", cols, got, want)
		}
	}
}

// A leader may what an orchestrator may in its fleet, but for the eight
// things that are refused; its three commands upward are a worker's.
func TestTheLeader(t *testing.T) {
	none := func(string) bool { return false }
	for _, tc := range []struct {
		command, sub, flag string
		want               bool
	}{
		{"start", "", "", true}, {"accept", "", "", true}, {"task", "add", "", true}, {"task", "add", "fleet", false}, {"wait", "", "", true},
		{"need", "", "", true}, {"escalate", "", "", true}, {"answer", "", "", true}, {"answer", "", "relayed", false}, {"status", "", "", true},
		{"report", "", "", true}, {"progress", "", "", true}, {"mail", "", "", true}, {"ask", "", "", false},
		{"land", "", "", false}, {"run", "new", "", false}, {"run", "close", "", false}, {"run", "rm", "", false}, {"run", "takeover", "", false},
		{"run", "list", "", true}, {"pause", "", "", false}, {"trust", "", "", false}, {"tell", "lead", "", false}, {"set", "", "", false},
	} {
		has := none
		if tc.flag != "" {
			has = func(f string) bool { return f == tc.flag }
		}
		if got := Find(tc.command).LeaderMay(tc.sub, has); got != tc.want {
			t.Errorf("a leader and %s %s --%s: %v, want %v", tc.command, tc.sub, tc.flag, got, tc.want)
		}
	}
	for _, never := range LeaderNever {
		name, _, _ := strings.Cut(never, " ")
		if Find(name) == nil {
			t.Errorf("%s: no such command", never)
		}
	}
}

// The stand-ins of phase 5 say that they are stand-ins and break nothing.
func TestThePhaseFiveStandIns(t *testing.T) {
	k := NewKit()
	if s := k.Side(SideInput{}); s.Version != SideVersion || !strings.Contains(s.Hint, ErrNotBuilt.Error()) {
		t.Errorf("the side model: %+v", s)
	}
	if !errors.Is(k.Terms.Dock(DockKeys), ErrNotBuilt) || !errors.Is(k.Store.Both("", "r1", "r2", nil), ErrNotBuilt) || k.Panes(k, "p1") != nil ||
		!errors.Is(k.ProjectSet("", nil, ""), ErrNotBuilt) {
		t.Error("a stand-in of phase 5 answers as if it were built")
	}
	if ErrOf(WireReply{Err: ErrCode(ErrDock)}) != ErrDock {
		t.Error("the dock's refusal loses its name on the way through the socket")
	}
	for _, id := range []string{"park", "note-leader", "settings", "side-keys", "side-fold", "workspace"} {
		if a, ok := FindAction(id); !ok || a.Label == "" || slices.ContainsFunc(Actions, func(b Action) bool { return b.ID == id }) {
			t.Errorf("the left section's actions have no row %s, or the list of every action has it already", id)
		}
	}
}

// What the first wave of phase 5 asked of the contract. The warning line
// names a changed tool once and any other setting by its key. With no
// folder of the login's known no description is read, least of all from a
// folder named tools where the command stands. A name is no option behind
// spaces either. And a key says its fields under names of their own.
func TestWhatTheFirstWaveAsked(t *testing.T) {
	for changed, want := range map[string]string{
		"": "", "notify.url": "setting changed: notify.url", "tools.hermes.run": "agent tool changed: hermes",
		"tools.hermes.run,shell,tools.hermes.env.KEY,tools.aider.run": "agent tool changed: hermes, aider · setting changed: shell",
	} {
		if got := Warned(strings.FieldsFunc(changed, func(r rune) bool { return r == ',' })); got != want {
			t.Errorf("changed %q warns %q, want %q", changed, got, want)
		}
	}

	h, _ := login(t)
	t.Chdir(t.TempDir())
	if err := os.Mkdir(ToolsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ToolsDir, "mine.toml"), []byte(`run = ["/bin/sh"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if tools, faults := Tools(h, Dirs{}); len(tools) != len(BuiltIn()) || faults != nil {
		t.Errorf("with no folder of the login's: %+v, %v", tools, faults)
	}
	if (Tool{Run: []string{"no-such-program-of-the-test"}}).Found() {
		t.Error("a program that is nowhere is found")
	}

	rule := ValueRules[slices.IndexFunc(ValueRules, func(r ValueRule) bool { return r.Value == "name" })]
	for name, passes := range map[string]bool{"billing": true, " help pages ": true, "-rf": false, " -rf": false, "\t--fleet=x": false, "  ": false} {
		if regexp.MustCompile(rule.Pattern).MatchString(name) != passes {
			t.Errorf("the name %q: passes %v", name, !passes)
		}
	}

	key, _ := json.Marshal(Keys[0])
	if got := string(key); !strings.HasPrefix(got, `{"name":"theme","section":"Look","label":"Colour scheme","kind":"choice","values":`) || strings.Contains(got, "ui.theme") {
		t.Errorf("a key as JSON: %s", got)
	}
}
