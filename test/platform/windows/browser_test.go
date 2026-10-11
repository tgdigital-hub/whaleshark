package windows

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/guide"
)

// TestTheBrowserLine runs the prompt's one command for a worker with no
// browser tool, as the prompt writes it on Windows, in every shell a
// worker's tool can type into there (one line for both PowerShells and
// git's bash, one for cmd): the line must have saved a
// picture of the worker's site by the time it ends, in each of them, or a
// report typed next would be refused. Then again on a PC with no Chrome,
// where the line names Edge.
func TestTheBrowserLine(t *testing.T) {
	onlyWindows(t)
	p := evening(t)
	site, err := net.Listen("tcp", contract.Loopback+":0")
	if err != nil {
		t.Fatal(err)
	}
	defer site.Close()
	go http.Serve(site, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "<!doctype html><title>a worker's site</title><h1>a worker's site</h1>")
	}))
	_, port, _ := net.SplitHostPort(site.Addr().String())
	// A PC with no Chrome: its folders are looked for where there is none.
	noChrome := func(name string) string {
		switch name {
		case "LOCALAPPDATA":
			return ""
		case "ProgramFiles":
			name = "ProgramFiles(x86)"
		}
		return os.Getenv(name)
	}
	for _, pc := range []struct {
		browser string
		getenv  func(string) string
	}{{"chrome.exe", os.Getenv}, {"msedge.exe", noChrome}} {
		for _, sh := range shells(p.Kit) {
			// Not the test's own temporary folder: a browser that is still
			// closing would keep the test from clearing it away.
			evidence, err := os.MkdirTemp("", "evidence")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(evidence) })
			tab := []string{contract.EnvRun + "=r1", contract.EnvAttempt + "=" + worker + "-" + sh.name, contract.EnvEvidence + "=" + evidence, "PORT=" + port}
			// The section's first line with the browser in it is for every
			// shell but cmd, its second for cmd.
			line := ""
			for _, l := range strings.Split(guide.OnWindows(guide.Browser(true, "windows", false), p.Kit.Platform.Quote, tab, pc.getenv), "\n") {
				if strings.Contains(l, "--headless") && (line == "" || sh.name == "cmd") {
					line = strings.TrimSpace(l)
				}
			}
			if !strings.Contains(line, pc.browser) {
				missing(t, pc.browser+" in its usual folder (the prompt's line is: "+line+")")
			}
			saved := func() bool {
				data, _ := os.ReadFile(filepath.Join(evidence, "page.png"))
				return bytes.HasPrefix(data, []byte("\x89PNG"))
			}
			out, err := typed(p, sh, line)
			atOnce := saved()
			t.Logf("FACT the browser line in %s: a picture when the line ended=%v\n%s", sh.name, atOnce, line)
			if !atOnce {
				t.Errorf("%s: no picture when the line ended (one came later: %v)\nline: %s\n%v %s", sh.name, soon(10*time.Second, saved), line, err, out)
			}
		}
	}
}
