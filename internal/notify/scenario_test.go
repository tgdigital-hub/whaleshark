package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/rules"
	"github.com/tgdigital-hub/whaleshark/internal/view"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// hookOut names the folder a hook of the tests writes into.
const hookOut = "NOTIFY_TEST_HOOK"

// TestMain is also the hook of the tests: a copy of this test program in the
// hook folder, started with nothing but the nudge on its standard input,
// writes that down. One whose name begins "hang" then stays, and says every
// few milliseconds that it is still there.
func TestMain(m *testing.M) {
	out := os.Getenv(hookOut)
	if out == "" || len(os.Args) > 1 {
		scenario.Main(m)
		return
	}
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), filepath.Ext(os.Args[0]))
	event, _ := io.ReadAll(os.Stdin)
	os.WriteFile(filepath.Join(out, name), event, 0o600)
	for strings.HasPrefix(name, "hang") {
		os.WriteFile(filepath.Join(out, "beat"), []byte(time.Now().String()), 0o600)
		time.Sleep(20 * time.Millisecond)
	}
}

// evening is the fixture's project with a nudger over the engine's double,
// with a window attached unless a test says otherwise.
type evening struct {
	*scenario.Project
	t     *testing.T
	n     Nudger
	run   string
	now   time.Time
	dirs  contract.Dirs
	after []string // the titles that went on to the channel after the pop-up
}

// prepare loads the evening, lets change alter it, and takes the shown stamp
// off the items named, so that they are new to the person.
func prepare(t *testing.T, change func(*testkit.Fixture), unshown ...string) *evening {
	t.Helper()
	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range unshown {
		f.State.Questions[id].ShownAt = time.Time{}
	}
	if change != nil {
		change(f)
	}
	e := &evening{Project: scenario.Prepare(t, f, nil), t: t, run: f.State.Run.ID, now: f.Now}
	rules.Plug(e.Kit)
	view.Plug(e.Kit)
	e.n = Nudger{k: e.Kit, next: []channel{func(_ context.Context, _ settings, c contract.Nudge) bool {
		e.after = append(e.after, c.Title)
		return true
	}}}
	if e.dirs, err = e.Kit.Platform.Dirs(); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *evening) write(path, text string) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

// pass moves the clock and runs the pass, as the next sweep would.
func (e *evening) pass(later time.Duration) {
	e.t.Helper()
	e.Clock(later)
	e.now = e.now.Add(later)
	if err := e.n.Items(e.Root, e.run, nil, e.now); err != nil {
		e.t.Fatal(err)
	}
}

// popups are the pop-ups the terminals were asked for: title, body and sound.
func (e *evening) popups() [][3]string {
	var out [][3]string
	for _, c := range e.Double.Calls() {
		if c.Op == contract.OpNotify {
			out = append(out, [3]string{c.Title, c.Text, map[bool]string{true: "sound", false: "silent"}[c.Sound]})
		}
	}
	return out
}

func (e *evening) want(titles ...string) {
	e.t.Helper()
	var got []string
	for _, p := range e.popups() {
		got = append(got, p[0])
	}
	if !slices.Equal(got, titles) {
		e.t.Fatalf("pop-ups %q, want %q", got, titles)
	}
}

func (e *evening) shown(id string) bool {
	s, _ := e.Record()
	return !s.Questions[id].ShownAt.IsZero()
}

func (e *evening) ui() (ui contract.UIFile) {
	e.t.Helper()
	file := filepath.Join(e.dirs.State, "ui.json")
	if err := contract.ReadVersioned(e.Kit.Platform.Read, file, contract.FileVersion, &ui); err != nil {
		e.t.Fatal(err)
	}
	return ui
}

// seen is the terminals with a look at the record at the moment of a pop-up.
type seen struct {
	contract.Terminals
	e      *evening
	before []bool
}

func (s *seen) Notify(title, body string, sound bool) (string, string, error) {
	s.before = append(s.before, s.e.shown("q7"))
	return s.Terminals.Notify(title, body, sound)
}

