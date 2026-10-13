package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

// 20.6: a switch of the section Guardrails says what happens with it off and
// with it on, and no key whose change can run a program, hold work, call the
// person or cost money comes without a description.
func TestEveryGuardHasAGoodDescription(t *testing.T) {
	guards := 0
	for i := range Keys {
		k := &Keys[i]
		if k.Section == "" {
			continue
		}
		if k.Section == SecGuards {
			if guards++; k.Kind == KindSwitch && !(strings.Contains(k.Help, "Off: ") && strings.Contains(k.Help, "On: ")) {
				t.Errorf("%s does not say what happens off and on: %q", k.Name, k.Help)
			}
		}
		if weighty := k.Asks == AsksRuns || k.Asks == AsksApproval || k.Agent() == SetAsks; weighty && k.Help == "" {
			t.Errorf("%s can run a program, hold work or call the person, and has no description", k.Name)
		}
		if len(k.Help) > 4*78 {
			t.Errorf("%s: its description does not fit four lines of the screen", k.Name)
		}
	}
	if guards != 8 || !slices.Contains(Sections, SecGuards) || slices.Index(Sections, SecGuards) != slices.Index(Sections, SecFleets)+1 {
		t.Errorf("%d keys under Guardrails, sections %v", guards, Sections)
	}
	cfg := PersonDefaults()
	for key, want := range map[string]string{"plan.first": Off, "protect.files": Off, "spend.guard": Off, "spend.job": "40", "spend.fleet": "unlimited",
		"stuck.watch": On, "stuck.climb_minutes": "10", "update.notice": On, "protect.paths": ProtectDefault} {
		if k, _ := FindKey(key); k == nil || k.Default != want || k.Of(&cfg) != want || k.Store != StoreConfig {
			t.Errorf("%s comes as %q in the table and %q in the file, want %q", key, k.Default, k.Of(&cfg), want)
		}
	}
}

// 20.18: which keys the person's agent sets at once, which wait for the
// person and which are theirs alone.
func TestWhatAnAgentMaySet(t *testing.T) {
	for name, want := range map[string]string{
		"theme": SetFree, "side.width": SetFree, "keys.style": SetFree, "scrollback.lines": SetFree, "ui.first_steps": SetFree,
		"update.notice": SetFree, "tools.<name>.suits": SetFree, "tools.<name>.label": SetFree, "panes.fleet": SetFree,
		"fleets.mode": SetAsks, "fleets.total": SetAsks, "fleets.leader": SetAsks, "plan.first": SetAsks, "protect.files": SetAsks,
		"protect.paths": SetAsks, "spend.guard": SetAsks, "spend.job": SetAsks, "stuck.watch": SetAsks, "stuck.climb_minutes": SetAsks,
		"nudge.sound": SetAsks, "notify.full_text": SetAsks, "dnd": SetAsks, "mute": SetAsks, "ui.settle_seconds": SetAsks,
		"shell": SetPerson, "shortcut.<key>": SetPerson, "notify.url": SetPerson, "tools.<name>.run": SetPerson, "tools.<name>.env.<VAR>": SetPerson,
		"tools.<name>.hooks": SetPerson, "tools.<name>.rules": SetPerson, "tools.<name>.tried": SetPerson, "protect.also": SetPerson,
		"land.who": SetPerson, "limits.stale_minutes": SetPerson, "server.road": SetPerson,
	} {
		if k, _ := FindKey(name); k == nil || k.Agent() != want {
			t.Errorf("%s: %v, want %s", name, k, want)
		}
	}
	if c := Find("set"); !c.May(Orchestrator, "") || !c.May(Unbound, "") || c.May(Worker, "") || c.May(Leader, "") {
		t.Error("set is the person's, the lead agent's and a tab's bound to no job; never a worker's or a leader's")
	}
	if c := Find("log"); !slices.ContainsFunc(c.Flags, func(f Flag) bool { return f.Name == "export" && f.Who == H && f.Value == "" }) {
		t.Error("log --export is the person's alone, and its file is the argument after it")
	}
}

// oneRun is a store of one record, and asks the rules that note what an
// agent's set asked for.
type oneRun struct {
	NoStore
	s *State
}

func (o oneRun) Change(_, _ string, fn func(*State) error) error { return fn(o.s) }

type asks struct {
	Rules
	got  *SetAsk
	text *string
}

