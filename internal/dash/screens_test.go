package dash

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
)

// drawn has the server hand over each page it draws in the place of text.
func (b *browser) drawn() *contract.Page {
	last := new(contract.Page)
	b.p.Kit.Page = func(w io.Writer, p *contract.Page) error {
		*last = *p
		_, err := io.WriteString(w, p.Screen)
		return err
	}
	return last
}

// The team's screen is the one view model; every other screen is the read
// command's own answer, read into what a screen is drawn from.
func TestEveryScreen(t *testing.T) {
	b := evening(t).signIn()
	page := b.drawn()
	b.refused("the team", b.ask("GET", contract.RouteTeam, nil, nil), http.StatusOK)
	v := page.View
	if page.Screen != contract.ScreenTeam || v == nil || v.Run != "r3" || len(v.Items) != 3 || v.Caller != contract.Human || page.Token != b.token() ||
		page.Theme == "" || !v.Fresh.Checked.Equal(contract.Now()) {
		t.Errorf("the team: screen %s, theme %q, view %v", page.Screen, page.Theme, v != nil)
	}
	b.refused("the plan", b.ask("GET", contract.RoutePlan, nil, nil), http.StatusOK)
	if page.Screen != contract.ScreenPlan || page.Plan == nil || len(page.Plan.Rows) < 12 || len(page.Plan.Chain) == 0 || page.View == nil || page.Said != nil {
		t.Errorf("the plan: %d rows, said %+v", len(page.Plan.Rows), page.Said)
	}
	b.refused("the messages", b.ask("GET", contract.RouteMessages, nil, nil), http.StatusOK)
	if page.Screen != contract.ScreenMessages || len(page.Messages) == 0 || page.Said != nil {
		t.Errorf("the messages: %d, said %+v", len(page.Messages), page.Said)
	}
	b.refused("the jobs", b.ask("GET", contract.RouteJobs, nil, nil), http.StatusOK)
	if page.Screen != contract.ScreenJobs || len(page.Jobs) != 1 || page.Jobs[0].View == nil || page.Jobs[0].View.Run != "r3" || page.Said != nil {
		t.Errorf("the jobs: %+v, said %+v", page.Jobs, page.Said)
	}
	b.refused("one task", b.ask("GET", contract.RouteTask+"T8", nil, nil), http.StatusOK)
	if page.Screen != contract.ScreenTask || page.Task == nil || page.Task.Card.Task != "T8" || len(page.Task.Tries) == 0 || page.Said != nil {
		t.Errorf("the task: %v, said %+v", page.Task != nil, page.Said)
	}
	for _, path := range []string{contract.RouteTask + "T99", contract.RouteTask + "--all", contract.RouteTask, "/no-such", contract.RouteTask + "T8/x"} {
		if b.refused(path, b.ask("GET", path, nil, nil), http.StatusNotFound); page.Screen != contract.ScreenRefused || page.Task != nil {
			t.Errorf("%s was drawn as %s", path, page.Screen)
		}
	}
	// Catch me up is a button whose answer is shown where it was pressed.
	w := b.ask("POST", contract.RouteDo+"catchup", url.Values{contract.FieldToken: {b.token()}}, func(r *http.Request) {
		r.Header.Set("Referer", own+contract.RoutePlan)
	})
	if b.refused("catch me up", w, http.StatusOK); page.Screen != contract.ScreenPlan || page.Catchup == nil || page.Catchup.To.IsZero() || page.Said != nil {
		t.Errorf("catch me up: on %s, %+v, said %+v", page.Screen, page.Catchup, page.Said)
	}
	// A fixed pointer goes to the lead agent's tab, for the item that offers it only.
	for _, a := range []string{"carry-on", "ask-lead"} {
		b.refused(a+" for an item that does not offer it", b.ask("POST", contract.RouteDo+a, url.Values{contract.FieldToken: {b.token()}, "id": {"q7"}}, nil), http.StatusConflict)
	}
}

// Which job a browser looks at is its own choice among the open runs of the
// login's own projects, and nothing else can be chosen.
func TestWhichJob(t *testing.T) {
	b := evening(t).signIn()
	page := b.drawn()
	for i, choice := range []string{"?root=/&run=r3", "?root=" + url.QueryEscape(b.p.Root+"/..") + "&run=r3", "?root=" + url.QueryEscape(b.p.Root) + "&run=r99"} {
		w := b.ask("GET", contract.RouteTeam+choice, nil, nil)
		if b.refused(choice, w, http.StatusOK); page.View.Run != "r3" {
			t.Errorf("%s: the page shows %q", choice, page.View.Run)
		}
		if i < 2 && len(w.Result().Cookies()) != 0 {
			t.Errorf("%s was kept as the browser's choice", choice)
		}
	}
	w := b.ask("GET", contract.RouteTeam+"?root="+url.QueryEscape(b.p.Root)+"&run=r3", nil, nil)
	if got := w.Result().Cookies(); len(got) != 1 || got[0].Name != b.s.cookie+"j" || !got[0].HttpOnly {
		t.Errorf("the choice was not kept: %v", got)
	}
}

