package dash

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io/fs"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// The tests of this file run the real program: `dash start` as a process of
// its own in a pretended login, reached over its socket file.

// short moves the login's state folder to a short path: a socket file's
// name has little room, and a test's own folder has a long name.
func short(t *testing.T, p *scenario.Project) contract.Dirs {
	t.Helper()
	dirs, err := p.Kit.Platform.Dirs()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "ws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := os.MkdirAll(dirs.State, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dirs.State, filepath.Join(dir, filepath.Base(dirs.State))); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("LOCALAPPDATA", dir)
	dirs, _ = p.Kit.Platform.Dirs()
	return dirs
}

// answer runs a command of the login's own and reads what it answers.
func answer(t *testing.T, p *scenario.Project, v any, args ...string) {
	t.Helper()
	out, err := p.Command(scenario.Unbound, append(args, "--json")...).Output()
	env := contract.Envelope{Result: v}
	if json.Unmarshal(out, &env) != nil || !env.OK {
		t.Fatalf("%v: %v %s", args, err, out)
	}
}

// begin starts the server as a login's service entry does, from a pane's
// own surroundings, and waits until it answers.
func begin(t *testing.T, p *scenario.Project) *exec.Cmd {
	t.Helper()
	cmd := p.Command(scenario.Unbound, "dash", "start")
	var said bytes.Buffer
	cmd.Stderr = &said
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	for end := time.Now().Add(startWait); ; time.Sleep(startStep) {
		var st status
		if answer(t, p, &st, "dash", "status"); st.Running {
			return cmd
		}
		if time.Now().After(end) {
			t.Fatalf("the server did not start: %s", &said)
		}
	}
}

// over is a browser that reaches the page through its socket file.
func over(sock string) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: time.Minute, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return new(net.Dialer).DialContext(ctx, "unix", sock)
	}}}
}

// get asks for one address and returns the answer's code and text.
func get(t *testing.T, c *http.Client, address string) string {
	t.Helper()
	req, _ := http.NewRequest("GET", address, nil)
	return send(t, c, req)
}

func send(t *testing.T, c *http.Client, req *http.Request) string {
	t.Helper()
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	b.ReadFrom(resp.Body)
	return strconv.Itoa(resp.StatusCode) + " " + b.String()
}

// until waits for what a moment of the server's own must bring.
func until(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(3 * sweepEvery); !ok(); time.Sleep(startStep) {
		if time.Now().After(end) {
			t.Fatalf("it never came: %s", what)
		}
	}
}

