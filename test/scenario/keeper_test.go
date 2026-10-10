package scenario

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
// the word for it and the events that only bring it are left out too.
func session(t *testing.T, terms contract.Terminals, root string) []string {
	t.Helper()
	var log []string
	say := func(what string, err error, answer ...any) {
		log = append(log, strings.TrimSpace(fmt.Sprintf("%s: %s %q", what, fmt.Sprint(answer...), contract.ErrCode(err))))
	}
	show := func(p contract.Pane) string {
		return fmt.Sprintf("%s in %s %q focused=%v", p.ID, p.Tab, p.Label, p.Focused)
	}
	picture := func(what string, s *contract.Snapshot, err error) {
		if say(what, err); s != nil {
			for _, p := range s.Panes {
				log = append(log, "  "+show(p))
			}
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

	p, err := terms.TabCreate(root, "third", []string{"A=1"})
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
	say("tab-close", terms.TabClose("t3"))
	last, err := terms.Snapshot(ctx)
	picture("snapshot", last, err)

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
			if e.Kind != contract.EvState || e.Pane.Terminal != was {
				log = append(log, fmt.Sprintf("event %s: %s", e.Kind, show(e.Pane)))
			}
			if contract.Apply(first, e); e.Seq < last.Seq {
				continue
			}
		case <-time.After(patience):
			t.Fatalf("the events stopped at number %d of %d", first.Seq, last.Seq)
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
	real := session(t, p.Kit.Terms, p.Root)

	double := fakeengine.New()
	defer double.Close()
	double.TabCreate(p.Root, "Lead", nil)
	double.TabCreate(p.Root, "you", nil)
	if fake := session(t, double, p.Root); strings.Join(fake, "\n") != strings.Join(real, "\n") {
		t.Errorf("the double:\n%s\n\nthe keeper:\n%s", strings.Join(fake, "\n"), strings.Join(real, "\n"))
	}
	t.Log("\n" + strings.Join(real, "\n"))

	hook, _ := p.Kit.Terms.(*engine.Client).Call(t.Context(), contract.WireCall{Op: contract.OpHook, Kind: "Stop", Pane: p.Lead})
	for call, err := range map[string]error{
		contract.OpAgentStart: p.Kit.Terms.AgentStart("ann", "claude", p.Lead, nil, time.Second),
		contract.OpPrompt:     p.Kit.Terms.Prompt("ann", "x", time.Second),
		contract.OpPoint:      p.Kit.Terms.Point(p.Lead, contract.PointMail, ""),
		contract.OpOverlay:    p.Kit.Terms.Overlay([]string{"whaleshark", "ask"}, 0.5, 0.5),
		contract.OpHook:       contract.ErrOf(hook),
	} {
		if errors.Is(err, contract.ErrNotBuilt) {
			t.Errorf("%s is not answered by the keeper", call)
		}
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
