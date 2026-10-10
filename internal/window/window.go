package window

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

const (
	// What the window asks of the terminal is what a pane asks, and keys in
	// the extended form where the terminal has it (it keeps one such
	// setting for each of its two screens, so it is asked for on the second
	// and given back there). A terminal that has none ignores both.
	enter = term.Enter + "\x1b[>1u"
	leave = "\x1b[<u" + term.Leave

	// A keeper that is starting is waited for this long, and asked this often.
	startWait, startStep = 5 * time.Second, 50 * time.Millisecond
)

// attachEnv are the variables the keeper composes for this terminal by.
var attachEnv = []string{"TERM", "COLORTERM", "NO_COLOR", "TERM_PROGRAM"}

func refuse(code, message string, next ...string) *contract.Refusal {
	return &contract.Refusal{Exit: contract.ExitEnv, Code: code, Message: message, Next: next}
}

// whose is the system whose copy keys apply: the one the person sits at.
// Through a bare ssh nobody can tell, and the empty answer is the safe rule.
func whose(getenv func(string) string, system string) string {
	if s := getenv(contract.EnvSystem); s != "" {
		return s
	}
	if getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" {
		return ""
	}
	return system
}

// link is the connection to the keeper. Two goroutines write frames to it.
type link struct {
	net.Conn
	mu sync.Mutex
}

func (l *link) send(kind byte, body []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return contract.WriteFrame(l.Conn, kind, body)
}

func (l *link) call(c contract.WireCall) error {
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return l.send(contract.FrameCall, body)
}

// connect dials the keeper and greets it. Where none is listening it starts
// one and waits until that one listens or has given up.
func connect(k *contract.Kit) (*link, error) {
	dirs, err := k.Platform.Dirs()
	if err != nil {
		return nil, err
	}
	path := contract.SocketPath(os.Getenv, dirs)
	conn, err := net.Dial("unix", path)
	if err != nil {
		conn, err = start(k, path)
	}
	if err != nil {
		return nil, refuse("not_connected", "The window is not connected: no keeper is running and none could be started ("+err.Error()+").",
			"whaleshark engine status")
	}
	l := &link{Conn: conn}
	if err = l.call(contract.WireCall{V: contract.WireVersion, ID: 1, Op: contract.OpHello}); err == nil {
		var kind byte
		var body []byte
		var r contract.WireReply
		if kind, body, err = contract.ReadFrame(conn); err == nil && kind == contract.FrameReply {
			if err = json.Unmarshal(body, &r); err == nil {
				err = contract.ErrOf(r)
			}
		} else if err == nil {
			err = contract.ErrFrame
		}
	}
	switch {
	case errors.Is(err, contract.ErrWireVersion):
		conn.Close()
		return nil, refuse("restart_needed", "The keeper that is running is another version of whaleshark than this one: it must be started again.",
			"whaleshark engine stop", "whaleshark open")
	case err != nil:
		conn.Close()
		return nil, refuse("not_connected", "The window is not connected: the keeper did not greet it ("+err.Error()+").", "whaleshark engine status")
	}
	return l, nil
}

// start runs `engine run` of this same program, apart from this terminal,
// and dials until it listens.
func start(k *contract.Kit, path string) (net.Conn, error) {
	self, err := k.Platform.SelfPath()
	if err != nil {
		return nil, err
	}
	// #nosec G204 -- this same program, by the path the system gives for it, with two words of ours
	cmd := exec.Command(self, "engine", "run")
	cmd.SysProcAttr = apart
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	gone := make(chan error, 1)
	go func() { gone <- cmd.Wait() }()
	for end := time.Now().Add(startWait); ; time.Sleep(startStep) {
		if conn, err := net.Dial("unix", path); err == nil || time.Now().After(end) {
			return conn, err
		}
		select {
		case err := <-gone:
			// A keeper that put itself in the background has ended well
			// and is waited for; one that failed is not.
			if gone = nil; err != nil {
				return nil, fmt.Errorf("the keeper ended at once: %w", err)
			}
		default:
		}
	}
}

// open is the window. Whatever ends it, by any way but being killed
// outright, the terminal is put back first, and nothing is written after.
func open(c *contract.Call) (any, error) {
	l, err := connect(c.Kit)
	if err != nil {
		return nil, err
	}
	defer l.Close()
	ended := make(chan os.Signal, 1)
	signal.Notify(ended, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	w, h, restore, err := term.Raw()
	if err != nil {
		return nil, refuse("no_terminal", "The window "+err.Error()+": run it in a terminal of your own.")
	}
	// asked is what must be taken back; restore is nil once it has been.
	var mu sync.Mutex
	asked := ""
	put := func(b []byte) {
		mu.Lock()
		defer mu.Unlock()
		if restore != nil {
			os.Stdout.Write(b)
		}
	}
	back := func() {
		mu.Lock()
		defer mu.Unlock()
		if restore != nil {
			os.Stdout.WriteString(asked)
			restore()
			restore = nil
		}
	}
	defer back()
	go func() {
		s := <-ended
		back()
		os.Exit(128 + int(s.(syscall.Signal)))
	}()

	attach := contract.WireCall{ID: 2, Op: contract.OpAttach, W: float64(w), H: float64(h), System: whose(os.Getenv, c.Kit.Platform.System())}
	for _, name := range attachEnv {
		if v, ok := os.LookupEnv(name); ok {
			attach.Env = append(attach.Env, name+"="+v)
		}
	}
	mu.Lock()
	if restore != nil {
		asked = leave
		os.Stdout.WriteString(enter)
	}
	mu.Unlock()
	if err = l.call(attach); err != nil {
		return nil, refuse("not_connected", "The window is not connected: "+err.Error()+".", "whaleshark engine status")
	}
	go term.Watch(w, h, func(w, h int) {
		l.call(contract.WireCall{Op: contract.OpWindowSize, W: float64(w), H: float64(h)})
	})
	// What is typed, clicked and pasted goes to the keeper as it comes; when
	// the terminal is gone, so is the window.
	go func() {
		b := make([]byte, 32<<10)
		for {
			n, err := os.Stdin.Read(b)
			if n > 0 && l.send(contract.FrameBytes, b[:n]) != nil || err != nil {
				l.Close()
				return
			}
		}
	}()
	// What the keeper composed goes to the terminal as it comes. The keeper
	// ends the window by closing the connection, with or without a last word.
	for {
		kind, body, err := contract.ReadFrame(l)
		var r contract.WireReply
		switch {
		case err != nil:
			back()
			fmt.Fprintln(c.Out, "The window is closed. Open it again with: whaleshark open")
			return nil, nil
		case kind == contract.FrameBytes:
			put(body)
		case kind == contract.FrameReply && json.Unmarshal(body, &r) == nil && r.Err != "":
			return nil, refuse("closed", "The keeper closed the window: "+contract.ErrOf(r).Error()+".")
		}
	}
}
