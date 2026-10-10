// Package watch is what the program in a pane is doing, from the three
// things that can tell: the program's own place in its terminal, its hooks,
// and its screen. It also holds the only ways the tool types at an agent,
// each of which refuses an agent that shows a question.
package watch

import (
	"sync"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Plug has nothing to put into the kit: the keeper makes a Watcher for each
// program it starts and answers its agent calls with it.
func Plug(*contract.Kit) {}

// Busy and Quiet are the honest words for a program whose output is all
// there is to go by: something is being printed, or nothing has been for a while.
const (
	Busy  = contract.StatusBusy
	Quiet = contract.StatusQuiet
)

const (
	// How often a pane is looked at. A hook is not waited for: it is taken at once.
	every = 100 * time.Millisecond
	// A program that has printed nothing for this long is quiet.
	quietAfter = 2 * time.Second
	// A program's start is over when it has printed and then nothing for this long.
	homeAfter = 500 * time.Millisecond
	// The pause between a prompt's text and its Enter: an agent that reads
	// both in one go takes the Enter for a line break of the text.
	enterAfter = 150 * time.Millisecond
)

// Pane is the pane a Watcher looks at and types into; the keeper hands one
// in. None of it may need a lock the caller of a Watcher's methods holds.
type Pane interface {
	// Text is what the pane shows, as contract.Picture.Text gives it.
	Text() string
	// Count is how many bytes the program has printed.
	Count() uint64
	// Front is contract.Pty.Front.
	Front() (string, error)
	// Paste reports whether the program asked for pasted text between marks.
	Paste() bool
	// Type writes into the pane's terminal.
	Type([]byte) error
}

// State is what is known of a pane's program. Agent is its kind, empty for
// a shell; Status is one of the contract's, or Busy or Quiet; Session and
// Cwd are what the agent's hooks said last.
type State struct {
	Agent, Name, Session, Cwd, Status string
}

// Watcher follows one program in one pane, from New until Close.
type Watcher struct {
	p    Pane
	emit func(State)
	own  string // the kind the pane was started as; empty for a shell
	poke chan struct{}
	done chan struct{}

	mu      sync.Mutex
	st      State
	home    string        // the program in front once the start was over; empty until then and where the system cannot tell
	away    bool          // another program is in front of the agent
	hook    string        // the state the hooks gave last; empty before the first and after the last
	sees    int           // what the screen showed at the last reading
	asked   bool          // the screen has shown the question a hook announced
	n       uint64        // the count at the last look
	grew    time.Time     // when it last differed
	settled bool          // the screen was read once more after the output went quiet
	wake    chan struct{} // closed at every change of st
}

// New starts following the program just started in p: an agent of a kind,
// under the name the tool gave it, or a shell when agent is empty. emit is
// called with every new state, in order and from one goroutine of the
// Watcher's own, never from inside a call of its methods.
func New(p Pane, agent, name string, emit func(State)) *Watcher {
	w := &Watcher{p: p, emit: emit, own: agent, poke: make(chan struct{}, 1), done: make(chan struct{}),
		st: State{Agent: agent, Name: name, Status: contract.StatusUnknown}, wake: make(chan struct{})}
	go w.loop()
	return w
}

// Close ends the following, when the program has ended or another takes its
// pane. Whoever waits in Ready or Prompt is answered contract.ErrNoPane.
func (w *Watcher) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	select {
	case <-w.done:
	default:
		close(w.done)
	}
}

func (w *Watcher) loop() {
	t := time.NewTicker(every)
	defer t.Stop()
	var sent State
	for {
		select {
		case <-w.done:
			return
		case <-t.C:
		case <-w.poke:
		}
		if s := w.look(time.Now()); s != sent {
			sent = s
			w.emit(s)
		}
	}
}
