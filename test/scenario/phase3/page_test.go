package phase3

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// at is the laptop's page as a browser names it through connect.
var at = "http://" + contract.PageHosts[0]

// site is the real page's server of a scenario's login, `dash start` as a
// process of its own, and a browser that reaches it over its socket file.
type site struct {
	t    *testing.T
	p    *scenario.Project
	c    *http.Client
	sock string
}

// ask runs a command of the login's own and reads what it answers.
func ask(t *testing.T, p *scenario.Project, v any, args ...string) bool {
	t.Helper()
	out, _ := p.Command(scenario.Unbound, append(args, "--json")...).Output()
	env := contract.Envelope{Result: v}
	return json.Unmarshal(out, &env) == nil && env.OK
}

// serve starts the page's server as a login's service entry does and signs
// a browser in by the link `dash url` gives.
func serve(t *testing.T, p *scenario.Project) *site {
	t.Helper()
	cmd := p.Command(scenario.Unbound, "dash", "start")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.Command(scenario.Unbound, "dash", "stop").Run()
		cmd.Process.Kill()
		cmd.Wait()
	})
	var st struct{ Running bool }
	for end := time.Now().Add(20 * time.Second); !st.Running; time.Sleep(50 * time.Millisecond) {
		if ask(t, p, &st, "dash", "status"); time.Now().After(end) {
			t.Fatal("the page's server did not start")
		}
	}
	var u contract.DashURL
	if !ask(t, p, &u, "dash", "url") {
		t.Fatal("dash url gave no link")
	}
	jar, _ := cookiejar.New(nil)
	s := &site{t: t, p: p, sock: u.Socket, c: &http.Client{Jar: jar, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return new(net.Dialer).DialContext(ctx, "unix", u.Socket)
	}}}}
	t.Cleanup(s.c.CloseIdleConnections)
	if code, body := s.get(contract.RouteIn + "?" + contract.FieldCode + "=" + u.Code); code != 200 || !screen(body, contract.ScreenTeam) {
		t.Fatalf("signing in by the link: %d\n%s", code, body)
	}
	return s
}

