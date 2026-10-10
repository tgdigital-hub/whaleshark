package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/engine"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/test/fakeengine"
)

// Every method of the interface is one call on the wire, with its
// arguments in the fields the contract names, and its answer read back.
func TestEveryMethodIsOneCall(t *testing.T) {
	f := fakeengine.New()
	defer f.Close()
	f.Attached = true
	var terms contract.Terminals = f
	keys := []contract.KeyEntry{{Key: "shift+a", Type: "shell", Argv: []string{"whaleshark", "pause"}}}
	type C = contract.WireCall
	steps := []struct {
		got  func() (any, error)
		call C
		want any
	}{
		{func() (any, error) { return terms.Version() }, C{Op: contract.OpVersion}, "double"},
		{func() (any, error) {
			p, err := terms.TabCreate("/w", "one", []string{"A=1"})
			return p.ID + " " + p.Tab + " " + p.Label, err
		},
			C{Op: contract.OpTabCreate, Cwd: "/w", Label: "one", Env: []string{"A=1"}}, "p1 t1 one"},
		{func() (any, error) { p, err := terms.Split("p1", contract.Right, 0.4); return p.ID + " " + p.Tab, err },
			C{Op: contract.OpSplit, Pane: "p1", Dir: contract.Right, Ratio: 0.4}, "p2 t1"},
		{func() (any, error) { return nil, terms.TabRename("t1", "two") }, C{Op: contract.OpTabRename, Tab: "t1", Label: "two"}, nil},
		{func() (any, error) { return nil, terms.TabFocus("t1") }, C{Op: contract.OpTabFocus, Tab: "t1"}, nil},
		{func() (any, error) { return terms.PaneFocus("p1", contract.Right) }, C{Op: contract.OpPaneFocus, Pane: "p1", Dir: contract.Right}, "p2"},
		{func() (any, error) { return terms.PaneFocus("p1", "") }, C{Op: contract.OpPaneFocus, Pane: "p1"}, "p1"},
		{func() (any, error) { return nil, terms.Swap("p1", "p2") }, C{Op: contract.OpSwap, Pane: "p1", Target: "p2"}, nil},
		{func() (any, error) { return nil, terms.Resize("p1", contract.Left, 0.1) }, C{Op: contract.OpResize, Pane: "p1", Dir: contract.Left, Ratio: 0.1}, nil},
		{func() (any, error) { w, h, err := terms.Size("p2"); return [2]int{w, h}, err }, C{Op: contract.OpSize, Pane: "p2"}, [2]int{120, 40}},
		{func() (any, error) { return nil, terms.Run("p2", []string{"whaleshark", "fleet"}) }, C{Op: contract.OpRun, Pane: "p2", Argv: []string{"whaleshark", "fleet"}}, nil},
		{func() (any, error) { return terms.Screen("p2") }, C{Op: contract.OpScreen, Pane: "p2"}, "% whaleshark fleet\n"},
		{func() (any, error) {
			return nil, terms.AgentStart("ann", "claude", "p1", []string{"--model", "m"}, 5*time.Second)
		},
			C{Op: contract.OpAgentStart, Name: "ann", Kind: "claude", Pane: "p1", Argv: []string{"--model", "m"}, Millis: 5000}, nil},
		{func() (any, error) { return nil, terms.Prompt("ann", "Read and follow it", 2*time.Second) },
			C{Op: contract.OpPrompt, Name: "ann", Text: "Read and follow it", Millis: 2000}, nil},
		{func() (any, error) { return nil, terms.Point("p1", contract.PointEvents, "3") },
			C{Op: contract.OpPoint, Pane: "p1", Pointer: contract.PointEvents, Text: "3"}, nil},
		{func() (any, error) { return terms.Screen("p1") }, C{Op: contract.OpScreen, Pane: "p1"},
			"> Read and follow it\n> whaleshark: 3 events waiting. Run: whaleshark wait\n"},
		{func() (any, error) { r, d, err := terms.Notify("T1", "done", true); return r + " " + d, err },
			C{Op: contract.OpNotify, Title: "T1", Text: "done", Sound: true}, contract.NotifyShown + " on"},
		{func() (any, error) { return nil, terms.SetKeys(keys) }, C{Op: contract.OpSetKeys, Keys: keys}, nil},
		{func() (any, error) { return nil, terms.Overlay([]string{"whaleshark", "ask"}, 0.5, 12) },
			C{Op: contract.OpOverlay, Argv: []string{"whaleshark", "ask"}, W: 0.5, H: 12}, nil},
		{func() (any, error) { return nil, terms.PaneClose("p2") }, C{Op: contract.OpPaneClose, Pane: "p2"}, nil},
		{func() (any, error) { s, err := terms.Snapshot(t.Context()); return len(s.Panes), err }, C{Op: contract.OpSnapshot}, 1},
		{func() (any, error) { return nil, terms.TabClose("t1") }, C{Op: contract.OpTabClose, Tab: "t1"}, nil},
	}
	for i, s := range steps {
		got, err := s.got()
		if calls := f.Calls(); err != nil || !reflect.DeepEqual(got, s.want) || len(calls) != i+1 || !reflect.DeepEqual(calls[i], s.call) {
			t.Fatalf("%s: answered %v, %v; the wire had %+v", s.call.Op, got, err, calls[len(calls)-1])
		}
	}
}

