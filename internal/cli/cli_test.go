package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// fakeStore holds runs in memory and counts every call made to it.
type fakeStore struct {
	contract.NoStore
	runs    map[string]*contract.State
	order   []string
	current string
	broken  error
	calls   int
}

func (s *fakeStore) Runs(string) ([]string, error)  { s.calls++; return s.order, nil }
func (s *fakeStore) Current(string) (string, error) { s.calls++; return s.current, nil }
func (s *fakeStore) Read(_, run string) (*contract.State, error) {
	s.calls++
	if s.broken != nil {
		return nil, s.broken
	}
	return s.runs[run], nil
}

// scene is the surroundings a command is run in.
type scene struct {
	pane, attempt, run, from, key, own string
	terminal                           bool
}

// stdin is what the next command reads as its standard input.
var stdin io.Reader = strings.NewReader("")

var (
	lead     = scene{pane: "w1:p1"}
	worker   = scene{pane: "w1:p2", attempt: "T1.1", run: "r3"}
	restored = scene{pane: "w1:p2"}
	person   = scene{terminal: true}
	nobody   = scene{}
	other    = scene{pane: "w9:p9"}
)

// world is the evening fixture as run r3, with an older closed run r1 whose
// lead agent sat in pane w1:p20.
func world(t *testing.T) (*contract.Kit, *fakeStore) {
	t.Helper()
	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	old := &contract.State{Run: contract.Run{ID: "r1", ClosedAt: f.Now, Orchestrator: &contract.Binding{Pane: "w1:p20"}}}
	store := &fakeStore{runs: map[string]*contract.State{"r1": old, "r3": &f.State}, order: []string{"r1", "r3"}, current: "r3"}
	k := contract.NewKit()
	Plug(k)
	k.Store = store
	return k, store
}

// call runs one command line as a caller and returns what it printed.
func call(t *testing.T, k *contract.Kit, w scene, line ...string) (out, errw string, exit int) {
	t.Helper()
	t.Setenv(contract.EnvPane, w.pane)
	t.Setenv(contract.EnvAttempt, w.attempt)
	t.Setenv(contract.EnvRun, w.run)
	t.Setenv(contract.EnvFrom, w.from)
	t.Setenv(contract.EnvActivePane, w.key)
	t.Setenv(contract.EnvOwnTab, w.own)
	t.Setenv(contract.EnvRoot, "")
	onTerminal = func() bool { return w.terminal }
	var o, e bytes.Buffer
	exit = k.Main(k, line, stdin, &o, &e)
	// A button's tab stays for a failure to be read; the line that says so is no part of the answer.
	return o.String(), strings.TrimSuffix(e.String(), "This tab closes when you press Enter.\n"), exit
}

// seen binds a handler that records the call it was given.
func seen(k *contract.Kit, command string) *contract.Call {
	got := new(contract.Call)
	k.Handle(command, func(c *contract.Call) (any, error) { *got = *c; return nil, nil })
	return got
}

func golden(t *testing.T, name string, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs from its golden file:\n%s", name, got)
	}
}

// transcript runs command lines and writes down what each printed.
func transcript(t *testing.T, cases []struct {
	as   string
	w    scene
	line string
}) string {
	var b strings.Builder
	for _, tc := range cases {
		k, _ := world(t)
		out, errw, exit := call(t, k, tc.w, strings.Fields(tc.line)...)
		fmt.Fprintf(&b, "$ whaleshark %s    # as %s\n%s%sexit %d\n\n", tc.line, tc.as, out, errw, exit)
	}
	return b.String()
}

func TestHelpGolden(t *testing.T) {
	golden(t, "help", transcript(t, []struct {
		as   string
		w    scene
		line string
	}{
		{"a program", nobody, ""},
		{"the person", person, "help start"},
		{"the person", person, "help run"},
		{"the person", person, "pause --help"},
		{"the lead agent", lead, "help pause"},
		{"the lead agent", lead, "help answer"},
		{"a worker", worker, "help report"},
		{"the lead agent", lead, "help need --json"},
	}))
}

func TestErrorsGolden(t *testing.T) {
	golden(t, "errors", transcript(t, []struct {
		as   string
		w    scene
		line string
	}{
		{"the person", person, "strat T1"},
		{"the person", person, "stpo T1"},
		{"the person", person, "clos --settled"},
		{"the person", person, "help lnad"},
		{"the person", person, "help a b"},
		{"the person", person, "start --redy"},
		{"the person", person, "start --ready=yes"},
		{"the person", person, "wait --timeout"},
		{"the person", person, "wait --timeout soon"},
		{"the person", person, "task add T9 title --after T1,"},
		{"the person", person, "task add T9 title --cwd ../elsewhere"},
		{"the person", person, "task add T9 title --owns src/**,/etc/passwd"},
		{"the person", person, "status --run -r3"},
		{"the person", person, "status --run r9"},
		{"the person", person, "tell T3 --file testdata/none.md"},
		{"the person", person, "version now"},
		{"the person", person, "strat --json"},
		{"the lead agent", lead, "start --redy --json"},
		{"the lead agent", lead, "start T3"},
		{"the lead agent", lead, "pause"},
		{"the lead agent", lead, "pause --human"},
		{"the lead agent", lead, "answer n1 --undo"},
		{"the lead agent", lead, "report done ok"},
		{"a worker", worker, "status"},
		{"a worker", worker, "accept T1 --json"},
		{"a worker", worker, "accept T1 --human"},
		{"a worker in a restored tab", restored, "status"},
		{"another tab", other, "start T3"},
		{"another tab", other, "start T3 --json"},
		{"no tab and no terminal", nobody, "accept T7"},
	}))
}

