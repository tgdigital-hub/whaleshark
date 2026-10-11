package windows

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/engine"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/internal/restore"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// login is a made-up login of the test's own, with the socket where a real
// login has it: every folder the program asks the system for is under one
// short folder, since a socket's path may not be long.
func login(t *testing.T) (dir string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "ws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	for name, sub := range map[string]string{"HOME": "", "USERPROFILE": "", "XDG_CONFIG_HOME": "config", "XDG_CACHE_HOME": "cache",
		"XDG_STATE_HOME": "state", "APPDATA": "roaming", "LOCALAPPDATA": "local"} {
		t.Setenv(name, filepath.Join(dir, sub))
	}
	t.Setenv(contract.EnvSocket, "")
	// A pane's shell is the system's own: PowerShell on Windows, whatever
	// shell this test was started from.
	t.Logf("FACT SHELL was %q where the test began", os.Getenv("SHELL"))
	t.Setenv("SHELL", "")
	return dir
}

// keeper starts the real keeper in the login the surroundings name and
// waits until it answers; what comes back stops it.
func keeper(t *testing.T, start func() *exec.Cmd) (terms *engine.Client, stop func()) {
	t.Helper()
	k := system()
	terms = engine.New(k)
	run := start()
	var said bytes.Buffer
	run.Stdout, run.Stderr = &said, &said
	if err := run.Start(); err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() { ended <- run.Wait() }()
	stopped := false
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		terms.Call(context.Background(), contract.WireCall{Op: contract.OpStop})
		select {
		case <-ended:
		case <-time.After(20 * time.Second):
			run.Process.Kill()
			t.Errorf("the keeper did not end when asked\n%s", &said)
		}
	}
	t.Cleanup(stop)
	if !soon(20*time.Second, func() bool { _, err := terms.Version(); return err == nil }) {
		t.Fatalf("the keeper did not answer\n%s", &said)
	}
	return terms, stop
}

// script is a file a pane runs: it prints a mark made of a variable of the
// tab and the pane's own, then stays, on Windows with a program of its own
// that holds a file so that nobody else can open it.
func script(t *testing.T, k *contract.Kit) (argv []string, held string) {
	t.Helper()
	dir := t.TempDir()
	held = filepath.Join(dir, "held")
	if k.Platform.System() != "windows" {
		file := filepath.Join(dir, "stay.sh")
		os.WriteFile(file, []byte("echo got-$WS_PROOF-$"+contract.EnvPane+"\nsleep 600\n"), 0o700)
		return []string{"sh", file}, ""
	}
	file := filepath.Join(dir, "stay.cmd")
	os.WriteFile(file, []byte("@echo got-%WS_PROOF%-%"+contract.EnvPane+"%\r\n"+
		`@powershell -NoProfile -Command "$f = [IO.File]::Open('`+held+`', 'OpenOrCreate', 'ReadWrite', 'None'); Start-Sleep 600"`+"\r\n"), 0o700)
	return []string{"cmd.exe", "/d", "/c", file}, held
}

