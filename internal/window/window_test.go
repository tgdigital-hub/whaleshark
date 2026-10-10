//go:build darwin || linux

package window

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// The test program is the program under test too: started again with
// asWindow set it runs `open` as the real one does, and asked for
// `engine run` it is whatever keeper asKeeper names.
const (
	asWindow = "WHALESHARK_TEST_WINDOW"
	asKeeper = "WHALESHARK_TEST_KEEPER"
)

func TestMain(m *testing.M) {
	switch {
	case os.Getenv(asWindow) == "":
		os.Exit(m.Run())
	case len(os.Args) > 2 && os.Args[1] == "engine" && os.Getenv(asKeeper) == "late":
		// A keeper that takes its time, serves one window and ends.
		time.Sleep(300 * time.Millisecond)
		k, err := listen(os.Getenv(contract.EnvSocket))
		if err != nil {
			os.Exit(1)
		}
		k.greeting = "a late keeper"
		<-k.attached
		time.Sleep(100 * time.Millisecond)
		k.hangUp()
		os.Exit(0)
	case len(os.Args) > 1 && os.Args[1] == "engine":
		os.Exit(1)
	}
	k := contract.NewKit()
	platform.Plug(k)
	Plug(k)
	_, err := k.Handler("open")(&contract.Call{Kit: k, Out: os.Stdout, Err: os.Stderr})
	var r *contract.Refusal
	if errors.As(err, &r) {
		fmt.Fprintln(os.Stderr, r.Code, r.Message)
		os.Exit(r.Exit)
	} else if err != nil {
		os.Exit(1)
	}
}

// keeper is the stand-in: it speaks the contract's frames, greets a
// window, takes its attach and keeps what the window sends after it.
type keeper struct {
	net.Listener
	greeting string
	version  int
	flood    bool
	attached chan contract.WireCall

	mu     sync.Mutex
	conn   net.Conn
	events []term.Event
	sizes  [][2]int
}

func listen(path string) (*keeper, error) {
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	k := &keeper{Listener: l, greeting: "the keeper's first frame", version: contract.WireVersion, attached: make(chan contract.WireCall, 1)}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go k.serve(conn)
		}
	}()
	return k, nil
}

func reply(conn net.Conn, r contract.WireReply) {
	body, _ := json.Marshal(r)
	contract.WriteFrame(conn, contract.FrameReply, body)
}

func (k *keeper) serve(conn net.Conn) {
	defer conn.Close()
	var p term.Parser
	for {
		kind, body, err := contract.ReadFrame(conn)
		if err != nil {
			return
		}
		var c contract.WireCall
		if kind == contract.FrameCall && json.Unmarshal(body, &c) != nil {
			return
		}
		k.mu.Lock()
		switch {
		case kind == contract.FrameBytes:
			k.events = append(k.events, p.Feed(body)...)
		case c.Op == contract.OpHello && c.V != k.version:
			reply(conn, contract.WireReply{ID: c.ID, Err: contract.ErrCode(contract.ErrWireVersion)})
		case c.Op == contract.OpHello:
			reply(conn, contract.WireReply{ID: c.ID})
		case c.Op == contract.OpAttach:
			k.conn = conn
			contract.WriteFrame(conn, contract.FrameBytes, []byte(k.greeting))
			k.attached <- c
			if k.flood {
				go func() {
					for contract.WriteFrame(conn, contract.FrameBytes, []byte("\x1b[1;1Hsome more of the picture")) == nil {
					}
				}()
			}
		case c.Op == contract.OpWindowSize:
			k.sizes = append(k.sizes, [2]int{int(c.W), int(c.H)})
		}
		k.mu.Unlock()
	}
}

func (k *keeper) hangUp() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.conn.Close()
}

// session is one window in a pseudo-terminal of its own.
type session struct {
	t            *testing.T
	outer, inner *os.File
	cmd          *exec.Cmd
	socket       string
	errs         strings.Builder
	mu           sync.Mutex
	sent         strings.Builder
}

// begin starts the window in a terminal of 40 by 10 with a pretended login.
func begin(t *testing.T, socket string, env ...string) *session {
	t.Helper()
	outer, inner, err := openPTY()
	if err != nil {
		t.Skipf("no pseudo-terminal here: %v", err)
	}
	s := &session{t: t, outer: outer, inner: inner, socket: socket}
	t.Cleanup(func() { outer.Close(); inner.Close() })
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := outer.Read(b)
			s.mu.Lock()
			s.sent.Write(b[:n])
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	if err := unix.IoctlSetWinsize(int(inner.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: 40, Row: 10}); err != nil || !s.cooked() {
		t.Fatalf("a new terminal should take a size (%v), take whole lines and echo them", err)
	}
	home := t.TempDir()
	s.cmd = exec.Command(os.Args[0])
	s.cmd.Env = append([]string{asWindow + "=1", contract.EnvSocket + "=" + socket, "HOME=" + home,
		"APPDATA=" + filepath.Join(home, "roaming"), "LOCALAPPDATA=" + filepath.Join(home, "local"),
		"TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=a_terminal"}, env...)
	s.cmd.Stdin, s.cmd.Stdout, s.cmd.Stderr = inner, inner, &s.errs
	if err := s.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.cmd.Process.Kill() })
	return s
}