func TestItemBeforeNudge(t *testing.T) {
	e := prepare(t, nil, "q7")
	spy := &seen{Terminals: e.Kit.Terms, e: e}
	e.Kit.Terms = spy
	e.pass(0)
	if !slices.Equal(spy.before, []bool{true}) {
		t.Fatalf("the item was in the record before its nudge: %v, want one true", spy.before)
	}
	want := [3]string{"sign-up page — needs you", "Must the old sign-up link keep working?", "sound"}
	if got := e.popups(); len(got) != 1 || got[0] != want {
		t.Fatalf("pop-ups %q, want %q", got, want)
	}
	e.pass(5 * time.Second)
	e.want("sign-up page — needs you")
	if len(e.after) != 0 {
		t.Fatalf("a pop-up that was shown went on to the next channel: %q", e.after)
	}
}

func TestNextChannel(t *testing.T) {
	for _, c := range []struct {
		name          string
		attached, on  bool
		popups, after int
	}{
		{"on and a window attached", true, true, 1, 0},
		{"on and no window", false, true, 1, 1},
		{"our own switch off", true, false, 0, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := prepare(t, nil, "q7")
			if e.Double.Attached = c.attached; !c.on {
				e.write(filepath.Join(e.dirs.Config, "config.toml"), "[nudge]\npopup = false\n")
			}
			if reason, _, err := e.Double.Notify("a", "b", false); err != nil || (reason == contract.NotifyShown) != c.attached {
				t.Fatalf("the double answered %q, %v", reason, err)
			}
			e.pass(0)
			if got := len(e.popups()) - 1; got != c.popups || len(e.after) != c.after {
				t.Fatalf("%d pop-ups and %d on the next channel, want %d and %d", got, len(e.after), c.popups, c.after)
			}
		})
	}
}

func TestThirtySeconds(t *testing.T) {
	e := prepare(t, nil)
	nudge := func(task string) {
		t.Helper()
		if err := e.n.Nudge(contract.Nudge{Title: task, Task: task}); err != nil {
			t.Fatal(err)
		}
	}
	nudge("T7")
	nudge("T7")
	nudge("T8")
	// Another project's task of the same name is another task.
	if err := e.n.Nudge(contract.Nudge{Root: "elsewhere", Run: e.run, Title: "theirs", Task: "T7"}); err != nil {
		t.Fatal(err)
	}
	e.Clock(29 * time.Second)
	nudge("T7")
	e.want("T7", "T8", "theirs")
	e.Clock(time.Second)
	nudge("T7")
	e.want("T7", "T8", "theirs", "T7")
}

func TestFocusedTab(t *testing.T) {
	e := prepare(t, nil, "q7", "q9")
	e.Double.Push(testkit.PushFocused, "w1:p3")
	e.pass(0)
	e.want("photo upload — needs you")
	if !e.shown("q7") {
		t.Fatal("the item of the tab in front was not stamped as shown")
	}
}

func TestDoNotDisturb(t *testing.T) {
	e := prepare(t, func(f *testkit.Fixture) {
		f.UI.DND, f.UI.DNDUntil = true, f.Now.Add(time.Hour)
		f.State.Questions["n4"].Urgent = true
	}, "q7", "q9", "n4")
	e.pass(0)
	e.want("price list — needs you")
	if e.shown("q7") || e.shown("q9") {
		t.Fatal("an item held back was stamped as shown")
	}
	for _, urgent := range []bool{false, true} {
		if err := e.n.Nudge(contract.Nudge{Title: "the engine is not running", Urgent: urgent}); err != nil {
			t.Fatal(err)
		}
	}
	e.want("price list — needs you", "the engine is not running")

	// The end time passes while nothing runs; the next pass applies it. By
	// then a worker's question has waited for the lead agent long enough to
	// be the person's too, and was held with the others.
	e.pass(2 * time.Hour)
	if ui := e.ui(); ui.DND || !ui.DNDUntil.IsZero() || ui.FleetPane == "" {
		t.Fatalf("ui.json after the end time: %+v", ui)
	}
	if got := e.popups(); len(got) != 3 || got[2] != [3]string{"3 things waited while you were not to be disturbed", "order emails, sign-up page, photo upload", "sound"} {
		t.Fatalf("pop-ups %q", got)
	}
	if !e.shown("q7") || !e.shown("q9") || !e.shown("q10") {
		t.Fatal("what was held is not stamped as shown")
	}
	e.pass(5 * time.Second)
	if len(e.popups()) != 3 {
		t.Fatalf("a second nudge for what was held: %q", e.popups())
	}
}

