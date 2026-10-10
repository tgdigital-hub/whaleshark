package connect

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/pick"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
)

// safe is a place a goroutine writes and the test reads.
type safe struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safe) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safe) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// onMac is the real platform, saying it is a Mac wherever the test runs.
type onMac struct{ contract.Platform }

func (onMac) System() string { return "darwin" }

// login is a pretended login in a short folder of its own (a socket file's
// path has to be short), and a call of connect in it.
func login(t *testing.T) (dir string, c *contract.Call, out *safe) {
	t.Helper()
	dir, err := os.MkdirTemp("", "c")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	for _, v := range []string{"HOME", "USERPROFILE", "XDG_STATE_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(v, dir)
	}
	k := contract.NewKit()
	platform.Plug(k)
	k.Platform = onMac{k.Platform}
	out = &safe{}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("what connect said:\n%s", out)
		}
	})
	return dir, &contract.Call{Kit: k, Flags: map[string][]string{}, Stdin: strings.NewReader(""), Out: out, Err: out}, out
}

func clip(text string) string {
	return osc52 + "c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
}

func TestCopiedTextIsLiftedOutOfTheStream(t *testing.T) {
	long := strings.Repeat("a line of text\n", 500)
	stream := "one\x1b[1mtwo" + clip("first") + "three\x1b]0;a title\a" + osc52 + "c;" + base64.StdEncoding.EncodeToString([]byte(long)) + "\x1b\\four\x1b"
	want := "one\x1b[1mtwothree\x1b]0;a title\afour"
	// Cut into pieces of every size, a sequence is found across their edges.
	for size := 1; size <= len(stream); size += 1 + size/7 {
		var out bytes.Buffer
		var got []string
		l := &lift{out: &out, copy: func(text string) []byte { got = append(got, text); return nil }}
		for rest := []byte(stream); len(rest) > 0; {
			n := min(size, len(rest))
			l.Write(rest[:n])
			rest = rest[n:]
		}
		if out.String() != want || len(got) != 2 || got[0] != "first" || got[1] != long {
			t.Fatalf("in pieces of %d: the terminal got %q, the clipboard %d texts", size, out.String(), len(got))
		}
		if string(l.held) != "\x1b" {
			t.Fatalf("in pieces of %d: %q is held back, want the last byte alone", size, l.held)
		}
	}
	// What copy hands back goes to the terminal in the sequence's place, and
	// a sequence that never ends is passed on once it is longer than any text.
	var out bytes.Buffer
	l := &lift{out: &out, copy: func(string) []byte { return []byte("<asked>") }}
	l.Write([]byte("a" + clip("x") + "b" + osc52 + "c;"))
	l.Write(bytes.Repeat([]byte("A"), liftMax))
	if got := out.String(); !strings.HasPrefix(got, "a<asked>b"+osc52+"c;AAAA") || len(got) < liftMax {
		t.Fatalf("the terminal got %.40q and %d bytes", got, len(got))
	}
}

func TestAtLoginWritesTheItemAndTheLinesOnAYesAndTakesBothAway(t *testing.T) {
	home, c, out := login(t)
	item := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	settings := filepath.Join(home, ".ssh", "config")
	mine := "Host old\n\tUser somebody\n"
	os.MkdirAll(filepath.Dir(settings), 0o700)
	os.WriteFile(settings, []byte(mine), 0o600)

	if err := atLogin(c, "a<server>", 7781); err != nil {
		t.Fatal(err)
	}
	text, _ := os.ReadFile(item)
	for _, want := range []string{"<string>connect</string>", "<string>a&lt;server&gt;</string>", "<string>7781</string>", "RunAtLoad"} {
		if !strings.Contains(string(text), want) {
			t.Errorf("the login item lacks %s:\n%s", want, text)
		}
	}
	if got, _ := os.ReadFile(settings); string(got) != mine {
		t.Fatalf("with no yes the ssh settings changed:\n%s", got)
	}
	if !strings.Contains(out.String(), "AddKeysToAgent yes") || !strings.Contains(out.String(), "UseKeychain yes") {
		t.Errorf("the two lines were not shown:\n%s", out)
	}

	c.Stdin = strings.NewReader("y\n")
	if err := atLogin(c, "server", contract.PagePort); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(settings); string(got) != mine+keychain {
		t.Fatalf("after a yes the ssh settings are:\n%s", got)
	}
	// A second time finds the lines and asks nothing.
	c.Stdin = strings.NewReader("")
	asked := strings.Count(out.String(), "[y/N]")
	if err := atLogin(c, "server", contract.PagePort); err != nil || strings.Count(out.String(), "[y/N]") != asked {
		t.Fatalf("asked again, or: %v", err)
	}

	if err := notAtLogin(c); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(item); err == nil {
		t.Error("the login item is still there")
	}
	if got, _ := os.ReadFile(settings); string(got) != mine {
		t.Fatalf("after --not-at-login the ssh settings are:\n%s", got)
	}
	if err := notAtLogin(c); err != nil {
		t.Fatalf("taking away what is not there: %v", err)
	}

	c.Kit.Platform = c.Kit.Platform.(onMac).Platform
	if c.Kit.Platform.System() != "darwin" && atLogin(c, "server", contract.PagePort) == nil {
		t.Error("a login item was written on a system that is no Mac")
	}
}

