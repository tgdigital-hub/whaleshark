// Package mail is the messages down to a worker and the person's note to the
// lead agent: tell, mail and reject. A message is stored in the record and
// read by the worker with its own command; what is typed into a tab is never
// more than the one fixed line that says a message is there.
package mail

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Plug binds this package's handlers.
func Plug(k *contract.Kit) {
	k.Handle("tell", tell)
	k.Handle("mail", read)
	k.Handle("reject", reject)
}

// What became of the pointer.
const (
	typed    = "typed"
	deferred = "deferred"
	skipped  = "skipped"
)

// sent is what tell and reject answer with. Status is the task's after a
// reject; Why says what a pointer that was not typed waits for.
type sent struct {
	Task    string              `json:"task"`
	Attempt string              `json:"attempt,omitempty"`
	Seq     int                 `json:"seq,omitempty"`
	Body    string              `json:"body,omitempty"`
	Status  contract.TaskStatus `json:"status,omitempty"`
	Pointer string              `json:"pointer,omitempty"`
	Why     string              `json:"why,omitempty"`
}

// words takes a command's two arguments, a task and a text, through the validator.
func words(c *contract.Call) (task, text string, err error) {
	bad := &contract.Refusal{Exit: contract.ExitUsage, Code: "usage",
		Message: "Usage: whaleshark " + c.Command.Usage, Next: []string{"whaleshark help " + c.Command.Name}}
	if len(c.Args) != 2 || strings.TrimSpace(c.Args[1]) == "" {
		return "", "", bad
	}
	for i, kind := range []string{"task", "text"} {
		if err := cli.Valid(kind, c.Args[i], ""); err != nil {
			bad.Code, bad.Message = "invalid_value", err.Error()+"."
			return "", "", bad
		}
	}
	return c.Args[0], strings.TrimSpace(c.Args[1]), nil
}

// current is a task, found by its id or its name, and its last attempt.
func current(k *contract.Kit, s *contract.State, task string) (*contract.Task, *contract.Attempt, error) {
	t, err := k.Rules.FindTask(s, task)
	if err != nil || len(t.Attempts) == 0 {
		return t, nil, err
	}
	return t, s.Attempts[t.Attempts[len(t.Attempts)-1]], nil
}

// file puts a message of more than one line under msg/ in the run's folder,
// readable by the login only and named by the number the message is about to
// get, and returns its first line and the path. It is called inside the
// store's change, so the record never names a file that is not there yet; a
// file left by a change that was not saved is written over by the message
// that gets its number.
func file(c *contract.Call, s *contract.State, text string) (line, path string, err error) {
	line, _, long := strings.Cut(text, "\n")
	if !long {
		return text, "", nil
	}
	dir := filepath.Join(c.Kit.Store.Dir(c.Root, c.Run), "msg")
	if err = os.MkdirAll(dir, 0o700); err == nil {
		path = filepath.Join(dir, strconv.Itoa(s.Counters.Seq+1)+".md")
		err = os.WriteFile(path, []byte(text+"\n"), 0o600)
	}
	return strings.TrimSpace(line), path, err
}

