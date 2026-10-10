package integrate

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// call runs a program in a folder with a list of arguments and returns what
// it printed, without the last line break. Nothing it asks at a terminal can
// stop it; what it says of a failure is in the error.
func call(dir, stdin, name string, args ...string) (string, error) {
	// #nosec G204 G702 -- git or gh, with a list of arguments the tool built
	cmd := exec.Command(name, args...)
	var said strings.Builder
	cmd.Dir, cmd.Stdin, cmd.Stderr = dir, strings.NewReader(stdin), &said
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		err = fmt.Errorf("%s %s: %w: %s", name, args[0], err, strings.TrimSpace(said.String()))
	}
	return strings.TrimRight(string(out), "\n"), err
}

func git(dir string, args ...string) (string, error) { return call(dir, "", "git", args...) }

// exit is the exit code a call ended with: 0 for none, -1 when it never ran.
func exit(err error) int {
	var ended *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ended):
		return ended.ExitCode()
	}
	return -1
}

func ref(branch string) string { return "refs/heads/" + branch }

// at is the commit a name stands for; the name is never read as an option.
func at(dir, name string) (string, error) {
	return git(dir, "rev-parse", "--verify", "--quiet", "--end-of-options", name+"^{commit}")
}

// within reports whether everything of commit a is in commit b.
func within(root, a, b string) bool {
	_, err := git(root, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

// simulate merges two commits in git's own store and touches no branch, no
// index and no file: it returns the merged tree, or the files that clash.
// It needs git 2.38.
func simulate(root, ours, theirs string) (tree string, clash []string, err error) {
	out, err := git(root, "merge-tree", "--write-tree", "--name-only", "--no-messages", "-z", ours, theirs)
	parts := strings.Split(out, "\x00")
	switch exit(err) {
	case 0:
		return parts[0], nil, nil
	case 1:
		for _, file := range parts[1:] {
			if file != "" {
				clash = append(clash, file)
			}
		}
		return "", clash, nil
	}
	return "", nil, err
}

// commit makes a commit of a tree and returns it; no branch moves.
func commit(root, tree, message string, parents ...string) (string, error) {
	args := []string{"commit-tree", tree, "-m", message}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	return git(root, args...)
}

// run runs one line of the project's own in a folder, for no longer than
// limit, with all it prints written to the log. It reports whether the line
// passed and the last line it printed; an error means it never ran.
func (g integrator) run(line, dir, log string, limit time.Duration) (ok bool, tail string, err error) {
	if err = os.MkdirAll(filepath.Dir(log), 0o700); err != nil {
		return false, "", err
	}
	file, err := os.OpenFile(log, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return false, "", err
	}
	defer file.Close()
	cmd := g.k.Platform.Shell(line)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, file, file
	if err := cmd.Start(); err != nil {
		return false, "", fmt.Errorf("%q could not be started: %w", line, err)
	}
	over := time.AfterFunc(limit, func() { g.k.Platform.EndTree(cmd) })
	if err = cmd.Wait(); !over.Stop() {
		fmt.Fprintf(file, "\nwhaleshark: stopped after %v\n", limit)
	}
	data, _ := os.ReadFile(log)
	text := strings.TrimSpace(cli.Plain(strings.ToValidUTF8(string(data[max(0, len(data)-400):]), "")))
	return err == nil, strings.TrimSpace(text[strings.LastIndexByte(text, '\n')+1:]), nil
}

func refuse(exit int, code, message string, next ...string) *contract.Refusal {
	return &contract.Refusal{Exit: exit, Code: code, Message: message, Next: next}
}