func TestWhatConnectIsNotGiven(t *testing.T) {
	_, c, _ := login(t)
	for _, tc := range []struct {
		args  []string
		flags map[string][]string
	}{
		{nil, nil}, {[]string{"a", "b"}, nil}, {[]string{"-oProxyCommand=x"}, nil},
		{[]string{"a"}, map[string][]string{"terminals": {""}, "at-login": {""}}},
		{[]string{"a"}, map[string][]string{"port": {"80"}}},
	} {
		c.Args, c.Flags = tc.args, tc.flags
		if _, err := run(c); err == nil || err.(*contract.Refusal).Exit != contract.ExitUsage {
			t.Errorf("%v %v: %v", tc.args, tc.flags, err)
		}
	}
}

// box is this machine's own ssh server program, started by the test as the
// login itself on a loopback port, with a host key, a login key and both
// configurations of the test's own: nothing of the person's is read. The
// ssh connect runs is pointed at it under the name "box", and the program
// connect runs on the server is the script the test wrote.
func box(t *testing.T, dir, script string) {
	t.Helper()
	sshd, err := exec.LookPath("sshd")
	if err != nil {
		sshd = "/usr/sbin/sshd"
	}
	if _, err := os.Stat(sshd); err != nil || os.PathSeparator != '/' {
		t.Skip("no ssh server program on this machine: connect is not tried against one here")
	}
	path := func(name string) string { return filepath.Join(dir, name) }
	for _, key := range []string{"host", "id"} {
		if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", path(key)).CombinedOutput(); err != nil {
			t.Skipf("no key could be made: %v: %s", err, out)
		}
	}
	port := strconv.Itoa(freePort(t, contract.Loopback))
	write := func(name, text string, mode os.FileMode) {
		if err := os.WriteFile(path(name), []byte(text), mode); err != nil {
			t.Fatal(err)
		}
	}
	pub, _ := os.ReadFile(path("id.pub"))
	write("auth", string(pub), 0o600)
	write("sshd.conf", "Port "+port+"\nListenAddress "+contract.Loopback+"\nHostKey "+path("host")+"\nAuthorizedKeysFile "+path("auth")+
		"\nPidFile "+path("pid")+"\nUsePAM no\nStrictModes no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\n", 0o600)
	write("ssh.conf", "Host box\n\tHostName "+contract.Loopback+"\n\tPort "+port+"\n\tIdentityFile "+path("id")+
		"\n\tIdentitiesOnly yes\n\tIdentityAgent none\n\tUserKnownHostsFile "+path("known")+"\n\tStrictHostKeyChecking accept-new\n\tLogLevel ERROR\n", 0o600)
	write("played", script, 0o700)
	server := exec.Command(sshd, "-D", "-e", "-f", path("sshd.conf"))
	if err := server.Start(); err != nil {
		t.Skipf("the ssh server program did not start: %v", err)
	}
	t.Cleanup(func() { server.Process.Kill(); server.Wait() })
	old, oldRemote := sshArgv, remote
	sshArgv, remote = []string{"ssh", "-F", path("ssh.conf")}, path("played")
	t.Cleanup(func() { sshArgv, remote = old, oldRemote })
	for end := time.Now().Add(5 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		out, err := ssh(context.Background(), "box", "echo", "in").CombinedOutput()
		if err == nil {
			return
		}
		if time.Now().After(end) {
			t.Skipf("the ssh server program lets this login in with no key of the test's: %v: %s", err, out)
		}
	}
}

