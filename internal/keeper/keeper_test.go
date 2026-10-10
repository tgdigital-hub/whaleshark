package keeper

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
)

var update = flag.Bool("update", false, "write the session's log as the approved one")

// program is a pane's terminal with a pretended program in it: it prints
// how it was started, says each new size, and gives back what is typed.
type program struct {
	spec    contract.PtySpec
	out     chan []byte
	ended   chan struct{}
	once    sync.Once
	printed atomic.Uint64
	mu      sync.Mutex
	typed   []byte
}

func (f *program) print(s string) {
	f.printed.Add(uint64(len(s)))
	f.out <- []byte(s)
}

func (f *program) Read(b []byte) (int, error) {
	select {
	case d := <-f.out:
		return copy(b, d), nil
	default:
	}
	select {
	case d := <-f.out:
		return copy(b, d), nil
	case <-f.ended:
		return 0, io.EOF
	}
}

func (f *program) Write(b []byte) (int, error) {
	f.mu.Lock()
	f.typed = append(f.typed, b...)
	f.mu.Unlock()
	f.print(string(b))
	return len(b), nil
}

func (f *program) got() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.typed)
}

func (f *program) Resize(cols, rows int) error {
	f.print(fmt.Sprintf("size %dx%d\r\n", cols, rows))
	return nil
}

func (f *program) Wait() (int, error)     { <-f.ended; return 0, nil }
func (f *program) Front() (string, error) { return "program", nil }
func (f *program) Close() error           { f.once.Do(func() { close(f.ended) }); return nil }

// rig is a keeper of the test's own: a login in a folder of its own, a
// socket there, and pretended programs in the panes.
type rig struct {
	t    *testing.T
	k    *Keeper
	kit  *contract.Kit
	fail bool // the next start of a program fails
}

func value(env []string, name string) string {
	for _, v := range env {
		if n, val, _ := strings.Cut(v, "="); n == name {
			return val
		}
	}
	return ""
}

// login is a kit for a pretended login whose folders, and whose socket, are
// in a folder of the test's own. The folder's name is short: a socket's
// path has little room.
func login(t *testing.T) *contract.Kit {
	t.Helper()
	dir, err := os.MkdirTemp("", "ws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sys := platform.New(runtime.GOOS)
	sys.Home = dir
	sys.Env = func(name string) string {
		return map[string]string{"APPDATA": filepath.Join(dir, "roaming"), "LOCALAPPDATA": filepath.Join(dir, "local")}[name]
	}
	t.Setenv(contract.EnvSocket, filepath.Join(dir, "k.sock"))
	t.Setenv("SHELL", "the-shell")
	t.Setenv("TERM_PROGRAM", "a terminal the keeper was started in")
	kit := contract.NewKit()
	kit.Platform = sys
	return kit
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, kit: login(t)}
	r.kit.Pty = func(s contract.PtySpec) (contract.Pty, error) {
		if r.fail {
			r.fail = false
			return nil, errors.New("the program is not there")
		}
		f := &program{spec: s, out: make(chan []byte, 4096), ended: make(chan struct{})}
		f.print(fmt.Sprintf("%s in %q at %dx%d pane=%s colour=%s outer=%q\r\n", strings.Join(s.Argv, " "), s.Dir, s.Cols, s.Rows,
			value(s.Env, contract.EnvTermPane), value(s.Env, "COLOUR"), value(s.Env, "TERM_PROGRAM")))
		return f, nil
	}
	k, err := open(r.kit)
	if err != nil {
		t.Fatal(err)
	}
	k.instance, k.version = "one", "test"
	r.k = k
	go k.serve()
	t.Cleanup(k.stop)
	return r
}

// until waits for something the keeper does by itself.
func (r *rig) until(what string, ok func() bool) {
	r.t.Helper()
	for end := time.Now().Add(10 * time.Second); !ok(); time.Sleep(time.Millisecond) {
		if time.Now().After(end) {
			r.t.Fatalf("waited in vain for %s", what)
		}
	}
}

// locked asks something of the keeper's state.
func (r *rig) locked(ask func() bool) bool {
	r.k.mu.Lock()
	defer r.k.mu.Unlock()
	return ask()
}

// program is the pretended program in a pane now.
func (r *rig) program(pane string) (f *program) {
	r.locked(func() bool {
		if p := r.k.panes[pane]; p != nil {
			f = p.tty.(*program)
		}
		return true
	})
	if f == nil {
		r.t.Fatalf("no pane %s", pane)
	}
	return f
}

