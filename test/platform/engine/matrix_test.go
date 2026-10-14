// Package engine holds the engine's matrix: every call of the terminals'
// interface and every kind of event, made against the real keeper with real
// terminals on the system the test runs on, and once more over ssh. What
// each came to is a cell of the table in docs/platform/engine-matrix.md, and
// the test fails where the table says anything else.
package engine

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/engine"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/internal/pty"
	"github.com/tgdigital-hub/whaleshark/test/leash"
	"github.com/tgdigital-hub/whaleshark/test/ownlogin"
)

const (
	patience = 10 * time.Second
	tree     = "../../.."
	// envFar is set for the run that sshd started: it names this folder.
	envFar = "ENGINE_MATRIX_FAR"
	// The rows that are no call and no event of the interface.
	rowShell, rowWindow, rowHook, rowSSH = "status", "open", "hook state", "ssh"
)

// TestMain builds the real program and the fake agent, which stands on the
// path under an agent kind's name too, so that no test starts a real agent.
// The run on the far side of ssh finds them built.
func TestMain(m *testing.M) {
	os.Exit(func() int {
		defer ownlogin.Fresh()() // on the far side of ssh too, which is this login again
		if far := os.Getenv(envFar); far != "" {
			if err := os.Chdir(far); err != nil {
				fmt.Fprintln(os.Stderr, "matrix:", err)
				return 1
			}
			return m.Run()
		}
		dir, err := os.MkdirTemp("", "matrix")
		if err == nil {
			defer os.RemoveAll(dir)
			build := exec.Command("go", "build", "-o", dir+string(filepath.Separator), "./cmd/whaleshark", "./test/fakeagent")
			build.Dir, build.Stderr = tree, os.Stderr
			err = build.Run()
		}
		if agent, _ := filepath.Glob(filepath.Join(dir, "fakeagent*")); err == nil && len(agent) == 1 {
			err = os.Link(agent[0], filepath.Join(dir, "claude"+filepath.Ext(agent[0])))
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "matrix: building the programs:", err)
			return 1
		}
		os.Setenv("PATH", dir+string(filepath.ListSeparator)+os.Getenv("PATH"))
		return m.Run()
	}())
}

// table is this system's column of the document, by the name each row
// carries between its first two backticks.
func table(t *testing.T, system string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(tree, "docs", "platform", "engine-matrix.md"))
	if err != nil {
		t.Fatal(err)
	}
	col := 2 + slices.Index([]string{"darwin", "linux", "windows"}, system)
	rows := map[string]string{}
	for line := range strings.Lines(string(data)) {
		cells := strings.Split(line, "|")
		if len(cells) != 6 {
			continue
		}
		if name := strings.Split(cells[1], "`"); len(name) > 2 {
			rows[name[1]] = strings.TrimSpace(cells[col])
		}
	}
	return rows
}

// names are the rows the interface itself asks for: its methods, and the
// kinds of event the contract's source declares.
func names(t *testing.T) (calls, kinds []string) {
	t.Helper()
	for m := range reflect.TypeFor[contract.Terminals]().Methods() {
		calls = append(calls, m.Name)
	}
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(tree, "internal", "contract", "ports.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if v, ok := n.(*ast.ValueSpec); ok && len(v.Values) == len(v.Names) {
			for i, name := range v.Names {
				if lit, ok := v.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING && strings.HasPrefix(name.Name, "Ev") {
					kind, _ := strconv.Unquote(lit.Value)
					kinds = append(kinds, kind)
				}
			}
		}
		return true
	})
	return calls, kinds
}

// held fails for every row where the table and the run differ, and prints
// the run's whole column as the table would hold it.
func held(t *testing.T, want, got map[string]string, rows []string) {
	t.Helper()
	wrong := false
	for _, row := range rows {
		if got[row] != want[row] {
			wrong = true
			t.Errorf("%s: the table says %q, the run %q", row, want[row], got[row])
		}
	}
	if wrong {
		var column strings.Builder
		for _, row := range rows {
			fmt.Fprintf(&column, "| `%s` | %s |\n", row, got[row])
		}
		t.Logf("this run's column:\n%s", &column)
	}
}

// stream gathers the events of one reading: every kind that came, the
// picture brought up to date by each, and whether they came in order.
type stream struct {
	mu      sync.Mutex
	picture *contract.Snapshot
	kinds   map[string]bool
	ordered bool
	ended   chan struct{}
}

func gather(first *contract.Snapshot, events <-chan contract.TermEvent) *stream {
	s := &stream{picture: first, kinds: map[string]bool{}, ordered: true, ended: make(chan struct{})}
	go func() {
		defer close(s.ended)
		for e := range events {
			s.mu.Lock()
			s.ordered = s.ordered && e.Seq == s.picture.Seq+1
			s.kinds[e.Kind] = true
			contract.Apply(s.picture, e)
			s.mu.Unlock()
		}
	}()
	return s
}

