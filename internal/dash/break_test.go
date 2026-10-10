package dash

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// What the third security review tried beyond the rows' own tests.

// Any page open in the person's browser can ask for the sign-in address with
// a wrong code, as often as it likes. That slows wrong codes; it must never
// keep the right one out, whose signature nobody can guess.
func TestWrongCodesNeverKeepTheRightOneOut(t *testing.T) {
	b := evening(t)
	for range slowFrom + 3 {
		b.ask("GET", contract.RouteIn+"?code=1.x", nil, nil)
	}
	b.refused("one more wrong code", b.ask("GET", contract.RouteIn+"?code=1.x", nil, nil), http.StatusTooManyRequests)
	right := newCode(b.s.key, "in", contract.Now())
	b.refused("the right code in the middle of a flood of wrong ones", b.ask("GET", contract.RouteIn+"?code="+right, nil, nil), http.StatusSeeOther)
	b.refused("that code a second time", b.ask("GET", contract.RouteIn+"?code="+right, nil, nil), http.StatusForbidden)
}

// The way back after a button is a path of this page, whatever the request
// says it came from.
func TestTheWayBackIsAPathOfThePage(t *testing.T) {
	b := evening(t).signIn()
	for _, from := range []string{"https://elsewhere.example/plan", own + "//elsewhere.example/", own + `/\elsewhere.example`, own + "/%2F%2Felsewhere.example",
		own + "/%5Celsewhere.example", "//elsewhere.example/x", "javascript:alert(1)", own + "/plan\r\nSet-Cookie: x=y", own + contract.RouteDo + "stop-all"} {
		w := b.ask("POST", contract.RouteDo+"mute", url.Values{contract.FieldToken: {b.token()}, "value": {"on"}}, func(r *http.Request) { r.Header.Set("Referer", from) })
		to := w.Header().Get("Location")
		if u, err := url.Parse(to); w.Code != http.StatusSeeOther || err != nil || u.Host != "" || u.Scheme != "" || !strings.HasPrefix(to, "/") ||
			strings.HasPrefix(to, "//") || strings.Contains(to, `\`) || strings.HasPrefix(to, contract.RouteDo) || len(w.Header().Values("Set-Cookie")) != 0 {
			t.Errorf("coming from %q the way back is %d %q, cookies %v", from, w.Code, to, w.Header().Values("Set-Cookie"))
		}
	}
}

// What nobody signed in can read on the laptop's road: the refusal, the
// fixed files and the ports of running sites, which are numbers. Nothing of
// the work.
func TestWhatNobodySignedInCanRead(t *testing.T) {
	b := evening(t)
	for _, path := range []string{contract.RouteTeam, contract.RoutePlan, contract.RouteMessages, contract.RouteJobs, contract.RouteLead, contract.RouteTask + "T2",
		contract.RouteLive, contract.RouteEvidence + "T7.1/0", routeOwn + "status", routeOwn + "stop", routeOwn + "approve?id=000000"} {
		w := b.ask("GET", path, nil, nil)
		if text := w.Body.String(); w.Code != http.StatusForbidden || strings.Contains(text, "sign-up page") || strings.Contains(text, "FOR YOU") {
			t.Errorf("%s without a session: %d %.200s", path, w.Code, text)
		}
	}
	if w := b.ask("GET", contract.RouteSites, nil, nil); strings.Trim(w.Body.String(), "[]0123456789,\n") != "" {
		t.Errorf("the sites' address says more than numbers: %s", w.Body)
	}
}
