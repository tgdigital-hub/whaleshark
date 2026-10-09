// Command ci is the build gate: it builds and tests the tree, holds it to the
// limits in limits.txt, runs the security checkers that are installed and
// refuses private material. Run it from the top of the repository:
//
//	go run ./build/ci            every step
//	go run ./build/ci lines      one step, or several
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type gate struct {
	root string
	out  io.Writer
	lim  limits
	args []string // what follows the step, for message and history
}

type step struct {
	name string
	all  bool // part of a run with no step named
	run  func(*gate) error
}

var steps = []step{
	{"build", true, func(g *gate) error { return g.goDo("build", "./...") }},
	{"vet", true, func(g *gate) error { return g.goDo("vet", "./...") }},
	{"fmt", true, (*gate).format},
	{"test", true, func(g *gate) error { return g.goDo("test", "./...") }},
	{"cross", true, (*gate).cross},
	{"lines", true, (*gate).lines},
	{"modules", true, (*gate).modules},
	{"page", true, (*gate).page},
	{"commands", true, (*gate).commands},
	{"imports", true, (*gate).imports},
	{"timing", true, (*gate).timing},
	{"security", true, (*gate).security},
	{"publish", true, (*gate).publish},
	{"message", false, (*gate).message},
	{"history", false, (*gate).history},
}

func main() {
	root, err := os.Getwd()
	if err == nil {
		err = run(root, os.Args[1:], os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ci: FAILED:", err)
		os.Exit(1)
	}
}

// run does the named steps in the table's order, all of them even after one
// has failed, so one run shows everything that is wrong.
func run(root string, args []string, out io.Writer) error {
	lim, err := readLimits(root)
	if err != nil {
		return err
	}
	g := &gate{root: root, out: out, lim: lim}
	todo := map[string]bool{}
	for i, a := range args {
		if a == "message" || a == "history" {
			g.args = args[i+1:]
			args = args[:i+1]
			break
		}
	}
	for _, a := range args {
		todo[a] = true
	}
	var failed []string
	for _, s := range steps {
		if !todo[s.name] && !(len(args) == 0 && s.all) {
			continue
		}
		delete(todo, s.name)
		fmt.Fprintf(out, "== %s\n", s.name)
		if err := s.run(g); err != nil {
			fmt.Fprintf(out, "FAILED %s:\n%v\n", s.name, err)
			failed = append(failed, s.name)
		}
	}
	for a := range todo {
		return fmt.Errorf("no step %q", a)
	}
	if failed != nil {
		return errors.New(strings.Join(failed, ", "))
	}
	fmt.Fprintln(out, "ci: all passed")
	return nil
}

// limits is limits.txt: the fields of every line, by its first word.
type limits map[string][][]string

func readLimits(root string) (limits, error) {
	data, err := os.ReadFile(filepath.Join(root, "build", "ci", "limits.txt"))
	if err != nil {
		return nil, err
	}
	l := limits{}
	for _, line := range strings.Split(string(data), "\n") {
		if f := strings.Fields(line); len(f) > 0 && !strings.HasPrefix(f[0], "#") {
			l[f[0]] = append(l[f[0]], f[1:])
		}
	}
	return l, nil
}

// num is the figure of a limit that has one line.
func (l limits) num(key string) (int, error) {
	if len(l[key]) != 1 || len(l[key][0]) == 0 {
		return 0, fmt.Errorf("limits.txt needs exactly one %q line with a figure", key)
	}
	return strconv.Atoi(l[key][0][0])
}

// words is every field of every line of a key.
func (l limits) words(key string) []string {
	var w []string
	for _, row := range l[key] {
		w = append(w, row...)
	}
	return w
}

// cmd runs a program in the tree and returns what it printed. Cgo is off
// for everything, as it is for the program we ship.
func (g *gate) cmd(env []string, name string, args ...string) (string, error) {
	c := exec.Command(name, args...)
	c.Dir = g.root
	c.Env = append(append(os.Environ(), "CGO_ENABLED=0"), env...)
	var errOut bytes.Buffer
	c.Stderr = &errOut
	out, err := c.Output()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %v\n%s%s", name, strings.Join(args, " "), err, out, errOut.Bytes())
	}
	return string(out), nil
}

func (g *gate) goDo(args ...string) error {
	_, err := g.cmd(nil, "go", args...)
	return err
}

// published is every file git would publish: tracked, staged, or new and
// not ignored.
func (g *gate) published() ([]string, error) {
	out, err := g.cmd(nil, "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	return strings.Split(strings.TrimSuffix(out, "\x00"), "\x00"), err
}

func (g *gate) format() error {
	files, err := g.published()
	if err != nil {
		return err
	}
	args := []string{"-l"}
	for _, f := range files {
		if _, gone := os.Stat(filepath.Join(g.root, f)); strings.HasSuffix(f, ".go") && gone == nil {
			args = append(args, f)
		}
	}
	out, err := g.cmd(nil, "gofmt", args...)
	if err == nil && out != "" {
		err = fmt.Errorf("not formatted:\n%s", out)
	}
	return err
}