func TestSwitchedOffByHand(t *testing.T) {
	e := prepare(t, func(f *testkit.Fixture) { f.UI.DND = true }, "q7", "q9")
	e.pass(time.Hour)
	e.want()
	ui := e.ui()
	ui.DND = false
	if err := contract.WriteVersioned(e.Kit.Platform, filepath.Join(e.dirs.State, "ui.json"), contract.FileVersion, ui); err != nil {
		t.Fatal(err)
	}
	e.pass(time.Second)
	e.want("3 things wait for you")
}

func TestSound(t *testing.T) {
	for name, c := range map[string]struct {
		mute         bool
		config, want string
	}{
		"as it comes":      {false, "", "sound"},
		"mute":             {true, "", "silent"},
		"sound switch off": {false, "[nudge]\nsound = false\n", "silent"},
	} {
		t.Run(name, func(t *testing.T) {
			e := prepare(t, func(f *testkit.Fixture) { f.UI.Mute = c.mute }, "q7")
			if c.config != "" {
				e.write(filepath.Join(e.dirs.Config, "config.toml"), c.config)
			}
			e.pass(0)
			if got := e.popups(); len(got) != 1 || got[0][2] != c.want {
				t.Fatalf("pop-ups %q, want one with the sound %q", got, c.want)
			}
		})
	}
}

func TestPlug(t *testing.T) {
	k := contract.NewKit()
	Plug(k)
	if n, ok := k.Notifier.(Nudger); !ok || len(n.next) != 1 {
		t.Fatal("the nudger with the phone after the pop-up is not the kit's notifier")
	}
}

// service is a notice service of the test's own, at the address the
// person's settings then name with rest after it; it keeps each request as
// its line and its body.
func (e *evening) service(rest, more string) chan string {
	got := make(chan string, 9)
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- r.Method + " " + r.URL.RequestURI() + " " + string(body)
	}))
	e.t.Cleanup(srv.Close)
	e.n.next, e.Double.Attached = []channel{phone}, false
	e.write(filepath.Join(e.dirs.Config, "config.toml"),
		fmt.Sprintf("[notify]\nurl = %q\nlink = \"https://page.example/r3\"\n%s", srv.URL+rest, more))
	return got
}

// requests are what the service was sent so far.
func requests(got chan string) (out []string) {
	for len(got) > 0 {
		out = append(out, <-got)
	}
	return out
}

func TestPhone(t *testing.T) {
	const title, text = "sign-up page — needs you", "Must the old sign-up link keep working?"
	for _, c := range []struct {
		name, rest, more, project string
		attached                  bool
		want                      []string
	}{
		{name: "title and name only, and the link", rest: "/topic?click={link}",
			want: []string{"POST /topic?click=https%3A%2F%2Fpage.example%2Fr3 " + title}},
		{name: "the text once the project allows it", rest: "/topic", project: "[notify]\nfull_text = true\n",
			want: []string{"POST /topic " + title + "\n" + text}},
		{name: "the message as a part of the address", rest: "/send?chat=7&text={text}",
			want: []string{"POST /send?chat=7&text=sign-up+page+%E2%80%94+needs+you "}},
		{name: "the phone switched off", rest: "/topic", more: "[nudge]\nphone = false\n"},
		{name: "a window showed the pop-up", rest: "/topic", attached: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := prepare(t, nil, "q7")
			got := e.service(c.rest, c.more)
			if e.Double.Attached = c.attached; c.project != "" {
				e.write(filepath.Join(e.Root, "whaleshark.toml"), c.project)
			}
			e.pass(0)
			if sent := requests(got); !slices.Equal(sent, c.want) {
				t.Fatalf("the service was sent %q, want %q", sent, c.want)
			}
		})
	}
}