func (a asks) SetAsked(_ *State, ask SetAsk, text string, _ time.Time) (string, error) {
	*a.got, *a.text = ask, text
	return "n1", nil
}

func TestAnAgentsSet(t *testing.T) {
	h, c := login(t)
	var got SetAsk
	var text string
	var out, errw bytes.Buffer
	c.Kit.Store, c.Kit.Rules, c.Out, c.Err = oneRun{s: &State{}}, asks{got: &got, text: &text}, &out, &errw
	c.Caller.Kind, c.Run = Orchestrator, "r1"
	cfg := func() PersonConfig {
		cfg, _ := ReadPerson(os.ReadFile, h.config)
		return cfg
	}
	// A key that holds its agents becomes an item with the setting's own
	// words, and nothing is changed.
	if was, err := Set(c, "spend.guard", "on"); err != nil || was != Off || cfg().Spend.Guard || got != (SetAsk{"spend.guard", Off, On}) ||
		!strings.HasPrefix(text, "Spending guard: Off: a job runs until it is done.") || !strings.Contains(out.String(), "waiting for the person's click") {
		t.Fatalf("an agent switches the guard on: %q, %v, asked %+v %q, said %q", was, err, got, text, out.String())
	}
	// The person's click on that item makes the change, as theirs.
	if err := Granted(c.Kit, "", got, Origin{Caller: Human, Where: WherePhone}, time.Time{}); err != nil || !cfg().Spend.Guard {
		t.Fatalf("the change the person granted: %v, guard %v", err, cfg().Spend.Guard)
	}
	// A harmless one is set, and kept as the agent's doing with what it was.
	if now, err := Set(c, "theme", "kelp"); err != nil || now != "kelp" || cfg().UI.Theme != "kelp" {
		t.Fatalf("an agent sets the theme: %q, %v", now, err)
	}
	Set(c, "theme", "ember")
	var ui UIFile
	ReadVersioned(os.ReadFile, filepath.Join(h.dir, UIFileName), FileVersion, &ui)
	if !slices.Equal(ui.ByAgent, []SetAsk{{"theme", "kelp", "ember"}}) || len(ui.Changed) != 0 || AgentChanged(ui.ByAgent) != "setting changed by your agent: theme" {
		t.Errorf("ui.json after an agent's set: %+v", ui)
	}
	// What names a program, a tool taken away and a value that is none.
	for _, kv := range [][2]string{{"shell", "/bin/sh"}, {"notify.url", "https://example.org/n"}, {"tools.hermes.run", "/opt/h/hermes"}, {"tools.hermes.hooks", "claude"}} {
		var r *Refusal
		if _, err := Set(c, kv[0], kv[1]); !errors.As(err, &r) || r.Code != "human_only" {
			t.Errorf("an agent sets %s: %v", kv[0], err)
		}
	}
	var r *Refusal
	if _, err := Set(c, "tools.hermes", "remove"); !errors.As(err, &r) || r.Exit != ExitUsage {
		t.Errorf("an agent takes a tool away: %v", err)
	}
	if _, err := Set(c, "tools.hermes.tried", "started"); !errors.As(err, &r) || r.Exit != ExitUsage {
		t.Errorf("what a test saw is typed: %v", err)
	}
	// The person's own: set at once, with the warning for unlimited.
	c.Caller.Kind = Human
	if now, err := Set(c, "fleets.total", "unlimited"); err != nil || now != "unlimited" || cfg().Fleets.Total != Unlimited ||
		!strings.Contains(errw.String(), "No limit on agents in all.") || strings.Contains(errw.String(), "The spending guard is off.") { // it is on by now
		t.Errorf("the person lifts a limit: %q, %v, warned %q", now, err, errw.String())
	}
	if now, err := Set(c, "protect.paths", " db/migrations/** , .env* "); err != nil || now != "db/migrations/**,.env*" {
		t.Errorf("a protected list: %q, %v", now, err)
	}
	for _, bad := range []string{"../x", "/etc/passwd", "a,,b", "-rf"} {
		if _, err := Set(c, "protect.paths", bad); err == nil {
			t.Errorf("the protected list took %q", bad)
		}
	}
	k, _ := FindKey("spend.job")
	if w := k.UnlimitedWarning(0, 0, true); len(w) != 1 || !strings.HasPrefix(w[0], "No limit on agent-hours.") {
		t.Errorf("the guard's own warning: %q", w)
	}
	k, _ = FindKey("fleets.total")
	if w := k.UnlimitedWarning(60, 12, true); len(w) != 3 || !strings.Contains(w[1], "(about 60 agents here)") || w[2] != "This server allows you 12." {
		t.Errorf("the warning with the machine's figure and a server's: %q", w)
	}
}

