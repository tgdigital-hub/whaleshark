package watch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/term"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/hook"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/internal/pty"
	"github.com/tgdigital-hub/whaleshark/internal/screen"
)

// roleEnv turns this test program into one of two others: the fake agent,
// or the `hook` command an agent's harness runs. logEnv names the file the
// second writes its own clock to.
const (
	roleEnv = "WHALESHARK_TEST_ROLE"
	logEnv  = "WHALESHARK_TEST_HOOKLOG"
)

func TestMain(m *testing.M) {
	switch os.Getenv(roleEnv) {
	case "agent":
		fakeAgent(os.Args[1])
	case "hook":
		hookRole()
	}
	os.Exit(m.Run())
}

// dial is the terminals as the engine's adapter will be for `hook state`:
// one call on the keeper's socket.
type dial struct {
	contract.NoTerminals
	sock string
}

func (d dial) Hook(event, pane, session, cwd string) error {
	conn, err := net.Dial("unix", d.sock)
	if err != nil {
		return err
	}
	defer conn.Close()
	for _, c := range []contract.WireCall{{Op: contract.OpHello, V: contract.WireVersion},
		{ID: 1, Op: contract.OpHook, Kind: event, Pane: pane, Session: session, Cwd: cwd}} {
		body, _ := json.Marshal(c)
		if err := contract.WriteFrame(conn, contract.FrameCall, body); err != nil {
			return err
		}
	}
	_, _, err = contract.ReadFrame(conn)
	return err
}

// hookRole is `whaleshark hook ...` as the entry point runs it, with the
// stand-in for the adapter, after it has written down when it was started.
func hookRole() {
	now := time.Now()
	if f, err := os.OpenFile(os.Getenv(logEnv), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		fmt.Fprintf(f, "%d %s\n", now.UnixNano(), strings.Join(os.Args[2:], " "))
		f.Close()
	}
	k := contract.NewKit()
	platform.Plug(k)
	hook.Plug(k)
	k.Terms = dial{sock: os.Getenv(contract.EnvSocket)}
	k.Handler("hook")(&contract.Call{Kit: k, Args: os.Args[2:], Now: now, Stdin: os.Stdin, Out: os.Stdout, Err: os.Stderr})
	os.Exit(0)
}

// fakeAgent is an agent as the engine sees one: it draws the words of a
// kind's screen, asks for marked pastes, reads its keys one by one, and with
// hooks tells each moment through the real `hook state`. Its modes: "hooks"
// (a whole agent), "screen" (the same with its hooks switched off), "trust"
// (stopped at the question about its folder), "other" (a kind nothing is
// known of: it prints and falls silent).
func fakeAgent(mode string) {
	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		os.Exit(3)
	}
	leave := func(code int) { term.Restore(int(os.Stdin.Fd()), old); os.Exit(code) }
	say := func(event string) {
		if mode != "hooks" {
			return
		}
		cmd := exec.Command(os.Args[0], "hook", "state", event)
		cmd.Env = append(os.Environ(), roleEnv+"=hook")
		cmd.Stdin = strings.NewReader(`{"session_id":"session-one","cwd":"/work/here","hook_event_name":"` + event + `"}`)
		cmd.Run()
	}
	draw := func(lines ...string) { fmt.Print("\x1b[2J\x1b[H" + strings.Join(lines, "\r\n")) }
	key := func() byte {
		var b [1]byte
		if n, _ := os.Stdin.Read(b[:]); n == 0 {
			leave(0)
		}
		return b[0]
	}
	if mode == "trust" {
		draw("Is this a project you trust?", "> No, exit", "  Yes, I trust this folder", "Enter to confirm · Esc to cancel")
		key()
		leave(0)
	}
	fmt.Print("\x1b[?2004h")
	draw("a fake agent", "> ", "  ? for shortcuts")
	say("SessionStart")
	for line := []byte{}; ; {
		b := key()
		if b != '\r' || bytes.Count(line, []byte("\x1b[200~")) > bytes.Count(line, []byte("\x1b[201~")) {
			line = append(line, b)
			continue
		}
		text := strings.NewReplacer("\x1b[200~", "", "\x1b[201~", "").Replace(string(line))
		line = line[:0]
		if text == "/exit" {
			say("SessionEnd")
			leave(0)
		}
		say("UserPromptSubmit")
		draw("got: "+text, "  esc to interrupt")
		if mode == "other" {
			continue
		}
		if strings.Contains(text, "step") {
			say("PreToolUse")
			draw("Do you want to make this step?", "> 1. Yes", "  2. No", "Esc to cancel")
			say("PermissionRequest")
			if key() == '\r' {
				// A long step: its hook comes only when it has ended.
				draw("got: "+text, "  esc to interrupt")
				time.Sleep(time.Second)
				say("PostToolUse")
			} else {
				// Refused: the real agent runs no hook for it.
				draw("stopped: "+text, "> ", "  questions on")
				continue
			}
		}
		time.Sleep(200 * time.Millisecond)
		draw("did: "+text, "> ", "  ? for shortcuts")
		say("Stop")
		say("SubagentStop")
		say("Notification")
	}
}