func TestPhoneDoNotDisturb(t *testing.T) {
	e := prepare(t, func(f *testkit.Fixture) {
		f.UI.DND, f.UI.DNDUntil = true, f.Now.Add(time.Hour)
		f.State.Questions["n4"].Urgent = true
	}, "q7", "q9", "n4")
	got := e.service("/topic", "")
	e.pass(0)
	if sent, want := requests(got), []string{"POST /topic price list — needs you"}; !slices.Equal(sent, want) {
		t.Fatalf("under Do not disturb the service was sent %q, want %q", sent, want)
	}
}

// hook puts a hook program of that name into the person's hook folder and
// returns the folder it writes into.
func (e *evening) hook(names ...string) (out string) {
	e.t.Helper()
	self, err := os.Executable()
	if err != nil {
		e.t.Fatal(err)
	}
	program, err := os.ReadFile(self)
	if err != nil {
		e.t.Fatal(err)
	}
	out = e.t.TempDir()
	e.t.Setenv(hookOut, out)
	for _, name := range names {
		path := filepath.Join(e.dirs.Config, "hooks", "nudge.d", name+filepath.Ext(self))
		e.write(path, "")
		if err := os.WriteFile(path, program, 0o700); err != nil {
			e.t.Fatal(err)
		}
		if err := os.Chmod(path, 0o700); err != nil {
			e.t.Fatal(err)
		}
	}
	return out
}

func TestHook(t *testing.T) {
	e := prepare(t, func(f *testkit.Fixture) { f.UI.Mute = true }, "q7")
	out := e.hook("log")
	e.pass(0)
	var got map[string]any
	data, err := os.ReadFile(filepath.Join(out, "log"))
	if err == nil {
		err = json.Unmarshal(data, &got)
	}
	want := map[string]any{"event": "nudge", "title": "sign-up page — needs you", "body": "Must the old sign-up link keep working?",
		"task": "T2", "run": e.run, "root": e.Root, "urgent": false, "sound": false}
	if err != nil || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("the hook was handed %v (%v), want %v", got, err, want)
	}
}

func TestHookFolderOfOthers(t *testing.T) {
	e := prepare(t, nil, "q7")
	out := e.hook("log")
	dir := filepath.Join(e.dirs.Config, "hooks", "nudge.d")
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if others, err := e.Kit.Platform.WritableByOthers(dir); errors.Is(err, contract.ErrCannotTell) {
		t.Skip("this system cannot tell who else may write to a folder")
	} else if !others {
		t.Fatalf("the folder was not made writable by others: %v", err)
	}
	e.pass(0)
	if _, err := os.Stat(filepath.Join(out, "log")); err == nil {
		t.Fatal("a hook ran from a folder others can write to")
	}
	e.want("sign-up page — needs you")
}

func TestHangingHook(t *testing.T) {
	e := prepare(t, nil, "q7")
	out := e.hook("hang", "log")
	start := time.Now()
	e.pass(0)
	if took := time.Since(start); took < reach || took > reach+3*time.Second {
		t.Fatalf("the nudge with a hanging hook took %v, want %v", took, reach)
	}
	beat := func() string {
		data, _ := os.ReadFile(filepath.Join(out, "beat"))
		return string(data)
	}
	// Ended in the middle of a sign, the hanging one leaves an empty file.
	for _, name := range []string{"log", "beat"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Fatalf("the two hooks did not both run: %v", err)
		}
	}
	was := beat()
	if time.Sleep(300 * time.Millisecond); beat() != was {
		t.Fatal("the hanging hook is still there after its ten seconds")
	}
}

// gone is the terminals out of reach: no picture and no pop-up.
type gone struct{ contract.Terminals }

func (gone) Snapshot(context.Context) (*contract.Snapshot, error) {
	return nil, contract.ErrEngineUnreachable
}

func (gone) Notify(string, string, bool) (string, string, error) {
	return "", "", contract.ErrEngineUnreachable
}

