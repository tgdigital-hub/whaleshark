package watch

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/screen"
	"github.com/tgdigital-hub/whaleshark/test/corpus"
)

const (
	idle    = contract.StatusIdle
	atWork  = contract.StatusWorking
	blocked = contract.StatusBlocked
)

// wait is long enough for anything a pane's program does here, on a slow
// machine and under the race detector, where each hook takes a second.
const wait = 30 * time.Second

// shows waits until the pane shows a text: a state can be known before the
// lines printed just ahead of it are read.
func shows(t *testing.T, p *pane, text string) {
	t.Helper()
	for end := time.Now().Add(wait); !strings.Contains(p.Text(), text); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("the pane does not show %q:\n%s", text, p.Text())
		}
	}
}

func pasted(text string) string { return pasteOpen + text + pasteClose + "\r" }

// typedAs reports whether what was typed is the text, once, and its Enter:
// one, or more where the agent's hooks were slow to say that it had started.
func typedAs(got, text string) bool {
	return strings.TrimRight(got, "\r")+"\r" == pasted(text) && strings.HasSuffix(got, "\r")
}

// after is the states from the first one of a kind on.
func after(all []string, first string) []string {
	return all[max(slices.Index(all, first), 0):]
}

// The fake agent, with its hooks, in a real terminal: every state there
// is, each reached by the source that should give it.
func TestTheFakeAgentThroughEveryState(t *testing.T) {
	s := listen(t)
	p := open(t, s.env("agent"), os.Args[0], "hooks")
	w := s.watch(t, p, "claude")
	if err := w.Ready(wait); err != nil {
		t.Fatal(err)
	}
	if st := s.reach(t, idle, wait); st != (State{"claude", "the name", "session-one", "/work/here", idle}) {
		t.Fatalf("ready as %+v", st)
	}

	// A turn with no step.
	if err := w.Prompt("say\r\nhello\x1b[201~\x07", wait); err != nil {
		t.Fatal(err)
	}
	if got := p.wasTyped(); !typedAs(got, "say\nhello[201~") {
		t.Fatalf("typed %q", got)
	}
	s.reach(t, idle, wait)

	// A step that is asked about and allowed. While the question is open
	// nothing is typed; the agent is at work again as soon as its screen
	// says so, long before the hook that follows the step.
	if err := w.Prompt("a step", wait); err != nil {
		t.Fatal(err)
	}
	s.reach(t, blocked, wait)
	typed := p.wasTyped()
	if err := w.Prompt("more", time.Second); !errors.Is(err, contract.ErrAgentBlocked) {
		t.Errorf("a prompt at a question: %v", err)
	}
	if err := w.Point(contract.PointMail, ""); !errors.Is(err, contract.ErrAgentBlocked) {
		t.Errorf("a pointer at a question: %v", err)
	}
	if got := p.wasTyped(); got != typed {
		t.Fatalf("typed at a question: %q", got[len(typed):])
	}
	p.press("\r")
	s.reach(t, atWork, wait)
	if slices.Contains(s.heard(), "PostToolUse") {
		t.Error("the hook after the step had come: the screen was not what ended the question")
	}
	s.reach(t, idle, wait)

	// A step that is refused: no hook says so, and the screen at rest does.
	if err := w.Prompt("another step", wait); err != nil {
		t.Fatal(err)
	}
	s.reach(t, blocked, wait)
	p.press("n")
	s.reach(t, idle, wait)

	// A pointer is typed like a prompt.
	typed = p.wasTyped()
	if err := w.Point(contract.PointAnswered, "Q3"); err != nil {
		t.Fatal(err)
	}
	if got := p.wasTyped()[len(typed):]; !typedAs(got, "Your question Q3 was answered. Run: whaleshark ask --resume Q3") {
		t.Errorf("the pointer was typed as %q", got)
	}
	s.reach(t, atWork, wait)
	s.reach(t, idle, wait)
	// The two events that mean nothing follow every turn; give the last time to arrive.
	for end := time.Now().Add(wait); strings.Count(strings.Join(s.heard(), " "), "Notification") < 3 && time.Now().Before(end); {
		time.Sleep(20 * time.Millisecond)
	}

	want := []string{idle, atWork, idle, atWork, blocked, atWork, idle, atWork, blocked, idle, atWork, idle}
	if got := after(s.statuses(), idle); !slices.Equal(got, want) {
		t.Errorf("the states were\n%v, want\n%v\nhooks: %v", got, want, s.heard())
	}

	// The agent's own end: its session is over, and then its terminal.
	p.press("/exit\r")
	select {
	case <-p.gone:
	case <-time.After(wait):
		t.Fatal("the agent did not end")
	}
	if h := s.heard(); h[len(h)-1] != "SessionEnd" {
		t.Errorf("the last hook was %s", h[len(h)-1])
	}
	s.mu.Lock()
	last := s.states[len(s.states)-1]
	s.mu.Unlock()
	if last.Session != "" {
		t.Errorf("after its end the agent still has the session %q", last.Session)
	}
	w.Close()
	if err := w.Prompt("late", time.Second); !errors.Is(err, contract.ErrNoPane) {
		t.Errorf("a prompt for an agent that is gone: %v", err)
	}
}

