package keeper

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/layout"
	"github.com/tgdigital-hub/whaleshark/internal/pty"
	"github.com/tgdigital-hub/whaleshark/internal/restore"
)

// standIn is the dock's program of the tests, in a process of its own: it
// lets the keeper's own line be read first, then prints one frame that names
// its process, and ends by itself when it is typed "bye" or its terminal ends.
func standIn() {
	time.Sleep(300 * time.Millisecond)
	fmt.Printf("side %d up\r\n", os.Getpid())
	for in := bufio.NewScanner(os.Stdin); in.Scan(); {
		if strings.TrimSpace(in.Text()) == "bye" {
			break
		}
	}
	os.Exit(0)
}

// sized waits until a pane has that size.
func (r *rig) sized(c *client, pane string, w, h int) {
	r.t.Helper()
	var got contract.WireReply
	for end := time.Now().Add(10 * time.Second); ; time.Sleep(time.Millisecond) {
		if got = c.call(contract.WireCall{Op: contract.OpSize, Pane: pane}); got.W == w && got.H == h {
			return
		} else if time.Now().After(end) {
			r.t.Fatalf("%s is %d by %d, not %d by %d", pane, got.W, got.H, w, h)
		}
	}
}

// cell is what a window shows in one cell.
func (o *outer) cell(x, y int) string {
	if lines := strings.Split(o.text(), "\n"); y < len(lines) && x < len([]rune(lines[y])) {
		return string([]rune(lines[y])[x])
	}
	return ""
}

func (r *rig) dockIs(ask func(d layout.Dock) bool) bool {
	return r.locked(func() bool { return ask(r.k.lay.Dock) })
}

// The dock is one pane in no tab, in every snapshot, started from the
// keeper's own program file and three words; it shows beside every tab at
// the width contract.Fit gives the window, at the edge the settings say.
func TestTheDockStandsBesideEveryTab(t *testing.T) {
	r := dockRig(t)
	c := r.dial()
	self, _ := r.kit.Platform.SelfPath()
	snap := c.call(contract.WireCall{Op: contract.OpSnapshot}).Snapshot
	if len(snap.Panes) != 1 || !snap.Panes[0].Dock || snap.Panes[0].Tab != "" || !snap.Panes[0].Ours || snap.Panes[0].ID != layout.DockPane {
		t.Fatalf("a keeper with no tab has these panes: %+v", snap.Panes)
	}
	if spec := r.program(layout.DockPane).spec; !slices.Equal(spec.Argv, []string{self, "ui", "run", "side"}) || value(spec.Env, contract.EnvPane) != layout.DockPane {
		t.Errorf("the dock's program was started as %q", spec.Argv)
	}
	w := r.window(120, 30)
	for _, at := range []struct{ cols, side, tab int }{{120, 22, 97}, {200, 28, 171}, {160, 28, 131}, {99, 14, 84}, {71, 4, 66}, {30, 4, 25}, {120, 22, 97}} {
		w.send(contract.WireCall{Op: contract.OpWindowSize, W: float64(at.cols), H: 30})
		r.sized(c, layout.DockPane, at.side, 30)
		r.sized(c, "p1", at.tab, 28)
	}
	// The frame of the last width, not one of a width before it that the
	// window still shows on a busy machine.
	r.until("the tab row to begin beside the dock", func() bool { return w.cell(24, 0)+w.cell(25, 0) == "Or" }) // the first tab is the Orchestrator's
	r.until("the dock's edge", func() bool { return w.cell(22, 0) == "│" && w.cell(22, 1) == "├" && w.cell(22, 29) == "│" })
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "two"})
	c.call(contract.WireCall{Op: contract.OpTabFocus, Tab: "t2"})
	r.sized(c, "p2", 97, 28)
	r.sized(c, layout.DockPane, 22, 30)

	path := contract.SettingsPath(r.k.dirs)
	r.until("the dock at the right", func() bool {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(path, []byte("[side]\nplace = \"right\"\nwidth = 18\n"), 0o600)
		time.Sleep(20 * time.Millisecond)
		return r.dockIs(func(d layout.Dock) bool { return d.Right && d.Width == 18 })
	})
	r.sized(c, layout.DockPane, 18, 30)
	r.sized(c, "p2", 101, 28)
	r.until("the edge at the right", func() bool { return w.cell(101, 0) == "│" && w.cell(101, 1) == "┤" })
	if x, _ := w.shows(contract.MainTab); x != 1 {
		t.Errorf("with the dock at the right the tab row begins at %d", x)
	}
}

