package inbox

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/launch"
	"github.com/tgdigital-hub/whaleshark/internal/rules"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// What an agent tool prints at its usage limit names the reset in a few
// ways; the next such moment is read, and nothing where none is said.
func TestTheResetIsRead(t *testing.T) {
	where := time.FixedZone("here", -7*3600)
	now := time.Date(2026, 10, 23, 21, 14, 0, 0, where)
	for screen, want := range map[string]time.Time{
		"5-hour limit reached ∙ resets 11pm":                              time.Date(2026, 10, 23, 23, 0, 0, 0, where),
		"limit reached ∙ resets 3:30am":                                   time.Date(2026, 10, 24, 3, 30, 0, 0, where),
		"Your limit will reset at 9pm (UTC).":                             time.Date(2026, 10, 24, 21, 0, 0, 0, time.UTC),
		"old line: resets 1pm\nWeekly limit reached ∙ resets Oct 27, 9am": time.Date(2026, 10, 27, 9, 0, 0, 0, where),
		"resets 12am":               time.Date(2026, 10, 24, 0, 0, 0, 0, where),
		"working on the reset page": {},
		"resets 25pm":               {},
	} {
		if got := resets(screen, now); !got.Equal(want) {
			t.Errorf("%q reads as %v, want %v", screen, got, want)
		}
	}
}

// screened is the terminals with one text on every screen.
type screened struct {
	contract.Terminals
	text string
}

func (s screened) Screen(string) (string, error) { return s.text, nil }

// Most workers stopping together with a usage limit on a screen pause the
// run once, with the reset their tool printed: the gated agents are held,
// nothing starts, the person's item says when, and at that moment the run
// is resumed. Without a limit on any screen nothing is paused.
func TestAUsageLimitPausesUntilItsReset(t *testing.T) {
	w := evening(t, crew(4))
	w.sweep()
	for i := range 3 {
		w.p.Double.Drop(contract.StatusIdle, "w1:p3"+string(rune('0'+i)))
	}
	w.after(5 * time.Second)
	q := item(w.state(), contract.CauseTogether)
	if q == nil || w.state().Run.Paused != nil {
		t.Fatalf("with no limit on a screen: %+v, paused %+v", q, w.state().Run.Paused)
	}
	k := *w.p.Kit
	rules.Plug(&k)
	k.Terms, k.Hold = screened{w.p.Double, "5-hour limit reached ∙ resets 11pm"}, launch.Hold
	sw, now, hold := sweeper{&k}, q.CreatedAt, contract.HoldPath(w.p.Root, "r3")
	sw.limit(w.p.Root, "r3", now)
	p := w.state().Run.Paused
	if p == nil || p.By != contract.LimitBy || p.Until.Hour() != 23 || !p.Until.After(now) || p.Until.Sub(now) > 24*time.Hour {
		t.Fatalf("paused as %+v at %v", p, now)
	}
	if q := item(w.state(), contract.CauseTogether); !strings.Contains(q.Text, "resumes by itself at "+p.Until.Local().Format("15:04")) {
		t.Errorf("the item says %q", q.Text)
	}
	if err := launch.Hold(&k, w.p.Root, "r3"); err != nil || !fileThere(hold) {
		t.Errorf("the gated agents are not held: %v", err)
	}
	if out, _, exit := w.run(scenario.Orch, "start", "--ready"); exit != contract.ExitRefused {
		t.Errorf("a start while paused at the limit: exit %d\n%s", exit, out)
	}
	sw.limit(w.p.Root, "r3", p.Until.Add(-time.Minute))
	if w.state().Run.Paused == nil {
		t.Fatal("resumed before the reset")
	}
	sw.limit(w.p.Root, "r3", p.Until)
	s := w.state()
	if s.Run.Paused != nil || s.Inbox.Events[len(s.Inbox.Events)-1].Kind != "resumed" {
		t.Fatalf("not resumed at the reset: %+v", s.Run.Paused)
	}
	if launch.Hold(&k, w.p.Root, "r3"); fileThere(hold) {
		t.Error("the hold file stayed after the reset")
	}
	// The same item does not pause the run a second time.
	if sw.limit(w.p.Root, "r3", p.Until.Add(time.Minute)); w.state().Run.Paused != nil {
		t.Fatal("paused again for the item it was paused for")
	}
	for text, met := range map[string]bool{"Claude usage limit reached.": true, "You've hit your limit · resets 3pm": true, "the rate limiter is done": false, "limits.go: 3 tests pass": false} {
		if limitLine.MatchString(text) != met {
			t.Errorf("%q says a limit was met: %v", text, !met)
		}
	}
}

func fileThere(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