// The question about a folder comes before any hook: only the screen sees it.
func TestAnAgentStoppedAtTheTrustQuestion(t *testing.T) {
	s := listen(t)
	p := open(t, s.env("agent"), os.Args[0], "trust")
	w := s.watch(t, p, "claude")
	if err := w.Ready(wait); !errors.Is(err, contract.ErrAgentNotReady) {
		t.Fatalf("ready: %v", err)
	}
	if err := w.Prompt("hello", time.Second); !errors.Is(err, contract.ErrAgentBlocked) {
		t.Errorf("a prompt: %v", err)
	}
	if got := p.wasTyped(); got != "" {
		t.Errorf("typed at the question: %q", got)
	}
}

// With its hooks switched off the same agent is followed by its screen alone.
func TestTheScreenAlone(t *testing.T) {
	s := listen(t)
	p := open(t, s.env("agent"), os.Args[0], "screen")
	w := s.watch(t, p, "claude")
	if err := w.Ready(wait); err != nil {
		t.Fatal(err)
	}
	if err := w.Prompt("a step", wait); err != nil {
		t.Fatal(err)
	}
	s.reach(t, blocked, wait)
	if err := w.Prompt("more", time.Second); !errors.Is(err, contract.ErrAgentBlocked) {
		t.Errorf("a prompt at a question: %v", err)
	}
	p.press("\r")
	s.reach(t, atWork, wait)
	s.reach(t, idle, wait)
	if got, want := p.wasTyped(), pasted("a step"); got != want {
		t.Errorf("typed %q, want %q", got, want)
	}
	if len(s.heard()) != 0 {
		t.Errorf("hooks ran: %v", s.heard())
	}
}

// A kind nothing is known of is busy or quiet, and is called neither
// working nor idle.
func TestAKindNothingIsKnownOf(t *testing.T) {
	s := listen(t)
	p := open(t, s.env("agent"), os.Args[0], "other")
	w := s.watch(t, p, "other")
	if err := w.Ready(wait); err != nil {
		t.Fatal(err)
	}
	s.reach(t, Quiet, time.Second)
	if err := w.Prompt("hello", wait); err != nil {
		t.Fatal(err)
	}
	s.reach(t, Quiet, wait)
	if got, want := s.statuses(), []string{contract.StatusUnknown, Busy, Quiet, Busy, Quiet}; !slices.Equal(after(got, Busy), want[1:]) {
		t.Errorf("the states were %v, want %v", got, want)
	}
}

// Codex is known by its hooks alone: they give it an exact state and its
// session, and no word of its screen is read.
func TestCodexByItsHooks(t *testing.T) {
	s := listen(t)
	p := open(t, s.env("agent"), os.Args[0], "hooks")
	w := s.watch(t, p, "codex")
	if err := w.Ready(wait); err != nil {
		t.Fatal(err)
	}
	if st := s.reach(t, idle, wait); st.Agent != "codex" || st.Session != "session-one" {
		t.Fatalf("ready as %+v", st)
	}
	if err := w.Prompt("say hello", wait); err != nil {
		t.Fatal(err)
	}
	// A state is given out by the Watcher's own goroutine, a moment after
	// Prompt has returned on it: the turn is over when the agent has been
	// given out at work and then at rest, not when the last state is idle,
	// which it still is in that moment.
	over := func() bool {
		got := s.statuses()
		i := slices.Index(got, contract.StatusWorking)
		return i >= 0 && slices.Contains(got[i:], idle)
	}
	for end := time.Now().Add(wait); !over(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("the turn did not end: the states were %v, from the hooks %v", s.statuses(), s.heard())
		}
	}
	if got := after(s.statuses(), idle); slices.Contains(got, Busy) || slices.Contains(got, Quiet) {
		t.Errorf("once its hooks spoke, the states were %v", got)
	}
}

