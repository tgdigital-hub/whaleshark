package phase3

import (
	"crypto"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

const robin = "robin@example.org"

var (
	b64      = base64.RawURLEncoding
	numberRE = regexp.MustCompile(`<p class="number">(\d{3}) (\d{3})</p>`)
	tokenRE  = regexp.MustCompile(`name="t" value="([^"]+)"`)
)

// block is the first port of a block whose door nobody listens on now,
// looked for from the top of the range.
func block(t *testing.T) int {
	t.Helper()
	for base := contract.ServerPorts.Base + contract.ServerPorts.Count - contract.BlockSize; base >= contract.ServerPorts.Base; base -= contract.BlockSize {
		if l, err := net.Listen("tcp4", net.JoinHostPort(contract.Loopback, strconv.Itoa(base+contract.BlockSize-1))); err == nil {
			l.Close()
			return base
		}
	}
	t.Fatal("no block's door is free on this machine")
	return 0
}

// phone is a phone's browser as the road's own program on the server hands
// its requests to the door: over plain HTTP on the loopback port, under the
// name the phone asked for, with the road's header. The door's cookies are
// for HTTPS only, so the test keeps them by hand.
type phone struct {
	t      *testing.T
	door   int
	host   string // the name the phone's browser asked under
	header map[string]string
	cookie map[string]string
}

func (ph *phone) do(method, path string, form url.Values) (int, string) {
	ph.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, "http://"+net.JoinHostPort(contract.Loopback, strconv.Itoa(ph.door))+path, body)
	req.Host = ph.host
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "https://"+ph.host)
		req.Header.Set("Referer", "https://"+ph.host+contract.RouteTeam)
	}
	for name, value := range ph.header {
		req.Header.Set(name, value)
	}
	for name, value := range ph.cookie {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	c := http.Client{Timeout: time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		ph.t.Fatal(err)
	}
	defer resp.Body.Close()
	for _, set := range resp.Cookies() {
		if !set.Secure || !set.HttpOnly || !strings.HasPrefix(set.Name, "__Host-") {
			ph.t.Errorf("the door set the cookie %+v", set)
		}
		ph.cookie[set.Name] = set.Value
	}
	text, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(text)
}

// pair takes a phone from its first request to the page: it is sent to the
// number's screen, the number is in `dash phone list` and in the person's
// status, an agent may not approve it, and the person approves it at a
// terminal with the command status names.
func pair(t *testing.T, p *scenario.Project, ph *phone, who string) {
	t.Helper()
	if code, _ := ph.do("GET", contract.RouteTeam, nil); code != http.StatusSeeOther {
		out, _ := p.Command(scenario.Unbound, "dash", "status").CombinedOutput()
		t.Fatalf("an unpaired phone asking for the page: %d\n%s", code, out)
	}
	code, body := ph.do("GET", contract.RoutePair, nil)
	n := numberRE.FindStringSubmatch(body)
	if code != 200 || n == nil || strings.Contains(body, "<form") || strings.Contains(body, "T7") {
		t.Fatalf("an unpaired phone is shown %d:\n%s", code, body)
	}
	number := n[1] + n[2]
	said, _ := p.Command(scenario.Human, "status").CombinedOutput()
	list, _ := p.Command(scenario.Unbound, "dash", "phone", "list").CombinedOutput()
	approve := "whaleshark dash phone approve " + number
	for _, out := range [][]byte{said, list} {
		if !strings.Contains(string(out), n[1]+" "+n[2]) || !strings.Contains(string(out), who) || !strings.Contains(string(out), approve) {
			t.Errorf("the phone that asks (%s %s, %s) is not put before the person with its command:\n%s", n[1], n[2], who, out)
		}
	}
	if out, err := p.Command(scenario.Orch, "dash", "phone", "approve", number).CombinedOutput(); err == nil {
		t.Fatalf("the lead agent approved a phone:\n%s", out)
	}
	if code, _ := ph.do("GET", contract.RouteTeam, nil); code != http.StatusSeeOther {
		t.Fatalf("the phone got in on an agent's word: %d", code)
	}
	if out, err := p.Command(scenario.Unbound, "dash", "phone", "approve", number).CombinedOutput(); err != nil || !strings.Contains(string(out), "The phone is paired") {
		t.Fatalf("approving at a terminal: %v\n%s", err, out)
	}
	if out, err := p.Command(scenario.Unbound, "dash", "phone", "approve", number).CombinedOutput(); err == nil {
		t.Fatalf("a number was approved twice:\n%s", out)
	}
	code, body = ph.do("GET", contract.RouteTeam, nil)
	if code != 200 || !screen(body, contract.ScreenTeam) || !strings.Contains(body, " phone\">") || !strings.Contains(body, `id="i-q7"`) {
		t.Fatalf("the paired phone is shown %d:\n%s", code, body)
	}
	// A button pressed on the phone is the phone's.
	form := url.Values{contract.FieldToken: {tokenRE.FindStringSubmatch(body)[1]}, "id": {"q7"}, "text": {"yes"}}
	if code, body := ph.do("POST", contract.RouteDo+"answer", form); code != http.StatusSeeOther {
		t.Fatalf("answering from the phone: %d\n%s", code, body)
	}
	s, _ := p.Record()
	if q := s.Questions["q7"]; q.Answer != "yes" || q.AnsweredBy == nil || q.AnsweredBy.Where != contract.WherePhone {
		t.Errorf("q7 answered from the phone is recorded as %+v by %+v", q.Answer, q.AnsweredBy)
	}
}