// window attaches to the keeper as a person's window of that size does, and
// gives what the keeper has drawn into it so far.
func window(t *testing.T, k *contract.Kit, w, h int) (drawn func() string) {
	t.Helper()
	dirs, err := k.Platform.Dirs()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("unix", contract.SocketPath(os.Getenv, dirs))
	if err != nil {
		t.Fatalf("a window: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	send := func(c contract.WireCall) {
		body, _ := json.Marshal(c)
		if err := contract.WriteFrame(conn, contract.FrameCall, body); err != nil {
			t.Fatalf("a window: %v", err)
		}
	}
	send(contract.WireCall{V: contract.WireVersion, ID: 1, Op: contract.OpHello})
	if _, _, err := contract.ReadFrame(conn); err != nil {
		t.Fatalf("a window: %v", err)
	}
	send(contract.WireCall{ID: 2, Op: contract.OpAttach, W: float64(w), H: float64(h), System: k.Platform.System()})
	var mu sync.Mutex
	var all []byte
	go func() {
		for {
			kind, body, err := contract.ReadFrame(conn)
			if err != nil {
				return
			}
			if kind == contract.FrameBytes {
				mu.Lock()
				all = append(all, body...)
				mu.Unlock()
			}
		}
	}()
	return func() string { mu.Lock(); defer mu.Unlock(); return string(all) }
}

// proveAKeeper asks a running keeper for what a run needs of it: a tab with
// its variables, a window that gives its panes their size, a program
// started in a pane from a list of words, what the pane shows, a pane
// beside it, and the end of everything a pane started.
func proveAKeeper(t *testing.T, terms *engine.Client) {
	t.Helper()
	k := system()
	argv, held := script(t, k)
	pane, err := terms.TabCreate(os.TempDir(), "proof", []string{"WS_PROOF=mark"})
	if err != nil {
		t.Fatalf("a tab: %v", err)
	}
	if err := terms.TabFocus(pane.Tab); err != nil {
		t.Errorf("the tab in front: %v", err)
	}
	shown := func() string { text, _ := terms.Screen(pane.ID); return text }
	if !soon(30*time.Second, func() bool { return strings.TrimSpace(shown()) != "" }) {
		t.Errorf("the pane's shell showed nothing")
	}
	t.Logf("FACT a new pane's shell shows: %q", strings.TrimSpace(shown()))
	drawn := window(t, k, 120, 40)
	var w, h int
	if !soon(10*time.Second, func() bool { w, h, _ = terms.Size(pane.ID); return w > 80 }) {
		t.Errorf("a window 120 wide left the pane at %dx%d", w, h)
	}
	if err := terms.Run(pane.ID, argv); err != nil {
		t.Fatalf("a program in the pane: %v", err)
	}
	want := "got-mark-" + pane.ID
	if !soon(30*time.Second, func() bool { return strings.Contains(shown(), want) }) {
		t.Fatalf("the pane should show %q and shows:\n%s", want, shown())
	}
	beside, err := terms.Split(pane.ID, contract.Right, 0.5)
	if err != nil {
		t.Fatalf("a pane beside it: %v", err)
	}
	var w2 int
	if !soon(10*time.Second, func() bool { w2, _, _ = terms.Size(pane.ID); return w2 < w && w2 >= 8 }) {
		t.Errorf("a pane %d wide, split in two, is %d wide", w, w2)
	}
	if !soon(10*time.Second, func() bool { return strings.Contains(shown(), "got-mark") }) {
		t.Errorf("made narrower, the pane no longer shows its mark:\n%s", shown())
	}
	t.Logf("FACT the pane was %dx%d in a window of 120x40 and %d wide beside another; the window was sent %d bytes, the mark among them: %v",
		w, h, w2, len(drawn()), strings.Contains(drawn(), "got-mark"))
	if drawn() == "" {
		t.Error("the keeper drew nothing into the window")
	}
	if held != "" && !soon(30*time.Second, func() bool { return fileIsThere(held) && os.Remove(held) != nil }) {
		t.Fatalf("the pane's program never held its file")
	}
	if err := terms.TabClose(pane.Tab); err != nil {
		t.Errorf("closing the tab: %v", err)
	}
	snap, err := terms.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, left := range snap.Panes {
		if left.ID == pane.ID || left.ID == beside.ID {
			t.Errorf("pane %s is still there after its tab was closed", left.ID)
		}
	}
	if held != "" && !soon(20*time.Second, func() bool { return os.Remove(held) == nil }) {
		t.Errorf("a program the pane started outlived the pane: it still holds its file")
	}
}

func fileIsThere(path string) bool { _, err := os.Stat(path); return err == nil }

// TestTheKeeperOnThisSystem is the keeper as a person starts it, with real
// terminals: on Windows the pseudo-console and the socket file, which the
// scenarios do not play there. It answers on the socket file in the login's
// own folder, does what proveAKeeper asks, refuses a second keeper of the
// same login, stops when asked, and brings a tab back when it is started
// again.
func TestTheKeeperOnThisSystem(t *testing.T) {
	login(t)
	bin, err := exec.LookPath("whaleshark")
	if err != nil {
		t.Fatal(err)
	}
	start := func() *exec.Cmd { return exec.Command(bin, "engine", "run") }
	terms, stop := keeper(t, start)
	k := system()
	dirs, err := k.Platform.Dirs()
	if err != nil {
		t.Fatal(err)
	}
	socket := contract.SocketPath(os.Getenv, dirs)
	if !fileIsThere(socket) {
		t.Errorf("no socket file at %s", socket)
	}
	t.Logf("FACT the socket file is %s (%d characters)", socket, len(socket))
	proveAKeeper(t, terms)

	if out, err := exec.Command(bin, "engine", "run").CombinedOutput(); err == nil {
		t.Errorf("a second keeper of the same login started:\n%s", out)
	} else {
		t.Logf("FACT a second keeper is refused: %s", bytes.TrimSpace(out))
	}
	status, err := exec.Command(bin, "engine", "status").CombinedOutput()
	if err != nil || !strings.Contains(string(status), "pid ") {
		t.Errorf("engine status: %v\n%s", err, status)
	}
	kept, err := terms.TabCreate(os.TempDir(), "kept", nil)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	if _, err := terms.Version(); err == nil {
		t.Error("the keeper still answers after it was stopped")
	}
	terms, _ = keeper(t, start)
	snap, err := terms.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	back := false
	for _, pane := range snap.Panes {
		back = back || pane.ID == kept.ID && pane.Label == "kept"
	}
	if !back {
		t.Errorf("the tab is not back after the keeper was started again: %+v", snap.Panes)
	}
}

// TestTheKeeperAtLogOn has the tool itself set the keeper to start when the
// person logs on, on a hosted machine, whose own list of start-up programs
// this changes and puts back: `init --at-login` writes one line under one
// name into the login's own list (the registry's Run key, which asks no
// administrator), doctor says so, and `init --remove --all` takes it out
// again, which doctor says too. In between, the line is started as Windows
// starts it at log-on, with a console of its own and no terminal, and that
// keeper is asked for a pane as any other. A hosted machine has nobody who
// logs on: that Windows runs the line at a real log-on, and that no window
// stays on the screen, is for the real machine.
func TestTheKeeperAtLogOn(t *testing.T) {
	onlyHosted(t)
	p := evening(t)
	bin, err := exec.LookPath("whaleshark")
	if err != nil {
		t.Fatal(err)
	}
	name, want := restore.Service("windows", bin, os.Getenv)
	t.Cleanup(func() { platform.SetLogOn(name, "") })
	row := func() string {
		out, _ := p.Command(scenario.Unbound, "doctor").CombinedOutput()
		for _, l := range strings.Split(string(out), "\n") {
			if strings.Contains(l, "when you log on") {
				return strings.TrimSpace(l)
			}
		}
		return "no row in: " + string(out)
	}
	if have, err := platform.LogOn(name); have != "" || err != nil || !strings.HasPrefix(row(), "note") || !strings.Contains(row(), "Fix: whaleshark init --at-login") {
		t.Errorf("before: the list holds %q (%v), and doctor says: %s", have, err, row())
	}
	out, err := p.Command(scenario.Unbound, "init", "--at-login").CombinedOutput()
	t.Logf("FACT init --at-login: %v\n%s", err, out)
	line, err := platform.LogOn(name)
	if line != want || err != nil || !strings.HasPrefix(row(), "ok") {
		t.Fatalf("after init --at-login the list holds %q (%v), not %q; doctor says: %s", line, err, want, row())
	}
	t.Logf("FACT Windows starts at log-on, under the name %s: %s", name, line)
	out, err = p.Command(scenario.Unbound, "init", "--remove", "--all").CombinedOutput()
	if have, gone := platform.LogOn(name); have != "" || gone != nil || err != nil {
		t.Errorf("after init --remove --all the list holds %q (%v)\n%v %s", have, gone, err, out)
	}
	// The project is out of the login's list with it; set up again, doctor speaks of it.
	if out, err := p.Command(scenario.Human, "init", "--adopt").CombinedOutput(); err != nil || !strings.HasPrefix(row(), "note") {
		t.Errorf("after init --remove --all doctor says: %s\n%v %s", row(), err, out)
	}

	login(t)
	// start gives the program a console of its own, as a log-on does.
	terms, _ := keeper(t, func() *exec.Cmd { return p.Kit.Platform.Shell(`start "" ` + line) })
	status, _ := exec.Command(bin, "engine", "status").CombinedOutput()
	_, pid, _ := strings.Cut(string(status), "pid ")
	pid = strings.TrimFunc(strings.Fields(pid + " 0")[0], func(r rune) bool { return r < '0' || r > '9' })
	shown, _ := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", "(Get-Process -Id "+pid+").MainWindowHandle").CombinedOutput()
	t.Logf("FACT the keeper started that way is process %s, and the handle of a window of its own is: %s", pid, bytes.TrimSpace(shown))
	proveAKeeper(t, terms)
}