// A usage error is reported before the record is even read, and writes nothing.
func TestUsageErrorTouchesNoState(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, line := range [][]string{
		{"strat"}, {"start", "--redy"}, {"start", "--ready=1"}, {"wait", "--timeout"}, {"wait", "--timeout", "x"},
		{"task", "add", "T1", "t", "--after", "a\nb"}, {"task", "add", "T1", "t", "--cwd", "/tmp"},
		{"status", "--run"}, {"accept", "T1", "--by-hand"}, {"init", "--agent", "Claude"},
	} {
		for _, w := range []scene{lead, worker, person, nobody, other} {
			k, store := world(t)
			for i := range contract.Commands {
				k.Handle(contract.Commands[i].Name, func(*contract.Call) (any, error) {
					t.Errorf("%v: a handler ran", line)
					return nil, nil
				})
			}
			if _, errw, exit := call(t, k, w, line...); exit != contract.ExitUsage || errw == "" {
				t.Errorf("%v: exit %d, %q", line, exit, errw)
			}
			if store.calls != 0 {
				t.Errorf("%v: the store was called %d times", line, store.calls)
			}
		}
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("files were written: %v", left)
	}
}

// refusal runs a command line with --json and returns the error of its envelope.
func refusal(t *testing.T, k *contract.Kit, w scene, line ...string) (contract.Refusal, int) {
	t.Helper()
	out, errw, exit := call(t, k, w, append(line, "--json")...)
	var e struct {
		OK    bool
		Error contract.Refusal
	}
	if err := json.Unmarshal([]byte(out), &e); err != nil || errw != "" || e.OK != (exit == 0) {
		t.Fatalf("%v: not one envelope on stdout: %q %q (%v)", line, out, errw, err)
	}
	return e.Error, exit
}

// A worker, known by its variable or by its pane, is refused every command
// and every form the table does not mark for it; no handler runs.
func TestWorkerRefused(t *testing.T) {
	for _, w := range []scene{worker, restored, {attempt: "T1.1"}} {
		for i := range contract.Commands {
			cmd := &contract.Commands[i]
			forms := []string{""}
			for _, s := range cmd.Subs {
				forms = append(forms, s.Name)
			}
			for _, sub := range forms {
				k, _ := world(t)
				ran := false
				k.Handle(cmd.Name, func(*contract.Call) (any, error) { ran = true; return nil, nil })
				line := []string{cmd.Name}
				if sub != "" {
					line = append(line, sub)
				}
				r, exit := refusal(t, k, w, line...)
				if may := cmd.May(contract.Worker, sub); may != ran || may != (exit == 0) {
					t.Errorf("%v as a worker: ran %v, exit %d, the table says %v", line, ran, exit, may)
				} else if !may && (exit != contract.ExitRefused || r.Code != "not_for_worker" || !slices.Equal(r.Next, []string{"whaleshark guide worker"})) {
					t.Errorf("%v as a worker: exit %d, %+v", line, exit, r)
				}
			}
		}
	}
}