// With no open run the page says so and a button runs nothing.
func TestWithNoOpenRun(t *testing.T) {
	b := login(t, nil).signIn()
	page := b.drawn()
	for _, path := range []string{contract.RouteTeam, contract.RoutePlan, contract.RouteTask + "T1"} {
		if b.refused(path, b.ask("GET", path, nil, nil), http.StatusOK); page.View == nil || len(page.View.Alerts) != 1 || page.Plan != nil || page.Task != nil {
			t.Errorf("%s with no open run: %+v", path, page.View)
		}
	}
	b.refused("a button with no open run", b.ask("POST", contract.RouteDo+"stop-all", url.Values{contract.FieldToken: {b.token()}}, nil), http.StatusOK)
	if w := b.ask("GET", contract.RouteSites, nil, nil); w.Body.String() != "[]\n" {
		t.Errorf("the sites with no open run: %q", w.Body)
	}
}

type answering struct {
	contract.NoEvidence
	up []int
}

func (a answering) Answering(ports []int) []int {
	return slices.DeleteFunc(slices.Clone(ports), func(p int) bool { return !slices.Contains(a.up, p) })
}

// The running sites: the ports of live tasks' slots that answer, and no
// other port, whatever listens there.
func TestTheRunningSites(t *testing.T) {
	b := evening(t)
	page := b.drawn()
	k := b.p.Kit
	state, _ := b.p.Record()
	var done string
	for id, task := range state.Tasks {
		if task.Status == contract.TaskDone {
			done = id
		}
	}
	slot := func(root, run, task string) int {
		got, err := contract.TakeSlot(k.Platform, contract.DefaultPorts, root, run, task, 10)
		if err != nil {
			t.Fatal(err)
		}
		return got.First(contract.DefaultPorts)
	}
	live, other, settled, gone := slot(b.p.Root, "r3", "T3"), slot(b.p.Root, "r3", "T4"), slot(b.p.Root, "r3", done), slot(b.p.Root, "r2", "T3")
	k.Evidence = answering{up: []int{live + 3, live + 1, settled, gone, 22, 8080}}
	w := b.ask("GET", contract.RouteSites, nil, nil)
	var ports []int
	if err := json.Unmarshal(w.Body.Bytes(), &ports); err != nil || !slices.Equal(ports, []int{live + 1, live + 3}) {
		t.Errorf("the sites: %s (%v), want %d and %d; T4's slot begins at %d and nothing answers there", w.Body, err, live+1, live+3, other)
	}
	b.signIn()
	if b.ask("GET", contract.RouteTeam, nil, nil); len(page.Sites) != 1 || page.Sites["T3"] != live+1 {
		t.Errorf("the team's sites: %v", page.Sites)
	}
}

// The two sources of news, and letting go: a held tab is told of a change
// the keeper reports; with no tab held the server looks at nothing; and
// past a few held tabs the oldest is let go.
func TestNewsAndLettingGo(t *testing.T) {
	f, _ := testkit.Load(testkit.Evening)
	b := evening(t)
	if b.s.unlook != nil {
		t.Fatal("the server looks before any tab is held")
	}
	tab := b.s.join()
	time.Sleep(200 * time.Millisecond) // the connection to the keeper is opened
	for len(tab) > 0 {
		<-tab
	}
	b.p.Double.Push(contract.StatusBlocked, f.State.Attempts["T3.1"].Place.Pane)
	select {
	case <-tab:
	case <-time.After(2 * time.Second):
		t.Error("a change the keeper reported was not told to the held tab")
	}
	tabs := []chan struct{}{tab}
	for range tabsMost {
		tabs = append(tabs, b.s.join())
	}
	// A line that was waiting is read first; then the tab is found let go.
	for held, wait := true, time.After(2*time.Second); held; {
		select {
		case _, held = <-tab:
		case <-wait:
			t.Fatal("the oldest tab is still held")
		}
	}
	for _, tab := range tabs {
		b.s.leave(tab)
	}
	if b.s.mu.Lock(); b.s.unlook != nil || len(b.s.tabs) != 0 {
		t.Error("the server still looks with no tab held")
	}
	b.s.mu.Unlock()
}