func (s *stream) saw(kind string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.kinds[kind]
}

// column starts a keeper in a pretended login of its own and makes every
// call through the adapter, as a command does. Each cell is "yes", or "no"
// with the name the refusal travels under, or with "not seen" where the call
// was answered and what it should bring about did not come.
func column(t *testing.T) (cells map[string]string, system string) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// A socket's path is short, so its folder is not under the test's own.
	short, err := os.MkdirTemp("", "ws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(short) })
	for name, value := range map[string]string{"HOME": base, "USERPROFILE": base, "XDG_CONFIG_HOME": filepath.Join(base, "config"),
		"XDG_CACHE_HOME": filepath.Join(base, "cache"), "APPDATA": filepath.Join(base, "roaming"),
		"XDG_STATE_HOME": filepath.Join(short, "state"), "LOCALAPPDATA": filepath.Join(short, "local"),
		contract.EnvSocket: filepath.Join(short, "s"), testkit.EnvScripts: short,
		// The shell of a pane is the system's own, and no pane is this test's.
		"SHELL": "", contract.EnvPane: "", contract.EnvActivePane: ""} {
		t.Setenv(name, value)
	}
	kit := contract.NewKit()
	platform.Plug(kit)
	pty.Plug(kit)
	terms, ctx := engine.New(kit), t.Context()
	root := filepath.Join(base, "project")
	if err = os.MkdirAll(root, 0o700); err == nil {
		// The agent works a moment on its prompt, rests until it is told
		// something, and then sits at its prompt for good.
		err = os.WriteFile(filepath.Join(short, "A.1"), []byte("sleep 0.5 | rest | hang"), 0o600)
	}
	if err != nil {
		t.Fatal(err)
	}
	bin, _ := exec.LookPath("whaleshark")
	agent, _ := exec.LookPath("fakeagent")

	var said bytes.Buffer
	keeper, let := leash.Command(bin, "engine", "run") // it ends with this test however the test ends
	keeper.Stdout, keeper.Stderr = &said, &said
	if err = keeper.Start(); err != nil {
		t.Fatal(err)
	}
	// A stop ends every pane's program before the keeper ends, so nothing
	// holds the login's folders when they are taken away.
	stop := sync.OnceFunc(func() {
		late := time.AfterFunc(patience, let)
		terms.Call(context.Background(), contract.WireCall{Op: contract.OpStop})
		keeper.Wait()
		late.Stop()
	})
	t.Cleanup(stop)
	until := func(ok func() bool) bool {
		for end := time.Now().Add(patience); !ok(); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(end) {
				return false
			}
		}
		return true
	}
	cells = map[string]string{}
	row := func(name string, err error, seen ...bool) {
		cell := "yes"
		if err != nil {
			t.Logf("%s: %v", name, err)
			cell = "no: " + contract.ErrCode(err)
		} else if slices.Contains(seen, false) {
			cell = "no: not seen"
		}
		if was := cells[name]; was == "" || was == "yes" {
			cells[name] = cell
		}
	}
	pane := func(id string) (found contract.Pane) {
		if s, _ := terms.Snapshot(ctx); s != nil {
			for _, p := range s.Panes {
				if p.ID == id {
					found = p
				}
			}
		}
		return found
	}
	shows := func(id, text string) bool {
		return until(func() bool { s, _ := terms.Screen(id); return strings.Contains(s, text) })
	}
	rests := func(id string) bool { return until(func() bool { return pane(id).Status == contract.StatusIdle }) }

	version := ""
	if !until(func() bool { version, err = terms.Version(); return err == nil }) {
		stop()
		t.Fatalf("the keeper did not start: %v\n%s", err, &said)
	}
	row("Version", err, version != "")
	first, events, err := terms.Events(ctx)
	if err != nil {
		t.Fatalf("the keeper gives no events: %v", err)
	}
	got := gather(first, events)

	a, err := terms.TabCreate("", "", root, "one", []string{contract.EnvAttempt + "=A.1"})
	row("TabCreate", err, a.Tab != "", pane(a.ID).Cwd == root)
	_, err = terms.Snapshot(ctx)
	row("Snapshot", err, pane(a.ID).Label == "one")
	sh, err := terms.Split(a.ID, contract.Right, 0.5)
	row("Split", err, pane(sh.ID).Tab == a.Tab)
	// Above the new pane one more, five cells high whatever the window's size.
	up, err := terms.Split(sh.ID, contract.Up, 5)
	row("Split", err, pane(up.ID).Tab == a.Tab)
	w, h, err := terms.Size(sh.ID)
	row("Size", err, w > 0, h > 0)
	row("TabRename", terms.TabRename(a.Tab, "renamed"), pane(sh.ID).Label == "renamed")
	b, err := terms.TabCreate("", "", root, "two", nil)
	row("TabCreate", err)
	row("TabFocus", terms.TabFocus(b.Tab), pane(b.ID).Focused)
	to, err := terms.PaneFocus(a.ID, "")
	row("PaneFocus", err, to == a.ID, pane(a.ID).Focused)
	to, err = terms.PaneFocus(a.ID, contract.Right)
	row("PaneFocus", err, to == sh.ID)
	row("Swap", terms.Swap(a.ID, sh.ID))
	row("Resize", terms.Resize(a.ID, contract.Right, 0.1))
	// A shell at its prompt is at rest, by the program in front in its
	// terminal. This cell is the word the pane reads: a system that cannot
	// tell which program is in front has another.
	rests(sh.ID)
	cells[rowShell] = pane(sh.ID).Status
	reason, _, err := terms.Notify("matrix", "with no window to show it", false)
	row("Notify", err, reason == contract.NotifyNoWindow)

	// The window is the real one, in a terminal of the engine's own making.
	// It is opened in the made-up project's folder, as a person opens one in
	// theirs: from the folder of this test the program would look upward and
	// could find a project that lies round the whole tree, which is not this
	// login's and is refused.
	env := append(slices.DeleteFunc(os.Environ(), func(v string) bool { return strings.HasPrefix(v, "TERM=") }), "TERM=xterm-256color")
	window, err := kit.Pty(contract.PtySpec{Argv: []string{bin, "open"}, Dir: root, Env: env, Cols: 120, Rows: 40})
	if err == nil {
		go io.Copy(io.Discard, window)
		defer window.Close()
		attached := until(func() bool {
			r, _ := terms.Call(ctx, contract.WireCall{Op: contract.OpStatus})
			return strings.Contains(r.Text, " 1 windows")
		})
		reason, _, err = terms.Notify("matrix", "in a window", false)
		w, h, _ = terms.Size(up.ID)
		row(rowWindow, err, attached, reason == contract.NotifyShown, w < 120, h == 5)
	} else {
		row(rowWindow, err)
	}

	row("AgentStart", terms.AgentStart("ann", "claude", a.ID, nil, patience), pane(a.ID).Agent == "claude")
	_, err = terms.Screen(a.ID)
	row("Screen", err, shows(a.ID, "fakeagent: ready"))
	row("Prompt", terms.Prompt("ann", "Read and follow "+filepath.Join(root, "prompt.md"), patience))
	rests(a.ID)
	row("Point", terms.Point(a.ID, contract.PointMail, ""), shows(a.ID, "fakeagent: told:"))
	// What an agent's own hooks say of its session and its folder is an
	// event of its own kind when nothing else changed with it.
	for n, kind := range []string{contract.EvSession, contract.EvFolder} {
		tries := 0
		until(func() bool {
			tries++
			word := strconv.Itoa(tries)
			err = terms.Hook("Stop", a.ID, []string{"session-" + word, ""}[n], []string{"", filepath.Join(base, word)}[n])
			return err != nil || got.saw(kind)
		})
		row(rowHook, err)
	}

	row("Run", terms.Run(b.ID, []string{bin, "version"}), until(func() bool { return pane(b.ID).ID == "" }))
	err = terms.Overlay([]string{agent}, 0.5, 0.5)
	over := ""
	if s, _ := terms.Snapshot(ctx); s != nil {
		for _, p := range s.Panes {
			if p.ID != a.ID && p.ID != sh.ID && p.ID != up.ID {
				over = p.ID
			}
		}
	}
	row("Overlay", err, over != "")
	over = cmp.Or(over, sh.ID)
	row("PaneClose", terms.PaneClose(over), pane(over).ID == "")
	if window != nil {
		// The command key as it comes, then a full stop, typed into the
		// window: the menu of the tab that shows is asked for. The left
		// section answers with that tab's menu in a pane over the tab,
		// which is closed again, so that the picture is at rest.
		window.Write([]byte{0, '.'})
		until(func() bool { return got.saw(contract.EvMenu) })
		menu := ""
		until(func() bool {
			if s, _ := terms.Snapshot(ctx); s != nil {
				for _, p := range s.Panes {
					if p.Ours && !p.Dock {
						menu = p.ID
					}
				}
			}
			return menu != ""
		})
		if menu != "" {
			terms.PaneClose(menu)
			until(func() bool { return pane(menu).ID == "" })
		}
	}
	dirs, _ := kit.Platform.Dirs()
	err = terms.SetKeys([]contract.KeyEntry{{Key: "shift+a", Type: "shell", Argv: []string{"whaleshark", "pause"}}})
	settings, _ := kit.Platform.Read(contract.SettingsPath(dirs))
	row("SetKeys", err, strings.Contains(string(settings), "shift+a"))
	row("TabClose", terms.TabClose(a.Tab), pane(a.ID).ID == "")

	// The events alone brought the first picture to the last one.
	last, err := terms.Snapshot(ctx)
	same := err == nil && until(func() bool {
		got.mu.Lock()
		defer got.mu.Unlock()
		return got.picture.Seq >= last.Seq
	})
	got.mu.Lock()
	if same = same && got.ordered && reflect.DeepEqual(got.picture.Panes, last.Panes); !same && last != nil {
		t.Logf("the events came to\n%+v\nand the picture is\n%+v", got.picture.Panes, last.Panes)
	}
	got.mu.Unlock()
	stop()
	ended := until(func() bool {
		select {
		case <-got.ended:
			return true
		default:
			return false
		}
	})
	row("Events", nil, same, ended)
	_, kinds := names(t)
	for _, kind := range kinds {
		row(kind, nil, got.saw(kind))
	}
	return cells, kit.Platform.System()
}