func TestCallers(t *testing.T) {
	two := scene{pane: "w1:p20"}
	for _, tc := range []struct {
		name string
		w    scene
		line string
		kind contract.CallerKind
		code string
		at   contract.Caller
		run  string
	}{
		{"the lead agent's pane", lead, "start T3", contract.Orchestrator, "", contract.Caller{Pane: "w1:p1"}, "r3"},
		{"a worker by its variable", worker, "progress 40 x", contract.Worker, "", contract.Caller{Pane: "w1:p2", Attempt: "T1.1"}, "r3"},
		{"a worker in a restored tab", restored, "progress 40 x", contract.Worker, "", contract.Caller{Pane: "w1:p2", Attempt: "T1.1"}, "r3"},
		{"a worker whose attempt has ended is no worker", scene{pane: "w1:p99"}, "status", contract.Unbound, "", contract.Caller{Pane: "w1:p99"}, "r3"},
		{"a person at a terminal", person, "pause", contract.Human, "", contract.Caller{Where: contract.WhereTyped}, "r3"},
		{"no pane and no terminal", nobody, "status", contract.Unbound, "", contract.Caller{}, "r3"},
		{"no pane and no terminal changes nothing", nobody, "start T3", "", "not_bound", contract.Caller{}, ""},
		{"a pane no run is bound to", other, "status", contract.Unbound, "", contract.Caller{Pane: "w9:p9"}, "r3"},
		{"a pane no run is bound to may bind", other, "run takeover", contract.Unbound, "", contract.Caller{Pane: "w9:p9"}, "r3"},
		{"a pane no run is bound to changes nothing", other, "accept T7", "", "not_bound", contract.Caller{}, ""},
		{"the pane of a closed run", two, "start T3", "", "not_bound", contract.Caller{}, ""},
		{"the lead agent addressing another run", lead, "start T3 --run r1", "", "not_bound", contract.Caller{}, ""},
		{"--human from a pane of the person's", other, "pause --human", contract.Human, "", contract.Caller{Pane: "w9:p9", Where: contract.WhereTyped}, "r3"},
		{"--human from the pane of a closed run", two, "pause --human", contract.Human, "", contract.Caller{Pane: "w1:p20", Where: contract.WhereTyped}, "r3"},
		{"--human from a shortcut of the keeper, whichever pane is in front", scene{key: "w1:p2"}, "pause --human", contract.Human, "", contract.Caller{Where: contract.WhereTyped}, "r3"},
		{"a shortcut's mark alone makes nobody the human", scene{key: "w1:p2"}, "pause", "", "human_only", contract.Caller{}, ""},
		{"--human with no pane, no terminal and no shortcut", nobody, "pause --human", "", "not_human", contract.Caller{}, ""},
		{"--human from a shortcut with a worker's variable", scene{attempt: "T1.1", key: "w1:p2"}, "accept T1 --human", "", "not_human", contract.Caller{}, ""},
		{"--human from one of our panes", scene{pane: "w9:p9", from: "pane"}, "accept T7 --human", contract.Human, "", contract.Caller{Pane: "w9:p9", Where: contract.WherePane}, "r3"},
		{"a pane's mark alone makes nobody the human", scene{pane: "w9:p9", from: "pane"}, "accept T7", "", "not_bound", contract.Caller{}, ""},
		{"--human from the lead agent's pane", lead, "pause --human", "", "not_human", contract.Caller{}, ""},
		{"--human from a worker's pane", restored, "accept T1 --human", "", "not_human", contract.Caller{}, ""},
		{"--human with a worker's variable", scene{attempt: "T1.1", terminal: true}, "accept T1 --human", "", "not_human", contract.Caller{}, ""},
		{"a child of the page", scene{from: "page"}, "pause", contract.Human, "", contract.Caller{Where: contract.WherePage}, "r3"},
		{"a child of the page, from a phone", scene{from: "phone"}, "answer n1 yes --human", contract.Human, "", contract.Caller{Where: contract.WherePhone}, "r3"},
		{"the page's mark in the lead agent's pane", scene{pane: "w1:p1", from: "page"}, "pause", "", "human_only", contract.Caller{}, ""},
		{"the page's mark and --human in the lead agent's pane", scene{pane: "w1:p1", from: "phone"}, "answer n1 yes --human", "", "not_human", contract.Caller{}, ""},
		{"the page's mark in a worker's pane", scene{pane: "w1:p2", from: "page"}, "accept T1", "", "not_for_worker", contract.Caller{}, ""},
		{"the page's mark with a worker's variable", scene{attempt: "T1.1", from: "page"}, "accept T1", "", "not_for_worker", contract.Caller{}, ""},
		{"the page's mark in a pane no run is bound to", scene{pane: "w9:p9", from: "page"}, "accept T7", "", "not_bound", contract.Caller{}, ""},
		{"the page's mark leaves the lead agent the lead agent", scene{pane: "w1:p1", from: "page"}, "start T3", contract.Orchestrator, "", contract.Caller{Pane: "w1:p1"}, "r3"},
		{"the lead agent addressing another run with --human", lead, "pause --human --run r1", "", "not_human", contract.Caller{}, ""},
		{"the lead agent under another run's variable with --human", scene{pane: "w1:p1", run: "r1"}, "pause --human", "", "not_human", contract.Caller{}, ""},
		{"the tab of a button's long command is the button's", scene{pane: "w9:p9", own: "page"}, "accept T7 --human", contract.Human, "", contract.Caller{Pane: "w9:p9", Where: contract.WherePage}, "r3"},
		{"that tab's mark in the lead agent's pane", scene{pane: "w1:p1", own: "pane"}, "pause --human", "", "not_human", contract.Caller{}, ""},
		{"that tab's mark alone in the lead agent's pane", scene{pane: "w1:p1", own: "page"}, "pause", "", "human_only", contract.Caller{}, ""},
		{"that tab's mark in a worker's pane", scene{pane: "w1:p2", own: "page"}, "accept T1 --human", "", "not_human", contract.Caller{}, ""},
		{"that tab's mark with a worker's variable", scene{pane: "w9:p9", attempt: "T1.1", own: "pane"}, "accept T1 --human", "", "not_human", contract.Caller{}, ""},
		{"that tab's mark where there is no pane", scene{own: "page"}, "pause", "", "human_only", contract.Caller{}, ""},
		{"the lead agent may not pause", lead, "pause", "", "human_only", contract.Caller{}, ""},
		{"the lead agent may not undo", lead, "answer n1 --undo", "", "human_only", contract.Caller{}, ""},
		{"the person may undo", person, "answer n1 --undo", contract.Human, "", contract.Caller{Where: contract.WhereTyped}, "r3"},
		{"a note to the lead agent is the person's", lead, "tell lead hello", "", "human_only", contract.Caller{}, ""},
		{"the person does not report", person, "report done ok", "", "worker_only", contract.Caller{}, ""},
		{"the run named by the flag", person, "status --run r1", contract.Human, "", contract.Caller{Where: contract.WhereTyped}, "r1"},
		{"the run of the worker's variable", scene{attempt: "T1.1", run: "r1"}, "mail", contract.Worker, "", contract.Caller{Attempt: "T1.1"}, "r1"},
	} {
		k, _ := world(t)
		line := strings.Fields(tc.line)
		got := seen(k, line[0])
		r, exit := refusal(t, k, tc.w, line...)
		tc.at.Kind = tc.kind
		if r.Code != tc.code || (tc.code != "") != (exit == contract.ExitRefused) || (tc.code == "") != (exit == 0) {
			t.Errorf("%s: exit %d, code %q, want %q", tc.name, exit, r.Code, tc.code)
		} else if tc.code == "" && (got.Caller != tc.at || got.Run != tc.run) {
			t.Errorf("%s: caller %+v of run %q, want %+v of %q", tc.name, got.Caller, got.Run, tc.at, tc.run)
		} else if strings.Contains(r.Message+strings.Join(r.Next, " "), "--human") && tc.code != "not_human" {
			t.Errorf("%s: --human is shown to a caller that is not the human: %+v", tc.name, r)
		}
	}
}

