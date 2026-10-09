// Package fakeherdr stands in for herdr in tests. It keeps a picture of
// tabs, panes and agents, answers herdr's command lines with herdr's own
// JSON, and serves herdr's event lines on a socket. The real adapter runs
// over it unchanged: in this process through Handle, and from other
// processes through the program in the herdr folder, which is put on the
// PATH under herdr's name.
package fakeherdr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/herdr"
)

// Version is the herdr this fake was written against.
const Version = "0.9.1"

// snapshotType is herdr's name for the answer to "api snapshot". It is put
// together here so that a scan for session ids does not take it for one.
const snapshotType = "session" + "_snapshot"

type tab struct {
	id, label string
	env       []string
}

type pane struct {
	id, tab, terminal, cwd       string
	from, side                   string // the pane it was split from, and on which side of it
	agent, name, session, status string
	screen                       *bytes.Buffer
	proc                         *exec.Cmd
	stdin                        io.WriteCloser
}

type sub struct {
	conn  net.Conn
	kinds []string
	panes []string
}

// Fake is the fake herdr. The embedded adapter is the real one, calling
// Handle where it would start the herdr program.
type Fake struct {
	*herdr.Adapter
	// Attached says a screen is attached, which decides the answer to a notification.
	Attached bool
	// Width is how many cells wide the fake says every pane is.
	Width int

	mu      sync.Mutex
	dir     string
	cli     net.Listener
	events  net.Listener
	tabs    []*tab
	panes   []*pane
	focus   string
	serial  int
	subs    []*sub
	lose    bool
	scripts map[string]string
	calls   [][]string
}

var _ testkit.FakeHerdr = (*Fake)(nil)

// New starts a fake with one workspace, one tab and one pane, as herdr has
// once a person has opened it. Close ends it.
func New(k *contract.Kit) (*Fake, error) {
	dir, err := os.MkdirTemp("", "fh")
	if err != nil {
		return nil, err
	}
	f := &Fake{dir: dir, Attached: true, Width: 120, scripts: map[string]string{}}
	if f.cli, err = net.Listen("unix", filepath.Join(dir, "cli.sock")); err == nil {
		f.events, err = net.Listen("unix", filepath.Join(dir, "events.sock"))
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	f.Adapter = herdr.New(k)
	f.Settings = filepath.Join(dir, "config.toml")
	f.Call = func(ctx context.Context, env, args []string) ([]byte, []byte, int, error) {
		a := f.Handle(testkit.FakeCall{Args: args, Env: env})
		return []byte(a.Stdout), []byte(a.Stderr), a.Exit, nil
	}
	f.focus = f.newTab("1", "", nil).id
	go serve(f.cli, f.serveCall)
	go serve(f.events, f.serveEvents)
	return f, nil
}

// Env is what a process needs in its environment to reach this fake through
// the stand-in program.
func (f *Fake) Env() []string {
	return []string{testkit.EnvFake + "=" + f.cli.Addr().String(), testkit.EnvEvents + "=" + f.events.Addr().String()}
}

// Close stops the listeners and every fake agent, and removes the fake's folder.
func (f *Fake) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range []net.Listener{f.cli, f.events} {
		if l != nil {
			l.Close()
		}
	}
	f.cut()
	for _, p := range f.panes {
		f.kill(p)
	}
	os.RemoveAll(f.dir)
}

func serve(l net.Listener, each func(net.Conn)) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		go each(conn)
	}
}

func (f *Fake) serveCall(conn net.Conn) {
	defer conn.Close()
	var call testkit.FakeCall
	if json.NewDecoder(conn).Decode(&call) == nil {
		json.NewEncoder(conn).Encode(f.Handle(call))
	}
}

