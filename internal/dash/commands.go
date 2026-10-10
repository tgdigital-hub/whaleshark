// Package dash is the page's server: `whaleshark dash`. One resident
// program a login, which nobody sees and nothing needs. It listens on
// dash.sock in the login's state folder and on nothing else; a browser
// reaches it through the person's own ssh connection. It signs a browser in
// with a code that works once, checks the host name, the origin and the
// session's token of every request, and draws each screen from the files
// when it is asked for: the team's with Kit.View, every other from a read
// command's own answer. A button is one row of contract.Actions, run as a
// child that carries the page's mark; the server itself writes no run file.
// Every five seconds it starts the sweep for each open run of the login,
// whether or not a browser is connected, and that is all it does when
// nobody is looking. The phone's door and the tunnel's are not built yet.
//
// A server that holds a connection open and says a line when something
// changed is the ordinary shape of a live page (the HTML standard's
// server-sent events), and a signed cookie with a token for each form is
// the ordinary defence against another site's requests; both are written
// here from those descriptions.
package dash

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const startWait, startStep = 10 * time.Second, 50 * time.Millisecond

// Plug binds dash.
func Plug(k *contract.Kit) { k.Handle("dash", dash) }

func refuse(code, message string, next ...string) *contract.Refusal {
	return &contract.Refusal{Exit: contract.ExitEnv, Code: code, Message: message, Next: next}
}

// atTerminal says whether a person typed the command: the server is then
// started apart from the terminal, and otherwise is this very program, as
// a login's service entry needs it.
var atTerminal = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

func dash(c *contract.Call) (any, error) {
	k := c.Kit
	dirs, err := k.Platform.Dirs()
	if err != nil {
		return nil, err
	}
	word := ""
	if len(c.Args) == 1 {
		word = c.Args[0]
	}
	st, running := ask(k, dirs, "status")
	switch {
	case len(c.Args) > 0 && c.Args[0] == "phone":
		return nil, refuse("not_built", "The door for phones is "+contract.ErrNotBuilt.Error()+".")
	case word == "start" && !running && !atTerminal():
		return nil, serve(k, dirs)
	case word == "start" || word == "url":
		if !running {
			if st, err = start(k, dirs); err != nil {
				return nil, refuse("not_started", "The page's server did not start: "+err.Error()+".", "whaleshark dash status")
			}
		}
		if word == "start" {
			fmt.Fprintln(c.Out, "The page's server is running. To open the page: whaleshark dash url")
			return st, nil
		}
		key, err := readKey(k.Platform, dirs, false, false)
		if err != nil {
			return nil, err
		}
		u := contract.DashURL{Socket: st.Socket, Code: newCode(key, "in", contract.Now()), Version: st.Version}
		fmt.Fprintf(c.Out, "http://%s:%d%s?%s=%s\nIt works once, for a minute, through `whaleshark connect`.\n",
			contract.PageHosts[0], contract.PagePort, contract.RouteIn, contract.FieldCode, u.Code)
		return u, nil
	case word == "stop":
		if running {
			ask(k, dirs, "stop")
		}
		line := "The page's server has stopped."
		if c.Flags["forget"] != nil {
			// Under the server's own lock, so that none starts with the old key.
			unlock, free, err := k.Platform.TryLock(filepath.Join(dirs.State, lockFile))
			for end := time.Now().Add(startWait); err == nil && !free && time.Now().Before(end); time.Sleep(startStep) {
				unlock, free, err = k.Platform.TryLock(filepath.Join(dirs.State, lockFile))
			}
			if err != nil || !free {
				return nil, refuse("still_running", "The page's server did not stop, so its key was left as it is.", "whaleshark dash status")
			}
			defer unlock()
			if _, err := readKey(k.Platform, dirs, true, true); err != nil {
				return nil, err
			}
			line += " Every browser is signed out."
		}
		fmt.Fprintln(c.Out, line)
		return status{}, nil
	case word == "status" && running:
		fmt.Fprintf(c.Out, "The page's server is running since %s: %d MB of memory, %d browser tabs held open, %d open runs swept.\n",
			st.Since.Format("15:04"), st.Memory>>20, st.Tabs, st.Runs)
		return st, nil
	case word == "status":
		fmt.Fprintln(c.Out, "The page's server is not running.")
		return status{}, nil
	}
	return nil, &contract.Refusal{Exit: contract.ExitUsage, Code: "usage", Message: "dash takes one word: start, stop, url or status.", Next: []string{"whaleshark help dash"}}
}

// ask puts one question of this login's own to the running server, over
// its socket file, with a code signed for that word. ok is false when no
// server answers.
func ask(k *contract.Kit, dirs contract.Dirs, word string) (st status, ok bool) {
	key, err := readKey(k.Platform, dirs, false, false)
	if err != nil {
		return st, false
	}
	sock := filepath.Join(dirs.State, sockFile)
	client := http.Client{Timeout: startWait, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return new(net.Dialer).DialContext(ctx, "unix", sock)
	}}}
	defer client.CloseIdleConnections()
	resp, err := client.Get("http://" + contract.Loopback + routeOwn + word + "?" + contract.FieldCode + "=" + newCode(key, word, contract.Now()))
	if err != nil {
		return st, false
	}
	defer resp.Body.Close()
	return st, resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&st) == nil
}

// start runs `dash start` of this same program apart from this terminal
// and waits until it answers.
func start(k *contract.Kit, dirs contract.Dirs) (status, error) {
	self, err := k.Platform.SelfPath()
	if err != nil {
		return status{}, err
	}
	cmd := exec.Command(self, "dash", "start") // #nosec G204 -- this same program, with two words of ours
	cmd.SysProcAttr = apart
	if err = cmd.Start(); err != nil {
		return status{}, err
	}
	gone := make(chan error, 1)
	go func() { gone <- cmd.Wait() }()
	for end := time.Now().Add(startWait); time.Now().Before(end); time.Sleep(startStep) {
		if st, ok := ask(k, dirs, "status"); ok {
			return st, nil
		}
		select {
		case <-gone:
			// It found another one starting: that one is given a moment.
			end, gone = time.Now().Add(time.Second), nil
		default:
		}
	}
	return status{}, errors.New("it does not answer")
}

// serve is the server itself, until it is told to stop. One runs a login:
// the lock says so, and a socket file a dead one left is taken away.
func serve(k *contract.Kit, dirs contract.Dirs) error {
	if err := os.MkdirAll(dirs.State, 0o700); err != nil {
		return err
	}
	unlock, free, err := k.Platform.TryLock(filepath.Join(dirs.State, lockFile))
	if err != nil || !free {
		return refuse("already_running", "The page's server of this login is running already.", "whaleshark dash status")
	}
	defer unlock()
	s, err := newServer(k, dirs)
	if err != nil {
		return err
	}
	os.Remove(s.sock)
	l, err := listen("unix", s.sock)
	if err != nil {
		return err
	}
	if err = k.Platform.Private(s.sock); err != nil {
		l.Close()
		return err
	}
	ended := make(chan os.Signal, 1)
	signal.Notify(ended, os.Interrupt, syscall.SIGTERM)
	// A caller that never finishes its request is not waited for; a tab's
	// held-open answer has no end, so writing has no limit.
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		select {
		case <-ended:
			s.stop()
		case <-s.ctx.Done():
		}
		srv.Close()
	}()
	go s.sweeps()
	if err = srv.Serve(l); err == http.ErrServerClosed {
		err = nil
	}
	s.stop()
	return err
}
