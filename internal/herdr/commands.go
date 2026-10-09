// Package herdr is the one place that names herdr: its command line for
// every call, and its socket for the event stream. It has no call that sends
// a key to an agent.
package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Plug puts the real adapter into the kit.
func Plug(k *contract.Kit) { k.Terms = New(k) }

// Adapter is contract.Terminals over the herdr program on the PATH.
type Adapter struct {
	kit *contract.Kit
	// Call runs herdr once and returns what it printed and its exit code. The
	// fake herdr puts itself here.
	Call func(ctx context.Context, env, args []string) (stdout, stderr []byte, exit int, err error)
	// Settings is herdr's settings file. Empty means the file herdr reads; a
	// test names a copy, and then herdr is never told to reload.
	Settings string
}

// New returns an adapter that starts the herdr program for every call.
func New(k *contract.Kit) *Adapter { return &Adapter{kit: k, Call: run} }

func run(ctx context.Context, env, args []string) ([]byte, []byte, int, error) {
	// #nosec G204 -- always the program herdr, with a list of arguments and no shell
	cmd := exec.CommandContext(ctx, "herdr", args...)
	cmd.Env = append(os.Environ(), env...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && ctx.Err() == nil {
		return out.Bytes(), errOut.Bytes(), exit.ExitCode(), nil
	}
	return out.Bytes(), errOut.Bytes(), 0, err
}

// Error is herdr saying no. Code is herdr's own code for it ("pane_not_found",
// "agent_blocked", "agent_prompt_stalled", "timeout" and so on), or one of
// the two below.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

const (
	// Unreachable: herdr is not installed, its server is not running, or it
	// did not answer in time.
	Unreachable = "unreachable"
	// Failed: herdr refused a call without a code, or answered something
	// this adapter cannot read.
	Failed = "failed"
)

func (e *Error) Error() string { return "herdr: " + e.Message + " (" + e.Code + ")" }

// Is makes herdr's codes the contract's errors.
func (e *Error) Is(target error) bool {
	switch target {
	case contract.ErrAgentNotReady, contract.ErrAgentBlocked, contract.ErrPromptStalled:
		return e.Code == target.Error()
	case contract.ErrNoPane:
		return e.Code == "pane_not_found" || e.Code == "agent_not_found"
	}
	return target == contract.ErrEngineUnreachable && e.Code == Unreachable
}

// Code returns herdr's code for an error of this package, or "".
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// wait is how long a call that should answer at once may take.
const wait = 10 * time.Second

// call runs one herdr command and decodes the "result" of its answer into v.
func (a *Adapter) call(ctx context.Context, limit time.Duration, v any, args ...string) error {
	out, err := a.text(ctx, limit, nil, args...)
	if err != nil || v == nil {
		return err
	}
	if err := json.Unmarshal(out, &struct{ Result any }{v}); err != nil {
		return &Error{Failed, "unreadable answer to " + strings.Join(args[:2], " ") + ": " + err.Error()}
	}
	return nil
}

func (a *Adapter) text(ctx context.Context, limit time.Duration, env []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	out, errOut, exit, err := a.Call(ctx, env, args)
	if err != nil {
		return nil, &Error{Unreachable, err.Error()}
	}
	if exit == 0 {
		return out, nil
	}
	var refusal struct{ Error *Error }
	if json.Unmarshal(errOut, &refusal) == nil && refusal.Error != nil {
		if refusal.Error.Code == "server_not_running" {
			refusal.Error.Code = Unreachable
		}
		return nil, refusal.Error
	}
	return nil, &Error{Failed, strings.Join(strings.Fields(string(errOut)+string(out)), " ")}
}

func (a *Adapter) do(args ...string) error {
	return a.call(context.Background(), wait, nil, args...)
}

type status struct {
	Client struct{ Version string }
	Server struct {
		Running bool
		Version string
		Socket  string
	}
}

func (a *Adapter) status(ctx context.Context) (status, error) {
	var s status
	out, err := a.text(ctx, wait, nil, "status", "--json")
	if err == nil && json.Unmarshal(out, &s) != nil {
		err = &Error{Failed, "unreadable answer to status"}
	}
	return s, err
}

// Version is the running server's version, or the program's when no server runs.
func (a *Adapter) Version() (string, error) {
	s, err := a.status(context.Background())
	if s.Server.Running {
		return s.Server.Version, err
	}
	return s.Client.Version, err
}

func (a *Adapter) Snapshot(ctx context.Context) (*contract.Snapshot, error) {
	var w struct{ Snapshot wireSnapshot }
	if err := a.call(ctx, wait, &w, "api", "snapshot"); err != nil {
		return nil, err
	}
	return w.Snapshot.snapshot(), nil
}

func (a *Adapter) TabCreate(cwd, label string, env []string) (contract.Pane, error) {
	args := []string{"tab", "create", "--cwd", cwd, "--label", label, "--no-focus"}
	for _, kv := range env {
		args = append(args, "--env", kv)
	}
	var w struct {
		RootPane record `json:"root_pane"`
		Tab      record
	}
	err := a.call(context.Background(), wait, &w, args...)
	p := w.RootPane.pane()
	p.Label = w.Tab.Label
	return p, err
}

func (a *Adapter) TabRename(tab, label string) error { return a.do("tab", "rename", tab, label) }
func (a *Adapter) TabFocus(tab string) error         { return a.do("tab", "focus", tab) }
func (a *Adapter) TabClose(tab string) error         { return a.do("tab", "close", tab) }

func ms(d time.Duration) string { return strconv.FormatInt(d.Milliseconds(), 10) }

// AgentStart returns when the agent is ready for its prompt. herdr takes a
// timeout above three seconds and of at most five minutes.
func (a *Adapter) AgentStart(name, kind, pane string, args []string, timeout time.Duration) error {
	argv := append([]string{"agent", "start", name, "--kind", kind, "--pane", pane, "--timeout", ms(timeout), "--"}, args...)
	return a.call(context.Background(), timeout+wait, nil, argv...)
}

// Prompt returns when the agent has started on the text, not when its turn ends.
func (a *Adapter) Prompt(name, text string, timeout time.Duration) error {
	return a.call(context.Background(), timeout+wait, nil,
		"agent", "prompt", name, text, "--wait", "--until", contract.StatusWorking, "--timeout", ms(timeout))
}

var pointers = map[contract.Pointer]bool{
	contract.PointEvents: true, contract.PointAnswer: true, contract.PointQuestion: true,
	contract.PointCarryOn: true, contract.PointAnswered: true, contract.PointMail: true,
	contract.PointResumed: true, contract.PointLeadOn: true,
}

var pointerArg = regexp.MustCompile(`^[A-Za-z0-9_-]{0,16}$`)

// Point queues the line behind whatever the agent is doing. herdr refuses an
// agent that sits at a prompt of its own ("agent_blocked") and types nothing.
func (a *Adapter) Point(pane string, p contract.Pointer, arg string) error {
	if !pointers[p] || !pointerArg.MatchString(arg) {
		return &Error{Failed, "not one of the fixed pointers"}
	}
	text := string(p)
	if strings.Contains(text, "%") {
		text = fmt.Sprintf(text, arg)
	}
	return a.do("agent", "prompt", pane, text)
}

func (a *Adapter) Run(pane string, argv []string) error {
	return a.do("pane", "run", pane, a.kit.Platform.Quote("", argv))
}

func (a *Adapter) Screen(pane string) (string, error) {
	out, err := a.text(context.Background(), wait, nil, "pane", "read", pane, "--source", "visible")
	return string(out), err
}

func fraction(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// errEngine is the answer to what only the engine can do.
var errEngine = errors.New("herdr cannot do this; WhaleShark's own engine will")

// Split never moves the focus. The new pane's Label is left empty. herdr
// splits to the right and below only, and only by a share.
func (a *Adapter) Split(pane, direction string, ratio float64) (contract.Pane, error) {
	if direction == contract.Left || direction == contract.Up || ratio > 1 {
		return contract.Pane{}, errEngine
	}
	var w struct{ Pane record }
	err := a.call(context.Background(), wait, &w,
		"pane", "split", pane, "--direction", direction, "--ratio", fraction(ratio), "--no-focus")
	return w.Pane.pane(), err
}

// Swap exchanges two panes of one tab and gives the focus to source.
func (a *Adapter) Swap(source, target string) error {
	return a.do("pane", "swap", "--source-pane", source, "--target-pane", target)
}

// Resize moves the dividing line towards direction; herdr ignores the sign
// of amount and keeps every pane between a tenth and nine tenths of its split.
func (a *Adapter) Resize(pane, direction string, amount float64) error {
	return a.do("pane", "resize", "--pane", pane, "--direction", direction, "--amount", fraction(amount))
}

func (a *Adapter) PaneClose(pane string) error { return a.do("pane", "close", pane) }

func (a *Adapter) Size(pane string) (w, h int, err error) {
	var l struct {
		Layout struct {
			Panes []struct {
				ID   string `json:"pane_id"`
				Rect struct{ Width, Height int }
			}
		}
	}
	err = a.call(context.Background(), wait, &l, "pane", "layout", "--pane", pane)
	for _, p := range l.Layout.Panes {
		if p.ID == pane {
			w, h = p.Rect.Width, p.Rect.Height
		}
	}
	return w, h, err
}

// Overlay is the engine's: herdr has a pop-up only behind a shortcut.
func (a *Adapter) Overlay([]string, float64, float64) error { return errEngine }

// PaneFocus has only the neighbour form, which is the one way herdr has.
func (a *Adapter) PaneFocus(pane, direction string) (string, error) {
	if direction == "" {
		return "", errEngine
	}
	var w struct {
		Focus struct {
			Focused string `json:"focused_pane_id"`
		}
	}
	err := a.call(context.Background(), wait, &w, "pane", "focus", "--pane", pane, "--direction", direction)
	return w.Focus.Focused, err
}

// Notify returns herdr's reason beside herdr's own pop-up setting: with
// delivery "off" herdr 0.9.1 still answers "shown" and draws nothing.
func (a *Adapter) Notify(title, body string, sound bool) (reason, delivery string, err error) {
	if delivery, err = a.delivery(); err != nil {
		return "", "", err
	}
	kind := "none"
	if sound {
		kind = "request"
	}
	var w struct{ Reason string }
	err = a.call(context.Background(), wait, &w, "notification", "show", title, "--body", body, "--sound", kind)
	return w.Reason, delivery, err
}
