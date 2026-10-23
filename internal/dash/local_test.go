package dash

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/dashweb"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// The road where no server is: the door on the port the person picked, of
// the machine itself, reached from the phone over ssh's forward under
// localhost, or through a program in front of it that ends HTTPS under the
// name the person gave (tailscale serve). Nothing names the caller; a phone
// is paired by its number, as on every road.
const (
	forward = "localhost:8822"
	tailnet = "box.tailnet.example"
)

// noServer makes this machine no server, and the person's settings pick a
// port and a name for the phone.
func noServer(t *testing.T, k *contract.Kit, port int) contract.Dirs {
	t.Helper()
	file := contract.ServerFile
	t.Cleanup(func() { contract.ServerFile = file })
	contract.ServerFile = filepath.Join(t.TempDir(), "none.toml")
	dirs, err := k.Platform.Dirs()
	if err == nil {
		err = os.MkdirAll(dirs.Config, 0o700)
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(dirs.Config, contract.ConfigFile), fmt.Appendf(nil, "[phone]\nport = %d\nname = %q\n", port, tailnet), 0o600)
	}
	if err != nil {
		t.Fatal(err)
	}
	return dirs
}

// plainly is the evening where no server is: the person signed in on the
// laptop's road, and phone access on.
func plainly(t *testing.T) *browser {
	t.Helper()
	b := evening(t).signIn()
	dashweb.Plug(b.p.Kit)
	noServer(t, b.p.Kit, 8822)
	access(t, b.p.Kit, true)
	return b
}

// overSSH is a phone that reaches the door over ssh's forward and is paired.
func (b *browser) overSSH() *phone {
	b.t.Helper()
	p := &phone{b: b, agent: iphone, host: forward, scheme: "http://"}
	if w := b.decide(p.knock(), "approve"); w.Code != http.StatusSeeOther {
		b.t.Fatalf("approving: %d %s", w.Code, w.Body)
	}
	return p
}

