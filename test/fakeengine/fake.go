// Package fakeengine is the keeper's double for tests: tabs, panes and agents
// held in memory, answering the keeper's own calls. The real adapter runs
// over it unchanged: in this process through a pipe, and from other
// processes through a socket file once Listen has been called. It keeps no
// sizes and no layout, starts nothing for Run, and a pane's screen is what
// was typed at it and what its fake agent printed. It has the dock's pane
// in every picture, as the keeper has, and starts no program in it. It
// keeps workspaces as the keeper does: each tab in one from its opening to
// its end, one in front, the wall between them; and it draws no tab row,
// no menu and no line.
package fakeengine

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/engine"
	"github.com/tgdigital-hub/whaleshark/internal/layout"
)

// hooks is the state each of an agent's own events means, as the design's
// table of events has it; an event that is not here changes nothing.
var hooks = map[string]string{
	"SessionStart": contract.StatusIdle, "UserPromptSubmit": contract.StatusWorking,
	"PreToolUse": contract.StatusWorking, "PermissionRequest": contract.StatusBlocked,
	"PostToolUse": contract.StatusWorking, "Stop": contract.StatusIdle, "StopFailure": contract.StatusIdle,
	"SessionEnd": testkit.PushGone,
}

type pane struct {
	rec        contract.Pane
	from, side string // the pane it was split from, and on which side of it
	screen     *bytes.Buffer
	proc       *exec.Cmd
	stdin      io.WriteCloser
}

// Fake is the double. The embedded client is the real adapter, so a Fake is
// the Terminals of a test.
type Fake struct {
	*engine.Client
	// Build is what the double answers for the keeper's version.
	Build string
	// Attached says a window is attached, which decides the answer to a notice.
	Attached bool
	// Width is how many cells wide the double says every pane is.
	Width int

	agents             sync.WaitGroup // the fake agents that have not ended
	mu                 sync.Mutex
	listener           net.Listener
	instance           int
	panes              []*pane
	dock               contract.Pane       // the dock's pane, which is in no tab
	env                map[string][]string // a tab's variables
	opened             map[string]string   // a tab's opener, as the keeper's fence keeps it
	focus              string
	nTab, nPane, nTerm int
	seq                uint64
	readers            map[chan contract.TermEvent]bool
	quiet, closed      bool // a change is being made that is told to nobody; Close was called
	scripts            map[string]string
	calls              []contract.WireCall
	spaces             []*contract.Space // the workspaces, in the order they were opened
	nSpace             int
	group              map[string]string // a tab's fleet
	marks              map[string]int    // what waits in each tab, as the dock said it
	told               string            // the workspaces as the readers of events last heard of them
}

// New returns a double with no tab in it, as a keeper has before its first window.
func New() *Fake {
	f := &Fake{Build: "double", Width: 120, instance: 1, env: map[string][]string{}, opened: map[string]string{}, group: map[string]string{}, readers: map[chan contract.TermEvent]bool{}, scripts: map[string]string{}}
	f.dock = contract.Pane{ID: layout.DockPane, Label: contract.SideTitle, Status: contract.StatusUnknown, Ours: true, Dock: true, Terminal: "i1.0"}
	f.Client = &engine.Client{Dial: func(context.Context) (net.Conn, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.closed {
			return nil, errors.New("the double is closed")
		}
		near, far := net.Pipe()
		go f.serve(far)
		return near, nil
	}}
	return f
}