func (s *site) get(path string) (int, string) {
	s.t.Helper()
	resp, err := s.c.Get(at + path)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func screen(body, name string) bool {
	return strings.Contains(body, `<body data-screen="`+name+`">`)
}

var (
	formRE  = regexp.MustCompile(`(?s)<form method="post" action="([^"]+)">(.*?)</form>`)
	fieldRE = regexp.MustCompile(`<input type="hidden" name="([^"]*)" value="([^"]*)">`)
	lineRE  = regexp.MustCompile(`<input name="([^"]*)" id="[^"]*" value="([^"]*)"`)
)

// press submits one form of the screen at path as a browser would: the form
// under the element of that id whose button has the label, with the fields
// the page wrote into it and, where it has a line, the words typed.
func (s *site) press(path, id, label, typed string) (int, string) {
	s.t.Helper()
	_, body := s.get(path)
	part := body
	if id != "" {
		_, rest, found := strings.Cut(body, ` id="`+id+`"`)
		if part, _, _ = strings.Cut(rest, "</article>"); !found {
			s.t.Fatalf("%s has nothing with the id %s:\n%s", path, id, body)
		}
	}
	for _, f := range formRE.FindAllStringSubmatch(part, -1) {
		line := lineRE.FindStringSubmatch(f[2])
		if !strings.Contains(f[2], ">"+label+"</button>") || (typed != "") != (line != nil) {
			continue
		}
		form := url.Values{}
		for _, in := range fieldRE.FindAllStringSubmatch(f[2], -1) {
			form.Set(in[1], html.UnescapeString(in[2]))
		}
		if line != nil {
			form.Set(line[1], html.UnescapeString(line[2])+typed)
		}
		req, _ := http.NewRequest("POST", at+f[1], strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", at)
		req.Header.Set("Referer", at+path)
		resp, err := s.c.Do(req)
		if err != nil {
			s.t.Fatal(err)
		}
		defer resp.Body.Close()
		after, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(after)
	}
	s.t.Fatalf("%s shows no button %q under %q:\n%s", path, label, id, part)
	return 0, ""
}

// came is what a run came to, in the words the two ways of doing a thing
// must agree on: every task, attempt and question, and whether all is stopped.
func came(p *scenario.Project) (lines []string) {
	s, _ := p.Record()
	for _, id := range slices.Sorted(maps.Keys(s.Tasks)) {
		lines = append(lines, "task "+id+" "+string(s.Tasks[id].Status))
	}
	for _, id := range slices.Sorted(maps.Keys(s.Attempts)) {
		lines = append(lines, "attempt "+id+" "+string(s.Attempts[id].State))
	}
	for _, id := range slices.Sorted(maps.Keys(s.Questions)) {
		q, by := s.Questions[id], ""
		if q.AnsweredBy != nil {
			by = string(q.AnsweredBy.Caller) + " " + q.AnsweredBy.Where
		}
		lines = append(lines, fmt.Sprintf("question %s %s %q by %s", id, q.State, q.Answer, by))
	}
	return append(lines, fmt.Sprint("stopped ", s.Run.Paused != nil))
}

// The evening on the real page's server (8k, 9.9), on the double and on the
// real keeper: every screen is the real one, a tab held open is told of a
// change, and two questions, a sign-off, one task stopped, Stop all and
// Resume, each pressed as the page's own form, leave the record exactly as
// the same commands run with the page's mark leave a second project.
func TestTheEveningThroughThePage(t *testing.T) {
	for _, on := range []scenario.Backend{scenario.Double, scenario.Keeper} {
		t.Run(string(on), func(t *testing.T) {
			f, err := testkit.Load(testkit.Evening)
			if err != nil {
				t.Fatal(err)
			}
			p := scenario.PrepareOn(t, on, f, nil)
			s := serve(t, p)
			for path, name := range map[string]string{contract.RouteTeam: contract.ScreenTeam, contract.RoutePlan: contract.ScreenPlan,
				contract.RouteMessages: contract.ScreenMessages, contract.RouteJobs: contract.ScreenJobs,
				contract.RouteTask + "T7": contract.ScreenTask, contract.RouteLead: contract.ScreenLead} {
				if code, body := s.get(path); code != 200 || !screen(body, name) || strings.Contains(body, `class="said"`) {
					t.Errorf("%s: %d, not the %s screen without a complaint:\n%s", path, code, name, body)
				}
			}
			if code, body := s.get("/nowhere"); code != 404 || !screen(body, contract.ScreenRefused) {
				t.Errorf("a screen the page has not: %d\n%s", code, body)
			}

			// A tab's held-open connection says a line when something changed.
			live, err := s.c.Get(at + contract.RouteLive)
			if err != nil {
				t.Fatal(err)
			}
			defer live.Body.Close()
			told := make(chan string, 64)
			go func() {
				for sc := bufio.NewScanner(live.Body); sc.Scan(); {
					told <- sc.Text()
				}
			}()
			if first := <-told; first != ": live" {
				t.Fatalf("the held-open connection began with %q", first)
			}

			steps := []struct {
				path, id, label, typed string
				args                   []string
			}{
				{contract.RouteTeam, "i-q7", "Yes", "", []string{"answer", "q7", "--file", "yes", "--human"}},
				{contract.RouteTeam, "i-q9", "Send", "two sizes", []string{"answer", "q9", "--file", "two sizes", "--human"}},
				{contract.RouteTeam, "i-n4", "Approve", "", []string{"answer", "n4", "--file", "approve", "--human"}},
				{contract.RouteTask + "T1", "", "Stop", "", []string{"stop", "T1", "--human"}},
				{contract.RouteTeam, "top", "Stop all", "", []string{"pause", "--everywhere", "--human"}},
				{contract.RouteTeam, "top", "Resume", "", []string{"resume", "--everywhere", "--human"}},
			}
			twin := scenario.PrepareOn(t, on, f, nil)
			for i, st := range steps {
				if st.id == "top" {
					st.id = ""
				}
				if code, body := s.press(st.path, st.id, st.label, st.typed); code != 200 || strings.Contains(body, `class="said"`) || screen(body, contract.ScreenRefused) {
					t.Fatalf("%s pressed on %s: %d\n%s", st.label, st.path, code, body)
				}
				// The words of an answer ride in a file, as the page hands them over.
				if at := slices.Index(st.args, "--file"); at >= 0 {
					file := filepath.Join(t.TempDir(), "text")
					if err := os.WriteFile(file, []byte(st.args[at+1]), 0o600); err != nil {
						t.Fatal(err)
					}
					st.args[at+1] = file
				}
				if out, err := twin.Command(scenario.Page, st.args...).CombinedOutput(); err != nil {
					t.Fatalf("%v: %v\n%s", st.args, err, out)
				}
				if got, want := came(p), came(twin); !slices.Equal(got, want) {
					t.Fatalf("after %q the page left\n%s\nand the command\n%s", st.label, strings.Join(got, "\n"), strings.Join(want, "\n"))
				}
				stopped := came(p)[len(came(p))-1]
				if want := fmt.Sprint("stopped ", i == 4); stopped != want {
					t.Errorf("after %q: %s", st.label, stopped)
				}
				if i == 0 {
					select {
					case line := <-told:
						if line != "data: changed" && line != "" {
							t.Errorf("the open tab was told %q", line)
						}
					case <-time.After(20 * time.Second):
						t.Error("the open tab was never told of the answer")
					}
				}
			}
			s2, _ := p.Record()
			if q := s2.Questions["q7"]; q.Answer != "yes" || q.AnsweredBy == nil || q.AnsweredBy.Where != contract.WherePage {
				t.Errorf("q7 was answered %+v", q)
			}
			if s2.Attempts["T1.1"].State.Live() {
				t.Errorf("T1.1 is still %s after Stop", s2.Attempts["T1.1"].State)
			}
		})
	}
}