// A shell is at its prompt or runs a command, by who is in front in its
// terminal; nothing is ever typed at it. An agent started in it by hand is
// known by its program's name, for as long as it is in front.
func TestAShell(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh here")
	}
	s := listen(t)
	agent := filepath.Join(t.TempDir(), "claude")
	self, err := os.ReadFile(os.Args[0])
	if err == nil {
		err = os.WriteFile(agent, self, 0o700)
	}
	if err != nil {
		t.Fatal(err)
	}
	p := open(t, s.env("agent"), "/bin/sh")
	w := s.watch(t, p, "")
	s.reach(t, idle, wait)
	p.press("sleep 2\r")
	s.reach(t, atWork, 1500*time.Millisecond)
	if err := w.Prompt("echo typed", time.Second); !errors.Is(err, contract.ErrAgentNotReady) {
		t.Errorf("a prompt at a shell: %v", err)
	}
	if st := s.reach(t, idle, wait); st.Agent != "" {
		t.Errorf("the shell is called %q", st.Agent)
	}

	p.press(agent + " screen\r")
	for end := time.Now().Add(wait); ; time.Sleep(10 * time.Millisecond) {
		if st := s.reach(t, idle, wait); st.Agent == "claude" {
			break
		} else if time.Now().After(end) {
			t.Fatalf("the agent in the shell was not seen: %+v", st)
		}
	}
	if err := w.Prompt("hello", wait); err != nil {
		t.Errorf("a prompt at the agent in the shell: %v", err)
	}
	s.reach(t, idle, wait)
	p.press("/exit\r")
	for end := time.Now().Add(wait); ; time.Sleep(10 * time.Millisecond) {
		if st := s.reach(t, idle, wait); st.Agent == "" {
			break
		} else if time.Now().After(end) {
			t.Fatal("the shell did not come back")
		}
	}
	if got, want := p.wasTyped(), pasted("hello"); got != want {
		t.Errorf("typed %q, want %q", got, want)
	}

	// An agent typed for in the shell's first instant, before the pane has
	// once been quiet, is the agent all the same and not the pane's own program.
	// (Where /bin/sh hands over to another shell at its start, as on a Mac,
	// the pane's own program is not the one in front, and zsh is asked.)
	shell := []string{"/bin/sh"}
	if _, err := os.Stat("/bin/zsh"); err == nil {
		shell = []string{"/bin/zsh", "-f"}
	}
	fast := listen(t)
	q := open(t, fast.env("agent"), shell...)
	fast.watch(t, q, "")
	time.Sleep(300 * time.Millisecond)
	q.press(agent + " screen\r")
	for end := time.Now().Add(wait); ; time.Sleep(10 * time.Millisecond) {
		if st := fast.reach(t, idle, wait); st.Agent == "claude" {
			break
		} else if time.Now().After(end) {
			t.Fatalf("the agent started at once in a new shell was not seen: %+v", st)
		}
	}
}

// still is a pane that shows what a test puts there.
type still struct {
	mu     sync.Mutex
	text   string
	n      uint64
	marked bool
	typed  []string
	onType func()
}

func (p *still) show(text string) {
	p.mu.Lock()
	p.text, p.n = text, p.n+1
	p.mu.Unlock()
}

func (p *still) Front() (string, error) { return "", contract.ErrCannotTell }

func (p *still) Text() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.text
}

func (p *still) Count() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n
}

func (p *still) Paste() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.marked
}

func (p *still) Type(b []byte) error {
	p.mu.Lock()
	p.typed = append(p.typed, string(b))
	on := p.onType
	p.mu.Unlock()
	if on != nil {
		on()
	}
	return nil
}

const askScreen = "Do you want to go on?\n> Yes\n  No\nEsc to cancel"