// settle waits until every pane's screen holds all its program printed.
func (r *rig) settle() {
	r.t.Helper()
	r.until("the panes to read everything", func() bool {
		return r.locked(func() bool {
			for _, p := range r.k.panes {
				p.mu.Lock()
				same := p.tty.(*program).printed.Load() == p.count.Load()
				p.mu.Unlock()
				if !same {
					return false
				}
			}
			return true
		})
	})
}

// client is one connection to the keeper, greeted.
type client struct {
	t    *testing.T
	conn net.Conn
	id   uint64
}

func (r *rig) dial() *client {
	r.t.Helper()
	conn, err := net.Dial("unix", os.Getenv(contract.EnvSocket))
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { conn.Close() })
	c := &client{t: r.t, conn: conn}
	if reply := c.call(contract.WireCall{V: contract.WireVersion, Op: contract.OpHello}); reply.Err != "" {
		r.t.Fatalf("hello: %+v", reply)
	}
	return c
}

func (c *client) send(call contract.WireCall) {
	c.t.Helper()
	c.id++
	call.ID = c.id
	body, _ := json.Marshal(call)
	if err := contract.WriteFrame(c.conn, contract.FrameCall, body); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) reply() (r contract.WireReply) {
	c.t.Helper()
	kind, body, err := contract.ReadFrame(c.conn)
	if err != nil || kind != contract.FrameReply || json.Unmarshal(body, &r) != nil {
		c.t.Fatalf("no reply: kind %q, %v", kind, err)
	}
	return r
}

func (c *client) call(call contract.WireCall) contract.WireReply {
	c.t.Helper()
	c.send(call)
	r := c.reply()
	if r.ID != c.id {
		c.t.Fatalf("the reply to call %d carries %d", c.id, r.ID)
	}
	return r
}

