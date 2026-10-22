package watch

import (
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

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

// onScreen is what a screen shows by a tool's words, each of which is as
// fragile as a screen is; a tool with none is told busy from quiet and no
// more. Every phrase of its question together is a question about a step.
// The question about a folder can only be open while no hook has run, so
// later the same words are a text like any other.
func onScreen(k contract.ToolScreen, text string, hooked bool) int {
	has := func(s string) bool { return s != "" && strings.Contains(text, s) }
	switch {
	case !hooked && has(k.Trust), len(k.Question) > 0 && !slices.ContainsFunc(k.Question, func(s string) bool { return !has(s) }):
		return question
	case has(k.Working):
		return working
	case has(k.Ready):
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
	w.st.Agent, w.words, w.away = w.own, w.tool.Screen, w.home != "" && err == nil && front != w.home
	// A tool a person starts in a shell is known by its program's name. One
	// system's builds of a program carry its ending in their name on another.
	// A tool that is a script runs under its interpreter's name (node): it is
	// known by its screen's words once they showed, while that program stays.
	if name := strings.TrimSuffix(front, ".exe"); w.own == "" && w.away {
		for _, t := range w.all {
			if by := strings.TrimSuffix(filepath.Base(t.Run[0]), ".exe") == name; t.Screen.Working != "" && (by || read && onScreen(t.Screen, text, false) != nothing) {
				if w.under = [2]string{name, t.Name}; by {
					break
				}
			}
		}
		if t, ok := contract.FindTool(w.all, w.under[1]); ok && w.under[0] == name {
			w.st.Agent, w.words = t.Name, t.Screen
		}
	} else if !w.away {
		w.under = [2]string{}
	}
	if read {
		w.sees = onScreen(w.words, text, w.hook != "")
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
	if s := w.st.Status; s != was.Status && (s == contract.StatusWorking || s == contract.StatusBlocked) {
		w.starts++
	}
	if w.st != was {
		close(w.wake)
		w.wake = make(chan struct{})
	}
}