// Listen serves the double on a socket file, for the programs a test starts
// with the file's path in contract.EnvSocket.
func (f *Fake) Listen(path string) (err error) {
	if f.listener, err = net.Listen("unix", path); err != nil {
		return err
	}
	go func() {
		for {
			conn, err := f.listener.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	return nil
}

// Close stops the listener, every reader of events and every fake agent:
// from then on the double is a keeper that is not running. It returns when
// the agents and all they started have ended, as a pane's programs have
// when the keeper has closed it: a command an agent was in the middle of
// writes nothing into the project after that.
func (f *Fake) Close() {
	f.mu.Lock()
	f.closed = true
	if f.listener != nil {
		f.listener.Close()
	}
	f.cut()
	var ended []int
	for _, p := range f.panes {
		if p.proc != nil {
			ended = append(ended, p.proc.Process.Pid)
		}
		kill(p)
	}
	f.mu.Unlock()
	left := make(chan struct{})
	go func() { f.agents.Wait(); close(left) }()
	select {
	case <-left:
	case <-time.After(endWait):
	}
	for end := time.Now().Add(endWait); slices.ContainsFunc(ended, func(pid int) bool { return !treeGone(pid) }) && time.Now().Before(end); {
		time.Sleep(time.Millisecond)
	}
}

// endWait is the longest Close waits for what it has ended to be gone.
const endWait = 10 * time.Second

// Load replaces the picture with a prepared one, such as a fixture's, and
// numbers what is opened afterwards past every id of it.
func (f *Fake) Load(s *contract.Snapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.panes, f.focus, f.spaces = nil, "", nil
	for _, sp := range s.Spaces {
		f.spaces = append(f.spaces, &sp)
	}
	past := func(n *int, id string) { *n = max(*n, number(id)) }
	for _, rec := range s.Panes {
		past(&f.nPane, rec.ID)
		past(&f.nTab, rec.Tab)
		past(&f.nTerm, rec.Terminal)
		past(&f.nTerm, rec.Session)
		past(&f.nSpace, rec.Workspace)
		if rec.Dock { // the double's own stands
			continue
		} else if rec.Focused {
			f.focus = rec.ID
		}
		// A picture of before phase 7 lists no workspace: its tabs are
		// those of no project, under the name it gives them or ours.
		if rec.Workspace == "" {
			rec.Workspace = f.of("").ID
		} else if f.space(rec.Workspace) == nil {
			f.spaces = append(f.spaces, &contract.Space{ID: rec.Workspace, Name: contract.OtherTabs})
		}
		f.panes = append(f.panes, &pane{rec: rec, screen: new(bytes.Buffer)})
	}
	f.quiet = true
	f.settle()
	f.quiet = false
}

// Script registers what the fake agent of an attempt plays.
func (f *Fake) Script(attempt, script string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts[attempt] = script
}

// Push changes a pane as its program or the person would have: kind is one
// of the contract's states of an agent, or testkit's PushGone (the agent left
// its pane), PushClosed (the pane was closed) or PushFocused. Drop does the
// same and tells no reader of events, which the keeper never does: it is
// there for the flows that must survive a missed event until the swap.
func (f *Fake) Push(kind, pane string) { f.change(kind, pane, false) }
func (f *Fake) Drop(kind, pane string) { f.change(kind, pane, true) }

func (f *Fake) change(kind, id string, quiet bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.pane(id)
	if p == nil {
		return
	}
	f.quiet = quiet
	switch kind {
	case testkit.PushGone:
		f.release(p)
	case testkit.PushClosed:
		f.close(p)
	case testkit.PushFocused:
		f.setFocus(id)
	default:
		f.setStatus(p, kind)
	}
	f.quiet = false
}

// Restart is the keeper started again with everything restored, as the
// design promises: the same ids and each tab's variables, a new instance,
// a new terminal in every pane, every reader of events cut off. An agent
// that had a session is started again, at its prompt and with no script:
// its conversation is behind it, and it plays nothing a second time when a
// line is typed at it. Any other pane is a bare shell.
func (f *Fake) Restart() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cut()
	f.instance++
	f.dock.Terminal = fmt.Sprintf("i%d.0", f.instance)
	for _, p := range f.panes {
		kill(p)
		p.screen = new(bytes.Buffer)
		p.rec.Terminal = f.terminal()
		if p.rec.Session == "" {
			p.rec.Agent, p.rec.Name, p.rec.Status = "", "", contract.StatusUnknown
		} else {
			p.rec.Status = contract.StatusIdle
			f.launch(p, nil, true)
		}
	}
}

// Calls is every call made so far, in order, but the hellos: the proof of
// what was asked of the terminals, and that no key was sent.
func (f *Fake) Calls() []contract.WireCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *Fake) cut() {
	for ch := range f.readers {
		close(ch)
	}
	clear(f.readers)
}

func (f *Fake) terminal() string {
	f.nTerm++
	return fmt.Sprintf("i%d.%d", f.instance, f.nTerm)
}