// The dock runs nothing but ours and cannot be lost: every call that would
// close, split, swap, move or start a program in it is refused for every
// caller, and nothing of it is in the layout file.
func TestTheDockRunsNothingButOurs(t *testing.T) {
	r := dockRig(t)
	c := r.dial()
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "one"})
	was := r.program(layout.DockPane)
	dock := layout.DockPane
	for _, call := range []contract.WireCall{
		{Op: contract.OpPaneClose, Pane: dock}, {Op: contract.OpSplit, Pane: dock, Dir: contract.Right},
		{Op: contract.OpSwap, Pane: dock, Target: "p1"}, {Op: contract.OpSwap, Pane: "p1", Target: dock},
		{Op: contract.OpResize, Pane: dock, Dir: contract.Right, Ratio: 0.1}, {Op: contract.OpPaneFocus, Pane: dock},
		{Op: contract.OpRun, Pane: dock, Argv: []string{"a-program"}},
		{Op: contract.OpAgentStart, Pane: dock, Kind: "claude", Name: "quiet otter"},
		{Op: contract.OpPoint, Pane: dock, Text: "a"}, {Op: contract.OpHook, Pane: dock, Kind: "Stop"},
	} {
		if reply := c.call(call); reply.Err != contract.ErrCode(contract.ErrDock) {
			t.Errorf("%s on the dock: %+v", call.Op, reply)
		}
	}
	// The pane has read its program's line by now. The line begins with this
	// program's own path, as long as the system makes it, and breaks wherever
	// the screen's width falls: at a blank too, which a row does not end in.
	r.settle()
	if reply := c.call(contract.WireCall{Op: contract.OpScreen, Pane: dock}); !strings.Contains(strings.Join(strings.Fields(reply.Text), ""), "uirunside") {
		t.Errorf("the dock's screen is not read: %q %s", reply.Text, reply.Err)
	}
	if r.program(dock) != was || !slices.Equal(was.spec.Argv[1:], []string{"ui", "run", "side"}) || !r.locked(func() bool { return len(r.k.panes) == 2 }) {
		t.Error("a refused call changed the dock")
	}
	r.k.stop()
	if data, err := os.ReadFile(restore.Path(r.k.dirs)); err != nil || strings.Contains(string(data), dock) || !strings.Contains(string(data), `"p1"`) {
		t.Errorf("the layout file after a stop: %v\n%s", err, data)
	}
	r.again()
	if spec := r.program(dock).spec; !slices.Equal(spec.Argv[1:], []string{"ui", "run", "side"}) {
		t.Errorf("after a restart the dock's program is %q", spec.Argv)
	}
	if snap := r.dial().call(contract.WireCall{Op: contract.OpSnapshot}).Snapshot; len(snap.Panes) != 2 {
		t.Errorf("after a restart: %+v", snap.Panes)
	}
}