// A next line with --human reaches the person and nobody else.
func TestHumanNeverShown(t *testing.T) {
	for _, w := range []scene{lead, other, person} {
		k, _ := world(t)
		k.Handle("status", func(*contract.Call) (any, error) {
			return nil, &contract.Refusal{Exit: contract.ExitRefused, Code: "x", Message: "no", Next: []string{"whaleshark show T1", "whaleshark accept T1 --human"}}
		})
		_, errw, _ := call(t, k, w, "status")
		if shown := strings.Contains(errw, "--human"); shown != w.terminal || !strings.Contains(errw, "Next: whaleshark show T1") {
			t.Errorf("%+v: %q", w, errw)
		}
	}
	for i := range contract.Commands {
		for _, w := range []scene{lead, worker, other, nobody} {
			k, _ := world(t)
			out, errw, _ := call(t, k, w, "help", contract.Commands[i].Name)
			js, _, _ := call(t, k, w, "help", contract.Commands[i].Name, "--json")
			if strings.Contains(out+errw+js, "--human") {
				t.Errorf("help %s shows --human to %+v", contract.Commands[i].Name, w)
			}
		}
	}
}

// A record that cannot be read stops everything but what is open to all.
func TestUnreadableRecord(t *testing.T) {
	for err, code := range map[error]string{contract.ErrNewer: "newer_state", errors.New("torn"): "state_unreadable"} {
		k, store := world(t)
		store.broken = fmt.Errorf("run r3: %w", err)
		if r, exit := refusal(t, k, other, "pause", "--human"); exit != contract.ExitEnv || r.Code != code {
			t.Errorf("pause: exit %d, %+v", exit, r)
		}
		if _, exit := refusal(t, k, other, "version"); exit != 0 {
			t.Errorf("version: exit %d", exit)
		}
	}
}

func TestParse(t *testing.T) {
	k, _ := world(t)
	note := filepath.Join(t.TempDir(), "note.md")
	os.WriteFile(note, []byte("line one\nline two\n"), 0o600)
	for _, tc := range []struct {
		line  []string
		stdin string
		args  []string
		flags map[string][]string
	}{
		{[]string{"task", "add", "T9", "a title", "--after=T1,T2", "--owns", "src/**,docs", "--shared", "--check", "make test"}, "",
			[]string{"add", "T9", "a title"}, map[string][]string{"after": {"T1,T2"}, "owns": {"src/**,docs"}, "shared": {""}, "check": {"make test"}}},
		{[]string{"task", "add", "T9", "t", "--owns", "src/{a,b}/**,docs"}, "", []string{"add", "T9", "t"}, map[string][]string{"owns": {"src/{a,b}/**,docs"}}},
		{[]string{"tell", "T3", "--", "--not-a-flag", "-x"}, "", []string{"T3", "--not-a-flag", "-x"}, map[string][]string{}},
		{[]string{"tell", "T3", "- a dash first"}, "", []string{"T3", "- a dash first"}, map[string][]string{}},
		{[]string{"tell", "T3", "--file", note}, "", []string{"T3", "line one\nline two"}, map[string][]string{"file": {note}}},
		{[]string{"tell", "T3", "-"}, "typed in\n", []string{"T3", "typed in"}, map[string][]string{}},
		{[]string{"tell", "T3", "--file", "-"}, "typed in", []string{"T3", "typed in"}, map[string][]string{"file": {"-"}}},
		{[]string{"accept", "T7", "--by-hand", "--file", note}, "", []string{"T7"}, map[string][]string{"by-hand": {"line one\nline two"}, "file": {note}}},
		{[]string{"accept", "T7", "--by-hand=read it"}, "", []string{"T7"}, map[string][]string{"by-hand": {"read it"}}},
		{[]string{"run", "remove", "r1"}, "", []string{"rm", "r1"}, map[string][]string{}},
		{[]string{"run", "delete", "r1"}, "", []string{"rm", "r1"}, map[string][]string{}},
		{[]string{"tell", "remove", "x"}, "", []string{"remove", "x"}, map[string][]string{}},
	} {
		got := seen(k, tc.line[0])
		stdin = strings.NewReader(tc.stdin)
		if _, errw, exit := call(t, k, person, tc.line...); exit != 0 {
			t.Errorf("%v: exit %d, %s", tc.line, exit, errw)
		}
		if !slices.Equal(got.Args, tc.args) || fmt.Sprint(got.Flags) != fmt.Sprint(tc.flags) {
			t.Errorf("%v: args %q, flags %q", tc.line, got.Args, got.Flags)
		}
	}
	if _, _, exit := call(t, k, person, "tell", "T3", "--file", note+".none"); exit != contract.ExitUsage {
		t.Errorf("a file that is not there: exit %d", exit)
	}
	stdin = strings.NewReader(strings.Repeat("x", maxText+1))
	if _, _, exit := call(t, k, person, "tell", "T3", "-"); exit != contract.ExitUsage {
		t.Errorf("too much text: exit %d", exit)
	}
}

