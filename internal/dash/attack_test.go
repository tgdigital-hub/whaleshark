package dash

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/view"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func TestMain(m *testing.M) { scenario.Main(m) }

// The attacks of the design's page table, one test a row, each refused. The
// server is the real one over the evening's fixture in a login of its own;
// what a button starts is the real program.
const own = "http://whaleshark.localhost:7780"

type browser struct {
	t      *testing.T
	s      *server
	p      *scenario.Project
	cookie []*http.Cookie
}

func evening(t *testing.T) *browser {
	t.Helper()
	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	return login(t, f)
}

// login is a browser before a server in a pretended login: with a
// fixture's run, or with no project at all.
func login(t *testing.T, f *testkit.Fixture) *browser {
	t.Helper()
	p := scenario.Prepare(t, f, nil)
	view.Plug(p.Kit)
	for _, kv := range p.Env("") {
		if name, value, _ := strings.Cut(kv, "="); name != contract.EnvPane {
			t.Setenv(name, value)
		}
	}
	return &browser{t: t, p: p, s: serverOf(t, p.Kit)}
}

func serverOf(t *testing.T, k *contract.Kit) *server {
	t.Helper()
	dirs, err := k.Platform.Dirs()
	if err != nil {
		t.Fatal(err)
	}
	s, err := newServer(k, dirs)
	if err != nil {
		t.Fatal(err)
	}
	if s.self, err = exec.LookPath("whaleshark"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.stop)
	return s
}

// ask sends one request as a browser on the page's own name would, with the
// cookies this browser holds; change alters it before it goes.
func (b *browser) ask(method, path string, form url.Values, change func(*http.Request)) *httptest.ResponseRecorder {
	b.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, own+path, body)
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", own)
		r.Header.Set("Referer", own+contract.RouteTeam)
	}
	for _, c := range b.cookie {
		r.AddCookie(c)
	}
	if change != nil {
		change(r)
	}
	w := httptest.NewRecorder()
	b.s.ServeHTTP(w, r)
	return w
}

func (b *browser) signIn() *browser {
	b.t.Helper()
	w := b.ask("GET", contract.RouteIn+"?code="+newCode(b.s.key, "in", contract.Now()), nil, nil)
	if w.Code != http.StatusSeeOther || len(w.Result().Cookies()) != 1 {
		b.t.Fatalf("signing in: %d %s", w.Code, w.Body)
	}
	b.cookie = w.Result().Cookies()
	return b
}

// token is the request token of the browser's session, as a screen carries it.
func (b *browser) token() string {
	id, _, _ := strings.Cut(b.cookie[0].Value, ".")
	return mac(b.s.key, "token", id)
}

func (b *browser) refused(what string, w *httptest.ResponseRecorder, code int) {
	b.t.Helper()
	if w.Code != code {
		b.t.Errorf("%s: answered %d, not %d: %.200s", what, w.Code, code, w.Body)
	}
}

// Reaching it at all: there is a socket file and no other door.
func TestNoDoorButTheSocketFile(t *testing.T) {
	for _, door := range [][2]string{{"tcp", ":7780"}, {"tcp", "localhost:7780"}, {"tcp4", contract.Loopback + ":0"}, {"udp", ":0"}} {
		l, err := listen(door[0], door[1])
		if r := new(contract.Refusal); err == nil || !errors.As(err, &r) {
			t.Errorf("%v was opened: %v", door, err)
		}
		if l != nil {
			l.Close()
		}
	}
}

