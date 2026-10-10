// Package keeper is the keeper, `engine run`: the one program of a login
// that owns every pane's terminal. It listens on one socket file, starts
// each pane's program from a list of arguments, reads what it prints into a
// picture, and draws every attached window from those pictures. A server
// that owns the terminals, with windows that are only views of it, is the
// ordinary shape of such a program; this one is written from the design's
// own description of it and from the contract's frames.
package keeper

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Plug binds `engine`.
func Plug(k *contract.Kit) { k.Handle("engine", engine) }

func refuse(exit int, code, message string, next ...string) *contract.Refusal {
	return &contract.Refusal{Exit: exit, Code: code, Message: message, Next: next}
}

// state is what `engine stop` and `engine status` answer.
type state struct {
	Running  bool   `json:"running"`
	Instance string `json:"instance,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

func engine(c *contract.Call) (any, error) {
	if len(c.Args) != 1 {
		return nil, refuse(contract.ExitUsage, "usage", "engine takes one word: run, stop or status.", "whaleshark help engine")
	}
	switch c.Args[0] {
	case "run":
		k, err := open(c.Kit)
		if err != nil {
			return nil, refuse(contract.ExitEnv, "not_started", "The keeper did not start: "+err.Error()+".", "whaleshark engine status")
		}
		// The hang-up is taken, never ignored: an ignored signal would
		// be handed on to every program a pane starts.
		ended := make(chan os.Signal, 1)
		signal.Notify(ended, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		go func() {
			<-ended
			k.stop()
		}()
		return nil, k.serve()
	case "stop", "status":
		r, err := ask(c.Kit, map[string]string{"stop": contract.OpStop, "status": contract.OpStatus}[c.Args[0]])
		switch {
		case errors.Is(err, contract.ErrWireVersion):
			return nil, refuse(contract.ExitEnv, "restart_needed", "The keeper that is running is another version of whaleshark than this one: it must be started again.",
				"whaleshark engine stop")
		case errors.Is(err, contract.ErrEngineUnreachable):
			fmt.Fprintln(c.Out, "No keeper is running.")
			return state{}, nil
		case err != nil:
			return nil, err
		case c.Args[0] == "stop":
			fmt.Fprintln(c.Out, "The keeper has stopped, and every pane's program with it.")
			return state{}, nil
		}
		fmt.Fprintf(c.Out, "The keeper is running: %s.\n", r.Text)
		return state{Running: true, Instance: r.Snapshot.Instance, Detail: r.Text}, nil
	}
	return nil, refuse(contract.ExitUsage, "usage", "engine takes run, stop or status, not "+c.Args[0]+".", "whaleshark help engine")
}

// ask makes one call to the running keeper. A stop goes without a hello, so
// that a keeper of another version can be stopped too, and is answered
// before the keeper stops: the end of the connection is waited for as well.
func ask(k *contract.Kit, op string) (r contract.WireReply, err error) {
	dirs, err := k.Platform.Dirs()
	if err != nil {
		return r, err
	}
	conn, err := net.DialTimeout("unix", contract.SocketPath(os.Getenv, dirs), time.Second)
	if err != nil {
		return r, contract.ErrEngineUnreachable
	}
	defer conn.Close()
	l := &link{conn: conn}
	calls := []contract.WireCall{{V: contract.WireVersion, ID: 1, Op: contract.OpHello}, {ID: 2, Op: op}}
	if op == contract.OpStop {
		calls = calls[1:]
	}
	for _, call := range calls {
		body, _ := json.Marshal(call)
		if err = l.send(contract.FrameCall, body); err != nil {
			return r, err
		}
		if _, body, err = contract.ReadFrame(conn); err == nil {
			if err = json.Unmarshal(body, &r); err == nil {
				err = contract.ErrOf(r)
			}
		}
		if err != nil {
			return r, err
		}
	}
	if op == contract.OpStop {
		contract.ReadFrame(conn)
	}
	return r, nil
}