// Load replaces the picture with a prepared one, such as a fixture's, and
// numbers what is opened afterwards past every id of it.
func (f *Fake) Load(s *contract.Snapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tabs, f.panes, f.focus = nil, nil, ""
	for _, p := range s.Panes {
		for _, id := range []string{p.ID, p.Tab, p.Terminal, p.Session} {
			digits := len(id) - len(strings.TrimRight(id, "0123456789"))
			if n, err := strconv.Atoi(id[len(id)-digits:]); err == nil {
				f.serial = max(f.serial, n)
			}
		}
		if !slices.ContainsFunc(f.tabs, func(t *tab) bool { return t.id == p.Tab }) {
			f.tabs = append(f.tabs, &tab{id: p.Tab, label: p.Label})
		}
		f.panes = append(f.panes, &pane{id: p.ID, tab: p.Tab, terminal: p.Terminal, cwd: p.Cwd,
			agent: p.Agent, name: p.Name, session: p.Session, status: p.Status, screen: new(bytes.Buffer)})
		if p.Focused {
			f.focus = p.ID
		}
	}
}

func (f *Fake) Script(attempt, script string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts[attempt] = script
}

// Push and Drop take as kind one of the five states of an agent, or "gone"
// (the agent left its pane), "closed" (the pane was closed) or "focused".
func (f *Fake) Push(kind, pane string) { f.change(kind, pane, true) }
func (f *Fake) Drop(kind, pane string) { f.change(kind, pane, false) }

func (f *Fake) change(kind, id string, tell bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.pane(id)
	if p == nil {
		return
	}
	subs := f.subs
	if !tell {
		f.subs = nil
	}
	switch kind {
	case testkit.PushGone:
		f.release(p)
	case testkit.PushClosed:
		f.closePane(p)
	case testkit.PushFocused:
		f.setFocus(id)
	default:
		f.setStatus(p, kind)
	}
	f.subs = subs
}

func (f *Fake) Lose() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lose = true
}

// Cut closes every event connection, as the real server does when it stops.
func (f *Fake) Cut() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cut()
}

func (f *Fake) cut() {
	for _, s := range f.subs {
		s.conn.Close()
	}
	f.subs = nil
}

// Restart keeps every id and gives every pane a new terminal id. A tab loses
// the variables it was created with. Every program in a pane is gone; an
// agent that had a session is started again by the fake as herdr would, with
// herdr's own variables only, and any other pane is an empty shell.
func (f *Fake) Restart() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cut()
	for _, t := range f.tabs {
		t.env = nil
	}
	for _, p := range f.panes {
		f.kill(p)
		p.screen = new(bytes.Buffer)
		p.terminal = f.next("term_")
		if p.session == "" {
			p.agent, p.name, p.status = "", "", contract.StatusUnknown
		} else {
			p.status = contract.StatusIdle
			f.launch(p, nil)
		}
	}
}

func (f *Fake) Calls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *Fake) next(prefix string) string {
	f.serial++
	return prefix + strconv.Itoa(f.serial)
}

func (f *Fake) pane(id string) *pane {
	for _, p := range f.panes {
		if p.id == id || p.name == id && id != "" {
			return p
		}
	}
	return nil
}

func (f *Fake) tab(id string) *tab {
	for _, t := range f.tabs {
		if t.id == id {
			return t
		}
	}
	return nil
}

func (f *Fake) newTab(label, cwd string, env []string) *pane {
	t := &tab{id: f.next("w1:t"), label: label, env: env}
	f.tabs = append(f.tabs, t)
	return f.newPane(t.id, cwd)
}

func (f *Fake) newPane(tab, cwd string) *pane {
	p := &pane{id: f.next("w1:p"), tab: tab, terminal: f.next("term_"), cwd: cwd, status: contract.StatusUnknown, screen: new(bytes.Buffer)}
	f.panes = append(f.panes, p)
	f.send("pane_created", "", obj{"pane": f.paneInfo(p)})
	return p
}

type obj = map[string]any