// Signing in: a code works once, for a minute, and a used one stays used
// when the server starts again; the cookie is one scripts cannot read and no
// other site's request carries, and a forged or ended one is nobody's.
func TestSigningIn(t *testing.T) {
	b := evening(t)
	now := contract.Now()
	code := newCode(b.s.key, "in", now)
	old := newCode(b.s.key, "in", now.Add(-codeLife-time.Second))
	early := newCode(b.s.key, "in", now.Add(time.Hour))
	other := newCode(b.s.key, "status", now)
	forged := newCode([]byte(strings.Repeat("k", 40)), "in", now)
	for name, c := range map[string]string{"old": old, "from the future": early, "for another purpose": other, "forged": forged, "empty": "", "no code": "x.y"} {
		b.refused("a code that is "+name, b.ask("GET", contract.RouteIn+"?code="+url.QueryEscape(c), nil, nil), http.StatusForbidden)
		b.s.fails = 0 // the slowing has its own test
	}
	b.refused("no session", b.ask("GET", contract.RouteTeam, nil, nil), http.StatusForbidden)
	w := b.ask("GET", contract.RouteIn+"?code="+code, nil, nil)
	if b.refused("the right code", w, http.StatusSeeOther); len(w.Result().Cookies()) != 1 {
		t.Fatal("no cookie")
	}
	c := w.Result().Cookies()[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.MaxAge != int(sessionLife/time.Second) || c.Name != b.s.cookie {
		t.Errorf("the cookie: %+v", c)
	}
	b.refused("the code a second time", b.ask("GET", contract.RouteIn+"?code="+code, nil, nil), http.StatusForbidden)
	again := &browser{t: t, p: b.p, s: serverOf(t, b.p.Kit)}
	again.refused("the code after a restart", again.ask("GET", contract.RouteIn+"?code="+code, nil, nil), http.StatusForbidden)

	b.cookie = []*http.Cookie{c}
	b.refused("the session", b.ask("GET", contract.RouteTeam, nil, nil), http.StatusOK)
	id, _, _ := strings.Cut(c.Value, ".")
	for name, value := range map[string]string{
		"forged":  id + ".zzzzzz." + mac([]byte("another key"), "session", id, "zzzzzz"),
		"ended":   id + ".1." + mac(b.s.key, "session", id, "1"),
		"cut off": id,
	} {
		b.cookie = []*http.Cookie{{Name: c.Name, Value: value}}
		b.refused("a cookie that is "+name, b.ask("GET", contract.RouteTeam, nil, nil), http.StatusForbidden)
	}
	// A new key, as `dash stop --forget` writes, ends every session.
	dirs, _ := b.p.Kit.Platform.Dirs()
	if _, err := readKey(b.p.Kit.Platform, dirs, true, true); err != nil {
		t.Fatal(err)
	}
	fresh := &browser{t: t, p: b.p, s: serverOf(t, b.p.Kit), cookie: []*http.Cookie{c}}
	fresh.refused("the session after --forget", fresh.ask("GET", contract.RouteTeam, nil, nil), http.StatusForbidden)
}

// A session in use for a day is given a new end, so it lasts as long as it
// is used.
func TestASessionIsRenewedByUse(t *testing.T) {
	b := evening(t).signIn()
	if w := b.ask("GET", contract.RouteTeam, nil, nil); len(w.Result().Cookies()) != 0 {
		t.Error("a fresh session was written again")
	}
	b.p.Clock(renewAfter + time.Minute)
	w := b.ask("GET", contract.RouteTeam, nil, nil)
	if got := w.Result().Cookies(); w.Code != http.StatusOK || len(got) != 1 || got[0].Value == b.cookie[0].Value {
		t.Errorf("a day later: %d, cookies %v", w.Code, got)
	}
	b.p.Clock(sessionLife)
	b.refused("a session left alone for thirty days", b.ask("GET", contract.RouteTeam, nil, nil), http.StatusForbidden)
}

// Flooding: after five wrong codes the next is not looked at, right or
// wrong; and a body over the cap is not read.
func TestFlooding(t *testing.T) {
	b := evening(t)
	for range slowFrom {
		b.refused("a wrong code", b.ask("GET", contract.RouteIn+"?code=1.x", nil, nil), http.StatusForbidden)
	}
	right := contract.RouteIn + "?code=" + newCode(b.s.key, "in", contract.Now())
	b.refused("the right code straight after five wrong ones", b.ask("GET", right, nil, nil), http.StatusTooManyRequests)
	b.s.failed = time.Now().Add(-2 * time.Second)
	w := b.ask("GET", right, nil, nil)
	b.refused("the right code a moment later", w, http.StatusSeeOther)

	b.cookie = w.Result().Cookies()
	before, _ := b.p.Record()
	big := url.Values{contract.FieldToken: {b.token()}, "id": {"q9"}, "text": {strings.Repeat("x", maxBody)}}
	b.refused("a body over the cap", b.ask("POST", contract.RouteDo+"answer", big, nil), http.StatusForbidden)
	if after, _ := b.p.Record(); after.Questions["q9"].State != before.Questions["q9"].State {
		t.Error("the oversized answer was taken")
	}
}

// Another site: a change is a POST from the page's own origin that carries
// the session's token, and nothing else is one.
func TestAnotherSitesRequests(t *testing.T) {
	b := evening(t).signIn()
	good := url.Values{contract.FieldToken: {b.token()}, "task": {"T2"}}
	foreign := func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }
	b.refused("a POST from another origin", b.ask("POST", contract.RouteDo+"stop", good, foreign), http.StatusForbidden)
	b.refused("a POST from another port of this name", b.ask("POST", contract.RouteDo+"stop", good,
		func(r *http.Request) { r.Header.Set("Origin", "http://whaleshark.localhost:20143") }), http.StatusForbidden)
	b.refused("a POST that names no origin", b.ask("POST", contract.RouteDo+"stop", good,
		func(r *http.Request) { r.Header.Del("Origin") }), http.StatusForbidden)
	b.refused("a read from another origin", b.ask("GET", contract.RouteTeam, nil, foreign), http.StatusForbidden)
	b.refused("a POST with no token", b.ask("POST", contract.RouteDo+"stop", url.Values{"task": {"T2"}}, nil), http.StatusForbidden)
	b.refused("a POST with another session's token", b.ask("POST", contract.RouteDo+"stop",
		url.Values{contract.FieldToken: {mac(b.s.key, "token", "another session")}, "task": {"T2"}}, nil), http.StatusForbidden)
	b.refused("a change asked for with GET", b.ask("GET", contract.RouteDo+"stop?task=T2&t="+b.token(), nil, nil), http.StatusMethodNotAllowed)
	b.refused("a POST to a screen", b.ask("POST", contract.RouteTeam, good, nil), http.StatusMethodNotAllowed)
	b.refused("a DELETE", b.ask("DELETE", contract.RouteTask+"T2", nil, nil), http.StatusMethodNotAllowed)
	if s, _ := b.p.Record(); !s.Attempts[s.Tasks["T2"].Attempts[0]].State.Live() {
		t.Error("one of them stopped the task")
	}
}