// pane finds a pane by its id or by the name of the agent in it.
func (f *Fake) pane(id string) *pane {
	for _, p := range f.panes {
		if p.rec.ID == id || p.rec.Name == id && id != "" {
			return p
		}
	}
	return nil
}

func (f *Fake) tab(id string) (panes []*pane) {
	for _, p := range f.panes {
		if p.rec.Tab == id {
			panes = append(panes, p)
		}
	}
	return panes
}

// Told is what the dock last told of each tab, for a test to read.
func (f *Fake) Told() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.marks)
}

// Group is the fleet a tab is of, as the keeper keeps it for its tab row.
func (f *Fake) Group(tab string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.group[tab]
}

// space finds a workspace by its id, and of by a project's folder: made
// where there is none, and in front when it is the only one.
func (f *Fake) space(id string) *contract.Space {
	for _, sp := range f.spaces {
		if sp.ID == id {
			return sp
		}
	}
	return nil
}

func (f *Fake) of(root string) *contract.Space {
	for _, sp := range f.spaces {
		if sp.Root == root {
			return sp
		}
	}
	return f.made(root)
}

// folder is of for a folder a caller named, which is cleaned and, where no
// workspace is of it yet, checked to be one: the keeper's own check.
func (f *Fake) folder(root string) (*contract.Space, error) {
	if root != "" {
		root = filepath.Clean(root)
	}
	known := slices.ContainsFunc(f.spaces, func(sp *contract.Space) bool { return sp.Root == root })
	if st, err := os.Stat(root); root != "" && !known && (err != nil || !st.IsDir()) {
		return nil, fmt.Errorf("%s is no folder to open a workspace of", root)
	}
	return f.of(root), nil
}

func (f *Fake) made(root string) *contract.Space {
	f.nSpace++
	sp := &contract.Space{ID: "s" + strconv.Itoa(f.nSpace), Root: root, Name: contract.SpaceName(root), Front: len(f.spaces) == 0}
	f.spaces = append(f.spaces, sp)
	return sp
}

// front is the workspace in front, nil while there is none.
func (f *Fake) front() *contract.Space {
	for _, sp := range f.spaces {
		if sp.Front {
			return sp
		}
	}
	return nil
}

// settle brings the workspaces in line with the tabs and tells every reader
// when anything of them differs from what was told last, a name too: a
// workspace ends with its last tab, each shows a tab of its own, the one
// that holds the keys is in front, and one is in front while any is left.
func (f *Fake) settle() {
	keys := ""
	if p := f.pane(f.focus); p != nil {
		keys = p.rec.Workspace
	}
	f.spaces = slices.DeleteFunc(f.spaces, func(sp *contract.Space) bool {
		return !slices.ContainsFunc(f.panes, func(p *pane) bool { return p.rec.Workspace == sp.ID })
	})
	for _, sp := range f.spaces {
		if keys != "" {
			sp.Front = sp.ID == keys
		}
		for _, p := range f.panes {
			if own := p.rec.Workspace == sp.ID; own && (p.rec.ID == f.focus || len(f.tab(sp.Tab)) == 0 || f.tab(sp.Tab)[0].rec.Workspace != sp.ID) {
				sp.Tab = p.rec.Tab
			}
		}
	}
	if f.front() == nil && len(f.spaces) > 0 {
		f.spaces[0].Front = true
	}
	now, _ := json.Marshal(f.spaces)
	if was := f.told; string(now) != was {
		f.told = string(now)
		if f.quiet {
			return
		}
		f.seq++
		f.tell(contract.TermEvent{Seq: f.seq, Kind: contract.EvSpace, Spaces: f.list()})
	}
}

func (f *Fake) list() (out []contract.Space) {
	for _, sp := range f.spaces {
		out = append(out, *sp)
	}
	return out
}

func (f *Fake) open(tab, label, cwd string) *pane {
	f.nPane++
	p := &pane{screen: new(bytes.Buffer), rec: contract.Pane{ID: "p" + strconv.Itoa(f.nPane), Tab: tab,
		Terminal: f.terminal(), Label: label, Cwd: cwd, Status: contract.StatusUnknown}}
	if in := f.tab(tab); len(in) > 0 {
		p.rec.Workspace = in[0].rec.Workspace
	}
	f.panes = append(f.panes, p)
	f.emit(contract.EvOpened, p.rec)
	f.refocus()
	return p
}

