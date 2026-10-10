package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// channel is one way to the person after the pop-up; it reports whether it
// reached them. It is given up when ctx ends.
type channel func(ctx context.Context, set settings, c contract.Nudge) bool

// phone is one web request to the address the person configured, such as a
// topic of a notice service or a chat bot's. It passes through somebody
// else's service, so it carries the title, which names the task, and the
// nudge's text only where the project's [notify] full_text allows it.
//
// In the address, {text} stands for the message and {link} for the address
// a tap opens, each escaped for a query; a service that takes a link is
// given one that way. An address without {text} gets the message as the
// request's body.
func phone(ctx context.Context, set settings, c contract.Nudge) bool {
	if !set.Nudge.Phone || set.Notify.URL == "" {
		return false
	}
	text := c.Title
	if c.Root != "" && c.Body != "" {
		if p, err := contract.ReadProjectFile(c.Root); err == nil && p.Notify.FullText {
			text += "\n" + c.Body
		}
	}
	var body io.Reader
	if !strings.Contains(set.Notify.URL, "{text}") {
		body = strings.NewReader(text)
	}
	address := strings.NewReplacer("{text}", url.QueryEscape(text), "{link}", url.QueryEscape(set.Notify.Link)).Replace(set.Notify.URL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address, body)
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode < 300
}

// hooks starts every program in hooks/nudge.d of the person's settings
// folder with the nudge on its standard input, as one JSON object, and
// returns what waits for them. When ctx ends each is ended with all it
// started. The folder is the person's own way to add a sound or a message;
// it is used only while nobody but the login can write to it, to what leads
// to it, or to the program.
func hooks(ctx context.Context, k *contract.Kit, set settings, c contract.Nudge, sound bool) (wait func()) {
	dir := filepath.Join(set.dirs.Config, "hooks", "nudge.d")
	private := func(path string) bool {
		others, err := k.Platform.WritableByOthers(path)
		return !others && (err == nil || errors.Is(err, contract.ErrCannotTell))
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) == 0 || !private(set.dirs.Config) || !private(filepath.Dir(dir)) || !private(dir) {
		return func() {}
	}
	event, _ := json.Marshal(map[string]any{"event": "nudge", "title": c.Title, "body": c.Body,
		"task": c.Task, "run": c.Run, "root": c.Root, "urgent": c.Urgent, "sound": sound})
	dialect := contract.ShellPosix
	if k.Platform.System() == "windows" {
		dialect = contract.ShellCmd
	}
	var all sync.WaitGroup
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if e.IsDir() || !private(path) {
			continue
		}
		cmd := k.Platform.Shell(k.Platform.Quote(dialect, []string{path}))
		if cmd.Stdin = bytes.NewReader(event); cmd.Start() != nil {
			continue
		}
		all.Go(func() {
			stop := context.AfterFunc(ctx, func() { k.Platform.EndTree(cmd) })
			cmd.Wait()
			stop()
		})
	}
	return all.Wait
}