// A made-up host name pointed at the page: only the page's own two names.
func TestAMadeUpHostName(t *testing.T) {
	b := evening(t).signIn()
	for _, host := range []string{"evil.example", "localhost:7780", "whaleshark.localhost.evil.example", "", "0:7780"} {
		b.refused("the host "+host, b.ask("GET", contract.RouteTeam, nil, func(r *http.Request) { r.Host = host }), http.StatusForbidden)
	}
	for _, host := range []string{contract.PageHosts[0], contract.Loopback + ":7780", contract.Loopback} {
		b.refused("the host "+host, b.ask("GET", contract.RouteTeam, nil, func(r *http.Request) { r.Host = host }), http.StatusOK)
	}
}

// What a button can run: one row of the action table the page may use, with
// an id the view lists now and words that cannot be an option; never a
// command of the browser's, and never one of the rows no screen may run.
func TestWhatAButtonCanRun(t *testing.T) {
	b := evening(t).signIn()
	post := func(button string, fields ...string) *httptest.ResponseRecorder {
		form := url.Values{contract.FieldToken: {b.token()}}
		for i := 0; i < len(fields); i += 2 {
			form.Set(fields[i], fields[i+1])
		}
		return b.ask("POST", contract.RouteDo+url.PathEscape(button), form, nil)
	}
	before, _ := b.p.Record()
	for _, never := range []string{"land", "trust", "run", "task", "report", "progress", "ask", "mail", "go", "fleet", "actions", "to-actions",
		"narrower", "menu", "show", "no-such", "", "answer/../land", "start --retry"} {
		b.refused("the button "+never, post(never, "task", "T8", "id", "q9"), http.StatusForbidden)
	}
	for _, a := range contract.Actions {
		for _, never := range contract.Never {
			if line := strings.Join(a.Run, " "); row(a.ID) != nil && strings.HasPrefix(line+" ", never+" ") {
				t.Errorf("the page may run %q, which begins with %q", line, never)
			}
		}
	}
	b.refused("an item nobody listed", post("answer", "id", "q99", "text", "yes"), http.StatusConflict)
	b.refused("a task nobody listed", post("stop", "task", "T99"), http.StatusConflict)
	b.refused("a button its card does not show", post("accept", "task", "T7"), http.StatusConflict)
	b.refused("a task that is an option", post("stop", "task", "--everywhere"), http.StatusConflict)
	b.refused("no task", post("stop"), http.StatusConflict)
	b.refused("a setting that is an option", post("set", "key", "theme", "value", "--root=/"), http.StatusConflict)
	b.refused("a setting of two lines", post("set", "key", "theme", "value", "reef\n--human"), http.StatusConflict)
	b.refused("a word that is too long", post("team", "name", strings.Repeat("a", wordMost+1)), http.StatusConflict)
	b.refused("a pointer for no item", post("carry-on", "id", "q9"), http.StatusConflict)
	b.refused("a phone no screen lists", post("forget-phone", "id", "q9x"), http.StatusConflict)
	if s, _ := b.p.Record(); s.Questions["q9"].State != contract.QuestionOpen || len(s.Inbox.Events) != len(before.Inbox.Events) {
		t.Fatal("a refused button changed the record")
	}

	// Free text rides in a file: an answer that looks like an option is the answer.
	w := post("answer", "id", "q9", "text", "--undo")
	b.refused("an answer", w, http.StatusSeeOther)
	s, _ := b.p.Record()
	if q := s.Questions["q9"]; q.Answer != "--undo" || q.AnsweredBy == nil || q.AnsweredBy.Where != contract.WherePage || q.AnsweredBy.Caller != contract.Human {
		t.Errorf("the answer as recorded: %+v by %+v", q.Answer, q.AnsweredBy)
	}
	// The command's own refusal is shown where the click was made.
	w = post("undo", "id", "q7")
	if b.refused("an undo of nothing", w, http.StatusOK); !strings.Contains(w.Body.String(), contract.ScreenTeam) {
		t.Errorf("the refusal was drawn as %q", w.Body)
	}
}