// From a phone over ssh, the page does what the person needs away from the
// computer: every agent's state, the lead agent's last screen, a line typed
// to it, an answer, Stop all and Resume. A note for a worker is stored for
// it and is never typed into its tab.
func TestFromAPhoneOverSSH(t *testing.T) {
	b := plainly(t)
	p := &phone{b: b, agent: iphone, host: forward, scheme: "http://"}
	w := p.ask("GET", contract.RoutePair, nil, nil)
	got := w.Result().Cookies()
	if len(got) != 1 || !strings.HasPrefix(got[0].Name, "pws") || got[0].Secure || !got[0].HttpOnly || got[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("the cookie over ssh's plain forward: %v", w.Header()["Set-Cookie"])
	}
	if !numberRE.MatchString(w.Body.String()) {
		t.Fatalf("an unpaired phone over ssh: %d %.300s", w.Code, w.Body)
	}
	p.cookie = got
	m := numberRE.FindStringSubmatch(w.Body.String())
	if item := b.ask("GET", contract.RouteTeam, nil, nil).Body.String(); !strings.Contains(item, "A phone wants to connect: "+m[1]+" "+m[2]+" · nobody named · iPhone") {
		t.Errorf("the laptop's page does not show the phone that asks:\n%s", item)
	}
	b.refused("approving", b.decide(m[1]+m[2], "approve"), http.StatusSeeOther)

	b.s.k.Terms = screens{b.s.k.Terms, "the last line of the lead agent"}
	team := p.ask("GET", contract.RouteTeam, nil, nil).Body.String()
	for _, want := range []string{"sign-up page", "the last line of the lead agent", `action="/do/note-lead"`, `action="/do/stop-all"`} {
		if !strings.Contains(team, want) {
			t.Errorf("a paired phone's page over ssh has no %q:\n%.2000s", want, team)
		}
	}
	if lead := p.ask("GET", contract.RouteLead, nil, nil); lead.Code != http.StatusOK || !strings.Contains(lead.Body.String(), "the last line of the lead agent") {
		t.Errorf("the lead agent's screen: %d", lead.Code)
	}
	press := func(button string, fields ...string) {
		t.Helper()
		form := url.Values{contract.FieldToken: {p.token()}}
		for i := 0; i < len(fields); i += 2 {
			form.Set(fields[i], fields[i+1])
		}
		b.refused(button, p.ask("POST", contract.RouteDo+button, form, nil), http.StatusSeeOther)
	}
	press("answer", "id", "q9", "text", "three sizes")
	press("note-lead", "text", "ship it once the tests pass")
	press("note", "task", "T7", "text", "look at the sign-up form")
	s, _ := b.p.Record()
	if q := s.Questions["q9"]; q.Answer != "three sizes" || q.AnsweredBy == nil || q.AnsweredBy.Where != contract.WherePhone {
		t.Errorf("the answer as recorded: %q by %+v", q.Answer, q.AnsweredBy)
	}
	// The line for the lead agent is typed there as the person's (chat);
	// the one for a worker is not.
	for _, e := range s.Inbox.Events {
		chat := e.Data["chat"] == true && e.Data["where"] == contract.WherePhone
		if strings.Contains(e.Text, "ship it once") != chat {
			t.Errorf("the event of a note: %+v", e)
		}
	}
	stored := false
	for _, a := range s.Attempts {
		stored = stored || a.Task == "T7" && slices.ContainsFunc(a.Mail, func(m contract.Mail) bool { return strings.Contains(m.Text, "sign-up form") })
	}
	if !stored {
		t.Error("the note for a worker is not stored for it")
	}
	press("stop-all")
	if s, _ = b.p.Record(); s.Run.Paused == nil {
		t.Error("Stop all from the phone paused nothing")
	}
	if team := p.ask("GET", contract.RouteTeam, nil, nil).Body.String(); !strings.Contains(team, `action="/do/resume"`) {
		t.Error("a paused run's page on the phone has no Resume")
	}
	press("resume")
	if s, _ = b.p.Record(); s.Run.Paused != nil {
		t.Error("Resume from the phone left the run paused")
	}

	// What a phone may press types into the lead agent's tab and no other:
	// a pointer is the lead's, and a free text is typed only by tell lead.
	for _, a := range contract.Actions {
		if r := row(a.ID); r != nil && slices.Contains(r.Run, "tell") && !slices.Contains(r.Run, contract.Lead) && !slices.Contains(r.Run, "{task}") {
			t.Errorf("row %s tells somebody else: %v", a.ID, r.Run)
		}
	}
}

// The road is told apart by its host name, and checks the origin of what
// changes something as every road does: over ssh plain HTTP under the
// machine's own name, through a program in front HTTPS under the name the
// person gave, and under any other name nothing at all.
func TestTheLocalRoadsNamesAndOrigins(t *testing.T) {
	b := plainly(t)
	p := b.overSSH()
	b.refused("a button from the HTTPS origin over ssh", p.ask("POST", contract.RouteDo+"stop-all", url.Values{contract.FieldToken: {p.token()}},
		func(r *http.Request) { r.Header.Set("Origin", "https://"+forward) }), http.StatusForbidden)
	b.refused("a button from another site", p.ask("POST", contract.RouteDo+"stop-all", url.Values{contract.FieldToken: {p.token()}},
		func(r *http.Request) { r.Header.Set("Origin", "http://elsewhere.example") }), http.StatusForbidden)
	if s, _ := b.p.Record(); s.Run.Paused != nil {
		t.Fatal("a refused button paused the run")
	}
	// The cookie of ssh's road is nobody's under the name of the road in front.
	through := &phone{b: b, agent: iphone, host: tailnet, cookie: p.cookie}
	if w := through.ask("GET", contract.RouteTeam, nil, nil); w.Code != http.StatusSeeOther || w.Header().Get("Location") != contract.RoutePair {
		t.Errorf("the plain cookie under the HTTPS name: %d", w.Code)
	}
	w := through.ask("GET", contract.RoutePair, nil, nil)
	if got := w.Result().Cookies(); len(got) != 1 || !strings.HasPrefix(got[0].Name, bound) || !got[0].Secure || !numberRE.MatchString(w.Body.String()) {
		t.Errorf("through the program in front: %d %v", w.Code, w.Header()["Set-Cookie"])
	}
	for _, host := range []string{"elsewhere.example", "elsewhere.example:8822", "localhost.elsewhere.example"} {
		stranger := &phone{b: b, agent: iphone, host: host, scheme: "http://"}
		nothing(t, "under "+host, stranger.ask("GET", contract.RoutePair, nil, nil))
	}
	// A server's file takes the road away: there, the server's road holds.
	aServer(t)
	nothing(t, "over ssh on a server", p.ask("GET", contract.RouteTeam, nil, nil))
}

