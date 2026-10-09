package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"slices"
	"strings"
	"sync"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// record is every field this package reads from herdr, whichever answer or
// event line it comes in.
type record struct {
	ID        string `json:"pane_id"`
	Tab       string `json:"tab_id"`
	Workspace string `json:"workspace_id"`
	Terminal  string `json:"terminal_id"`
	Label     string
	Cwd       string
	Focused   bool
	Agent     string
	Name      string
	Status    string                 `json:"agent_status"`
	Session   struct{ Value string } `json:"agent_session"`
	Released  bool
	Pane      *record
}

func (r record) pane() contract.Pane {
	if r.Status == "" {
		r.Status = contract.StatusUnknown
	}
	return contract.Pane{ID: r.ID, Tab: r.Tab, Workspace: r.Workspace, Terminal: r.Terminal, Cwd: r.Cwd,
		Focused: r.Focused, Agent: r.Agent, Session: r.Session.Value, Status: r.Status}
}

type wireSnapshot struct{ Panes, Agents, Tabs []record }

// byID is the order of the panes in every picture of this package, so that
// two pictures of the same moment are equal.
func byID(a, b contract.Pane) int { return strings.Compare(a.ID, b.ID) }

// snapshot joins herdr's three lists: a pane's Label is its tab's label and
// its Name the name of the agent in it.
func (w wireSnapshot) snapshot() *contract.Snapshot {
	s := &contract.Snapshot{At: contract.Now(), Panes: make([]contract.Pane, len(w.Panes))}
	for i, r := range w.Panes {
		p := r.pane()
		for _, t := range w.Tabs {
			if t.Tab == p.Tab {
				p.Label = t.Label
			}
		}
		for _, g := range w.Agents {
			if g.ID == p.ID {
				p.Name = g.Name
			}
		}
		s.Panes[i] = p
	}
	slices.SortFunc(s.Panes, byID)
	return s
}

// The kinds of event Events delivers. Created, Updated and Closed are also
// what it reports when a fresh snapshot shows a change no line told of.
const (
	Created   = "pane_created"
	Updated   = "pane_updated"
	Closed    = "pane_closed"
	Exited    = "pane_exited"
	Focused   = "pane_focused"
	Status    = "pane.agent_status_changed"
	Released  = "pane_agent_detected"
	TabClosed = "tab_closed"
	TabNamed  = "tab_renamed"
)

// Apply brings a picture up to date with one event of Events. TabClosed and
// TabNamed carry the tab's id, and TabNamed its new label; every other kind
// carries the pane's whole record as it is after the event, or as it last
// was when the pane is gone.
func Apply(s *contract.Snapshot, e contract.HerdrEvent) {
	panes, found := make([]contract.Pane, 0, len(s.Panes)+1), false
	for _, p := range s.Panes {
		switch e.Kind {
		case Closed, Exited:
			if p.ID == e.Pane.ID {
				continue
			}
		case TabClosed:
			if p.Tab == e.Pane.Tab {
				continue
			}
		case TabNamed:
			if p.Tab == e.Pane.Tab {
				p.Label = e.Pane.Label
			}
		default:
			if e.Pane.Focused {
				p.Focused = false
			}
			if p.ID == e.Pane.ID {
				p, found = e.Pane, true
			}
		}
		panes = append(panes, p)
	}
	switch e.Kind {
	case Closed, Exited, TabClosed, TabNamed:
	default:
		if !found {
			panes = append(panes, e.Pane)
			slices.SortFunc(panes, byID)
		}
	}
	s.Panes = panes
}