// A click in the dock goes to its program and moves no keys; the command
// key and g gives the dock the keys and takes them back, b folds it to its
// rail, s opens the settings screen; and the three words do the same for a
// program, but for an agent from its own pane.
func TestTheDocksKeysAndAClickInIt(t *testing.T) {
	r := dockRig(t)
	c := r.dial()
	w := r.window(120, 30)
	r.sized(c, "p1", 97, 28)
	dock := r.program(layout.DockPane)
	dock.print("\x1b[?1000h\x1b[?1006h")
	r.settle()
	w.types("ab")
	r.got("p1", "ab")
	w.click(3, 5)
	r.got(layout.DockPane, "\x1b[<0;4;6M\x1b[<0;4;6m")
	w.types("cd")
	r.got("p1", "abcd")
	if r.focused() != "p1" || r.dockIs(func(d layout.Dock) bool { return d.Keys }) {
		t.Error("a click in the dock moved the keys")
	}

	w.types(cmd + "g")
	w.types("xy")
	r.got(layout.DockPane, "\x1b[<0;4;6M\x1b[<0;4;6mxy")
	w.types(cmd + "g")
	w.types("e")
	r.got("p1", "abcde")
	w.types(cmd + "g")
	r.until("the keys in the dock", func() bool { return r.dockIs(func(d layout.Dock) bool { return d.Keys }) })
	w.click(60, 10) // a click anywhere else takes them back
	r.until("the keys back in the tab", func() bool { return !r.dockIs(func(d layout.Dock) bool { return d.Keys }) })

	w.types(cmd + "b")
	r.sized(c, layout.DockPane, contract.SideRail, 30)
	r.sized(c, "p1", 115, 28)
	w.types(cmd + "b")
	r.sized(c, layout.DockPane, 22, 30)

	self, _ := r.kit.Platform.SelfPath()
	w.types(cmd + "s")
	r.until("the settings screen over the tab", func() bool {
		return r.locked(func() bool {
			return r.k.over != nil && slices.Equal(r.k.over.argv, []string{self, "ui", "run", "settings"})
		})
	})
	if at := r.locked(func() bool { return r.k.overAt().W == 120*8/10-2 }); !at {
		t.Error("the settings screen is not four fifths of the window")
	}

	for _, told := range []struct {
		word string
		want layout.Dock
	}{{contract.DockKeys, layout.Dock{Keys: true, Open: true}}, {contract.DockBack, layout.Dock{}},
		{contract.DockFold, layout.Dock{Fold: true}}, {contract.DockKeys, layout.Dock{Fold: true, Keys: true, Open: true}},
		{contract.DockFold, layout.Dock{Keys: true, Open: true}}, {contract.DockBack, layout.Dock{}}} {
		reply := c.call(contract.WireCall{Op: contract.OpDock, Dir: told.word})
		if got := r.dockIs(func(d layout.Dock) bool {
			return d.Keys == told.want.Keys && d.Open == told.want.Open && d.Fold == told.want.Fold
		}); reply.Err != "" || !got {
			t.Errorf("dock %s: %+v", told.word, reply)
		}
	}
	if reply := c.call(contract.WireCall{Op: contract.OpDock, Dir: "close"}); reply.Err == "" {
		t.Error("the dock was told a word it does not know, and took it")
	}
	r.locked(func() bool { r.k.panes["p1"].rec.Name = "quiet otter"; return true })
	if reply := c.call(contract.WireCall{Op: contract.OpDock, Dir: contract.DockKeys, From: "p1"}); !strings.Contains(reply.Message, "not a call for an agent") {
		t.Errorf("an agent moved the person's keys to the dock: %+v", reply)
	}
}

// In a window too narrow the dock is a rail, which opens at its set width
// over the tab while the pointer is in it or it has the keys, and closes
// when a row is chosen: the tab keeps its size all the while.
func TestARailOpensOverTheTab(t *testing.T) {
	r := dockRig(t)
	c := r.dial()
	w := r.window(60, 20)
	move := func(x, y int) { w.types(fmt.Sprintf("\x1b[<35;%d;%dM", x+1, y+1)) }
	r.sized(c, layout.DockPane, contract.SideRail, 20)
	r.sized(c, "p1", 55, 18)
	move(1, 5)
	r.sized(c, layout.DockPane, contract.SideWidth, 20)
	r.until("the open rail's edge over the tab", func() bool { return w.cell(contract.SideWidth, 5) == "│" && w.cell(contract.SideRail, 5) != "│" })
	move(20, 5) // still inside the open rail
	move(21, 6)
	r.sized(c, layout.DockPane, contract.SideWidth, 20)
	move(40, 5)
	r.sized(c, layout.DockPane, contract.SideRail, 20)
	r.until("the rail's edge back", func() bool { return w.cell(contract.SideRail, 5) == "│" })

	w.types(cmd + "g")
	r.sized(c, layout.DockPane, contract.SideWidth, 20)
	c.call(contract.WireCall{Op: contract.OpTabFocus, Tab: "t1"})
	r.sized(c, layout.DockPane, contract.SideRail, 20)
	r.sized(c, "p1", 55, 18)
}

