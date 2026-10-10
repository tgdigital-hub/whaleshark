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

// patience is how long a blocked command waits: 540 seconds when --timeout
// says nothing.
func patience(c *contract.Call) time.Duration {
	limit := 540
	if f := c.Flags["timeout"]; f != nil {
		limit, _ = strconv.Atoi(f[len(f)-1])
	}
	return time.Duration(limit) * time.Second
}

type asked struct {
	ID     string `json:"id"`
	Answer string `json:"answer"`
}

func ask(c *contract.Call) (any, error) {
	token, err := contract.ReadToken(c.Kit.Platform.Read, c.Kit.Store.Dir(c.Root, c.Run), c.Caller.Attempt)
	if err != nil {
		return nil, err
	}
	if f := c.Flags["resume"]; f != nil {
		if len(c.Args) != 0 || c.Flags["options"] != nil {
			return nil, usage(c, "ask --resume takes the id of the question and nothing else.")
		}
		// The second time the time is up there is no third (8b step 6): a
		// question left open overnight costs two turns.
		return wait(c, f[len(f)-1], token, " Stop now and wait: you are told in this tab when it is answered.")
	}
	id := ""
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
		if err = c.Kit.Rules.Token(s, c.Caller.Attempt, token); err == nil {
			id, err = c.Kit.Rules.Ask(s, c.Caller.Attempt, text, choices, c.Now)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if s.Run.Paused != nil && !s.Attempts[c.Caller.Attempt].Gated {
		fmt.Fprintln(c.Err, contract.PausedReply)
	}
	unread(c, s)
	return wait(c, id, token, "", "whaleshark ask --resume "+id)
}

// wait blocks until the answer to a question of the calling attempt, or to
// an item of the lead agent's, which has no token, may be used, and hands it
// over: from then on it is final. It holds the question's lock, which is how
// answer knows that somebody is waiting, and it reads the record only when
// the file has changed. An answer that is still settling, or given while all
// work is paused, is not returned (8n, 8o). When the time is up it says
// hint, and next is what to run then.
func wait(c *contract.Call, id, token, hint string, next ...string) (any, error) {
	k, lock, limit := c.Kit, askLock(c, id), patience(c)
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
	file, paused, late := filepath.Join(k.Store.Dir(c.Root, c.Run), contract.StateFile), false, false
	for began, beat := c.Now, time.Now(); ; time.Sleep(look) {
		now := contract.Now()
		if at, err := os.Stat(file); err != nil || seen == nil || !os.SameFile(seen, at) || !seen.ModTime().Equal(at.ModTime()) || seen.Size() != at.Size() {
			s, err := k.Store.Read(c.Root, c.Run)
			if err == nil && token != "" {
				err = k.Rules.Token(s, c.Caller.Attempt, token)
			}
			if err != nil {
				return nil, err
			}
			if q, paused, seen = s.Questions[id], s.Run.Paused != nil, at; q == nil || q.Attempt != c.Caller.Attempt {
				return nil, &contract.Refusal{Exit: contract.ExitMissing, Code: "no_such_question", Message: "This attempt has no question " + id + "."}
			}
		}
		if !late && q.State == contract.QuestionOpen && q.For == contract.ForOrchestrator && now.Sub(q.CreatedAt) >= overdue {
			// The lead agent has left it for ten minutes: from now on it is
			// an item for the person, who is called for it here, since
			// nobody else may be looking (8e).
			late = true
			k.Notifier.Items(c.Root, c.Run, nil, now)
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
				if raised && token != "" {
					unread(c, s)
				}
				fmt.Fprintln(c.Out, cli.Plain(answer))
				return asked{id, answer}, nil
			}
			seen = nil // the record moved on between the look and the lock
		}
		if now.Sub(began) >= limit {
			return nil, &contract.Refusal{Exit: contract.ExitPending, Code: "still_open", Message: id + " is still open." + hint,
				Next: next, Data: map[string]string{"id": id}}
		}
		if time.Since(beat) >= keepalive {
			fmt.Fprintf(c.Err, "whaleshark: still waiting for the answer to %s\n", id)
			beat = time.Now()
		}
	}
}
