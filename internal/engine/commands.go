// Package engine is the Terminals interface over the keeper's socket.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Plug puts the keeper behind the kit where the surroundings name it: in a
// pane of the keeper's, and where a socket is named, as a test does. Anywhere
// else herdr stays behind the kit until the swap, which takes the condition out.
func Plug(k *contract.Kit) {
	if os.Getenv(contract.EnvSocket) != "" || os.Getenv(contract.EnvTermPane) != "" {
		k.Terms = New(k)
	}
}

// patience is how long the keeper is given to answer, beyond the time a
// call itself allows an agent.
const patience = 10 * time.Second

// Client is the keeper as the rest of the program sees it. Every call is a
// connection of its own: the hello, the call, its answer. Nothing is kept
// between two calls, so a keeper that was started again is simply the one
// the next call reaches.
type Client struct {
	// Dial opens a connection to the keeper. A test puts its own here.
	Dial func(ctx context.Context) (net.Conn, error)
}

// New returns the client of the keeper whose socket the surroundings name.
func New(k *contract.Kit) *Client {
	return &Client{Dial: func(ctx context.Context) (net.Conn, error) {
		dirs, err := k.Platform.Dirs()
		if err != nil {
			return nil, err
		}
		d := net.Dialer{Timeout: time.Second}
		return d.DialContext(ctx, "unix", contract.SocketPath(os.Getenv, dirs))
	}}
}

// read takes one answer off a connection. One that cannot be read is a
// keeper that cannot be reached.
func read(conn net.Conn) (r contract.WireReply, err error) {
	kind, body, err := contract.ReadFrame(conn)
	if err == nil && kind != contract.FrameReply {
		err = contract.ErrFrame
	}
	if err == nil {
		err = json.Unmarshal(body, &r)
	}
	if err != nil {
		return r, fmt.Errorf("%w: %v", contract.ErrEngineUnreachable, err)
	}
	return r, contract.ErrOf(r)
}

func ask(conn net.Conn, call contract.WireCall) (contract.WireReply, error) {
	body, err := json.Marshal(call)
	if err == nil {
		err = contract.WriteFrame(conn, contract.FrameCall, body)
	}
	if err != nil {
		return contract.WireReply{}, fmt.Errorf("%w: %v", contract.ErrEngineUnreachable, err)
	}
	return read(conn)
}

// open connects and says hello, and has the end of ctx end the connection.
// A keeper of another version answers the hello with ErrWireVersion.
func (c *Client) open(ctx context.Context, wait time.Duration) (net.Conn, func() bool, error) {
	conn, err := c.Dial(ctx)
	if err != nil {
		return nil, nil, contract.ErrEngineUnreachable
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(patience + wait))
	if _, err = ask(conn, contract.WireCall{V: contract.WireVersion, Op: contract.OpHello}); err != nil {
		stop()
		conn.Close()
		return nil, nil, err
	}
	return conn, stop, nil
}

// Call makes one call to the keeper and returns its answer. The methods of
// Terminals are built on it, and so is whatever else speaks to the keeper
// once: `hook state` sends its OpHook through here.
func (c *Client) Call(ctx context.Context, call contract.WireCall) (contract.WireReply, error) {
	conn, stop, err := c.open(ctx, time.Duration(call.Millis)*time.Millisecond)
	if err != nil {
		return contract.WireReply{}, err
	}
	defer conn.Close()
	defer stop()
	r, err := ask(conn, call)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return r, err
}

// do is a call that answers with nothing but whether it went well.
func (c *Client) do(call contract.WireCall) error {
	_, err := c.Call(context.Background(), call)
	return err
}

// pane is a call that answers with a pane.
func (c *Client) pane(call contract.WireCall) (contract.Pane, error) {
	r, err := c.Call(context.Background(), call)
	if err == nil && r.Pane == nil {
		err = contract.ErrFrame
	}
	if err != nil {
		return contract.Pane{}, err
	}
	return *r.Pane, nil
}

func (c *Client) Version() (string, error) {
	r, err := c.Call(context.Background(), contract.WireCall{Op: contract.OpVersion})
	return r.Text, err
}

func (c *Client) Snapshot(ctx context.Context) (*contract.Snapshot, error) {
	r, err := c.Call(ctx, contract.WireCall{Op: contract.OpSnapshot})
	if err == nil && r.Snapshot == nil {
		err = contract.ErrFrame
	}
	return r.Snapshot, err
}