// A drag of the dock's edge sets its width, within the limits, and its end
// writes side.width to the person's settings file.
func TestADragOfTheEdgeSetsTheWidth(t *testing.T) {
	r := dockRig(t)
	c := r.dial()
	w := r.window(200, 30)
	r.sized(c, layout.DockPane, contract.SideWidth, 30)
	for _, to := range []struct{ x, width int }{{40, 40}, {3, contract.SideMin}, {190, contract.SideMax}, {33, 33}} {
		edge := 0
		r.locked(func() bool { edge = r.k.place(r.k.panes[layout.DockPane]).W; return true })
		w.drag(edge, 10, to.x, 12)
		r.sized(c, layout.DockPane, to.width, 30)
		r.sized(c, "p1", 199-to.width, 28)
		r.until("the width in the settings file", func() bool {
			s, err := contract.ReadSettings(r.kit.Platform.Peek, r.k.dirs)
			return err == nil && s.Side.Width == to.width
		})
	}
}

// While its program is not up the dock's place is the keeper's to draw: the
// title and one dim line. The program is tried again at once, and after
// three ends within ten seconds no more often than every five.
func TestTheKeeperDrawsThePlaceWhileNoProgramIsUp(t *testing.T) {
	r := dockRig(t)
	c, watch := r.dial(), r.dial()
	watch.send(contract.WireCall{Op: contract.OpEvents})
	first := watch.reply().Snapshot
	w := r.window(120, 30)
	r.sized(c, "p1", 97, 28)
	r.program(layout.DockPane).print("\r\nSIDE UP\r\n")
	w.shows("SIDE UP")
	r.down.Store(true)
	tried := r.sides.Load()
	r.program(layout.DockPane).Close()
	if x, y := w.shows(contract.SideTitle); x != 1 || y != 0 {
		t.Errorf("the title is at %d,%d", x, y)
	}
	if x, y := w.shows("starting…"); x != 1 || y != 2 {
		t.Errorf("the keeper's line is at %d,%d", x, y)
	}
	if strings.Contains(w.text(), "SIDE UP") {
		t.Errorf("the ended program's picture still shows:\n%s", w.text())
	}
	w.types("still here")
	r.got("p1", "still here")
	time.Sleep(1500 * time.Millisecond)
	if n := r.sides.Load() - tried; n != 2 {
		t.Errorf("the program was tried %d times in the second and a half after three ends in a row, want 2", n)
	}
	if snap := c.call(contract.WireCall{Op: contract.OpSnapshot}).Snapshot; !slices.ContainsFunc(snap.Panes, func(p contract.Pane) bool { return p.Dock }) {
		t.Error("the dock is not in the snapshot while its program is down")
	}
	r.down.Store(false)
	began := time.Now()
	r.until("the program to be tried again", func() bool { return r.sides.Load()-tried == 3 })
	if d := time.Since(began); d < 2*time.Second || d > 5*time.Second {
		t.Errorf("the next try came %v later, want five seconds after the third end", d+1500*time.Millisecond)
	}
	r.program(layout.DockPane).print("\r\nSIDE BACK\r\n")
	w.shows("SIDE BACK")
	// The events alone say that the dock's terminal is another now.
	last := c.call(contract.WireCall{Op: contract.OpSnapshot}).Snapshot
	for first.Seq < last.Seq {
		contract.Apply(first, *watch.reply().Event)
	}
	if !reflect.DeepEqual(first.Panes, last.Panes) || first.Panes[0].Terminal == "one.1" {
		t.Errorf("the events came to %+v, and the picture is %+v", first.Panes, last.Panes)
	}
}

var sideUp = regexp.MustCompile(`side (\d+) up`)