func (f *Fake) paneInfo(p *pane) obj {
	o := obj{"pane_id": p.id, "tab_id": p.tab, "workspace_id": "w1", "terminal_id": p.terminal,
		"cwd": p.cwd, "foreground_cwd": p.cwd, "focused": p.id == f.focus, "agent_status": p.status, "revision": 0,
		"scroll": obj{"max_offset_from_bottom": 0, "offset_from_bottom": 0, "viewport_rows": 40}}
	if p.agent != "" {
		o["agent"] = p.agent
		if p.session != "" {
			o["agent_session"] = obj{"agent": p.agent, "kind": "id", "source": "herdr:" + p.agent, "value": p.session}
		}
	}
	return o
}

func (f *Fake) tabInfo(t *tab) obj {
	o := obj{"tab_id": t.id, "workspace_id": "w1", "label": t.label, "number": slices.Index(f.tabs, t) + 1,
		"pane_count": 0, "focused": false, "agent_status": contract.StatusUnknown}
	for _, p := range f.panes {
		if p.tab == t.id {
			o["pane_count"] = o["pane_count"].(int) + 1
			o["focused"] = o["focused"].(bool) || p.id == f.focus
		}
	}
	return o
}

func (f *Fake) agentInfo(p *pane) obj {
	o := f.paneInfo(p)
	delete(o, "scroll")
	o["interactive_ready"], o["state_change_seq"] = p.status != contract.StatusBlocked, 0
	if p.name != "" {
		o["name"] = p.name
	}
	return o
}

// layout gives every pane of a tab the whole screen, Width cells wide: the
// fake keeps no sizes.
func (f *Fake) layout(tab string) obj {
	rect := obj{"x": 0, "y": 0, "width": f.Width, "height": 40}
	o := obj{"tab_id": tab, "workspace_id": "w1", "area": rect, "splits": []obj{}, "zoomed": false}
	panes := []obj{}
	for _, p := range f.panes {
		if p.tab == tab {
			panes = append(panes, obj{"pane_id": p.id, "focused": p.id == f.focus, "rect": rect})
			if _, ok := o["focused_pane_id"]; !ok || p.id == f.focus {
				o["focused_pane_id"] = p.id
			}
		}
	}
	o["panes"] = panes
	return o
}

func (f *Fake) snapshot() obj {
	agents, panes, tabs, layouts := []obj{}, []obj{}, []obj{}, []obj{}
	for _, t := range f.tabs {
		tabs, layouts = append(tabs, f.tabInfo(t)), append(layouts, f.layout(t.id))
	}
	for _, p := range f.panes {
		if panes = append(panes, f.paneInfo(p)); p.agent != "" {
			agents = append(agents, f.agentInfo(p))
		}
	}
	space := obj{"workspace_id": "w1", "label": "fake", "number": 1, "focused": true,
		"agent_status": contract.StatusUnknown, "pane_count": len(panes), "tab_count": len(tabs)}
	s := obj{"version": Version, "protocol": 22, "agents": agents, "panes": panes, "tabs": tabs,
		"layouts": layouts, "workspaces": []obj{space}}
	if p := f.pane(f.focus); p != nil {
		s["focused_pane_id"], s["focused_tab_id"], s["focused_workspace_id"], space["active_tab_id"] = p.id, p.tab, "w1", p.tab
	}
	return s
}

// send pushes one event line to every connection that asked for it. A
// status line goes only to those that named its pane.
func (f *Fake) send(event, statusPane string, data obj) {
	if statusPane == "" {
		data["type"] = event
	}
	line, _ := json.Marshal(obj{"event": event, "data": data})
	for _, s := range f.subs {
		if statusPane == "" && !slices.Contains(s.kinds, event) || statusPane != "" && !slices.Contains(s.panes, statusPane) {
			continue
		}
		if f.lose {
			f.lose = false
			continue
		}
		s.conn.Write(append(line, '\n'))
	}
}

