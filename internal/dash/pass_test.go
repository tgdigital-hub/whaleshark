package dash

import (
	"bytes"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/dashweb"
)

// The attacks of the design's page table on the tunnel's rows, each refused
// with nothing of the page shown. No account at Cloudflare exists here: the
// keys are the test's own, a pass is signed with them as Cloudflare would
// sign it, and the address its keys are published at is played by the test.
const (
	public  = "ws1.example.org"
	team    = "harbour"
	tag     = "0123abcd4567ef89"
	subject = "subject-of-robin"
	heading = `{"alg":"RS256","kid":"k1","typ":"JWT"}`
	iphone  = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)"
)

// No test calls out: the address of the keys answers only where a test plays it.
func init() {
	fetch = func(string) ([]byte, error) { return nil, errors.New("a test calls nowhere") }
}

func newKey(bits int) *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		panic(err)
	}
	return key
}

// cloud is the key Cloudflare signs with here; stranger is anybody else's.
var (
	cloud    = sync.OnceValue(func() *rsa.PrivateKey { return newKey(2048) })
	stranger = sync.OnceValue(func() *rsa.PrivateKey { return newKey(2048) })
)

// cloudflare plays the address a team's keys are published at.
type cloudflare struct {
	calls atomic.Int32
	down  bool
	keys  map[string]*rsa.PrivateKey
}

func publish(t *testing.T) *cloudflare {
	t.Helper()
	cf, was := &cloudflare{keys: map[string]*rsa.PrivateKey{"k1": cloud()}}, fetch
	t.Cleanup(func() { fetch = was })
	fetch = func(address string) ([]byte, error) {
		if cf.calls.Add(1); cf.down || address != "https://"+team+".cloudflareaccess.com/cdn-cgi/access/certs" {
			return nil, errors.New("not reachable")
		}
		list := []map[string]string{}
		for id, key := range cf.keys {
			list = append(list, map[string]string{"kid": id, "kty": "RSA", "alg": "RS256", "use": "sig",
				"n": b64.EncodeToString(key.N.Bytes()), "e": b64.EncodeToString(big.NewInt(int64(key.E)).Bytes())})
		}
		return json.Marshal(map[string]any{"keys": list})
	}
	return cf
}

// values writes what the administrator sets for robin on Cloudflare's road
// into the server's file that aServer made.
func values(t *testing.T, team, name, aud string, email ...string) {
	t.Helper()
	data, err := os.ReadFile(contract.ServerFile)
	if err != nil {
		t.Fatal(err)
	}
	base := string(data)
	if _, rest, ok := strings.Cut(base, "# base\n"); ok {
		base, _, _ = strings.Cut(rest, "# values\n")
	}
	for i := range email {
		email[i] = strconv.Quote(email[i])
	}
	text := fmt.Sprintf("team = %q\n# base\n%s# values\nname = %q\naud = %q\nemail = [%s]\n", team, base, name, aud, strings.Join(email, ", "))
	if err := os.WriteFile(contract.ServerFile, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// tunnel is the evening on a server of Cloudflare's road: robin has a public
// name, a tag and an address, is signed in on the laptop's road and has
// phone access on, and Cloudflare publishes one key.
func tunnel(t *testing.T) (*browser, *cloudflare) {
	t.Helper()
	aServer(t, contract.RoadCloudflare, contract.RulesUnproven, 0)
	values(t, team, public, tag, robin)
	b := evening(t).signIn()
	dashweb.Plug(b.p.Kit)
	access(t, b.p.Kit, true)
	return b, publish(t)
}

// signed is a pass as Cloudflare makes one for robin at this moment, under
// the heading given, with its fields changed by change.
func signed(key *rsa.PrivateKey, heading string, change func(f map[string]any)) string {
	now := contract.Now().Unix()
	f := map[string]any{"iss": "https://" + team + ".cloudflareaccess.com", "aud": []string{tag}, "email": robin, "sub": subject,
		"type": "app", "iat": now - 10, "nbf": now - 10, "exp": now + 3600}
	if change != nil {
		change(f)
	}
	body, _ := json.Marshal(f)
	text := b64.EncodeToString([]byte(heading)) + "." + b64.EncodeToString(body)
	sum := sha256.Sum256([]byte(text))
	mark, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, sum[:])
	if err != nil {
		panic(err)
	}
	return text + "." + b64.EncodeToString(mark)
}

func set(name string, value any) func(map[string]any) {
	return func(f map[string]any) { f[name] = value }
}

// calm forgets the refusals counted so far, where a test is not about them.
func (b *browser) calm() {
	b.s.mu.Lock()
	b.s.shut, b.s.turned = time.Time{}, 0
	b.s.mu.Unlock()
}

// nothing holds an answer to be the empty refusal: no page, no cookie, and
// the word that it must not be kept.
func nothing(t *testing.T, what string, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusForbidden || w.Body.Len() != 0 || len(w.Result().Cookies()) != 0 || w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("%s: answered %d with %d bytes, cookies %v, %q", what, w.Code, w.Body.Len(), w.Result().Cookies(), w.Header().Get("Cache-Control"))
	}
}

