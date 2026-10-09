package fakeherdr

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/herdr"
)

// A measurement, not a test: it fails on purpose so that the hosted Windows
// machine prints what it found. It is removed once it has been read. It
// runs the random cuts many times over, first on a machine left alone and
// then beside sixteen programs that do nothing but use a processor, and
// notes where every goroutine stood whenever the reader was slow to connect
// again.
func TestMeasureACutOnWindows(t *testing.T) {
	const busy = "WHALESHARK_MEASURE_BUSY"
	if os.Getenv(busy) != "" {
		for end := time.Now().Add(40 * time.Second); time.Now().Before(end); {
		}
		return
	}
	var out strings.Builder
	var mu sync.Mutex
	var took []time.Duration
	dumps := 0
	for round := range 24 {
		if round == 8 {
			for range 16 {
				spin := exec.Command(os.Args[0], "-test.run=TestMeasureACutOnWindows")
				spin.Env = append(os.Environ(), busy+"=1")
				if err := spin.Start(); err != nil {
					t.Fatal(err)
				}
				defer spin.Process.Kill()
			}
			time.Sleep(time.Second)
			fmt.Fprintln(&out, "from here on beside sixteen busy programs:")
		}
		f := start(t)
		ctx, cancel := context.WithCancel(context.Background())
		built := make(chan []contract.Pane, 1)
		built <- nil
		var connections atomic.Int32
		var since atomic.Int64 // when the reader began to connect; 0 while it reads
		go func() {
			for ctx.Err() == nil {
				began := time.Now()
				since.Store(began.UnixNano())
				pic, events, err := f.Events(ctx)
				if err != nil {
					continue
				}
				since.Store(0)
				mu.Lock()
				took = append(took, time.Since(began))
				mu.Unlock()
				connections.Add(1)
				<-built
				built <- pic.Panes
				for e := range events {
					herdr.Apply(pic, e)
					<-built
					built <- pic.Panes
				}
			}
		}()
		go func() {
			for ctx.Err() == nil {
				time.Sleep(5 * time.Millisecond)
				at := since.Load()
				if at == 0 || time.Since(time.Unix(0, at)) < time.Second {
					continue
				}
				mu.Lock()
				if dumps < 2 {
					dumps++
					where := make([]byte, 1<<15)
					where = where[:runtime.Stack(where, true)]
					fmt.Fprintf(&out, "round %d: connecting for %v, everything stood:\n%s\n", round, time.Since(time.Unix(0, at)), where)
				}
				mu.Unlock()
				time.Sleep(200 * time.Millisecond)
			}
		}()
		random := rand.New(rand.NewPCG(uint64(round), 2))
		states := []string{contract.StatusWorking, contract.StatusIdle, contract.StatusDone, contract.StatusBlocked, "gone", "focused"}
		began, cuts, behind := time.Now(), 0, time.Duration(0)
		for step := range 1500 {
			all := panes(t, f)
			if len(all) == 0 {
				f.TabCreate("/work/shop", "tab", nil)
				continue
			}
			pick := all[random.IntN(len(all))]
			switch n := random.IntN(100); {
			case n < 4 && len(all) < 25:
				tab, _ := f.TabCreate("/work/shop", "tab", nil)
				f.AgentStart("agent-"+strings.ReplaceAll(tab.ID, ":", "-"), "claude", tab.ID, nil, time.Minute)
			case n < 6 && len(all) < 25:
				f.Split(pick.ID, "down", 0.5)
			case n < 8 && len(all) > 3:
				f.PaneClose(pick.ID)
			case n < 10 && len(all) > 3:
				f.TabClose(pick.Tab)
			case n < 14:
				f.TabRename(pick.Tab, "name "+pick.ID)
			case n < 16:
				f.TabFocus(pick.Tab)
			case n < 18:
				f.Cut()
				cuts++
			case n < 20 && pick.Agent == "":
				f.AgentStart("again-"+strings.ReplaceAll(pick.ID, ":", "-"), "claude", pick.ID, nil, time.Minute)
			default:
				f.Push(states[random.IntN(len(states))], pick.ID)
			}
			if step%300 == 299 {
				wait := time.Now()
				for range 600 {
					got := <-built
					built <- got
					if slices.Equal(got, panes(t, f)) {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				behind = max(behind, time.Since(wait))
			}
		}
		fmt.Fprintf(&out, "round %2d: %v, %d cuts, %d connections, longest wait to catch up %v\n",
			round, time.Since(began).Round(time.Millisecond), cuts, connections.Load(), behind.Round(time.Millisecond))
		cancel()
		f.Close()
	}
	mu.Lock()
	slices.Sort(took)
	fmt.Fprintf(&out, "%d subscriptions: middle %v, nine in ten under %v, the five longest %v\n",
		len(took), took[len(took)/2], took[len(took)*9/10], took[len(took)-5:])
	mu.Unlock()
	t.Fatalf("measured:\n%s", out.String())
}
