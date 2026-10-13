package keeper

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// A bench, here for a few hosted runs only: how often does the end of a
// connection the other side closed fail to arrive? It fails on purpose
// where it runs, so that what it counted is printed.

// ended is what one connection saw after its answer.
type ended struct {
	stalls, noAnswer, n atomic.Int64
	mu                  sync.Mutex
	notes               []string
}

func (e *ended) note(format string, args ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.notes) < 12 {
		e.notes = append(e.notes, fmt.Sprintf(format, args...))
	}
}

// probe says a hello of another version, reads the answer and waits two
// seconds for the end. Where none comes it asks again, writes, and asks once more.
func (e *ended) probe(path string) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		e.note("dial: %v", err)
		return
	}
	defer conn.Close()
	e.n.Add(1)
	body, _ := json.Marshal(contract.WireCall{V: contract.WireVersion + 1, ID: 7, Op: contract.OpHello})
	if err = contract.WriteFrame(conn, contract.FrameCall, body); err != nil {
		e.note("write: %v", err)
		return
	}
	conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	if _, _, err = contract.ReadFrame(conn); err != nil {
		e.noAnswer.Add(1)
		e.note("no answer: %v", err)
		return
	}
	start := time.Now()
	conn.SetReadDeadline(start.Add(2 * time.Second))
	if _, _, err = contract.ReadFrame(conn); !errors.Is(err, os.ErrDeadlineExceeded) {
		if d := time.Since(start); d > 200*time.Millisecond {
			e.note("a late end, after %v: %v", d, err)
		}
		return
	}
	e.stalls.Add(1)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, _, again := contract.ReadFrame(conn)
	conn.SetWriteDeadline(time.Now().Add(time.Second))
	_, wrote := conn.Write([]byte{0})
	_, wrote2 := conn.Write([]byte{0})
	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, _, last := contract.ReadFrame(conn)
	e.note("NO END in 2s; read again: %v; write: %v; second write: %v; read after: %v", again, wrote, wrote2, last)
}

func (e *ended) String() string {
	return fmt.Sprintf("%d connections, %d without an end, %d without an answer %s", e.n.Load(), e.stalls.Load(), e.noAnswer.Load(), strings.Join(e.notes, " | "))
}

// plain is a listener with nothing of the keeper in it: each connection is
// read once, answered once and closed.
func plain(t *testing.T) (string, net.Listener) {
	dir, err := os.MkdirTemp("", "ws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "p.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if _, _, err := contract.ReadFrame(conn); err == nil {
					contract.WriteFrame(conn, contract.FrameReply, []byte(`{"id":7,"err":"wire_version"}`))
				}
			}()
		}
	}()
	return path, ln
}

func TestBenchTheEndOfAConnection(t *testing.T) {
	if os.Getenv("CI") == "" && os.Getenv("WS_BENCH") == "" {
		t.Skip("a bench for the hosted machines")
	}
	unit := 10 * time.Second
	if s := os.Getenv("WS_BENCH"); s != "" {
		unit, _ = time.ParseDuration(s)
	}
	// many has four connections at a time ask one listener, for a while.
	many := func(path string, d time.Duration) *ended {
		e, end := &ended{}, time.Now().Add(d)
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				for time.Now().Before(end) && e.stalls.Load() < 10 {
					e.probe(path)
				}
			})
		}
		wg.Wait()
		return e
	}
	var report []string
	path, ln := plain(t)
	report = append(report, "plain listener, four at a time: "+many(path, 6*unit).String())
	ln.Close()

	r := newRig(t)
	report = append(report, "one keeper, four at a time: "+many(os.Getenv(contract.EnvSocket), 6*unit).String())
	r.k.stop()

	e := &ended{}
	for end := time.Now().Add(3 * unit); time.Now().Before(end) && e.stalls.Load() < 10; {
		path, ln := plain(t)
		e.probe(path)
		ln.Close()
	}
	report = append(report, "a new plain listener each time, its first connection: "+e.String())

	e = &ended{}
	for end := time.Now().Add(5 * unit); time.Now().Before(end) && e.stalls.Load() < 10; {
		t.Run("rig", func(t *testing.T) {
			newRig(t)
			e.probe(os.Getenv(contract.EnvSocket))
		})
	}
	report = append(report, "a new keeper each time, its first connection: "+e.String())
	t.Errorf("BENCH\n%s", strings.Join(report, "\n"))
}