// A scripted session of a hundred calls and more, compared with a file. Each
// line of testdata/session.txt is one call, or "!" and something that
// happens by itself: a window of a size attaches, a pane's program ends or
// fails to start. The log holds every call, its answer, and the events it
// made, in order; at the end the events applied to the first picture must
// give the last.
func TestAScriptedSessionOfAHundredCalls(t *testing.T) {
	r := newRig(t)
	calls, watch := r.dial(), r.dial()
	watch.send(contract.WireCall{Op: contract.OpEvents})
	picture := watch.reply().Snapshot
	script, err := os.Open(filepath.Join("testdata", "session.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer script.Close()
	pid := regexp.MustCompile(`pid \d+`)
	var log bytes.Buffer
	seen, count := uint64(0), 0
	for lines := bufio.NewScanner(script); lines.Scan(); {
		line := lines.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fmt.Fprintln(&log, line)
		if f := strings.Fields(line); f[0] == "!window" {
			var w, h float64
			fmt.Sscan(f[1]+" "+f[2], &w, &h)
			win := r.dial()
			win.send(contract.WireCall{Op: contract.OpAttach, W: w, H: h, Env: []string{"TERM=xterm-256color"}})
			go io.Copy(io.Discard, win.conn)
			r.until("the window", func() bool { return r.locked(func() bool { return r.k.lay.W == int(w) }) })
		} else if f[0] == "!end" {
			r.program(f[1]).Close()
			r.until("the pane to go", func() bool { return r.locked(func() bool { return r.k.panes[f[1]] == nil }) })
		} else if f[0] == "!fail" {
			r.fail = true
		} else {
			var call contract.WireCall
			if err := json.Unmarshal([]byte(line), &call); err != nil {
				t.Fatalf("%s: %v", line, err)
			}
			r.settle()
			count++
			reply := calls.call(call)
			if reply.ID = 0; reply.Snapshot != nil {
				reply.Snapshot.At = time.Time{}
			}
			body, _ := json.Marshal(reply)
			fmt.Fprintf(&log, "  < %s\n", pid.ReplaceAll(body, []byte("pid N")))
		}
		var last uint64
		r.locked(func() bool { last = r.k.seq; return true })
		for seen < last {
			ev := watch.reply().Event
			if ev == nil || ev.Seq != seen+1 {
				t.Fatalf("after %q: event %+v where number %d was due", line, ev, seen+1)
			}
			seen = ev.Seq
			contract.Apply(picture, *ev)
			body, _ := json.Marshal(ev)
			fmt.Fprintf(&log, "  * %s\n", body)
		}
	}
	if count < 100 {
		t.Errorf("the session has %d calls, not a hundred", count)
	}
	final := calls.call(contract.WireCall{Op: contract.OpSnapshot}).Snapshot
	if final.Seq != picture.Seq || !slices.Equal(final.Panes, picture.Panes) {
		t.Errorf("the events applied to the first picture give\n%+v\nand the keeper's picture is\n%+v", picture, final)
	}
	approved := filepath.Join("testdata", "session.log")
	if *update {
		if err := os.WriteFile(approved, log.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, _ := os.ReadFile(approved)
	if got := log.String(); got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		g, w := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for i := range g {
			if i >= len(w) || g[i] != w[i] {
				t.Fatalf("the session differs from %s at line %d:\n got %s\nwant %s", approved, i+1, g[i], append(w, "")[min(i, len(w))])
			}
		}
		t.Fatalf("the session is shorter than %s", approved)
	}
}

// Who may connect: this login, with a hello of this format's number first.
// The socket is the login's alone, and a second keeper does not start.
func TestWhoMayConnect(t *testing.T) {
	r := newRig(t)
	path := os.Getenv(contract.EnvSocket)
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("the socket's permissions are %v", info.Mode().Perm())
	}
	if _, err := open(r.kit); !errors.Is(err, errRunning) {
		t.Errorf("a second keeper: %v", err)
	}
	raw := func(call contract.WireCall) (contract.WireReply, error) {
		conn, err := net.Dial("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		body, _ := json.Marshal(call)
		contract.WriteFrame(conn, contract.FrameCall, body)
		var reply contract.WireReply
		_, body, err = contract.ReadFrame(conn)
		if err == nil {
			json.Unmarshal(body, &reply)
			_, _, err = contract.ReadFrame(conn)
		}
		return reply, err
	}
	if reply, err := raw(contract.WireCall{V: contract.WireVersion + 1, ID: 7, Op: contract.OpHello}); reply.ID != 7 ||
		!errors.Is(contract.ErrOf(reply), contract.ErrWireVersion) || err == nil {
		t.Errorf("another version was answered %+v, and then %v", reply, err)
	}
	if reply, err := raw(contract.WireCall{ID: 3, Op: contract.OpSnapshot}); !errors.Is(contract.ErrOf(reply), contract.ErrFrame) || err == nil {
		t.Errorf("a call before the hello was answered %+v, and then %v", reply, err)
	}
	c := r.dial()
	if reply := c.call(contract.WireCall{V: contract.WireVersion, Op: contract.OpHello}); !errors.Is(contract.ErrOf(reply), contract.ErrFrame) {
		t.Errorf("a second hello was answered %+v", reply)
	}
	// A folder another login can write is no place for the socket.
	if runtime.GOOS != "windows" {
		shared := filepath.Join(filepath.Dir(path), "shared")
		if err := os.Mkdir(shared, 0o777); err != nil {
			t.Fatal(err)
		}
		os.Chmod(shared, 0o777)
		t.Setenv(contract.EnvSocket, filepath.Join(shared, "k.sock"))
		if k, err := open(r.kit); err == nil {
			k.stop()
			t.Error("a keeper started in a folder anyone can write")
		}
	}
}

// `engine status` and `engine stop` against a running keeper and against
// none; a stop ends every pane's program and leaves no socket behind.
func TestEngineStatusAndStop(t *testing.T) {
	r := newRig(t)
	c := r.dial()
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "one"})
	f := r.program("p1")
	Plug(r.kit)
	run := func(word string) (string, state) {
		var out bytes.Buffer
		res, err := r.kit.Handler("engine")(&contract.Call{Kit: r.kit, Args: []string{word}, Out: &out})
		if err != nil {
			t.Fatalf("engine %s: %v", word, err)
		}
		return out.String(), res.(state)
	}
	if out, st := run("status"); !st.Running || st.Instance != "one" || !strings.Contains(out, "1 tabs, 1 panes, 0 windows") {
		t.Errorf("status of a running keeper: %q %+v", out, st)
	}
	if out, st := run("stop"); st.Running || !strings.Contains(out, "stopped") {
		t.Errorf("stop: %q %+v", out, st)
	}
	select {
	case <-f.ended:
	default:
		t.Error("the pane's program was not ended by the stop")
	}
	if _, err := os.Stat(os.Getenv(contract.EnvSocket)); err == nil {
		t.Error("the socket file is still there")
	}
	if _, _, err := contract.ReadFrame(c.conn); err == nil {
		t.Error("a connection outlived the keeper")
	}
	if out, st := run("status"); st.Running || !strings.Contains(out, "No keeper") {
		t.Errorf("status with no keeper: %q %+v", out, st)
	}
	if out, _ := run("stop"); !strings.Contains(out, "No keeper") {
		t.Errorf("stop with no keeper: %q", out)
	}
	var refusal *contract.Refusal
	if _, err := r.kit.Handler("engine")(&contract.Call{Kit: r.kit, Args: []string{"dance"}, Out: io.Discard}); !errors.As(err, &refusal) || refusal.Exit != contract.ExitUsage {
		t.Errorf("an unknown word: %v", err)
	}
}

// A fault in the screen reader costs one pane its picture and nothing else.
func TestAFaultInOnePaneLeavesTheRest(t *testing.T) {
	r := newRig(t)
	c := r.dial()
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "one"})
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "two"})
	r.settle()
	r.locked(func() bool {
		p := r.k.panes["p1"]
		p.mu.Lock()
		p.scr = nil // the next write fails as a fault in the reader would
		p.mu.Unlock()
		return true
	})
	r.program("p1").print("unseen\r\n")
	r.program("p1").print("kept\r\n")
	r.settle()
	one := c.call(contract.WireCall{Op: contract.OpScreen, Pane: "p1"}).Text
	if !strings.Contains(one, "lost to a fault") || !strings.HasSuffix(one, "kept") || strings.Contains(one, "unseen") {
		t.Errorf("the pane after the fault shows %q", one)
	}
	if two := c.call(contract.WireCall{Op: contract.OpScreen, Pane: "p2"}).Text; !strings.HasPrefix(two, "the-shell") {
		t.Errorf("the other pane shows %q", two)
	}
}