// A handler's refusal keeps its exit code, the same in text and in JSON; any
// other error is exit 1; what is printed cannot steer the terminal.
func TestEnvelopeAndExit(t *testing.T) {
	k, _ := world(t)
	var fail error
	k.Handle("status", func(c *contract.Call) (any, error) {
		fmt.Fprintln(c.Out, "text for a person")
		return map[string]int{"tasks": 3}, fail
	})
	if out, _, exit := call(t, k, person, "status", "--json"); exit != 0 || out != "{\"ok\":true,\"result\":{\"tasks\":3}}\n" {
		t.Errorf("exit %d, %q", exit, out)
	}
	if out, _, _ := call(t, k, person, "status"); out != "text for a person\n" {
		t.Errorf("%q", out)
	}
	for _, exit := range []int{1, 2, 3, 4, 5, 6, 7, 75} {
		fail = fmt.Errorf("wrapped: %w", &contract.Refusal{Exit: exit, Code: "c", Message: "\x1b]0;title\a\x1b[31mred\u202e", Next: []string{"whaleshark <graph> & more"}})
		out, errw, text := call(t, k, person, "status")
		js, _, code := call(t, k, person, "status", "--json")
		want := `{"ok":false,"error":{"code":"c","message":"\u001b]0;title\u0007\u001b[31mred` + "\u202e" + `","next":["whaleshark <graph> & more"]}}` + "\n"
		if text != exit || code != exit || out != "text for a person\n" || errw != "]0;title[31mred\nNext: whaleshark <graph> & more\n" || js != want {
			t.Errorf("exit %d: text %d, json %d, %q, %q", exit, text, code, errw, js)
		}
	}
	fail = errors.New("it broke")
	if r, exit := refusal(t, k, person, "status"); exit != contract.ExitFailed || r.Code != "failed" || r.Message != "it broke" {
		t.Errorf("exit %d, %+v", exit, r)
	}
	if r, exit := refusal(t, k, person, "graph"); exit != contract.ExitFailed || r.Code != "not_built" {
		t.Errorf("exit %d, %+v", exit, r)
	}
}

// The nearest word is offered, and never a destructive one.
func TestDidYouMean(t *testing.T) {
	for word, want := range map[string]string{"strat": "start", "statsu": "status", "hlep": "help", "acept": "accept", "xyzzy": ""} {
		if got, _ := NoSuch("command", word, names(), "x").Data.(map[string]string); got["did_you_mean"] != want {
			t.Errorf("%s: %q, want %q", word, got["did_you_mean"], want)
		}
	}
	known := append(names(), contract.Destructive...)
	for _, d := range contract.Destructive {
		for i := range d {
			for _, slip := range []string{d[:i] + d[i+1:], d[:i] + "x" + d[i:], d[:i] + "x" + d[i+1:]} {
				r := NoSuch("command", slip, known, "x")
				if got, _ := r.Data.(map[string]string); slices.Contains(contract.Destructive, got["did_you_mean"]) {
					t.Errorf("%q is corrected into %q", slip, got["did_you_mean"])
				}
			}
		}
	}
}

func TestValid(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "src", "url"), 0o755)
	os.Symlink(t.TempDir(), filepath.Join(root, "out"))
	os.Symlink(filepath.Join(root, "src"), filepath.Join(root, "in"))
	for _, tc := range []struct {
		kind, v string
		ok      bool
	}{
		{"id", "T3", true}, {"id", "a_b-c", true}, {"id", "", false}, {"id", "-T3", false}, {"id", "T 3", false},
		{"id", "T3\n", false}, {"id", "T3.1", false}, {"id", "abcdefghijklmnop", true}, {"id", "abcdefghijklmnopq", false}, {"id", "tâche", false},
		{"title", "url parser", true}, {"title", strings.Repeat("é", 80), true}, {"title", strings.Repeat("e", 81), false},
		{"title", "two\nlines", false}, {"title", "bell\a", false}, {"title", "turned\u202e", false}, {"title", "\xff", false},
		{"name", "sign-up page", true}, {"name", "one two three", true}, {"name", "one two three four", false},
		{"name", "a name that is far too long", false}, {"name", "   ", false},
		{"task", "T3", true}, {"task", "sign-up page", true}, {"task", "-x", false}, {"name", "-x", false}, {"name", "x -y", true}, {"task", "a\tb\x00", false},
		{"agent", "claude", true}, {"agent", "Claude", false}, {"agent", "-claude", false},
		{"model", "opus[1m]", true}, {"model", "--dangerously-skip-permissions", false}, {"model", "-m", false}, {"model", "a\nb", false},
		{"slug", "url-parser", true}, {"slug", "url parser", false},
		{"number", "540", true}, {"number", "-5", false}, {"number", "1e3", false},
		{"attempt", "T3.2", true}, {"attempt", "T3", false}, {"attempt", "T3.2.1", false}, {"attempt", "../T3.2", false},
		{"pane", "w1:p3", true}, {"pane", "w1:pA", true}, {"pane", "-w1", false}, {"pane", "w1 p3", false},
		{"path", "src/url/**", true}, {"path", "src/new/file.go", true}, {"path", "in/url", true}, {"path", ".", true},
		{"path", "/etc/passwd", false}, {"path", `\share`, false}, {"path", `c:\x`, false}, {"path", "../x", false},
		{"path", "src/../../x", false}, {"path", `src\..\..\x`, false}, {"path", "-rf", false}, {"path", "out/x", false},
		{"path", "out", false}, {"path", "a\nb", false}, {"path", "", false}, {"path", "//", false},
		{"text", "", true}, {"text", "any\x1b[31m thing\n-- at all", true}, {"text", "\xff", false},
		{"nothing", "x", false},
	} {
		if err := Valid(tc.kind, tc.v, root); (err == nil) != tc.ok {
			t.Errorf("%s %q: %v, want ok %v", tc.kind, tc.v, err, tc.ok)
		} else if err != nil && Plain(err.Error()) != err.Error() {
			t.Errorf("%s %q: the error itself is not plain: %q", tc.kind, tc.v, err)
		}
	}
}