// The subscription is one connection to herdr's socket, and of the two ways
// the design allows this package builds the second: a status subscription for
// every pane, renewed as panes appear. The first, an unfiltered subscription
// to pane.updated alone, does not work on herdr 0.9.1: a change of status
// arrives only as the status event, which must name its pane (tried). A
// connection takes one request, so the request names every pane of the
// snapshot, and when a pane appears, or an agent in one, the stream connects
// again with the longer list, takes a fresh snapshot and delivers what
// differs.
//
// What the lines do not tell: a pane's working folder and its agent's
// session id change without any line, and herdr sends status lines up to a
// few tenths of a second late, after other lines that happened later; the
// last status line is the true one. So a picture built from events can
// differ from a snapshot for a moment, and only a snapshot shows the first two.
var unfiltered = []string{"pane.created", "pane.closed", "pane.exited", "pane.updated", "pane.focused",
	"pane.moved", "pane.agent_detected", "tab.closed", "tab.renamed"}

type stream struct {
	a     *Adapter
	ctx   context.Context
	mu    sync.Mutex
	conn  net.Conn
	lines *bufio.Reader
	pic   *contract.Snapshot
	out   chan contract.HerdrEvent
}

// Events subscribes first and takes the snapshot second, so that nothing can
// change between the two unseen. The channel closes when herdr's server
// closes the connection, as it does when it stops, or when ctx ends; the
// caller then calls Events again. A closed connection and a pane's changed
// Terminal are the two signs that herdr was restarted.
func (a *Adapter) Events(ctx context.Context) (*contract.Snapshot, <-chan contract.HerdrEvent, error) {
	s := &stream{a: a, ctx: ctx, pic: &contract.Snapshot{}, out: make(chan contract.HerdrEvent, 64)}
	first, err := a.Snapshot(ctx)
	if err == nil {
		s.pic = first
		err = s.sync("")
	}
	if err != nil {
		return nil, nil, err
	}
	first = &contract.Snapshot{At: s.pic.At, Panes: slices.Clone(s.pic.Panes)}
	context.AfterFunc(ctx, func() { s.swap(nil, nil) })
	go s.read()
	return first, s.out, nil
}

// swap closes the connection in use and takes another in its place.
func (s *stream) swap(conn net.Conn, lines *bufio.Reader) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		s.conn.Close()
	}
	if s.conn, s.lines = conn, lines; conn != nil && s.ctx.Err() != nil {
		conn.Close()
	}
}

// sync connects with a status subscription for every pane of the picture and
// for pane more, then makes a fresh snapshot the picture. It tries again
// while panes come and go under it.
func (s *stream) sync(more string) error {
	ids := []string{more}
	for _, p := range s.pic.Panes {
		ids = append(ids, p.ID)
	}
	for range 5 {
		conn, lines, err := s.a.subscribe(s.ctx, ids)
		if err != nil && Code(err) != "pane_not_found" {
			return err
		}
		now, failed := s.a.Snapshot(s.ctx)
		if failed != nil {
			if conn != nil {
				conn.Close()
			}
			return failed
		}
		seen := make([]string, len(now.Panes))
		for i, p := range now.Panes {
			seen[i] = p.ID
		}
		if err == nil && !slices.ContainsFunc(seen, func(id string) bool { return !slices.Contains(ids, id) }) {
			s.swap(conn, lines)
			s.pic = now
			return nil
		}
		if conn != nil {
			conn.Close()
		}
		ids = seen
	}
	return &Error{Failed, "panes kept coming and going while subscribing"}
}

