package scenario

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/engine"
	"github.com/tgdigital-hub/whaleshark/test/fakeengine"
)

// session makes one round of calls through the interface and writes down
// every answer and, at the end, every event, without what differs from one
// start of a keeper to the next: the time, the instance, a terminal's id.
// What a pane's program is doing the keeper finds on a clock of its own, so
// the word for it and the events that only bring it are left out too; and
// so is the event that brings a session, which is the program's own word:
// the agent run in a pane says it when it has got that far, before the
// pane is closed or after, and on the double a run starts no program. The
// dock is in both pictures and answers alike; the events of its program's
// starts are left out, since the keeper starts that program on its own
// clock, again whenever it ends, and the double starts none. second is the
// folder of a second workspace: both backends keep workspaces, and the log
// holds each pane's, every picture's list of them and every change to it.
func session(t *testing.T, terms contract.Terminals, root, second string) []string {
	t.Helper()
	var log []string
	say := func(what string, err error, answer ...any) {
		log = append(log, strings.TrimSpace(fmt.Sprintf("%s: %s %q", what, fmt.Sprint(answer...), contract.ErrCode(err))))
	}
	show := func(p contract.Pane) string {
		return fmt.Sprintf("%s in %s of %s %q focused=%v", p.ID, p.Tab, p.Workspace, p.Label, p.Focused)
	}
	spaces := func(list []contract.Space) (out string) {
		for _, sp := range list {
			out += fmt.Sprintf(" %s %q named=%v shows %s front=%v;", sp.ID, sp.Name, sp.Named, sp.Tab, sp.Front)
		}
		return out
	}
	picture := func(what string, s *contract.Snapshot, err error) {
		if say(what, err); s != nil {
			for _, p := range s.Panes {
				log = append(log, "  "+show(p))
			}
			log = append(log, "  workspaces:"+spaces(s.Spaces))
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first, events, err := terms.Events(ctx)
	picture("events", first, err)
	agent, err := exec.LookPath("fakeagent")
	if err != nil {
		t.Fatal(err)
	}

	p, err := terms.TabCreate("", "", root, "third", []string{"A=1"})
	say("tab-create", err, show(p))
	p, err = terms.Split("p3", contract.Right, 0.5)
	say("split right", err, show(p))
	say("tab-rename", terms.TabRename("t3", "renamed"))
	say("tab-focus", terms.TabFocus("t3"))
	for _, c := range [][2]string{{"p4", ""}, {"p1", ""}, {"p1", contract.Left}, {"p9", ""}} {
		to, err := terms.PaneFocus(c[0], c[1])
		say("pane-focus "+c[0]+" "+c[1], err, to)
	}
	p, err = terms.Split("p3", contract.Down, 0.5)
	say("split down", err, show(p))
	say("swap", terms.Swap("p3", "p4"))
	say("resize", terms.Resize("p3", contract.Right, 0.1))
	say("resize a pane that is not there", terms.Resize("p9", contract.Right, 0.1))
	_, _, err = terms.Size("p3")
	say("size", err)
	_, _, err = terms.Size("p9")
	say("size of a pane that is not there", err)
	_, err = terms.Screen("p3")
	say("screen", err)
	_, err = terms.Screen("p9")
	say("screen of a pane that is not there", err)
	say("run", terms.Run("p5", []string{agent, "hang"}))
	say("run nothing", terms.Run("p5", nil))
	say("run in a pane that is not there", terms.Run("p9", []string{agent}))
	_, err = terms.Split("p9", contract.Down, 0.5)
	say("split a pane that is not there", err)
	say("pane-close", terms.PaneClose("p4"))
	say("pane-close again", terms.PaneClose("p4"))
	say("tab-rename a tab that is not there", terms.TabRename("t9", "x"))
	say("tab-focus a tab that is not there", terms.TabFocus("t9"))
	say("tab-close a tab that is not there", terms.TabClose("t9"))
	reason, delivery, err := terms.Notify("a notice", "with no window to show it", false)
	say("notify", err, reason, " ", delivery)
	say("set-keys", terms.SetKeys([]contract.KeyEntry{{Key: "shift+a", Type: "shell", Argv: []string{"whaleshark", "pause"}}}))
	dock := first.Panes[0].ID
	for _, word := range []string{contract.DockKeys, contract.DockBack, "away"} {
		say("dock "+word, terms.Dock(word))
	}
	say("close the dock", terms.PaneClose(dock))
	_, err = terms.Split(dock, contract.Right, 0.5)
	say("split the dock", err)
	say("swap with the dock", terms.Swap("p3", dock))
	say("run in the dock", terms.Run(dock, []string{agent}))
	_, err = terms.PaneFocus(dock, "")
	say("the keys to the dock as to a pane", err)
	_, err = terms.Screen(dock)
	say("the dock's screen", err)
	_, _, err = terms.Size(dock)
	say("the dock's size", err)
	// The fence round the pane of an agent the tool started, and the one way
	// through it: a tab that pane opened itself. How long an agent takes to
	// start is each backend's own, so only the fence's word is noted.
	raw := terms.(interface {
		Call(context.Context, contract.WireCall) (contract.WireReply, error)
	})
	terms.AgentStart("ann", "claude", "p3", nil, time.Second)
	fence := func(what string, c contract.WireCall) contract.WireReply {
		c.From = "p3"
		r, err := raw.Call(ctx, c)
		log = append(log, fmt.Sprintf("%s, from an agent's pane: refused by the fence=%v", what, err != nil && strings.Contains(err.Error(), contract.Fence(c.Op).Error())))
		return r
	}
	own := fence("tab-create", contract.WireCall{Op: contract.OpTabCreate, Label: "its own", Cwd: root}).Pane
	fence("agent-start in its own tab", contract.WireCall{Op: contract.OpAgentStart, Pane: own.ID, Kind: "claude", Name: "bob", Millis: 1000})
	fence("agent-start in a person's tab", contract.WireCall{Op: contract.OpAgentStart, Pane: "p2", Kind: "claude", Name: "cy", Millis: 1000})
	fence("prompt to its own agent", contract.WireCall{Op: contract.OpPrompt, Name: "bob", Text: "x", Millis: 1000})
	fence("prompt to itself", contract.WireCall{Op: contract.OpPrompt, Name: "ann", Text: "x", Millis: 1000})
	fence("run in its own tab", contract.WireCall{Op: contract.OpRun, Pane: own.ID, Argv: []string{agent, "hang"}})
	fence("pane-close in a person's tab", contract.WireCall{Op: contract.OpPaneClose, Pane: "p2"})
	fence("tab-close of the lead agent's tab", contract.WireCall{Op: contract.OpTabClose, Tab: "t1"})
	fence("tab-focus", contract.WireCall{Op: contract.OpTabFocus, Tab: "t1"})
	fence("tab-close of its own tab", contract.WireCall{Op: contract.OpTabClose, Tab: own.Tab})
	// A tab is given by the lead agent's pane or the person, never taken.
	fence("tab-give of a person's tab to itself", contract.WireCall{Op: contract.OpTabGive, Tab: "t2", Target: "p3"})
	say("tab-give a tab that is not there", terms.TabGive("t9", "p3"))
	say("tab-give to a pane that is not there", terms.TabGive("t2", "p9"))
	say("tab-give", terms.TabGive("t2", "p3"))
	// A person's note is for an agent started by hand, and no agent's pane sends one.
	fence("say", contract.WireCall{Op: contract.OpSay, Pane: "p1", Kind: contract.WhereTyped, Text: "x"})
	say("say to an agent the tool started", terms.Say("p3", contract.WhereTyped, "x"))
	say("say to a shell", terms.Say("p2", contract.WherePage, "x"))
	say("say from no place of the person's", terms.Say("p2", "an agent", "x"))
	say("say to a pane that is not there", terms.Say("p9", contract.WherePhone, "x"))
	fence("tab-close of a tab it was given", contract.WireCall{Op: contract.OpTabClose, Tab: "t2"})
	// A second workspace: opened with its first tab and in front, opened
	// again with nothing made, left behind while a tab is opened in it; its
	// tabs are held to its own rules, refuse a screen of ours and are behind
	// the wall for an agent of the first; it is named and gets its folder's
	// name back, and is opened of a folder only; it shows the tab after the
	// one it showed when that is closed, in front the one before when the
	// last is, and it is closed whole. What waits in a tab is the dock's
	// alone to tell.
	from := func(what, pane string, c contract.WireCall) {
		c.From = pane
		_, err := raw.Call(ctx, c)
		say(what, err)
	}
	sp, err := terms.SpaceOpen(second, second)
	say("space-open", err, show(sp))
	sp, err = terms.SpaceOpen(second, root)
	say("space-open again", err, show(sp))
	say("tab-focus of a tab of the first workspace", terms.TabFocus("t1"))
	behind, err := terms.TabCreate(second, "", second, "behind", nil)
	say("tab-create in the workspace behind", err, show(behind))
	say("space-rename of one that is not there", terms.SpaceRename("s9", "x"))
	say("tab-rename to a second main tab", terms.TabRename(behind.Tab, " orchestrator"))
	say("tab-rename", terms.TabRename(behind.Tab, "--still\tbehind, and with a name that is too long"))
	say("space-rename", terms.SpaceRename(sp.Workspace, "  --a name\tof its own, and too long a one"))
	say("space-rename to no name", terms.SpaceRename(sp.Workspace, " "))
	_, err = terms.SpaceOpen(filepath.Join(second, "none"), second)
	say("space-open of a folder that is not there", err)
	_, err = terms.TabCreate(filepath.Join(second, "none"), "", second, "x", nil)
	say("tab-create in a folder that is not there", err)
	_, err = terms.TabCreate(second, "", second, " ORCHESTRATOR ", nil)
	say("tab-create of a second main tab", err)
	last, err := terms.TabCreate(second, "", second, "--a\x1b[2Jlabel\tto clean", nil)
	say("tab-create with a label to clean", err, show(last))
	from("overlay from the workspace behind", behind.ID, contract.WireCall{Op: contract.OpOverlay, Argv: []string{agent, "hang"}})
	from("screen of another workspace's pane, from an agent's", "p3", contract.WireCall{Op: contract.OpScreen, Pane: behind.ID})
	from("size of another workspace's pane, from an agent's", "p3", contract.WireCall{Op: contract.OpSize, Pane: behind.ID})
	from("swap with another workspace's pane, from an agent's", "p3", contract.WireCall{Op: contract.OpSwap, Pane: "p5", Target: behind.ID})
	from("size inside its own workspace", "p3", contract.WireCall{Op: contract.OpSize, Pane: "p5"})
	from("screen of another workspace's pane, from a pane with no agent", "p2", contract.WireCall{Op: contract.OpScreen, Pane: behind.ID})
	log = append(log, fmt.Sprintf("marks from no pane: refused=%v", terms.Marks(map[string]int{behind.Tab: 2}) != nil))
	from("marks from the dock", dock, contract.WireCall{Op: contract.OpMarks, Marks: map[string]int{behind.Tab: 2, "t9": 1}})
	say("tab-focus of the tab behind", terms.TabFocus(behind.Tab))
	say("tab-focus back", terms.TabFocus("t1"))
	say("tab-close of the tab the workspace behind showed", terms.TabClose(behind.Tab))
	say("tab-focus of the tab it shows then", terms.TabFocus(last.Tab))
	say("tab-close of the tab that shows", terms.TabClose(last.Tab))
	say("space-close of the workspace in front", terms.SpaceClose(sp.Workspace))
	say("space-close again", terms.SpaceClose(sp.Workspace))
	say("tab-close", terms.TabClose("t3"))
	end, err := terms.Snapshot(ctx)
	picture("snapshot", end, err)

	for {
		select {
		case e, open := <-events:
			if !open {
				t.Fatal("the events ended before the session did")
			}
			if e.Seq != first.Seq+1 {
				t.Fatalf("event %d came after event %d", e.Seq, first.Seq)
			}
			was := ""
			for _, p := range first.Panes {
				if p.ID == e.Pane.ID {
					was = p.Terminal
				}
			}
			if e.Kind == contract.EvSpace {
				log = append(log, "event space:"+spaces(e.Spaces))
			} else if e.Kind != contract.EvSession && !e.Pane.Dock && (e.Kind != contract.EvState || e.Pane.Terminal != was) {
				log = append(log, fmt.Sprintf("event %s: %s", e.Kind, show(e.Pane)))
			}
			if contract.Apply(first, e); e.Seq < end.Seq {
				continue
			}
		case <-time.After(patience):
			t.Fatalf("the events stopped at number %d of %d", first.Seq, end.Seq)
		}
		break
	}
	return log
}

// The double answers as the keeper does: the same calls, made through the
// adapter to the real keeper and to the double, give the same answers and
// the same events. And the keeper answers every call of the interface: none
// is left that it has not built.
func TestTheDoubleAnswersAsTheKeeper(t *testing.T) {
	p := PrepareOn(t, Keeper, nil, nil)
	second := filepath.Join(t.TempDir(), "second")
	p.must(os.Mkdir(second, 0o700))
	real := session(t, p.Kit.Terms, p.Root, second)

	double := fakeengine.New()
	defer double.Close()
	double.TabCreate(p.Root, "", p.Root, "Lead", nil)
	double.TabCreate(p.Root, "", p.Root, "you", nil)
	if fake := session(t, double, p.Root, second); strings.Join(fake, "\n") != strings.Join(real, "\n") {
		t.Errorf("the double:\n%s\n\nthe keeper:\n%s", strings.Join(fake, "\n"), strings.Join(real, "\n"))
	}
	t.Log("\n" + strings.Join(real, "\n"))

	hook, _ := p.Kit.Terms.(*engine.Client).Call(t.Context(), contract.WireCall{Op: contract.OpHook, Kind: "Stop", Pane: p.Lead})
	for call, err := range map[string]error{
		contract.OpAgentStart: p.Kit.Terms.AgentStart("ann", "claude", p.Lead, nil, time.Second),
		contract.OpPrompt:     p.Kit.Terms.Prompt("ann", "x", time.Second),
		contract.OpPoint:      p.Kit.Terms.Point(p.Lead, contract.PointMail, ""),
		contract.OpSay:        p.Kit.Terms.Say(p.Lead, contract.WhereTyped, "x"),
		contract.OpOverlay:    p.Kit.Terms.Overlay([]string{"whaleshark", "ask"}, 0.5, 0.5),
		contract.OpHook:       contract.ErrOf(hook),
	} {
		if errors.Is(err, contract.ErrNotBuilt) {
			t.Errorf("%s is not answered by the keeper", call)
		}
	}
}

// The person's note through the real program and the real keeper: typed
// into the tab of the agent the person started by hand, as one line that
// says where it came from, once; a second note finds the agent at work,
// waits, and is typed by the next sweep after the agent has come to rest.
func TestAPersonsNoteIsTypedByTheRealKeeper(t *testing.T) {
	p := PrepareOn(t, Keeper, nil, nil)
	p.must(p.Kit.Terms.AgentStart("", "claude", p.Lead, []string{"--state", contract.StatusIdle}, patience))
	lead := func(status string) {
		t.Helper()
		for end := time.Now().Add(patience); ; time.Sleep(20 * time.Millisecond) {
			snap, err := p.Kit.Terms.Snapshot(t.Context())
			if err == nil && slices.ContainsFunc(snap.Panes, func(q contract.Pane) bool { return q.ID == p.Lead && q.Agent != "" && q.Status == status }) {
				return
			} else if time.Now().After(end) {
				t.Fatalf("the lead agent did not come to be %s: %+v, %v", status, snap, err)
			}
		}
	}
	tell := func(who, want, text string) {
		t.Helper()
		if out, err := p.Command(who, "tell", "lead", text).CombinedOutput(); err != nil || !strings.Contains(string(out), want) {
			t.Fatalf("tell lead as %s: %v\n%s", who, err, out)
		}
	}
	shows := func(line string, times int) {
		t.Helper()
		for end := time.Now().Add(patience); ; time.Sleep(20 * time.Millisecond) {
			screen, _ := p.Kit.Terms.Screen(p.Lead)
			if got := strings.Count(strings.Join(strings.Fields(screen), " "), line); got == times {
				return
			} else if time.Now().After(end) {
				t.Fatalf("the lead agent's tab shows %q %d times, expected %d:\n%s", line, got, times, screen)
			}
		}
	}
	lead(contract.StatusIdle)
	if out, err := p.Command(Orch, "run", "new", "a job to talk about").CombinedOutput(); err != nil {
		t.Fatalf("run new: %v\n%s", err, out)
	}
	lead(contract.StatusIdle)
	tell(Human, "It is typed into the lead agent's tab as your own line.", "say hello\x1b[201~\nin one line")
	shows("typed at a terminal: say hello [201~ in one line", 1)
	lead(contract.StatusWorking)
	tell(Page, "It waits, and is typed into the lead agent's tab", "and one from the page")
	p.Clock(6 * time.Second)
	p.Command(Human, "status", "--quiet").Run()
	shows("from the page: and one from the page", 0)

	hook, err := p.Kit.Terms.(*engine.Client).Call(t.Context(), contract.WireCall{Op: contract.OpHook, Kind: "Stop", Pane: p.Lead})
	p.must(errors.Join(err, contract.ErrOf(hook)))
	lead(contract.StatusIdle)
	p.Clock(6 * time.Second)
	if out, err := p.Command(Human, "status", "--quiet").CombinedOutput(); err != nil {
		t.Fatalf("the sweep: %v\n%s", err, out)
	}
	shows("from the page: and one from the page", 1)
	shows("typed at a terminal: say hello [201~ in one line", 1)
	if s, _ := p.Record(); len(s.Inbox.Say) != 0 {
		t.Errorf("a typed note still waits: %+v", s.Inbox.Say)
	}
}

// A start on a loaded machine, through the real keeper: the agent takes the
// pasted prompt in only after a while and drops an Enter that comes sooner.
// The Enter waits until the pane shows the prompt, so the start is sure,
// and no Enter is spent on a paste the agent is still taking in.
func TestAStartWhoseAgentIsSlowToTakeAPasteIn(t *testing.T) {
	p := PrepareOn(t, Keeper, nil, map[string]string{"A.1": `slow-paste 0.7 | write-result | report done "it is in"`})
	run := func(args ...string) string {
		t.Helper()
		out, err := p.Command(Orch, args...).CombinedOutput()
		if err != nil {
			s, _ := p.Record()
			screen := ""
			if a := s.Attempts["A.1"]; a != nil {
				screen, _ = p.Kit.Terms.Screen(a.Place.Pane)
			}
			t.Fatalf("%v: %v\n%s\nthe agent's pane:\n%s", args, err, out, screen)
		}
		return string(out)
	}
	brief := "# Target\na file\n## Change\nwrite it\nConstraints: none\n**Ownership** it\nAcceptance\nit is there\n"
	p.must(os.WriteFile(filepath.Join(p.Root, "b.md"), []byte(brief), 0o600))
	run("run", "new", "one start")
	run("task", "add", "A", "the A part", "--brief", "b.md", "--check", "none")
	if out := run("start", "A"); !strings.Contains(out, "working") {
		t.Fatalf("start A printed\n%s", out)
	}
	var screen string
	for end := time.Now().Add(patience); ; time.Sleep(20 * time.Millisecond) {
		s, _ := p.Record()
		a := s.Attempts["A.1"]
		screen, _ = p.Kit.Terms.Screen(a.Place.Pane)
		if a.State == contract.AttemptReported {
			break
		} else if time.Now().After(end) {
			t.Fatalf("the agent did not do its task: A.1 is %s\n%s", a.State, screen)
		}
	}
	if !strings.Contains(screen, "fakeagent: took the prompt") || strings.Contains(screen, "an Enter came before") {
		t.Errorf("the prompt's Enter did not wait for the pane to show the text:\n%s", screen)
	}
}
