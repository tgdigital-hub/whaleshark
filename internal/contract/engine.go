package contract

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// What the packages of the engine share: a pane's terminal, the keeper's
// socket with its frames and messages, and the settings. A length before
// each message is the ordinary way to cut a stream into messages.

// PtySpec is what a pane's terminal is started with: a program and its
// arguments, never a line for a shell. Env is the whole environment.
type PtySpec struct {
	Argv       []string
	Dir        string
	Env        []string
	Cols, Rows int
}

// Pty is one pane's terminal with its program in it. Read gives what is
// printed there and ends with io.EOF once everything in the terminal has
// ended; Write types into it; Close ends the program and all it started.
// On Windows the terminal ends with its program: what the program left
// running ends with it, where on the other systems it keeps the terminal open.
type Pty interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
	// Wait blocks until the program has ended and returns its exit code.
	Wait() (int, error)
	// Front names the program in front in the terminal, which tells an agent
	// from the shell it left behind. ErrCannotTell where the system has no such thing.
	Front() (string, error)
}

// NoPty is the kit's Pty until the pty package plugs its own in.
func NoPty(PtySpec) (Pty, error) { return nil, ErrNotBuilt }

// The pane variables the keeper sets where herdr sets EnvPane and
// EnvActivePane, and the variable that moves the keeper's socket.
const (
	EnvTermPane       = "WHALESHARK_PANE"
	EnvTermActivePane = "WHALESHARK_ACTIVE_PANE"
	EnvSocket         = "WHALESHARK_SOCKET"
)

// PaneOf is the pane a command was started in and, for the command of a
// shortcut, the pane that had the keys: the keeper's variables first, then
// herdr's. Every caller rule reads through it, on either backend.
func PaneOf(getenv func(string) string) (pane, active string) {
	if pane, active = getenv(EnvTermPane), getenv(EnvTermActivePane); pane == "" && active == "" {
		pane, active = getenv(EnvPane), getenv(EnvActivePane)
	}
	return pane, active
}

// SocketPath is the keeper's socket file: where EnvSocket says, which is how
// a test has a keeper of its own, else engine.sock in the login's state folder.
func SocketPath(getenv func(string) string, d Dirs) string {
	if p := getenv(EnvSocket); p != "" {
		return p
	}
	return filepath.Join(d.State, "engine.sock")
}

// A frame is its kind, the length of its body in four bytes, and the body.
// FrameCall holds one WireCall, FrameReply one WireReply, both as JSON; FrameBytes
// holds bytes of a window's stream, either way. WireVersion is the number
// the first WireCall of a connection carries.
const (
	FrameCall   byte = 'c'
	FrameReply  byte = 'r'
	FrameBytes  byte = 'b'
	FrameMax         = 1 << 20
	WireVersion      = 1
)

// ErrFrame is a frame of an unknown kind or with a body over FrameMax.
var ErrFrame = errors.New("malformed frame")

// WriteFrame writes one frame in one write.
func WriteFrame(w io.Writer, kind byte, body []byte) error {
	if len(body) > FrameMax {
		return ErrFrame
	}
	head := binary.BigEndian.AppendUint32([]byte{kind}, uint32(len(body))) // #nosec G115 -- at most FrameMax, checked above
	_, err := w.Write(append(head, body...))
	return err
}

