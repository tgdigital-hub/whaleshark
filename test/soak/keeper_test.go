package soak

import (
	"bytes"
	"encoding/json"
	"hash/crc32"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/keeper"
	"github.com/tgdigital-hub/whaleshark/internal/pty"
	"github.com/tgdigital-hub/whaleshark/internal/restore"
)

var looks, lookNanos, saves, lineSaves, wrote atomic.Int64

// counted is a pane's real terminal, with everything the keeper takes from
// it counted and summed on the way.
type counted struct {
	contract.Pty
	mu sync.Mutex
	tally
}

func (c *counted) Read(b []byte) (int, error) {
	n, err := c.Pty.Read(b)
	c.mu.Lock()
	c.N, c.Sum = c.N+uint64(n), crc32.Update(c.Sum, crc32.IEEETable, b[:n]) // #nosec G115 -- a length
	c.mu.Unlock()
	return n, err
}

func (c *counted) Front() (string, error) {
	began := time.Now()
	defer func() {
		looks.Add(1)
		lookNanos.Add(int64(time.Since(began)))
	}()
	return c.Pty.Front()
}

// disk is the real platform, with what the keeper writes for a restart
// counted: the layout file, the files beside it, and the bytes of both.
type disk struct {
	contract.Platform
	layout string
}

func (d disk) Replace(tmp, final string) error {
	if strings.HasPrefix(final, strings.TrimSuffix(d.layout, "json")) {
		if info, err := os.Stat(tmp); err == nil {
			wrote.Add(info.Size())
		}
		if final == d.layout {
			saves.Add(1)
		} else {
			lineSaves.Add(1)
		}
	}
	return d.Platform.Replace(tmp, final)
}

func openFiles() int {
	list, err := os.ReadDir("/dev/fd")
	if err != nil {
		return -1
	}
	return len(list)
}

// keep runs the real keeper, `engine run`, in this process, for the login
// in the folder of the socket, and reports on it until it has stopped.
func keep() {
	dir := filepath.Dir(os.Getenv(contract.EnvSocket))
	sys := login(dir)
	dirs, _ := sys.Dirs()
	kit := contract.NewKit()
	kit.Platform = disk{sys, restore.Path(dirs)}
	var mu sync.Mutex
	read := map[string]*counted{}
	kit.Pty = func(s contract.PtySpec) (contract.Pty, error) {
		tty, err := pty.Start(s)
		if err != nil {
			return nil, err
		}
		c := &counted{Pty: tty}
		for _, v := range s.Env {
			if pane, ok := strings.CutPrefix(v, contract.EnvPane+"="); ok {
				mu.Lock()
				read[pane] = c
				mu.Unlock()
			}
		}
		return c, nil
	}
	keeper.Plug(kit)
	var profile *os.File
	if path := os.Getenv(envProfile); path != "" {
		if profile, _ = os.Create(path); profile != nil { // #nosec G304 -- the file the person named
			pprof.StartCPUProfile(profile)
		}
	}
	// Go opens what it waits for files with at the first such wait, and
	// what it hears signals with when a program first asks for one, as
	// `engine run` does. Both are done before the count, or they would be
	// counted as the keeper's.
	if r, w, err := os.Pipe(); err == nil {
		r.Close()
		w.Close()
	}
	heard := make(chan os.Signal, 1)
	signal.Notify(heard, os.Interrupt)
	signal.Stop(heard)
	r := report{BaseGoroutines: runtime.NumGoroutine(), BaseFiles: openFiles()}
	say := func() {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		r.Heap, r.Sys, r.Goroutines, r.Files = m.HeapInuse, m.Sys, runtime.NumGoroutine(), openFiles()
		r.Saves, r.LineSaves, r.Wrote, r.Looks, r.LookNanos = saves.Load(), lineSaves.Load(), wrote.Load(), looks.Load(), lookNanos.Load()
		r.Read = map[string]tally{}
		mu.Lock()
		for pane, c := range read {
			c.mu.Lock()
			r.Read[pane] = c.tally
			c.mu.Unlock()
		}
		mu.Unlock()
		data, _ := json.Marshal(r)
		if os.WriteFile(reportPath(dir)+".part", data, 0o600) == nil {
			sys.Replace(reportPath(dir)+".part", reportPath(dir))
		}
	}
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-stop:
				return
			case <-time.After(200 * time.Millisecond):
				say()
			}
		}
	}()
	kit.Handler("engine")(&contract.Call{Kit: kit, Args: []string{"run"}, Out: io.Discard, Err: os.Stderr})
	close(stop)
	<-stopped
	if profile != nil {
		pprof.StopCPUProfile()
		profile.Close()
	}
	// What ends by itself is given two seconds to end.
	for end := time.Now().Add(2 * time.Second); runtime.NumGoroutine() > r.BaseGoroutines && time.Now().Before(end); {
		time.Sleep(10 * time.Millisecond)
	}
	var left bytes.Buffer
	pprof.Lookup("goroutine").WriteTo(&left, 1)
	r.Left, r.Ended = left.String(), true
	say()
}