func TestTheOneMatcher(t *testing.T) {
	list := List(ProtectDefault)
	for file, want := range map[string]bool{
		".env": true, ".env.example": true, "app/.env.local": true, "db/migrations/0042_refunds.sql": true, "migrations/1.sql": true,
		"a/b/secrets/key.pem": true, ".github/workflows/ci.yml": true, "deploy/prod/values.yaml": true,
		"src/migrations.go": false, "docs/deploying.md": false, ".github/CODEOWNERS": false, "environment.go": false,
	} {
		if Owned(list, file) != want {
			t.Errorf("%s protected: %v, want %v", file, !want, want)
		}
	}
	for _, c := range []struct {
		pattern, file string
		want          bool
	}{
		{"src/url/**", "src/url/a/b.go", true}, {"src/url", "src/url/a.go", true}, {"src/url/", "src/url.go", false}, {"*.md", "docs/a.md", true},
		{"src/*/x.go", "src/a/x.go", true}, {"src/*/x.go", "src/a/b/x.go", false}, {"src/**/x.go", "src/a/b/x.go", true}, {"src/**/x.go", "src/x.go", true},
		{".", "anything", true}, {"**", "a/b", true}, {"a.go", "a.go", true}, {"a.go", "b/a.go", true}, {"b/a.go", "a.go", false},
	} {
		if Owned([]string{c.pattern}, c.file) != c.want {
			t.Errorf("%s covers %s: want %v", c.pattern, c.file, c.want)
		}
	}
	cfg, project := PersonDefaults(), ProjectDefaults()
	project.Protect.Also = []string{"infra/**"}
	if Protected(cfg, project) != nil {
		t.Error("the list holds something with the setting off")
	}
	if cfg.Protect.Files = true; !Owned(Protected(cfg, project), "infra/main.tf") || len(Protected(cfg, project)) != 6 {
		t.Errorf("a project's additions: %v", Protected(cfg, project))
	}
}

func TestSpent(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 12, h, m, 0, 0, time.UTC) }
	job := &State{Attempts: map[string]*Attempt{
		"G1.1": {State: AttemptWorking, StartedAt: at(9, 0), Leads: "r2"},                               // a leader, still open: 3h
		"G2.1": {State: AttemptStopped, StartedAt: at(9, 0), EndedAt: at(10, 0), StateSince: at(10, 0)}, // 1h
		"G3.1": {State: AttemptStartFailed, StateSince: at(9, 5)},                                       // never started
	}}
	fleet := &State{Attempts: map[string]*Attempt{
		"T1.1": {State: AttemptWorking, StartedAt: at(10, 0), Rested: 30 * time.Minute}, // 2h less the rest
		"T2.1": {State: AttemptReported, StartedAt: at(11, 30)},                         // 30m
	}}
	if got := Spent(at(12, 0), job, fleet); got != 6*time.Hour {
		t.Errorf("spent %v, want 6h", got)
	}
	// Held at eleven: the clocks stand from then on.
	fleet.Run.Paused = &Pause{At: at(11, 0), By: HeldBy}
	if got := Spent(at(15, 0), fleet); got != 30*time.Minute {
		t.Errorf("a held run has spent %v, want 30m", got)
	}
	cfg := PersonDefaults()
	if cfg.SpendLimit(Spend{}, false) != Unlimited {
		t.Error("the guard is off and holds a line")
	}
	cfg.Spend.Guard = true
	if job, fleet, more, off := cfg.SpendLimit(Spend{}, false), cfg.SpendLimit(Spend{}, true), cfg.SpendLimit(Spend{More: 8}, false), cfg.SpendLimit(Spend{Off: true}, false); job != 40 || fleet != Unlimited || more != 48 || off != Unlimited {
		t.Errorf("limits: a job %d, a fleet %d, with more %d, lifted %d", job, fleet, more, off)
	}
	if HoldPath("/p", "r7") != filepath.Join("/p", ".whaleshark", "runs", "r7", "hold") {
		t.Errorf("the hold file: %s", HoldPath("/p", "r7"))
	}
}

