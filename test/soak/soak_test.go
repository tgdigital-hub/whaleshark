package soak

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/restore"
)

const (
	panes = 20
	tick  = 200 * time.Millisecond
	// What the design allows: a key's echo, 19 times of 20, and the keeper's memory.
	echoMost, memMost = 50 * time.Millisecond, 200 << 20
	// An echo not seen by then is counted as late by this much.
	echoLost = 2 * time.Second
	// The keys typed: 336 letters of two bytes each, one after another, so
	// that the one waited for is on no pane's screen from an earlier key.
	firstKey, keys = 0x100, 336
)

// client is a connection to the keeper that makes calls.
type client struct {
	t    *testing.T
	conn net.Conn
}

func dial(t *testing.T, path string) *client {
	t.Helper()
	var conn net.Conn
	var err error
	for end := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if conn, err = net.Dial("unix", path); err == nil {
			break
		} else if time.Now().After(end) {
			t.Fatal("the keeper did not start: ", err)
		}
	}
	t.Cleanup(func() { conn.Close() })
	c := &client{t, conn}
	c.call(contract.WireCall{Op: contract.OpHello, V: contract.WireVersion})
	return c
}

func (c *client) send(call contract.WireCall) {
	body, _ := json.Marshal(call)
	if err := contract.WriteFrame(c.conn, contract.FrameCall, body); err != nil {
		c.t.Fatalf("%s: %v", call.Op, err)
	}
}

func (c *client) call(call contract.WireCall) (r contract.WireReply) {
	c.t.Helper()
	c.conn.SetDeadline(time.Now().Add(time.Minute))
	c.send(call)
	_, body, err := contract.ReadFrame(c.conn)
	if err == nil {
		err = json.Unmarshal(body, &r)
	}
	if err != nil || r.Err != "" {
		c.t.Fatalf("%s: %v %+v", call.Op, err, r)
	}
	return r
}

// window is the person's end of an attached window: it takes every frame,
// and says when the key it waits for is in one.
type window struct {
	*client
	mu     sync.Mutex
	want   []byte
	before byte // the last byte of the frame before: a frame may end inside a letter
	hit    chan time.Time
}

func (w *window) read() {
	for {
		kind, body, err := contract.ReadFrame(w.conn)
		if err != nil {
			return
		}
		now := time.Now()
		if kind != contract.FrameBytes || len(body) == 0 {
			continue
		}
		w.mu.Lock()
		if w.want != nil && (bytes.Contains(body, w.want) || w.before == w.want[0] && body[0] == w.want[1]) {
			w.want = nil
			w.hit <- now
		}
		w.before = body[len(body)-1]
		w.mu.Unlock()
	}
}

// key types one letter into the pane with the keys and waits until a frame
// shows it.
func (w *window) key(i int) time.Duration {
	k := []byte(string(rune(firstKey + i%keys)))
	select {
	case <-w.hit:
	default:
	}
	w.mu.Lock()
	w.want = k
	w.mu.Unlock()
	began := time.Now()
	if err := contract.WriteFrame(w.conn, contract.FrameBytes, k); err != nil {
		w.t.Fatal("a key: ", err)
	}
	select {
	case at := <-w.hit:
		return at.Sub(began)
	case <-time.After(echoLost):
		return echoLost
	}
}

// part is the value a share of the way through a sorted list.
func part[T any](sorted []T, share float64) (v T) {
	if len(sorted) > 0 {
		v = sorted[min(int(share*float64(len(sorted))), len(sorted)-1)]
	}
	return v
}

func mb(n uint64) string { return fmt.Sprintf("%.1f MB", float64(n)/(1<<20)) }

