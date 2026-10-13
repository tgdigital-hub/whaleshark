package doctor

import (
	"bytes"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// wired is the wire of a test: no request leaves the machine, each is kept,
// and the answer points where the test says.
type wired struct {
	to   string
	err  error
	reqs []*http.Request
}

func (a *wired) RoundTrip(r *http.Request) (*http.Response, error) {
	a.reqs = append(a.reqs, r)
	if a.err != nil {
		return nil, a.err
	}
	return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {a.to}}, Body: http.NoBody, Request: r}, nil
}

func pretend(t *testing.T, to string) *wired {
	a, was, version := &wired{to: to}, wire, contract.Version
	wire, contract.Version = a, "1.2.0"
	t.Cleanup(func() { wire, contract.Version = was, version })
	return a
}

// The notice asks one fixed address once a day, says who asks with one word
// and nothing else, follows nothing, and keeps a version number only; with
// the setting off, and on a server, it never asks.
func TestTheVersionNoticeAsksOnceADayAndSendsNothing(t *testing.T) {
	d := project(t)
	if real, _ := wire.(*http.Transport); real == nil || real.Proxy != nil {
		t.Fatal("the request may go by a proxy")
	}
	a := pretend(t, releases+"tag/v1.3")
	seen := func() contract.VersionSeen { return contract.ReadVersion(d.k.Platform.Peek, d.dirs.State) }
	d.k.Notice(d.k)
	d.k.Notice(d.k)
	if len(a.reqs) != 1 || seen().Seen != "v1.3" || time.Since(seen().At) > time.Minute {
		t.Fatalf("asked %d times, kept %+v", len(a.reqs), seen())
	}
	r := a.reqs[0]
	if r.URL.String() != releases+"latest" || r.Method != http.MethodGet || r.Body != nil || len(r.Header) != 1 || r.Header.Get("User-Agent") != "whaleshark" || r.URL.RawQuery != "" {
		t.Errorf("the request says more than who asks: %s %s %v", r.Method, r.URL, r.Header)
	}
	e, _ := d.doctor()
	d.want(e, note, "this is whaleshark 1.2.0: version 1.3 is out · upgrade it the way you installed it")
	if len(a.reqs) != 1 {
		t.Error("doctor asked")
	}

	// A day later it asks again. An answer that is no version number, one
	// that points elsewhere and a request that fails change nothing but the
	// day, so that the next is not made before tomorrow.
	old := func() {
		v := seen()
		v.At = v.At.Add(-contract.NoticeEvery - time.Minute)
		if err := contract.WriteVersioned(d.k.Platform, filepath.Join(d.dirs.State, contract.VersionFile), contract.FileVersion, &v); err != nil {
			t.Fatal(err)
		}
	}
	for i, to := range []string{releases + "tag/<script>", releases + "tag/1.4/../../x", "https://elsewhere.example/tag/v9", releases, ""} {
		old()
		a.to, a.err = to, nil
		if to == "" {
			a.err = errors.New("no network")
		}
		d.k.Notice(d.k)
		d.k.Notice(d.k)
		if len(a.reqs) != i+2 || seen().Seen != "v1.3" || time.Since(seen().At) > time.Minute {
			t.Errorf("answer %q: asked %d times, kept %+v", to, len(a.reqs), seen())
		}
	}
	n := len(a.reqs)
	old()
	d.write(filepath.Join(d.dirs.Config, contract.ConfigFile), "[update]\nnotice = false\n")
	d.k.Notice(d.k)
	d.write(filepath.Join(d.dirs.Config, contract.ConfigFile), "")
	d.write(contract.ServerFile, file)
	d.k.Notice(d.k)
	if e, _ = d.doctor(); len(a.reqs) != n || e.line("is out") != nil || e.line("this is whaleshark 1.2.0") == nil {
		t.Errorf("switched off or on a server: asked %d times more\n%s", len(a.reqs)-n, e.text())
	}
}

// On a server the administrator's own `server status` asks, at most once a
// day, and adds the line; a work login's does not, nor one whose server's
// file says no.
func TestServerStatusSaysANewerReleaseToTheAdministrator(t *testing.T) {
	status := func(root bool, text string) (string, int) {
		b := server(t, text, root)
		a := pretend(t, releases+"tag/v2.0")
		out := new(bytes.Buffer)
		b.Setenv("WS_LOG", filepath.Join(b.sys, "script.log"))
		script = "echo the machine is well\n"
		for range 2 {
			out.Reset()
			if _, err := serve(&contract.Call{Kit: b.k, Command: contract.Find("server"), Args: []string{"status"}, Flags: map[string][]string{}, Now: time.Now(), Out: out, Err: new(bytes.Buffer)}); err != nil {
				t.Fatal(err)
			}
		}
		return out.String(), len(a.reqs)
	}
	if out, n := status(true, file); n != 1 || out != "the machine is well\nversion 2.0 is out · upgrade it the way you installed it\n" {
		t.Errorf("the administrator: asked %d times, %q", n, out)
	}
	if out, n := status(false, file); n != 0 || strings.Contains(out, "is out") {
		t.Errorf("a work login: asked %d times, %q", n, out)
	}
	if out, n := status(true, "notice = false\n"+file); n != 0 || strings.Contains(out, "is out") {
		t.Errorf("notice = false: asked %d times, %q", n, out)
	}
}

// doctor ends with where a problem is reported, and what it printed can be
// pasted there: no value of a tool's variable, no address a nudge goes to,
// nothing of a phone's session or keys.
func TestDoctorEndsWithWhereToReportAndHoldsNoSecret(t *testing.T) {
	d := project(t)
	secrets := []string{"sk-tool-variable", "notices.example/private-topic", "phone-session-word", "phone-key-word", "phone-auth-word"}
	d.write(filepath.Join(d.dirs.Config, contract.ToolsDir, "hermes.toml"),
		"run = ['"+d.program(t.TempDir(), "hermes")+"']\nhooks = 'none'\n[env]\nMODEL_KEY = '"+secrets[0]+"'\n")
	d.write(filepath.Join(d.dirs.Config, contract.ConfigFile), "[notify]\nurl = \"https://"+secrets[1]+"\"\n")
	d.write(filepath.Join(d.dirs.State, contract.PhonesFile), `{"version":1,"paired":[{"id":"ph1","name":"a phone","session":"`+secrets[2]+
		`","paired_at":"2026-10-08T21:00:00Z","push":{"endpoint":"https://push.example/1","p256dh":"`+secrets[3]+`","auth":"`+secrets[4]+`"}}]}`)
	t.Setenv("MODEL_KEY", secrets[0])
	for _, flags := range [][]string{nil, {"notify"}, {"fix"}} {
		out := new(bytes.Buffer)
		c := &contract.Call{Kit: d.k, Command: contract.Find("doctor"), Flags: map[string][]string{}, Caller: d.caller, Root: d.root, Now: time.Now(), Out: out, Err: out}
		for _, f := range flags {
			c.Flags[f] = []string{""}
		}
		run(c)
		if !strings.HasSuffix(out.String(), "\n\n"+contract.ReportLine+"\nPaste this output with your report: it holds no secret.\n") || !strings.Contains(out.String(), "agent tool hermes") {
			t.Errorf("doctor %v ends:\n%s", flags, out)
		}
		for _, s := range secrets {
			if strings.Contains(out.String(), s) {
				t.Errorf("doctor %v prints %q:\n%s", flags, s, out)
			}
		}
	}
}
