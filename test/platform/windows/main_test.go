// Package windows is the proof of the tool on Windows: what a hosted
// Windows machine can show of it, played with the real program in a made-up
// login. What is true of every system runs on every system; what is a
// question of Windows alone is skipped elsewhere, and says so.
// docs/platform/windows.md is the list of what each test proves and the
// checklist for a real machine.
package windows

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func TestMain(m *testing.M) { scenario.Main(m) }

// worker is an attempt of the fixture that is at work in a tab.
const worker = "T1.1"

// evening is the fixture's run as a project on disk.
func evening(t *testing.T) *scenario.Project {
	t.Helper()
	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	return scenario.Prepare(t, f, nil)
}

// system is the real platform with nothing else behind it.
func system() *contract.Kit {
	k := contract.NewKit()
	platform.Plug(k)
	return k
}

// onlyWindows skips a question that only Windows asks, out loud.
func onlyWindows(t *testing.T) {
	t.Helper()
	if s := system().Platform.System(); s != "windows" {
		t.Skipf("NOT PROVEN HERE: this asks something of Windows alone, and this machine runs %s", s)
	}
}

// hosted says whether this is a hosted machine of the pipeline: one that is
// thrown away after the run, and that is known to have every shell and a
// browser.
func hosted() bool { return os.Getenv("GITHUB_ACTIONS") == "true" }

// onlyHosted skips what changes the machine it runs on (a scheduled task, an
// installed package) anywhere but on a hosted Windows machine.
func onlyHosted(t *testing.T) {
	t.Helper()
	onlyWindows(t)
	if !hosted() {
		t.Skip("NOT PROVEN HERE: this changes the machine, so it runs on a hosted one only")
	}
}

// missing is a thing a test needs and this machine lacks: a skip that says
// so, but a failure on a hosted Windows machine, where a proof that
// quietly did not run would read as one that passed.
func missing(t *testing.T, what string) {
	t.Helper()
	if hosted() && system().Platform.System() == "windows" {
		t.Fatalf("the hosted machine has no %s, so nothing was proven", what)
	}
	t.Skipf("NOT PROVEN HERE: this machine has no %s", what)
}

// A shell is a program that takes one line as a person or an agent's tool
// would type it, and the spelling Quote writes for it.
type shell struct {
	name, dialect string
	run           func(line string) *exec.Cmd
}

// encoded is a line as both PowerShells take it with no quoting in between.
func encoded(line string) string {
	units := utf16.Encode([]rune(line))
	raw := make([]byte, 2*len(units))
	for i, u := range units {
		binary.LittleEndian.PutUint16(raw[2*i:], u)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// gitBash is the shell that comes with git on Windows, which some agents'
// tools run their commands in; empty where there is none.
func gitBash() string {
	out, err := exec.Command("git", "--exec-path").Output()
	bash := filepath.Join(strings.TrimSpace(string(out)), "..", "..", "..", "bin", "bash.exe")
	if _, gone := os.Stat(bash); err != nil || gone != nil {
		return ""
	}
	return bash
}

// shells is every shell of this machine a worker's commands may pass
// through: the system's own, both PowerShells and git's.
func shells(k *contract.Kit) []shell {
	found := []shell{{"sh", contract.ShellPosix, k.Platform.Shell}}
	if k.Platform.System() == "windows" {
		found[0].name, found[0].dialect = "cmd", contract.ShellCmd
	}
	for _, name := range []string{"powershell", "pwsh"} {
		if exe, err := exec.LookPath(name); err == nil {
			found = append(found, shell{name, contract.ShellPowerShell, func(line string) *exec.Cmd {
				return exec.Command(exe, "-NoProfile", "-NonInteractive", "-EncodedCommand", encoded(line))
			}})
		}
	}
	if bash := gitBash(); bash != "" {
		found = append(found, shell{"git-bash", contract.ShellPosix, func(line string) *exec.Cmd {
			return exec.Command(bash, "-c", line)
		}})
	}
	return found
}

// typed runs a line in a shell as the worker's tab would: in the worker's
// folder, with the tab's variables and whatever more is given. It waits for
// the shell alone, not for a program the line left running with the
// shell's output still in its hands.
func typed(p *scenario.Project, sh shell, line string, env ...string) (string, error) {
	tab := p.Command(worker, "mail")
	cmd := sh.run(line)
	var out bytes.Buffer
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr, cmd.WaitDelay = tab.Dir, append(tab.Env, env...), &out, &out, 2*time.Second
	if err := cmd.Start(); err != nil {
		return "", err
	}
	late := time.AfterFunc(2*time.Minute, func() { p.Kit.Platform.EndTree(cmd) })
	defer late.Stop()
	err := cmd.Wait()
	return out.String(), err
}

// soon waits up to a time for something to become true.
func soon(within time.Duration, is func() bool) bool {
	for end := time.Now().Add(within); !is(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			return false
		}
	}
	return true
}
