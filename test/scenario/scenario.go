package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/term/termtest"
)

// Pane is the program of a pane, "fleet" or "actions": it draws on the
// terminal it is handed and returns when that terminal's input ends.
type Pane func(p *Project, name string, t *term.Term)

// patience is how long a command may run that was not left running with "&",
// and how long a pane is given to show something. apart is the time a person
// leaves before a key or a click: a pane takes keys that follow each other
// faster for typing meant elsewhere, and refuses a click on a row that has
// just moved.
const (
	patience = 20 * time.Second
	apart    = 450 * time.Millisecond
)

type screen struct {
	*termtest.Screen
	rows int
	left chan struct{}
}

type running struct {
	who string
	cmd *exec.Cmd
}

type runner struct {
	t       testing.TB
	p       *Project
	pane    Pane
	at      string // the file and line of the step being run
	ack     string // the delivery id the last wait printed
	left    []running
	screens map[string]*screen
	shown   *screen // the pane the pane steps act on
}

// Run plays one scenario file from its first line to its last and fails the
// test at the first step that does not go as written. pane may be nil for a
// scenario that opens no pane. The project is returned for what a test wants
// to look at beyond the file's own expectations.
func Run(t testing.TB, path string, pane Pane) *Project {
	t.Helper()
	file, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	type step struct{ at, kind, rest string }
	var steps []step
	var fixture *testkit.Fixture
	var asserts []step
	scripts := map[string]string{}
	for n, line := range strings.Split(string(file), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		s := step{at: fmt.Sprintf("%s:%d", filepath.Base(path), n+1)}
		s.kind, s.rest, _ = strings.Cut(line, " ")
		s.rest = strings.TrimSpace(s.rest)
		switch s.kind {
		case "fixture":
			if fixture, err = testkit.Load(s.rest); err != nil {
				t.Fatalf("%s: %v", s.at, err)
			}
		case "agent":
			id, script, _ := strings.Cut(s.rest, ":")
			for _, part := range strings.Split(script, " | ") {
				if w := words(part); len(w) > 0 && testkit.AgentSteps[w[0]] == "" {
					t.Fatalf("%s: %q is not a step of a fake agent", s.at, w[0])
				}
			}
			scripts[id] = strings.TrimSpace(script)
		case "assert:":
			asserts = append(asserts, s)
		default:
			if testkit.Lines[s.kind] == "" {
				t.Fatalf("%s: %q does not start a line of a scenario", s.at, s.kind)
			}
			steps = append(steps, s)
		}
	}

	r := &runner{t: t, p: Prepare(t, fixture, scripts), pane: pane, screens: map[string]*screen{}}
	t.Cleanup(func() {
		for name := range r.screens {
			r.shut(name)
		}
		r.kill(func(running) bool { return true })
	})
	for _, s := range append(steps, asserts...) {
		r.at = s.at
		r.step(s.kind, s.rest)
	}
	return r.p
}

func (r *runner) fail(format string, a ...any) {
	r.t.Helper()
	r.t.Fatalf("%s: %s", r.at, fmt.Sprintf(format, a...))
}

func (r *runner) step(kind, rest string) {
	r.t.Helper()
	switch kind {
	case "orch:":
		r.command(Orch, rest)
	case "task":
		r.command(Orch, "task add "+rest)
	case "file":
		name, body, _ := strings.Cut(rest, ":")
		body = strings.ReplaceAll(strings.TrimSpace(body), " | ", "\n") + "\n"
		r.p.must(os.WriteFile(filepath.Join(r.p.Root, name), []byte(body), 0o600))
	case "human:":
		if button, ok := strings.CutPrefix(rest, "page:"); ok {
			r.command(Page, button)
		} else {
			r.command(Human, rest)
		}
	case "as-unbound:":
		r.command(Unbound, rest)
	case "as-worker":
		id, line, _ := strings.Cut(rest, ":")
		r.command(strings.TrimSpace(id), line)
	case "clock":
		by, err := time.ParseDuration(strings.TrimPrefix(rest, "+"))
		if err != nil {
			r.fail("%v", err)
		}
		r.p.Clock(by)
	case "kill-wait":
		r.kill(func(c running) bool { return slices.Contains(c.cmd.Args, "wait") })
	case "kill-orchestrator":
		r.kill(func(c running) bool { return c.who == Orch })
		r.p.Herdr.Push(testkit.PushGone, r.p.Lead)
	case "restart-terminals":
		r.p.Herdr.Restart()
		r.p.restarted = true
	case "term-event", "term-drop":
		w := words(rest)
		if len(w) != 2 || !slices.Contains(pushes, w[0]) {
			r.fail("it takes one of %s, then a task, an attempt or lead", strings.Join(pushes, ", "))
		}
		if kind == "term-event" {
			r.p.Herdr.Push(w[0], r.where(w[1]))
		} else {
			r.p.Herdr.Drop(w[0], r.where(w[1]))
		}
	case "notices":
		r.t.Setenv(contract.EnvNotices, rest)
	case "pane":
		r.open(rest)
	case "click":
		s := r.on()
		if !s.shows(text(rest)) {
			r.fail("the pane does not show %q:\n%s", text(rest), s.all())
		}
		s.Still(apart)
		if !s.ClickText(text(rest)) {
			r.fail("the pane does not show %q:\n%s", text(rest), s.all())
		}
	case "key":
		time.Sleep(apart)
		r.on().Key(rest)
	case "type":
		time.Sleep(apart)
		r.on().Type(text(rest))
	case "expect-row":
		if s := r.on(); !s.shows(text(rest)) {
			r.fail("no row shows %q:\n%s", text(rest), s.all())
		}
	case "expect-last-line":
		s := r.on()
		for end := time.Now().Add(2 * time.Second); !strings.Contains(s.Row(s.rows-1), text(rest)); time.Sleep(2 * time.Millisecond) {
			if time.Now().After(end) {
				r.fail("the last line does not show %q:\n%s", text(rest), s.all())
			}
		}
	case "assert:", "await:":
		// An await line gives the record time to become so: a fake agent
		// reports when it gets there, not when the scenario's next line runs.
		for _, fact := range strings.Split(rest, ";") {
			not := r.assert(strings.TrimSpace(fact))
			for end := time.Now().Add(patience); not != "" && kind == "await:" && time.Now().Before(end); not = r.assert(strings.TrimSpace(fact)) {
				time.Sleep(20 * time.Millisecond)
			}
			if not != "" {
				r.fail("%s", not)
			}
		}
	}
}

