package watch

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/hook"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
)

// selfAt is the platform with the program somewhere else.
type selfAt struct {
	contract.Platform
	path string
}

func (s selfAt) SelfPath() (string, error) { return s.path, nil }

// real starts one small Claude Code agent in a pane, with the hooks the
// tool sets for it and none of the person's own, and follows it.
func real(t *testing.T, s *stand, dir string) (*pane, *Watcher) {
	t.Helper()
	k := contract.NewKit()
	platform.Plug(k)
	hook.Plug(k)
	k.Platform = selfAt{k.Platform, os.Args[0]}
	setup, err := k.AgentSettings.Ensure("claude", dir, t.TempDir(), false)
	if err != nil || len(setup.Args) != 2 {
		t.Fatalf("%+v, %v", setup, err)
	}
	env := s.env("hook")
	for _, v := range os.Environ() {
		// The agent's own sign-in stays; nothing of the terminals or of an
		// agent this test may itself be running in is handed on.
		if !strings.HasPrefix(v, "HERDR_") && !strings.HasPrefix(v, "CLAUDE") && !strings.HasPrefix(v, "WHALESHARK_") {
			env = append(env, v)
		}
	}
	p := openIn(t, dir, env, append([]string{"claude", "--model", "haiku", "--setting-sources", "local", "--permission-mode", "manual"}, setup.Args...)...)
	return p, s.watch(t, p, "claude")
}

// One real agent is taken through working, a question about a step that is
// allowed, one that is refused, idle and its own end, and every state given
// out is held against the clock of the agent's own hooks. A second is
// killed outright. It spends a little of the login's own usage, so it runs
// only when asked for, in a plain folder the agent already trusts:
//
//	WHALESHARK_REAL_AGENT=<folder> go test -count=1 -v -run RealAgent ./internal/watch/
func TestARealAgent(t *testing.T) {
	dir := os.Getenv("WHALESHARK_REAL_AGENT")
	if dir == "" {
		t.Skip("set WHALESHARK_REAL_AGENT to a plain folder to start two small Claude Code agents there")
	}
	// A folder of this run's own, which is left there: it holds what the agent made.
	dir, err := os.MkdirTemp(dir, "run")
	if err != nil {
		t.Fatal(err)
	}
	s := listen(t)
	p, w := real(t, s, dir)
	began := time.Now()
	if err := w.Ready(wait); err != nil {
		t.Fatalf("ready: %v\n%s", err, p.Text())
	}
	front, _ := p.Front()
	t.Logf("ready after %v; the program in front is called %q", time.Since(began).Round(time.Millisecond), front)

	step := func(file, key string) {
		t.Helper()
		if err := w.Prompt("Use the Bash tool once to run exactly this command, then say done: touch "+file, wait); err != nil {
			t.Fatalf("the prompt: %v\n%s", err, p.Text())
		}
		s.reach(t, blocked, 2*wait)
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
		time.Sleep(time.Second)
		p.press(key)
		s.reach(t, idle, 2*wait)
		t.Logf("after the key %q the hooks so far are %v", key, s.heard())
	}
	step("allowed.txt", "\r")
	if _, err := os.Stat(filepath.Join(dir, "allowed.txt")); err != nil {
		t.Errorf("the allowed step did not run: %v", err)
	}
	// Refused twice: with Escape, and with the answer "No" (the fourth).
	for _, key := range []string{"\x1b", "4"} {
		step("refused.txt", key)
		if _, err := os.Stat(filepath.Join(dir, "refused.txt")); err == nil {
			t.Error("the refused step ran")
		}
	}
	if err := w.Point(contract.PointResumed, ""); err != nil {
		t.Errorf("a pointer at an idle agent: %v", err)
	}
	// The pointer names a command, which the agent may ask to run.
	time.Sleep(time.Second)
	for end := time.Now().Add(2 * wait); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if last := s.statuses(); last[len(last)-1] == blocked {
			p.press("\x1b")
			break
		} else if last[len(last)-1] == idle {
			break
		}
	}
	s.reach(t, idle, 2*wait)

	p.press("/exit")
	time.Sleep(500 * time.Millisecond)
	p.press("\r")
	select {
	case <-p.gone:
	case <-time.After(wait):
		t.Fatalf("the agent did not end\n%s", p.Text())
	}
	time.Sleep(200 * time.Millisecond)
	s.compare(t)

	// Whether an agent that is killed says so itself.
	s2 := listen(t)
	p2, w2 := real(t, s2, dir)
	if err := w2.Ready(wait); err != nil {
		t.Fatalf("the second agent: %v", err)
	}
	p2.tty.Close()
	<-p2.gone
	time.Sleep(3 * time.Second)
	t.Logf("a killed agent's hooks: %v", s2.heard())

	// A folder the agent does not trust yet: a repository of its own. It
	// asks, no hook runs, and nothing is typed. The question stays unanswered.
	if out, err := exec.Command("git", "init", "-q", filepath.Join(dir, "untrusted")).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	s3 := listen(t)
	p3, w3 := real(t, s3, filepath.Join(dir, "untrusted"))
	if err := w3.Ready(wait); !errors.Is(err, contract.ErrAgentNotReady) {
		t.Errorf("ready in a folder that is not trusted: %v\n%s", err, p3.Text())
	}
	if err := w3.Prompt("hello", time.Second); !errors.Is(err, contract.ErrAgentBlocked) || p3.wasTyped() != "" || len(s3.heard()) != 0 {
		t.Errorf("at the question about the folder: %v, typed %q, hooks %v", err, p3.wasTyped(), s3.heard())
	}
}

// compare prints every hook with its own clock beside the moment its call
// arrived and the states given out, and holds each state a hook means to
// that clock.
func (s *stand) compare(t *testing.T) {
	t.Helper()
	f, err := os.Open(filepath.Join(filepath.Dir(s.sock), "hooks.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	var lines []string
	for i, st := range s.states {
		lines = append(lines, fmt.Sprintf("%s  state %s", s.at[i].Format("15:04:05.000"), st.Status))
	}
	n, worst := 0, time.Duration(0)
	for sc := bufio.NewScanner(f); sc.Scan(); {
		nanos, form, _ := strings.Cut(sc.Text(), " ")
		event, isState := strings.CutPrefix(form, "state ")
		if !isState {
			continue
		}
		ns, _ := strconv.ParseInt(nanos, 10, 64)
		at := time.Unix(0, ns)
		if n >= len(s.hooks) || s.hooks[n] != event {
			t.Fatalf("hook %d was %s by its own clock; the calls that arrived were %v", n, event, s.hooks)
		}
		lag := s.got[n].Sub(at)
		worst = max(worst, lag)
		lines = append(lines, fmt.Sprintf("%s  hook  %s (arrived %v later)", at.Format("15:04:05.000"), event, lag.Round(time.Millisecond)))
		n++
		// The state the hook means was given out before the hook (by the
		// screen) or within a moment of it, unless a question was showing.
		want, means := events[event]
		ok := !means || want == ""
		for i, st := range s.states {
			next := time.Now()
			if i+1 < len(s.at) {
				next = s.at[i+1]
			}
			if st.Status == want || st.Status == blocked {
				ok = ok || s.at[i].Before(at.Add(time.Second)) && next.After(at)
			}
		}
		if !ok {
			t.Errorf("no state %q within a second of the hook %s at %s", want, event, at.Format("15:04:05.000"))
		}
	}
	slices.Sort(lines)
	t.Logf("the latest call arrived %v after its hook's own clock\n%s", worst.Round(time.Millisecond), strings.Join(lines, "\n"))
}
