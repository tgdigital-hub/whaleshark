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
// has come to a question of its own on the way. The text is typed once:
// when the agent does not start, the answer is contract.ErrPromptStalled
// and what was typed stays where it is.
func (w *Watcher) Prompt(text string, timeout time.Duration) error {
	return w.typed(text, timeout, func(s State) bool { return s.Status == Busy })
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
	return w.typed(line, pointWait, func(State) bool { return true })
}

// typed is the one place that types at an agent: the text as one paste,
// then Enter. It looks first, and types nothing at a shell, at an agent
// that has another program in front of it, at one that shows a question,
// or at one that has not been seen ready.
// No control character of the text is typed, so no text can end its own
// paste or press a key.
//
// The Enter goes when the pane shows that the text has arrived, not after
// a fixed pause: an agent still taking a paste in drops the Enter or takes
// it for a line break, and how long it takes depends on the machine's
// load. An agent whose hooks were heard and that was at its prompt then
// shows that it started; while it does not, Enter is pressed again, a few
// times, and the text never. An Enter too many finds an empty input box,
// where every kind does nothing with it; it is never pressed at a question,
// which hooks and screen announce only after the agent was seen at work.
// Where nothing shows a start (a kind without hooks, an agent already at
// work, which keeps the line for later), one Enter is all, and enough says
// which state then ends the wait.
func (w *Watcher) typed(text string, timeout time.Duration, enough func(State) bool) error {
	w.typing.Lock()
	defer w.typing.Unlock()
	end := time.Now().Add(timeout)
	// enter presses Enter and waits for the start it should bring.
	enter := func(shown bool, from uint64, wait time.Duration) error {
		if err := w.p.Type([]byte("\r")); err != nil {
			return err
		}
		return w.until(min(wait, time.Until(end)), contract.ErrPromptStalled, func(s State) (bool, error) {
			w.mu.Lock()
			defer w.mu.Unlock()
			return w.starts != from || !shown && (enough(s) || s.Status == contract.StatusWorking || s.Status == contract.StatusBlocked), nil
		})
	}
	shown, from, err := w.open()
	// A text that was typed and never taken gets its Enter before another
	// is typed, and none is put beside it while it lies there.
	if err == nil && shown && w.lies != "" && strings.Count(squeeze(w.p.Text()), w.lies) > w.liesHad {
		if err := enter(true, from, againAfter); err != nil {
			return err
		}
		shown, _, err = w.open()
	}
	if w.lies = ""; err != nil {
		return err
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
	head := []rune(squeeze(text))
	head = head[:min(len(head), 24)]
	had, n := strings.Count(squeeze(w.p.Text()), string(head)), w.p.Count()
	if marked {
		text = pasteOpen + text + pasteClose
	}
	if err := w.p.Type([]byte(text)); err != nil {
		return err
	}
	w.arrive(string(head), had, n)
	// A question that came up meanwhile would take the Enter for its answer.
	s, from := w.seen()
	if s.Status == contract.StatusBlocked {
		return contract.ErrAgentBlocked
	}
	for left, gap := enters, againAfter; ; left, gap = left-1, 2*gap {
		if left == 1 || !shown {
			gap = timeout
		}
		err := enter(shown, from, gap)
		if _, now := w.seen(); err != nil && now != from {
			return nil // it started in the instant the wait ended
		}
		if !errors.Is(err, contract.ErrPromptStalled) || left == 1 || !shown || !time.Now().Before(end) {
			if err != nil && shown {
				w.lies, w.liesHad = string(head), had
			}
			return err
		}
	}
}

// open looks whether the agent may be typed at: whether it shows a start
// (its hooks were heard and it is at its prompt), and how often it has
// started so far.
func (w *Watcher) open() (bool, uint64, error) {
	select {
	case <-w.done:
		return false, 0, contract.ErrNoPane
	default:
	}
	s, from := w.seen()
	w.mu.Lock()
	away, shown := w.away, w.hook != "" && s.Status == contract.StatusIdle
	w.mu.Unlock()
	// An agent of a kind whose screen is known must have shown itself at its
	// prompt or at work: anything else may be a question the words do not cover.
	sure := s.Status == contract.StatusIdle || s.Status == contract.StatusWorking
	switch {
	case s.Status == contract.StatusBlocked:
		return shown, from, contract.ErrAgentBlocked
	case s.Agent == "" || away && s.Agent == w.own || kinds[s.Agent].working != "" && !sure:
		return shown, from, contract.ErrAgentNotReady
	}
	return shown, from, nil
}

// seen is the state now and how often the agent has started on something.
func (w *Watcher) seen() (State, uint64) {
	s := w.look(time.Now())
	w.mu.Lock()
	defer w.mu.Unlock()
	return s, w.starts
}

// squeeze is a text without its spaces and line breaks: a screen breaks a
// long line where the text has none.
func squeeze(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text)
}

// arrive waits until the pane shows the text just pasted: its beginning
// once more than before, or, for an agent that shows a long paste as a
// note of its own, something printed and then nothing. It gives up after
// arriveWait, and rests a moment either way: the Enter must not be read in
// one go with the paste's end.
func (w *Watcher) arrive(head string, had int, n uint64) {
	at := time.Now()
	for last, end := n, at.Add(arriveWait); time.Now().Before(end); time.Sleep(every / 4) {
		if now := w.p.Count(); now != last {
			if last, at = now, time.Now(); head != "" && strings.Count(squeeze(w.p.Text()), head) > had {
				break
			}
		} else if now != n && time.Since(at) >= stillAfter {
			break
		}
	}
	time.Sleep(enterAfter)
}