func TestThePageFromEndToEnd(t *testing.T) {
	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	p := scenario.Prepare(t, f, nil)
	dirs := short(t, p)
	cmd := begin(t, p)

	// The socket is the login's alone, and so is the key.
	if p.Kit.Platform.System() != "windows" {
		for path, mode := range map[string]fs.FileMode{filepath.Join(dirs.State, sockFile): 0o600, filepath.Join(dirs.Config, keyFile): 0o600, dirs.State: 0o700} {
			if info, err := os.Stat(path); err != nil || info.Mode().Perm() != mode {
				t.Errorf("%s: %v, %v", filepath.Base(path), info.Mode().Perm(), err)
			}
		}
	}
	var u contract.DashURL
	answer(t, p, &u, "dash", "url")
	if u.Socket != filepath.Join(dirs.State, sockFile) || u.Code == "" || u.Version != contract.Version {
		t.Fatalf("dash url: %+v", u)
	}
	page := "http://" + contract.PageHosts[0] + ":" + strconv.Itoa(contract.PagePort)
	browser := over(u.Socket)
	in := page + contract.RouteIn + "?" + contract.FieldCode + "=" + u.Code
	if got := get(t, browser, in); !strings.HasPrefix(got, "200 ") || !strings.Contains(got, contract.ScreenTeam) {
		t.Fatalf("signing in: %s", got)
	}
	if got := get(t, over(u.Socket), in); !strings.HasPrefix(got, "403 ") {
		t.Errorf("the code a second time: %s", got)
	}
	for path, screen := range map[string]string{contract.RoutePlan: contract.ScreenPlan, contract.RouteMessages: contract.ScreenMessages,
		contract.RouteJobs: contract.ScreenJobs, contract.RouteTask + "T8": contract.ScreenTask, contract.RouteLead: contract.ScreenLead} {
		if got := get(t, browser, page+path); !strings.HasPrefix(got, "200 ") || !strings.Contains(got, screen+":") {
			t.Errorf("%s: %s", path, got)
		}
	}
	// connect asks for the running sites with no session.
	if got := get(t, over(u.Socket), page+contract.RouteSites); strings.TrimSpace(got) != "200 []" {
		t.Errorf("the sites: %s", got)
	}
	// show ran as the person: the task is seen.
	if s, _ := p.Record(); s.Tasks["T8"].SeenAt.IsZero() {
		t.Error("the task screen did not mark the task as seen")
	}

	// A held-open tab is told of a change made through the tool, and the
	// server looks only while one is held.
	var st status
	if answer(t, p, &st, "dash", "status"); st.Tabs != 0 || st.Watching {
		t.Errorf("before a tab is held: %+v", st)
	}
	ctx, hide := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", page+contract.RouteLive, nil)
	tab, err := browser.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 16)
	go func() {
		for sc := bufio.NewScanner(tab.Body); sc.Scan(); {
			lines <- sc.Text()
		}
		close(lines)
	}()
	if first := <-lines; first != ": live" || tab.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("the held-open answer began %q (%s)", first, tab.Header.Get("Content-Type"))
	}
	until(t, "the server watching", func() bool { answer(t, p, &st, "dash", "status"); return st.Tabs == 1 && st.Watching })
	time.Sleep(4 * startStep) // the watch is set a moment after the tab is counted
	s, _ := p.Record()
	if out, err := p.Command(s.Tasks["T3"].Attempts[0], "progress", "77", "nearly").CombinedOutput(); err != nil {
		t.Fatalf("progress: %v %s", err, out)
	}
	told := time.After(2 * time.Second)
	for got := false; !got; {
		select {
		case line := <-lines:
			got = line == "data: changed"
		case <-told:
			t.Fatal("a change made through the tool was not told to the held-open tab within two seconds")
		}
	}

	// A button, through the server as started in a pane: the person's, from the page.
	key, _ := readKey(p.Kit.Platform, dirs, false, false)
	site, _ := url.Parse(page)
	id, _, _ := strings.Cut(browser.Jar.Cookies(site)[0].Value, ".")
	post, _ := http.NewRequest("POST", page+contract.RouteDo+"answer", strings.NewReader(url.Values{
		contract.FieldToken: {mac(key, "token", id)}, "id": {"q9"}, "text": {"three sizes"}}.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Origin", page)
	if got := send(t, browser, post); !strings.HasPrefix(got, "200 ") {
		t.Errorf("the answer: %s", got)
	}
	s, _ = p.Record()
	if q := s.Questions["q9"]; q.Answer != "three sizes" || q.AnsweredBy == nil || q.AnsweredBy.Where != contract.WherePage {
		t.Errorf("the answer as recorded: %q by %+v", q.Answer, q.AnsweredBy)
	}

	// A hidden tab lets go, and the server stops looking.
	hide()
	until(t, "the server letting go", func() bool { answer(t, p, &st, "dash", "status"); return st.Tabs == 0 && !st.Watching })

	// Memory when idle, as the server counts it and as the system does.
	rss := "not asked"
	if out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(cmd.Process.Pid)).Output(); err == nil {
		rss = strings.TrimSpace(string(out)) + " KB"
	}
	t.Logf("memory when idle: %.1f MB held of the system's by the server's own count; resident by ps: %s", float64(st.Memory)/(1<<20), rss)
	if st.Memory > 30<<20 {
		t.Errorf("the idle server holds %d bytes", st.Memory)
	}

	// Stop, with --forget: the server ends by itself and the browser is signed out.
	if out, err := p.Command(scenario.Unbound, "dash", "stop", "--forget").CombinedOutput(); err != nil || !strings.Contains(string(out), "signed out") {
		t.Fatalf("dash stop --forget: %v %s", err, out)
	}
	if err := cmd.Wait(); err != nil {
		t.Errorf("the stopped server ended with %v", err)
	}
	if _, err := os.Stat(u.Socket); err == nil {
		t.Error("the stopped server left its socket file")
	}
	if answer(t, p, &st, "dash", "status"); st.Running {
		t.Error("dash status after stop says it runs")
	}
	// dash url starts the server where none runs, apart from its caller.
	t.Cleanup(func() { p.Command(scenario.Unbound, "dash", "stop").Run() })
	p.Clock(time.Second) // a code's number is its moment, and the test's clock stands still
	var second contract.DashURL
	if answer(t, p, &second, "dash", "url"); second.Code == "" || second.Code == u.Code {
		t.Fatalf("dash url with no server running: %+v", second)
	}
	if got := get(t, browser, page+contract.RouteTeam); !strings.HasPrefix(got, "403 ") {
		t.Errorf("the old session after --forget: %s", got)
	}
	if got := get(t, browser, page+contract.RouteIn+"?"+contract.FieldCode+"="+second.Code); !strings.HasPrefix(got, "200 ") {
		t.Errorf("signing in again: %s", got)
	}
	if out, err := p.Command(scenario.Unbound, "dash", "stop").CombinedOutput(); err != nil {
		t.Fatalf("dash stop: %v %s", err, out)
	}
	until(t, "the server started apart ending", func() bool { answer(t, p, &st, "dash", "status"); return !st.Running })
}

