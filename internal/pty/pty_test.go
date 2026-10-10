package pty

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// reading collects what a terminal prints, on a goroutine of its own, as
// the keeper will.
type reading struct {
	mu   sync.Mutex
	text strings.Builder
	end  chan struct{}
}

func reader(p contract.Pty) *reading {
	r := &reading{end: make(chan struct{})}
	go func() {
		defer close(r.end)
		buf := make([]byte, 4096)
		for {
			n, err := p.Read(buf)
			r.mu.Lock()
			r.text.Write(buf[:n])
			r.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return r
}

func (r *reading) until(t *testing.T, want string) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(time.Millisecond) {
		r.mu.Lock()
		text := r.text.String()
		r.mu.Unlock()
		if strings.Contains(text, want) {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("%q never came; printed: %q", want, text)
		}
	}
}

func type_(t *testing.T, p contract.Pty, keys string) {
	t.Helper()
	if _, err := p.Write([]byte(keys)); err != nil {
		t.Fatal(err)
	}
}

func TestPlugPutsTheStarterIntoTheKit(t *testing.T) {
	k := contract.NewKit()
	Plug(k)
	if _, err := k.Pty(contract.PtySpec{}); err == nil || errors.Is(err, contract.ErrNotBuilt) {
		t.Fatalf("an empty start gave %v", err)
	}
}

// The row's check: a shell is started, typed to, resized and ended a
// thousand times, four at a time, and no process of any of them is left.
// One in ten leaves programs running behind it, one of which ignores the
// hang-up.
func TestAThousandShellsAndNoProcessLeft(t *testing.T) {
	const shells, atOnce = 1000, 4
	var all sync.WaitGroup
	turn := make(chan int)
	for range atOnce {
		all.Go(func() {
			for i := range turn {
				mark := fmt.Sprintf("mark%d", i)
				p, err := Start(contract.PtySpec{Argv: shell, Dir: os.TempDir(),
					Env: append(os.Environ(), "WS_MARK="+mark), Cols: 80, Rows: 24})
				if err != nil {
					t.Errorf("shell %d: %v", i, err)
					continue
				}
				read := reader(p)
				type_(t, p, sayMark+"\r")
				read.until(t, "got-"+mark)
				if err := p.Resize(100+i%50, 30+i%20); err != nil {
					t.Errorf("shell %d: resize: %v", i, err)
				}
				if i%10 == 0 {
					type_(t, p, linger+"\r"+sayMark+"-again\r")
					read.until(t, "got-"+mark+"-again")
				}
				left := watch(t, p)
				if err := p.Close(); err != nil {
					t.Errorf("shell %d: close: %v", i, err)
				}
				if _, err := p.Wait(); err != nil {
					t.Errorf("shell %d: wait: %v", i, err)
				}
				<-read.end
				if still := left(); len(still) != 0 {
					t.Errorf("shell %d left processes: %v", i, still)
				}
			}
		})
	}
	for i := range shells {
		turn <- i
	}
	close(turn)
	all.Wait()
}