func TestWhatIsTyped(t *testing.T) {
	p := &still{}
	w := New(p, "claude", "", func(State) {})
	defer w.Close()
	p.show("? for shortcuts")

	// Without marks a line break would be an Enter, and no control character is typed either way.
	if err := w.Prompt("one\ntwo\x03\x1b[201~\tend\u009b", 10*time.Millisecond); !errors.Is(err, contract.ErrPromptStalled) {
		t.Errorf("an agent that did not start on its prompt: %v", err)
	}
	p.marked = true
	w.Point(contract.PointEvents, "12")
	if want := []string{"one two[201~\tend", "\r", pasteOpen + "whaleshark: 12 events waiting. Run: whaleshark wait" + pasteClose, "\r"}; !slices.Equal(p.typed, want) {
		t.Errorf("typed %q, want %q", p.typed, want)
	}

	// An agent whose screen shows nothing its kind's words cover has not
	// been seen ready: it may be at a question of a kind nobody recorded.
	p.typed = nil
	p.show("Choose a style\n> dark\n  light")
	if err := w.Prompt("hello", time.Second); !errors.Is(err, contract.ErrAgentNotReady) || len(p.typed) != 0 {
		t.Errorf("at a screen nobody knows: %v, typed %q", err, p.typed)
	}
	p.show("? for shortcuts")

	// Only the fixed lines, and in them only an id or a count.
	p.typed = nil
	for _, arg := range []string{"1; rm -rf x", "a b", "$(x)", "é\n", strings.Repeat("1", 65)} {
		if err := w.Point(contract.PointEvents, arg); err == nil {
			t.Errorf("a pointer took %q", arg)
		}
	}
	if err := w.Point("run this", ""); err == nil {
		t.Error("a line that is no pointer was taken")
	}

	// A question that comes up between the text and its Enter gets no Enter.
	p.onType = func() { p.onType = nil; p.show(askScreen) }
	if err := w.Prompt("hello", time.Second); !errors.Is(err, contract.ErrAgentBlocked) {
		t.Errorf("a question after the text: %v", err)
	}
	if want := []string{pasteOpen + "hello" + pasteClose}; !slices.Equal(p.typed, want) {
		t.Errorf("typed %q, want %q", p.typed, want)
	}
}

// Whatever the hooks say and in whatever order, nothing is typed while the
// screen shows a question, and the two events that mean nothing move nothing.
func TestAQuestionOutweighsEveryHook(t *testing.T) {
	p := &still{}
	w := New(p, "claude", "", func(State) {})
	defer w.Close()
	names := []string{"SubagentStop", "Notification", "Elsewhere"}
	for name := range events {
		names = append(names, name)
	}
	slices.Sort(names)
	for i := range 400 {
		name := names[(i*7+i/5)%len(names)]
		if p.show(askScreen); i%3 == 0 {
			p.show("? for shortcuts")
		}
		before := w.look(time.Now())
		w.Hook(name, "s", "")
		st := w.look(time.Now())
		if _, means := events[name]; !means && st != before {
			t.Fatalf("%s moved %+v to %+v", name, before, st)
		}
		if i%3 == 0 {
			continue
		}
		if err := w.Prompt("x", time.Millisecond); st.Status != blocked || !errors.Is(err, contract.ErrAgentBlocked) || len(p.typed) > 0 {
			t.Fatalf("after %s at a question: %+v, %v, typed %q", name, st, err, p.typed)
		}
	}
}

// The words of the table are held against every recording of the real
// agent, where the folder of those recordings is given: the question is
// seen in each, with work before and after it, and none at the end.
func TestTheWordsAgainstTheRecordings(t *testing.T) {
	if os.Getenv(testkit.EnvCorpus) == "" {
		t.Skip("the recordings of the real agent are private: set " + testkit.EnvCorpus)
	}
	for _, set := range corpus.Sets()[1:] {
		recs, err := corpus.All(set)
		if err != nil {
			t.Fatal(err)
		}
		for _, rec := range recs {
			if !strings.HasPrefix(rec.Name, "claude-") {
				continue
			}
			scr, saw := screen.New(rec.W, rec.H), []int{nothing}
			for _, out := range rec.Out {
				scr.Write(out)
				if now := kinds["claude"].read(scr.Picture().Text(), true); now != saw[len(saw)-1] {
					saw = append(saw, now)
				}
			}
			// One recording is of a session with no step in it.
			at, asks := slices.Index(saw, question), !strings.Contains(rec.Name, "silent")
			if !slices.Contains(saw, ready) || !slices.Contains(saw, working) || asks != (at > 0) ||
				asks && (saw[at-1] != working || saw[at+1] != working || slices.Contains(saw[at+1:], question)) {
				t.Errorf("%s: the screen showed %v", rec.Name, saw)
			}
		}
	}
}

