package guide

import (
	_ "embed"
	"fmt"
	"strings"
	"text/template"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

var (
	//go:embed prompt.tmpl
	promptText string
	//go:embed sync.tmpl
	syncText string
	//go:embed browser.tmpl
	browserText string
)

// Prompt is what a worker's prompt file says. Bin is the program's path as a
// shell takes it. Shared says that Dir is not a copy of the code of the
// task's own. Decisions, Sync and Browser are sections, left out while empty.
type Prompt struct {
	Bin, Run, Task, Name string
	Dir, Brief, Result   string
	Owns                 []string
	Shared               bool
	Decisions            []contract.Decision
	Sync, Browser        string
}

// Text renders the prompt file: the worker's whole protocol.
func (p Prompt) Text() string {
	return render(promptText, struct {
		Prompt
		Paused string
	}{p, contract.PausedReason})
}

// shots is the one command that takes a screenshot with no browser tool at
// all, for each system: Chromium's own switches, with the place the browser
// is usually found in. On a server it is the browser every login shares.
var shots = map[string]string{
	"darwin":  `"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"`,
	"linux":   `chromium`,
	"server":  `"$` + contract.EnvBrowsers + `"/chromium-*/chrome-linux/chrome`,
	"windows": `chrome.exe`,
}

// Browser is the prompt's section for a task whose result must be looked at
// in a browser (8l), empty for any other task: where the site runs, how it
// is checked, where the proof goes, and the command for a worker with no
// browser tool. system is the platform's name for this machine.
func Browser(check bool, system string, server bool) string {
	if !check {
		return ""
	}
	if server {
		system = "server"
	}
	return render(browserText, struct{ Loopback, Evidence, Shot string }{contract.Loopback, contract.EnvEvidence, shots[system]})
}

// Sync words a sync brief: the facts, then the rules for bringing a copy of
// the code up to date. A file's name and git's word come from the
// repository, so neither can begin a line of its own.
func Sync(b contract.SyncBrief) string { return render(syncText, b) }

func render(text string, data any) string {
	t := template.Must(template.New("").Funcs(template.FuncMap{
		"join": func(s []string) string { return strings.Join(s, ", ") },
		"line": func(s string) string { return strings.Join(strings.Fields(s), " ") },
		"names": func(s []string) string {
			return strings.Trim(fmt.Sprintf("%q", s), "[]")
		},
	}).Parse(text))
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		panic(err)
	}
	return b.String()
}