// through is a browser that Cloudflare let through with that pass, paired.
func (b *browser) through(pass string) *phone {
	b.t.Helper()
	p := &phone{b: b, agent: iphone, host: public, pass: pass}
	if w := b.decide(p.knock(), "approve"); w.Code != http.StatusSeeOther {
		b.t.Fatalf("approving: %d %s", w.Code, w.Body)
	}
	return p
}

// Reaching it through Cloudflare: the door answers only a request that
// carries Cloudflare's pass, and checks it itself every time.
func TestWithoutCloudflaresPassNothingIsShown(t *testing.T) {
	b, _ := tunnel(t)
	now := contract.Now().Unix()
	good := signed(cloud(), heading, nil)
	part := strings.Split(good, ".")
	// The old trick of naming a way of signing that takes the published key
	// for a shared secret.
	shared := hmac.New(sha256.New, cloud().N.Bytes())
	other := b64.EncodeToString([]byte(`{"alg":"HS256","kid":"k1","typ":"JWT"}`)) + "." + part[1]
	shared.Write([]byte(other))
	attacks := []struct{ what, pass, host string }{
		{"no pass", "", public},
		{"a pass signed with another key", signed(stranger(), heading, nil), public},
		{"a pass that names another way of signing", other + "." + b64.EncodeToString(shared.Sum(nil)), public},
		{"a pass that names another way of signing over Cloudflare's signature", signed(cloud(), `{"alg":"RS384","kid":"k1"}`, nil), public},
		{"a pass that names no way of signing", b64.EncodeToString([]byte(`{"alg":"none","kid":"k1"}`)) + "." + part[1] + ".", public},
		{"a pass with no heading for the way of signing", signed(cloud(), `{"kid":"k1"}`, nil), public},
		{"another person's valid pass", signed(cloud(), heading, func(f map[string]any) {
			f["aud"], f["email"], f["sub"] = []string{"9876fedc5432ba10"}, "ana@example.org", "subject-of-ana"
		}), public},
		{"this person's pass for another application", signed(cloud(), heading, set("aud", "9876fedc5432ba10")), public},
		{"another person's address under this tag", signed(cloud(), heading, set("email", "ana@example.org")), public},
		{"a wrong issuer", signed(cloud(), heading, set("iss", "https://elsewhere.cloudflareaccess.com")), public},
		{"an issuer that only begins like the team's", signed(cloud(), heading, set("iss", "https://"+team+".cloudflareaccess.com.example.org")), public},
		{"an expired pass", signed(cloud(), heading, set("exp", now-61)), public},
		{"a pass with no end", signed(cloud(), heading, func(f map[string]any) { delete(f, "exp") }), public},
		{"a pass from the future", signed(cloud(), heading, set("nbf", now+61)), public},
		{"a pass made in the future", signed(cloud(), heading, set("iat", now+61)), public},
		{"the wider pass of a whole login at Cloudflare", signed(cloud(), heading, set("type", "org")), public},
		{"a pass that names no subject", signed(cloud(), heading, set("sub", "")), public},
		{"a pass whose key Cloudflare does not publish", signed(cloud(), `{"alg":"RS256","kid":"k9"}`, nil), public},
		{"a pass with its fields changed under the old signature", part[0] + "." + b64.EncodeToString([]byte(`{"sub":"x"}`)) + "." + part[2], public},
		{"a pass with a fourth part", good + ".x", public},
		{"something that is no pass", "x.y.z", public},
		{"a wrong host", good, "ws2.example.org"},
		{"the name with a port", good, public + ":8443"},
		{"the door's own address as the host", good, net.JoinHostPort("localhost", "8443")},
	}
	paths := [][2]string{{"GET", contract.RouteTeam}, {"GET", contract.RoutePair}, {"GET", contract.RouteAsset + "page.css"},
		{"GET", contract.RouteLive}, {"GET", contract.RouteEvidence + "T7.1/0"}, {"POST", contract.RouteDo + "answer"}}
	for _, a := range attacks {
		for _, path := range paths {
			b.calm()
			p := &phone{b: b, agent: iphone, host: a.host, pass: a.pass}
			var form url.Values
			if path[0] == "POST" {
				form = url.Values{contract.FieldToken: {"x"}, "id": {"pair000000"}, "text": {"approve"}}
			}
			nothing(t, a.what+", "+path[0]+" "+path[1], p.ask(path[0], path[1], form, nil))
		}
		// The door is not simply shut: the right pass is still let through.
		if w := (&phone{b: b, agent: iphone, host: public, pass: good}).ask("GET", contract.RoutePair, nil, nil); w.Code == http.StatusForbidden {
			t.Fatalf("after %s the right pass was refused too", a.what)
		}
	}
	// What the check allows: the tag as one word, the name in capitals, and
	// a minute for two clocks that disagree.
	for what, pass := range map[string]string{
		"the tag as one word":       signed(cloud(), heading, set("aud", tag)),
		"the tag among others":      signed(cloud(), heading, set("aud", []string{"9876fedc5432ba10", tag})),
		"run out half a minute ago": signed(cloud(), heading, set("exp", now-30)),
		"half a minute ahead":       signed(cloud(), heading, func(f map[string]any) { f["nbf"], f["iat"] = now+30, now+30 }),
		"the address in capitals":   signed(cloud(), heading, set("email", strings.ToUpper(robin))),
	} {
		if w := (&phone{b: b, agent: iphone, host: strings.ToUpper(public), pass: pass}).ask("GET", contract.RoutePair, nil, nil); w.Code == http.StatusForbidden {
			t.Errorf("%s was refused", what)
		}
	}
	// And with the right pass an unpaired browser is still shown a number only.
	w := (&phone{b: b, agent: iphone, host: public, pass: good}).ask("GET", contract.RouteTeam, nil, nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != contract.RoutePair || len(w.Result().Cookies()) != 0 {
		t.Errorf("an unpaired browser with a pass, asking for the page: %d %v", w.Code, w.Header())
	}
}

// With the keys unreachable everything is refused; they are asked for again
// when a pass names one that is not held, at most once a minute.
func TestWithTheKeysUnreachableEverythingIsRefused(t *testing.T) {
	b, cf := tunnel(t)
	cf.down = true
	p := &phone{b: b, agent: iphone, host: public, pass: signed(cloud(), heading, nil)}
	nothing(t, "the right pass with no keys", p.ask("GET", contract.RoutePair, nil, nil))
	nothing(t, "the right pass with no keys, again", p.ask("GET", contract.RouteTeam, nil, nil))
	cf.down = false
	nothing(t, "the right pass within the minute", p.ask("GET", contract.RoutePair, nil, nil))
	if n := cf.calls.Load(); n != 1 {
		t.Errorf("Cloudflare was asked %d times in one minute", n)
	}
	b.p.Clock(keysEvery)
	if w := p.ask("GET", contract.RoutePair, nil, nil); w.Code != http.StatusOK || cf.calls.Load() != 2 {
		t.Fatalf("with the keys back: %d, asked %d times", w.Code, cf.calls.Load())
	}
	// Cloudflare changes its key: the new one is found, the old one still holds.
	cf.keys["k2"] = stranger()
	next := &phone{b: b, agent: iphone, host: public, pass: signed(stranger(), `{"alg":"RS256","kid":"k2"}`, nil), cookie: p.cookie}
	nothing(t, "a new key within the minute", next.ask("GET", contract.RoutePair, nil, nil))
	b.p.Clock(keysEvery)
	if w := next.ask("GET", contract.RoutePair, nil, nil); w.Code == http.StatusForbidden || cf.calls.Load() != 3 {
		t.Errorf("with the new key published: %d, asked %d times", w.Code, cf.calls.Load())
	}
	if w := p.ask("GET", contract.RoutePair, nil, nil); w.Code == http.StatusForbidden || cf.calls.Load() != 3 {
		t.Errorf("the old key after the change: %d, asked %d times", w.Code, cf.calls.Load())
	}
	// A key too small to trust is not taken, and one Cloudflare withdrew goes
	// with the next list.
	delete(cf.keys, "k1")
	cf.keys["k3"] = newKey(1024)
	b.p.Clock(keysEvery)
	small := &phone{b: b, agent: iphone, host: public, pass: signed(cf.keys["k3"], `{"alg":"RS256","kid":"k3"}`, nil)}
	nothing(t, "a pass signed with a small key", small.ask("GET", contract.RoutePair, nil, nil))
	p.pass = signed(cloud(), heading, nil)
	nothing(t, "a pass signed with a key Cloudflare withdrew", p.ask("GET", contract.RoutePair, nil, nil))
}

// Everything is refused while one of the person's values is missing.
func TestWithoutThePersonsValuesTheDoorAnswersNothing(t *testing.T) {
	b, _ := tunnel(t)
	p := &phone{b: b, agent: iphone, host: public, pass: signed(cloud(), heading, nil)}
	for what, write := range map[string]func(){
		"no team":    func() { values(t, "", public, tag, robin) },
		"no name":    func() { values(t, team, "", tag, robin) },
		"no tag":     func() { values(t, team, public, "", robin) },
		"no address": func() { values(t, team, public, tag) },
	} {
		write()
		p.host = public
		nothing(t, what, p.ask("GET", contract.RoutePair, nil, nil))
		p.host = ""
		nothing(t, what+", under no name", p.ask("GET", contract.RoutePair, nil, func(r *http.Request) { r.Host = "" }))
	}
	values(t, team, public, tag, robin)
	p.host = public
	if w := p.ask("GET", contract.RoutePair, nil, nil); w.Code != http.StatusOK {
		t.Errorf("with every value back: %d", w.Code)
	}
}

// A wrong setting at Cloudflare: more than twenty requests in a minute
// without a valid pass shut the door for a minute and raise an item.
func TestTwentyRefusalsShutTheDoorAndRaiseAnItem(t *testing.T) {
	const item = "Requests without a valid pass are reaching your page; check the Cloudflare set-up."
	b, _ := tunnel(t)
	good := &phone{b: b, agent: iphone, host: public, pass: signed(cloud(), heading, nil)}
	bare := &phone{b: b, agent: iphone, host: public}
	for i := range refusalsMost {
		nothing(t, "request "+strconv.Itoa(i+1), bare.ask("GET", contract.RouteTeam, nil, nil))
	}
	if w := good.ask("GET", contract.RoutePair, nil, nil); w.Code != http.StatusOK {
		t.Fatalf("after twenty refusals the right pass: %d", w.Code)
	}
	if strings.Contains(b.ask("GET", contract.RouteTeam, nil, nil).Body.String(), item) {
		t.Error("the item is there before the door shut")
	}
	nothing(t, "the one too many", bare.ask("GET", contract.RouteTeam, nil, nil))
	nothing(t, "the right pass at the shut door", good.ask("GET", contract.RoutePair, nil, nil))
	if !strings.Contains(b.ask("GET", contract.RouteTeam, nil, nil).Body.String(), item) {
		t.Error("the door shut and the laptop's page has no item")
	}
	var st status
	w := b.ask("GET", routeOwn+"status?"+contract.FieldCode+"="+newCode(b.s.key, "status", contract.Now()), nil, nil)
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil || st.Refused != "no pass" || !st.Shut.Equal(contract.Now()) {
		t.Errorf("the status after the door shut: %v %+v", err, st)
	}
	b.p.Clock(shutFor - time.Second)
	nothing(t, "the right pass a second before the minute is over", good.ask("GET", contract.RoutePair, nil, nil))
	b.p.Clock(time.Second)
	if w := good.ask("GET", contract.RoutePair, nil, nil); w.Code == http.StatusForbidden {
		t.Error("the door stayed shut after its minute")
	}
	// The refusals of an old minute do not count towards a new one.
	for range refusalsMost {
		bare.ask("GET", contract.RouteTeam, nil, nil)
	}
	b.p.Clock(time.Minute)
	bare.ask("GET", contract.RouteTeam, nil, nil)
	if w := good.ask("GET", contract.RoutePair, nil, nil); w.Code == http.StatusForbidden {
		t.Error("twenty refusals in one minute and one in the next shut the door")
	}
	if !strings.Contains(b.ask("GET", contract.RouteTeam, nil, nil).Body.String(), item) {
		t.Error("the item went before the person can have seen it")
	}
	b.p.Clock(shutShown)
	if strings.Contains(b.ask("GET", contract.RouteTeam, nil, nil).Body.String(), item) {
		t.Error("the item stays for ever")
	}
}

// A copy kept on the way, or a cookie set from a neighbouring name: every
// answer says it must not be stored, and the cookies are bound to the one name.
func TestTheCookieIsBoundToTheOneNameAndNothingIsKept(t *testing.T) {
	b, _ := tunnel(t)
	p := &phone{b: b, agent: iphone, host: public, pass: signed(cloud(), heading, nil)}
	bound := func(what string, w *httptest.ResponseRecorder) {
		t.Helper()
		got := w.Result().Cookies()
		if len(got) != 1 || !strings.HasPrefix(got[0].Name, "__Host-ws") || !got[0].Secure || !got[0].HttpOnly ||
			got[0].SameSite != http.SameSiteStrictMode || got[0].Path != "/" || got[0].Domain != "" {
			t.Errorf("%s: %v", what, w.Header()["Set-Cookie"])
		}
	}
	w := p.ask("GET", contract.RoutePair, nil, nil)
	bound("the session's cookie", w)
	p.cookie = w.Result().Cookies()
	if m := numberRE.FindStringSubmatch(w.Body.String()); m == nil || b.decide(m[1]+m[2], "approve").Code != http.StatusSeeOther {
		t.Fatalf("pairing: %s", w.Body)
	}
	bound("the cookie of the chosen job", p.ask("GET", contract.RouteTeam+"?root="+url.QueryEscape(b.p.Root)+"&run=r3", nil, nil))
	for _, path := range []string{contract.RouteTeam, contract.RoutePlan, contract.RoutePair, contract.RouteAsset + "page.css", contract.RouteAsset + "none",
		contract.RouteEvidence + "T7.1/0", contract.RoutePush, "/nowhere"} {
		if w := p.ask("GET", path, nil, nil); w.Header().Get("Cache-Control") != "no-store" || w.Code == http.StatusForbidden {
			t.Errorf("%s: %d, Cache-Control %q", path, w.Code, w.Header().Get("Cache-Control"))
		}
	}
	// The cookie under the laptop's name, which another name of the domain
	// could have set, is no session at the door.
	plain := &phone{b: b, agent: iphone, host: public, pass: p.pass, cookie: []*http.Cookie{{Name: b.s.cookie, Value: p.cookie[0].Value}}}
	if w := plain.ask("GET", contract.RouteTeam, nil, nil); w.Code != http.StatusSeeOther {
		t.Errorf("a cookie without the binding was taken for the session: %d", w.Code)
	}
}

// The session is tied to the subject of the pass it was paired under, and is
// worth nothing without a pass.
func TestTheSessionIsTiedToThePasssSubject(t *testing.T) {
	b, _ := tunnel(t)
	values(t, team, public, tag, robin, "robin@example.net")
	p := b.through(signed(cloud(), heading, nil))
	if w := p.ask("GET", contract.RouteTeam, nil, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), ` phone">`) {
		t.Fatalf("the paired browser's page: %d %.300s", w.Code, w.Body)
	}
	if list := listOf(t, b); len(list.Paired) != 1 || list.Paired[0].Identity != robin+" "+subject {
		t.Errorf("the paired browser's record: %+v", list.Paired)
	}
	other := func(what, pass string) {
		t.Helper()
		q := &phone{b: b, agent: iphone, host: public, pass: pass, cookie: p.cookie}
		w := q.ask("GET", contract.RouteTeam, nil, nil)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "paired by somebody else") || strings.Contains(w.Body.String(), "<script") ||
			strings.Contains(w.Body.String(), "robin") || len(w.Result().Cookies()) != 0 {
			t.Errorf("%s: %d %.300s", what, w.Code, w.Body)
		}
	}
	other("the session under another subject", signed(cloud(), heading, set("sub", "subject-of-somebody")))
	other("the session under the person's other address", signed(cloud(), heading, set("email", "robin@example.net")))
	nothing(t, "the session with no pass", (&phone{b: b, agent: iphone, host: public, cookie: p.cookie}).ask("GET", contract.RouteTeam, nil, nil))
	b.calm()
	// A button's request must come from the page under the person's own name.
	form := url.Values{contract.FieldToken: {p.token()}}
	if w := p.ask("POST", contract.RouteDo+"dnd", form, func(r *http.Request) { r.Header.Set("Origin", "https://ws2.example.org") }); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "did not come from the page") {
		t.Errorf("a button pressed on a neighbouring name: %d %.200s", w.Code, w.Body)
	}
	// A later pass of the same subject is the same person: Cloudflare's login lasts a day.
	b.p.Clock(25 * time.Hour)
	nothing(t, "yesterday's pass", p.ask("GET", contract.RouteTeam, nil, nil))
	p.pass = signed(cloud(), heading, nil)
	if w := p.ask("GET", contract.RouteTeam, nil, nil); w.Code != http.StatusOK {
		t.Errorf("today's pass of the same subject: %d", w.Code)
	}
}

