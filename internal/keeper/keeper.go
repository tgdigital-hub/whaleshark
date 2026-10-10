package keeper

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/layout"
	"github.com/tgdigital-hub/whaleshark/internal/screen"
)

const (
	// A connection that has not said hello by then is closed.
	helloWait = 5 * time.Second
	// A reader of events this many behind is closed; it reads a new
	// picture when it asks again.
	eventQueue = 1024
	// A side of a window is held to what the screen reader takes.
	maxSide = 1000
)

var errRunning = errors.New("a keeper is already running for this login")

// Keeper is the state of the one keeper. Everything below mu is guarded by
// it; a pane's screen has a lock of its own, taken after mu and never before.
type Keeper struct {
	kit      *contract.Kit
	dirs     contract.Dirs
	instance string
	version  string
	listener net.Listener
	unlock   func()
	once     sync.Once
	done     chan struct{} // closed when the keeper starts to stop
	stopped  chan struct{} // closed when it has

	mu     sync.Mutex
	lay    layout.Layout
	tabs   map[string][]string // a tab's own variables
	panes  map[string]*pane
	conns  map[net.Conn]struct{}
	subs   map[chan contract.WireReply]reader
	wins   []*window
	last   *window // the window used last, whose size the panes have
	seq    uint64
	nTab   int
	nPane  int
	nTerm  int
	focus  string // the pane with the keys
	note   string // the notice on the tab row, shown from noteAt
	noteAt time.Time
}

// open makes the keeper of this login: the one lock, the socket file that
// only this login can reach, and nothing running yet.
func open(kit *contract.Kit) (*Keeper, error) {
	dirs, err := kit.Platform.Dirs()
	if err != nil {
		return nil, err
	}
	path := contract.SocketPath(os.Getenv, dirs)
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if dir == dirs.State {
		if err = kit.Platform.Private(dir); err != nil {
			return nil, err
		}
	}
	// Whoever can write the folder can put a socket of their own in it.
	if others, err := kit.Platform.WritableByOthers(dir); others {
		return nil, fmt.Errorf("the folder of the socket, %s, can be written by another login", dir)
	} else if err != nil && !errors.Is(err, contract.ErrCannotTell) {
		return nil, err
	}
	unlock, ok, err := kit.Platform.TryLock(path + ".lock")
	if err != nil {
		return nil, err
	} else if !ok {
		return nil, errRunning
	}
	os.Remove(path) // what a keeper that was killed left behind
	ln, err := net.Listen("unix", path)
	if err == nil {
		if err = kit.Platform.Private(path); err != nil {
			ln.Close()
		}
	}
	if err != nil {
		unlock()
		return nil, err
	}
	id := make([]byte, 6)
	rand.Read(id)
	k := &Keeper{kit: kit, dirs: dirs, instance: hex.EncodeToString(id), version: "dev", listener: ln, unlock: unlock,
		done: make(chan struct{}), stopped: make(chan struct{}), tabs: map[string][]string{}, panes: map[string]*pane{},
		conns: map[net.Conn]struct{}{}, subs: map[chan contract.WireReply]reader{}}
	// The contract has no name for the program's version; the build has one.
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		k.version = info.Main.Version
	}
	screen.Version = k.version
	return k, nil
}

// serve answers connections until the keeper is stopped.
func (k *Keeper) serve() error {
	for {
		conn, err := k.listener.Accept()
		if err != nil {
			select {
			case <-k.done:
				<-k.stopped
				return nil
			default:
				k.stop()
				return err
			}
		}
		go k.handle(conn)
	}
}

// stop ends every pane's program, closes every connection and gives the
// socket and the lock back.
func (k *Keeper) stop() {
	k.once.Do(func() {
		close(k.done)
		k.listener.Close()
		k.mu.Lock()
		var all []contract.Pty
		for _, p := range k.panes {
			all = append(all, p.tty)
		}
		k.panes = map[string]*pane{}
		k.mu.Unlock()
		end(all)
		// The connections close last: whoever asked for the stop waits
		// for the end of its own, and by then every terminal has ended.
		k.mu.Lock()
		for conn := range k.conns {
			conn.Close()
		}
		k.mu.Unlock()
		k.unlock()
		close(k.stopped)
	})
}

// end closes terminals, all at once: one whose program ignores the hang-up
// takes two seconds, and no lock is held meanwhile.
func end(ttys []contract.Pty) {
	var wg sync.WaitGroup
	for _, t := range ttys {
		wg.Go(func() { t.Close() })
	}
	wg.Wait()
}

// ours reports whether the other end of a connection is this login.
func ours(conn net.Conn) bool {
	u, ok := conn.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := u.SyscallConn()
	if err != nil {
		return false
	}
	uid := 0
	if cerr := raw.Control(func(fd uintptr) { uid, err = peer(fd) }); cerr != nil || err != nil {
		return false
	}
	return uid == os.Getuid()
}

// reader is a connection that reads events, and the number of its call.
type reader struct {
	id   uint64
	conn net.Conn
}

// link is one connection. Several goroutines write frames to it.
type link struct {
	conn net.Conn
	mu   sync.Mutex
}

func (l *link) send(kind byte, body []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return contract.WriteFrame(l.conn, kind, body)
}

func (l *link) reply(r contract.WireReply) error {
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return l.send(contract.FrameReply, body)
}

// Write sends a window the bytes composed for it, in frames of the size
// the contract allows.
func (l *link) Write(b []byte) (int, error) {
	for sent := 0; sent < len(b); {
		n := min(len(b)-sent, contract.FrameMax)
		if err := l.send(contract.FrameBytes, b[sent:sent+n]); err != nil {
			return sent, err
		}
		sent += n
	}
	return len(b), nil
}

