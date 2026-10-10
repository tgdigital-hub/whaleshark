package keeper

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/screen"
)

// reached is a window that says how it got to the keeper.
func (r *rig) reached(w, h int, reach string) *outer {
	r.t.Helper()
	o := &outer{client: r.dial(), r: r, scr: screen.New(w, h)}
	o.send(contract.WireCall{Op: contract.OpAttach, W: float64(w), H: float64(h), System: "darwin", Reach: reach,
		Env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	go o.read()
	return o
}

// copied waits until the clipboard's program was given that, and forgets it.
func (r *rig) copied(want string) {
	r.t.Helper()
	r.until("the clipboard to be given "+want, func() bool {
		boardMu.Lock()
		defer boardMu.Unlock()
		ok := slices.Contains(board, want)
		if ok {
			board = nil
		}
		return ok
	})
}

func (o *outer) drag(x0, y0, x1, y1 int) {
	o.types(fmt.Sprintf("\x1b[<0;%d;%dM\x1b[<32;%d;%dM\x1b[<0;%d;%dm", x0+1, y0+1, x1+1, y1+1, x1+1, y1+1))
}

const cmd = "\x00" // the command key as a terminal sends it: Ctrl+Space

// The keys of a window: each goes to the program in the form that program
// asked for, the command key and the key after it are the engine's, and a
// program that asked is told when the keys come and go.
func TestTheKeysAreWrittenAsAskedAndTheCommandKeyIsTheEngines(t *testing.T) {
	r := newRig(t)
	w := r.window(100, 30)
	w.shows("pane=p1")
	f := r.program("p1")
	w.types("\x1b[A\x1b[200~a\nb\x1b[201~")
	r.got("p1", "\x1b[Aa\rb")
	f.print("\x1b[?1h\x1b[?2004h\x1b[?1004h")
	r.settle()
	w.types("\x1b[A\x1b[200~a\nb\x1b[201~")
	r.got("p1", "\x1b[Aa\rb\x1bOA\x1b[200~a\nb\x1b[201~")

	// A split by key: the new pane has the keys, and the old one is told.
	w.types(cmd + "r")
	r.until("the keys in the pane made by a key", func() bool { return r.focused() == "p2" })
	r.got("p1", "\x1b[Aa\rb\x1bOA\x1b[200~a\nb\x1b[201~\x1b[O")
	w.types("x" + cmd + "\x1b[D")
	r.until("the keys back on the left", func() bool { return r.focused() == "p1" })
	r.got("p2", "x")
	if got := r.program("p1").got(); !strings.HasSuffix(got, "\x1b[O\x1b[I") {
		t.Errorf("the pane that got the keys back was typed %q", got)
	}
	// A new tab, the one before it, and its number; the command key twice is the key.
	w.types(cmd + "t")
	r.until("the keys in the new tab", func() bool { return r.focused() == "p3" })
	w.shows("pane=p3")
	w.types(cmd + "p")
	r.until("the tab before", func() bool { return r.focused() == "p1" })
	w.types(cmd + "2" + cmd + cmd + cmd + "?")
	r.got("p3", cmd)
	// One pane zoomed to the whole tab, and closed.
	w.types(cmd + "1" + cmd + "z")
	r.until("the zoom", func() bool { return r.locked(func() bool { return r.k.lay.Tabs[0].Zoom == "p1" }) })
	w.types(cmd + "z" + cmd + "\x1b[C" + cmd + "w")
	r.until("the pane closed by a key", func() bool { return r.locked(func() bool { return r.k.panes["p2"] == nil }) })
}

// Scroll-back with the wheel, a selection that stays in its pane, and the
// copy on letting go: by the system's program only for a window that said
// it is on the keeper's own computer, and by asking the terminal for any other.
func TestSelectingCopiesByTheRouteOfTheWindow(t *testing.T) {
	r := newRig(t)
	c := r.dial()
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "one"})
	c.call(contract.WireCall{Op: contract.OpSplit, Pane: "p1", Dir: contract.Right, Ratio: 0.5})
	w := r.reached(100, 30, contract.ReachLocal)
	w.shows("pane=p2")
	f := r.program("p1")
	for i := 1; i <= 60; i++ {
		f.print(fmt.Sprintf("\r\nreef line %d of the left pane", i))
	}
	r.settle()
	x, y := w.shows("reef line 60")
	w.drag(x, y, 99, y)
	r.copied("pbcopy: reef line 60 of the left pane")
	w.shows("copied")

	// The wheel goes back, and a key returns to the live picture.
	w.types(fmt.Sprintf("\x1b[<64;%d;%dM", x+1, y+1))
	w.shows("3 back")
	w.shows("reef line 30")
	w.types("k")
	r.got("p1", "k")
	r.until("the live picture", func() bool { return !strings.Contains(w.text(), " back ") })

	// Selecting by keys: the command key and v, a line down, and y copies.
	w.types(cmd + "v")
	w.shows("select")
	w.types("0v$y")
	r.copied("pbcopy: reef line 60 of the left panek")
	if got := r.program("p1").got(); got != "k" {
		t.Errorf("the keys of a selection reached the program: %q", got)
	}

	// A window that does not say how it came is asked nothing of this
	// computer: its own terminal is sent the clipboard sequence.
	w.conn.Close()
	far := r.window(100, 30)
	x, y = far.shows("reef line 60")
	far.drag(x, y, x+8, y)
	far.shows("sent to your terminal")
	r.until("the sequence at the far terminal", func() bool {
		far.mu.Lock()
		defer far.mu.Unlock()
		for _, ev := range far.scr.Events() {
			if ev.Kind == screen.Copy && ev.Text == "reef line" {
				return true
			}
		}
		return false
	})
	boardMu.Lock()
	defer boardMu.Unlock()
	if len(board) > 0 {
		t.Errorf("a window that did not say where it is reached the clipboard's program: %q", board)
	}
}

