// Package fakeengine is the keeper's double for tests: tabs, panes and agents
// held in memory, answering the keeper's own calls. The real adapter runs
// over it unchanged: in this process through a pipe, and from other
// processes through a socket file once Listen has been called. It keeps no
// sizes and no layout, starts nothing for Run, and a pane's screen is what
// was typed at it and what its fake agent printed.
package fakeengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/engine"
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

	mu                 sync.Mutex
	listener           net.Listener
	instance           int
	panes              []*pane
	env                map[string][]string // a tab's variables
	focus              string
	nTab, nPane, nTerm int
	seq                uint64
	readers            map[chan contract.TermEvent]bool
	quiet, closed      bool // a change is being made that is told to nobody; Close was called
	scripts            map[string]string
	calls              []contract.WireCall
}

// New returns a double with no tab in it, as a keeper has before its first window.
func New() *Fake {
	f := &Fake{Build: "double", Width: 120, instance: 1, env: map[string][]string{}, readers: map[chan contract.TermEvent]bool{}, scripts: map[string]string{}}
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
// from then on the double is a keeper that is not running.
func (f *Fake) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	if f.listener != nil {
		f.listener.Close()
	}
	f.cut()
	for _, p := range f.panes {
		kill(p)
	}
}

// Load replaces the picture with a prepared one, such as a fixture's, and
// numbers what is opened afterwards past every id of it.
func (f *Fake) Load(s *contract.Snapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.panes, f.focus = nil, ""
	past := func(n *int, id string) {
		digits := len(id) - len(strings.TrimRight(id, "0123456789"))
		if v, err := strconv.Atoi(id[len(id)-digits:]); err == nil {
			*n = max(*n, v)
		}
	}
	for _, rec := range s.Panes {
		past(&f.nPane, rec.ID)
		past(&f.nTab, rec.Tab)
		past(&f.nTerm, rec.Terminal)
		past(&f.nTerm, rec.Session)
		if rec.Focused {
			f.focus = rec.ID
		}
		f.panes = append(f.panes, &pane{rec: rec, screen: new(bytes.Buffer)})
	}
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
// that had a session is started again; any other pane is a bare shell.
func (f *Fake) Restart() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cut()
	f.instance++
	for _, p := range f.panes {
		kill(p)
		p.screen = new(bytes.Buffer)
		p.rec.Terminal = f.terminal()
		if p.rec.Session == "" {
			p.rec.Agent, p.rec.Name, p.rec.Status = "", "", contract.StatusUnknown
		} else {
			p.rec.Status = contract.StatusIdle
			f.launch(p, nil)
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

func (f *Fake) open(tab, label, cwd string) *pane {
	f.nPane++
	p := &pane{screen: new(bytes.Buffer), rec: contract.Pane{ID: "p" + strconv.Itoa(f.nPane), Tab: tab,
		Terminal: f.terminal(), Label: label, Cwd: cwd, Status: contract.StatusUnknown}}
	f.panes = append(f.panes, p)
	f.emit(contract.EvOpened, p.rec)
	f.refocus()
	return p
}

// emit numbers a change and hands it to every reader of events. One that
// has fallen too far behind is closed, and reads a new picture.
func (f *Fake) emit(kind string, rec contract.Pane) {
	if f.quiet {
		return
	}
	f.seq++
	for ch := range f.readers {
		select {
		case ch <- contract.TermEvent{Seq: f.seq, Kind: kind, Pane: rec}:
		default:
			delete(f.readers, ch)
			close(ch)
		}
	}
}

func (f *Fake) snapshot() *contract.Snapshot {
	s := &contract.Snapshot{At: contract.Now(), Instance: "i" + strconv.Itoa(f.instance), Seq: f.seq, Panes: []contract.Pane{}}
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
	}
}

// refocus gives the keys to the first pane when the pane that had them is gone.
func (f *Fake) refocus() {
	if f.pane(f.focus) == nil && len(f.panes) > 0 {
		f.setFocus(f.panes[0].rec.ID)
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
		delete(f.env, p.rec.Tab)
	}
	f.emit(contract.EvClosed, p.rec)
	f.refocus()
}

func kill(p *pane) {
	if p.proc != nil {
		p.proc.Process.Kill()
		p.proc = nil
	}
}

// launch starts the fake agent in a pane with the variables of its tab, the
// keeper's own, and of the test's surroundings what is not ours. What the
// program prints is the pane's screen; when it ends, the agent has left.
func (f *Fake) launch(p *pane, args []string) {
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
		if attempt, ok := strings.CutPrefix(kv, contract.EnvAttempt+"="); ok {
			script = f.scripts[attempt]
		}
	}
	cmd := exec.Command(program, append([]string{script}, args...)...)
	cmd.Env, cmd.Dir = env, p.rec.Cwd
	stdin, err := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout
	if err != nil || cmd.Start() != nil {
		return
	}
	p.proc, p.stdin = cmd, stdin
	screen := p.screen
	go func() {
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
	}()
}

// serve answers the calls of one connection until it ends or asks for the events.
func (f *Fake) serve(conn net.Conn) {
	defer conn.Close()
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

// Typed is the line a call types at an agent: a prompt's text or a fixed
// pointer with its argument, and nothing for any other call.
func Typed(c contract.WireCall) string {
	switch {
	case c.Op == contract.OpPrompt:
		return c.Text
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
	switch c.Op {
	case contract.OpVersion:
		r.Text = f.Build
	case contract.OpSnapshot, contract.OpStatus:
		r.Snapshot = f.snapshot()
	case contract.OpTabCreate:
		f.nTab++
		id := "t" + strconv.Itoa(f.nTab)
		f.env[id] = c.Env
		rec := f.open(id, c.Label, c.Cwd).rec
		r.Pane = &rec
	case contract.OpNotify:
		if r.Text, r.Delivery = contract.NotifyNoWindow, "on"; f.Attached {
			r.Text = contract.NotifyShown
		}
	case contract.OpSetKeys, contract.OpOverlay:
	case contract.OpTabRename, contract.OpTabFocus, contract.OpTabClose:
		if len(tab) == 0 {
			return r, contract.ErrNoPane
		}
		switch c.Op {
		case contract.OpTabRename:
			for _, q := range tab {
				q.rec.Label = c.Label
			}
			f.emit(contract.EvRenamed, contract.Pane{Tab: c.Tab, Label: c.Label})
		case contract.OpTabFocus:
			if !slices.ContainsFunc(tab, func(q *pane) bool { return q.rec.ID == f.focus }) {
				f.setFocus(tab[0].rec.ID)
			}
		case contract.OpTabClose:
			for _, q := range tab {
				kill(q)
			}
			f.panes = slices.DeleteFunc(f.panes, func(q *pane) bool { return q.rec.Tab == c.Tab })
			delete(f.env, c.Tab)
			f.emit(contract.EvTabClosed, contract.Pane{Tab: c.Tab})
			f.refocus()
		}
	default:
		if p == nil {
			return r, contract.ErrNoPane
		}
		return f.onPane(c, p)
	}
	return r, nil
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
		p.rec.Agent, p.rec.Name, p.rec.Session, p.rec.Status = c.Kind, c.Name, "session-"+strconv.Itoa(f.nTerm), contract.StatusIdle
		f.emit(contract.EvState, p.rec)
		f.launch(p, c.Argv)
	case contract.OpPrompt, contract.OpPoint:
		text := Typed(c)
		switch {
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
