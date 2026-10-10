package linux

import (
	"context"
	"fmt"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// started is a browser check a worker has just begun, on a server
// (testdata/browser.scn), with its result written: the project, the attempt
// and the attempt's folder. Only a login in a container comes here.
func started(t *testing.T) (*scenario.Project, *contract.Attempt, string) {
	t.Helper()
	if inside == "" {
		t.Skip("a check of a login in a container: TestContainers runs it there")
	}
	p := scenario.Run(t, "testdata/browser.scn", nil)
	s, run := p.Record()
	dir := contract.AttemptDir(run, "A.1")
	if err := os.WriteFile(filepath.Join(dir, "result.md"), []byte("The page is built.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p, s.Attempts["A.1"], dir
}

func report(p *scenario.Project) (string, error) {
	out, err := p.Command("A.1", "report", "done", "the page shows its heading").CombinedOutput()
	return string(out), err
}

// A picture counts from the second its attempt began. So this system's
// files must keep a time at least to the second and never earlier than it
// was: a picture of the instant of the start is taken, one of the instant
// before that second is not.
func TestAPictureOfTheSecondTheAttemptBeganCounts(t *testing.T) {
	p, a, dir := started(t)
	shot, began := filepath.Join(dir, "evidence", "page.png"), a.StartedAt
	save := func(at time.Time) {
		t.Helper()
		if err := os.WriteFile(shot, []byte("\x89PNG\r\n\x1a\n a picture"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(shot, at, at); err != nil {
			t.Fatal(err)
		}
	}
	save(began.Truncate(time.Second).Add(-time.Nanosecond))
	if out, err := report(p); err == nil || !strings.Contains(out, "screenshot") {
		t.Fatalf("a picture from before the attempt began was taken (%v):\n%s", err, out)
	}
	save(began)
	if out, err := report(p); err != nil {
		t.Fatalf("a picture of the instant the attempt began was not taken (%v):\n%s", err, out)
	}
	s, _ := p.Record()
	got := s.Attempts["A.1"].Report.Evidence
	if len(got) != 1 || got[0].Kind != "png" || got[0].At.Before(began.Truncate(time.Second)) {
		t.Fatalf("the report lists %+v for an attempt begun at %s", got, began)
	}
	t.Logf("begun at %s; the picture's time was kept as %s", began.Format(time.RFC3339Nano), got[0].At.Format(time.RFC3339Nano))
}

// The one command the prompt of a server's worker names for a screenshot
// with no browser tool, run as written, by a work login, with the browser
// server setup installed: it saves a picture, in a profile folder of the
// attempt's own, and the report takes it.
func TestThePromptsOwnCommandSavesAPictureTheReportTakes(t *testing.T) {
	if inside != "server" {
		t.Skip("needs the browser that server setup installs: the containers set up as servers run it")
	}
	p, _, dir := started(t)
	prompt, err := os.ReadFile(filepath.Join(dir, "prompt.md"))
	if err != nil {
		t.Fatal(err)
	}
	var line string
	for l := range strings.Lines(string(prompt)) {
		if strings.Contains(l, " --headless --") {
			line = strings.TrimSpace(l)
		}
	}
	if !strings.HasPrefix(line, `"$`+contract.EnvBrowsers+`"/chromium-*/chrome-linux/chrome --headless`) {
		t.Fatalf("the prompt's command for a server is %q", line)
	}
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "<h1>a page to look at</h1>")
	}))
	defer site.Close()
	_, port, _ := net.SplitHostPort(site.Listener.Addr().String())
	tmp, err := p.Kit.Platform.PrivateTemp()
	if err != nil {
		t.Fatal(err)
	}
	shot := filepath.Join(dir, "evidence", "page.png")
	// The tab's variables as start gives them on a server, and the port the
	// worker's site answers on.
	take := func(line string) string {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sh", "-c", line)
		cmd.Env = append(p.Command("A.1").Env, "PORT="+port, contract.EnvEvidence+"="+filepath.Dir(shot),
			contract.EnvBrowsers+"="+contract.ServerBrowsers, "TMPDIR="+tmp)
		out, err := cmd.CombinedOutput()
		return fmt.Sprintf("%v\n%s", err, out)
	}
	said := take(line)
	if _, err := os.Stat(shot); err != nil && strings.Contains(said, "sandbox") {
		t.Logf("NO SANDBOX: this container does not let the browser make its sandbox, so the picture is taken without one, which the prompt never does. As written: %s", said)
		said = take(strings.Replace(line, " --headless ", " --headless --no-sandbox ", 1))
	}
	file, err := os.Open(shot)
	if err != nil {
		browsers, _ := filepath.Glob(contract.ServerBrowsers + "/*/*")
		t.Fatalf("the prompt's command saved no picture: %s\nthe shared browser holds %v", said, browsers)
	}
	defer file.Close()
	if size, err := png.DecodeConfig(file); err != nil || size.Width != 1280 || size.Height != 900 {
		t.Errorf("the picture is %dx%d (%v), not the 1280x900 the command asks for", size.Width, size.Height, err)
	}
	if profile, _ := filepath.Glob(filepath.Join(tmp, "whaleshark-browser-*")); len(profile) != 1 {
		t.Errorf("the browser's profile is not the attempt's own folder under %s: %v", tmp, profile)
	}
	if out, err := report(p); err != nil {
		t.Fatalf("the report did not take the picture (%v):\n%s", err, out)
	}
	show, _ := p.Command(scenario.Orch, "show", "A").CombinedOutput()
	if !strings.Contains(string(show), "1 screenshot") || !strings.Contains(string(show), "page.png") {
		t.Errorf("show does not list the picture:\n%s", show)
	}
}