// A refusal callers tell apart is the same error on this side of the
// socket; any other keeps the keeper's words.
func TestARefusalKeepsItsName(t *testing.T) {
	f := fakeengine.New()
	defer f.Close()
	pane, _ := f.TabCreate("", "a", nil)
	if err := f.PaneClose("p9"); err != contract.ErrNoPane {
		t.Errorf("closing a pane that is not there: %v", err)
	}
	if _, err := f.Split("p9", contract.Down, 0.5); err != contract.ErrNoPane {
		t.Errorf("splitting a pane that is not there: %v", err)
	}
	f.AgentStart("ann", "claude", pane.ID, nil, time.Second)
	if err := f.AgentStart("bob", "claude", pane.ID, nil, time.Second); err == nil || err.Error() != "pane p1 already holds an agent" {
		t.Errorf("a second agent in one pane: %v", err)
	}
	f.Push(contract.StatusBlocked, pane.ID)
	if err := f.Prompt("ann", "x", time.Second); err != contract.ErrAgentBlocked {
		t.Errorf("a prompt for an agent at a question: %v", err)
	}
	if text, _ := f.Screen(pane.ID); text != "" {
		t.Errorf("something was typed at an agent at a question: %q", text)
	}
}

// Events gives the picture and then every change in order; applied to the
// picture they give the keeper's own. The channel ends with ctx, and when
// the keeper stops.
func TestEventsFollowThePicture(t *testing.T) {
	f := fakeengine.New()
	f.TabCreate("", "a", nil)
	ctx, cancel := context.WithCancel(t.Context())
	picture, events, err := f.Events(ctx)
	if err != nil || len(picture.Panes) != 1 || picture.Instance == "" {
		t.Fatalf("the first picture: %+v, %v", picture, err)
	}
	f.TabCreate("", "b", nil)
	f.Split("p2", contract.Right, 0.5)
	f.TabRename("t2", "c")
	f.PaneFocus("p3", "")
	f.Drop(contract.StatusWorking, "p1") // told to nobody, so not in the count
	f.TabClose("t2")
	want := []string{contract.EvOpened, contract.EvOpened, contract.EvRenamed, contract.EvFocus, contract.EvTabClosed, contract.EvFocus}
	for i, kind := range want {
		select {
		case e := <-events:
			if e.Kind != kind || e.Seq != picture.Seq+1 {
				t.Fatalf("event %d is %+v, not a %s numbered %d", i, e, kind, picture.Seq+1)
			}
			contract.Apply(picture, e)
		case <-time.After(5 * time.Second):
			t.Fatalf("no event %d (%s)", i, kind)
		}
	}
	now, _ := f.Snapshot(t.Context())
	if !reflect.DeepEqual(picture.Panes, now.Panes) || picture.Seq != now.Seq {
		t.Errorf("the events applied give %+v, the keeper says %+v", picture, now)
	}
	cancel()
	if _, open := <-events; open {
		t.Error("the channel outlived its context")
	}
	_, events, _ = f.Events(t.Context())
	f.Close()
	if _, open := <-events; open {
		t.Error("the channel outlived the keeper")
	}
}