// With every pane closed and the lead agent not waiting, the page's server
// alone has the run swept: an agent at a prompt still raises its item.
func TestAnAgentAtAPromptStillRaisesItsItem(t *testing.T) {
	p := scenario.Run(t, filepath.Join("testdata", "at-a-prompt.scn"), nil)
	short(t, p)
	prompt := func() bool {
		s, _ := p.Record()
		for _, q := range s.Questions {
			if q.Cause == contract.CausePrompt && q.Task == "B" && q.For == contract.ForHuman {
				return true
			}
		}
		return false
	}
	time.Sleep(sweepEvery + time.Second)
	if prompt() {
		t.Fatal("the item was raised with nobody watching: the test proves nothing")
	}
	cmd := begin(t, p)
	until(t, "the item of the agent at a prompt", prompt)
	// Stopped, and its children with it, before the test's folder goes.
	if out, err := p.Command(scenario.Unbound, "dash", "stop").CombinedOutput(); err != nil {
		t.Errorf("dash stop: %v %s", err, out)
	}
	cmd.Wait()
}

// sums is every file of the login and of the project with what it holds,
// but the socket, which is no file to read, and each run's sweep stamp,
// which a sweep on its way writes whoever started it.
func sums(t *testing.T, roots ...string) map[string][sha256.Size]byte {
	t.Helper()
	out := map[string][sha256.Size]byte{}
	for _, root := range roots {
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if name := filepath.Base(path); err == nil && d.Type().IsRegular() && name != contract.SweepFile {
				data, _ := os.ReadFile(path)
				out[path] = sha256.Sum256(data)
			}
			return nil
		})
	}
	return out
}

// The server killed outright at a random moment of a run changes no file,
// leaves every command working and starts again over what it left.
func TestKilledAtRandomItChangesNoFile(t *testing.T) {
	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	p := scenario.Prepare(t, f, nil)
	dirs := short(t, p)
	// One round first, so that what a first sweep of the fixture writes is written.
	first := begin(t, p)
	time.Sleep(sweepEvery + time.Second)
	first.Process.Kill()
	first.Wait()
	for round := range 3 {
		cmd := begin(t, p)
		time.Sleep(rand.N(sweepEvery + 2*time.Second))
		before := sums(t, p.Root, dirs.State, dirs.Config)
		cmd.Process.Kill()
		cmd.Wait()
		time.Sleep(time.Second)
		after := sums(t, p.Root, dirs.State, dirs.Config)
		for path, sum := range after {
			if before[path] != sum {
				t.Errorf("round %d: %s changed when the server was killed", round, path)
			}
		}
		if len(after) != len(before) {
			t.Errorf("round %d: %d files before the kill, %d after", round, len(before), len(after))
		}
		if out, err := p.Command(scenario.Orch, "status").CombinedOutput(); err != nil {
			t.Errorf("round %d: status after the kill: %v %s", round, err, out)
		}
	}
}