func freePort(t *testing.T, host string) int {
	t.Helper()
	l, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Skipf("no port on %s: %v", host, err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// within waits until ok says yes, and reports how long that took.
func within(t *testing.T, limit time.Duration, what string, ok func() bool) time.Duration {
	t.Helper()
	began := time.Now()
	for !ok() {
		if time.Since(began) > limit {
			t.Fatalf("after %s: %s", limit, what)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return time.Since(began)
}

// get asks a local port once, under a host name.
func get(port int, host string) (string, error) {
	req, _ := http.NewRequest(http.MethodGet, "http://"+net.JoinHostPort(contract.Loopback, strconv.Itoa(port))+"/", nil)
	req.Host = host
	resp, err := (&http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}

func TestThePageAndTheSitesThroughOneConnection(t *testing.T) {
	dir, c, out := login(t)
	sock := filepath.Join(dir, "dash.sock")
	urls := filepath.Join(dir, "urls")
	// The server's side of `dash url --json`: it counts how often it was asked.
	reply, _ := json.Marshal(contract.Envelope{OK: true, Result: contract.DashURL{Socket: sock, Code: "c0de", Version: "test"}})
	box(t, dir, "#!/bin/sh\necho asked >> "+urls+"\ncat <<'END'\n"+string(reply)+"\nEND\n")
	asked := func() int {
		data, _ := os.ReadFile(urls)
		return strings.Count(string(data), "asked")
	}

	// The page on its socket file, and two sites on the server's side: one
	// that becomes a live task's slot, and one that never is.
	var mu sync.Mutex
	var sites []int
	var stall atomic.Bool
	pagePort := freePort(t, contract.Loopback)
	host := net.JoinHostPort(contract.PageHosts[0], strconv.Itoa(pagePort))
	mux := http.NewServeMux()
	mux.HandleFunc(contract.RouteSites, func(w http.ResponseWriter, r *http.Request) {
		if r.Host != host {
			http.Error(w, "another name", http.StatusForbidden)
			return
		}
		for stall.Load() && r.Context().Err() == nil {
			time.Sleep(20 * time.Millisecond)
		}
		mu.Lock()
		defer mu.Unlock()
		json.NewEncoder(w).Encode(sites)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "the page") })
	listen := func(network, addr string, h http.Handler) {
		l, err := net.Listen(network, addr)
		if err != nil {
			t.Skipf("%s: %v", addr, err)
		}
		srv := &http.Server{Handler: h}
		go srv.Serve(l)
		t.Cleanup(func() { srv.Close() })
	}
	listen("unix", sock, mux)
	// The server is this machine, where a site and its forwarded port
	// cannot both have the one loopback address: the sites stand on the
	// other one.
	siteHost = "::1"
	t.Cleanup(func() { siteHost = contract.Loopback })
	slot, other := freePort(t, siteHost), freePort(t, siteHost)
	site := func(port int, says string) {
		listen("tcp", net.JoinHostPort(siteHost, strconv.Itoa(port)), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, says) }))
	}
	site(slot, "a task's site")
	site(other, "not a slot")
	is := func(port int, want string) func() bool {
		return func() bool { got, err := get(port, host); return err == nil && got == want }
	}
	closed := func(port int) bool { _, err := get(port, host); return err != nil }

	var opened safe
	oldBrowse, oldLadder, oldTick := browse, ladder, tick
	browse = func(system, link string) error { fmt.Fprintln(&opened, system, link); return nil }
	ladder = []time.Duration{100 * time.Millisecond}
	var ahead atomic.Int64
	now = func() time.Time { return time.Now().Round(0).Add(time.Duration(ahead.Load())) }
	t.Cleanup(func() {
		browse, ladder, tick = oldBrowse, oldLadder, oldTick
		now = func() time.Time { return time.Now().Round(0) }
	})
	connect := func() (stop func()) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			err := page(ctx, c, "box", pagePort)
			fmt.Fprintf(out, "connect ended: %v\n", err)
			done <- err
		}()
		return func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("connect ended with %v\n%s", err, out)
			}
		}
	}

	// With the real five seconds between questions.
	stop := connect()
	within(t, 15*time.Second, "the page is not reached", is(pagePort, "the page"))
	if want := "darwin http://" + host + contract.RouteIn + "?" + contract.FieldCode + "=c0de\n"; opened.String() != want {
		t.Fatalf("the browser was opened with %q, want %q", opened.String(), want)
	}
	if !closed(slot) || !closed(other) {
		t.Fatal("a port is forwarded that the page never named")
	}
	mu.Lock()
	sites = []int{slot}
	mu.Unlock()
	took := within(t, 10*time.Second, "the slot that started answering is not forwarded", is(slot, "a task's site"))
	t.Logf("the slot was forwarded after %s", took.Round(100*time.Millisecond))
	mu.Lock()
	sites = nil
	mu.Unlock()
	within(t, 10*time.Second, "the slot that left the answer is still forwarded", func() bool { return closed(slot) })
	if !closed(other) {
		t.Fatal("a port that is no slot was forwarded")
	}
	stop()
	if !closed(pagePort) {
		t.Fatal("the page's port is still forwarded after connect ended")
	}

	// The link killed, and restored without a keypress.
	tick = 200 * time.Millisecond
	mu.Lock()
	sites = []int{slot}
	mu.Unlock()
	stop = connect()
	within(t, 15*time.Second, "the slot is not forwarded", is(slot, "a task's site"))
	before := asked()
	master := masterOf(t, dir)
	master.Kill()
	within(t, 2*time.Second, "the killed link still carries the page", func() bool { return closed(pagePort) || asked() > before })
	within(t, 15*time.Second, "the link did not come back", func() bool { return asked() > before && is(pagePort, "the page")() && is(slot, "a task's site")() })
	if !strings.Contains(out.String(), "link down since") || !strings.Contains(out.String(), "should the page ask") {
		t.Errorf("the person was not told:\n%s", out)
	}
	if strings.Count(opened.String(), "\n") != 2 {
		t.Errorf("the browser is opened once by each run of connect, and never when a link comes back:\n%s", opened.String())
	}

	// A pretended sleep: the clock jumps and the page does not answer. The
	// link is tested at once and made anew at the first miss; without the
	// jump three misses of four seconds each would be waited for.
	before = asked()
	stall.Store(true)
	ahead.Store(int64(time.Hour))
	took = within(t, 6*time.Second, "after the clock jumped the link was not made anew", func() bool { return asked() > before })
	t.Logf("after a pretended sleep the link was made anew in %s", took.Round(100*time.Millisecond))
	stall.Store(false)
	within(t, 15*time.Second, "the page did not come back after the sleep", is(pagePort, "the page"))
	stop()
}