// Every value rule of the shared table has a rule here, under its own name.
func TestRulesCoverTheTable(t *testing.T) {
	for _, r := range contract.ValueRules {
		if got, ok := rules()[r.Value]; !ok || got.ValueRule != r {
			t.Errorf("the rule for %s is not the table's", r.Value)
		}
	}
}

func TestVersionIsFast(t *testing.T) {
	k, _ := world(t)
	start := time.Now()
	if _, _, exit := call(t, k, lead, "version"); exit != 0 {
		t.Fatal(exit)
	}
	if d := time.Since(start); d > 30*time.Millisecond {
		t.Errorf("version took %v", d)
	}
}

// FuzzValid feeds the validator and the printer hostile strings. Whatever
// passes a rule must have the properties the rule is there to give, checked
// here a second way, by hand.
func FuzzValid(f *testing.F) {
	root := f.TempDir()
	base, _ := filepath.EvalSymlinks(root)
	os.MkdirAll(filepath.Join(root, "src"), 0o755)
	os.Symlink(f.TempDir(), filepath.Join(root, "out"))
	os.Symlink("..", filepath.Join(root, "src", "up"))
	for _, seed := range []string{"T3", "-rf", "--upload-pack=x", "../../etc", "out/x", "src/up/up/x", "a\nb", "\x1b]0;t\a", "é\u202e", "a b c d", "$(x)", "`x`", ";x", "T3.2", "w1:p3", "src/**", `c:\x`, "\xff\xfe", ""} {
		f.Add(seed)
	}
	word := func(v string, first, rest string) bool {
		for i, r := range v {
			if !strings.ContainsRune(rest, r) || i == 0 && !strings.ContainsRune(first, r) {
				return false
			}
		}
		return v != ""
	}
	const lower, digits = "abcdefghijklmnopqrstuvwxyz", "0123456789"
	const letters = lower + "ABCDEFGHIJKLMNOPQRSTUVWXYZ" + digits
	oneLine := func(v string) bool {
		return utf8.ValidString(v) && !strings.ContainsFunc(v, func(r rune) bool {
			return unicode.IsControl(r) || r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 || r == 0x202e
		})
	}
	f.Fuzz(func(t *testing.T, v string) {
		for kind := range rules() {
			if Valid(kind, v, root) != nil {
				continue
			}
			ok := utf8.ValidString(v)
			switch kind {
			case "id":
				ok = word(v, letters+"_", letters+"_-") && len(v) <= 16
			case "title":
				ok = oneLine(v) && utf8.RuneCountInString(v) <= 80
			case "name":
				ok = oneLine(v) && utf8.RuneCountInString(v) <= 24 && len(strings.Fields(v)) >= 1 && len(strings.Fields(v)) <= 3
			case "agent":
				ok = word(v, lower, lower+digits+"_-") && len(v) <= 32
			case "slug":
				ok = word(v, lower+digits+"-", lower+digits+"-")
			case "number":
				ok = word(v, digits, digits) && len(v) <= 9
			case "pane":
				ok = word(v, letters, letters+":._-") && len(v) <= 64
			case "attempt":
				task, n, _ := strings.Cut(v, ".")
				ok = word(task, letters+"_", letters+"_-") && len(task) <= 16 && word(n, digits, digits)
			case "path":
				at := filepath.Join(base, v)
				for {
					if _, err := os.Lstat(at); err == nil {
						break
					}
					at = filepath.Dir(at)
				}
				at, err := filepath.EvalSymlinks(at)
				ok = oneLine(v) && err == nil && (at == base || strings.HasPrefix(at, base+string(filepath.Separator))) &&
					!filepath.IsAbs(v) && v[0] != '-' && !slices.Contains(strings.FieldsFunc(v, func(r rune) bool { return r == '/' || r == '\\' }), "..")
			}
			if !ok {
				t.Errorf("%q passes as a %s", v, kind)
			}
		}
		if Valid("task", v, root) == nil && Valid("id", v, root) != nil && Valid("name", v, root) != nil {
			t.Errorf("%q passes as a task and is neither an id nor a name", v)
		}
		plain := Plain(v)
		if !utf8.ValidString(plain) || Plain(plain) != plain || strings.ContainsFunc(plain, func(r rune) bool {
			return r != '\n' && r != '\t' && (unicode.IsControl(r) || r == 0x202e)
		}) {
			t.Errorf("Plain(%q) = %q can still steer a terminal", v, plain)
		}
		if err := Valid("id", v, root); err != nil && Plain(err.Error()) != err.Error() {
			t.Errorf("the error for %q is not plain: %q", v, err)
		}
	})
}