// emit numbers a change and hands it to every reader of events. One that
// has fallen too far behind is closed, and reads a new picture.
// Window gives the window another width, as a person dragging its edge does.
func (f *Fake) Window(width int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Width = width
	f.emit(contract.EvWindow, contract.Pane{})
}

func (f *Fake) emit(kind string, rec contract.Pane) {
	if f.quiet {
		return
	}
	f.seq++
	f.tell(contract.TermEvent{Seq: f.seq, Kind: kind, Pane: rec})
}

func (f *Fake) tell(e contract.TermEvent) {
	for ch := range f.readers {
		select {
		case ch <- e:
		default:
			delete(f.readers, ch)
			close(ch)
		}
	}
}

func (f *Fake) snapshot() *contract.Snapshot {
	s := &contract.Snapshot{At: contract.Now(), Instance: "i" + strconv.Itoa(f.instance), Seq: f.seq, Panes: []contract.Pane{f.dock}, Spaces: f.list()}
	for _, p := range f.panes {
		s.Panes = append(s.Panes, p.rec)
	}
	slices.SortFunc(s.Panes, func(a, b contract.Pane) int { return strings.Compare(a.ID, b.ID) })
	return s
}

func (f *Fake) setStatus(p *pane, status string) {
	if p.rec.Status != status {
		p.rec.Status = status
		f.emit(contract.EvState, p.rec)
	}
}

func (f *Fake) setFocus(id string) {
	if p := f.pane(id); p != nil && id != f.focus {
		if old := f.pane(f.focus); old != nil {
			old.rec.Focused = false
		}
		f.focus, p.rec.Focused = id, true
		f.emit(contract.EvFocus, p.rec)
		f.settle() // the pane's workspace comes to the front with it
	}
}

// refocus gives the keys on when the pane that had them is gone: to the tab
// its workspace then shows, and to the first workspace that is left only
// when that one has ended.
func (f *Fake) refocus() {
	if f.pane(f.focus) != nil {
		return
	}
	for _, sp := range slices.Insert(slices.Clone(f.spaces), 0, f.front()) {
		if sp != nil && len(f.tab(sp.Tab)) > 0 {
			f.setFocus(f.tab(sp.Tab)[0].rec.ID)
			return
		}
	}
}

// number is the number an id ends with.
func number(id string) int {
	v, _ := strconv.Atoi(id[len(strings.TrimRight(id, "0123456789")):])
	return v
}

// ran keeps the openers as the keeper does when a program is run or an
// agent started in a pane: the pane has what it opened no longer, and the
// tab's opener has the tab no longer when the call is another's.
func (f *Fake) ran(tab, pane, from string) {
	maps.DeleteFunc(f.opened, func(_, by string) bool { return by == pane })
	if f.opened[tab] != from {
		delete(f.opened, tab)
	}
}

// release is an agent leaving its pane: the pane stays, as a bare shell.
func (f *Fake) release(p *pane) {
	if p.rec.Agent != "" {
		p.rec.Agent, p.rec.Name, p.rec.Session, p.rec.Status = "", "", "", contract.StatusUnknown
		f.emit(contract.EvState, p.rec)
	}
}

func (f *Fake) close(p *pane) {
	kill(p)
	f.panes = slices.DeleteFunc(f.panes, func(q *pane) bool { return q == p })
	if len(f.tab(p.rec.Tab)) == 0 {
		f.gone(p.rec.Tab, p.rec.Workspace)
	}
	f.ran("", p.rec.ID, "")
	f.emit(contract.EvClosed, p.rec)
	f.refocus()
	f.settle()
}