func TestTheWordsOfTheGuards(t *testing.T) {
	if got := StuckWhy("check,stale"); got != "no progress note · the same check failed 3 times" {
		t.Errorf("the signs in words: %q", got)
	}
	hard := 0
	for _, s := range Signs {
		if s.Hard {
			hard++
		}
	}
	if len(Signs) != 7 || hard != 2 {
		t.Errorf("%d signs, %d of them hard", len(Signs), hard)
	}
	for _, cause := range []string{CausePlan, CauseProtect, CauseBudget, CauseSetting} {
		if !PersonOnly(cause) {
			t.Errorf("an item of cause %s can be answered by an agent", cause)
		}
	}
	if PersonOnly(CauseStuck) || PersonOnly("") || PersonOnly(CausePrompt) {
		t.Error("a to-do of the tool's is the person's alone")
	}
	for _, kind := range []string{"protected", "budget", "stuck"} {
		if !slices.Contains(EventKinds, kind) {
			t.Errorf("no event kind %s", kind)
		}
	}
	s := &State{Run: Run{Plan: &Approval{Approved: []string{"T1", "T2"}}}}
	if lack := Unplanned(s, []string{"T1", "T3", "T2", "T4"}); !slices.Equal(lack, []string{"T3", "T4"}) {
		t.Errorf("not approved: %v", lack)
	}
	if lack := Unplanned(&State{}, []string{"T1"}); len(lack) != 1 {
		t.Errorf("with no plan approved: %v", lack)
	}
	for _, id := range []string{"export", "show-changes", "open-plan", "go", "stop", "note-leader"} {
		if _, ok := FindAction(id); !ok {
			t.Errorf("no action %s", id)
		}
	}
	if !slices.Contains(LeaderNever, "set") || !strings.Contains(ReportLine, "/issues") {
		t.Error("a leader sets nothing; the report line names the Issues page")
	}
}

func TestTheVersionNotice(t *testing.T) {
	for _, c := range []struct {
		seen, mine string
		want       bool
	}{
		{"v1.3.0", "v1.2.9", true}, {"1.3", "v1.2.9", true}, {"v1.2.9", "v1.2.9", false}, {"v1.2", "v1.2.0", false}, {"v1.10", "v1.9", true},
		{"v1.3.0", "dev", false}, {"v1.3.0; rm", "v1.0", false}, {"", "v1.0", false}, {"v0.9", "v1.0", false},
	} {
		if Newer(c.seen, c.mine) != c.want {
			t.Errorf("%q newer than %q: want %v", c.seen, c.mine, c.want)
		}
	}
	if got := VersionLine("v1.3", "v1.2.0"); got != "version 1.3 is out · upgrade it the way you installed it" {
		t.Errorf("the line: %q", got)
	}
	dir := t.TempDir()
	if v := ReadVersion(os.ReadFile, dir); v.Seen != "" || !v.At.IsZero() {
		t.Errorf("no file, and something was seen: %+v", v)
	}
}

func TestTheRanksOfATool(t *testing.T) {
	tools := BuiltIn()
	mine := Tool{Name: "hermes", Run: []string{"/opt/h/hermes"}, Hooks: HooksNone, Mine: true, Tried: ToolTried{Day: "2026-10-12", Saw: TriedSaw[:2]}}
	if tools[0].Rank() != RankProven || tools[1].Rank() != RankReady || mine.Rank() != RankTerminal {
		t.Errorf("ranks: %s, %s, %s", tools[0].Rank(), tools[1].Rank(), mine.Rank())
	}
	if got := tools[0].Facts(); got != "exact states · held by Stop all · context bar · picks up after a restart" {
		t.Errorf("the proven tool: %q", got)
	}
	if got := mine.Facts(); got != "busy or quiet only · not held by Stop all · tried here 2026-10-12: started, took its task · experimental" {
		t.Errorf("a tool of the person's own: %q", got)
	}
	mine.Tried.Saw = []string{"started\x1b[2J"}
	if mine.Check(desk{}, nil) == nil {
		t.Error("what a test saw may steer a terminal")
	}
}