// A program's own wish to copy waits for the person's yes.
func TestAProgramsCopyWaitsForThePerson(t *testing.T) {
	r := newRig(t)
	w := r.reached(100, 30, contract.ReachLocal)
	w.shows("pane=p1")
	r.program("p1").print("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("a secret")) + "\a")
	w.shows("wants to copy 1 lines: allow?")
	boardMu.Lock()
	if len(board) > 0 {
		t.Errorf("copied before the person said yes: %q", board)
	}
	boardMu.Unlock()
	w.types(cmd + "y")
	r.copied("pbcopy: a secret")
}

// A pane drawn over the tab has every key until its program ends.
func TestAPaneOverTheTab(t *testing.T) {
	r := newRig(t)
	c := r.dial()
	w := r.window(100, 30)
	w.shows("pane=p1")
	if reply := c.call(contract.WireCall{Op: contract.OpOverlay, Argv: []string{"whaleshark", "ui", "run", "list"}, W: 80, H: 10, From: "p1"}); reply.Err != "" {
		t.Fatalf("overlay: %+v", reply)
	}
	x, y := w.shows("┌")
	if spec := r.program("p2").spec; spec.Cols != 80 || spec.Rows != 10 {
		t.Errorf("the program over the tab was started at %d by %d", spec.Cols, spec.Rows)
	}
	w.types("j" + cmd + "t")
	r.got("p2", "j"+cmd+"t")
	if env := r.program("p2").spec.Env; value(env, contract.EnvActivePane) != "p1" {
		t.Errorf("the program over the tab is not told the pane with the keys: %q", env)
	}
	w.click(x+2, y+2)
	r.program("p2").Close()
	r.until("the pane over the tab to go", func() bool { return r.locked(func() bool { return r.k.over == nil && len(r.k.panes) == 1 }) })
	w.types("back")
	r.got("p1", "back")
	if r.focused() != "p1" {
		t.Errorf("the keys are in %q after the pane over the tab", r.focused())
	}
}

// An agent's state comes from its hooks and its screen, and a prompt is
// typed only at an agent that shows no question.
func TestAnAgentIsStartedWatchedAndTypedAtSafely(t *testing.T) {
	r := newRig(t)
	c, watch := r.dial(), r.dial()
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "work", Cwd: "/work"})
	watch.send(contract.WireCall{Op: contract.OpEvents})
	watch.reply()
	state := func(want string) contract.Pane {
		t.Helper()
		for {
			if ev := watch.reply().Event; ev != nil && ev.Pane.ID == "p1" && ev.Pane.Status == want {
				return ev.Pane
			}
		}
	}
	hook := func(event string) {
		r.dial().call(contract.WireCall{Op: contract.OpHook, Kind: event, Pane: "p1", Session: "s-1", Cwd: "/work/tree"})
	}
	go func() {
		r.until("the agent's program", func() bool { return slices.Contains(r.program("p1").spec.Argv, "--model") })
		hook("SessionStart")
	}()
	if reply := c.call(contract.WireCall{Op: contract.OpAgentStart, Name: "quiet otter", Kind: "claude", Pane: "p1", Argv: []string{"--model", "small"}, Millis: 5000}); reply.Err != "" {
		t.Fatalf("agent-start: %+v", reply)
	}
	if got := r.program("p1").spec.Argv; !slices.Equal(got, []string{"claude", "--model", "small"}) {
		t.Errorf("the agent was started as %q", got)
	}
	if p := state(contract.StatusIdle); p.Agent != "claude" || p.Name != "quiet otter" || p.Session != "s-1" || p.Cwd != "/work/tree" {
		t.Errorf("the agent's record once it is ready: %+v", p)
	}

	// A prompt: typed, then Enter, and answered when the agent works on it.
	go func() {
		r.got("p1", "read the brief\r")
		hook("UserPromptSubmit")
	}()
	if reply := c.call(contract.WireCall{Op: contract.OpPrompt, Name: "quiet otter", Text: "read the brief", Millis: 5000}); reply.Err != "" {
		t.Errorf("prompt: %+v", reply)
	}
	state(contract.StatusWorking)

	// A question on its screen: nothing is typed, whatever the hooks said.
	r.program("p1").print("\r\nDo you want to proceed?\r\n 1. Yes\r\n Esc to cancel\r\n")
	state(contract.StatusBlocked)
	for _, call := range []contract.WireCall{{Op: contract.OpPrompt, Name: "quiet otter", Text: "more", Millis: 500},
		{Op: contract.OpPoint, Pane: "p1", Pointer: contract.PointMail}} {
		if reply := c.call(call); reply.Err != contract.ErrAgentBlocked.Error() {
			t.Errorf("%s at an agent that shows a question: %+v", call.Op, reply)
		}
	}
	if got := r.program("p1").got(); got != "read the brief\r" {
		t.Errorf("the agent at a question was typed %q", got)
	}
	if reply := c.call(contract.WireCall{Op: contract.OpPrompt, Name: "nobody", Text: "x"}); reply.Err != contract.ErrNoPane.Error() {
		t.Errorf("a prompt for an agent that is not there: %+v", reply)
	}
}