// gone forgets what was kept of a tab that is closed. A workspace that
// showed it shows the tab opened after it, or the one before.
func (f *Fake) gone(tab, space string) {
	if sp := f.space(space); sp != nil && sp.Tab == tab {
		after, before := "", ""
		for _, p := range f.panes {
			if n := number(p.rec.Tab); p.rec.Workspace != space {
			} else if n > number(tab) && (after == "" || n < number(after)) {
				after = p.rec.Tab
			} else if n < number(tab) && (before == "" || n >= number(before)) {
				before = p.rec.Tab
			}
		}
		sp.Tab = cmp.Or(after, before)
	}
	delete(f.env, tab)
	delete(f.opened, tab)
	delete(f.group, tab)
	delete(f.marks, tab)
}

// shut closes a tab whole, with what runs in it.
func (f *Fake) shut(tab string) {
	space := ""
	for _, q := range f.tab(tab) {
		kill(q)
		space = q.rec.Workspace
	}
	f.panes = slices.DeleteFunc(f.panes, func(q *pane) bool { return q.rec.Tab == tab })
	f.gone(tab, space)
	f.emit(contract.EvTabClosed, contract.Pane{Tab: tab})
}

// walled says a call from an agent's pane names something of another
// workspace than that pane's own: the wall of contract.Walled.
func (f *Fake) walled(c contract.WireCall, from, p *pane, tab []*pane) bool {
	if from == nil || from.rec.Agent == "" || !slices.Contains(contract.Walled, c.Op) {
		return false
	}
	own := from.rec.Workspace
	for _, q := range append(tab, p, f.pane(c.Target)) {
		if q != nil && q.rec.Workspace != own {
			return true
		}
	}
	return c.Space != "" && c.Space != own
}

// kill ends a pane's agent with every command it started, as closing a
// pane of the keeper ends all that runs in it.
func kill(p *pane) {
	if p.proc != nil {
		endTree(p.proc)
		p.proc = nil
	}
}

// launch starts the fake agent in a pane with the variables of its tab, the
// keeper's own, and of the test's surroundings what is not ours. What the
// program prints is the pane's screen; when it ends, the agent has left.
func (f *Fake) launch(p *pane, args []string, again bool) {
	program := os.Getenv(testkit.EnvAgent)
	if program == "" {
		return
	}
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(kv, "WHALESHARK_") || name == contract.EnvClock || name == contract.EnvNotices {
			env = append(env, kv)
		}
	}
	env = append(append(env, f.env[p.rec.Tab]...), contract.EnvPane+"="+p.rec.ID)
	if f.listener != nil {
		env = append(env, contract.EnvSocket+"="+f.listener.Addr().String())
	}
	script := ""
	for _, kv := range f.env[p.rec.Tab] {
		if attempt, ok := strings.CutPrefix(kv, contract.EnvAttempt+"="); ok && !again {
			script = f.scripts[attempt]
		}
	}
	cmd := exec.Command(program, append([]string{script}, args...)...)
	cmd.Env, cmd.Dir = env, p.rec.Cwd
	stdin, err := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout
	if apart(cmd); err != nil || cmd.Start() != nil {
		return
	}
	p.proc, p.stdin = cmd, stdin
	screen := p.screen
	f.agents.Go(func() {
		buf := make([]byte, 4096)
		for {
			n, err := out.Read(buf)
			f.mu.Lock()
			screen.Write(buf[:n])
			f.mu.Unlock()
			if err != nil {
				break
			}
		}
		cmd.Wait()
		f.mu.Lock()
		defer f.mu.Unlock()
		if p.proc == cmd {
			p.proc = nil
			f.release(p)
		}
	})
}

// serve answers the calls of one connection until it ends or asks for the events.
func (f *Fake) serve(conn net.Conn) {
	defer conn.Close()
	conn = contract.Patient(conn) // as the keeper reads: a caller that left is noticed on every system
	reply := func(r contract.WireReply, id uint64, err error) error {
		if r.ID, r.Err = id, contract.ErrCode(err); err != nil && err.Error() != r.Err {
			r.Message = err.Error()
		}
		body, _ := json.Marshal(r)
		return contract.WriteFrame(conn, contract.FrameReply, body)
	}
	for {
		kind, body, err := contract.ReadFrame(conn)
		var c contract.WireCall
		if err != nil || kind != contract.FrameCall || json.Unmarshal(body, &c) != nil {
			return
		}
		f.mu.Lock()
		if c.Op == contract.OpEvents {
			ch := make(chan contract.TermEvent, 1024)
			f.readers[ch] = true
			first := f.snapshot()
			f.mu.Unlock()
			err = reply(contract.WireReply{Snapshot: first}, c.ID, nil)
			go func() { // the reader left: its end of the connection is closed
				io.Copy(io.Discard, conn)
				f.mu.Lock()
				defer f.mu.Unlock()
				if f.readers[ch] {
					delete(f.readers, ch)
					close(ch)
				}
			}()
			for e := range ch {
				if err == nil {
					err = reply(contract.WireReply{Event: &e}, c.ID, nil)
				} else {
					conn.Close() // and the channel is read to its end
				}
			}
			return
		}
		r, err := f.do(c)
		f.mu.Unlock()
		if reply(r, c.ID, err) != nil {
			return
		}
	}
}

