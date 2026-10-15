package ownlogin_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/test/ownlogin"
)

func TestMain(m *testing.M) { ownlogin.Main(m) }

var named = []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "APPDATA", "LOCALAPPDATA"}

// A test started in a person's login, with that person's engine named, is
// in a login of its own: the program's folders are worked out under a
// temporary one, a project that is listed lands there and nothing in the
// person's folders, a program the test starts is handed the same, and the
// Go command keeps its own cache and settings. When the tests are over the
// folder is gone.
func TestATestIsInALoginOfItsOwn(t *testing.T) {
	person := t.TempDir()
	for _, name := range named {
		t.Setenv(name, filepath.Join(person, name))
	}
	t.Setenv("WHALESHARK_SOCKET", filepath.Join(person, "engine.sock"))
	t.Setenv("GOCACHE", "")
	t.Setenv("GOENV", "")
	cache, _ := os.UserCacheDir()
	config, _ := os.UserConfigDir()

	done := ownlogin.Fresh()
	sys := platform.New(runtime.GOOS)
	dirs, err := sys.Dirs()
	if err != nil {
		t.Fatal(err)
	}
	own := filepath.Dir(os.Getenv(named[0]))
	for _, dir := range []string{dirs.Config, dirs.State, dirs.Cache} {
		if !strings.HasPrefix(dir, own+string(filepath.Separator)) || strings.HasPrefix(dir, person) {
			t.Errorf("the program's folder %s is not under the login made for the tests, %s", dir, own)
		}
	}
	if !strings.HasPrefix(own, filepath.Clean(os.TempDir())) || len(own) > len(os.TempDir())+16 {
		t.Errorf("the login made for the tests is %s: a short path under the temporary folder was expected", own)
	}
	if got := contract.SocketPath(os.Getenv, dirs); got != filepath.Join(dirs.State, "engine.sock") {
		t.Errorf("the engine's socket is looked for at %s", got)
	}
	if os.Getenv("GOCACHE") != filepath.Join(cache, "go-build") || os.Getenv("GOENV") != filepath.Join(config, "go", "env") {
		t.Errorf("the Go command's cache and settings moved: %s, %s", os.Getenv("GOCACHE"), os.Getenv("GOENV"))
	}
	if err := contract.ListProject(sys, filepath.Join(person, "a project")); err != nil {
		t.Fatal(err)
	}
	if list, err := contract.ReadProjects(os.ReadFile, dirs.State); err != nil || len(list) != 1 {
		t.Errorf("the list of the login made for the tests holds %v: %v", list, err)
	}
	if left, _ := os.ReadDir(person); len(left) != 0 {
		t.Errorf("%d things were written into the person's own login: %v", len(left), left)
	}
	done()
	if _, err := os.Stat(own); !os.IsNotExist(err) {
		t.Errorf("the login made for the tests is still there: %v", err)
	}
}

// The tests of this package were themselves given such a login before the
// first of them ran.
func TestThePackagesOwnTestsHaveOne(t *testing.T) {
	own := filepath.Dir(os.Getenv(named[0]))
	for _, name := range named {
		if dir := os.Getenv(name); filepath.Dir(dir) != own || !strings.HasPrefix(dir, filepath.Clean(os.TempDir())) {
			t.Errorf("%s is %s", name, dir)
		}
	}
	if os.Getenv("WHALESHARK_SOCKET") != "" || os.Getenv("HISTFILE") != os.DevNull {
		t.Errorf("an engine of the login the tests were started in is named, or a shell would keep its history in %q", os.Getenv("HISTFILE"))
	}
}

// A test started in a worker's tab is no tab's: no variable of ours reaches
// a program it starts, which would otherwise take itself for that worker,
// but one the test sets itself after does, and so do a test's own.
func TestATestIsNoTabs(t *testing.T) {
	tab := []string{"WHALESHARK_ROOT", "WHALESHARK_RUN", "WHALESHARK_TASK", "WHALESHARK_ATTEMPT", "WHALESHARK_DEPTH",
		"WHALESHARK_BIN", "WHALESHARK_EVIDENCE", "WHALESHARK_PANE", "WHALESHARK_SOCKET", "WHALESHARK_CLOCK"}
	for _, name := range tab {
		t.Setenv(name, "from the tab")
	}
	t.Setenv("WHALESHARK_TEST_ROLE", "the test's own")
	t.Setenv("WHALESHARK_SOAK", "the person's switch")
	defer ownlogin.Fresh()()
	t.Setenv("WHALESHARK_MARKS", "the test's own")
	shell := exec.Command("env")
	if runtime.GOOS == "windows" {
		shell = exec.Command("cmd", "/c", "set")
	}
	out, err := shell.Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range tab {
		if strings.Contains(string(out), name+"=") {
			t.Errorf("%s reached a program the test started", name)
		}
	}
	for _, kv := range []string{"WHALESHARK_MARKS=the test's own", "WHALESHARK_TEST_ROLE=the test's own", "WHALESHARK_SOAK=the person's switch"} {
		if !strings.Contains(string(out), kv) {
			t.Errorf("%s did not reach the program:\n%s", kv, out)
		}
	}
	if got := ownlogin.NoTab([]string{"WHALESHARK_TASK=T1", "HOME=/h", "whaleshark_pane=p2"}); len(got) != 1 || got[0] != "HOME=/h" {
		t.Errorf("NoTab left %q", got)
	}
}