type logins struct {
	contract.NoPlatform
	dir string
}

func (l logins) Dirs() (contract.Dirs, error) { return contract.Dirs{State: l.dir}, nil }

// Every command leaves one line in the login's log: who, the run, the
// command, its form and the names of its flags, how it ended. Nothing a
// person or an agent typed is in it, and a hook leaves none.
func TestTheCommandLog(t *testing.T) {
	k, _ := world(t)
	dir := t.TempDir()
	k.Platform = logins{dir: dir}
	seen(k, "need")
	seen(k, "hook")
	call(t, k, lead, "need", "choice", "is the secret word plover?", "--options", "aye,nay", "--urgent")
	call(t, k, lead, "need", "choice", "x", "--no-such-flag") // refused before the command is taken up
	call(t, k, worker, "need", "todo", "x")
	call(t, k, worker, "hook", "gate")
	call(t, k, person, "version")
	seen(k, "status")
	call(t, k, lead, "status", "--sweep", "--quiet")
	call(t, k, lead, "status", "--run", "plugh") // no such run: its name is a word somebody typed
	files, _ := filepath.Glob(filepath.Join(dir, "log", "*.log"))
	if len(files) != 1 {
		t.Fatalf("the log folder holds %v", files)
	}
	data, _ := os.ReadFile(files[0])
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], " orchestrator r3 need choice --options --urgent exit=0 ") ||
		!strings.Contains(lines[1], " worker r3 need todo exit=5 ") || !strings.Contains(lines[2], " - status --run exit=4 ") {
		t.Fatalf("the log:\n%s", data)
	}
	for _, typed := range []string{"plover", "aye", "nay", "plugh"} {
		if strings.Contains(string(data), typed) {
			t.Errorf("the log holds %q, which somebody typed", typed)
		}
	}
	// Windows keeps no such bits: there the file is private by the profile folder it lies in.
	if info, err := os.Stat(files[0]); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("the log file: %v %v", info, err)
	}
}

// writing is the store of the tests with a record that can be changed, and
// raiser the one rule this package calls itself.
type writing struct{ *fakeStore }

func (s writing) Change(_, run string, fn func(*contract.State) error) error { return fn(s.runs[run]) }

type raiser struct{ contract.Rules }

func (raiser) Raise(s *contract.State, e contract.Event, _ time.Time) int {
	s.Inbox.Events = append(s.Inbox.Events, e)
	return len(s.Inbox.Events)
}

// Every command that changes the record, run by the person and ended well,
// leaves one human event that says what was done, to what, and where the
// command says it came from: typed with the flag in a pane no run records
// (which "done as you" counts), or a pane of ours, the page or a phone.
// Nothing is written for the lead agent, for a command that failed, or for
// one that is not on the list.
func TestWhatThePersonDoesIsRecorded(t *testing.T) {
	k, store := world(t)
	k.Store, k.Rules = writing{store}, raiser{}
	events := func() []contract.Event {
		e := store.runs["r3"].Inbox.Events
		store.runs["r3"].Inbox.Events = nil
		return e
	}
	events()
	places := []struct {
		scene scene
		where string
	}{
		{other, contract.WhereTyped}, {scene{key: "w1:p1"}, contract.WhereTyped},
		{scene{pane: "w9:p9", from: contract.WherePane}, contract.WherePane}, {scene{pane: "w9:p9", own: contract.WherePane}, contract.WherePane},
		{scene{from: contract.WherePage}, contract.WherePage}, {scene{from: contract.WherePhone}, contract.WherePhone},
	}
	for _, entry := range contract.Recorded {
		line := append(strings.Fields(entry), "T2", "a reason", "--human")
		seen(k, line[0])
		for _, p := range places {
			_, errw, exit := call(t, k, p.scene, line...)
			got := events()
			if exit != 0 || len(got) != 1 {
				t.Errorf("%s from %+v: exit %d, %q, %d events", entry, p.scene, exit, errw, len(got))
				continue
			}
			if e := got[0]; e.Kind != "human" || e.Data["what"] != entry || e.Data["where"] != p.where || e.Data["on"] != "T2" || !strings.Contains(e.Text, entry) {
				t.Errorf("%s from %+v left %+v", entry, p.scene, e)
			}
		}
		if call(t, k, lead, line[:len(line)-1]...); len(events()) != 0 {
			t.Errorf("%s by the lead agent is recorded as the person's", entry)
		}
		k.Handle(line[0], func(*contract.Call) (any, error) { return nil, errors.New("no") })
		if _, _, exit := call(t, k, other, line...); exit == 0 || len(events()) != 0 {
			t.Errorf("%s that failed is recorded, or ended with %d", entry, exit)
		}
	}
	for _, entry := range contract.Unrecorded {
		line := append(strings.Fields(entry), "--human")
		if entry == "help" {
			continue // it stands for itself and is never another's handler
		}
		seen(k, line[0])
		if _, errw, _ := call(t, k, other, line...); len(events()) != 0 {
			t.Errorf("%s is recorded as the person's doing: %q", entry, errw)
		}
	}
	seen(k, "need")
	call(t, k, other, "need", "todo", "a longer text, no id", "--human")
	if got := events(); len(got) != 1 || got[0].Data["what"] != "need todo" || got[0].Data["on"] != nil {
		t.Errorf("an argument that is no id is named in the event: %+v", got)
	}
}

