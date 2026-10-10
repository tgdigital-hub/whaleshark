package pty

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"
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

// seen waits for the words to be printed and gives all that was.
func (r *reading) seen(want string) (text string, ok bool) {
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(time.Millisecond) {
		r.mu.Lock()
		text = r.text.String()
		r.mu.Unlock()
		if strings.Contains(text, want) || time.Now().After(deadline) {
			return text, strings.Contains(text, want)
		}
	}
}

func (r *reading) until(t *testing.T, want string) {
	t.Helper()
	if text, ok := r.seen(want); !ok {
		t.Fatalf("%q never came; printed: %q", want, text)
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
	// A terminal that never ends must not hold a run for the ten minutes
	// the testing package allows: every goroutine is printed long before.
	alarm := time.AfterFunc(4*time.Minute, func() {
		debug.SetTraceback("all")
		panic("a thousand shells took more than four minutes")
	})
	defer alarm.Stop()
	var all sync.WaitGroup
	turn := make(chan int)
	for range atOnce {
		all.Go(func() {
			for i := range turn {
				if err := oneShell(t, i); err != nil {
					t.Errorf("shell %d: %v", i, err)
				}
			}
		})
	}
	// The first failures say what is wrong, and a thousand more say no more.
	for i := 0; i < shells && !t.Failed(); i++ {
		turn <- i
	}
	close(turn)
	all.Wait()
}

// oneShell is one turn of the thousand. It runs beside the test's own
// goroutine, so it hands back what went wrong and ends nothing itself; the
// terminal is closed and waited for whatever happened before.
func oneShell(t *testing.T, i int) error {
	mark := fmt.Sprintf("mark%d", i)
	p, err := Start(contract.PtySpec{Argv: shell, Dir: os.TempDir(),
		Env: append(os.Environ(), "WS_MARK="+mark), Cols: 80, Rows: 24})
	if err != nil {
		return err
	}
	read := reader(p)
	said := func(keys, want string) error {
		if _, err := p.Write([]byte(keys)); err != nil {
			return err
		}
		if text, ok := read.seen(want); !ok {
			return fmt.Errorf("%q never came; printed: %q", want, text)
		}
		return nil
	}
	if err = said(sayMark+"\r", "got-"+mark); err == nil {
		err = p.Resize(100+i%50, 30+i%20)
	}
	if err == nil && i%10 == 0 {
		err = said(linger+"\r"+sayMark+"-again\r", "got-"+mark+"-again")
	}
	left := watch(t, p)
	if cerr := p.Close(); err == nil {
		err = cerr
	}
	if _, werr := p.Wait(); err == nil {
		err = werr
	}
	<-read.end
	if still := left(); err == nil && len(still) != 0 {
		err = fmt.Errorf("it left processes: %v", still)
	}
	return err
}