// Every call of the interface and every kind of event, on this system: the
// table has a row for each, and each row says what this run came to.
func TestEveryCallAndEveryEvent(t *testing.T) {
	got, system := column(t)
	calls, kinds := names(t)
	rows := slices.Concat(calls, kinds, []string{rowShell, rowWindow, rowHook})
	want := table(t, system)
	for row := range want {
		if !slices.Contains(append(rows, rowSSH), row) {
			t.Errorf("the table has a row %q that is no call and no event", row)
		}
	}
	held(t, want, got, rows)
}

// The whole column again by a program that sshd started: a server program
// of this test's own on the loopback, with keys of its own, as this login.
// A machine with no sshd cannot say, which on a hosted machine is a fault.
func TestTheColumnAgainOverSSH(t *testing.T) {
	kit := contract.NewKit()
	platform.Plug(kit)
	system := kit.Platform.System()
	sshd, err := exec.LookPath("sshd")
	if err != nil {
		sshd, err = exec.LookPath("/usr/sbin/sshd")
	}
	got := "yes"
	switch {
	case system == "windows":
		got = "not tried"
	case err != nil && os.Getenv("CI") == "":
		t.Skip("no sshd on this machine")
	case err != nil:
		got = "no: no sshd"
	default:
		if out, err := overSSH(t, kit, sshd); err != nil {
			t.Logf("%v\n%s", err, out)
			got = "no: failed"
		}
	}
	held(t, table(t, system), map[string]string{rowSSH: got}, []string{rowSSH})
}