// On the real port: `dash phone on` wants a port picked, then opens the
// door on it, at the machine's own loopback address and on none another
// machine can reach, and `off` closes it.
func TestTheLocalDoorOnItsRealPort(t *testing.T) {
	base, release, err := testkit.Block()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	b := login(t, nil)
	dashweb.Plug(b.p.Kit)
	noServer(t, b.p.Kit, 0)
	dirs := short(t, b.p)
	ended := make(chan error, 1)
	go func() { ended <- serve(b.p.Kit, dirs) }()
	until(t, "the server answering", func() bool { _, ok := ask(b.p.Kit, dirs, "status"); return ok })
	word := func(args ...string) (string, listed, error) {
		var out bytes.Buffer
		res, err := dash(&contract.Call{Kit: b.p.Kit, Args: args, Out: &out})
		got, _ := res.(listed)
		return out.String(), got, err
	}
	if _, _, err := word("phone", "on"); err == nil || err.(*contract.Refusal).Code != "no_port" {
		t.Fatalf("dash phone on with no port picked: %v", err)
	}
	if out, err := b.p.Command(scenario.Human, "set", "phone.port", "80").CombinedOutput(); err == nil {
		t.Errorf("a port below 1024 was taken: %s", out)
	}
	if out, err := b.p.Command(scenario.Human, "set", "phone.port", strconv.Itoa(base)).CombinedOutput(); err != nil {
		t.Fatalf("set phone.port: %v %s", err, out)
	}
	said, got, err := word("phone", "on")
	if err != nil || !got.On || got.Name != tailnet || !strings.Contains(said, "ssh -L 8822:localhost:"+strconv.Itoa(base)+" ") {
		t.Fatalf("dash phone on: %v %+v\n%s", err, got, said)
	}
	if st, _ := ask(b.p.Kit, dirs, "status"); st.Door != base {
		t.Errorf("the status names door %d, not %d", st.Door, base)
	}
	knock := func(host string) (int, string) {
		req, _ := http.NewRequest("GET", "http://"+net.JoinHostPort(contract.Loopback, strconv.Itoa(base))+contract.RoutePair, nil)
		req.Host = host
		resp, err := (&http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	if code, body := knock(forward); code != http.StatusOK || !numberRE.MatchString(body) {
		t.Errorf("a phone over ssh at the real door: %d %.200s", code, body)
	}
	if code, body := knock("elsewhere.example"); code != http.StatusForbidden || body != "" {
		t.Errorf("another name at the real door: %d %q", code, body)
	}
	// Every other address of this machine is shut on that port.
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ip, ok := a.(*net.IPNet); ok && !ip.IP.IsLoopback() && !ip.IP.IsLinkLocalUnicast() {
			if c, err := net.DialTimeout("tcp", net.JoinHostPort(ip.IP.String(), strconv.Itoa(base)), time.Second); err == nil {
				c.Close()
				t.Errorf("the door answers at %s, which another machine can reach", ip.IP)
			}
		}
	}
	if _, _, err := word("phone", "off"); err != nil {
		t.Fatal(err)
	}
	if c, err := net.DialTimeout("tcp", net.JoinHostPort(contract.Loopback, strconv.Itoa(base)), time.Second); err == nil {
		c.Close()
		t.Error("the door is open after dash phone off")
	}
	if _, _, err := word("stop"); err != nil {
		t.Fatal(err)
	}
	if err := <-ended; err != nil {
		t.Errorf("the server ended with %v", err)
	}
}