// subscribe opens one connection and returns it once herdr has accepted the request.
func (a *Adapter) subscribe(ctx context.Context, panes []string) (net.Conn, *bufio.Reader, error) {
	st, err := a.status(ctx)
	if err != nil {
		return nil, nil, err
	}
	type sub struct {
		Type string `json:"type"`
		Pane string `json:"pane_id,omitempty"`
	}
	var subs []sub
	for _, kind := range unfiltered {
		subs = append(subs, sub{Type: kind})
	}
	for _, id := range panes {
		if id != "" {
			subs = append(subs, sub{Status, id})
		}
	}
	request, _ := json.Marshal(map[string]any{"id": "whaleshark", "method": "events.subscribe",
		"params": map[string]any{"subscriptions": subs}})
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", st.Server.Socket)
	if err != nil {
		return nil, nil, &Error{Unreachable, err.Error()}
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	lines := bufio.NewReader(conn)
	var line []byte
	if _, err = conn.Write(append(request, '\n')); err == nil {
		line, err = lines.ReadBytes('\n')
	}
	var answer struct{ Error *Error }
	if err != nil || json.Unmarshal(line, &answer) != nil {
		conn.Close()
		return nil, nil, &Error{Unreachable, "the event connection gave no answer"}
	}
	if answer.Error != nil {
		conn.Close()
		return nil, nil, answer.Error
	}
	return conn, lines, nil
}

// read turns herdr's lines into events until the connection ends. Several
// lines can arrive in one read; the reader splits on line ends.
func (s *stream) read() {
	defer close(s.out)
	defer s.swap(nil, nil)
	for {
		s.mu.Lock()
		lines := s.lines
		s.mu.Unlock()
		if lines == nil {
			return
		}
		line, err := lines.ReadBytes('\n')
		if err != nil {
			return
		}
		var w struct {
			Event string
			Data  record
		}
		if json.Unmarshal(line, &w) != nil {
			continue
		}
		if !s.line(w.Event, w.Data) {
			return
		}
	}
}

func (s *stream) emit(kind string, p contract.Pane) bool {
	e := contract.HerdrEvent{Kind: kind, Pane: p}
	Apply(s.pic, e)
	select {
	case s.out <- e:
		return true
	case <-s.ctx.Done():
		return false
	}
}

// line handles one event line; false ends the stream.
func (s *stream) line(kind string, d record) bool {
	if d.Pane != nil {
		d.ID = d.Pane.ID
	}
	at := slices.IndexFunc(s.pic.Panes, func(p contract.Pane) bool { return p.ID == d.ID })
	switch kind {
	case TabNamed:
		return s.emit(kind, contract.Pane{Tab: d.Tab, Workspace: d.Workspace, Label: d.Label})
	case TabClosed:
		return s.emit(kind, contract.Pane{Tab: d.Tab, Workspace: d.Workspace}) && s.refocus()
	case Closed, Exited:
		return at < 0 || s.emit(kind, s.pic.Panes[at]) && s.refocus()
	}
	if d.ID == "" || at < 0 && kind == Status {
		return true // a status line is only ever late for a pane that is gone, never early for a new one
	}
	if at < 0 || kind == Created || kind == "pane_moved" || kind == Released && !d.Released {
		return s.resync(d.ID)
	}
	p := s.pic.Panes[at]
	switch kind {
	case Updated:
		now := d.Pane.pane()
		now.Label = p.Label
		if now.Agent != "" {
			now.Name = p.Name
			if now.Session == "" {
				now.Session = p.Session
			}
		}
		p = now
	case Status:
		p.Status = d.Status // the line still names an agent that has just left
	case Released:
		p.Agent, p.Name, p.Session, p.Status = "", "", "", contract.StatusUnknown
	case Focused:
		p.Focused = true
	default:
		return true
	}
	return s.emit(kind, p)
}

// refocus asks for a snapshot when the pane that had the focus is gone:
// herdr moves the focus to another pane then, and sends no line for it.
func (s *stream) refocus() bool {
	if slices.ContainsFunc(s.pic.Panes, func(p contract.Pane) bool { return p.Focused }) {
		return true
	}
	return s.resync("")
}

// resync connects again with pane added and delivers what a fresh snapshot
// shows differently from the picture.
func (s *stream) resync(pane string) bool {
	old := s.pic
	if s.sync(pane) != nil {
		return false
	}
	now := s.pic
	s.pic = old
	for _, p := range old.Panes {
		if !slices.ContainsFunc(now.Panes, func(n contract.Pane) bool { return n.ID == p.ID }) && !s.emit(Closed, p) {
			return false
		}
	}
	for _, p := range now.Panes {
		at := slices.IndexFunc(old.Panes, func(o contract.Pane) bool { return o.ID == p.ID })
		kind := Updated
		if at < 0 {
			kind = Created
		} else if old.Panes[at] == p {
			continue
		}
		if !s.emit(kind, p) {
			return false
		}
	}
	s.pic.At = now.At
	return true
}