func (f *Fake) setStatus(p *pane, status string) {
	if p.status == status {
		return
	}
	p.status = status
	f.send("pane.agent_status_changed", p.id, obj{"pane_id": p.id, "workspace_id": "w1", "agent": p.agent, "agent_status": status})
}

func (f *Fake) setFocus(id string) {
	if f.focus = id; id != "" {
		f.send("pane_focused", "", obj{"pane_id": id, "workspace_id": "w1"})
	}
}

// release is an agent leaving its pane: the pane stays, as a bare shell.
func (f *Fake) release(p *pane) {
	if p.agent == "" {
		return
	}
	agent, last := p.agent, p.status
	p.agent, p.name, p.session, p.status = "", "", "", contract.StatusUnknown
	f.send("pane_updated", "", obj{"pane": f.paneInfo(p)})
	f.send("pane_agent_detected", "", obj{"pane_id": p.id, "workspace_id": "w1", "agent": agent, "final_status": last, "released": true})
	f.send("pane.agent_status_changed", p.id, obj{"pane_id": p.id, "workspace_id": "w1", "agent": agent, "agent_status": p.status})
}

func (f *Fake) closePane(p *pane) {
	f.kill(p)
	f.panes = slices.DeleteFunc(f.panes, func(q *pane) bool { return q == p })
	if !slices.ContainsFunc(f.panes, func(q *pane) bool { return q.tab == p.tab }) {
		f.tabs = slices.DeleteFunc(f.tabs, func(t *tab) bool { return t.id == p.tab })
	}
	f.send("pane_closed", "", obj{"pane_id": p.id, "workspace_id": "w1"})
	f.refocus()
}

// refocus moves the focus when its pane is gone and, like herdr, tells nobody.
func (f *Fake) refocus() {
	if f.pane(f.focus) == nil {
		if f.focus = ""; len(f.panes) > 0 {
			f.focus = f.panes[0].id
		}
	}
}

func (f *Fake) kill(p *pane) {
	if p.proc != nil {
		p.proc.Process.Kill()
		p.proc = nil
	}
}

// launch starts the fake agent in a pane with the environment of its tab,
// herdr's own variables, and the way back to this fake. What the program
// prints is the pane's screen.
func (f *Fake) launch(p *pane, args []string) {
	program := os.Getenv(testkit.EnvAgent)
	if program == "" {
		return
	}
	env := f.Env()
	for _, kv := range os.Environ() {
		// Of our variables an agent has the two that are the surroundings
		// of a test, the clock and the notices switch, and its tab's.
		name, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(kv, "WHALESHARK_") && !strings.HasPrefix(kv, "HERDR_") || name == contract.EnvClock || name == contract.EnvNotices {
			env = append(env, kv)
		}
	}
	t := f.tab(p.tab)
	env = append(env, t.env...)
	env = append(env, "HERDR_ENV=1", "HERDR_PANE_ID="+p.id, "HERDR_TAB_ID="+p.tab, "HERDR_WORKSPACE_ID=w1",
		"HERDR_SOCKET_PATH="+f.events.Addr().String())
	script := ""
	for _, kv := range t.env {
		if attempt, ok := strings.CutPrefix(kv, contract.EnvAttempt+"="); ok {
			script = f.scripts[attempt]
		}
	}
	cmd := exec.Command(program, append([]string{script}, args...)...)
	cmd.Env, cmd.Dir = env, p.cwd
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

// Handle answers one herdr command line as herdr 0.9.1 does, and keeps it
// for Calls.
func (f *Fake) Handle(call testkit.FakeCall) testkit.FakeAnswer {
	f.mu.Lock()
	f.calls = append(f.calls, call.Args)
	flags, rest, after := parse(call.Args)
	result, fail := f.answer(call, flags, rest, after)
	f.mu.Unlock()
	id := "cli:" + strings.Join(rest[:min(2, len(rest))], ":")
	switch r := result.(type) {
	case nil:
		if fail == nil {
			return testkit.FakeAnswer{Stderr: "unknown command: " + strings.Join(call.Args, " ") + "\n", Exit: 2}
		}
		line, _ := json.Marshal(obj{"id": id, "error": fail})
		return testkit.FakeAnswer{Stderr: string(line) + "\n", Exit: 1}
	case string:
		return testkit.FakeAnswer{Stdout: r}
	default:
		line, _ := json.Marshal(obj{"id": id, "result": r})
		return testkit.FakeAnswer{Stdout: string(line) + "\n"}
	}
}

// parse splits a command line into its options, the words that are not
// options, and for "agent start" what follows "--". Like herdr, it takes the
// word after an option as its value whatever that word looks like.
func parse(args []string) (flags map[string][]string, rest, after []string) {
	flags = map[string][]string{}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case len(rest) > 1 && textAt[rest[0]+" "+rest[1]] == len(rest):
			rest = append(rest, a)
		case a == "--" && len(rest) > 1 && rest[1] == "start":
			return flags, rest, args[i+1:]
		case slices.Contains(switches, a):
			flags[a] = nil
		case slices.Contains(options, a) && i+1 < len(args):
			flags[a] = append(flags[a], args[i+1])
			i++
		default:
			rest = append(rest, a)
		}
	}
	return flags, rest, nil
}