// Twenty panes, each with a program that prints coloured, moving output in
// bursts of up to a megabyte a second, under the real keeper in a process
// of its own, with a window attached that is typed in five times a second,
// shown another tab every second and resized every half minute. It fails
// on a byte not accounted for, on a key echoed later than 50 ms more than
// once in twenty, on a keeper above 200 MB or growing, and on a goroutine
// or a file the keeper leaves open.
func TestTwentyPanesForAnHour(t *testing.T) {
	length, err := time.ParseDuration(os.Getenv(envSoak))
	if err != nil {
		t.Skipf("runs only when asked, for example %s=1h", envSoak)
	}
	// A login of the test's own, in a folder with a short name: a socket's
	// path has little room.
	dir, err := os.MkdirTemp("", "ws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sys := login(dir)
	dirs, _ := sys.Dirs()
	os.MkdirAll(dirs.Config, 0o700)
	os.WriteFile(contract.SettingsPath(dirs), []byte("[keys]\ncommand = \"ctrl+space\"\n\n[scrollback]\nlines = 5000\n"), 0o600)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "k.sock")
	kp := exec.Command(self) // #nosec G204 -- this test's own program
	kp.Env = append(os.Environ(), envRole+"=keeper", contract.EnvSocket+"="+sock, "SHELL=/bin/sh", "HOME="+dir)
	kp.Stderr = os.Stderr
	if err = kp.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { kp.Process.Kill(); kp.Wait() })
	load := func() string {
		out, _ := exec.Command("uptime").Output()
		return strings.TrimSpace(string(out))
	}
	t.Logf("for %v; the machine before: %s", length, load())

	c := dial(t, sock)
	w := &window{client: dial(t, sock), hit: make(chan time.Time, 1)}
	w.conn.SetDeadline(time.Time{})
	w.send(contract.WireCall{Op: contract.OpAttach, W: 160, H: 50, System: sys.System(), Env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	go w.read()
	// The panes are known by what the keeper calls them, never by a guess:
	// the window may have been given a first tab of its own.
	began := time.Now()
	end := began.Add(length)
	file := func(i int) string { return filepath.Join(dir, "count-"+strconv.Itoa(i)) }
	var ids, tabs []string
	for i := range panes {
		pane := c.call(contract.WireCall{Op: contract.OpTabCreate, Label: fmt.Sprint("agent ", i), Cwd: dir, Env: []string{envRole + "=agent"}}).Pane
		ids, tabs = append(ids, pane.ID), append(tabs, pane.Tab)
		c.call(contract.WireCall{Op: contract.OpRun, Pane: pane.ID, Argv: []string{self, strconv.Itoa(i), strconv.FormatInt(end.Unix(), 10), file(i)}})
	}
	said := func() (r report) {
		data, _ := sys.Peek(reportPath(dir))
		json.Unmarshal(data, &r)
		return r
	}

	var echoes, calls, reads []time.Duration
	var rss, heap []uint64
	var layoutSize int64
	var goroutines, files []int
	for i := 0; time.Now().Before(end.Add(-2 * echoLost)); i++ {
		next := time.Now().Add(tick)
		if i%5 == 0 {
			c.call(contract.WireCall{Op: contract.OpTabFocus, Tab: tabs[i/5%panes]})
		}
		if i%150 == 149 {
			w.send(contract.WireCall{Op: contract.OpWindowSize, W: float64(120 + i/150%3*20), H: float64(40 + i/150%2*10)})
		}
		echoes = append(echoes, w.key(i))
		at := time.Now()
		c.call(contract.WireCall{Op: contract.OpScreen, Pane: ids[i*7%panes]})
		calls = append(calls, time.Since(at))
		// What a pane of ours does when one of its files changes.
		at = time.Now()
		contract.ReadSettings(sys.Peek, dirs)
		reads = append(reads, time.Since(at))
		if i%5 == 4 {
			r := said()
			heap, goroutines, files = append(heap, r.Heap), append(goroutines, r.Goroutines), append(files, r.Files)
			if out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(kp.Process.Pid)).Output(); err == nil { // #nosec G204 -- the keeper started above
				kb, _ := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
				rss = append(rss, kb<<10)
			}
			if info, err := os.Stat(restore.Path(dirs)); err == nil {
				layoutSize = max(layoutSize, info.Size())
			}
		}
		if i%3000 == 2999 {
			t.Logf("after %v: %s", time.Since(began).Round(time.Second), load())
		}
		time.Sleep(time.Until(next))
	}
	mid := said()

	// Every agent says what it printed; the keeper's process says what the
	// terminal handed the keeper; the keeper says what is on the screen.
	// So a pane that fails says which of the three it failed at.
	var total uint64
	for i, id := range ids {
		var want, got tally
		for wait := time.Now().Add(time.Minute); ; time.Sleep(50 * time.Millisecond) {
			data, _ := sys.Peek(file(i))
			if n, _ := fmt.Sscan(string(data), &want.N, &want.Sum); n == 2 || time.Now().After(wait) {
				break
			}
		}
		if want.N == 0 {
			t.Errorf("%s: its agent never said how much it printed: it is stuck, with the keeper not reading or the agent itself", id)
			continue
		}
		total += want.N
		at := time.Now()
		for wait := at.Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			if got = said().Read[id]; got.N >= want.N || time.Now().After(wait) {
				break
			}
		}
		late := time.Since(at).Round(100 * time.Millisecond)
		ends := []string{fmt.Sprintf(lastButOne, i), fmt.Sprintf(last, i)}
		var text []string
		for wait := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			text = strings.Split(c.call(contract.WireCall{Op: contract.OpScreen, Pane: id}).Text, "\n")
			if text = text[max(len(text)-2, 0):]; slices.Equal(text, ends) || time.Now().After(wait) {
				break
			}
		}
		switch {
		case got.N < want.N:
			t.Errorf("%s: LOST before the keeper, or the keeper stopped reading: the agent printed %d bytes and %v later its terminal had handed the keeper %d (%d short); the screen ends with %q",
				id, want.N, late, got.N, want.N-got.N, text)
		case got != want:
			t.Errorf("%s: NOT THE SAME BYTES: the agent printed %d with the sum %08x, the keeper was handed %d with the sum %08x", id, want.N, want.Sum, got.N, got.Sum)
		case !slices.Equal(text, ends):
			t.Errorf("%s: LOST OR LATE INSIDE the keeper: it was handed every one of the %d bytes, and ten seconds later its screen ends with %q, not %q", id, want.N, text, ends)
		case late > time.Second:
			t.Logf("%s: every byte arrived, the last of them %v after the agent had finished", id, late)
		}
	}
	lasted := time.Since(began)
	status := c.call(contract.WireCall{Op: contract.OpStatus}).Text
	loadAfter := load()

	// The keeper is stopped as a person stops it, and says what it left.
	c.send(contract.WireCall{Op: contract.OpStop})
	if err := kp.Wait(); err != nil {
		t.Errorf("the keeper's process ended with %v", err)
	}
	final := said()
	if !final.Ended {
		t.Fatalf("the keeper's process did not report after it stopped: %+v", final)
	}
	// One goroutine is left by design: `engine run` waits for a signal to
	// the end of its process. Each other one is a fault.
	var left []string
	for _, g := range strings.Split(final.Left, "\n\n") {
		if strings.Contains(g, "\n#") && !strings.Contains(g, "keeper.engine.func") && !strings.Contains(g, "os/signal.") && !strings.Contains(g, "pprof.") {
			left = append(left, g)
		}
	}
	if len(left) > 0 {
		t.Errorf("%d goroutines are left after the keeper stopped:\n%s", len(left), strings.Join(left, "\n\n"))
	}
	if final.Files > final.BaseFiles {
		t.Errorf("%d files are open after the keeper stopped, %d before it started", final.Files, final.BaseFiles)
	}

	slices.Sort(echoes)
	slices.Sort(calls)
	slices.Sort(reads)
	inTime, _ := slices.BinarySearch(echoes, echoMost+1)
	lost := len(echoes) - func() int { n, _ := slices.BinarySearch(echoes, echoLost); return n }()
	if part(echoes, 0.95) > echoMost {
		t.Errorf("a key's echo: 19 of 20 within %v, where the design allows %v", part(echoes, 0.95), echoMost)
	}
	// Growing: the middle of the last fifth of the run against the middle
	// of the second fifth, by which time every pane's scroll-back is full.
	// More than a tenth and 5 MB over is growth. A run under ten minutes
	// is too short to say.
	fifth := func(n int) uint64 {
		s := slices.Sorted(slices.Values(rss[len(rss)*n/5 : len(rss)*(n+1)/5]))
		return part(s, 0.5)
	}
	early, late, peak := uint64(0), uint64(0), uint64(0)
	if len(rss) >= 5 {
		early, late, peak = fifth(1), fifth(4), slices.Max(rss)
	}
	if peak > memMost {
		t.Errorf("the keeper's memory reached %s, where the design allows %s", mb(peak), mb(memMost))
	}
	if length >= 10*time.Minute && late > early+early/10+5<<20 {
		t.Errorf("the keeper's memory is growing: %s in the second fifth of the run, %s in the last", mb(early), mb(late))
	}
	cpu := kp.ProcessState.UserTime() + kp.ProcessState.SystemTime()
	per := func(nanos, n int64) time.Duration { return time.Duration(nanos / max(n, 1)) }
	t.Logf(`the figures, after %v:
the machine after: %s
the keeper at the end: %s
bytes printed by the %d agents, every one handed to the keeper and the last on each screen: %d (%s, %s a second)
a key's echo, %d keys: half within %v, 19 of 20 within %v, 99 of 100 within %v, the slowest %v; %d of them (%.2f%%) within %v; %d never seen in %v
a call that reads a screen, %d calls: half within %v, 19 of 20 within %v, the slowest %v
the keeper's memory as the system counts it: %s at most; %s in the second fifth of the run, %s in the last
the keeper's heap in use as Go counts it: %s at most, %s at the end; from the system in all %s
the keeper's processor time: %v, which is %.0f%% of one processor
goroutines: %d before the keeper started, %d at most while it ran, %d after it stopped (one waits for a signal by design); open files: %d before, %d at most, %d after
the layout file: %d writes (one every %v), %s at its largest, %v a write from reading the old file to replacing it
a watcher asking which program is in front: %d times (%.0f a second), %v each
one read of the settings file, %d reads: half within %v, 19 of 20 within %v, the slowest %v`,
		lasted.Round(time.Second), loadAfter, status,
		panes, total, mb(total), mb(uint64(float64(total)/lasted.Seconds())),
		len(echoes), part(echoes, 0.5), part(echoes, 0.95), part(echoes, 0.99), part(echoes, 1), inTime, 100*float64(inTime)/float64(max(len(echoes), 1)), echoMost, lost, echoLost,
		len(calls), part(calls, 0.5), part(calls, 0.95), part(calls, 1),
		mb(peak), mb(early), mb(late),
		mb(slices.Max(append(heap, 0))), mb(mid.Heap), mb(mid.Sys),
		cpu.Round(time.Second), 100*cpu.Seconds()/lasted.Seconds(),
		final.BaseGoroutines, slices.Max(append(goroutines, 0)), final.Goroutines, final.BaseFiles, slices.Max(append(files, 0)), final.Files,
		mid.Saves, (lasted / time.Duration(max(mid.Saves, 1))).Round(time.Millisecond), mb(uint64(layoutSize)), per(mid.SaveNanos, mid.Saves).Round(time.Microsecond),
		mid.Looks, float64(mid.Looks)/lasted.Seconds(), per(mid.LookNanos, mid.Looks).Round(time.Microsecond),
		len(reads), part(reads, 0.5), part(reads, 0.95), part(reads, 1))
}