// The live connection is checked when it opens and is closed when its pass
// runs out.
func TestTheLiveConnectionEndsWithItsPass(t *testing.T) {
	b, _ := tunnel(t)
	p := b.through(signed(cloud(), heading, nil))
	// Two seconds are left of this one, with the minute for the clocks.
	p.pass = signed(cloud(), heading, set("exp", contract.Now().Add(2*time.Second-skew).Unix()))
	began, done := time.Now(), make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- p.ask("GET", contract.RouteLive, nil, nil) }()
	select {
	case w := <-done:
		if held := time.Since(began); w.Code != http.StatusOK || w.Body.String() != ": live\n\n" || held < time.Second {
			t.Errorf("the connection: %d %q, held for %v", w.Code, w.Body, held)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the connection outlived its pass")
	}
}

// The door on its real port, as Cloudflare's connector reaches it, and the
// words of `dash phone on` on this road.
func TestCloudflaresDoorOnItsRealPort(t *testing.T) {
	door := aServer(t, contract.RoadCloudflare, contract.RulesProven, 0)
	b := login(t, nil)
	dashweb.Plug(b.p.Kit)
	dirs := short(t, b.p)
	publish(t)
	word := func(args ...string) (string, listed, error) {
		var out bytes.Buffer
		res, err := dash(&contract.Call{Kit: b.p.Kit, Args: args, Out: &out})
		got, _ := res.(listed)
		return out.String(), got, err
	}
	if _, _, err := word("phone", "on"); err == nil || err.(*contract.Refusal).Code != "no_door" {
		t.Fatalf("dash phone on before the administrator gave the values: %v", err)
	}
	values(t, team, public, tag, robin)
	ended := make(chan error, 1)
	go func() { ended <- serve(b.p.Kit, dirs) }()
	until(t, "the server answering", func() bool { _, ok := ask(b.p.Kit, dirs, "status"); return ok })
	said, got, err := word("phone", "on")
	if err != nil || !got.On || got.Name != public || got.Port != 0 || !strings.Contains(said, "https://"+public+"/\n") || strings.Contains(said, "private network") {
		t.Fatalf("dash phone on: %v %+v %s", err, got, said)
	}
	knock := func(pass string) (*http.Response, string) {
		req, _ := http.NewRequest("GET", "http://"+net.JoinHostPort(contract.Loopback, strconv.Itoa(door))+contract.RoutePair, nil)
		if req.Host = public; pass != "" {
			req.Header.Set(contract.HeaderAccess, pass)
		}
		resp, err := (&http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp, string(body)
	}
	for what, pass := range map[string]string{"no pass": "", "a pass signed with another key": signed(stranger(), heading, nil)} {
		if resp, body := knock(pass); resp.StatusCode != http.StatusForbidden || body != "" || len(resp.Cookies()) != 0 || resp.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s at the real door: %d %q %v", what, resp.StatusCode, body, resp.Header)
		}
	}
	if resp, body := knock(signed(cloud(), heading, nil)); resp.StatusCode != http.StatusOK || !numberRE.MatchString(body) {
		t.Errorf("the right pass at the real door: %d %.200s", resp.StatusCode, body)
	}
	st, _ := ask(b.p.Kit, dirs, "status")
	if st.Door != door || st.Refused != "a wrong signature" && st.Refused != "no pass" || st.Caller != robin+" "+subject ||
		st.Pass != heading+" aud email exp iat iss nbf sub type" {
		t.Errorf("the status: %+v", st)
	}
	if _, _, err := word("stop"); err != nil {
		t.Fatal(err)
	}
	if err := <-ended; err != nil {
		t.Errorf("the server ended with %v", err)
	}
}

