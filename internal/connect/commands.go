// Package connect binds connect, which runs on the person's own computer and
// speaks to the server through the system's ssh alone. It stores nothing
// about the work, and all it ever runs on the server is `whaleshark dash url`
// and, for the terminals, `whaleshark open`.
package connect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// What a test replaces: the ssh that is run, the program's name on the
// server, the clock on the wall (which moves on while the computer sleeps),
// how often the page is asked, the waits between tries, the address a site
// is reached at on the server, and the browser.
var (
	sshArgv  = []string{"ssh"}
	remote   = "whaleshark"
	now      = func() time.Time { return time.Now().Round(0) }
	tick     = 5 * time.Second
	ladder   = []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second}
	siteHost = contract.Loopback
	browse   = func(system, link string) error {
		argv := map[string][]string{"darwin": {"open"}, "linux": {"xdg-open"}, "windows": {"rundll32", "url.dll,FileProtocolHandler"}}[system]
		// #nosec G204 G702 -- the system's own opener and a link made here
		return exec.Command(argv[0], append(argv[1:], link)...).Run()
	}
)

// keep is on every connection: no agent handed over, so the server never has
// this computer's keys, and a line every 15 seconds, three unanswered end it.
var keep = []string{"-a", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3", "-o", "ConnectTimeout=10"}

func refuse(exit int, code, message string, next ...string) *contract.Refusal {
	return &contract.Refusal{Exit: exit, Code: code, Message: message, Next: next}
}

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) { k.Handle("connect", run) }