// server answers each call on one connection with what answer gives; no
// answer leaves the caller waiting.
func server(answer func(contract.WireCall) *contract.WireReply) *engine.Client {
	return &engine.Client{Dial: func(context.Context) (net.Conn, error) {
		near, far := net.Pipe()
		go func() {
			for {
				_, body, err := contract.ReadFrame(far)
				var c contract.WireCall
				if err != nil || json.Unmarshal(body, &c) != nil {
					return
				}
				if r := answer(c); r != nil {
					body, _ = json.Marshal(r)
					contract.WriteFrame(far, contract.FrameReply, body)
				}
			}
		}()
		return near, nil
	}}
}

func TestAKeeperThatCannotBeTalkedTo(t *testing.T) {
	// Another version: the hello is answered with the error by its name.
	old := server(func(contract.WireCall) *contract.WireReply {
		return &contract.WireReply{Err: contract.ErrWireVersion.Error()}
	})
	if _, err := old.Version(); err != contract.ErrWireVersion {
		t.Errorf("a keeper of another version: %v", err)
	}
	if _, _, err := old.Events(t.Context()); err != contract.ErrWireVersion {
		t.Errorf("events of a keeper of another version: %v", err)
	}
	// One that says hello and then nothing: the caller's context ends the wait.
	silent := server(func(c contract.WireCall) *contract.WireReply {
		if c.Op == contract.OpHello {
			return &contract.WireReply{}
		}
		return nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := silent.Snapshot(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a snapshot the keeper never gave: %v", err)
	}
	// One that answers with something that is no answer.
	odd := server(func(c contract.WireCall) *contract.WireReply { return &contract.WireReply{} })
	if _, err := odd.Snapshot(t.Context()); err != contract.ErrFrame {
		t.Errorf("a snapshot with no picture in it: %v", err)
	}
	if _, err := odd.TabCreate("", "", nil); err != contract.ErrFrame {
		t.Errorf("a new tab with no pane in it: %v", err)
	}
}

// The socket is where the surroundings say, and Plug puts the keeper behind
// the kit wherever a command is run.
func TestTheSocketOfTheSurroundings(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(name, filepath.Join(home, name))
	}
	t.Setenv(contract.EnvPane, "")
	dir, err := os.MkdirTemp("", "ws") // a socket's path is short
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	t.Setenv(contract.EnvSocket, filepath.Join(dir, "s"))
	k := contract.NewKit()
	platform.Plug(k)
	engine.Plug(k)
	if _, err := k.Terms.Version(); !errors.Is(err, contract.ErrEngineUnreachable) {
		t.Errorf("with no keeper listening: %v", err)
	}
	f := fakeengine.New()
	defer f.Close()
	if err := f.Listen(filepath.Join(dir, "s")); err != nil {
		t.Fatal(err)
	}
	if v, err := k.Terms.Version(); v != "double" || err != nil {
		t.Errorf("the keeper on the named socket: %q, %v", v, err)
	}
	if r, err := engine.New(k).Call(t.Context(), contract.WireCall{Op: contract.OpHook, Kind: "Stop", Pane: "p1"}); err != contract.ErrNoPane {
		t.Errorf("a hook from a pane that is not there: %+v, %v", r, err)
	}

	// With nothing naming the keeper it is still the keeper, at its own
	// place in the login's state folder, and there none is running.
	t.Setenv(contract.EnvSocket, "")
	k = contract.NewKit()
	platform.Plug(k)
	engine.Plug(k)
	if _, err := k.Terms.Version(); !errors.Is(err, contract.ErrEngineUnreachable) || err.Error() != "the engine is not running" {
		t.Errorf("with nothing naming the keeper: %v", err)
	}
}