// pushes is what a term-event or term-drop line may name.
var pushes = []string{contract.StatusWorking, contract.StatusIdle, contract.StatusDone, contract.StatusBlocked,
	contract.StatusUnknown, testkit.PushGone, testkit.PushClosed, testkit.PushFocused}

// words splits a line at spaces; what stands between two double or two
// single quotes is one word, or part of one.
func words(line string) []string {
	var out []string
	var word strings.Builder
	var quote rune
	open := false
	for _, c := range line {
		switch {
		case c == quote:
			quote = 0
		case quote != 0:
			word.WriteRune(c)
		case c == '"' || c == '\'':
			quote, open = c, true
		case c != ' ' && c != '\t':
			word.WriteRune(c)
			open = true
		case open:
			out, open = append(out, word.String()), false
			word.Reset()
		}
	}
	if open {
		out = append(out, word.String())
	}
	return out
}

// text is what a pane step names, with or without quotes round it.
func text(rest string) string {
	if len(rest) > 1 && rest[0] == '"' && rest[len(rest)-1] == '"' {
		return rest[1 : len(rest)-1]
	}
	return rest
}

var deliveryID = regexp.MustCompile(`\bd[0-9]+\b`)

// command runs one real command as a caller and holds it to what follows
// "-> expect": "exit N" first for another exit code than 0, then words that
// what it printed must contain. A line that ends in "&" is left running.
func (r *runner) command(who, line string) {
	r.t.Helper()
	want := ""
	if i := strings.LastIndex(line, "-> expect"); i >= 0 {
		line, want = line[:i], line[i+len("-> expect"):]
	}
	args := words(line)
	leave := len(args) > 1 && args[len(args)-1] == "&"
	if leave {
		args = args[:len(args)-1]
	}
	if len(args) == 0 {
		r.fail("no command on this line")
	}
	if i := slices.Index(args, "--ack"); i >= 0 && i+1 < len(args) && args[i+1] == "last" {
		if r.ack == "" {
			r.fail("--ack last, but no wait before this line printed a delivery id")
		}
		args[i+1] = r.ack
	}
	cmd := r.p.Command(who, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		r.fail("%v", err)
	}
	if leave {
		r.left = append(r.left, running{who, cmd})
		return
	}
	late := time.AfterFunc(patience, func() { cmd.Process.Kill() })
	cmd.Wait()
	if !late.Stop() {
		r.fail("still running after %v; end the line with & to leave it running\n%s", patience, &out)
	}
	if id := deliveryID.Find(out.Bytes()); args[0] == "wait" && id != nil {
		r.ack = string(id)
	}
	exit, must := 0, words(want)
	if len(must) > 1 && must[0] == "exit" {
		exit, _ = strconv.Atoi(must[1])
		must = must[2:]
	}
	if got := cmd.ProcessState.ExitCode(); got != exit {
		r.fail("exit %d, expected %d\n%s", got, exit, &out)
	}
	for _, w := range must {
		if !bytes.Contains(out.Bytes(), []byte(w)) {
			r.fail("expected %q in what it printed:\n%s", w, &out)
		}
	}
}

// kill ends the commands left running that match, as a closed tab would.
func (r *runner) kill(which func(running) bool) {
	r.left = slices.DeleteFunc(r.left, func(c running) bool {
		if which(c) {
			c.cmd.Process.Kill()
			c.cmd.Wait()
		}
		return which(c)
	})
}