func tell(c *contract.Call) (any, error) {
	task, text, err := words(c)
	if err != nil {
		return nil, err
	}
	k, out, sub := c.Kit, &sent{Task: task}, ""
	if task == contract.Lead {
		sub = task
	}
	var a contract.Attempt // the worker's attempt as the change left it
	var pane string        // the lead agent's
	var unread int
	err = k.Store.Change(c.Root, c.Run, func(s *contract.State) error {
		if err := k.Rules.Allowed(s, c.Caller, c.Command.Name, sub); err != nil {
			return err
		}
		line, path, err := file(c, s, text)
		if err != nil {
			return err
		}
		if out.Seq, err = k.Rules.Tell(s, task, line, path, c.Now); err != nil {
			if path != "" {
				os.Remove(path)
			}
			return err
		}
		out.Body, unread = path, len(s.Inbox.Events)
		if o := s.Run.Orchestrator; o != nil {
			pane = o.Pane
		}
		if sub == "" {
			t, cur, _ := current(k, s, task)
			a, out.Task, out.Attempt = *cur, t.ID, cur.ID
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	now, quiet := c.Flags["now"] != nil, c.Flags["quiet"] != nil
	if sub == "" {
		err = toWorker(c, out, &a, now, quiet)
	} else {
		toLead(c, out, pane, unread, now, quiet)
	}
	fmt.Fprintf(c.Out, "Stored as message %d for %s. %s\n", out.Seq, out.Task, out.said())
	return out, err
}

func (o *sent) said() string {
	if o.Why == "" {
		return "Pointer " + o.Pointer + "."
	}
	return "Pointer " + o.Pointer + ": " + o.Why + "."
}

// toWorker types the one line that says a message is waiting, when the agent
// is idle or the caller said now, and stamps the message. Otherwise the line
// is left to the sweep, which types it once the agent is idle. The agent's
// state is the one the last sweep saw.
func toWorker(c *contract.Call, out *sent, a *contract.Attempt, now, quiet bool) error {
	stamp := func() error {
		return c.Kit.Store.Change(c.Root, c.Run, func(s *contract.State) error {
			return c.Kit.Rules.Pointed(s, a.ID, out.Seq, c.Now)
		})
	}
	switch {
	case quiet:
		// Stamped as pointed at, so that no sweep types the line later.
		out.Pointer, out.Why = skipped, "--quiet"
		return stamp()
	case a.State == contract.AttemptReported || a.State == contract.AttemptChecking:
		out.Pointer, out.Why = skipped, "it has reported and reads this if its result is sent back"
	case a.State == contract.AttemptStarting:
		out.Pointer, out.Why = deferred, "until it has started"
	case !now && !idle(a.Seen.Status):
		out.Pointer, out.Why = deferred, "until it is idle"
	default:
		if out.Pointer, out.Why = point(c, a.Place.Pane, contract.PointMail, ""); out.Pointer == typed {
			return stamp()
		}
	}
	return nil
}

// toLead types the pointer into the lead agent's tab when no wait of its is
// running to hand the note over and its pane is idle, as every command that
// raises an event does. Best effort: the note is in the inbox either way, and
// the sweep points an idle lead agent at what it has not fetched.
func toLead(c *contract.Call, out *sent, pane string, unread int, now, quiet bool) {
	if quiet {
		out.Pointer, out.Why = skipped, "--quiet"
		return
	}
	k := c.Kit
	unlock, free, err := k.Platform.TryLock(filepath.Join(k.Store.Dir(c.Root, c.Run), contract.WaitLock))
	if free {
		unlock()
	}
	switch {
	case err != nil:
		out.Pointer, out.Why = deferred, err.Error()
	case !free:
		out.Pointer, out.Why = skipped, "its wait hands the note over"
	case !now && !idleAt(k, pane):
		out.Pointer, out.Why = deferred, "until it is idle"
	default:
		out.Pointer, out.Why = point(c, pane, contract.PointEvents, strconv.Itoa(unread))
	}
}

func idle(status string) bool { return status == contract.StatusIdle }

// idleAt asks the terminals whether the agent in a pane is idle now.
func idleAt(k *contract.Kit, pane string) bool {
	snap, err := k.Terms.Snapshot(context.Background())
	return err == nil && slices.ContainsFunc(snap.Panes, func(p contract.Pane) bool { return p.ID == pane && p.Agent != "" && idle(p.Status) })
}

// point types one fixed line into a pane and says how it went.
func point(c *contract.Call, pane string, p contract.Pointer, arg string) (how, why string) {
	switch err := c.Kit.Terms.Point(pane, p, arg); {
	case err == nil:
		return typed, ""
	case errors.Is(err, contract.ErrAgentBlocked):
		return deferred, "the agent is at a prompt of its own"
	default:
		return deferred, err.Error()
	}
}

// inbox is what mail answers with.
type inbox struct {
	Paused   bool            `json:"paused,omitempty"`
	Messages []contract.Mail `json:"messages"`
}

// read is the worker's mail command: the unread messages, which are then
// marked read, for a caller that holds the attempt's token. With nothing
// unread the record is not written. While the
// person has paused all work it says so and hands nothing over, so that a
// message is never taken for the end of the pause.
func read(c *contract.Call) (any, error) {
	k, id, out := c.Kit, c.Caller.Attempt, &inbox{Messages: []contract.Mail{}}
	s, err := k.Store.Read(c.Root, c.Run)
	if err == nil {
		err = k.Rules.Allowed(s, c.Caller, c.Command.Name, "")
	}
	if err != nil {
		return nil, err
	}
	a := s.Attempts[id]
	token, err := contract.ReadToken(k.Platform.Read, k.Store.Dir(c.Root, c.Run), id)
	if err == nil {
		err = k.Rules.Token(s, id, token)
	}
	if err != nil {
		return nil, err
	}
	if out.Paused = s.Run.Paused != nil; out.Paused {
		fmt.Fprintln(c.Out, contract.PausedReply)
		return out, nil
	}
	if !slices.ContainsFunc(a.Mail, func(m contract.Mail) bool { return m.ReadAt.IsZero() }) {
		fmt.Fprintln(c.Out, "No new messages.")
		return out, nil
	}
	err = k.Store.Change(c.Root, c.Run, func(s *contract.State) error {
		out.Messages, err = k.Rules.ReadMail(s, id, c.Now)
		return err
	})
	for _, m := range out.Messages {
		fmt.Fprintf(c.Out, "[%d] %s\n", m.Seq, m.Text)
		if m.Body != "" {
			fmt.Fprintf(c.Out, "    That is its first line. Read all of it: %s\n", m.Body)
		}
	}
	return out, err
}

// reject sends a result back. With the agent still there the same attempt
// works on, told why by mail and by the pointer; with the agent proven gone
// the task is ready again and the reason goes into the next prompt; when the
// last sweep could not say which, the rules refuse.
func reject(c *contract.Call) (any, error) {
	task, why, err := words(c)
	if err != nil {
		return nil, err
	}
	k, out := c.Kit, &sent{}
	var a contract.Attempt
	err = k.Store.Change(c.Root, c.Run, func(s *contract.State) error {
		if err := k.Rules.Allowed(s, c.Caller, c.Command.Name, ""); err != nil {
			return err
		}
		t, cur, err := current(k, s, task)
		if err != nil {
			return err
		}
		seen := contract.Unverifiable
		if cur != nil {
			seen = cur.Seen.Liveness
		}
		if err := k.Rules.Reject(s, task, why, seen, c.Now); err != nil {
			return err
		}
		a, out.Task, out.Attempt, out.Status = *cur, t.ID, cur.ID, t.Status
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out.Status != contract.TaskRunning {
		fmt.Fprintf(c.Out, "%s is %s again: its agent is gone. The reason is kept for the next start.\n", out.Task, out.Status)
		return out, nil
	}
	out.Seq = a.Mail[len(a.Mail)-1].Seq
	err = toWorker(c, out, &a, false, false)
	fmt.Fprintf(c.Out, "%s is back with %s as message %d. %s\n", out.Task, out.Attempt, out.Seq, out.said())
	return out, err
}
