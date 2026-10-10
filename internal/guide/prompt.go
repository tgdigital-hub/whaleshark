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
