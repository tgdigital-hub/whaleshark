package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// checkers are the outside tools of the security step. None is shipped and
// none is installed by the gate: one that is missing is skipped out loud,
// and on a hosted machine (CI set) a skipped one fails.
var checkers = []struct {
	tool, what string
	args       []string
}{
	{"staticcheck", "the stricter static analyser", []string{"./..."}},
	{"gosec", "the security analyser", []string{"-quiet", "./..."}},
	{"govulncheck", "known flaws in the modules", []string{"./..."}},
	{"gitleaks", "secrets in the history", []string{"git", "--no-banner", "--redact", "."}},
	{"gitleaks", "secrets in the files", []string{"dir", "--no-banner", "--redact", "."}},
	{"shellcheck", "the shell scripts", nil},
}

func (g *gate) security() error {
	files, err := g.published()
	if err != nil {
		return err
	}
	var scripts []string
	for _, f := range files {
		data, _ := os.ReadFile(filepath.Join(g.root, f))
		if strings.HasSuffix(f, ".sh") || bytes.HasPrefix(data, []byte("#!/bin/sh")) {
			scripts = append(scripts, f)
		}
	}
	var errs []error
	for _, c := range checkers {
		args := c.args
		if c.tool == "shellcheck" {
			args = scripts
		}
		if _, err := exec.LookPath(c.tool); err != nil {
			fmt.Fprintf(g.out, "  SKIPPED %s (%s): it is not installed\n", c.tool, c.what)
			if os.Getenv("CI") != "" {
				errs = append(errs, fmt.Errorf("%s is not installed", c.tool))
			}
			continue
		}
		if _, err := g.cmd(nil, c.tool, args...); err != nil {
			errs = append(errs, err)
			continue
		}
		fmt.Fprintf(g.out, "  ok %s (%s)\n", c.tool, c.what)
	}
	return errors.Join(errs...)
}

// privateNames are files and folders that never belong in the repository.
var privateNames = regexp.MustCompile(`(^|/)(CLAUDE(\.local)?\.md|HANDOVER\.md|\.claude/|\.whaleshark/|\.private/)`)

// privateMarks are the shapes of what must not be published. An address is
// let through when it is GitHub's no-reply one, an example, or git's own.
// A letter here and there stands in brackets so that no pattern is itself
// the shape of what a scanner looks for.
var privateMarks = []struct {
	what string
	re   *regexp.Regexp
}{
	{"a path under a home folder", regexp.MustCompile(`(?i)(^|[^\w./-])(/U[s]ers/|/home/|[a-z]:\\+Users\\+)[a-z0-9._-]+`)},
	{"a private key", regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY`)},
	{"an access key", regexp.MustCompile(`\bA[KS]IA[0-9A-Z]{16}\b`)},
	{"a token", regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{30,}|github_pa[t]_[A-Za-z0-9_]{30,}|xox[abprs]-[A-Za-z0-9-]{10,}|sk-[A-Za-z0-9_-]{20,})`)},
	{"an email address", regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)},
}

var publicAddress = regexp.MustCompile(`@users\.noreply\.github\.com$|@example\.(com|org|net)$|^git@`)

// marks checks one piece of text and returns a finding for each line that
// holds a private mark; where says what the text is.
func marks(where string, text []byte) []error {
	var errs []error
	for i, line := range bytes.Split(text, []byte("\n")) {
		for _, m := range privateMarks {
			hit := m.re.Find(line)
			if hit == nil || m.what == "an email address" && publicAddress.Match(hit) {
				continue
			}
			errs = append(errs, fmt.Errorf("%s:%d: %s", where, i+1, m.what))
		}
	}
	return errs
}

// publish checks everything git would publish, as it is in the folder and
// as it is staged.
func (g *gate) publish() error {
	files, err := g.published()
	if err != nil {
		return err
	}
	var errs []error
	for _, f := range files {
		if privateNames.MatchString(f) {
			errs = append(errs, fmt.Errorf("%s: a private file", f))
		}
		errs = append(errs, marks(f, []byte(f))...)
		if data, err := os.ReadFile(filepath.Join(g.root, f)); err == nil {
			errs = append(errs, marks(f, data)...)
		}
	}
	staged, err := g.cmd(nil, "git", "diff", "--cached", "--no-color", "-U0")
	if err != nil {
		return err
	}
	errs = append(errs, marks("staged", added(staged))...)
	fmt.Fprintf(g.out, "  %d files\n", len(files))
	return errors.Join(errs...)
}

// added keeps what a patch adds and what stands around it, and drops what
// it removes or only shows.
func added(patch string) []byte {
	var keep []string
	for _, line := range strings.Split(patch, "\n") {
		if !strings.HasPrefix(line, "-") && !strings.HasPrefix(line, " ") {
			keep = append(keep, line)
		}
	}
	return []byte(strings.Join(keep, "\n"))
}

// message checks a commit message file.
func (g *gate) message() error {
	if len(g.args) != 1 {
		return errors.New("usage: message FILE")
	}
	data, err := os.ReadFile(g.args[0])
	if err != nil {
		return err
	}
	return errors.Join(marks("the message", data)...)
}

// history checks the commits of a range (all of HEAD's when none is given):
// who made them, what they say and what they add.
func (g *gate) history() error {
	args := append([]string{"log", "-p", "--no-color", "--format=commit %h%n%an <%ae>%n%cn <%ce>%n%B"}, g.args...)
	out, err := g.cmd(nil, "git", args...)
	if err != nil {
		return err
	}
	return errors.Join(marks("history", added(out))...)
}
