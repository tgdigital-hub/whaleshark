package scenario

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/engine"
	"github.com/tgdigital-hub/whaleshark/internal/layout"
	"github.com/tgdigital-hub/whaleshark/internal/restore"
)

// keeper starts the real keeper in the project's login, on a socket of its
// own, and returns what stops it; the test's end stops it too. This process
// reaches it as every command does: by the socket its surroundings name. Its
// panes' shell is the plain one, and its fake agents find their scripts in dir.
func (p *Project) keeper(socket, dir string) (stop func()) {
	p.t.Helper()
	var said bytes.Buffer
	run := exec.Command(p.bin, "engine", "run")
	run.Env = append(append(p.surroundings(), p.env...), "SHELL=/bin/sh", testkit.EnvScripts+"="+dir)
	run.Stdout, run.Stderr = &said, &said
	p.must(run.Start())
	p.t.Setenv(contract.EnvSocket, socket)
	client := engine.New(p.Kit)
	stop = sync.OnceFunc(func() {
		ended := time.AfterFunc(patience, func() { run.Process.Kill() })
		client.Call(context.Background(), contract.WireCall{Op: contract.OpStop})
		run.Wait()
		ended.Stop()
	})
	p.t.Cleanup(stop)
	for end := time.Now().Add(patience); ; time.Sleep(20 * time.Millisecond) {
		if _, err := client.Version(); err == nil {
			break
		} else if time.Now().After(end) {
			p.t.Fatalf("scenario: the keeper did not start: %v\n%s", err, &said)
		}
	}
	p.Kit.Terms = client
	return stop
}

// onKeeper is what the runner asks of the real keeper beyond the interface.
// Nothing but the keeper's own file and its own calls can change it, so
// each thing a stand-in is simply told is brought about here as it comes
// about for real.
type onKeeper struct {
	p           *Project
	socket, dir string
	stop        func()
}

// Script puts an attempt's script where the keeper's fake agent looks for it.
func (k *onKeeper) Script(attempt, script string) {
	k.p.must(os.WriteFile(filepath.Join(k.dir, attempt), []byte(script), 0o600))
}

// Load writes the picture as the keeper's layout file, a tab for each of its
// tabs and every pane under its id, starts the keeper, which brings it back
// as it would after a restart, and starts each agent in its pane by the
// keeper's own call: a fake agent that says it is in the picture's state.
func (k *onKeeper) Load(s *contract.Snapshot) {
	p := k.p
	p.t.Helper()
	dirs, err := p.Kit.Platform.Dirs()
	p.must(err)
	var lay layout.Layout
	file := &restore.File{W: 200, H: 60}
	first := map[string]string{}
	for _, pane := range s.Panes {
		if in := first[pane.Tab]; in == "" {
			lay.Add(pane.Tab, pane.Label, pane.ID)
			first[pane.Tab] = pane.ID
		} else {
			p.must(lay.Split(in, contract.Right, 0.5, pane.ID))
		}
		if pane.Focused {
			lay.Focus(pane.ID)
			lay.Show(pane.Tab)
		}
		file.Panes = append(file.Panes, restore.Pane{Rec: contract.Pane{ID: pane.ID, Cwd: pane.Cwd}})
	}
	file.Layout, err = json.Marshal(&lay)
	p.must(err)
	p.must(os.MkdirAll(dirs.State, 0o700))
	p.must(restore.Save(p.Kit.Platform, dirs, file))
	k.stop = p.keeper(k.socket, k.dir)
	var wg sync.WaitGroup
	for _, pane := range s.Panes {
		if pane.Agent != "" {
			wg.Go(func() {
				args := []string{"--state", pane.Status, "--session", cmp.Or(pane.Session, "session-of-"+pane.ID)}
				if err := p.Kit.Terms.AgentStart(pane.Name, pane.Agent, pane.ID, args, patience); err != nil {
					p.t.Errorf("scenario: the agent in %s: %v", pane.ID, err)
				}
				k.await(pane.ID, pane.Status)
			})
		}
	}
	wg.Wait()
}

// await waits until the keeper has found a pane's program in a state.
func (k *onKeeper) await(pane, status string) {
	k.p.t.Helper()
	if status == contract.StatusDone {
		status = contract.StatusIdle // an agent that has finished is idle to the engine
	}
	for end := time.Now().Add(patience); ; time.Sleep(10 * time.Millisecond) {
		s, err := k.p.Kit.Terms.Snapshot(context.Background())
		k.p.must(err)
		i := slices.IndexFunc(s.Panes, func(p contract.Pane) bool { return p.ID == pane })
		if i < 0 || s.Panes[i].Status == status {
			return
		} else if time.Now().After(end) {
			k.p.t.Fatalf("scenario: pane %s is %s on the keeper, not %s", pane, s.Panes[i].Status, status)
		}
	}
}

// hookFor is the event of an agent's own hooks that means a state.
var hookFor = map[string]string{contract.StatusWorking: "UserPromptSubmit", contract.StatusIdle: "Stop", contract.StatusDone: "Stop",
	contract.StatusBlocked: "PermissionRequest"}

// Push brings a change about as it comes about for real: a state by the
// event of the agent's own hooks, an agent that left by a shell in its
// place, a closed pane and the keys by their calls. The keeper loses no
// event, so Drop is Push.
func (k *onKeeper) Push(kind, pane string) {
	p := k.p
	p.t.Helper()
	switch event := hookFor[kind]; {
	case kind == testkit.PushGone:
		// An agent whose program ended took its pane with it already.
		if err := p.Kit.Terms.Run(pane, []string{"/bin/sh"}); !errors.Is(err, contract.ErrNoPane) {
			p.must(err)
		}
	case kind == testkit.PushClosed:
		if err := p.Kit.Terms.PaneClose(pane); !errors.Is(err, contract.ErrNoPane) {
			p.must(err)
		}
	case kind == testkit.PushFocused:
		_, err := p.Kit.Terms.PaneFocus(pane, "")
		p.must(err)
	case event != "":
		_, err := p.Kit.Terms.(*engine.Client).Call(context.Background(), contract.WireCall{Op: contract.OpHook, Kind: event, Pane: pane})
		p.must(err)
		k.await(pane, kind)
	}
}

func (k *onKeeper) Drop(kind, pane string) { k.Push(kind, pane) }

// Restart stops the keeper as an upgrade does and starts it again: it
// brings everything back by itself.
func (k *onKeeper) Restart() {
	k.stop()
	k.stop = k.p.keeper(k.socket, k.dir)
}
