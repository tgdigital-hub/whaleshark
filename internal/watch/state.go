package watch

import (
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// kind is what can be known of one kind of agent beyond its output. hooks
// says its harness runs `hook state`. The other fields are words on its
// screen, read from recordings of it, and are as fragile as that: trust is
// the question about a folder, asked before any hook runs; both words of
// question together are a question about a step; working and ready are the
// line under its prompt box.
type kind struct {
	hooks          bool
	trust          string
	question       [2]string
	working, ready string
}

// kinds are the agents known by the name of their program. One that is not
// here is told busy from quiet and no more.
var kinds = map[string]kind{
	"claude": {hooks: true, trust: "trust this folder", question: [2]string{"Do you want to", "Esc to cancel"},
		working: "esc to interrupt", ready: "? for shortcuts"},
	// Codex's hooks are its maker's page's, read and not yet run; of its
	// screen no word is known, so no question on it is seen before a hook.
	"codex": {hooks: true},
}

// events are the moments an agent's harness reports and the state each one
// means; an empty state is the agent's own end. Any other event means
// nothing: the one that follows every end of a turn a second later, and the
// notice, which comes six seconds after the question it is about.
var events = map[string]string{
	"SessionStart":       contract.StatusIdle,
	"UserPromptSubmit":   contract.StatusWorking,
	"PreToolUse":         contract.StatusWorking,
	"PermissionRequest":  contract.StatusBlocked,
	"PostToolUse":        contract.StatusWorking,
	"PostToolUseFailure": contract.StatusWorking,
	"PermissionDenied":   contract.StatusWorking,
	"Stop":               contract.StatusIdle,
	"StopFailure":        contract.StatusIdle,
	"SessionEnd":         "",
}

// What a screen shows, by a kind's words.
const (
	nothing = iota
	ready
	working
	question
)

// read is what a screen shows. The question about a folder can only be open
// while no hook has run, so later the same words are a text like any other.
func (k kind) read(text string, hooked bool) int {
	has := func(s string) bool { return s != "" && strings.Contains(text, s) }
	switch {
	case !hooked && has(k.trust), has(k.question[0]) && has(k.question[1]):
		return question
	case has(k.working):
		return working
	case has(k.ready):
		return ready
	}
	return nothing
}

// Hook takes one event of the agent's harness, with the session and the
// folder it named. It returns at once and may be called under any lock.
func (w *Watcher) Hook(event, session, cwd string) {
	status, ok := events[event]
	if !ok {
		return
	}
	w.mu.Lock()
	w.hook, w.asked = status, false
	if status == "" {
		w.st.Session = ""
	} else if session != "" {
		w.st.Session = session
	}
	if cwd != "" {
		w.st.Cwd = cwd
	}
	w.settle(time.Now())
	w.mu.Unlock()
	select {
	case w.poke <- struct{}{}:
	default:
	}
}

// look reads the pane and returns the state it is in now. The screen is
// read only when something was printed since the last look, and once more
// when the printing has stopped: a picture held back by the program that
// draws it is let go by then.
func (w *Watcher) look(now time.Time) State {
	w.mu.Lock()
	n := w.p.Count()
	late := n == w.n && !w.settled && now.Sub(w.grew) >= quietAfter
	read := n != w.n || late
	w.mu.Unlock()

	front, err := w.p.Front()
	text := ""
	if read {
		text = w.p.Text()
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if n < w.n {
		// Another look has read a later screen meanwhile: this one would
		// put an older picture's word in its place.
		return w.st
	}
	if n != w.n {
		w.n, w.grew, w.settled = n, now, false
	}
	w.settled = w.settled || late
	// The program the pane is called after is the one in front once its
	// start is over: a name taken sooner can be a launcher's. A shell that
	// is still the program its pane started is taken at once, or an agent
	// typed for in its first instant would be taken for the pane's own program.
	if w.home == "" && err == nil && (n > 0 && now.Sub(w.grew) >= homeAfter || front != "" && front == w.first) {
		w.home = front
	}
	w.st.Agent, w.away = w.own, w.home != "" && err == nil && front != w.home
	// One system's builds of a program carry its ending in their name on another.
	if name := strings.TrimSuffix(front, ".exe"); w.own == "" && w.away && kinds[name].working != "" {
		w.st.Agent = name
	}
	if read {
		w.sees = kinds[w.st.Agent].read(text, w.hook != "")
	}
	// A question the hooks announced is over when the screen that showed it
	// shows the agent at work instead: the hook after the step comes only
	// when the step has ended, which can be long after the answer. And when
	// the person refuses the step no hook comes at all: then the question
	// is gone, the agent is not at work, and its screen has come to rest.
	if w.hook == contract.StatusBlocked {
		switch {
		case w.sees == question:
			w.asked = true
		case w.asked && w.sees == working:
			w.hook = contract.StatusWorking
		case w.asked && w.settled:
			w.hook = contract.StatusIdle
		}
	}
	w.settle(now)
	return w.st
}

// settle works the state out from the three sources and wakes whoever
// waits for a change. A question on the screen outweighs everything: it is
// where nothing may be typed.
func (w *Watcher) settle(now time.Time) {
	was := w.st
	switch {
	case w.st.Agent == "" && w.home != "":
		if w.st.Status = contract.StatusIdle; w.away {
			w.st.Status = contract.StatusWorking
		}
	case w.sees == question:
		w.st.Status = contract.StatusBlocked
	case w.hook != "":
		w.st.Status = w.hook
	case w.sees == working:
		w.st.Status = contract.StatusWorking
	case w.sees == ready:
		w.st.Status = contract.StatusIdle
	case w.n == 0:
		w.st.Status = contract.StatusUnknown
	case now.Sub(w.grew) < quietAfter:
		w.st.Status = Busy
	default:
		w.st.Status = Quiet
	}
	if w.st != was {
		close(w.wake)
		w.wake = make(chan struct{})
	}
}