func (s *session) shown() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sent.String()
}

func (s *session) cooked() bool {
	tio, err := unix.IoctlGetTermios(int(s.inner.Fd()), getTermios)
	if err != nil {
		s.t.Fatal(err)
	}
	return tio.Lflag&unix.ICANON != 0 && tio.Lflag&unix.ECHO != 0
}

func wait(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for range 2500 {
		if ok() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal(what)
}

// end waits for the window to leave and returns its exit code.
func (s *session) end() int {
	s.t.Helper()
	done := make(chan struct{})
	go func() { s.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		s.t.Fatalf("the window did not end; the terminal got %q", s.shown())
	}
	return s.cmd.ProcessState.ExitCode()
}

// usable fails unless the terminal is as a shell needs it: whole lines,
// echo, and everything the window asked for given back after it was asked.
func (s *session) usable() {
	s.t.Helper()
	if !s.cooked() {
		s.t.Error("the terminal was left in raw mode")
	}
	wait(s.t, fmt.Sprintf("the terminal was not given back: it got %q", s.shown()), func() bool {
		got := s.shown()
		if i := strings.LastIndex(got, "\x1b[?1049h"); i >= 0 {
			_, after, gave := strings.Cut(got[i:], leave)
			return gave && !strings.Contains(after, "\x1b")
		}
		return !strings.Contains(got, "\x1b[?")
	})
}

func socketFor(t *testing.T) string {
	dir, err := os.MkdirTemp("", "ws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "k.sock")
}

// standIn starts a stand-in keeper; set changes it before any window connects.
func standIn(t *testing.T, set func(*keeper)) (*keeper, string) {
	path := socketFor(t)
	k, err := listen(path)
	if err != nil {
		t.Fatal(err)
	}
	k.mu.Lock()
	set(k)
	k.mu.Unlock()
	t.Cleanup(func() { k.Close() })
	return k, path
}

func (k *keeper) attach(t *testing.T) contract.WireCall {
	t.Helper()
	select {
	case c := <-k.attached:
		return c
	case <-time.After(10 * time.Second):
		t.Fatal("the window never attached")
		return contract.WireCall{}
	}
}

// Through a real pseudo-terminal: the window says what it is, shows what
// the keeper sends, and keys, clicks, a paste and a size change arrive.
func TestKeysClicksAPasteAndASizeChangeArrive(t *testing.T) {
	k, path := standIn(t, func(*keeper) {})
	s := begin(t, path)
	c := k.attach(t)
	if c.W != 40 || c.H != 10 || c.System != runtime.GOOS || strings.Join(c.Env, " ") != "TERM=xterm-256color COLORTERM=truecolor TERM_PROGRAM=a_terminal" {
		t.Fatalf("the window said of itself: %+v", c)
	}
	wait(t, "the keeper's frame never showed", func() bool { return strings.Contains(s.shown(), k.greeting) })
	if got := s.shown(); !strings.HasPrefix(got, enter+k.greeting) || s.cooked() {
		t.Fatalf("the window did not take the terminal over before the first frame: %q", got)
	}

	fmt.Fprint(s.outer, "a\x1b[99;9u\x1b[<0;5;3M\x1b[<0;5;3m\x1b[200~two\nlines\x1b[201~\x03\x1b[I")
	want := []term.Event{{Kind: term.KeyPress, Key: "a", Rune: 'a'}, {Kind: term.KeyPress, Key: "super+c"},
		{Kind: term.Mouse, Button: term.Left, Action: term.Press, X: 4, Y: 2}, {Kind: term.Mouse, Button: term.Left, Action: term.Release, X: 4, Y: 2},
		{Kind: term.Paste, Text: "two\nlines"}, {Kind: term.KeyPress, Key: "ctrl+c"}, {Kind: term.Focus, Focused: true}}
	wait(t, "what was typed, clicked and pasted did not all arrive", func() bool {
		k.mu.Lock()
		defer k.mu.Unlock()
		return len(k.events) >= len(want)
	})
	k.mu.Lock()
	for i, ev := range k.events {
		if ev != want[i] {
			t.Errorf("event %d arrived as %+v, want %+v", i, ev, want[i])
		}
	}
	k.mu.Unlock()

	if err := unix.IoctlSetWinsize(int(s.outer.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: 100, Row: 30}); err != nil {
		t.Fatal(err)
	}
	// The terminal is not the window's controlling one here (a terminal
	// whose session ends takes its settings with it, and the test reads
	// them afterwards), so the system's notice is sent by hand.
	s.cmd.Process.Signal(syscall.SIGWINCH)
	wait(t, "the new size never arrived", func() bool {
		k.mu.Lock()
		defer k.mu.Unlock()
		return len(k.sizes) == 1 && k.sizes[0] == [2]int{100, 30}
	})

	k.hangUp()
	if code := s.end(); code != 0 {
		t.Errorf("closed by the keeper, the window ended with %d: %s", code, s.errs.String())
	}
	s.usable()
	wait(t, "the window did not say it had closed", func() bool { return strings.Contains(s.shown(), "The window is closed.") })
}

// Killed at any moment, by any signal a program can answer, the window
// leaves the terminal usable: before it connects, while it asks for the
// terminal, and while the keeper's frames pour in.
func TestKilledAtAnyMomentTheTerminalIsUsable(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := range 45 {
		sig := []syscall.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP}[i%3]
		k, path := standIn(t, func(k *keeper) { k.flood = true })
		s := begin(t, path)
		if i < 9 {
			k.attach(t)
		}
		time.Sleep(time.Duration(rng.Intn(40_000)) * time.Microsecond)
		s.cmd.Process.Signal(sig)
		// Before the program has begun, the signal ends it as it would any other.
		if code := s.end(); code != 128+int(sig) && code != -1 {
			t.Errorf("round %d: killed by %v, the window ended with %d", i, sig, code)
		}
		s.usable()
		k.Close()
	}
}

func TestTheKeeperRefusingMidStream(t *testing.T) {
	k, path := standIn(t, func(*keeper) {})
	s := begin(t, path)
	k.attach(t)
	k.mu.Lock()
	reply(k.conn, contract.WireReply{Err: "failed", Message: "the keeper is stopping"})
	k.mu.Unlock()
	if code := s.end(); code != contract.ExitEnv || !strings.Contains(s.errs.String(), "closed The keeper closed the window: the keeper is stopping.") {
		t.Errorf("ended with %d and %q", code, s.errs.String())
	}
	s.usable()
}

// With no keeper the window starts one. One that fails is not waited for,
// one that is slow is, and in neither case is the terminal touched early.
func TestAKeeperThatIsNotThereOrIsStarting(t *testing.T) {
	s := begin(t, socketFor(t))
	began := time.Now()
	if code := s.end(); code != contract.ExitEnv || !strings.Contains(s.errs.String(), "not_connected The window is not connected") {
		t.Errorf("with no keeper the window ended with %d and %q", code, s.errs.String())
	}
	if took := time.Since(began); took > startWait/2 {
		t.Errorf("a keeper that failed at once was waited for %v", took)
	}
	if got := s.shown(); got != "" || !s.cooked() {
		t.Errorf("the terminal was touched: %q", got)
	}

	s = begin(t, socketFor(t), asKeeper+"=late")
	wait(t, "the late keeper's frame never showed", func() bool { return strings.Contains(s.shown(), "a late keeper") })
	if code := s.end(); code != 0 {
		t.Errorf("after a late keeper the window ended with %d: %s", code, s.errs.String())
	}
	s.usable()
}

func TestAKeeperOfAnotherVersion(t *testing.T) {
	_, path := standIn(t, func(k *keeper) { k.version++ })
	s := begin(t, path)
	if code := s.end(); code != contract.ExitEnv || !strings.Contains(s.errs.String(), "restart_needed") {
		t.Errorf("ended with %d and %q", code, s.errs.String())
	}
	if got := s.shown(); got != "" || !s.cooked() {
		t.Errorf("the terminal was touched: %q", got)
	}
}

func TestNoTerminal(t *testing.T) {
	_, path := standIn(t, func(*keeper) {})
	cmd := exec.Command(os.Args[0])
	cmd.Env = []string{asWindow + "=1", contract.EnvSocket + "=" + path, "HOME=" + t.TempDir()}
	out, err := cmd.CombinedOutput()
	if cmd.ProcessState.ExitCode() != contract.ExitEnv || !strings.Contains(string(out), "no_terminal The window needs a terminal") {
		t.Errorf("with no terminal: %v, %q", err, out)
	}
}

func TestWhoseKeysApply(t *testing.T) {
	for _, c := range []struct{ said, ssh, tty, want string }{
		{"", "", "", system}, {"", "a b c d", "", ""}, {"", "", "/dev/pts/4", ""},
		{"darwin", "a b c d", "/dev/pts/4", "darwin"}, {"windows", "", "", "windows"},
	} {
		env := map[string]string{envSystem: c.said, "SSH_CONNECTION": c.ssh, "SSH_TTY": c.tty}
		if got := whose(func(k string) string { return env[k] }); got != c.want {
			t.Errorf("%+v: %q, want %q", c, got, c.want)
		}
	}
}