func run(c *contract.Call) (any, error) {
	port := contract.PagePort
	// A switch that was given holds one empty word.
	if len(c.Args) != 1 || strings.HasPrefix(c.Args[0], "-") || len(c.Flags["terminals"])+len(c.Flags["at-login"])+len(c.Flags["not-at-login"]) > 1 {
		return nil, refuse(contract.ExitUsage, "usage", "connect takes what you would give ssh, and at most one of --terminals, --at-login and --not-at-login.",
			"whaleshark help connect")
	}
	if v := c.Flags["port"]; v != nil {
		if port, _ = strconv.Atoi(v[0]); port < 1024 || port > 65535 {
			return nil, refuse(contract.ExitUsage, "invalid_value", "--port takes a number from 1024 to 65535.")
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch {
	case c.Flags["at-login"] != nil:
		return nil, atLogin(c, c.Args[0], port)
	case c.Flags["not-at-login"] != nil:
		return nil, notAtLogin(c)
	case c.Flags["terminals"] != nil:
		return nil, terminals(ctx, c, c.Args[0])
	}
	return nil, page(ctx, c, c.Args[0], port)
}

// ssh is one run of the system's ssh.
func ssh(ctx context.Context, args ...string) *exec.Cmd {
	// #nosec G204 G702 -- ssh, with a list of arguments and a target that starts with no dash
	return exec.CommandContext(ctx, sshArgv[0], slices.Concat(sshArgv[1:], args)...)
}

// pause says the link is down and waits the next step of the ladder: 1, 2,
// 5 and 10 seconds, then 30 for as long as it takes. False: asked to end.
func pause(ctx context.Context, out io.Writer, since time.Time, try *int) bool {
	wait := ladder[min(*try, len(ladder)-1)]
	*try++
	fmt.Fprintf(out, "link down since %s · the work on the server continues · next try in %d s\n", since.Format("15:04"), int(wait.Seconds()))
	select {
	case <-ctx.Done():
		return false
	case <-time.After(wait):
		return true
	}
}

// link is the page's one master connection: through its control socket ctl
// forwards are added and dropped. sites are the ports forwarded now.
type link struct {
	c           *contract.Call
	target, ctl string
	port        int
	local       string
	sites       map[int]bool
	live, shown bool
}

func page(ctx context.Context, c *contract.Call, target string, port int) error {
	dirs, err := c.Kit.Platform.Dirs()
	if err != nil {
		return err
	}
	os.MkdirAll(dirs.State, 0o700)
	// One connect a port: the control socket is named by it, and kept short.
	l := &link{c: c, target: target, port: port, local: net.JoinHostPort(contract.Loopback, strconv.Itoa(port)),
		ctl: filepath.Join(dirs.State, "c-"+strconv.Itoa(port))}
	free, err := net.Listen("tcp", l.local)
	if err != nil {
		return refuse(contract.ExitEnv, "port_taken", fmt.Sprintf("Port %d is taken on this computer, perhaps by a connect that is running already.", port),
			fmt.Sprintf("whaleshark connect %s --port %d", target, port+1))
	}
	free.Close()
	for try, since := 0, now(); ; {
		l.live = false
		err := l.up(ctx)
		os.Remove(l.ctl)
		var r *contract.Refusal
		switch {
		case ctx.Err() != nil:
			return nil
		case errors.As(err, &r):
			return r
		case l.live:
			try, since = 0, now()
		}
		if !pause(ctx, c.Out, since, &try) {
			return nil
		}
	}
}

// mux speaks to the master connection, or through it runs a command there.
func (l *link) mux(ctx context.Context, opts []string, command ...string) *exec.Cmd {
	return ssh(ctx, slices.Concat([]string{"-S", l.ctl}, opts, []string{l.target}, command)...)
}

// up makes the connection, has the server start its page and say where it
// listens, forwards the page's port to that socket file and opens the
// sign-in link; then it asks the page every few seconds for the sites to
// forward. It returns when the link is lost.
func (l *link) up(ctx context.Context) error {
	os.Remove(l.ctl)
	master := ssh(ctx, slices.Concat([]string{"-M", "-S", l.ctl, "-N"}, keep, []string{l.target})...)
	master.Stderr = l.c.Err
	if err := master.Start(); err != nil {
		return refuse(contract.ExitEnv, "no_ssh", "ssh could not be started on this computer: "+err.Error())
	}
	gone := make(chan struct{})
	go func() { master.Wait(); close(gone) }()
	defer func() { master.Process.Kill(); <-gone }()
	lost := errors.New("the connection ended")
	for l.mux(ctx, []string{"-O", "check"}).Run() != nil {
		select {
		case <-gone:
			return lost
		case <-time.After(200 * time.Millisecond):
		}
	}
	url := l.mux(ctx, nil, remote, "dash", "url", "--json")
	out, err := url.Output()
	var d contract.DashURL
	e := contract.Envelope{Result: &d, Error: &contract.Refusal{Message: fmt.Sprint(err)}}
	// 255 is ssh's own word for "no connection".
	if bad := json.Unmarshal(out, &e); url.ProcessState.ExitCode() == 255 {
		return lost
	} else if bad != nil || !e.OK || d.Socket == "" {
		return refuse(contract.ExitEnv, "no_page", "The server did not say where its page is: "+e.Error.Message, "ssh "+l.target+" whaleshark dash url")
	}
	if l.mux(ctx, []string{"-O", "forward", "-L", l.local + ":" + d.Socket}).Run() != nil {
		return lost
	}
	// A name of the page's own: plain localhost shares its cookies over all ports.
	at := "http://" + net.JoinHostPort(contract.PageHosts[0], strconv.Itoa(l.port))
	in := at + contract.RouteIn + "?" + contract.FieldCode + "=" + d.Code
	if l.shown || browse(l.c.Kit.Platform.System(), in) != nil {
		fmt.Fprintf(l.c.Out, "connected to %s at %s · to sign in, should the page ask, open within a minute: %s\n", l.target, now().Format("15:04"), in)
	} else {
		fmt.Fprintf(l.c.Out, "connected to %s · the page is open in your browser: %s/\n", l.target, at)
	}
	l.live, l.shown, l.sites = true, true, map[int]bool{}
	for last, fails := now(), 0; ; last = now() {
		select {
		case <-ctx.Done():
			return nil
		case <-gone:
			return lost
		case <-time.After(tick):
		}
		// A clock that jumped means the computer slept: the link is tested
		// at once and given up at the first miss, where otherwise ssh's own
		// keep-alive decides and three misses mean the page is gone.
		limit, wait := 3, 4*time.Second
		if now().Sub(last) > 3*tick {
			limit, wait = 1, 2*time.Second
		}
		if fails++; l.ask(ctx, wait) == nil {
			fails = 0
		} else if fails >= limit {
			return lost
		}
	}
}

// ask puts RouteSites to the page through the forwarded port. Any answer
// proves the link; only a list of ports is acted on.
func (l *link) ask(ctx context.Context, wait time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+l.local+contract.RouteSites, nil)
	req.Host = net.JoinHostPort(contract.PageHosts[0], strconv.Itoa(l.port))
	// A connection of its own each time, or it would prove nothing.
	resp, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var ports []int
	if resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&ports) == nil {
		l.forward(ctx, ports)
	}
	return nil
}

// forward brings the forwarded sites in line with the page's answer: each
// new port under its own number on the loopback address, so that a link an
// agent prints works as written (one that is taken here is tried again at
// the next answer), and each port that left the answer dropped.
func (l *link) forward(ctx context.Context, ports []int) {
	spec := func(port int) string {
		return net.JoinHostPort(contract.Loopback, strconv.Itoa(port)) + ":" + net.JoinHostPort(siteHost, strconv.Itoa(port))
	}
	for _, p := range ports {
		if p >= 1024 && p <= 65535 && p != l.port && !l.sites[p] && l.mux(ctx, []string{"-O", "forward", "-L", spec(p)}).Run() == nil {
			l.sites[p] = true
			fmt.Fprintf(l.c.Out, "a site is running: http://%s\n", net.JoinHostPort(contract.Loopback, strconv.Itoa(p)))
		}
	}
	for p := range l.sites {
		if !slices.Contains(ports, p) {
			l.mux(ctx, []string{"-O", "cancel", "-L", spec(p)}).Run()
			delete(l.sites, p)
		}
	}
}