// The Tailscale road (8r) with the real program: the private network's own
// program is played by the test, which hands each request to the door with
// the header that says who is calling. The phone is paired at a terminal,
// hands over where its nudges go, and the next thing that waits for the
// person is handed towards its maker's push service: the test stands where
// the program's requests to other machines pass and sees the one for the
// phone, which it lets no further.
func TestAPhoneByTailscalesRoad(t *testing.T) {
	for _, on := range []scenario.Backend{scenario.Double, scenario.Keeper} {
		t.Run(string(on), func(t *testing.T) {
			asked := outward(t)
			f, err := testkit.Load(testkit.Evening)
			if err != nil {
				t.Fatal(err)
			}
			p := scenario.PrepareOn(t, on, f, nil)
			if p.Double != nil {
				p.Double.Attached = false // nobody sits at a window: a nudge goes on to the phone
			}
			base := block(t)
			p.Server(`road = "tailscale"`, `rules = "proven"`, "[login.{me}]", fmt.Sprint("ports = ", base), "phone = 8443")
			serve(t, p)
			ph := &phone{t: t, door: base + contract.BlockSize - 1, host: "harbour.example-net.ts.net:8443",
				header: map[string]string{contract.HeaderTailscale: robin, "User-Agent": "Mozilla/5.0 (iPhone; like a phone)"}, cookie: map[string]string{}}
			if _, err := net.DialTimeout("tcp4", net.JoinHostPort(contract.Loopback, strconv.Itoa(ph.door)), time.Second); err == nil {
				t.Fatal("the door is open before the person switched phone access on")
			}
			if out, err := p.Command(scenario.Unbound, "dash", "phone", "on").CombinedOutput(); err != nil || !strings.Contains(string(out), ":8443/") {
				t.Fatalf("dash phone on: %v\n%s", err, out)
			}
			pair(t, p, ph, robin)

			// The same session under another name of the network is nobody's.
			other := *ph
			other.header = map[string]string{contract.HeaderTailscale: "kim@example.org"}
			if code, body := other.do("GET", contract.RouteTeam, nil); code != http.StatusForbidden || strings.Contains(body, "T7") {
				t.Errorf("the session under another identity: %d\n%s", code, body)
			}

			// The phone asks for nudges: the login's key, then what its browser hands over.
			_, team := ph.do("GET", contract.RouteTeam, nil)
			code, key := ph.do("GET", contract.RoutePush, nil)
			if raw, err := b64.DecodeString(key); code != 200 || err != nil || len(raw) != 65 {
				t.Fatalf("the key for nudges: %d %q", code, key)
			}
			mine, _ := ecdh.P256().GenerateKey(rand.Reader)
			secret := make([]byte, 16)
			rand.Read(secret)
			form := url.Values{contract.FieldToken: {tokenRE.FindStringSubmatch(team)[1]}, "endpoint": {"https://fcm.googleapis.com/fcm/send/a-phone-of-the-test"},
				"p256dh": {b64.EncodeToString(mine.PublicKey().Bytes())}, "auth": {b64.EncodeToString(secret)}}
			if code, _ := ph.do("POST", contract.RoutePush, form); code != http.StatusNoContent {
				t.Fatalf("handing over where the nudges go: %d", code)
			}
			// The lead agent's new question for the person, and a sweep.
			if out, err := p.Command(scenario.Orch, "need", "question", "May the old price list go?").CombinedOutput(); err != nil {
				t.Fatalf("need: %v\n%s", err, out)
			}
			p.Command(scenario.Orch, "status").Run()
			select {
			case host := <-asked:
				if host != "fcm.googleapis.com:443" {
					t.Errorf("the nudge was sent towards %q", host)
				}
			case <-time.After(20 * time.Second):
				t.Error("no nudge was handed towards the paired phone's push service")
			}
			if out, err := p.Command(scenario.Unbound, "dash", "phone", "off").CombinedOutput(); err != nil {
				t.Fatalf("dash phone off: %v\n%s", err, out)
			}
			if _, err := net.DialTimeout("tcp4", net.JoinHostPort(contract.Loopback, strconv.Itoa(ph.door)), time.Second); err == nil {
				t.Error("the door is still open after dash phone off")
			}
		})
	}
}

