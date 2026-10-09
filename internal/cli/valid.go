package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

type rule struct {
	contract.ValueRule
	re *regexp.Regexp
}

// rules is the contract's table of value rules, by name.
var rules = sync.OnceValue(func() map[string]rule {
	m := make(map[string]rule, len(contract.ValueRules))
	for _, v := range contract.ValueRules {
		r := rule{ValueRule: v}
		if v.Pattern != "" {
			r.re = regexp.MustCompile(v.Pattern)
		}
		m[v.Value] = r
	}
	return m
})

// Valid is the one validator: every value a person, an agent or a file hands
// the tool passes it before anything is touched. kind is the name of a value
// rule, or "task" for what is a task's id or its name. root is the project
// folder a path must stay inside; with an empty root only the form is checked.
func Valid(kind, v, root string) error {
	if kind == "task" {
		if Valid("id", v, root) == nil {
			return nil
		}
		kind = "name"
	}
	r, ok := rules()[kind]
	why := ""
	switch {
	case !ok:
		return fmt.Errorf("no rule for a value of the kind %q", kind)
	case !utf8.ValidString(v):
		why = "is not text"
	case v == "" && kind != "text":
		why = "is empty"
	case r.Max > 0 && utf8.RuneCountInString(v) > r.Max:
		why = fmt.Sprintf("is longer than %d characters", r.Max)
	case r.Words > 0 && (len(strings.Fields(v)) == 0 || len(strings.Fields(v)) > r.Words):
		why = fmt.Sprintf("must be one to %d words", r.Words)
	case r.re != nil && !r.re.MatchString(v):
		why = "has a character or a form that is not allowed"
	case r.OneLine && strings.ContainsFunc(v, unsafe):
		why = "has a line break or a control character"
	case r.Inside:
		why = outside(root, v)
	}
	if why == "" {
		return nil
	}
	return fmt.Errorf("%s %.40q %s", kind, v, why)
}

// unsafe reports a character that could steer a terminal or break a line.
func unsafe(r rune) bool {
	return unicode.IsControl(r) || r == 0x2028 || r == 0x2029 || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069
}

// outside says why a path may not be used, or nothing when it is relative,
// never climbs, cannot be read as an option and, as far as it exists, still
// lies inside root once links are resolved.
func outside(root, p string) string {
	parts := strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' })
	switch {
	case filepath.IsAbs(p), p[0] == '/', p[0] == '\\', len(p) > 1 && p[1] == ':':
		return "must be relative to the project"
	case p[0] == '-':
		return "may not start with a dash"
	case len(parts) == 0:
		return "names nothing"
	}
	for _, part := range parts {
		if part == ".." {
			return `may not climb with ".."`
		}
	}
	if root == "" {
		return ""
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "cannot be checked: " + err.Error()
	}
	at := filepath.Join(base, p)
	for at != base {
		// #nosec G703 -- this walk is itself the check that the path stays inside the project
		if _, err := os.Lstat(at); err == nil {
			break
		}
		at = filepath.Dir(at)
	}
	if at, err = filepath.EvalSymlinks(at); err != nil {
		return "cannot be checked: " + err.Error()
	}
	if rel, err := filepath.Rel(base, at); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "leads out of the project"
	}
	return ""
}

// Plain takes out of a text everything that could recolour, retitle or
// rewrite a terminal: control characters but the line break and the tab, and
// the marks that turn the reading direction.
func Plain(s string) string {
	return strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unsafe(r) {
			return -1
		}
		return r
	}, s)
}