// The scenario: the dock's program, a real one in a real terminal, is ended
// ten times, by a kill and by its own exit in turn. Each time the keeper's
// own line is read in the place and then the program's first frame, within
// two seconds; the third end, which is the third within ten seconds, is
// answered after five.
func TestTheDocksProgramEndedTenTimes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("not tried on Windows, where a console ends with its program in another way")
	}
	r := rigOf(t, true, pty.Start)
	t.Setenv("SHELL", "/bin/sh")
	c := r.dial()
	w := r.attach(120, 30)
	go w.read()
	up := func() (pid int) {
		t.Helper()
		r.until("the program's first frame", func() bool {
			m := sideUp.FindStringSubmatch(w.text())
			if m != nil {
				pid, _ = strconv.Atoi(m[1])
			}
			return m != nil
		})
		return pid
	}
	c.call(contract.WireCall{Op: contract.OpDock, Dir: contract.DockKeys})
	last := 0
	for n := 1; n <= 10; n++ {
		pid := up()
		if pid == last {
			t.Fatalf("end %d: the same program is still there", n-1)
		}
		last = pid
		if n == 4 {
			// From here on the pace is the test's: ten ends at five seconds
			// each would say nothing more.
			r.locked(func() bool { dockEnds, dockWait = time.Millisecond, time.Millisecond; return true })
		}
		began := time.Now()
		if n%2 == 1 {
			p, _ := os.FindProcess(pid)
			if err := p.Kill(); err != nil {
				t.Fatal(err)
			}
		} else {
			w.types("bye\r")
		}
		w.shows("starting…")
		if strings.Contains(w.text(), "side "+strconv.Itoa(pid)) {
			t.Errorf("end %d: the ended program's frame still shows", n)
		}
		if line := time.Since(began); line > 2*time.Second {
			t.Errorf("end %d: the keeper's own line came after %v", n, line)
		}
		up()
		switch back := time.Since(began); {
		case n == 3 && (back < 4*time.Second || back > 8*time.Second):
			t.Errorf("the third end within ten seconds was answered after %v, want five seconds", back)
		case n != 3 && back > 2*time.Second:
			t.Errorf("end %d: the program was back after %v", n, back)
		}
	}
	if snap := c.call(contract.WireCall{Op: contract.OpSnapshot}).Snapshot; len(snap.Panes) != 2 || !snap.Panes[0].Dock {
		t.Errorf("after ten ends: %+v", snap.Panes)
	}
	if text := c.call(contract.WireCall{Op: contract.OpScreen, Pane: "p1"}).Text; strings.Contains(text, "bye") {
		t.Errorf("what was typed for the dock reached the tab's pane:\n%s", text)
	}
	t.Cleanup(func() { dockEnds, dockWait = 10*time.Second, 5*time.Second })
}

// An agent is started from its tool's description, which the keeper reads
// itself: the program and the variables of the login's own file, and no
// program for a kind nobody wired in.
func TestAnAgentIsStartedFromItsToolsDescription(t *testing.T) {
	r := newRig(t)
	c := r.dial()
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "work"})
	tool := filepath.Join(t.TempDir(), "hermes")
	file := filepath.Join(r.k.dirs.Config, contract.ToolsDir, "hermes.toml")
	text := fmt.Sprintf("label = \"Hermes\"\nrun = [%q, \"chat\"]\nhooks = \"none\"\n[env]\nMODEL_AT = \"localhost\"\n", tool)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if reply := c.call(contract.WireCall{Op: contract.OpAgentStart, Pane: "p1", Kind: "hermes", Name: "quiet otter", Argv: []string{"--fast"}, Millis: 9000}); reply.Err != "" {
		t.Fatalf("agent-start: %+v", reply)
	}
	spec := r.program("p1").spec
	if !slices.Equal(spec.Argv, []string{tool, "chat", "--fast"}) || value(spec.Env, "MODEL_AT") != "localhost" || value(spec.Env, contract.EnvPane) != "p1" {
		t.Errorf("the agent was started as %q", spec.Argv)
	}
	c.call(contract.WireCall{Op: contract.OpTabCreate, Label: "other"})
	was := r.program("p2")
	if reply := c.call(contract.WireCall{Op: contract.OpAgentStart, Pane: "p2", Kind: "a-program", Name: "calm heron", Argv: []string{"--fast"}}); reply.Message != "no program to run" {
		t.Errorf("a kind nobody wired in: %+v", reply)
	}
	if r.program("p2") != was || value(was.spec.Env, "MODEL_AT") != "" {
		t.Error("a refused start changed the pane, or a tool's variables reached another pane")
	}
}