// outward stands where every request of the test's programs to another
// machine passes (the environment's proxy), tells of each machine asked
// for, and lets nothing through: no program of a scenario reaches the world.
func outward(t *testing.T) <-chan string {
	t.Helper()
	asked := make(chan string, 16)
	l, err := net.Listen("tcp4", net.JoinHostPort(contract.Loopback, "0"))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case asked <- r.Host:
		default:
		}
		w.WriteHeader(http.StatusBadGateway)
	})}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		t.Setenv(name, "http://"+l.Addr().String())
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	return asked
}

// The Cloudflare road (8s) with the real program: Cloudflare's connector and
// its login are played by the test. The team's keys are published on this
// machine, where the scenarios' program looks for them, and each request
// carries a pass the test signed. Without one the door answers nothing.
func TestAPhoneByCloudflaresRoad(t *testing.T) {
	outward(t)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp4", net.JoinHostPort(contract.Loopback, "0"))
	if err != nil {
		t.Fatal(err)
	}
	keys := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cdn-cgi/access/certs" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kid": "k1", "kty": "RSA", "alg": "RS256", "use": "sig",
			"n": b64.EncodeToString(key.N.Bytes()), "e": b64.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	})}
	go keys.Serve(l)
	t.Cleanup(func() { keys.Close() })
	_, port, _ := net.SplitHostPort(l.Addr().String())
	issuer := "http://" + net.JoinHostPort(contract.Loopback, port)

	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	p := scenario.Prepare(t, f, nil)
	base := block(t)
	p.Server(`road = "cloudflare"`, fmt.Sprintf("team = %q", port), "[login.{me}]", fmt.Sprint("ports = ", base),
		`name = "ws1.example.org"`, `aud = "tag-of-the-test"`, fmt.Sprintf("email = [%q]", robin))
	serve(t, p)
	if out, err := p.Command(scenario.Unbound, "dash", "phone", "on").CombinedOutput(); err != nil || !strings.Contains(string(out), "https://ws1.example.org/") {
		t.Fatalf("dash phone on: %v\n%s", err, out)
	}
	pass := func(email, aud string) string {
		now := f.Now.Unix() // the scenario's clock, which the door reads
		head, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
		body, _ := json.Marshal(map[string]any{"iss": issuer, "aud": []string{aud}, "type": "app", "email": email, "sub": "subject-1",
			"exp": now + 600, "iat": now, "nbf": now})
		signed := b64.EncodeToString(head) + "." + b64.EncodeToString(body)
		sum := sha256.Sum256([]byte(signed))
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
		if err != nil {
			t.Fatal(err)
		}
		return signed + "." + b64.EncodeToString(sig)
	}
	ph := &phone{t: t, door: base + contract.BlockSize - 1, host: "ws1.example.org", cookie: map[string]string{},
		header: map[string]string{"User-Agent": "Mozilla/5.0 (Linux; Android; like a phone)"}}
	for why, header := range map[string]map[string]string{"no pass": {}, "another person's pass": {contract.HeaderAccess: pass("kim@example.org", "tag-of-the-test")},
		"another application's pass": {contract.HeaderAccess: pass(robin, "another-tag")}} {
		ph.header = header
		if code, body := ph.do("GET", contract.RoutePair, nil); code != http.StatusForbidden || body != "" || len(ph.cookie) != 0 {
			t.Fatalf("with %s the door answered %d %q and set %v", why, code, body, ph.cookie)
		}
	}
	ph.header = map[string]string{contract.HeaderAccess: pass(robin, "tag-of-the-test"), "User-Agent": "Mozilla/5.0 (Linux; Android; like a phone)"}
	pair(t, p, ph, robin+" subject-1")
	var st struct{ Pass, Refused, Caller string }
	if !ask(t, p, &st, "dash", "status") || !strings.HasSuffix(st.Pass, " aud email exp iat iss nbf sub type") || st.Caller != robin+" subject-1" || st.Refused == "" {
		t.Errorf("dash status says of the door: %+v", st)
	}
}