// placed is the test's store with a project's folder of ours where the real one has it.
type placed struct{ *fakeStore }

func (placed) Dir(root, _ string) string { return filepath.Join(root, contract.ProjectDir) }

// listed is a platform that has a state folder, for the login's list of projects.
type listed struct {
	contract.NoPlatform
	state string
}

func (l listed) Dirs() (contract.Dirs, error) { return contract.Dirs{State: l.state}, nil }

// The program typed alone by a person at a terminal opens the window, and
// first sets up a folder that is a project by the look of it and has no
// folder of ours yet. Anybody else, and a person inside the window, gets
// the help list: nothing but a person's own typing sets a project up.
func TestTheProgramTypedAlone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		who   scene
		make  []string // what the folder holds
		calls string
	}{
		{"a repository not set up", person, []string{".git"}, "init open "},
		{"a folder with our settings file", person, []string{"whaleshark.toml"}, "init open "},
		{"a project that is set up", person, []string{".git", contract.ProjectDir}, "open "},
		{"a folder that is no project", person, nil, "open "},
		{"a folder of ours this login did not make", person, []string{".git", contract.ProjectDir, "foreign"}, "refused"},
		{"an agent's shell with no terminal", nobody, []string{".git"}, ""},
		{"a tab of the window", scene{pane: "w9:p9", terminal: true}, []string{".git"}, ""},
		{"a shortcut's command", scene{key: "w9:p9", terminal: true}, []string{".git"}, ""},
		{"a worker", scene{attempt: "T3.1", terminal: true}, []string{".git"}, ""},
	} {
		dir := t.TempDir()
		t.Chdir(dir)
		for _, name := range tc.make {
			if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		k, store := world(t)
		k.Store, k.Platform = placed{store}, listed{state: t.TempDir()}
		if here, _ := os.Getwd(); slices.Contains(tc.make, contract.ProjectDir) && !slices.Contains(tc.make, "foreign") {
			list, _ := json.Marshal(contract.Projects{Versioned: contract.Versioned{Version: contract.FileVersion}, Roots: []string{here}})
			os.WriteFile(filepath.Join(k.Platform.(listed).state, "projects.json"), list, 0o600)
		}
		calls := ""
		for _, name := range []string{"init", "open"} {
			k.Handle(name, func(c *contract.Call) (any, error) {
				if calls += name + " "; c.Caller.Kind != contract.Human || c.Caller.Where != contract.WhereTyped {
					t.Errorf("%s: %s was run as %+v", tc.name, name, c.Caller)
				}
				return nil, nil
			})
		}
		out, errw, exit := call(t, k, tc.who)
		if tc.calls == "refused" {
			if calls != "" || exit != contract.ExitEnv || !strings.Contains(errw, "init --adopt") {
				t.Errorf("%s: ran %q, exit %d, said\n%s", tc.name, calls, exit, errw)
			}
		} else if calls != tc.calls || exit != 0 || (calls == "") != strings.Contains(out, "the guide that matches") {
			t.Errorf("%s: ran %q, exit %d, printed\n%s", tc.name, calls, exit, out)
		}
	}
	// The keeper is the login's and no project's: it starts in a folder
	// whose record another login made, and where the record cannot be read.
	dir := t.TempDir()
	t.Chdir(dir)
	os.Mkdir(filepath.Join(dir, contract.ProjectDir), 0o700)
	k, store := world(t)
	k.Store, k.Platform, store.broken = placed{store}, listed{state: t.TempDir()}, errors.New("torn")
	for _, who := range []scene{person, nobody, other} {
		kept := seen(k, "engine")
		if _, errw, exit := call(t, k, who, "engine", "run"); exit != 0 || kept.Command == nil {
			t.Errorf("engine run in a folder another login set up: exit %d, said %q", exit, errw)
		}
	}
	if _, errw, exit := call(t, k, person, "status"); exit != contract.ExitEnv || !strings.Contains(errw, "init --adopt") {
		t.Errorf("status in the same folder: exit %d, said %q", exit, errw)
	}

	// What init refuses stops it: the window is not opened over a refusal.
	dir = t.TempDir()
	t.Chdir(dir)
	os.Mkdir(filepath.Join(dir, ".git"), 0o700)
	k, store = world(t)
	k.Store = placed{store}
	k.Handle("init", func(*contract.Call) (any, error) { return nil, refuse("shared_folder", "no") })
	opened := seen(k, "open")
	if _, errw, exit := call(t, k, person); exit != contract.ExitRefused || errw != "no\n" || opened.Command != nil {
		t.Errorf("a refused init: exit %d, %q, opened %v", exit, errw, opened.Command != nil)
	}
}
