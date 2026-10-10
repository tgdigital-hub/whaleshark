// Package gitwt is each task's own copy of the code: a git worktree on a
// branch of its own, what it is given besides what git carries, its setup
// and its removal.
//
// It puts one thing into the kit, a contract.Placement. It reads the
// project's settings with contract.ReadProjectFile only, takes no port slot
// and writes nothing into a run's record: what it returns is recorded by its
// caller. A task with no worktree is placed as contract.NoPlacement does.
//
// Every worktree it makes carries a mark in git's own folder for that
// worktree, which no repository can bring along: nothing is ever deleted
// that does not carry it.
package gitwt

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) { k.Placement = &trees{k: k, warn: os.Stderr} }

// trees is the placement. warn is where a person reads what a call has no
// other way to say: the trust prompt ahead, a path that was not carried.
type trees struct {
	k    *contract.Kit
	warn io.Writer
}

// fetchWait is how long a new worktree waits for the newest base. A machine
// with no network makes its worktree from what it has.
const fetchWait = 15 * time.Second

// run runs git in a folder and returns what it prints, without the last
// line break. Every caller ends the options before the first name or path.
func run(ctx context.Context, dir string, args ...string) (string, error) {
	// #nosec G204 G702 -- always the program git, with a list of arguments and no shell
	cmd := exec.CommandContext(ctx, "git", args...)
	var said strings.Builder
	cmd.Dir, cmd.Stderr = dir, &said
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		err = fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(said.String()))
	}
	return strings.TrimRight(string(out), "\n"), err
}

func git(dir string, args ...string) (string, error) {
	return run(context.Background(), dir, args...)
}

func refuse(code, format string, args ...any) *contract.Refusal {
	return &contract.Refusal{Exit: contract.ExitRefused, Code: code, Message: fmt.Sprintf(format, args...)}
}
