package contract

import (
	"fmt"
	"io"
	"time"
)

// Refusal is an error a command ends with: its exit code, a code for
// programs, the message for people, and the exact commands to run next.
type Refusal struct {
	Exit    int      `json:"-"`
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Next    []string `json:"next,omitempty"`
	Data    any      `json:"data,omitempty"`
}

func (r *Refusal) Error() string { return r.Message }

// Call is one run of a command, after its arguments and its caller have been
// checked and before anything is touched.
type Call struct {
	Kit     *Kit
	Command *Command
	Args    []string            // what is left once the flags are taken out
	Flags   map[string][]string // by name, without the dashes; a switch holds ""
	Caller  Caller
	Root    string
	Run     string
	JSON    bool
	Now     time.Time
	Stdin   io.Reader
	Out     io.Writer // short text for a person; discarded under --json
	Err     io.Writer
}

// Handler runs a command. What it returns is the result in the JSON
// envelope; an error that is a *Refusal carries its own exit code, any other
// is exit 1.
type Handler func(c *Call) (result any, err error)

// Kit is what the entry point builds once and hands to every package: the
// handlers bound so far and the things behind each interface. A package's
// commands.go has one function, Plug(*Kit), which binds its handlers with
// Handle and puts its own implementation in place of a stand-in. The panes,
// the page and the connection never name Store: they read through Reader,
// and what they need changed they start as a child command. Rules has no
// stand-in: the real ones are pure, so a test plugs them in.
type Kit struct {
	Rules Rules
	Store Store
	Terms Terminals
	// Pty starts a pane's terminal; the keeper and its tests take it from here.
	Pty           func(PtySpec) (Pty, error)
	Platform      Platform
	Placement     Placement
	Integrator    Integrator
	Overlap       Overlap
	Notifier      Notifier
	Evidence      Evidence
	AgentSettings AgentSettings
	Sweeper       Sweeper
	// View builds the one view model; the view package puts the builder here.
	View func(ViewInput) *View
	// Main parses the arguments, works out the caller, runs the handler and
	// returns the exit code; in is the command's standard input. The cli
	// package replaces the one NewKit sets.
	Main func(k *Kit, args []string, in io.Reader, out, errw io.Writer) int

	handlers map[string]Handler
}

// NewKit returns a kit with every stand-in in place and nothing bound.
func NewKit() *Kit {
	return &Kit{
		Store: NoStore{}, Terms: NoTerminals{}, Pty: NoPty, Platform: NoPlatform{}, Placement: NoPlacement{},
		Integrator: NoIntegrator{}, Overlap: NoOverlap{}, Notifier: NoNotifier{},
		Evidence: NoEvidence{}, AgentSettings: NoAgentSettings{}, Sweeper: NoSweeper{},
		View: noView, Main: listOnly, handlers: map[string]Handler{},
	}
}

// noView is the view model until the builder is plugged in: it says so.
func noView(in ViewInput) *View {
	return &View{Version: ViewVersion, At: in.Now, Caller: in.Caller,
		Alerts: []string{"the view model is " + ErrNotBuilt.Error()}, Fresh: Fresh{Checked: in.Checked}}
}

// Reader is the store with nothing on it that writes.
func (k *Kit) Reader() Reader { return k.Store }

// Handle binds the handler of a command.
func (k *Kit) Handle(command string, h Handler) { k.handlers[command] = h }

// Handler returns the handler bound to a command, or nil while it is not built.
func (k *Kit) Handler(command string) Handler { return k.handlers[command] }

// listOnly is all the program does before the cli package exists: it lists
// the commands and runs what is bound, with no flags and no caller check.
func listOnly(k *Kit, args []string, in io.Reader, out, errw io.Writer) int {
	if len(args) == 0 || args[0] == "help" {
		for _, c := range Commands {
			built := ""
			if k.handlers[c.Name] == nil {
				built = "  (" + ErrNotBuilt.Error() + ")"
			}
			fmt.Fprintf(out, "  %-9s %s%s\n", c.Name, c.Help, built)
		}
		return ExitOK
	}
	h := k.handlers[args[0]]
	if h == nil && Find(args[0]) == nil {
		fmt.Fprintf(errw, "whaleshark: unknown command %q\nNext: whaleshark help\n", args[0])
		return ExitUsage
	}
	if h == nil {
		fmt.Fprintf(errw, "whaleshark %s: %v\n", args[0], ErrNotBuilt)
		return ExitFailed
	}
	_, err := h(&Call{Kit: k, Command: Find(args[0]), Args: args[1:], Now: Now(), Stdin: in, Out: out, Err: errw})
	if err != nil {
		fmt.Fprintln(errw, err)
		return ExitFailed
	}
	return ExitOK
}
