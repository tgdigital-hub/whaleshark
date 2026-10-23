package lock

import (
	"cmp"
	"fmt"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Command is lock: take, release, list and steal.
func Command(c *contract.Call) (any, error) {
	k := c.Kit
	form, name := "", ""
	if len(c.Args) > 0 {
		form = c.Args[0]
	}
	if len(c.Args) > 1 {
		name = c.Args[1]
	}
	switch {
	case form == "list" && len(c.Args) == 1:
		return list(c), nil
	case form != "take" && form != "release" && form != "steal" || len(c.Args) != 2:
		return nil, refuse(contract.ExitUsage, "usage", "lock takes one of take, release and steal with the lock's name, or list.", contract.Lock{}, "whaleshark help lock")
	}
	if err := cli.Valid("id", name, ""); err != nil {
		return nil, refuse(contract.ExitUsage, "invalid_value", "The lock's name "+err.Error()+".", contract.Lock{}, "whaleshark help lock")
	}
	note := ""
	if v := c.Flags["note"]; len(v) > 0 {
		if note = v[0]; cli.Valid("title", note, "") != nil {
			return nil, refuse(contract.ExitUsage, "invalid_value", "--note is one line of 80 characters at most.", contract.Lock{})
		}
	}
	var stale time.Duration
	if v := c.Flags["stale"]; len(v) > 0 {
		d, err := time.ParseDuration(v[0])
		if err != nil || d < time.Minute {
			return nil, refuse(contract.ExitUsage, "invalid_value", "--stale is a time of a minute or more, such as 45m or 2h.", contract.Lock{})
		}
		stale = d
	}
	// A worker holds a lock for its own task; anybody else for the task it names.
	task := ""
	if v := c.Flags["task"]; len(v) > 0 {
		task = v[0]
	}
	if c.Caller.Kind == contract.Worker && c.Run != "" {
		if s, err := k.Store.Read(c.Root, c.Run); err == nil && s.Attempts[c.Caller.Attempt] != nil {
			task = cmp.Or(task, s.Attempts[c.Caller.Attempt].Task)
		}
	}
	w := Want{Name: name, Holder: contract.HolderOf(c.Caller.Kind, c.Run, task), Task: task, Run: c.Run, Note: note, Stale: stale}
	person := c.Caller.Kind == contract.Human
	switch form {
	case "take":
		l, err := Take(k.Platform, c.Root, w, c.Now)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(c.Out, "You hold the lock %s: release it with `whaleshark lock release %s`; it is stale at %s.\n", name, name, clock(l.StaleAt()))
		return l, nil
	case "release":
		lead := ""
		if c.Caller.Kind == contract.Orchestrator || c.Caller.Kind == contract.Leader {
			lead = c.Run
		}
		l, err := Release(k.Platform, c.Root, name, w.Holder, lead, person, c.Now)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(c.Out, "The lock %s is let go.\n", name)
		return l, nil
	}
	// A steal is the lead agent's of its own run, and waits out a pause.
	var s *contract.State
	if c.Run != "" {
		s, _ = k.Store.Read(c.Root, c.Run)
	}
	if err := k.Rules.Allowed(s, c.Caller, "lock", "steal"); err != nil {
		return nil, err
	}
	was, err := Steal(k.Platform, c.Root, w, person, c.Now)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(c.Out, "You hold the lock %s, taken from %s, who held it since %s.\n", name, was.Holder, clock(was.Since))
	return was, nil
}

// list prints every lock that is held or waited for.
func list(c *contract.Call) any {
	locks := contract.ReadLocks(c.Kit.Platform.Peek, c.Root)
	if len(locks) == 0 {
		fmt.Fprintln(c.Out, "No lock is held.")
	}
	for _, l := range locks {
		fmt.Fprintln(c.Out, l.Line(c.Now))
	}
	if locks == nil {
		locks = []contract.Lock{}
	}
	return map[string]any{"locks": locks}
}
