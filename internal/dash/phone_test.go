package dash

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/dashweb"
)

// The attacks of the design's page table on the phone's rows, each refused.
// No phone and no tunnel exist here: a request is handed to the door as
// Cloudflare's connector would hand it over, with a pass that names the
// caller. named is who the road says robin's own phone is; kim is a second
// caller the same login's rule admits.
const (
	robin = "robin@example.org"
	named = robin + " " + subject
	kim   = "subject-of-kim"
)

var numberRE = regexp.MustCompile(`<p class="number">(\d{3}) (\d{3})</p>`)

// aServer makes this machine a server for the test: a file of its own with
// one login, robin, whose block has a door, on Cloudflare's road. What the
// administrator sets for robin there is written by values.
func aServer(t *testing.T) (door int) {
	t.Helper()
	me, file := contract.Me, contract.ServerFile
	t.Cleanup(func() { contract.Me, contract.ServerFile = me, file })
	contract.Me, contract.ServerFile = func() string { return "robin" }, filepath.Join(t.TempDir(), "server.toml")
	// A block that is this test's alone: it may open the door for real.
	base, release, err := testkit.Block()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	text := fmt.Sprintf("version = %d\nroad = %q\n[login.robin]\nports = %d\n", contract.FileVersion, contract.RoadCloudflare, base)
	if err := os.WriteFile(contract.ServerFile, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return base + contract.BlockSize - 1
}

// access switches phone access on or off as `dash phone` does.
func access(t *testing.T, k *contract.Kit, on bool) {
	t.Helper()
	if err := contract.ChangePhones(k.Platform, func(list *contract.Phones) error { list.On = on; return nil }); err != nil {
		t.Fatal(err)
	}
}

// house is the evening on a server, with the real screens, the person
// signed in on the laptop's road and phone access on.
func house(t *testing.T) *browser {
	t.Helper()
	b, _ := tunnel(t)
	return b
}

// phone is one phone's browser before the door. It asks under host, the
// person's public name unless a test says another, and carries pass; where
// a test gives none, a pass as Cloudflare makes one for the subject who.
type phone struct {
	b          *browser
	who        string
	agent      string
	cookie     []*http.Cookie
	host, pass string
}

// ask hands the door one request as Cloudflare's connector would.
func (p *phone) ask(method, path string, form url.Values, change func(*http.Request)) *httptest.ResponseRecorder {
	p.b.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	at, pass := "https://"+cmp.Or(p.host, public), p.pass
	if pass == "" && p.who != "" {
		pass = signed(cloud(), heading, set("sub", p.who))
	}
	r := httptest.NewRequest(method, at+path, body)
	if r.Header.Set("User-Agent", p.agent); method == http.MethodPost {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", at)
		r.Header.Set("Referer", at+contract.RouteTeam)
	}
	if pass != "" {
		r.Header.Set(contract.HeaderAccess, pass)
	}
	for _, c := range p.cookie {
		r.AddCookie(c)
	}
	if change != nil {
		change(r)
	}
	w := httptest.NewRecorder()
	p.b.s.phone(w, r)
	return w
}

// knock asks for a number and keeps the cookie that comes with it.
func (p *phone) knock() (number string) {
	p.b.t.Helper()
	w := p.ask("GET", contract.RoutePair, nil, nil)
	m := numberRE.FindStringSubmatch(w.Body.String())
	if w.Code != http.StatusOK || m == nil {
		p.b.t.Fatalf("knocking: %d %s", w.Code, w.Body)
	}
	if got := w.Result().Cookies(); len(got) == 1 {
		p.cookie = got
	}
	return m[1] + m[2]
}

// decide presses Approve or Refuse on the laptop's page.
func (b *browser) decide(number, answer string) *httptest.ResponseRecorder {
	b.t.Helper()
	button := map[string]string{"approve": "phone-yes", "refuse": "phone-no"}[answer]
	return b.ask("POST", contract.RouteDo+button, url.Values{contract.FieldToken: {b.token()}, "id": {"pair" + number}}, nil)
}

// paired is a phone the person has approved.
func (b *browser) paired(who string) *phone {
	b.t.Helper()
	p := &phone{b: b, who: who, agent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)"}
	if w := b.decide(p.knock(), "approve"); w.Code != http.StatusSeeOther {
		b.t.Fatalf("approving: %d %s", w.Code, w.Body)
	}
	return p
}

func (p *phone) token() string {
	return mac(p.b.s.key, "token", contract.TokenHash(p.cookie[0].Value))
}

func listOf(t *testing.T, b *browser) contract.Phones {
	t.Helper()
	list, err := contract.ReadPhones(b.p.Kit.Platform.Read, b.s.dirs.State)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

// A phone that is not his: nothing is shown to a phone that is not paired
// but a number, under one address, whatever it asks for; with phone access
// off, or without the person's values, the door answers nothing.
func TestAnUnpairedPhoneSeesANumberAndNothingElse(t *testing.T) {
	b := house(t)
	p := &phone{b: b, who: subject}
	access(t, b.p.Kit, false)
	if w := p.ask("GET", contract.RoutePair, nil, nil); w.Code != http.StatusForbidden || w.Body.Len() != 0 {
		t.Fatalf("with phone access off the door answered %d %q", w.Code, w.Body)
	}
	access(t, b.p.Kit, true)
	team := b.ask("GET", contract.RouteTeam, nil, nil).Body.String()
	for _, path := range []string{contract.RouteTeam, contract.RoutePlan, contract.RouteMessages, contract.RouteJobs, contract.RouteLead, contract.RouteTask + "T7",
		contract.RouteLive, contract.RouteEvidence + "T7.1/0", contract.RoutePush, contract.RouteSites, contract.RouteIn + "?code=" + newCode(b.s.key, "in", contract.Now()),
		routeOwn + "status?code=" + newCode(b.s.key, "status", contract.Now())} {
		w := p.ask("GET", path, nil, nil)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != contract.RoutePair || len(w.Result().Cookies()) != 0 {
			t.Errorf("%s: an unpaired phone was answered %d to %q: %.200s", path, w.Code, w.Header().Get("Location"), w.Body)
		}
	}
	b.refused("a button of an unpaired phone", p.ask("POST", contract.RouteDo+"stop-all", url.Values{contract.FieldToken: {b.token()}}, nil), http.StatusForbidden)
	b.refused("what an unpaired phone hands over", p.ask("POST", contract.RoutePush, url.Values{"endpoint": {"x"}}, nil), http.StatusForbidden)
	w := p.ask("GET", contract.RoutePair, nil, nil)
	got := w.Result().Cookies()
	if len(got) != 1 || !got[0].Secure || !got[0].HttpOnly || got[0].SameSite != http.SameSiteStrictMode || got[0].MaxAge <= 0 {
		t.Fatalf("the cookie of a phone that asks: %+v", got)
	}
	p.cookie = got
	if number := p.knock(); number != p.knock() || !strings.Contains(w.Body.String(), number[:3]+" "+number[3:]) {
		t.Errorf("the same phone was shown another number than %s", number)
	}
	text := w.Body.String()
	for _, never := range []string{"robin", b.s.host, "<form", "<script", "<a ", "sign-up page", "FOR YOU"} {
		if never != "" && strings.Contains(text, never) {
			t.Errorf("an unpaired phone is shown %q:\n%s", never, text)
		}
	}
	if !strings.Contains(team, "sign-up page") || !strings.Contains(text, `http-equiv="refresh"`) {
		t.Error("the fixture has no such card to hide, or the number's screen does not ask again by itself")
	}
	if w := p.ask("GET", contract.RouteAsset+"page.css", nil, nil); w.Code != http.StatusOK {
		t.Errorf("the style sheet at the door: %d", w.Code)
	}
	// The laptop's road does not know the phone's addresses.
	b.refused("the pairing address on the laptop's road", b.ask("GET", contract.RoutePair, nil, nil), http.StatusNotFound)
	b.refused("the nudge's address on the laptop's road", b.ask("GET", contract.RoutePush, nil, nil), http.StatusNotFound)

	// Another site's request, and a login the administrator set no values for.
	b.refused("a request from elsewhere", p.ask("GET", contract.RoutePair, nil, func(r *http.Request) { r.Header.Set("Origin", "https://elsewhere.example") }), http.StatusForbidden)
	b.refused("a way of asking the door does not know", p.ask("PUT", contract.RoutePair, nil, nil), http.StatusMethodNotAllowed)
	aServer(t)
	if w := p.ask("GET", contract.RoutePair, nil, nil); w.Code != http.StatusForbidden || w.Body.Len() != 0 {
		t.Errorf("without the person's values the door answered %d %q", w.Code, w.Body)
	}
}

// One request at a time, gone after two minutes, slowed after five
// refusals: a second phone is shown no number while the first is open.
func TestASecondPairingRequestIsRefused(t *testing.T) {
	b := house(t)
	first, second := &phone{b: b, who: subject}, &phone{b: b, who: kim}
	number := first.knock()
	w := second.ask("GET", contract.RoutePair, nil, nil)
	if w.Code != http.StatusTooManyRequests || numberRE.MatchString(w.Body.String()) || len(w.Result().Cookies()) != 0 {
		t.Fatalf("a second phone while one asks: %d %s", w.Code, w.Body)
	}
	if page := b.ask("GET", contract.RouteTeam, nil, nil).Body.String(); !strings.Contains(page, "A phone wants to connect: "+number[:3]+" "+number[3:]+" · "+named+" · a browser") {
		t.Errorf("the laptop's page does not show the phone that asks:\n%s", page)
	}
	// Two minutes on the number is gone: from the laptop's page, for the
	// press that comes too late, and for the phone, which is told once.
	b.p.Clock(pairLife)
	if page := b.ask("GET", contract.RouteTeam, nil, nil).Body.String(); strings.Contains(page, "A phone wants to connect") {
		t.Error("a request two minutes old is still an item")
	}
	b.refused("approving a number that ran out", b.decide(number, "approve"), http.StatusConflict)
	b.refused("a number that ran out", first.ask("GET", contract.RoutePair, nil, nil), http.StatusForbidden)
	if len(listOf(t, b).Paired) != 0 {
		t.Fatal("a phone was paired by a number that ran out")
	}
	// Five refusals, and the sixth request waits.
	for i := range slowFrom {
		p := &phone{b: b, who: subject}
		b.refused("refusing", b.decide(p.knock(), "refuse"), http.StatusSeeOther)
		if w := p.ask("GET", contract.RoutePair, nil, nil); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "refused or ran out") {
			t.Fatalf("refused phone %d was answered %d %s", i, w.Code, w.Body)
		}
	}
	if w := second.ask("GET", contract.RoutePair, nil, nil); w.Code != http.StatusTooManyRequests || numberRE.MatchString(w.Body.String()) {
		t.Errorf("after five refusals the next was answered %d", w.Code)
	}
	b.p.Clock(2 * time.Second)
	second.knock()
	if len(listOf(t, b).Paired) != 0 {
		t.Error("a refused phone was paired")
	}
}

// Pairing is approved by the person where they are signed in already, and
// by nobody else: not by the phone that asks, not by a paired phone, not by
// an old number. The record holds the hash of the phone's secret and who
// the road named.
func TestPairingIsApprovedWhereThePersonIsSignedIn(t *testing.T) {
	b := house(t)
	p := b.paired(subject)
	list := listOf(t, b)
	if len(list.Paired) != 1 {
		t.Fatalf("paired: %+v", list.Paired)
	}
	rec := list.Paired[0]
	if rec.ID != "ph1" || rec.Name != "iPhone" || rec.Identity != named || rec.Session != contract.TokenHash(p.cookie[0].Value) || rec.PairedAt.IsZero() || rec.Push != nil {
		t.Errorf("the record of a paired phone: %+v", rec)
	}
	if data, _ := os.ReadFile(filepath.Join(b.s.dirs.State, "phones.json")); bytes.Contains(data, []byte(p.cookie[0].Value)) {
		t.Error("the phone's secret is in the file")
	}
	if w := p.ask("GET", contract.RoutePair, nil, nil); w.Code != http.StatusSeeOther || w.Header().Get("Location") != contract.RouteTeam {
		t.Errorf("a paired phone at the pairing address: %d", w.Code)
	}
	page := p.ask("GET", contract.RouteTeam, nil, nil)
	if text := page.Body.String(); page.Code != http.StatusOK || !strings.Contains(text, " phone\">") || !strings.Contains(text, "sign-up page") || strings.Contains(text, "A paired phone") {
		t.Fatalf("a paired phone's page: %d %.300s", page.Code, text)
	}
	if text := b.ask("GET", contract.RouteTeam, nil, nil).Body.String(); !strings.Contains(text, "A paired phone: iPhone · "+named) ||
		!strings.Contains(text, `action="/do/forget-phone"><input type="hidden" name="t" value="`+b.token()+`"><input type="hidden" name="id" value="ph1">`) {
		t.Errorf("the laptop's page does not list the paired phone with its button:\n%s", text)
	}

	// A second phone asks.
	q := &phone{b: b, who: subject}
	number := q.knock()
	if item := b.ask("GET", contract.RouteTeam, nil, nil).Body.String(); !strings.Contains(item, "· "+named+" · a browser") {
		t.Errorf("the item of the phone that asks:\n%s", item)
	}
	press := func(by *phone, token string) *httptest.ResponseRecorder {
		return by.ask("POST", contract.RouteDo+"phone-yes", url.Values{contract.FieldToken: {token}, "id": {"pair" + number}}, nil)
	}
	b.refused("the asking phone approving itself", press(q, mac(b.s.key, "token", contract.TokenHash(q.cookie[0].Value))), http.StatusForbidden)
	b.refused("a paired phone approving another", press(p, p.token()), http.StatusConflict)
	b.refused("a number nobody was shown", b.decide("000000", "approve"), http.StatusConflict)
	b.refused("another site approving", b.ask("POST", contract.RouteDo+"phone-yes", url.Values{"id": {"pair" + number}}, nil), http.StatusForbidden)
	if len(listOf(t, b).Paired) != 1 {
		t.Fatal("a phone was paired by somebody who may not")
	}
	b.refused("approving", b.decide(number, "approve"), http.StatusSeeOther)
	if list = listOf(t, b); len(list.Paired) != 2 || list.Paired[1].ID != "ph2" || list.Paired[1].Identity != named || list.Paired[1].Name != "a browser" {
		t.Errorf("the second phone: %+v", list.Paired)
	}
	b.refused("the number a second time", b.decide(number, "approve"), http.StatusConflict)
}

// The session is tied to who the road said was calling when the phone was
// paired, and is refused under any other and under none; each road's cookie
// is nobody's on the other; forgetting a phone ends its session at once, and
// one left alone for thirty days has run out.
func TestASessionUnderAnotherIdentityIsRefused(t *testing.T) {
	b := house(t)
	p, alone := b.paired(subject), b.paired(kim)
	for who, of := range map[string]*phone{kim: p, "": p, subject: alone} {
		thief := &phone{b: b, who: who, cookie: of.cookie}
		w := thief.ask("GET", contract.RouteTeam, nil, nil)
		if text := w.Body.String(); w.Code != http.StatusForbidden || strings.Contains(text, "sign-up page") || numberRE.MatchString(text) {
			t.Errorf("the session of %q presented by %q: %d %.200s", of.who, who, w.Code, text)
		}
		b.refused("a button under another identity", thief.ask("POST", contract.RouteDo+"stop-all", url.Values{contract.FieldToken: {of.token()}}, nil), http.StatusForbidden)
	}
	if s, _ := b.p.Record(); s.Run.Paused != nil {
		t.Fatal("a refused phone stopped all work")
	}
	if w := alone.ask("GET", contract.RouteTeam, nil, nil); w.Code != http.StatusOK {
		t.Errorf("the second phone under its own name: %d", w.Code)
	}
	b.refused("the laptop's cookie at the door", (&phone{b: b, who: subject, cookie: b.cookie}).ask("GET", contract.RouteTeam, nil, nil), http.StatusSeeOther)
	b.refused("the phone's cookie on the laptop's road", (&browser{t: t, s: b.s, cookie: p.cookie}).ask("GET", contract.RouteTeam, nil, nil), http.StatusForbidden)

	// In use, the session is marked as seen and its cookie given a new end.
	if w := p.ask("GET", contract.RouteTeam, nil, nil); len(w.Result().Cookies()) != 0 {
		t.Error("a fresh session's cookie was set again")
	}
	b.p.Clock(seenEvery)
	w := p.ask("GET", contract.RouteTeam, nil, nil)
	if got := w.Result().Cookies(); w.Code != http.StatusOK || len(got) != 1 || got[0].Value != p.cookie[0].Value || !got[0].Secure || !listOf(t, b).Paired[0].SeenAt.Equal(contract.Now()) {
		t.Errorf("a session in use: %d, cookies %v", w.Code, got)
	}

	// Forget, by the laptop's button, which is the real command as a child.
	forget := func(id string) *httptest.ResponseRecorder {
		return b.ask("POST", contract.RouteDo+"forget-phone", url.Values{contract.FieldToken: {b.token()}, "id": {id}}, nil)
	}
	b.refused("forgetting a question", forget("q9"), http.StatusOK)
	b.refused("a phone forgetting a phone", p.ask("POST", contract.RouteDo+"forget-phone", url.Values{contract.FieldToken: {p.token()}, "id": {"ph2"}}, nil), http.StatusConflict)
	if b.refused("forgetting", forget("ph1"), http.StatusSeeOther); len(listOf(t, b).Paired) != 1 || listOf(t, b).Paired[0].ID != "ph2" {
		t.Fatalf("after forgetting ph1: %+v", listOf(t, b).Paired)
	}
	b.refused("a forgotten phone", p.ask("GET", contract.RouteTeam, nil, nil), http.StatusSeeOther)
	b.refused("a forgotten phone's button", p.ask("POST", contract.RouteDo+"stop-all", url.Values{contract.FieldToken: {p.token()}}, nil), http.StatusForbidden)
	b.p.Clock(sessionLife)
	b.refused("a session left alone for thirty days", alone.ask("GET", contract.RouteTeam, nil, nil), http.StatusSeeOther)
}

// What a paired phone may do is what the page may do, marked as the
// phone's; it is shown no running site, and the lead agent's last screen
// stands at the foot of its page as plain text. Its cookies are sent over
// HTTPS alone.
func TestAPhonesButtonIsThePhones(t *testing.T) {
	b := house(t)
	p := b.paired(subject)
	b.s.k.Terms = screens{b.s.k.Terms, "one\x1b[31m two\x07\n<b>three</b>"}
	s, _ := b.p.Record()
	if s.Run.Orchestrator == nil {
		t.Fatal("the fixture has no lead agent")
	}
	if text := p.ask("GET", contract.RouteTeam, nil, nil).Body.String(); !strings.Contains(text, "one[31m two\n&lt;b&gt;three&lt;/b&gt;") || !strings.Contains(text, `action="/do/note-lead"`) {
		t.Errorf("the foot of a phone's page:\n%s", text)
	}
	if text := b.ask("GET", contract.RouteTeam, nil, nil).Body.String(); strings.Contains(text, "three") {
		t.Error("the laptop's team screen reads the lead agent's tab")
	}
	b.refused("a button without the phone's token", p.ask("POST", contract.RouteDo+"answer", url.Values{contract.FieldToken: {b.token()}, "id": {"q9"}, "text": {"x"}}, nil), http.StatusForbidden)
	b.refused("a button from another origin", p.ask("POST", contract.RouteDo+"answer", url.Values{contract.FieldToken: {p.token()}, "id": {"q9"}, "text": {"x"}},
		func(r *http.Request) { r.Header.Set("Origin", own) }), http.StatusForbidden)
	b.refused("an answer", p.ask("POST", contract.RouteDo+"answer", url.Values{contract.FieldToken: {p.token()}, "id": {"q9"}, "text": {"three sizes"}}, nil), http.StatusSeeOther)
	s, _ = b.p.Record()
	if q := s.Questions["q9"]; q.Answer != "three sizes" || q.AnsweredBy == nil || q.AnsweredBy.Where != contract.WherePhone || q.AnsweredBy.Caller != contract.Human {
		t.Errorf("the answer as recorded: %q by %+v", q.Answer, q.AnsweredBy)
	}
	w := p.ask("GET", contract.RouteTeam+"?"+url.Values{contract.FieldRoot: {b.p.Root}, contract.FieldRun: {s.Run.ID}}.Encode(), nil, nil)
	if got := w.Result().Cookies(); len(got) != 1 || !got[0].Secure || !got[0].HttpOnly {
		t.Errorf("the cookie of a phone's chosen job: %+v", got)
	}
}

// The nudge: a paired phone's script is answered with the login's public
// key, and what the phone then hands over is stored as given, in that
// phone's own record; with no key nothing is taken.
func TestWhatAPhoneHandsOverForItsNudges(t *testing.T) {
	b := house(t)
	p, other := b.paired(subject), b.paired(subject)
	key := "BK-the-public-half"
	b.s.k.PushKey = func() string { return key }
	hand := func(token string, fields ...string) *httptest.ResponseRecorder {
		form := url.Values{contract.FieldToken: {token}}
		for i := 0; i < len(fields); i += 2 {
			form.Set(fields[i], fields[i+1])
		}
		return p.ask("POST", contract.RoutePush, form, nil)
	}
	if w := p.ask("GET", contract.RoutePush, nil, nil); w.Code != http.StatusOK || w.Body.String() != key || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("the key for a phone's script: %d %q", w.Code, w.Body)
	}
	three := []string{"endpoint", "https://push.example/send/a b?c=d&e", "p256dh", "BPk_-=", "auth", "s3 cr+t"}
	b.refused("handing over with another phone's token", hand(other.token(), three...), http.StatusForbidden)
	b.refused("handing over with no token", hand("", three...), http.StatusForbidden)
	b.refused("handing over two of the three", hand(p.token(), three[:4]...), http.StatusBadRequest)
	if listOf(t, b).Paired[0].Push != nil {
		t.Fatal("a refused request was stored")
	}
	b.refused("handing over", hand(p.token(), three...), http.StatusNoContent)
	list := listOf(t, b)
	if to := list.Paired[0].Push; to == nil || to.Endpoint != three[1] || to.P256dh != three[3] || to.Auth != three[5] || list.Paired[1].Push != nil {
		t.Errorf("what was stored: %+v", list.Paired)
	}
	key = ""
	if w := p.ask("GET", contract.RoutePush, nil, nil); w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Errorf("with no key the script was answered %d %q", w.Code, w.Body)
	}
	b.refused("handing over when the login has no key", hand(p.token(), three...), http.StatusBadRequest)
}

