package guide

import (
	_ "embed"
	"strings"
	"text/template"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

//go:embed prompt.tmpl
var promptText string

// Prompt is what a worker's prompt file says. Bin is the program's path as a
// shell takes it. Decisions, Sync and Browser are sections, left out while empty.
type Prompt struct {
	Bin, Run, Task, Name string
	Dir, Brief, Result   string
	Owns                 []string
	Shared               bool // other workers work in Dir too
	Decisions            []contract.Decision
	Sync, Browser        string
}

// Text renders the prompt file: the worker's whole protocol.
func (p Prompt) Text() string {
	t := template.Must(template.New("").Funcs(template.FuncMap{
		"join": func(s []string) string { return strings.Join(s, ", ") },
	}).Parse(promptText))
	var b strings.Builder
	err := t.Execute(&b, struct {
		Prompt
		Paused string
	}{p, contract.PausedReason})
	if err != nil {
		panic(err)
	}
	return b.String()
}