// An agent on a loaded machine takes a paste in only after a while and
// drops an Enter that comes sooner: the Enter waits until the pane shows
// the text, however long that takes, and then one is enough.
func TestTheEnterWaitsForTheText(t *testing.T) {
	s := listen(t)
	p := open(t, s.env("agent"), os.Args[0], "slow")
	w := s.watch(t, p, "claude")
	if err := w.Ready(wait); err != nil {
		t.Fatal(err)
	}
	if err := w.Prompt("Read and follow a file", wait); err != nil {
		t.Fatalf("the prompt of an agent that is slow to take a paste in: %v\n%s", err, p.Text())
	}
	if got := p.wasTyped(); !typedAs(got, "Read and follow a file") {
		t.Errorf("typed %q", got)
	}
	shows(t, p, "did: Read and follow a file")
	s.reach(t, idle, wait)
	// A pointer goes the same way.
	if err := w.Point(contract.PointMail, ""); err != nil {
		t.Fatalf("a pointer for the same agent: %v", err)
	}
	shows(t, p, "did: whaleshark: a message is waiting")
	if got := strings.Count(p.wasTyped(), pasteOpen); got != 2 {
		t.Errorf("%d texts typed for two", got)
	}
}

// An agent that shows the text and still does not take its Enter gets
// Enter again, and the text once.
func TestEnterAgainForATextThatLiesThere(t *testing.T) {
	s := listen(t)
	p := open(t, s.env("agent"), os.Args[0], "deaf")
	w := s.watch(t, p, "claude")
	if err := w.Ready(wait); err != nil {
		t.Fatal(err)
	}
	if err := w.Prompt("hello", wait); err != nil {
		t.Fatalf("the prompt: %v\n%s", err, p.Text())
	}
	if got := p.wasTyped(); !typedAs(got, "hello") || strings.Count(got, "\r") < 3 {
		t.Errorf("typed %q", got)
	}
	shows(t, p, "did: hello")
}

// An agent that never takes the Enter: a few are pressed and no more, the
// text is typed once, and while it lies in the box no other is put beside it.
func TestATextNobodyTakes(t *testing.T) {
	p := &still{marked: true}
	w := New(p, "claude", "", func(State) {})
	defer w.Close()
	p.show("> \n? for shortcuts")
	w.Hook("SessionStart", "one", "")
	box := func() {
		p.mu.Lock()
		p.onType = func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			if last := p.typed[len(p.typed)-1]; last != "\r" {
				p.text, p.n = "> "+last+"\n? for shortcuts", p.n+1
			}
		}
		p.mu.Unlock()
	}
	box()
	begin := time.Now()
	if err := w.Prompt("hello", 7*time.Second); !errors.Is(err, contract.ErrPromptStalled) {
		t.Fatalf("a prompt nobody takes: %v", err)
	}
	if want := []string{pasteOpen + "hello" + pasteClose, "\r", "\r", "\r"}; !slices.Equal(p.typed, want) {
		t.Errorf("typed %q, want %q", p.typed, want)
	}
	if took := time.Since(begin); took > 12*time.Second {
		t.Errorf("it took %v", took)
	}
	// The text still lies there: it gets an Enter, and nothing is typed beside it.
	p.typed = nil
	if err := w.Point(contract.PointMail, ""); !errors.Is(err, contract.ErrPromptStalled) || !slices.Equal(p.typed, []string{"\r"}) {
		t.Errorf("a pointer while a text lies in the box: %v, typed %q", err, p.typed)
	}
	// The person has emptied the box: the next text is typed as any other,
	// and the hook that follows its Enter ends the wait.
	p.typed = nil
	p.show("> \n? for shortcuts")
	p.mu.Lock()
	p.onType = func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if last := p.typed[len(p.typed)-1]; last == "\r" {
			go w.Hook("UserPromptSubmit", "one", "")
		} else {
			p.text, p.n = "> "+last+"\n? for shortcuts", p.n+1
		}
	}
	p.mu.Unlock()
	if err := w.Point(contract.PointMail, ""); err != nil || len(p.typed) != 2 {
		t.Errorf("a pointer into the emptied box: %v, typed %q", err, p.typed)
	}
}