// answer is the reply to a call: what it gave, or the error by its name.
func answer(id uint64, r contract.WireReply, err error) contract.WireReply {
	if r.ID, r.Err = id, contract.ErrCode(err); err != nil && err.Error() != r.Err {
		r.Message = err.Error()
	}
	return r
}

// handle serves one connection: the hello, then calls and their answers,
// until a call makes it a reader of events or a window.
func (k *Keeper) handle(conn net.Conn) {
	k.mu.Lock()
	select {
	case <-k.done:
		k.mu.Unlock()
		conn.Close()
		return
	default:
	}
	k.conns[conn] = struct{}{}
	k.mu.Unlock()
	defer func() {
		conn.Close()
		k.mu.Lock()
		delete(k.conns, conn)
		k.mu.Unlock()
	}()
	if !ours(conn) {
		return
	}
	l := &link{conn: conn}
	conn.SetReadDeadline(time.Now().Add(helloWait))
	for first := true; ; first = false {
		kind, body, err := contract.ReadFrame(conn)
		var c contract.WireCall
		// A hello comes first and once; a stop may come in its place, so
		// that a program of another version can still stop this keeper.
		if err != nil || kind != contract.FrameCall || json.Unmarshal(body, &c) != nil ||
			first != (c.Op == contract.OpHello) && !(first && c.Op == contract.OpStop) {
			if err == nil {
				l.reply(answer(c.ID, contract.WireReply{}, contract.ErrFrame))
			}
			return
		}
		switch c.Op {
		case contract.OpHello:
			if c.V != contract.WireVersion {
				l.reply(answer(c.ID, contract.WireReply{}, contract.ErrWireVersion))
				return
			}
			conn.SetReadDeadline(time.Time{})
			l.reply(contract.WireReply{ID: c.ID, Text: k.version})
		case contract.OpEvents:
			k.events(l, c.ID)
			return
		case contract.OpAttach:
			k.attach(l, c)
			return
		case contract.OpStop:
			l.reply(contract.WireReply{ID: c.ID})
			k.stop()
			return
		default:
			r, err := k.call(c)
			if l.reply(answer(c.ID, r, err)) != nil {
				return
			}
		}
	}
}

// events answers with the picture and then with every change, in order,
// until the reader leaves, falls too far behind or the keeper stops.
func (k *Keeper) events(l *link, id uint64) {
	ch := make(chan contract.WireReply, eventQueue)
	k.mu.Lock()
	snap := k.snapshot()
	k.subs[ch] = reader{id, l.conn}
	k.mu.Unlock()
	gone := make(chan struct{})
	go func() {
		for {
			if _, _, err := contract.ReadFrame(l.conn); err != nil {
				close(gone)
				return
			}
		}
	}()
	defer func() {
		k.mu.Lock()
		delete(k.subs, ch)
		k.mu.Unlock()
	}()
	for r, ok := (contract.WireReply{ID: id, Snapshot: snap}), true; ok && l.reply(r) == nil; {
		select {
		case r, ok = <-ch:
		case <-gone:
			return
		case <-k.done:
			return
		}
	}
}

// emit numbers a change and hands it to every reader of events.
func (k *Keeper) emit(kind string, p contract.Pane) {
	k.seq++
	for ch, to := range k.subs {
		select {
		case ch <- contract.WireReply{ID: to.id, Event: &contract.TermEvent{Seq: k.seq, Kind: kind, Pane: p}}:
		default:
			// Its connection is closed too: it may be full, with the
			// reply before this one waiting to be written.
			delete(k.subs, ch)
			close(ch)
			to.conn.Close()
		}
	}
}

// snapshot is every pane as it is now, in the order contract.Apply keeps.
func (k *Keeper) snapshot() *contract.Snapshot {
	s := &contract.Snapshot{At: contract.Now(), Instance: k.instance, Seq: k.seq, Panes: []contract.Pane{}}
	for _, p := range k.panes {
		s.Panes = append(s.Panes, p.rec)
	}
	slices.SortFunc(s.Panes, func(a, b contract.Pane) int { return strings.Compare(a.ID, b.ID) })
	return s
}

// shown is the tab that shows.
func (k *Keeper) shown() *layout.Tab {
	if k.lay.Active < len(k.lay.Tabs) {
		return k.lay.Tabs[k.lay.Active]
	}
	return nil
}

// changed is called after every change to the layout: the keys may have
// moved, panes may have new sizes, and every window is drawn again.
func (k *Keeper) changed() {
	now, tab := "", k.shown()
	if tab != nil {
		now = tab.Focus
	}
	if now != k.focus {
		if p := k.panes[k.focus]; p != nil {
			p.rec.Focused = false
		}
		k.focus = now
		if p := k.panes[now]; p != nil {
			p.rec.Focused = true
			k.emit(contract.EvFocus, p.rec)
		}
	}
	for _, t := range k.lay.Tabs {
		for _, at := range k.lay.Places(t) {
			// A place with no cells is a pane the window has no room for.
			if p := k.panes[at.Pane]; p != nil && at.W > 0 && at.H > 0 {
				p.size(at.W, at.H)
			}
		}
	}
	for _, p := range k.panes {
		p.shown.Store(tab != nil && p.rec.Tab == tab.ID)
	}
	k.wake()
}

// wake has every window drawn again, as soon as it can take a frame.
func (k *Keeper) wake() {
	for _, w := range k.wins {
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
}
