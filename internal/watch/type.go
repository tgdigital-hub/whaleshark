package watch

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// The marks a pasted text goes between, for a program that asked for them
// (xterm's control sequences, "Bracketed Paste Mode").
const pasteOpen, pasteClose = "\x1b[200~", "\x1b[201~"

var errSlowStart = errors.New("the agent did not start in time")

// pointers are the fixed lines Point may type.
var pointers = []contract.Pointer{contract.PointEvents, contract.PointAnswer, contract.PointQuestion, contract.PointCarryOn,
	contract.PointAnswered, contract.PointMail, contract.PointResumed, contract.PointLeadOn}

// until waits for a state that ends the wait, and answers late when none
// came in time.
func (w *Watcher) until(timeout time.Duration, late error, over func(State) (bool, error)) error {
	t := time.NewTimer(timeout)
	defer t.Stop()
	for {
		w.mu.Lock()
		s, wake := w.st, w.wake
		w.mu.Unlock()
		if ok, err := over(s); ok {
			return err
		}
		select {
		case <-wake:
		case <-w.done:
			return contract.ErrNoPane
		case <-t.C:
			return late
		}
	}
}

// Ready waits until the agent just started can take a prompt: its hook has
// said the session began, or its screen shows it ready, or, for a kind with
// neither, its output has gone quiet. An agent that shows a question
// instead is contract.ErrAgentNotReady, which no hook can tell: none runs
// while the question about the folder is open.
func (w *Watcher) Ready(timeout time.Duration) error {
	return w.until(timeout, errSlowStart, func(s State) (bool, error) {
		switch s.Status {
		case contract.StatusBlocked:
			return true, contract.ErrAgentNotReady
		case contract.StatusIdle:
			return true, nil
		}
		return s.Status == Quiet && kinds[s.Agent].ready == "", nil
	})
}

// Prompt types a prompt at the agent and waits until it works on it, or
// has come to a question of its own on the way. It types once: when the
// agent does not start, the answer is contract.ErrPromptStalled and what
// was typed stays where it is.
func (w *Watcher) Prompt(text string, timeout time.Duration) error {
	if err := w.typed(text); err != nil {
		return err
	}
	return w.until(timeout, contract.ErrPromptStalled, func(s State) (bool, error) {
		return s.Status == contract.StatusWorking || s.Status == Busy || s.Status == contract.StatusBlocked, nil
	})
}

// Point types one of the fixed lines, with an id or a count in its place.
func (w *Watcher) Point(p contract.Pointer, arg string) error {
	plain := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(".-_", r) }
	if !slices.Contains(pointers, p) || len(arg) > 64 || strings.ContainsFunc(arg, func(r rune) bool { return !plain(r) }) {
		return errors.New("not one of the fixed pointers")
	}
	line := string(p)
	if strings.Contains(line, "%") {
		line = fmt.Sprintf(line, arg)
	}
	return w.typed(line)
}

// typed is the one place that types at an agent: the text as one paste,
// then Enter. It looks first, and types nothing at a shell, at an agent
// that has another program in front of it, at one that shows a question,
// or at one that has not been seen ready.
// No control character of the text is typed, so no text can end its own
// paste or press a key.
func (w *Watcher) typed(text string) error {
	select {
	case <-w.done:
		return contract.ErrNoPane
	default:
	}
	s := w.look(time.Now())
	w.mu.Lock()
	away := w.away
	w.mu.Unlock()
	// An agent of a kind whose screen is known must have shown itself at its
	// prompt or at work: anything else may be a question the words do not cover.
	sure := s.Status == contract.StatusIdle || s.Status == contract.StatusWorking
	switch {
	case s.Status == contract.StatusBlocked:
		return contract.ErrAgentBlocked
	case s.Agent == "" || away && s.Agent == w.own || kinds[s.Agent].working != "" && !sure:
		return contract.ErrAgentNotReady
	}
	marked := w.p.Paste()
	text = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' && !marked:
			return ' ' // with no marks a line break would be an Enter
		case r == '\n' || r == '\t':
			return r
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, strings.ReplaceAll(text, "\r\n", "\n"))
	if marked {
		text = pasteOpen + text + pasteClose
	}
	if err := w.p.Type([]byte(text)); err != nil {
		return err
	}
	// A question that came up meanwhile would take the Enter for its answer.
	if time.Sleep(enterAfter); w.look(time.Now()).Status == contract.StatusBlocked {
		return contract.ErrAgentBlocked
	}
	return w.p.Type([]byte("\r"))
}