// Every child carries the page's mark and neither a pane nor an attempt,
// whatever tab the server itself was started in.
func TestAChildCarriesThePagesMark(t *testing.T) {
	t.Setenv(contract.EnvPane, "w1:p7")
	t.Setenv(contract.EnvAttempt, "T3.1")
	t.Setenv(contract.EnvFrom, contract.WherePane)
	b := evening(t)
	var from []string
	for _, kv := range b.s.env {
		switch name, value, _ := strings.Cut(kv, "="); name {
		case contract.EnvPane, contract.EnvAttempt:
			t.Errorf("a child would carry %s", kv)
		case contract.EnvFrom:
			from = append(from, value)
		}
	}
	if !slices.Equal(from, []string{contract.WherePage}) {
		t.Errorf("the mark: %q", from)
	}
}

type files struct {
	contract.NoEvidence
	list []contract.EvidenceFile
}

func (f files) Open(_, _, _ string, i int) (io.ReadCloser, contract.EvidenceFile, error) {
	if i >= len(f.list) {
		return nil, contract.EvidenceFile{}, errors.New("no such file")
	}
	return io.NopCloser(strings.NewReader("<svg onload=alert(1)>")), f.list[i], nil
}

// Evidence files: by number from the tool's own list, never by a name; a
// picture of the three kinds is shown, anything else is a download, and the
// browser is told not to guess.
func TestEvidenceFiles(t *testing.T) {
	b := evening(t).signIn()
	b.p.Kit.Evidence = files{list: []contract.EvidenceFile{{Name: "home.png", Kind: "png"}, {Name: `a";b.svg`}, {Name: "x.html", Kind: "html"}}}
	s, _ := b.p.Record()
	attempt := s.Tasks["T8"].Attempts[0]
	at := contract.RouteEvidence + attempt + "/"
	for _, path := range []string{at + "home.png", at + "..%2f..%2fstate.json", at + "-1", at + "3", at + "0/../../x", at,
		contract.RouteEvidence + "T99.1/0", contract.RouteEvidence + "..%2f" + attempt + "/0", contract.RouteEvidence} {
		b.refused(path, b.ask("GET", path, nil, nil), http.StatusNotFound)
	}
	w := b.ask("GET", at+"0", nil, nil)
	if b.refused("a picture", w, http.StatusOK); w.Header().Get("Content-Type") != "image/png" || w.Header().Get("Content-Disposition") != "" {
		t.Errorf("a picture: %v", w.Header())
	}
	for _, n := range []string{"1", "2"} {
		w = b.ask("GET", at+n, nil, nil)
		h := w.Header()
		if w.Code != http.StatusOK || h.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") ||
			h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("file %s, which is no picture: %d %v", n, w.Code, h)
		}
	}
	b.cookie = nil
	b.refused("a picture for nobody", b.ask("GET", at+"0", nil, nil), http.StatusForbidden)
}