// The shortcuts handed to the keeper are in the person's settings file after.
func TestSetKeysWritesTheSettingsFile(t *testing.T) {
	r := newRig(t)
	keys := []contract.KeyEntry{{Key: "s", Type: "shell", Argv: []string{"whaleshark", "stop"}}}
	if reply := r.dial().call(contract.WireCall{Op: contract.OpSetKeys, Keys: keys}); reply.Err != "" {
		t.Fatalf("%+v", reply)
	}
	s, err := contract.ReadSettings(r.kit.Platform.Read, r.k.dirs)
	if err != nil || len(s.Shortcuts) != 1 || !slices.Equal(s.Shortcuts[0].Argv, keys[0].Argv) || s.Keys.Command != "ctrl+space" {
		t.Errorf("the settings read back: %+v, %v", s, err)
	}
}

// A reader of events that takes nothing is dropped once it is too far
// behind, and holds nobody up meanwhile.
func TestAReaderOfEventsThatFallsBehindIsClosed(t *testing.T) {
	r := newRig(t)
	c, idle := r.dial(), r.dial()
	idle.send(contract.WireCall{Op: contract.OpEvents})
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "one"})
	began := time.Now()
	for i := range eventQueue * 3 {
		c.call(contract.WireCall{Op: contract.OpTabRename, Tab: "t1", Label: fmt.Sprint("name ", i, strings.Repeat(" long", 200))})
	}
	if took := time.Since(began); took > 20*time.Second {
		t.Errorf("the calls took %v beside a reader that takes nothing", took)
	}
	r.until("the reader to be dropped", func() bool { return r.locked(func() bool { return len(r.k.subs) == 0 }) })
	var last uint64
	got := 0
	for {
		kind, body, err := contract.ReadFrame(idle.conn)
		if err != nil {
			break
		}
		var reply contract.WireReply
		if kind != contract.FrameReply || json.Unmarshal(body, &reply) != nil {
			t.Fatal("not a reply")
		}
		if reply.Snapshot != nil {
			last = reply.Snapshot.Seq
		} else if reply.Event != nil {
			if reply.Event.Seq != last+1 {
				t.Fatalf("event %d came after %d", reply.Event.Seq, last)
			}
			last = reply.Event.Seq
			got++
		}
	}
	if got == 0 {
		t.Error("the reader got no event before it was closed")
	}
}
