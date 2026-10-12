package dash

import (
	"crypto/rsa"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

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

// A number belongs to whoever asked for it. Somebody else the login's rule
// admits asks at this person's door, gets a number and the secret that goes
// with it, and plants that secret in the person's own phone. The person's
// phone must not then show the other's number as its own, for the person to
// approve.
func TestANumberIsShownOnlyToWhoAskedForIt(t *testing.T) {
	b := house(t)
	theirs := &phone{b: b, who: kim}
	number := theirs.knock()
	mine := &phone{b: b, who: subject, cookie: theirs.cookie}
	w := mine.ask("GET", contract.RoutePair, nil, nil)
	if w.Code == http.StatusOK || strings.Contains(w.Body.String(), number[:3]+" "+number[3:]) {
		t.Fatalf("the person's phone, carrying another's secret, was shown that one's number: %d %s", w.Code, w.Body)
	}
	if again := theirs.knock(); again != number {
		t.Errorf("the one who asked is shown %s and no longer %s", again, number)
	}
}

// A key Cloudflare has taken back is not held for as long as the server
// runs: the keys are asked for again once a day, and what is no longer
// published signs nothing.
func TestAKeyCloudflareTookBackIsNotKept(t *testing.T) {
	b, cf := tunnel(t)
	p := b.through(signed(cloud(), heading, nil))
	if w := p.ask("GET", contract.RouteTeam, nil, nil); w.Code != http.StatusOK {
		t.Fatalf("the paired browser: %d", w.Code)
	}
	cf.keys = map[string]*rsa.PrivateKey{"k2": stranger()}
	b.p.Clock(keysLife - time.Hour)
	p.pass = signed(cloud(), heading, nil)
	if w := p.ask("GET", contract.RouteTeam, nil, nil); w.Code != http.StatusOK {
		t.Errorf("within the day the keys are not asked for at every request: %d", w.Code)
	}
	b.p.Clock(2 * time.Hour)
	p.pass = signed(cloud(), heading, nil)
	nothing(t, "a pass signed with a key Cloudflare took back a day ago", p.ask("GET", contract.RouteTeam, nil, nil))
	// And with the keys out of reach on that day nothing passes either.
	b.calm()
	cf.keys, cf.down = map[string]*rsa.PrivateKey{"k1": cloud()}, true
	b.p.Clock(keysLife + time.Hour)
	p.pass = signed(cloud(), heading, nil)
	nothing(t, "a pass while the keys are a day old and cannot be fetched", p.ask("GET", contract.RouteTeam, nil, nil))
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
