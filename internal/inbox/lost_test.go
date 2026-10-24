package inbox

import (
	"sync"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/watch"
)

// screen is a pane as a Watcher reads it: what its agent shows, and how
// often that was drawn.
type screen struct {
	mu   sync.Mutex
	text string
	n    uint64
}

func (s *screen) show(text string) {
	s.mu.Lock()
	s.text, s.n = text, s.n+1
	s.mu.Unlock()
}

func (s *screen) Text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.text
}

func (s *screen) Count() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

func (*screen) Front() (string, error) { return "", contract.ErrCannotTell }
func (*screen) Paste() bool            { return false }
func (*screen) Type([]byte) error      { return nil }

// Seen in r8: two workers reported, sync sent them back, and each agent sat
// at its prompt for half an hour while every pointer waited "until it is
// idle". The hook that ended its turn never reached the keeper, and the
// agent of today shows neither of its kind's words at rest, so its pane read
// as at work. Here the real Watcher follows the pane: the first sweep after
// it has found the agent at rest types the pointer.
func TestAWorkerSentBackWhoseStopWasLost(t *testing.T) {
	w := evening(t, nil)
	pane, pointer := w.pane("T1.1"), string(contract.PointMail)
	var mu sync.Mutex
	var last string
	var rested time.Time
	scr := &screen{}
	scr.show("✶ Building… (2s · ↓ 1.2k tokens)\n\n❯ \n\n  ⏵⏵ auto mode on (shift+tab to cycle)")
	agent := watch.New(scr, "claude", "", func(st watch.State) {
		mu.Lock()
		if last = st.Status; last == contract.StatusIdle && rested.IsZero() {
			rested = time.Now()
		}
		mu.Unlock()
		w.p.Double.Drop(st.Status, pane)
	})
	defer agent.Close()
	agent.Hook("UserPromptSubmit", "s", "")

	// It reports done, and sync sends it back with its brief, as sync does.
	w.change(func(s *contract.State, now time.Time) error {
		if _, no, err := rule.Report(s, "T1.1", s.Attempts["T1.1"].TokenHash, "done", "built", nil, now); no != nil || err != nil {
			t.Fatalf("report: %v %v", no, err)
		}
		if err := rule.Reject(s, "T1", "it has to be brought up to date with the collected work first", contract.Live, now); err != nil {
			return err
		}
		_, err := rule.Tell(s, "T1", "----- sync brief for T1 -----", "", now)
		return err
	})
	// Then it ends its turn, and its Stop is lost.
	scr.show("❯ \n\n  ⏵⏵ auto mode on (shift+tab to cycle)")
	for start := time.Now(); w.typed(pane, pointer) == 0; time.Sleep(time.Second) {
		mu.Lock()
		atRest, status := !rested.IsZero(), last
		mu.Unlock()
		if time.Since(start) > patience/2 {
			t.Fatalf("no pointer after %v: the agent at its prompt reads as %s", patience/2, status)
		}
		if w.after(5 * time.Second); atRest && w.typed(pane, pointer) == 0 {
			t.Fatal("a sweep found the agent at rest and typed nothing")
		}
	}
	if a := w.state().Attempts["T1.1"]; a.State != contract.AttemptWorking || a.Mail[len(a.Mail)-1].NudgedAt.IsZero() {
		t.Fatalf("after the pointer: %s, %+v", a.State, a.Mail)
	}
}
