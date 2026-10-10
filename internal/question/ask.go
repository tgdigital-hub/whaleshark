package question

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// How a blocked ask waits (8b): it looks every half second, says that it is
// alive every 15 seconds, and gives up below the ten minutes an agent's tool
// call may take. Variables, so that a test need not wait as long.
var (
	look      = 500 * time.Millisecond
	keepalive = 15 * time.Second
)

const (
	patience = 540 // seconds, when --timeout says nothing
	record   = "state.json"
)

type asked struct {
	ID     string `json:"id"`
	Answer string `json:"answer"`
}

func ask(c *contract.Call) (any, error) {
	limit, id := patience, ""
	if f := c.Flags["timeout"]; f != nil {
		limit, _ = strconv.Atoi(f[len(f)-1])
	}
	if f := c.Flags["resume"]; f != nil {
		if id = f[len(f)-1]; len(c.Args) != 0 || c.Flags["options"] != nil {
			return nil, usage(c, "ask --resume takes the id of the question and nothing else.")
		}
	} else {
		if len(c.Args) != 1 {
			return nil, usage(c, "ask takes one question, or --resume and the id of one.")
		}
		text, err := said(c, "The question", c.Args[0])
		if err != nil {
			return nil, err
		}
		choices, err := options(c)
		if err != nil {
			return nil, err
		}
		s, _, err := change(c, "", func(s *contract.State) (err error) {
			id, err = c.Kit.Rules.Ask(s, c.Caller.Attempt, text, choices, c.Now)
			return err
		})
		if err != nil {
			return nil, err
		}
		if s.Run.Paused != nil && !s.Attempts[c.Caller.Attempt].Gated {
			fmt.Fprintln(c.Err, contract.PausedReply)
		}
		unread(c, s)
	}
	return wait(c, id, time.Duration(limit)*time.Second)
}

// wait blocks until the answer to a question of the calling attempt may be
// used, and hands it over: from then on it is final. It holds the question's
// lock, which is how answer knows that somebody is waiting, and it reads the
// record only when the file has changed. An answer that is still settling,
// or given while all work is paused, is not returned (8n, 8o).
func wait(c *contract.Call, id string, limit time.Duration) (any, error) {
	k, lock := c.Kit, askLock(c, id)
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		return nil, err
	}
	// An answer looks at this lock for a moment to see whether it is held.
	unlock, free, err := k.Platform.TryLock(lock)
	for tries := 0; err == nil && !free && tries < 3; tries++ {
		time.Sleep(look / 5)
		unlock, free, err = k.Platform.TryLock(lock)
	}
	if err != nil {
		return nil, err
	}
	if !free {
		return nil, &contract.Refusal{Exit: contract.ExitRefused, Code: "another_waiter", Message: "Another ask is waiting for " + id + " already."}
	}
	defer unlock()

	var seen os.FileInfo
	var q *contract.Question
	file, paused := filepath.Join(k.Store.Dir(c.Root, c.Run), record), false
	for began, beat := c.Now, time.Now(); ; time.Sleep(look) {
		now := contract.Now()
		if at, err := os.Stat(file); err != nil || seen == nil || !os.SameFile(seen, at) || !seen.ModTime().Equal(at.ModTime()) || seen.Size() != at.Size() {
			s, err := k.Store.Read(c.Root, c.Run)
			if err != nil {
				return nil, err
			}
			if q, paused, seen = s.Questions[id], s.Run.Paused != nil, at; q == nil || q.Attempt != c.Caller.Attempt {
				return nil, &contract.Refusal{Exit: contract.ExitMissing, Code: "no_such_question", Message: "This attempt has no question " + id + "."}
			}
		}
		if q.State != contract.QuestionOpen && (q.State != contract.QuestionAnswered || !paused && !now.Before(q.SettlesAt)) {
			answer, ok := "", false
			s, raised, err := change(c, "", func(s *contract.State) (err error) {
				answer, ok, err = k.Rules.Use(s, id, now)
				return err
			})
			if err != nil {
				return nil, err
			}
			if ok {
				if raised {
					unread(c, s)
				}
				fmt.Fprintln(c.Out, cli.Plain(answer))
				return asked{id, answer}, nil
			}
			seen = nil // the record moved on between the look and the lock
		}
		if now.Sub(began) >= limit {
			return nil, &contract.Refusal{Exit: contract.ExitPending, Code: "still_open", Message: id + " is still open.",
				Next: []string{"whaleshark ask --resume " + id}, Data: map[string]string{"id": id}}
		}
		if time.Since(beat) >= keepalive {
			fmt.Fprintf(c.Err, "whaleshark: still waiting for the answer to %s\n", id)
			beat = time.Now()
		}
	}
}