// Typed is the line a call types at an agent: a prompt's text, a fixed
// pointer with its argument or a person's note as the keeper words it, and
// nothing for any other call.
func Typed(c contract.WireCall) string {
	switch {
	case c.Op == contract.OpPrompt:
		return c.Text
	case c.Op == contract.OpSay:
		return contract.Said(c.Kind, c.Text)
	case c.Op != contract.OpPoint:
		return ""
	case strings.Contains(string(c.Pointer), "%"):
		return fmt.Sprintf(string(c.Pointer), c.Text)
	}
	return string(c.Pointer)
}

// do answers one call as the keeper does.
func (f *Fake) do(c contract.WireCall) (r contract.WireReply, err error) {
	if c.Op == contract.OpHello {
		if c.V != contract.WireVersion {
			return r, contract.ErrWireVersion
		}
		return contract.WireReply{Text: f.Build}, nil
	}
	f.calls = append(f.calls, c)
	p, tab := f.pane(c.Pane), f.tab(c.Tab)
	if c.Op == contract.OpPrompt {
		p = f.pane(c.Name)
	}
	// The keeper's fence: an agent's pane makes no call of contract.Fenced,
	// but one of contract.OwnTab about a tab it opened itself.
	if q := f.pane(c.From); q != nil && q.rec.Name != "" && slices.Contains(contract.Fenced, c.Op) {
		at := c.Tab
		if c.Op != contract.OpTabClose {
			if at = ""; p != nil {
				at = p.rec.Tab
			}
		}
		if !slices.Contains(contract.OwnTab, c.Op) || at == "" || f.opened[at] != c.From {
			return r, contract.Fence(c.Op)
		}
	}
	if f.walled(c, f.pane(c.From), p, tab) {
		return r, contract.ErrSpace
	}
	// The dock is looked at and nothing else, and is told three words.
	if id := f.dock.ID; c.Op != contract.OpDock && c.Op != contract.OpOverlay && (c.Pane == id || c.Target == id) {
		switch c.Op {
		case contract.OpScreen:
		case contract.OpSize:
			r.W, r.H = contract.SideWidth, 40
		default:
			err = contract.ErrDock
		}
		return r, err
	}
	switch c.Op {
	case contract.OpDock:
		if c.Dir != contract.DockKeys && c.Dir != contract.DockBack && c.Dir != contract.DockFold {
			return r, fmt.Errorf("the dock is told %s, %s or %s", contract.DockKeys, contract.DockBack, contract.DockFold)
		}
	case contract.OpVersion:
		r.Text = f.Build
	case contract.OpSnapshot, contract.OpStatus:
		r.Snapshot = f.snapshot()
	case contract.OpTabCreate:
		// From a pane the tab is of that pane's workspace and its group,
		// whatever the call says; else of the workspace the call names, made
		// where there is none, or of the one in front.
		from, sp := f.pane(c.From), f.front()
		if from != nil {
			sp = f.space(from.rec.Workspace)
			c.Group = cmp.Or(f.group[from.rec.Tab], c.Group)
		} else if c.Space != "" || sp == nil {
			sp, err = f.folder(c.Space)
		}
		if sp == nil {
			return r, cmp.Or(err, contract.ErrNoPane)
		}
		// The label is cleaned as every label is, and one tab of a workspace
		// at most reads as the main agent's.
		if c.Label = contract.Label(c.Label); contract.IsMainTab(c.Label) && slices.ContainsFunc(f.panes, func(q *pane) bool {
			return q.rec.Workspace == sp.ID && contract.IsMainTab(q.rec.Label)
		}) {
			return r, contract.ErrMainTab
		}
		r.Pane = f.create(sp, c)
		f.settle()
	case contract.OpSpaceOpen:
		sp, err := f.folder(c.Space)
		if err != nil {
			return r, err
		}
		if len(f.tab(sp.Tab)) == 0 {
			c.Label, c.Group, c.Env, c.From = contract.MainTab, "", nil, ""
			f.create(sp, c)
		}
		f.setFocus(f.tab(sp.Tab)[0].rec.ID)
		f.settle()
		rec := f.tab(sp.Tab)[0].rec
		r.Pane = &rec
	case contract.OpSpaceRename, contract.OpSpaceClose:
		sp := f.space(c.Space)
		if sp == nil {
			return r, contract.ErrNoPane
		}
		if c.Op == contract.OpSpaceRename {
			sp.Name, sp.Named = contract.Label(c.Label), contract.Label(c.Label) != ""
			if !sp.Named {
				sp.Name = contract.SpaceName(sp.Root)
			}
		} else {
			for _, q := range slices.Clone(f.panes) {
				if q.rec.Workspace == sp.ID && len(f.tab(q.rec.Tab)) > 0 {
					f.shut(q.rec.Tab)
				}
			}
			f.refocus()
		}
		f.settle()
	case contract.OpMarks:
		// The dock's alone; a figure for a tab that is not there is dropped.
		if c.From != f.dock.ID {
			return r, contract.Fence(c.Op)
		}
		f.marks = maps.Clone(c.Marks)
		maps.DeleteFunc(f.marks, func(tab string, _ int) bool { return len(f.tab(tab)) == 0 })
	case contract.OpNotify:
		if r.Text, r.Delivery = contract.NotifyNoWindow, "on"; f.Attached {
			r.Text = contract.NotifyShown
		}
	case contract.OpSetKeys:
	case contract.OpOverlay:
		// A screen of ours never opens over a project it is not about.
		if q, sp := f.pane(c.From), f.front(); q != nil && sp != nil && q.rec.Workspace != sp.ID {
			return r, contract.ErrNotFront
		}
	case contract.OpTabRename, contract.OpTabFocus, contract.OpTabClose, contract.OpTabGive:
		if len(tab) == 0 || c.Op == contract.OpTabGive && f.pane(c.Target) == nil {
			return r, contract.ErrNoPane
		}
		switch c.Op {
		case contract.OpTabGive:
			f.opened[c.Tab] = c.Target
		case contract.OpTabRename:
			// One tab of a workspace at most reads as the main agent's.
			c.Label = contract.Label(c.Label)
			if contract.IsMainTab(c.Label) && slices.ContainsFunc(f.panes, func(q *pane) bool {
				return q.rec.Tab != c.Tab && q.rec.Workspace == tab[0].rec.Workspace && contract.IsMainTab(q.rec.Label)
			}) {
				return r, contract.ErrMainTab
			}
			for _, q := range tab {
				q.rec.Label = c.Label
			}
			f.emit(contract.EvRenamed, contract.Pane{Tab: c.Tab, Label: c.Label})
		case contract.OpTabFocus:
			if !slices.ContainsFunc(tab, func(q *pane) bool { return q.rec.ID == f.focus }) {
				f.setFocus(tab[0].rec.ID)
			}
		case contract.OpTabClose:
			f.shut(c.Tab)
			f.refocus()
			f.settle()
		}
	default:
		if p == nil {
			return r, contract.ErrNoPane
		}
		return f.onPane(c, p)
	}
	return r, nil
}

