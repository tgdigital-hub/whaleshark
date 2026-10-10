package inbox

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// keepalive is how often a blocked wait says on standard error that it is
// still there; a test shortens it.
var keepalive = 15 * time.Second

// delivery is what wait hands out: a batch under its id, or no event at all
// when its time ran out.
type delivery struct {
	ID       string           `json:"delivery,omitempty"`
	Replayed bool             `json:"replayed,omitempty"`
	Events   []contract.Event `json:"events"`
}

func refuse(exit int, code, format string, a ...any) *contract.Refusal {
	return &contract.Refusal{Exit: exit, Code: code, Message: fmt.Sprintf(format, a...)}
}

// wait blocks until the run has a batch for the lead agent. It is the one
// waiter of its run, sweeps every five seconds while it blocks, and looks at
// the record only when the file changed or an answer's settling time is up.
func wait(c *contract.Call) (any, error) {
	k := c.Kit
	switch {
	case len(c.Args) > 0:
		r := refuse(contract.ExitUsage, "usage", "wait takes no argument.")
		r.Next = []string{"whaleshark help wait"}
		return nil, r
	case c.Run == "":
		r := refuse(contract.ExitMissing, "no_run", "No run is open in this project.")
		r.Next = []string{`whaleshark run new "<objective>"`}
		return nil, r
	}
	dir := k.Store.Dir(c.Root, c.Run)
	// Whoever asks whether a wait is running takes this lock for an instant.
	unlock, ok, err := k.Platform.TryLock(filepath.Join(dir, contract.WaitLock))
	for tries := 0; err == nil && !ok && tries < 3; tries++ {
		time.Sleep(100 * time.Millisecond)
		unlock, ok, err = k.Platform.TryLock(filepath.Join(dir, contract.WaitLock))
	}
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, refuse(contract.ExitRefused, "another_waiter", "Another waiter is active on run %s.", c.Run)
	}
	defer unlock()

	last := func(flag string) string {
		if v := c.Flags[flag]; len(v) > 0 {
			return v[len(v)-1]
		}
		return ""
	}
	kinds, ack := contract.List(c.Flags["for"]...), last("ack")
	var over, settle <-chan time.Time
	if c.Flags["timeout"] != nil {
		sec, _ := strconv.Atoi(last("timeout"))
		over = time.After(time.Duration(sec) * time.Second)
	}
	var changed <-chan string
	if w, err := k.Platform.Watch(filepath.Join(dir, contract.StateFile)); err == nil {
		defer w.Close()
		changed = w.Changes()
	}
	began, gen := time.Now(), -1
	sweep, alive := time.NewTimer(0), time.NewTicker(keepalive)
	defer sweep.Stop()
	defer alive.Stop()

	for {
		var out delivery
		now := contract.Now()
		// A batch goes into the history first and is dropped from the record
		// second (6.5): cut off between the two it is there twice, which
		// the history's reader leaves out, and never lost.
		if ack != "" {
			s, err := k.Store.Read(c.Root, c.Run)
			if err != nil {
				return nil, err
			}
			if acked, _ := k.Rules.Ack(s, ack); len(acked) > 0 {
				if err := k.Store.Append(c.Root, c.Run, acked); err != nil {
					return nil, err
				}
			}
		}
		err := k.Store.Change(c.Root, c.Run, func(s *contract.State) (err error) {
			if gen >= 0 && s.Run.Gen != gen {
				return refuse(contract.ExitRefused, "replaced", "You have been replaced: run %s is driven from another pane now. Stop.", c.Run)
			}
			if err = k.Rules.Allowed(s, c.Caller, "wait", ""); err != nil {
				return err
			}
			gen = s.Run.Gen
			if ack != "" {
				if _, err = k.Rules.Ack(s, ack); err != nil {
					return err
				}
			}
			b, events, replayed, err := k.Rules.Batch(s, kinds, now)
			if b != nil {
				out = delivery{b.ID, replayed, events}
			}
			// An answer settles by the clock, with no change to any file.
			var in time.Duration
			for _, q := range s.Questions {
				if d := q.SettlesAt.Sub(now); q.State == contract.QuestionAnswered && d > 0 && (in == 0 || d < in) {
					in = d
				}
			}
			if settle = nil; in > 0 && s.Run.Paused == nil {
				settle = time.After(max(in, time.Second))
			}
			return err
		})
		if err != nil {
			return nil, err
		}
		ack = ""
		for again := out.ID != ""; !again; {
			select {
			case <-changed:
				again = true
			case <-settle:
				again = true
			case <-sweep.C:
				swept, _, _ := sweeper{k}.sweep(c.Root, c.Run, contract.Now(), true)
				again = swept.Changed && changed == nil
				sweep.Reset(sweepEvery)
			case <-alive.C:
				fmt.Fprintf(c.Err, "whaleshark wait: still waiting after %d s\n", int(time.Since(began).Seconds()))
			case <-over:
				out.Events, again = []contract.Event{}, true
			}
		}
		if out.Events != nil {
			out.print(c.Out)
			return out, nil
		}
	}
}

func (d delivery) print(w io.Writer) {
	if d.ID == "" {
		fmt.Fprintln(w, "No event yet.\n> whaleshark wait")
		return
	}
	note := " event"
	if len(d.Events) != 1 {
		note += "s"
	}
	if d.Replayed {
		note += ", handed out before and not acknowledged"
	}
	fmt.Fprintf(w, "%s: %d%s\n", d.ID, len(d.Events), note)
	for _, e := range d.Events {
		line := []string{strconv.Itoa(e.Seq), e.Kind, e.Task, e.Attempt, e.Text}
		if len(e.Data) > 0 {
			data, _ := json.Marshal(e.Data)
			line = append(line, string(data))
		}
		// One event is one row, whatever its text holds.
		fmt.Fprintln(w, " ", strings.Join(strings.Fields(cli.Plain(strings.Join(line, " "))), " "))
	}
	fmt.Fprintf(w, "> whaleshark wait --ack %s\n", d.ID)
}