// A recorded real pass verifies against its recorded keys. There is none
// yet, because there is no account at Cloudflare: once there is, four files
// in testdata/recorded make this test run. pass is the header's value, keys
// what the team's address of keys answered, server.toml the server's file
// with the one login robin, and at the moment of the recording (RFC 3339).
func TestARecordedRealPassVerifies(t *testing.T) {
	file := func(name string) string {
		data, err := os.ReadFile(filepath.Join("testdata", "recorded", name))
		if err != nil {
			t.Skip("no recorded pass yet: it needs an account at Cloudflare")
		}
		return strings.TrimSpace(string(data))
	}
	pass, keys := file("pass"), file("keys")
	at, err := time.Parse(time.RFC3339, file("at"))
	if err != nil {
		t.Fatal(err)
	}
	was, place := fetch, contract.ServerFile
	t.Cleanup(func() { fetch, contract.ServerFile = was, place })
	fetch = func(string) ([]byte, error) { return []byte(keys), nil }
	contract.ServerFile = filepath.Join("testdata", "recorded", "server.toml")
	srv, err := contract.ReadServer(os.ReadFile)
	a, known := srv.Access("robin")
	if err != nil || !known {
		t.Fatalf("the recorded server's file: %v", err)
	}
	r := httptest.NewRequest("GET", "https://"+a.Name+"/", nil)
	r.Header.Set(contract.HeaderAccess, pass)
	if who, _, shape, why := new(guard).check(r, a, at); why != "" {
		t.Errorf("the recorded pass is refused: %s (%s; %s)", why, who, shape)
	}
}