// create opens a tab in a workspace, as OpTabCreate asks it.
func (f *Fake) create(sp *contract.Space, c contract.WireCall) *contract.Pane {
	f.nTab++
	id := "t" + strconv.Itoa(f.nTab)
	if f.env[id] = c.Env; f.pane(c.From) != nil {
		f.opened[id] = c.From
	}
	if c.Group != "" {
		f.group[id] = c.Group
	}
	if sp.Tab == "" {
		sp.Tab = id
	}
	f.nPane++
	p := &pane{screen: new(bytes.Buffer), rec: contract.Pane{ID: "p" + strconv.Itoa(f.nPane), Tab: id, Workspace: sp.ID,
		Terminal: f.terminal(), Label: c.Label, Cwd: c.Cwd, Status: contract.StatusUnknown}}
	f.panes = append(f.panes, p)
	f.emit(contract.EvOpened, p.rec)
	f.refocus()
	rec := p.rec
	return &rec
}

// onPane answers a call that is about one pane, which is there.
func (f *Fake) onPane(c contract.WireCall, p *pane) (r contract.WireReply, err error) {
	switch c.Op {
	case contract.OpSplit:
		n := f.open(p.rec.Tab, p.rec.Label, p.rec.Cwd)
		n.from, n.side = p.rec.ID, c.Dir
		rec := n.rec
		r.Pane = &rec
	case contract.OpPaneFocus:
		// The neighbour is the pane split off on that side, or the pane this
		// one was split from when the direction leads back to it.
		back := map[string]string{contract.Left: contract.Right, contract.Right: contract.Left,
			contract.Up: contract.Down, contract.Down: contract.Up}[c.Dir]
		to := c.Pane
		if c.Dir != "" {
			i := slices.IndexFunc(f.panes, func(n *pane) bool {
				return n.from == p.rec.ID && n.side == c.Dir || p.from == n.rec.ID && p.side == back
			})
			if to = ""; i >= 0 {
				to = f.panes[i].rec.ID
			}
		}
		f.setFocus(to)
		r.Text = f.focus
	case contract.OpSwap:
		if f.pane(c.Target) == nil {
			return r, contract.ErrNoPane
		}
	case contract.OpResize:
	case contract.OpPaneClose:
		f.close(p)
	case contract.OpSize:
		r.W, r.H = f.Width, 40
	case contract.OpScreen:
		r.Text = p.screen.String()
	case contract.OpRun:
		if len(c.Argv) == 0 {
			return r, errors.New("no program to run")
		}
		fmt.Fprintf(p.screen, "%% %s\n", strings.Join(c.Argv, " "))
		f.ran(p.rec.Tab, p.rec.ID, c.From)
		p.rec.Terminal = f.terminal()
		// The keeper's own mark of a pane program of ours.
		p.rec.Agent, p.rec.Name, p.rec.Session = "", "", ""
		p.rec.Ours = len(c.Argv) > 3 && c.Argv[1] == "ui" && c.Argv[2] == "run"
		f.emit(contract.EvState, p.rec)
	case contract.OpHook:
		if state := hooks[c.Kind]; state == testkit.PushGone {
			f.release(p)
		} else if state != "" {
			if p.rec.Agent == "" {
				p.rec.Agent = "claude"
			}
			f.setStatus(p, state)
		}
	case contract.OpAgentStart:
		if p.rec.Agent != "" {
			return r, fmt.Errorf("pane %s already holds an agent", p.rec.ID)
		}
		f.nTerm++
		f.ran(p.rec.Tab, p.rec.ID, c.From)
		p.rec.Agent, p.rec.Name, p.rec.Session, p.rec.Status = c.Kind, c.Name, "session-"+strconv.Itoa(f.nTerm), contract.StatusIdle
		f.emit(contract.EvState, p.rec)
		f.launch(p, c.Argv, false)
	case contract.OpPrompt, contract.OpPoint, contract.OpSay:
		text := Typed(c)
		switch {
		case c.Op == contract.OpSay && (text == "" || p.rec.Agent == "" || p.rec.Name != "" || p.rec.Status != contract.StatusIdle):
			return r, contract.ErrAgentNotReady // the note is for an agent started by hand and at rest
		case p.rec.Agent == "":
			return r, contract.ErrNoPane
		case p.rec.Status == contract.StatusBlocked:
			return r, contract.ErrAgentBlocked
		}
		fmt.Fprintf(p.screen, "> %s\n", text)
		if p.stdin != nil {
			fmt.Fprintln(p.stdin, text)
		}
		f.setStatus(p, contract.StatusWorking)
	default:
		return r, fmt.Errorf("the keeper has no call %q", c.Op)
	}
	return r, nil
}