// ReadFrame reads one frame, and refuses a malformed one before it reads the body.
func ReadFrame(r io.Reader) (kind byte, body []byte, err error) {
	var head [5]byte
	if _, err = io.ReadFull(r, head[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(head[1:])
	if kind = head[0]; kind != FrameCall && kind != FrameReply && kind != FrameBytes || n > FrameMax {
		return 0, nil, ErrFrame
	}
	body = make([]byte, n)
	_, err = io.ReadFull(r, body)
	return kind, body, err
}

// The calls. OpHello is the first of every connection and carries V. The
// next names stand for the methods of Terminals, one each. After OpEvents
// the keeper answers once with the snapshot and then with one WireReply for each
// event, until the connection ends. After OpAttach the connection is a
// window's stream: FrameBytes both ways, and OpWindowSize when the window's
// size changes. OpAttach itself gets no reply; a reply with an error, at any
// moment after it, ends the window with that message. OpHook is what `hook state` sends; OpStop and OpStatus are
// `engine stop` and `engine status`.
const (
	OpHello      = "hello"
	OpVersion    = "version"
	OpSnapshot   = "snapshot"
	OpEvents     = "events"
	OpTabCreate  = "tab-create"
	OpTabRename  = "tab-rename"
	OpTabFocus   = "tab-focus"
	OpTabClose   = "tab-close"
	OpAgentStart = "agent-start"
	OpPrompt     = "prompt"
	OpPoint      = "point"
	OpRun        = "run"
	OpScreen     = "screen"
	OpSplit      = "split"
	OpSwap       = "swap"
	OpResize     = "resize"
	OpPaneClose  = "pane-close"
	OpSize       = "size"
	OpPaneFocus  = "pane-focus"
	OpNotify     = "notify"
	OpSetKeys    = "set-keys"
	OpOverlay    = "overlay"
	OpAttach     = "attach"
	OpWindowSize = "window-size"
	OpHook       = "hook"
	OpStop       = "stop"
	OpStatus     = "status"
)

// WireCall is one call to the keeper. ID is the caller's own number for it and
// comes back in the WireReply. A call fills the fields its method's arguments
// name and leaves the rest: Pane is also Swap's source and Target its
// target; Text is Prompt's text, Point's argument and Notify's body; Dir is
// a direction; Ratio is Split's ratio and Resize's amount; W and H are
// Overlay's size in shares or cells, and the window's size in cells for
// OpAttach and OpWindowSize; Millis is a timeout. For OpAttach, Env
// holds the window's TERM, COLORTERM, NO_COLOR and TERM_PROGRAM, System
// the system the person sits at and Reach how the window got here. For
// OpHook, Kind is the agent's event, Pane the pane the hook ran in, Session
// and Cwd what the agent said. From is the keeper's pane the caller itself
// runs in, when it runs in one: the keeper refuses an agent it started the
// calls that are not an agent's to make.
type WireCall struct {
	V       int        `json:"v,omitempty"`
	ID      uint64     `json:"id,omitempty"`
	Op      string     `json:"op"`
	Pane    string     `json:"pane,omitempty"`
	Tab     string     `json:"tab,omitempty"`
	Target  string     `json:"target,omitempty"`
	Name    string     `json:"name,omitempty"`
	Kind    string     `json:"kind,omitempty"`
	Label   string     `json:"label,omitempty"`
	Cwd     string     `json:"cwd,omitempty"`
	Text    string     `json:"text,omitempty"`
	Title   string     `json:"title,omitempty"`
	Dir     string     `json:"dir,omitempty"`
	Pointer Pointer    `json:"pointer,omitempty"`
	Session string     `json:"session,omitempty"`
	System  string     `json:"system,omitempty"`
	Reach   string     `json:"reach,omitempty"`
	From    string     `json:"from,omitempty"`
	Argv    []string   `json:"argv,omitempty"`
	Env     []string   `json:"env,omitempty"`
	Keys    []KeyEntry `json:"keys,omitempty"`
	Ratio   float64    `json:"ratio,omitempty"`
	W       float64    `json:"w,omitempty"`
	H       float64    `json:"h,omitempty"`
	Millis  int64      `json:"millis,omitempty"`
	Sound   bool       `json:"sound,omitempty"`
}

// How a window got to the keeper. Only a local one sits at the keeper's own
// clipboard; one that does not say is taken for the least: a bare ssh.
const (
	ReachLocal   = "local"
	ReachSSH     = "ssh"
	ReachConnect = "connect"
	ReachPage    = "page"
)

// Version is the program's version, which the cli sets at its start: what
// the keeper answers, and the name its panes' programs are told.
var Version = "dev"

// WireReply answers the WireCall with the same ID. Err is empty, or the name of the
// error (ErrCode) with Message for a person. Text is Version's, Screen's and
// PaneFocus's answer and Notify's reason; Delivery is Notify's second
// answer; W and H are Size's. Event is set in the replies after OpEvents.
type WireReply struct {
	ID       uint64     `json:"id,omitempty"`
	Err      string     `json:"err,omitempty"`
	Message  string     `json:"message,omitempty"`
	Text     string     `json:"text,omitempty"`
	Delivery string     `json:"delivery,omitempty"`
	W        int        `json:"w,omitempty"`
	H        int        `json:"h,omitempty"`
	Pane     *Pane      `json:"pane,omitempty"`
	Snapshot *Snapshot  `json:"snapshot,omitempty"`
	Event    *TermEvent `json:"event,omitempty"`
}

// ErrWireVersion is the keeper's answer to an OpHello with another number
// than its own: one of the two programs is older, and the keeper must be
// started again.
var ErrWireVersion = errors.New("restart needed")

// wireErrors are the errors that keep their identity across the socket.
var wireErrors = []error{ErrAgentNotReady, ErrAgentBlocked, ErrPromptStalled, ErrNoPane,
	ErrEngineUnreachable, ErrNotBuilt, ErrCannotTell, ErrWireVersion, ErrFrame}

// ErrCode is the name an error travels under in WireReply.Err: its own text for
// an error callers tell apart, "failed" for any other, empty for none.
func ErrCode(err error) string {
	for _, e := range wireErrors {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	if err != nil {
		return "failed"
	}
	return ""
}

// ErrOf is the error a WireReply carries: one errors.Is finds, with the
// keeper's message behind its name.
func ErrOf(r WireReply) error {
	for _, e := range wireErrors {
		if r.Err == e.Error() && r.Message == "" {
			return e
		} else if r.Err == e.Error() {
			return fmt.Errorf("%w: %s", e, r.Message)
		}
	}
	if r.Err == "" {
		return nil
	}
	return errors.New(r.Message)
}

// Settings is settings.toml in the login's settings folder: the person's
// own choices for the engine. `set` changes it and the keeper notices.
type Settings struct {
	Keys struct {
		// Command is the key that starts a shortcut. Style is whose copy keys
		// apply: auto, mac, windows or linux.
		Command string `toml:"command"`
		Style   string `toml:"style"`
	} `toml:"keys"`
	Scrollback struct {
		// Lines a pane keeps, and how many of them survive a restart; 0 keeps none.
		Lines int `toml:"lines"`
		Keep  int `toml:"keep"`
	} `toml:"scrollback"`
	Clipboard struct {
		// Programs says whether a program in a pane may write the clipboard: off, ask or on.
		Programs string `toml:"programs"`
	} `toml:"clipboard"`
	// Shell is what a new pane starts; empty is the login's own shell.
	Shell     []string   `toml:"shell"`
	Shortcuts []KeyEntry `toml:"shortcut"`
}

// SettingKeys are the names `set` takes for the engine's settings.
var SettingKeys = []string{"keys.command", "keys.style", "scrollback.lines", "scrollback.keep", "clipboard.programs", "shell"}

// SettingsPath is the settings file.
func SettingsPath(d Dirs) string { return filepath.Join(d.Config, "settings.toml") }

// ReadSettings reads the settings over what holds when the file says
// nothing; a file that is not there gives exactly that. read is the
// platform's Peek, or its Read for a caller that writes the file back.
func ReadSettings(read func(string) ([]byte, error), d Dirs) (Settings, error) {
	var s Settings
	s.Keys.Command, s.Keys.Style = "ctrl+space", "auto"
	s.Scrollback.Lines, s.Scrollback.Keep = 5000, 2000
	s.Clipboard.Programs = "ask"
	data, err := read(SettingsPath(d))
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	} else if err != nil {
		return s, err
	}
	return s, toml.Unmarshal(data, &s)
}