// Reaching it from a phone: the one loopback port of the login's block, open
// only while phone access is on, and no other address; `dash phone` is the
// switch, the list and the way to forget.
func TestTheDoorIsOpenOnlyWhilePhoneAccessIsOn(t *testing.T) {
	door := aServer(t)
	values(t, team, public, tag, robin)
	publish(t)
	b := login(t, nil)
	dashweb.Plug(b.p.Kit)
	dirs := short(t, b.p)
	at := net.JoinHostPort(contract.Loopback, strconv.Itoa(door))
	for _, no := range [][2]string{{"tcp4", net.JoinHostPort("", strconv.Itoa(door))}, {"tcp", net.JoinHostPort("localhost", strconv.Itoa(door))},
		{"tcp4", net.JoinHostPort(contract.Loopback, strconv.Itoa(door-1))}, {"tcp6", net.JoinHostPort("::1", strconv.Itoa(door))}} {
		l, err := listen(no[0], no[1], door)
		if r := new(contract.Refusal); err == nil || !errors.As(err, &r) {
			t.Errorf("%v was opened as the door: %v", no, err)
		}
		if l != nil {
			l.Close()
		}
	}
	ended := make(chan error, 1)
	go func() { ended <- serve(b.p.Kit, dirs) }()
	until(t, "the server answering", func() bool { _, ok := ask(b.p.Kit, dirs, "status"); return ok })
	word := func(args ...string) (string, listed, error) {
		var out bytes.Buffer
		res, err := dash(&contract.Call{Kit: b.p.Kit, Args: args, Out: &out})
		got, _ := res.(listed)
		return out.String(), got, err
	}
	number := "" // what the phone that knocked last was shown
	knock := func() (int, error) {
		req, _ := http.NewRequest("GET", "http://"+at+contract.RoutePair, nil)
		req.Host = public
		req.Header.Set(contract.HeaderAccess, signed(cloud(), heading, nil))
		resp, err := (&http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		m := numberRE.FindSubmatch(body)
		if m == nil {
			return 0, nil
		}
		number = string(m[1]) + string(m[2])
		return resp.StatusCode, nil
	}
	if _, err := knock(); err == nil {
		t.Fatal("the door is open before anybody switched phone access on")
	}
	// Another program has the door's port: the command says so, and why, and
	// once the port is free the same command opens the door.
	taken, err := net.Listen("tcp4", net.JoinHostPort(contract.Loopback, strconv.Itoa(door)))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := word("phone", "on"); err == nil || err.(*contract.Refusal).Code != "door_shut" || !strings.Contains(err.Error(), "the door did not open: listen tcp4") ||
		!strings.Contains(err.Error(), "Another program has this login's port") {
		t.Fatalf("dash phone on with the door's port taken: %v", err)
	}
	taken.Close()
	said, got, err := word("phone", "on")
	if err != nil || !got.On || got.Name != public || !strings.Contains(said, "https://"+public+"/\n") {
		t.Fatalf("dash phone on: %v %+v %s", err, got, said)
	}
	if st, _ := ask(b.p.Kit, dirs, "status"); st.Door != door || !st.Running || st.Closed != "" {
		t.Errorf("the status with the door open: %+v", st)
	}
	if code, err := knock(); err != nil || code != http.StatusOK {
		t.Fatalf("a phone at the open door: %d %v", code, err)
	}
	// A person at a terminal sees the phone that asks and decides it there:
	// the command names the number, and the running server decides.
	spaced := number[:3] + " " + number[3:]
	phones := func() contract.Phones {
		list, err := contract.ReadPhones(b.p.Kit.Platform.Read, dirs.State)
		if err != nil {
			t.Fatal(err)
		}
		return list
	}
	if said, got, err := word("phone", "list"); err != nil || got.Asking == nil || got.Asking.Number != spaced ||
		!strings.Contains(said, "A phone asks to be paired: "+spaced+" · "+named) || !strings.Contains(said, "> whaleshark dash phone approve "+number) {
		t.Fatalf("dash phone list with a phone asking: %v %+v\n%s", err, got, said)
	}
	other := "000000"
	if other == number {
		other = "000001"
	}
	for _, args := range [][]string{{"phone", "approve", other}, {"phone", "refuse", other}, {"phone", "approve"}, {"phone", "approve", number, "more"}} {
		if said, _, err := word(args...); err == nil || len(phones().Paired) != 0 {
			t.Fatalf("dash %v was taken: %s", args, said)
		}
	}
	if said, _, err := word("phone", "refuse", "pair"+number); err != nil || !strings.Contains(said, "refused") || phones().Asking != nil {
		t.Fatalf("dash phone refuse: %v %s, and the file still asks: %+v", err, said, phones().Asking)
	}
	if said, _, err := word("phone", "approve", number); err == nil {
		t.Fatalf("a refused number was approved afterwards: %s", said)
	}
	if code, err := knock(); err != nil || code != http.StatusOK {
		t.Fatalf("a second phone at the door: %d %v", code, err)
	}
	if said, _, err := word("phone", "approve", number[:3]+" "+number[3:]); err != nil || !strings.Contains(said, "is paired") {
		t.Fatalf("dash phone approve: %v %s", err, said)
	}
	if list := phones(); len(list.Paired) != 1 || list.Paired[0].ID != "ph1" || list.Asking != nil {
		t.Fatalf("after the approval at a terminal the phones are %+v", list)
	}
	if said, _, err := word("phone", "forget", "ph1"); err != nil {
		t.Fatalf("dash phone forget: %v %s", err, said)
	}
	if err := contract.ChangePhones(b.p.Kit.Platform, func(list *contract.Phones) error {
		list.Paired = append(list.Paired, contract.Phone{ID: "ph1", Name: "iPhone", Identity: named, Session: "x", Push: &contract.PushTo{Auth: "secret-of-the-nudge"}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if said, got, err = word("phone", "list"); err != nil || len(got.Phones) != 1 || !got.Phones[0].Nudges || !strings.Contains(said, "ph1  iPhone  "+named) || strings.Contains(said, "secret") {
		t.Errorf("dash phone list: %v %+v %s", err, got, said)
	}
	for _, bad := range [][]string{{"phone"}, {"phone", "open"}, {"phone", "forget"}, {"phone", "on", "now"}} {
		if _, _, err := word(bad...); err == nil || err.(*contract.Refusal).Exit != contract.ExitUsage {
			t.Errorf("dash %v: %v", bad, err)
		}
	}
	if _, _, err := word("phone", "forget", "ph9"); err == nil || err.(*contract.Refusal).Exit != contract.ExitMissing {
		t.Errorf("forgetting a phone nobody paired: %v", err)
	}
	if _, got, err = word("phone", "forget", "ph1"); err != nil || len(got.Phones) != 0 || !got.On {
		t.Errorf("dash phone forget: %v %+v", err, got)
	}
	if said, got, err = word("phone", "off"); err != nil || got.On || !strings.Contains(said, "closed") {
		t.Fatalf("dash phone off: %v %+v %s", err, got, said)
	}
	if code, err := knock(); err == nil {
		t.Errorf("with phone access off the door answered %d", code)
	}
	// A login the administrator gave no public name has no door to open.
	aServer(t)
	if _, _, err := word("phone", "on"); err == nil || err.(*contract.Refusal).Code != "no_door" || listOf(t, b).On {
		t.Errorf("dash phone on with no public name: %v", err)
	}
	if _, _, err := word("stop"); err != nil {
		t.Fatal(err)
	}
	if err := <-ended; err != nil {
		t.Errorf("the server ended with %v", err)
	}
}

// The evening on a phone: the same screen as the laptop's, from the same
// view, with nothing in another order.
func TestTheEveningOnAPhone(t *testing.T) {
	b := house(t)
	rows := regexp.MustCompile(` id="([ic]-[^"]+)"`)
	wide := rows.FindAllString(b.ask("GET", contract.RouteTeam, nil, nil).Body.String(), -1)
	p := b.paired(subject)
	narrow := rows.FindAllString(p.ask("GET", contract.RouteTeam, nil, nil).Body.String(), -1)
	if len(wide) < 10 || strings.Join(wide, "") != strings.Join(narrow, "") {
		t.Errorf("the rows of the laptop's page %v\nand of the phone's %v", wide, narrow)
	}
}

// A login with no open run can still pair a phone: the request is the one
// item of its laptop's page, and the phone is then shown the same nothing.
func TestPairingNeedsNoOpenRun(t *testing.T) {
	aServer(t)
	values(t, team, public, tag, robin)
	publish(t)
	b := login(t, nil).signIn()
	dashweb.Plug(b.p.Kit)
	access(t, b.p.Kit, true)
	p := &phone{b: b, who: subject}
	number := p.knock()
	if text := b.ask("GET", contract.RouteTeam, nil, nil).Body.String(); !strings.Contains(text, "A phone wants to connect: "+number[:3]) || !strings.Contains(text, `action="`+contract.RouteDo+`phone-yes"`) {
		t.Fatalf("the laptop's page with no run:\n%s", text)
	}
	b.refused("approving", b.decide(number, "approve"), http.StatusSeeOther)
	if w := p.ask("GET", contract.RouteTeam, nil, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "no open run") || len(listOf(t, b).Paired) != 1 {
		t.Errorf("the phone's page with no run: %d %s", w.Code, w.Body)
	}
}