// where is the pane of a task's latest attempt, of an attempt, or of the lead agent.
func (r *runner) where(name string) string {
	r.t.Helper()
	if name == "lead" {
		return r.p.Lead
	}
	s, _ := r.p.Record()
	if t := s.Tasks[name]; t != nil && len(t.Attempts) > 0 {
		name = t.Attempts[len(t.Attempts)-1]
	}
	if a := s.Attempts[name]; a != nil {
		return a.Place.Pane
	}
	r.fail("the record has no task and no attempt %s", name)
	return ""
}

// open starts a pane on a pretended terminal, in place of one of that name
// that is open already; with no size it only turns the pane steps to it.
func (r *runner) open(rest string) {
	r.t.Helper()
	name, size, _ := strings.Cut(rest, ":")
	if size = strings.TrimSpace(size); size == "" && r.screens[name] != nil {
		r.shown = r.screens[name]
		return
	}
	var w, h int
	if n, _ := fmt.Sscanf(size, "%dx%d", &w, &h); n != 2 || name != "fleet" && name != "actions" {
		r.fail("a pane line is: %s", testkit.Lines["pane"])
	}
	if r.pane == nil {
		r.fail("the test gave Run no pane program")
	}
	r.shut(name)
	s := &screen{Screen: termtest.New(w, h), rows: h, left: make(chan struct{})}
	go func() {
		defer close(s.left)
		r.pane(r.p, name, s.Term)
	}()
	r.screens[name], r.shown = s, s
}

func (r *runner) shut(name string) {
	s := r.screens[name]
	if s == nil {
		return
	}
	delete(r.screens, name)
	s.Close()
	select {
	case <-s.left:
	case <-time.After(patience):
		r.t.Errorf("the %s pane did not leave when its terminal was closed", name)
	}
}

func (r *runner) on() *screen {
	r.t.Helper()
	if r.shown == nil {
		r.fail("no pane is open; start one with: %s", testkit.Lines["pane"])
	}
	return r.shown
}

// shows gives a pane the runner's patience to show a text: what a button
// started may stand in line behind a command that takes its time.
func (s *screen) shows(text string) bool {
	for end := time.Now().Add(patience); !s.Wait(text); {
		if time.Now().After(end) {
			return false
		}
	}
	return true
}

func (s *screen) all() string {
	var b strings.Builder
	for y := range s.rows {
		fmt.Fprintf(&b, "%2d|%s\n", y, s.Row(y))
	}
	return b.String()
}

// assert holds the record to one fact: "<id> <field> <value>..." for a task,
// an attempt, a question or an item, with the fields as state.json names
// them; "attempts <id> <state>, ..."; "no event lost"; "no event twice". It
// answers with what is not so, or with nothing.
func (r *runner) assert(fact string) string {
	r.t.Helper()
	s, _ := r.p.Record()
	var raw struct{ Tasks, Attempts, Questions map[string]map[string]any }
	data, _ := json.Marshal(s)
	json.Unmarshal(data, &raw)
	w := words(fact)
	switch {
	case fact == "no event lost" || fact == "no event twice":
		return r.events(s, fact)
	case len(w) > 0 && w[0] == "attempts":
		for _, pair := range strings.Split(strings.TrimPrefix(fact, "attempts"), ",") {
			if p := words(pair); len(p) != 2 || raw.Attempts[p[0]]["state"] != p[1] {
				return fmt.Sprintf("not so: attempt %s (the record says %v)", strings.TrimSpace(pair), raw.Attempts[p[0]]["state"])
			}
		}
	case len(w) >= 3 && len(w)%2 == 1:
		var thing map[string]any
		for _, kind := range []map[string]map[string]any{raw.Tasks, raw.Attempts, raw.Questions} {
			if kind[w[0]] != nil {
				thing = kind[w[0]]
			}
		}
		for i := 1; i < len(w); i += 2 {
			if got := fmt.Sprint(thing[w[i]]); thing == nil || got != w[i+1] {
				return fmt.Sprintf("not so: %s (the record says %s %s)", fact, w[i], got)
			}
		}
	default:
		return "not a fact the runner knows: " + fact
	}
	return ""
}

// events counts where every number given out since the scenario began is
// now: an event still in the inbox, one acknowledged into history.jsonl, or
// a message to a worker, which takes its number from the same counter.
func (r *runner) events(s *contract.State, fact string) string {
	r.t.Helper()
	seen := map[int]int{}
	for _, e := range s.Inbox.Events {
		seen[e.Seq]++
	}
	history, err := r.p.Kit.Store.History(r.p.Root, s.Run.ID)
	r.p.must(err)
	for _, e := range history {
		if slices.Contains(contract.EventKinds, e.Kind) {
			seen[e.Seq]++
		}
	}
	for _, a := range s.Attempts {
		for _, m := range a.Mail {
			seen[m.Seq]++
		}
	}
	for seq := r.p.seq + 1; seq <= s.Counters.Seq; seq++ {
		if n := seen[seq]; n == 0 && fact == "no event lost" || n > 1 && fact == "no event twice" {
			return fmt.Sprintf("not so: %s (number %d is there %d times)", fact, seq, n)
		}
	}
	return ""
}