var (
	// textAt is where a command takes a word as text even if it looks like an option.
	textAt   = map[string]int{"tab rename": 3, "agent prompt": 3, "pane run": 3, "notification show": 2}
	switches = []string{"--no-focus", "--focus", "--wait", "--json"}
	options  = []string{"--cwd", "--label", "--env", "--kind", "--pane", "--timeout", "--until", "--source",
		"--direction", "--ratio", "--amount", "--source-pane", "--target-pane", "--body", "--sound",
		"--agent", "--state", "--agent-session-id"}
)

func missing(what, id string) *herdr.Error {
	return &herdr.Error{Code: what + "_not_found", Message: what + " " + id + " not found"}
}

func one(flags map[string][]string, name string) string {
	if v := flags[name]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func (f *Fake) answer(call testkit.FakeCall, flags map[string][]string, rest, after []string) (any, *herdr.Error) {
	arg := func(i int) string {
		if i < len(rest) {
			return rest[i]
		}
		return ""
	}
	ok := obj{"type": "ok"}
	switch arg(0) + " " + arg(1) {
	case "--version ":
		return "herdr " + Version + "\n", nil
	case "status ":
		line, _ := json.Marshal(obj{
			"client": obj{"version": Version, "protocol": 22, "binary": "herdr", "session": nil},
			"server": obj{"status": "running", "running": true, "version": Version, "protocol": 22, "compatible": true,
				"socket": f.events.Addr().String(), "session": nil, "restart_needed": false},
		})
		return string(line) + "\n", nil
	case "api snapshot":
		return obj{"type": snapshotType, "snapshot": f.snapshot()}, nil
	case "config check":
		path := f.Settings
		for _, kv := range call.Env {
			if v, found := strings.CutPrefix(kv, "HERDR_CONFIG_PATH="); found {
				path = v
			}
		}
		if _, err := toml.DecodeFile(path, new(map[string]any)); err != nil && !os.IsNotExist(err) {
			return nil, &herdr.Error{Code: herdr.Failed, Message: "config: issues found"}
		}
		return "config: ok\n", nil
	case "server reload-config":
		return obj{"type": "config_reload", "status": "applied", "diagnostics": []string{}}, nil
	case "notification show":
		var cfg struct {
			UI struct{ Toast struct{ Delivery string } }
		}
		toml.DecodeFile(f.Settings, &cfg)
		reason := "shown"
		if off := cfg.UI.Toast.Delivery == "" || cfg.UI.Toast.Delivery == "off"; off && !f.Attached {
			reason = "disabled"
		} else if !f.Attached {
			reason = "no_foreground_client"
		}
		return obj{"type": "notification_show", "reason": reason, "shown": reason == "shown"}, nil
	case "tab create":
		p := f.newTab(one(flags, "--label"), one(flags, "--cwd"), flags["--env"])
		if _, focus := flags["--focus"]; focus {
			f.setFocus(p.id)
		}
		return obj{"type": "tab_created", "root_pane": f.paneInfo(p), "tab": f.tabInfo(f.tab(p.tab))}, nil
	}
	if arg(0) == "tab" {
		t := f.tab(arg(2))
		if t == nil {
			return nil, missing("tab", arg(2))
		}
		switch arg(1) {
		case "rename":
			t.label = arg(3)
			f.send("tab_renamed", "", obj{"tab_id": t.id, "workspace_id": "w1", "label": t.label})
		case "focus":
			f.setFocus(f.panes[slices.IndexFunc(f.panes, func(p *pane) bool { return p.tab == t.id })].id)
		case "close":
			for _, p := range f.panes {
				if p.tab == t.id {
					f.kill(p)
				}
			}
			f.panes = slices.DeleteFunc(f.panes, func(p *pane) bool { return p.tab == t.id })
			f.tabs = slices.DeleteFunc(f.tabs, func(o *tab) bool { return o == t })
			f.send("tab_closed", "", obj{"tab_id": t.id, "workspace_id": "w1"})
			f.refocus()
			return ok, nil
		default:
			return nil, nil
		}
		return obj{"type": "tab_info", "tab": f.tabInfo(t)}, nil
	}
	if arg(0) != "pane" && arg(0) != "agent" {
		return nil, nil
	}
	id := arg(2)
	if arg(1) == "resize" || arg(1) == "focus" || arg(1) == "layout" {
		id = one(flags, "--pane")
	} else if arg(1) == "swap" {
		id = one(flags, "--source-pane")
	} else if arg(1) == "start" {
		id = one(flags, "--pane")
	}
	p := f.pane(id)
	if p == nil || arg(0) == "agent" && arg(1) != "start" && p.agent == "" {
		return nil, missing(arg(0), id)
	}
	switch arg(0) + " " + arg(1) {
	case "pane split":
		n := f.newPane(p.tab, p.cwd)
		n.from, n.side = p.id, one(flags, "--direction")
		return obj{"type": "pane_info", "pane": f.paneInfo(n)}, nil
	case "pane focus":
		// The neighbour is the pane split off on that side, or the pane this
		// one was split from when the direction leads back to it.
		back := map[string]string{"left": "right", "right": "left", "up": "down", "down": "up"}[one(flags, "--direction")]
		changed := false
		for _, n := range f.panes {
			if n.from == p.id && n.side == one(flags, "--direction") || p.from == n.id && p.side == back {
				f.setFocus(n.id)
				changed = true
				break
			}
		}
		return obj{"type": "pane_focus_direction", "focus": obj{"changed": changed, "focused_pane_id": f.focus, "source_pane_id": p.id}}, nil
	case "pane swap":
		if f.pane(one(flags, "--target-pane")) == nil {
			return nil, missing("pane", one(flags, "--target-pane"))
		}
		f.setFocus(p.id)
		return obj{"type": "pane_swap", "swap": obj{"changed": true, "focused_pane_id": f.focus, "layout": f.layout(p.tab),
			"source_pane_id": p.id, "target_pane_id": one(flags, "--target-pane")}}, nil
	case "pane resize":
		return obj{"type": "pane_resize", "resize": obj{"changed": true, "focused_pane_id": f.focus, "layout": f.layout(p.tab), "pane_id": p.id}}, nil
	case "pane layout":
		return obj{"type": "pane_layout", "layout": f.layout(p.tab)}, nil
	case "pane close":
		f.closePane(p)
		return ok, nil
	case "pane run":
		fmt.Fprintf(p.screen, "%% %s\n", arg(3))
		return "", nil
	case "pane read":
		return p.screen.String(), nil
	case "pane report-agent":
		if p.agent == "" {
			p.agent = one(flags, "--agent")
			f.send("pane_agent_detected", "", obj{"pane_id": p.id, "workspace_id": "w1", "agent": p.agent})
		}
		status := one(flags, "--state")
		if status == contract.StatusIdle && p.id != f.focus {
			status = contract.StatusDone
		}
		f.setStatus(p, status)
		return ok, nil
	case "pane release-agent":
		f.release(p)
		return ok, nil
	case "agent start":
		if p.agent != "" {
			return nil, &herdr.Error{Code: "pane_busy", Message: "pane " + p.id + " already holds an agent"}
		}
		if ms, _ := strconv.Atoi(one(flags, "--timeout")); flags["--timeout"] != nil && (ms <= 3000 || ms > 300000) {
			return nil, &herdr.Error{Code: "invalid_agent_timeout", Message: "agent start timeout must be greater than 3000ms and at most 300000ms"}
		}
		p.agent, p.name, p.session = one(flags, "--kind"), arg(2), f.next("session-")
		f.send("pane_agent_detected", "", obj{"pane_id": p.id, "workspace_id": "w1", "agent": p.agent})
		f.setStatus(p, contract.StatusIdle)
		f.launch(p, after)
		return obj{"type": "agent_started", "agent": f.agentInfo(p), "argv": append([]string{p.agent}, after...)}, nil
	case "agent prompt":
		if p.status == contract.StatusBlocked {
			return nil, &herdr.Error{Code: "agent_blocked", Message: "agent " + id + " is blocked and requires interactive input"}
		}
		fmt.Fprintf(p.screen, "> %s\n", arg(3))
		if p.stdin != nil {
			fmt.Fprintln(p.stdin, arg(3))
		}
		f.setStatus(p, contract.StatusWorking)
		return obj{"type": "agent_prompted", "agent": f.agentInfo(p)}, nil
	}
	return nil, nil
}

// serveEvents speaks herdr's socket: one request line, one answer line, then
// event lines until either side closes. Like herdr 0.9.1 it refuses a status
// subscription that names no pane or a pane that is not there, and closes.
func (f *Fake) serveEvents(conn net.Conn) {
	var req struct {
		ID     string
		Params struct {
			Subscriptions []struct {
				Type string
				Pane *string `json:"pane_id"`
			}
		}
	}
	line, _ := bufio.NewReader(conn).ReadBytes('\n')
	refuse := func(id, code, message string) {
		out, _ := json.Marshal(obj{"id": id, "error": herdr.Error{Code: code, Message: message}})
		conn.Write(append(out, '\n'))
		conn.Close()
	}
	if json.Unmarshal(line, &req) != nil {
		refuse("", "invalid_request", "invalid request")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &sub{conn: conn}
	for i, want := range req.Params.Subscriptions {
		switch {
		case want.Type != herdr.Status:
			s.kinds = append(s.kinds, strings.ReplaceAll(want.Type, ".", "_"))
		case want.Pane == nil:
			refuse("", "invalid_request", "invalid request: missing field `pane_id`")
			return
		case f.pane(*want.Pane) == nil:
			refuse(fmt.Sprintf("%s:sub:%d:probe", req.ID, i), "pane_not_found", "pane "+*want.Pane+" not found")
			return
		default:
			s.panes = append(s.panes, *want.Pane)
		}
	}
	out, _ := json.Marshal(obj{"id": req.ID, "result": obj{"type": "subscription_started"}})
	conn.Write(append(out, '\n'))
	f.subs = append(f.subs, s)
	go func() {
		io.Copy(io.Discard, conn)
		f.mu.Lock()
		defer f.mu.Unlock()
		conn.Close()
		f.subs = slices.DeleteFunc(f.subs, func(o *sub) bool { return o == s })
	}()
}