// pane is a pane as the keeper holds one: a real terminal read into a
// screen. typed is what the Watcher typed there, and nobody else.
type pane struct {
	tty   contract.Pty
	count atomic.Uint64
	gone  chan struct{}

	mu    sync.Mutex
	scr   *screen.Screen
	typed []byte
}

// open starts a program in a pane of its own. It skips the test where the
// system cannot say which program is in front in a terminal.
func open(t *testing.T, env []string, argv ...string) *pane {
	t.Helper()
	return openIn(t, t.TempDir(), append(os.Environ(), env...), argv...)
}

func openIn(t *testing.T, dir string, env []string, argv ...string) *pane {
	t.Helper()
	tty, err := pty.Start(contract.PtySpec{Argv: argv, Dir: dir, Env: env, Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	p := &pane{tty: tty, scr: screen.New(100, 30), gone: make(chan struct{})}
	go func() {
		defer close(p.gone)
		for buf := make([]byte, 32<<10); ; {
			n, err := tty.Read(buf)
			if n > 0 {
				p.mu.Lock()
				p.count.Add(uint64(n))
				p.scr.Write(buf[:n])
				reply := p.scr.Reply()
				p.mu.Unlock()
				tty.Write(reply)
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		tty.Wait()
		time.Sleep(300 * time.Millisecond)
		tty.Close()
	}()
	t.Cleanup(func() {
		tty.Close()
		<-p.gone
	})
	if _, err := tty.Front(); errors.Is(err, contract.ErrCannotTell) {
		t.Skip("this system cannot say which program is in front")
	}
	return p
}

func (p *pane) Count() uint64          { return p.count.Load() }
func (p *pane) Front() (string, error) { return p.tty.Front() }

func (p *pane) Text() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scr.Picture().Text()
}

func (p *pane) Paste() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scr.Modes().Paste
}

func (p *pane) Type(b []byte) error {
	p.mu.Lock()
	p.typed = append(p.typed, b...)
	p.mu.Unlock()
	_, err := p.tty.Write(b)
	return err
}

func (p *pane) wasTyped() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return string(p.typed)
}

// press is a key of the person's own, which is none of the Watcher's.
func (p *pane) press(keys string) { p.tty.Write([]byte(keys)) }

// stand is the keeper's side of the socket for `hook state`, and the
// record of every state a Watcher gave out, with the clock.
type stand struct {
	sock string

	mu     sync.Mutex
	w      *Watcher
	states []State
	at     []time.Time
	hooks  []string
	got    []time.Time // when each hook's call arrived
}

// listen opens a socket of the test's own and answers every call on it. Its
// folder has a short name: a socket's path may not be long.
func listen(t *testing.T) *stand {
	t.Helper()
	dir, err := os.MkdirTemp("", "ws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	s := &stand{sock: filepath.Join(dir, "e.sock")}
	ln, err := net.Listen("unix", s.sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

func (s *stand) serve(conn net.Conn) {
	defer conn.Close()
	for {
		_, body, err := contract.ReadFrame(conn)
		var c contract.WireCall
		if err != nil || json.Unmarshal(body, &c) != nil {
			return
		}
		if c.Op != contract.OpHook {
			continue
		}
		s.mu.Lock()
		w := s.w
		s.hooks, s.got = append(s.hooks, c.Kind), append(s.got, time.Now())
		s.mu.Unlock()
		if w != nil && c.Pane == "p7" {
			w.Hook(c.Kind, c.Session, c.Cwd)
		}
		reply, _ := json.Marshal(contract.WireReply{ID: c.ID})
		contract.WriteFrame(conn, contract.FrameReply, reply)
	}
}

// env is what the pane's program needs to reach this stand-in: the socket,
// the pane's id as the keeper sets it, and no pane of anyone else's.
func (s *stand) env(role string) []string {
	return []string{roleEnv + "=" + role, contract.EnvSocket + "=" + s.sock, contract.EnvTermPane + "=p7",
		contract.EnvPane + "=", logEnv + "=" + filepath.Join(filepath.Dir(s.sock), "hooks.log")}
}

// watch follows the program in a pane as an agent of a kind.
func (s *stand) watch(t *testing.T, p Pane, agent string) *Watcher {
	w := New(p, agent, "the name", func(st State) {
		s.mu.Lock()
		s.states, s.at = append(s.states, st), append(s.at, time.Now())
		s.mu.Unlock()
	})
	s.mu.Lock()
	s.w = w
	s.mu.Unlock()
	t.Cleanup(w.Close)
	return w
}

// statuses are the states given out so far, each once.
func (s *stand) statuses() (out []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.states {
		if len(out) == 0 || out[len(out)-1] != st.Status {
			out = append(out, st.Status)
		}
	}
	return out
}

func (s *stand) heard() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.hooks)
}

// reach waits until the last state given out is the one wanted.
func (s *stand) reach(t *testing.T, status string, within time.Duration) State {
	t.Helper()
	for end := time.Now().Add(within); ; time.Sleep(10 * time.Millisecond) {
		s.mu.Lock()
		var last State
		if len(s.states) > 0 {
			last = s.states[len(s.states)-1]
		}
		s.mu.Unlock()
		if last.Status == status {
			return last
		}
		if time.Now().After(end) {
			t.Fatalf("the state is %+v and did not become %q; so far %v, from the hooks %v", last, status, s.statuses(), s.heard())
		}
	}
}