// masterOf finds the master connection connect holds, by what it says of itself.
func masterOf(t *testing.T, dir string) *os.Process {
	t.Helper()
	socks, _ := filepath.Glob(filepath.Join(dir, "whaleshark", "c-*"))
	for _, s := range socks {
		if strings.HasSuffix(s, ".lock") {
			continue
		}
		out, _ := ssh(context.Background(), "-S", s, "-O", "check", "box").CombinedOutput()
		if _, rest, ok := strings.Cut(string(out), "pid="); ok {
			pid, _ := strconv.Atoi(strings.TrimRight(strings.TrimSpace(rest), ")"))
			if p, err := os.FindProcess(pid); err == nil && pid > 1 {
				return p
			}
		}
	}
	t.Fatalf("no master connection found in %v", socks)
	return nil
}

func TestTheTerminalsWindowIsRunAgainAndCopiedTextReachesThisComputer(t *testing.T) {
	dir, c, out := login(t)
	runs := filepath.Join(dir, "runs")
	// The window on the server, played: the first one copies a text and is
	// cut off as a lost connection is, the second ends well.
	box(t, dir, "#!/bin/sh\necho \"$1 $WHALESHARK_SYSTEM\" >> "+runs+"\nif [ $(wc -l < "+runs+") -eq 1 ]; then printf 'before"+
		strings.ReplaceAll(clip("copied on the server"), "\x1b", `\033`)+"after'; exit 255; fi\nprintf 'second window'\n")
	var copied safe
	oldRun, oldLadder := pick.Run, ladder
	pick.Run = func(argv []string, in []byte) error { fmt.Fprintf(&copied, "%s<%s>", argv[0], in); return nil }
	ladder = []time.Duration{100 * time.Millisecond}
	t.Cleanup(func() { pick.Run, ladder = oldRun, oldLadder })

	if err := terminals(context.Background(), c, "box"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, _ := os.ReadFile(runs); string(got) != "open darwin\nopen darwin\n" {
		t.Errorf("the server ran %q, want the window twice with this computer's system named", got)
	}
	if copied.String() != "pbcopy<copied on the server>" {
		t.Errorf("this computer's clipboard got %q", copied.String())
	}
	got := out.String()
	if strings.Contains(got, osc52) || !strings.Contains(got, "beforeafter") || !strings.Contains(got, "second window") {
		t.Errorf("the terminal got %q", got)
	}
	if !strings.Contains(got, "link down since") {
		t.Errorf("the person was not told the link dropped: %q", got)
	}
}
