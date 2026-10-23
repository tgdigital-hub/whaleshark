package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// eventWait is how long the project's on_event may take for one event, and
// for all it is given at one call; a test shortens it. markFile holds the
// last event it was given.
var eventWait = 10 * time.Second

const markFile = "hook.at"

// Mirror gives the project's [hooks] on_event, once it is approved, each
// event of a run it was not given yet, as JSON on its standard input. It
// runs outside the record's lock and one caller at a time; the mark moves on
// past an event whatever the command made of it, so a command that fails or
// hangs loses that event and holds up nothing. What is left when the time
// is up waits for the next call: a sweep, or a wait's.
func Mirror(k *contract.Kit, root, run string) {
	p, err := contract.ReadProjectFile(root)
	if err != nil || p.Hooks.OnEvent == "" {
		return
	}
	dir := k.Store.Dir(root, run)
	unlock, ok, err := k.Platform.TryLock(filepath.Join(dir, "hook.lock"))
	if err != nil || !ok {
		return
	}
	defer unlock()
	s, err := k.Store.Read(root, run)
	if err != nil {
		return
	}
	var mark struct {
		contract.Versioned
		Seq int `json:"seq"`
	}
	contract.ReadVersioned(k.Platform.Peek, filepath.Join(dir, markFile), contract.FileVersion, &mark)
	was, began := mark.Seq, time.Now()
	for _, e := range s.Inbox.Events {
		if e.Seq <= mark.Seq || time.Since(began) >= eventWait {
			continue
		}
		msg, _ := json.Marshal(struct {
			Project string `json:"project"`
			Run     string `json:"run"`
			contract.Event
		}{root, run, e})
		ctx, cancel := context.WithTimeout(context.Background(), eventWait)
		cmd := shell(ctx, p.Hooks.OnEvent)
		cmd.Dir, cmd.Stdin, cmd.WaitDelay = root, bytes.NewReader(msg), time.Second
		cmd.Run()
		cancel()
		mark.Seq = e.Seq
	}
	if mark.Seq != was {
		mark.Version = contract.FileVersion
		contract.WriteVersioned(k.Platform, filepath.Join(dir, markFile), contract.FileVersion, mark)
	}
}
