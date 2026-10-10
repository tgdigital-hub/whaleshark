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

// The one command that takes a screenshot, as it must be spelled for each
// shell of Windows to save the picture: the browser by its usual folder,
// since it is on nobody's PATH; every variable in the shell's own spelling;
// and, where the shell does not wait for a program with a window of its
// own, a way to make it wait, so that the picture is there before the
// worker reports. The words in braces are the variables' names.
var browserLines = map[string]string{
	"powershell": `& "$env:ProgramFiles\Google\Chrome\Application\chrome.exe" --headless --user-data-dir="$env:TEMP\whaleshark-browser-$env:{run}-$env:{attempt}" --screenshot="$env:{evidence}\page.png" --window-size=1280,900 "http://{loopback}:$env:PORT/" | Out-Null`,
	"cmd":        `start "" /wait "%ProgramFiles%\Google\Chrome\Application\chrome.exe" --headless --user-data-dir="%TEMP%\whaleshark-browser-%{run}%-%{attempt}%" --screenshot="%{evidence}%\page.png" --window-size=1280,900 "http://{loopback}:%PORT%/"`,
	"git-bash":   `"$PROGRAMFILES/Google/Chrome/Application/chrome.exe" --headless --user-data-dir="$TEMP/whaleshark-browser-${run}-${attempt}" --screenshot="${evidence}/page.png" --window-size=1280,900 "http://{loopback}:$PORT/"`,
}

// TestTheBrowserLine runs the prompt's one command for a worker with no
// browser tool, in every shell a worker can have on Windows: first as the
// prompt writes it there, which saves no picture in any of them (the
// browser is on no PATH, and the variables are spelled for a POSIX shell),
// then as browserLines spells it, which must have saved a picture of the
// worker's site by the time the line ends.
func TestTheBrowserLine(t *testing.T) {
	onlyWindows(t)
	chrome := filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe")
	if _, err := os.Stat(chrome); err != nil {
		missing(t, "Chrome at "+chrome)
	}
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

	var written string
	for _, l := range strings.Split(guide.Browser(true, p.Kit.Platform.System(), false), "\n") {
		if strings.Contains(l, "--headless") {
			written = strings.TrimSpace(l)
		}
	}
	if written == "" {
		t.Fatal("the prompt's browser section has no command")
	}
	t.Logf("FACT the prompt's line on Windows: %s", written)
	names := strings.NewReplacer("{run}", contract.EnvRun, "{attempt}", contract.EnvAttempt, "{evidence}", contract.EnvEvidence, "{loopback}", contract.Loopback)
	for _, sh := range shells(p.Kit) {
		must := browserLines[sh.name]
		if must == "" {
			must = browserLines["powershell"] // the newer PowerShell reads the older one's line
		}
		for i, line := range []string{written, names.Replace(must)} {
			// Not the test's own temporary folder: a browser that is still
			// closing would keep the test from clearing it away.
			evidence, err := os.MkdirTemp("", "evidence")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(evidence) })
			saved := func() bool {
				data, _ := os.ReadFile(filepath.Join(evidence, "page.png"))
				return bytes.HasPrefix(data, []byte("\x89PNG"))
			}
			out, err := typed(p, sh, line, contract.EnvEvidence+"="+evidence, "PORT="+port)
			atOnce := saved()
			inTheEnd := soon(10*time.Second, saved)
			t.Logf("FACT the browser line in %s, %s: a picture when the line ended=%v, a picture in the end=%v",
				sh.name, []string{"as the prompt writes it", "as it must be"}[i], atOnce, inTheEnd)
			if (i == 1) != atOnce || (i == 1) != inTheEnd {
				t.Errorf("%s, %s: a picture when the line ended=%v, in the end=%v\nline: %s\n%v %s",
					sh.name, []string{"as the prompt writes it, which should save none", "as it must be, which should save one before it ends"}[i], atOnce, inTheEnd, line, err, out)
			}
		}
	}
}