// again stops the keeper as an upgrade does and starts one in its place.
func (r *rig) again() {
	r.t.Helper()
	r.k.stop()
	k, err := open(r.kit)
	if err != nil {
		r.t.Fatal(err)
	}
	k.version = "test"
	r.k = k
	go k.serve()
	r.t.Cleanup(k.stop)
}

// After a stop and a start the same tabs and panes are back under their
// ids: an agent resumed in its session with the arguments it had, a pane
// program of ours started as it was, a shell over its old lines.
func TestEverythingComesBackAfterARestart(t *testing.T) {
	r := newRig(t)
	c := r.dial()
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "lead", Cwd: "/work", Env: []string{"COLOUR=coral"}})
	c.call(contract.WireCall{Op: contract.OpSplit, Pane: "p1", Dir: contract.Right, Ratio: 36})
	c.call(contract.WireCall{Op: contract.OpRun, Pane: "p2", Argv: []string{"whaleshark", "ui", "run", "fleet"}})
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "sign-up page", Cwd: "/work/tree"})
	c.call(contract.WireCall{Op: contract.OpAgentStart, Name: "quiet otter", Kind: "claude", Pane: "p3", Argv: []string{"--model", "small"}})
	c.call(contract.WireCall{Op: contract.OpHook, Kind: "SessionStart", Pane: "p3", Session: "s-1"})
	r.until("the session in the record", func() bool { return r.locked(func() bool { return r.k.panes["p3"].rec.Session == "s-1" }) })
	w := r.window(120, 40)
	w.shows("whaleshark fleet")
	r.program("p1").print("\r\nwhat the shell said before")
	r.settle()
	before := c.call(contract.WireCall{Op: contract.OpSnapshot}).Snapshot

	r.again()
	c = r.dial()
	after := c.call(contract.WireCall{Op: contract.OpSnapshot}).Snapshot
	if after.Instance == before.Instance || len(after.Panes) != 3 {
		t.Fatalf("after the restart: %+v", after)
	}
	for i, p := range after.Panes {
		was := before.Panes[i]
		if p.ID != was.ID || p.Tab != was.Tab || p.Label != was.Label || p.Cwd != was.Cwd || p.Agent != was.Agent ||
			p.Name != was.Name || p.Session != was.Session || p.Ours != was.Ours || p.Terminal == was.Terminal {
			t.Errorf("%s came back as %+v, was %+v", was.ID, p, was)
		}
	}
	for pane, want := range map[string][]string{"p1": {"the-shell"}, "p2": {"whaleshark", "ui", "run", "fleet"},
		"p3": {"claude", "--resume", "s-1", "--model", "small"}} {
		if got := r.program(pane).spec; !slices.Equal(got.Argv, want) {
			t.Errorf("%s was started again as %q, want %q", pane, got.Argv, want)
		}
	}
	if spec := r.program("p2").spec; spec.Cols != 36 || value(r.program("p1").spec.Env, "COLOUR") != "coral" {
		t.Errorf("the size or the tab's variables did not come back: %+v", spec)
	}
	text := c.call(contract.WireCall{Op: contract.OpScreen, Pane: "p1"}).Text
	if a, b := strings.Index(text, "what the shell said before"), strings.Index(text, "-- restarted "); a < 0 || b < a {
		t.Errorf("the old lines and the line of the restart:\n%s", text)
	}
	// The option to resume is not kept: a second restart resumes once.
	r.again()
	if got := r.program("p3").spec.Argv; !slices.Equal(got, []string{"claude", "--resume", "s-1", "--model", "small"}) {
		t.Errorf("the agent was resumed the second time as %q", got)
	}
}

// The settings file is noticed while the keeper runs.
func TestAChangedSettingsFileIsNoticed(t *testing.T) {
	r := newRig(t)
	w := r.window(100, 30)
	w.shows("pane=p1")
	path := contract.SettingsPath(r.k.dirs)
	if err := errors.Join(os.MkdirAll(filepath.Dir(path), 0o700), os.WriteFile(path, []byte("[keys]\ncommand = \"ctrl+a\"\n"), 0o600)); err != nil {
		t.Fatal(err)
	}
	r.until("the new command key", func() bool {
		w.types("\x01t")
		time.Sleep(20 * time.Millisecond)
		return r.locked(func() bool { return len(r.k.lay.Tabs) > 1 })
	})
}
