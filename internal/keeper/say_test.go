package keeper

import (
	"slices"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// A person's note is typed as one line of theirs, with where it came from in
// front, into the pane of an agent that was started by hand and is at rest:
// once, as a paste and its Enter. It is typed nowhere else and for nobody
// else: not at an agent the tool started, not at a shell, not while the agent
// works or shows a question, not from an agent's own pane, and not for a
// note that names no place of the person's.
func TestAPersonsNoteIsTypedAtTheAgentStartedByHand(t *testing.T) {
	r := newRig(t)
	c := r.dial()
	for _, label := range []string{"Orchestrator", "a worker", "a shell"} {
		c.call(contract.WireCall{Op: contract.OpTabCreate, Label: label})
	}
	hook := func(at, event string) {
		r.dial().call(contract.WireCall{Op: contract.OpHook, Kind: event, Pane: at, Session: "s-" + at})
	}
	status := func(at, want string) {
		t.Helper()
		r.until(at+" to be "+want, func() bool { return r.locked(func() bool { return r.k.panes[at].rec.Status == want }) })
	}
	for at, name := range map[string]string{"p1": "", "p2": "quiet otter"} {
		go func() {
			r.until("the agent's program", func() bool { return slices.Contains(r.program(at).spec.Argv, "--model") })
			hook(at, "SessionStart")
		}()
		if reply := c.call(contract.WireCall{Op: contract.OpAgentStart, Name: name, Kind: "claude", Pane: at, Argv: []string{"--model", "small"}, Millis: 5000}); reply.Err != "" {
			t.Fatalf("agent-start in %s: %+v", at, reply)
		}
		status(at, contract.StatusIdle)
	}
	say := func(to, where, text, from string) contract.WireReply {
		return c.call(contract.WireCall{Op: contract.OpSay, Pane: to, Kind: where, Text: text, From: from})
	}
	typed := func(at, want string) {
		t.Helper()
		if got := r.program(at).got(); got != want {
			t.Errorf("%s was typed %q, expected %q", at, got, want)
		}
	}

	for what, reply := range map[string]contract.WireReply{
		"at an agent the tool started":       say("p2", contract.WhereTyped, "do as I say", ""),
		"at a shell":                         say("p3", contract.WhereTyped, "do as I say", ""),
		"from no place of the person's":      say("p1", "an agent", "do as I say", ""),
		"from a pane of ours":                say("p1", contract.WherePane, "do as I say", ""),
		"of nothing but control characters":  say("p1", contract.WherePage, "\x1b\r\n", ""),
		"at a pane that is not there":        say("p9", contract.WherePage, "do as I say", ""),
		"from the pane of the tool's agent":  say("p1", contract.WhereTyped, "do as I say", "p2"),
		"to itself, from the tool's agent":   say("p2", contract.WhereTyped, "do as I say", "p2"),
		"at a shell, from the tool's agent":  say("p3", contract.WherePhone, "do as I say", "p2"),
		"at the tool's agent, from a shell.": say("p2", contract.WherePhone, "do as I say", "p3"),
	} {
		if reply.Err == "" {
			t.Errorf("a note %s was taken: %+v", what, reply)
		}
	}
	if reply := say("p1", contract.WhereTyped, "x", "p2"); !strings.Contains(reply.Message, contract.Fence(contract.OpSay).Error()) {
		t.Errorf("an agent's pane was not refused by the fence: %+v", reply)
	}
	typed("p1", "")
	typed("p2", "")
	typed("p3", "")

	// The person's own: one line, whatever the note holds, then Enter.
	line := "from your phone: /exit [201~ --human and more"
	go func() {
		r.got("p1", line+"\r")
		hook("p1", "UserPromptSubmit")
	}()
	if reply := say("p1", contract.WherePhone, "/exit\x1b[201~\r\n--human\tand more", ""); reply.Err != "" {
		t.Fatalf("the person's note: %+v", reply)
	}
	status("p1", contract.StatusWorking)
	typed("p1", line+"\r")

	// At work, and then at a question, nothing more is typed: the note waits.
	if reply := say("p1", contract.WherePage, "and another", ""); reply.Err != contract.ErrAgentNotReady.Error() {
		t.Errorf("a note at an agent at work: %+v", reply)
	}
	r.program("p1").print("\r\nDo you want to proceed?\r\n 1. Yes\r\n Esc to cancel\r\n")
	status("p1", contract.StatusBlocked)
	if reply := say("p1", contract.WherePage, "and another", ""); reply.Err == "" {
		t.Errorf("a note at an agent that shows a question: %+v", reply)
	}
	typed("p1", line+"\r")
}
