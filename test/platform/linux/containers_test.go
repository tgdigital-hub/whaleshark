// Package linux is the proof on Linux that needs no real server: the program
// file, the tests that lean on git and the checks of a work login, run in
// containers of the systems a person or a server may have. Two of them are
// also set up as a server, as far as a container holds one, and one, whose
// own git is older than the program works with, proves that it is refused.
// docs/platform/linux.md says what that proves and what waits for a real
// machine.
package linux

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// inside names what this run of the tests is, in a container: "plain" or
// "server". Outside one it is empty.
var inside = os.Getenv("WHALESHARK_INSIDE")

// Only inside a container do the tests need the program built.
func TestMain(m *testing.M) {
	if inside == "" {
		os.Exit(m.Run())
	}
	scenario.Main(m)
}

// images are the systems, each by its maker's official image. server says
// that the system is tried as a server: Debian 12 and Ubuntu 24.04, which
// are the systems a server may be, and Ubuntu 22.04, which is not one,
// because its own git (2.34) is older than 2.38, the least the program
// works with: oldGit says that this container proves the refusals.
var images = []struct {
	name           string
	server, oldGit bool
}{
	{"alpine:3.22", false, false},
	{"debian:12", true, false},
	{"ubuntu:22.04", true, true},
	{"ubuntu:24.04", true, false},
}

// wanted says how much is run here. WHALESHARK_CONTAINERS names it: "plain"
// or "server". Unnamed, a hosted Linux machine runs the plain part on every
// push, and the server part, which fetches a browser twice, on a
// branch whose name ends in "linux".
func wanted() string {
	if kind := os.Getenv("WHALESHARK_CONTAINERS"); kind != "" {
		return kind
	}
	switch {
	case os.Getenv("GITHUB_ACTIONS") != "true" || runtime.GOOS != "linux":
		return ""
	case strings.HasSuffix(os.Getenv("GITHUB_REF_NAME"), "linux"):
		return "server"
	}
	return "plain"
}

func run(t *testing.T, ctx context.Context, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// Each container is handed this tree, this machine's Go and modules and the
// program built as it is shipped, all to read only, and runs
// testdata/inside.sh. What they compile they share, so it is compiled once.
func TestContainers(t *testing.T) {
	kind := wanted()
	switch {
	case inside != "" || kind == "":
		t.Skip("the containers run on a hosted Linux machine, or where WHALESHARK_CONTAINERS says plain or server")
	case kind != "plain" && kind != "server":
		t.Fatalf("WHALESHARK_CONTAINERS is %q: plain or server", kind)
	case runtime.GOOS != "linux":
		t.Skip("the containers borrow this machine's Go, so it must be a Linux machine")
	}
	ctx := context.Background()
	if end, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, end.Add(-20*time.Second))
		t.Cleanup(cancel)
	}
	module := run(t, ctx, nil, "go", "list", "-m", "-f", "{{.Dir}}")
	goroot, mods := run(t, ctx, nil, "go", "env", "GOROOT"), run(t, ctx, nil, "go", "env", "GOMODCACHE")
	// The program as README builds it, and one that differs for the upgrade.
	bin, static := t.TempDir(), []string{"CGO_ENABLED=0"}
	run(t, ctx, static, "go", "build", "-C", module, "-trimpath", "-ldflags=-s -w", "-o", filepath.Join(bin, "whaleshark"), "./cmd/whaleshark")
	run(t, ctx, static, "go", "build", "-C", module, "-trimpath", "-o", filepath.Join(bin, "whaleshark-next"), "./cmd/whaleshark")
	if err := os.Chmod(bin, 0o755); err != nil { // #nosec G302 -- every login of a container runs the program from here
		t.Fatal(err)
	}
	own := "whaleshark-linux-" + strconv.Itoa(os.Getpid())
	run(t, ctx, nil, "docker", "volume", "create", own)
	t.Cleanup(func() { exec.Command("docker", "volume", "rm", "-f", own).Run() })

	for _, image := range images {
		t.Run(image.name, func(t *testing.T) {
			t.Parallel()
			as, name := "plain", own+"-"+strings.NewReplacer(":", "-", ".", "-").Replace(image.name)
			// The smallest share of the processor: the tests beside this one
			// keep the machine, and a container takes what they leave.
			args := []string{"run", "--rm", "--name", name, "--cpu-shares", "2", "-e", "WHALESHARK_INSIDE",
				"-v", module + ":/src:ro", "-v", goroot + ":/usr/local/go:ro", "-v", mods + ":/gomod:ro", "-v", bin + ":/ws:ro", "-v", own + ":/cache"}
			if kind == "server" && image.server {
				// The rule for who may connect to a login's ports is loaded for
				// real, which a container may do only when it is allowed to
				// change its own network; and Docker's filter forbids the
				// system call the browser's sandbox is made with.
				as, args = "server", append(args, "--cap-add", "NET_ADMIN", "--security-opt", "seccomp=unconfined")
			}
			cmd := exec.CommandContext(ctx, "docker", append(args, image.name, "sh", "/src/test/platform/linux/testdata/inside.sh", as)...)
			cmd.Env = append(os.Environ(), "WHALESHARK_INSIDE="+as)
			if image.oldGit {
				cmd.Args = slices.Insert(cmd.Args, 2, "-e", "WHALESHARK_OLD_GIT=1")
			}
			out, err := cmd.CombinedOutput()
			t.Cleanup(func() { exec.Command("docker", "rm", "-f", name).Run() })
			t.Logf("%s as %s:\n%s", image.name, as, out)
			if err != nil {
				t.Fatalf("%s: %v", image.name, err)
			}
		})
	}
}