type screens struct {
	contract.Terminals
	text string
}

func (s screens) Screen(string) (string, error) { return s.text, nil }

// Scripts in what agents wrote, and the page in a frame: every answer says
// that nothing inline and nothing from outside may run and that no page may
// frame it; an agent's screen is plain text with its control codes gone.
func TestHeadersAndAnAgentsScreen(t *testing.T) {
	b := evening(t)
	b.p.Kit.Terms = screens{b.p.Kit.Terms, "\x1b]0;title\x07red\x1b[31m <script>x</script>\u009b2J\r\nnext\tline\x00"}
	var lead string
	b.p.Kit.Page = func(w io.Writer, p *contract.Page) error {
		if p.Screen == contract.ScreenLead {
			lead = p.Lead
		}
		_, err := io.WriteString(w, p.Screen)
		return err
	}
	check := func(what string, w *httptest.ResponseRecorder) {
		h := w.Header()
		csp := h.Get("Content-Security-Policy")
		for _, part := range []string{"default-src 'self'", "frame-ancestors 'none'", "form-action 'self'", "base-uri 'none'"} {
			if !strings.Contains(csp, part) {
				t.Errorf("%s: the policy %q lacks %q", what, csp, part)
			}
		}
		if strings.Contains(csp, "unsafe") || strings.Contains(csp, "http") || strings.Contains(csp, "*") {
			t.Errorf("%s: the policy %q lets something in", what, csp)
		}
		if h.Get("X-Frame-Options") != "DENY" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %v", what, h)
		}
	}
	check("a refusal", b.ask("GET", contract.RouteTeam, nil, nil))
	check("a wrong host", b.ask("GET", contract.RouteTeam, nil, func(r *http.Request) { r.Host = "evil.example" }))
	b.signIn()
	for _, path := range []string{contract.RouteTeam, contract.RouteLead, contract.RouteLive + "x", contract.RouteSites} {
		check(path, b.ask("GET", path, nil, nil))
	}
	if want := "]0;titlered[31m <script>x</script>2J\nnext\tline"; lead != want {
		t.Errorf("the lead agent's screen: %q, want %q", lead, want)
	}
}