func TestNews(t *testing.T) {
	e := prepare(t, func(f *testkit.Fixture) {
		t7 := f.State.Tasks["T7"]
		t7.Status, t7.Failures, t7.SettledAt = contract.TaskFailed, contract.MaxFailures, f.Now
	})
	change := func(to func(s *contract.State)) {
		t.Helper()
		err := e.Kit.Store.Change(e.Root, e.run, func(s *contract.State) error { to(s); return nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	e.pass(0)
	e.pass(5 * time.Second)
	e.want("email sender — failed")

	// Reset and failed again is news again; the whole job settled is news
	// once, and under Do not disturb only when that is over.
	change(func(s *contract.State) { s.Tasks["T7"].Status = contract.TaskReady })
	e.pass(time.Minute)
	ui := e.ui()
	ui.DND = true
	file := filepath.Join(e.dirs.State, "ui.json")
	if err := contract.WriteVersioned(e.Kit.Platform, file, contract.FileVersion, ui); err != nil {
		t.Fatal(err)
	}
	change(func(s *contract.State) {
		for _, task := range s.Tasks {
			task.Status, task.SettledAt = contract.TaskDone, e.now
		}
		s.Tasks["T7"].Status = contract.TaskFailed
	})
	e.pass(time.Minute)
	e.want("email sender — failed")
	ui.DND = false
	if err := contract.WriteVersioned(e.Kit.Platform, file, contract.FileVersion, ui); err != nil {
		t.Fatal(err)
	}
	e.pass(time.Minute)
	e.pass(time.Minute)
	e.want("email sender — failed", "email sender — failed", "new shop site — finished")
	if len(e.after) != 0 {
		t.Fatalf("news a window showed went on to the next channel: %q", e.after)
	}

	// The terminals out of reach is the sweep's to say, which knows since
	// when no picture could be had: the pass adds no second nudge to it.
	change(func(s *contract.State) { s.Run.Terms.SeenAt = e.now })
	e.Kit.Terms = gone{e.Kit.Terms}
	e.n.k = e.Kit
	e.pass(time.Minute)
	e.pass(time.Minute)
	if slices.Contains(e.after, contract.ErrEngineUnreachable.Error()) {
		t.Fatalf("the pass said the engine's outage itself: %q", e.after)
	}
}

func TestLeadUnread(t *testing.T) {
	e := prepare(t, func(f *testkit.Fixture) {
		f.State.Run.Lead = contract.LeadSeen{Unseen: true, DoneSince: f.Now.Add(-time.Minute)}
		f.State.Questions["n9"] = &contract.Question{ID: "n9", Kind: contract.KindNeed, Form: contract.FormTodo,
			For: contract.ForHuman, From: contract.FromTool, Cause: contract.CauseLeadUnread, State: contract.QuestionOpen,
			Text: "The lead agent has said something you have not read.", CreatedAt: f.Now}
		// A worker's question that would become the person's in these minutes.
		f.State.Questions["q10"].State = contract.QuestionClosed
	})
	e.Double.Push(testkit.PushFocused, "w1:p3")
	e.pass(0)
	e.pass(8*time.Minute + 59*time.Second)
	if e.want(); e.shown("n9") {
		t.Fatal("what the lead agent said was stamped before its ten minutes")
	}
	e.pass(time.Second)
	e.pass(5 * time.Second)
	if e.want("lead agent — needs you"); !e.shown("n9") {
		t.Fatal("what the lead agent said is not stamped after its nudge")
	}
}

// The real program, as the lead agent runs it: its new item reaches the
// service when no window is there to show the pop-up.
func TestNeedReachesThePhone(t *testing.T) {
	e := prepare(t, nil)
	got := e.service("/topic", "")
	if out, err := e.Command(scenario.Orch, "need", "question", "Which colour for the buttons?").CombinedOutput(); err != nil {
		t.Fatalf("need: %v\n%s", err, out)
	}
	if sent, want := requests(got), []string{"POST /topic whaleshark — needs you"}; !slices.Equal(sent, want) {
		t.Fatalf("the service was sent %q, want %q", sent, want)
	}
}
