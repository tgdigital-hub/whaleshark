package panes

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/term/termtest"
)

// The left section opens the fleet beside the main agent's tab once that
// tab shows, with no job open: the fleet alone and beside the conversation,
// once for that tab, so a fleet the person closed stays closed; a job that
// starts then places the panes again and opens no second fleet.
func TestTheFleetOpensWithTheMainTabBeforeAnyJob(t *testing.T) {
	d := newDesk(t)
	main, err := d.double.TabCreate("", "", "/project", contract.MainTab, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	p := newPane(side, termtest.New(30, 40).Term, d.k, "", "", time.Now)
	p.bg, p.cfg = ctx, contract.PersonDefaults()
	look := func() {
		p.picture, _ = d.double.Snapshot(ctx)
		p.board()
	}
	ours := func() (ids []string) {
		for _, q := range d.panes() {
			if q.Ours {
				ids = append(ids, q.ID)
			}
		}
		return ids
	}
	soon := func(so func() bool) bool {
		for end := time.Now().Add(5 * time.Second); !so(); time.Sleep(10 * time.Millisecond) {
			if time.Now().After(end) {
				return false
			}
		}
		return true
	}

	look() // the first tab shows: no main agent's tab
	time.Sleep(100 * time.Millisecond)
	if got := d.did(); got != "" {
		t.Fatalf("beside a tab that is not the main agent's the left section asked for\n%s", got)
	}
	if err := d.double.TabFocus(main.Tab); err != nil {
		t.Fatal(err)
	}
	look()
	if !soon(func() bool { return len(ours()) == 1 }) {
		t.Fatalf("the main agent's tab shows and no fleet opened: %+v", d.panes())
	}
	self, _ := d.k.Platform.SelfPath()
	if got, want := d.did(), "split "+main.ID+" right 36\nrun "; !strings.HasPrefix(got, want) || !strings.Contains(got, self+" ui run fleet") || strings.Contains(got, "actions") {
		t.Errorf("the left section asked for\n%s\nwant the fleet alone beside the conversation", got)
	}
	// Closed by the person, it stays closed while the tab shows again.
	if _, err := d.ui(main.ID, fleet, "off"); err != nil || len(ours()) != 0 {
		t.Fatalf("ui fleet off: %v, %+v", err, d.panes())
	}
	d.did()
	look()
	time.Sleep(100 * time.Millisecond)
	if got := d.did(); got != "" {
		t.Errorf("a fleet the person closed was opened again:\n%s", got)
	}
	// Opened again, a job's start places both panes anew: one fleet stands.
	if _, err := d.ui(main.ID, fleet, "on"); err != nil {
		t.Fatal(err)
	}
	if err := Panes(d.k, main.ID); err != nil {
		t.Fatal(err)
	}
	o, err := d.ui(main.ID, actions, "on")
	if got := ours(); err != nil || len(got) != 2 || !slices.Contains(got, o.Fleet) || !slices.Contains(got, o.Actions) {
		t.Errorf("after a job's start the panes of ours are %v: %+v, %v", got, o, err)
	}
}

// With the fleet set off, no tab opens it.
func TestTheFleetSetOffOpensWithNoTab(t *testing.T) {
	d := newDesk(t)
	main, err := d.double.TabCreate("", "", "/project", contract.MainTab, nil)
	if err == nil {
		err = d.double.TabFocus(main.Tab)
	}
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	p := newPane(side, termtest.New(30, 40).Term, d.k, "", "", time.Now)
	p.bg, p.cfg = ctx, contract.PersonDefaults()
	p.cfg.Panes.Fleet = contract.Off
	p.picture, _ = d.double.Snapshot(ctx)
	d.did()
	p.board()
	time.Sleep(200 * time.Millisecond)
	if got := d.did(); got != "" {
		t.Errorf("a fleet set off was opened:\n%s", got)
	}
}

// Before any job the fleet says so, and how one begins.
func TestTheFleetBeforeAnyJob(t *testing.T) {
	p, _, _ := still(t, fleet, 46, 20)
	p.state, p.cards, p.make, p.stale = nil, nil, func(contract.ViewInput) contract.View { return contract.View{} }, true
	p.draw()
	if text := frame(p); !strings.Contains(text, "| no job yet ") || !strings.Contains(text, "| tell your agent what you want done") {
		t.Errorf("the fleet before any job:\n%s", text)
	}
}