// Version 1 of the interface is held by a file: a change of a shape that an
// app reads fails here before it breaks an app.
func TestTheInterfaceForAppsIsHeld(t *testing.T) {
	theme, _ := FindKey("theme")
	got, err := json.MarshalIndent(map[string]any{
		"root":    AppRoot{Versions: APIVersions, Newest: 1, Program: "v1.2.0", Paired: true},
		"setting": AppSetting{Key: *theme, Value: "kelp", Agent: theme.Agent()},
		"chat":    AppChat{Run: "r7", Who: "lead", Lines: []string{"> done"}},
		"tell":    AppTell{Text: "start with billing"},
		"set":     AppSet{Key: "theme", Value: "kelp"},
		"pair":    AppPair{Word: "word", Session: "sha256:of-a-secret-the-app-made", Name: "a phone"},
		"routes":  []string{RouteAPI, APIv1 + APIView, APIv1 + APITask, APIv1 + APIChat, APIv1 + APILive, APIv1 + APIDo, APIv1 + APITell, APIv1 + APISettings, APIv1 + APISet, APIv1 + APIPair, APIv1 + APIPush},
		"header":  HeaderApp,
	}, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/api1.json")
	if os.Getenv("WHALESHARK_WRITE_API1") != "" {
		err = os.WriteFile("testdata/api1.json", append(got, '\n'), 0o644)
	} else if !bytes.Equal(bytes.TrimSpace(want), got) {
		t.Errorf("version 1 of the interface changed its shape:\n%s", got)
	}
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	code := &PairCode{Hash: TokenHash("word"), Until: now.Add(PairCodeLife)}
	var none *PairCode
	if !code.Good("word", now.Add(59*time.Second)) || code.Good("word", now.Add(time.Minute)) || code.Good("other", now) || code.Good("", now) || none.Good("word", now) {
		t.Error("a pairing code is good for its word, for a minute")
	}
	if PairText("ws1.example.com", "word") != "whaleshark://pair?at=ws1.example.com&word=word" {
		t.Errorf("the code as shown: %s", PairText("ws1.example.com", "word"))
	}
}

// 20.19: a profile holds no secret by construction, and reading one changes
// nothing: it lists what it would change.
func TestAProfileHoldsNoSecret(t *testing.T) {
	h, c := login(t)
	for _, kv := range [][2]string{{"notify.url", "https://example.org/n?token=SECRET-ADDRESS"}, {"theme", "kelp"}, {"spend.guard", "on"},
		{"fleets.total", "unlimited"}, {"shell", "/bin/zsh -l"}, {"shortcut.x", "/usr/bin/true"}, {"tools.hermes.run", full("/opt/h/hermes") + " chat"},
		{"tools.hermes.env.API_KEY", "SECRET-KEY"}, {"tools.hermes.suits", "long reads"}} {
		if _, err := Set(c, kv[0], kv[1]); err != nil {
			t.Fatalf("set %s: %v", kv[0], err)
		}
	}
	if err := ChangePhones(h, func(p *Phones) error {
		p.Paired = []Phone{{Session: "sha256:SECRET-PHONE"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	d, _ := h.Dirs()
	pr, left, err := SaveProfile(h, d)
	var file bytes.Buffer
	if err == nil {
		err = toml.NewEncoder(&file).Encode(pr)
	}
	if err != nil || !slices.Equal(left, []string{"the nudge address", "1 variables of hermes"}) {
		t.Fatalf("saved: %v, left out %q", err, left)
	}
	for _, secret := range []string{"SECRET", "example.org"} {
		if strings.Contains(file.String(), secret) {
			t.Errorf("the profile holds %s:\n%s", secret, file.String())
		}
	}
	if !strings.Contains(file.String(), "API_KEY") {
		t.Error("the profile does not name the variable it left empty")
	}
	// Read back on another computer it is data: what this program does not
	// know is named and skipped, and an address somebody wrote in is dropped.
	back, skipped, err := ReadProfile(append(file.Bytes(), "\n[config.notify]\nurl = \"https://example.org/x\"\n[config.later]\nword = 1\n"...))
	if err != nil || !slices.Contains(skipped, "config.later.word") || back.Config.Notify.URL != "" || back.Config.UI.Theme != "kelp" ||
		!back.Config.Spend.Guard || back.Config.Fleets.Total != Unlimited || back.Tools["hermes"].Suits != "long reads" || back.Tools["hermes"].Env["API_KEY"] != "" ||
		len(back.Engine.Shortcuts) != 1 || !slices.Equal(back.Engine.Shell, []string{"/bin/zsh", "-l"}) {
		t.Fatalf("read back: %v, skipped %q, %+v", err, skipped, back)
	}
	if _, _, err := ReadProfile([]byte("version = 99\n")); !errors.Is(err, ErrNewer) {
		t.Errorf("a profile of a newer program: %v", err)
	}
}