// Events reads the picture and then every change on one connection, which
// stays open until ctx ends or the keeper stops. A reader too slow for the
// keeper is closed by it and asks again, as after any end of the channel.
func (c *Client) Events(ctx context.Context) (*contract.Snapshot, <-chan contract.TermEvent, error) {
	conn, stop, err := c.open(ctx, 0)
	if err != nil {
		return nil, nil, err
	}
	first, err := ask(conn, contract.WireCall{Op: contract.OpEvents})
	if err == nil && first.Snapshot == nil {
		err = contract.ErrFrame
	}
	if err != nil {
		stop()
		conn.Close()
		return nil, nil, err
	}
	conn.SetDeadline(time.Time{})
	events := make(chan contract.TermEvent)
	go func() {
		defer close(events)
		defer conn.Close()
		defer stop()
		for {
			r, err := read(conn)
			if err != nil || r.Event == nil {
				return
			}
			select {
			case events <- *r.Event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return first.Snapshot, events, nil
}

func (c *Client) TabCreate(cwd, label string, env []string) (contract.Pane, error) {
	return c.pane(contract.WireCall{Op: contract.OpTabCreate, Cwd: cwd, Label: label, Env: env})
}

func (c *Client) TabRename(tab, label string) error {
	return c.do(contract.WireCall{Op: contract.OpTabRename, Tab: tab, Label: label})
}

func (c *Client) TabFocus(tab string) error {
	return c.do(contract.WireCall{Op: contract.OpTabFocus, Tab: tab})
}

func (c *Client) TabClose(tab string) error {
	return c.do(contract.WireCall{Op: contract.OpTabClose, Tab: tab})
}

func (c *Client) AgentStart(name, kind, pane string, args []string, timeout time.Duration) error {
	return c.do(contract.WireCall{Op: contract.OpAgentStart, Name: name, Kind: kind, Pane: pane, Argv: args, Millis: timeout.Milliseconds()})
}

func (c *Client) Prompt(name, text string, timeout time.Duration) error {
	return c.do(contract.WireCall{Op: contract.OpPrompt, Name: name, Text: text, Millis: timeout.Milliseconds()})
}

func (c *Client) Point(pane string, p contract.Pointer, arg string) error {
	return c.do(contract.WireCall{Op: contract.OpPoint, Pane: pane, Pointer: p, Text: arg})
}

func (c *Client) Run(pane string, argv []string) error {
	return c.do(contract.WireCall{Op: contract.OpRun, Pane: pane, Argv: argv})
}

func (c *Client) Screen(pane string) (string, error) {
	r, err := c.Call(context.Background(), contract.WireCall{Op: contract.OpScreen, Pane: pane})
	return r.Text, err
}

func (c *Client) Split(pane, direction string, ratio float64) (contract.Pane, error) {
	return c.pane(contract.WireCall{Op: contract.OpSplit, Pane: pane, Dir: direction, Ratio: ratio})
}

func (c *Client) Swap(source, target string) error {
	return c.do(contract.WireCall{Op: contract.OpSwap, Pane: source, Target: target})
}

func (c *Client) Resize(pane, direction string, amount float64) error {
	return c.do(contract.WireCall{Op: contract.OpResize, Pane: pane, Dir: direction, Ratio: amount})
}

func (c *Client) PaneClose(pane string) error {
	return c.do(contract.WireCall{Op: contract.OpPaneClose, Pane: pane})
}

func (c *Client) Size(pane string) (w, h int, err error) {
	r, err := c.Call(context.Background(), contract.WireCall{Op: contract.OpSize, Pane: pane})
	return r.W, r.H, err
}

func (c *Client) PaneFocus(pane, direction string) (string, error) {
	r, err := c.Call(context.Background(), contract.WireCall{Op: contract.OpPaneFocus, Pane: pane, Dir: direction})
	return r.Text, err
}

func (c *Client) Notify(title, body string, sound bool) (reason, delivery string, err error) {
	r, err := c.Call(context.Background(), contract.WireCall{Op: contract.OpNotify, Title: title, Text: body, Sound: sound})
	return r.Text, r.Delivery, err
}

func (c *Client) SetKeys(entries []contract.KeyEntry) error {
	return c.do(contract.WireCall{Op: contract.OpSetKeys, Keys: entries})
}

func (c *Client) Overlay(argv []string, w, h float64) error {
	return c.do(contract.WireCall{Op: contract.OpOverlay, Argv: argv, W: w, H: h})
}