func overSSH(t *testing.T, kit *contract.Kit, sshd string) (string, error) {
	dir := t.TempDir()
	host, id := filepath.Join(dir, "host"), filepath.Join(dir, "id")
	for _, key := range []string{host, id} {
		if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
			return string(out), err
		}
	}
	free, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return "", err
	}
	_, port, _ := net.SplitHostPort(free.Addr().String())
	free.Close()
	config := filepath.Join(dir, "sshd_config")
	lines := []string{"Port " + port, "ListenAddress localhost", "HostKey " + host, "AuthorizedKeysFile " + id + ".pub",
		"PidFile none", "StrictModes no", "UsePAM no", "PasswordAuthentication no"}
	if err = os.WriteFile(config, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return "", err
	}
	var said bytes.Buffer
	server := exec.Command(sshd, "-D", "-e", "-f", config)
	server.Stderr = &said
	if err = server.Start(); err != nil {
		return "", err
	}
	// What the server said is read once it has ended.
	stop := sync.OnceFunc(func() {
		server.Process.Kill()
		server.Wait()
	})
	defer stop()
	for end := time.Now().Add(patience); ; time.Sleep(50 * time.Millisecond) {
		if conn, err := net.Dial("tcp", net.JoinHostPort("localhost", port)); err == nil {
			conn.Close()
			break
		} else if time.Now().After(end) {
			stop()
			return said.String(), err
		}
	}
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	here, _ := os.Getwd()
	// sshd hands on none of this side's variables: the line carries the three it needs.
	line := kit.Platform.Quote(contract.ShellPosix, []string{"env", "PATH=" + os.Getenv("PATH"), "TMPDIR=" + os.TempDir(), envFar + "=" + here,
		self, "-test.run", "^TestEveryCallAndEveryEvent$", "-test.v"})
	// Nothing of the login's own ssh is read or written: no settings, no
	// agent, no list of known hosts.
	out, err := exec.Command("ssh", "-F", "none", "-p", port, "-i", id, "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes",
		"-o", "IdentityAgent=none", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR",
		"localhost", line).CombinedOutput()
	stop()
	return string(out) + said.String(), err
}
