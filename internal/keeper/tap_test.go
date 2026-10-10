//go:build unix

package keeper

import (
	"bytes"
	"fmt"
	"os"
	"runtime/pprof"
	"strconv"
	"sync"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// A bench for one hosted run, to be taken out again: every byte a noisy
// shell's terminal gave, kept beside what the keeper counted.
type tap struct {
	contract.Pty
	mu  sync.Mutex
	all []byte
	end string
}

var (
	tapMu sync.Mutex
	taps  = map[string]*tap{}
	dump  sync.Once
)

func tapped(start func(contract.PtySpec) (contract.Pty, error)) func(contract.PtySpec) (contract.Pty, error) {
	return func(s contract.PtySpec) (contract.Pty, error) {
		tty, err := start(s)
		if err != nil || len(s.Argv) != 4 {
			return tty, err
		}
		t := &tap{Pty: tty}
		tapMu.Lock()
		taps[s.Argv[3]] = t
		tapMu.Unlock()
		return t, nil
	}
}

func (t *tap) Read(p []byte) (int, error) {
	n, err := t.Pty.Read(p)
	t.mu.Lock()
	t.all = append(t.all, p[:n]...)
	if err != nil {
		t.end = err.Error()
	}
	t.mu.Unlock()
	return n, err
}

// told is what the terminal of a count file gave, against the lines the
// shell says it printed: where it first differs, and how often.
func told(file, words string) string {
	tapMu.Lock()
	t := taps[file]
	tapMu.Unlock()
	if t == nil {
		return "no tap"
	}
	dump.Do(func() { pprof.Lookup("goroutine").WriteTo(os.Stderr, 2) })
	t.mu.Lock()
	defer t.mu.Unlock()
	out := fmt.Sprintf("the terminal gave %d bytes, its read ended with %q;", len(t.all), t.end)
	rows := bytes.SplitAfter(t.all, []byte("\n"))
	next, odd := 0, 0
	for i, row := range rows {
		if string(row) == fmt.Sprintf("%08d %s\r\n", next, words) {
			next++
			continue
		}
		if odd++; odd <= 6 {
			before := []byte{}
			if i > 0 {
				before = rows[i-1]
			}
			out += fmt.Sprintf("\n  row %d of %d, wanted line %d: %q after %q", i, len(rows), next, row, before)
		}
		if n, err := strconv.Atoi(string(row[:min(8, len(row))])); err == nil && len(row) > 8 {
			next = n + 1
		}
	}
	return out + fmt.Sprintf("\n  %d rows differ", odd)
}
