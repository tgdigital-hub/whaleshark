package contract

import (
	"errors"
	"path/filepath"
	"strings"
	"unicode"
)

// A person's note typed into the lead agent's tab (18.1, 20.20): a real
// agent acts on a line of its own chat and declines what a command prints.
// Only a note that came as the person is typed, only into the tab of the
// agent the person talks to, as one line that says first where it came from;
// the event stays as the record.

// SayMax is the most characters of a note that are typed; SayLock is the
// file in a run's folder that whoever types its notes holds.
const (
	SayMax  = 2000
	SayLock = "say.lock"
	sayCut  = " … (cut here; the whole note: whaleshark wait)"
)

// sayFrom is how the typed line begins, by where the note came from. A note
// from anywhere else is never typed, and no line begins with a note's own words.
var sayFrom = map[string]string{WhereTyped: "typed at a terminal: ", WherePage: "from the page: ", WherePhone: "from your phone: "}

// Note is a person's note that waits to be typed: the event it is the text
// of, where it came from, and the text as SayText left it.
type Note struct {
	Seq   int    `json:"seq"`
	Where string `json:"where"`
	Text  string `json:"text"`
}

// SayText is a note as it is typed: one line of printable characters, every
// control character, line break and invisible mark a space, SayMax at most.
func SayText(text string) string {
	line := strings.Join(strings.FieldsFunc(text, func(r rune) bool { return r == ' ' || !unicode.IsPrint(r) }), " ")
	if r, cut := []rune(line), []rune(sayCut); len(r) > SayMax {
		line = string(r[:SayMax-len(cut)]) + sayCut
	}
	return line
}

// Said is the whole line for a note from where, and empty for a note that is
// not typed: one from no place of the person's, or with nothing to say.
func Said(where, text string) string {
	head, line := sayFrom[where], SayText(text)
	if head == "" || line == "" {
		return ""
	}
	return head + line
}

var errSaid = errors.New("no note waits")

// Say types the notes of a run that wait, the oldest first, and answers how
// many still wait and what the first of them waits for. One caller at a time
// does it: wait says whether to stand in line or to leave it to whoever is
// at it. A note leaves the record before it is typed and comes back only when
// the keeper says that nothing was typed, so none is typed twice; and none
// is typed while the person has paused the job.
func Say(k *Kit, root, run string, wait bool) (left int, why error) {
	path := filepath.Join(k.Store.Dir(root, run), SayLock)
	unlock, free, err := k.Platform.TryLock(path)
	if err == nil && !free && wait {
		unlock, err = k.Platform.Lock(path, true)
		free = err == nil
	}
	if err != nil || !free {
		return -1, err
	}
	defer unlock()
	for {
		var n Note
		pane := ""
		err := k.Store.Change(root, run, func(s *State) error {
			switch left = len(s.Inbox.Say); {
			case left == 0:
				return errSaid
			case s.Run.Paused != nil:
				return errors.New("the person has paused all work")
			case s.Run.Orchestrator == nil:
				return errors.New("no tab of the lead agent's is known")
			}
			n, pane, s.Inbox.Say = s.Inbox.Say[0], s.Run.Orchestrator.Pane, s.Inbox.Say[1:]
			return nil
		})
		if errors.Is(err, errSaid) {
			return 0, nil
		} else if err != nil {
			return left, err
		}
		// Typed and not yet taken is typed: the keeper presses its Enter
		// before it types anything else there.
		if err = k.Terms.Say(pane, n.Where, n.Text); err == nil || errors.Is(err, ErrPromptStalled) {
			continue
		}
		k.Store.Change(root, run, func(s *State) error {
			s.Inbox.Say = append([]Note{n}, s.Inbox.Say...)
			left = len(s.Inbox.Say)
			return nil
		})
		return left, err
	}
}
