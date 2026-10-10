package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// checkers are the outside tools of the security step. None is shipped and
// none is installed by the gate: one that is missing is skipped out loud,
// and on a hosted machine (CI set) a skipped one fails. over names the list
// a checker is given after its own arguments: the shell scripts, the folders
// of the program and the gate, or the folders of the stand-ins under test/.
var checkers = []struct {
	tool, what string
	args       []string
	over       string
}{
	{"staticcheck", "the stricter static analyser", []string{"./..."}, ""},
	{"gosec", "the security analyser", []string{"-quiet", "-exclude=" + gosecAside}, "ours"},
	{"gosec", "the security analyser, over the stand-ins", []string{"-quiet", "-exclude=" + gosecDoubles}, "doubles"},
	{"govulncheck", "known flaws in the modules", []string{"./..."}, ""},
	{"gitleaks", "secrets in the history", []string{"git", "--no-banner", "--redact", "."}, ""},
	{"gitleaks", "secrets in the files", []string{"dir", "--no-banner", "--redact", "."}, ""},
	{"shellcheck", "the shell scripts", nil, "scripts"},
}

// Two of gosec's rules are set aside for every folder, because of what this
// program is: a command a person runs as themselves, on files they name.
//
// G304, a file opened by a path held in a variable. Every file this program
// opens is one: the person's project, their state folder, a file they name.
//
// G104, an error nobody reads. What was left unread when the rule was set
// aside, each read by a person: a close after the work is done or has
// failed, a line to a terminal or a connection that has gone, the restoring
// of a terminal on the way out, and the note of who holds a lock.
//
// The stand-ins under test/, the engine's double and the fake agent, are in no
// program we ship and are held to a lighter rule. A test tells them through
// their environment which program to start (G204, G702), which socket to
// call (G704) and which files to touch (G703), and that is their whole use.
const (
	gosecAside   = "G104,G304"
	gosecDoubles = gosecAside + ",G204,G702,G703,G704"
)

func (g *gate) security() error {
	files, err := g.published()
	if err != nil {
		return err
	}
	over := map[string][]string{}
	for _, f := range files {
		data, _ := os.ReadFile(filepath.Join(g.root, f))
		if strings.HasSuffix(f, ".sh") || bytes.HasPrefix(data, []byte("#!/bin/sh")) {
			over["scripts"] = append(over["scripts"], f)
		}
	}
	module, err := g.cmd(nil, "go", "list", "-m")
	if err != nil {
		return err
	}
	packages, err := g.cmd(nil, "go", "list", "./...")
	if err != nil {
		return err
	}
	for _, pkg := range strings.Fields(packages) {
		rel := strings.TrimPrefix(pkg, strings.TrimSpace(module)+"/")
		which := "ours"
		if under(rel, "test") {
			which = "doubles"
		}
		over[which] = append(over[which], "./"+rel)
	}
	var errs []error
	for _, c := range checkers {
		args := append(slices.Clone(c.args), over[c.over]...)
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

// scaffold is the name of the program the tool ran on while its own engine
// was built. Nothing published may hold it: not a file's name and not a
// line, but one line of the README, for its credits. It is put together
// here so that this file holds none.
var scaffold = []byte("her" + "dr")

// foreign finds that name in a file's own name and on each of its lines.
func foreign(f string, data []byte) (errs []error) {
	allowed := 0
	if f == "README.md" {
		allowed = 1
	}
	for i, line := range bytes.Split(append([]byte(f+"\n"), data...), []byte("\n")) {
		if !bytes.Contains(bytes.ToLower(line), scaffold) {
			continue
		}
		if allowed--; i == 0 || allowed < 0 {
			errs = append(errs, fmt.Errorf("%s:%d: names the program the tool once ran on", f, i))
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
		data, err := os.ReadFile(filepath.Join(g.root, f))
		if err == nil {
			errs = append(errs, marks(f, data)...)
		}
		errs = append(errs, foreign(f, data)...)
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
