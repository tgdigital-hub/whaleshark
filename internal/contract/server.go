package contract

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// What a server set up by `whaleshark server` has in fixed places: its file,
// the shared browser and the Node that runs it, and the two service entries
// of each work login. The browser's place goes into every tab as EnvBrowsers.
const (
	ServerFile     = "/etc/whaleshark/server.toml"
	ServerBrowsers = "/opt/whaleshark/browsers"
	ServerNode     = "/opt/whaleshark/node"
	UnitKeeper     = "whaleshark-keeper.service"
	UnitDash       = "whaleshark-dash.service"
	EnvBrowsers    = "PLAYWRIGHT_BROWSERS_PATH"
)

// The two roads a phone can come in by, of which a server names one or none,
// and what is known of the two port rules that keep a login's block its own:
// doctor --server proves them as a second login and is the one that may
// write "proven".
const (
	RoadTailscale  = "tailscale"
	RoadCloudflare = "cloudflare"

	RulesProven   = "proven"
	RulesUnproven = "unproven"
	RulesOff      = "off"
)

// ServerPorts is the range every login's block lies in, which the system
// keeps out of the numbers it hands out by itself; BlockSize is one login's
// share. The range starts above a person's own machine's block and above the
// ports Cloudflare's connector opens for its figures.
var ServerPorts = Ports{Base: 21000, Count: 11000}

const BlockSize = 1000

// Server is server.toml: what the administrator decided for the machine and
// for each work login. Only `whaleshark server` writes it, as root; everybody
// reads it. A machine without the file is no server: a person's own computer.
type Server struct {
	Versioned
	// Agents is the most agents one login may run at once.
	Agents int `toml:"agents"`
	// Road is how phones reach the page: RoadTailscale, RoadCloudflare or
	// empty for no phone. Team is the team name at Cloudflare.
	Road  string `toml:"road"`
	Team  string `toml:"team"`
	Rules string `toml:"rules"`
	// Login is each work login by its name.
	Login map[string]Person `toml:"login"`
}

// Person is one work login's share of the machine. Ports is the first port
// of its block. Phone is the port Tailscale answers at for this person's
// phone, 0 for none. Name, Aud and Email are the Cloudflare road's: the
// public name, the tag of this person's Access application and the
// addresses its rule admits.
type Person struct {
	Ports  int      `toml:"ports"`
	Agents int      `toml:"agents"`
	Phone  int      `toml:"phone"`
	Name   string   `toml:"name"`
	Aud    string   `toml:"aud"`
	Email  []string `toml:"email"`
}

// Me is the name of the login this program runs as.
var Me = func() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.Username
}

// ReadServer reads the server's file. It returns nil and no error on a
// machine that has none. A file that is newer, cannot be read as written, or
// gives two logins the same port is an error, and its caller then treats the
// machine as a server it knows nothing about: no phone's door, no start.
func ReadServer(read func(path string) ([]byte, error)) (*Server, error) {
	data, err := read(ServerFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	s := &Server{Agents: 20}
	if err == nil {
		err = toml.Unmarshal(data, s)
	}
	switch {
	case err != nil:
		return nil, fmt.Errorf("%s: %w", ServerFile, err)
	case s.Version > FileVersion:
		return nil, ErrNewer
	case s.Version < 1:
		return nil, fmt.Errorf("%s: it carries no version", ServerFile)
	case !slices.Contains([]string{"", RoadTailscale, RoadCloudflare}, s.Road):
		return nil, fmt.Errorf("%s: road %q is neither %s nor %s", ServerFile, s.Road, RoadTailscale, RoadCloudflare)
	}
	for name, p := range s.Login {
		if !ServerPorts.holds(p.Ports) || (p.Ports-ServerPorts.Base)%BlockSize != 0 {
			return nil, fmt.Errorf("%s: the block of %s does not start at a block's first port", ServerFile, name)
		}
		for other, q := range s.Login {
			if other != name && q.Ports == p.Ports {
				return nil, fmt.Errorf("%s: %s and %s have one block", ServerFile, name, other)
			}
		}
	}
	return s, nil
}

func (p Ports) holds(port int) bool { return port >= p.Base && port < p.Base+p.Count }

// Block is the ports a login's tasks take their slots from: on a person's
// own machine DefaultPorts, on a server the login's block without its last
// port, which is the door. ok is false for a login the server does not list.
func (s *Server) Block(login string) (block Ports, ok bool) {
	if s == nil {
		return DefaultPorts, true
	}
	p, ok := s.Login[login]
	return Ports{Base: p.Ports, Count: BlockSize - 1}, ok
}

// Door is the loopback port a login's page listens on for phones while the
// person has phone access on: the last port of the login's block, so no
// task's slot is ever it. 0 where there is none.
func (s *Server) Door(login string) int {
	if s == nil || s.Login[login].Ports == 0 {
		return 0
	}
	return s.Login[login].Ports + BlockSize - 1
}

// Cap is the most agents a login may run: its own figure, else the
// machine's. 0 is no cap of the server's; start enforces the lower of this
// and the project's own limit.
func (s *Server) Cap(login string) int {
	if s == nil {
		return 0
	}
	if n := s.Login[login].Agents; n > 0 {
		return n
	}
	return s.Agents
}

// Free is the first port of the lowest block no login has, for server add.
func (s *Server) Free() (ports int, ok bool) {
	for ports = ServerPorts.Base; ServerPorts.holds(ports); ports += BlockSize {
		taken := false
		for _, p := range s.Login {
			taken = taken || p.Ports == ports
		}
		if !taken {
			return ports, true
		}
	}
	return 0, false
}

// Access is what the page checks a Cloudflare token against for one person.
type Access struct {
	Team, Name, Aud string
	Email           []string
}

// Issuer is the address a token of this team names and the keys are under.
func (a Access) Issuer() string { return "https://" + a.Team + ".cloudflareaccess.com" }

// Access returns a login's values on the Cloudflare road. ok is false when
// the road is another or any value is missing: the door then answers nothing.
func (s *Server) Access(login string) (a Access, ok bool) {
	if s == nil || s.Road != RoadCloudflare {
		return a, false
	}
	p := s.Login[login]
	a = Access{Team: s.Team, Name: p.Name, Aud: p.Aud, Email: p.Email}
	return a, a.Team != "" && a.Name != "" && a.Aud != "" && len(a.Email) > 0
}

// The header each road puts on a request to a phone's door. Tailscale's is
// its word for who is calling and is believed only because nothing but the
// system and the login itself can connect to the door; Cloudflare's is a
// signed token, which the page checks itself and takes nobody's word for.
const (
	HeaderTailscale = "Tailscale-User-Login"
	HeaderAccess    = "Cf-Access-Jwt-Assertion"
)

// Identity is who a road's header names as the caller at a phone's door, the
// one place that reads it. It is empty where the road gives none: a device
// with a tag on Tailscale, and always on the Cloudflare road, where the
// identity is the checked token's subject and never a header's word.
func Identity(road string, header func(name string) string) string {
	if road == RoadTailscale {
		return strings.TrimSpace(header(HeaderTailscale))
	}
	return ""
}

// Phones is phones.json in the login's state folder: whether the door for
// phones is open, and the phones this login has paired.
type Phones struct {
	Versioned
	On     bool    `json:"on"`
	Paired []Phone `json:"paired,omitempty"`
}

// Phone is one paired phone. Session is the TokenHash of the secret in its
// cookie, never the secret. Identity is who the road said was calling when
// it was paired, and the session is refused under any other; empty means
// the road named nobody and the cookie stands alone. Push is where its
// nudges go, nil until the person taps "Nudge me on this phone".
type Phone struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Identity string    `json:"identity,omitempty"`
	Session  string    `json:"session"`
	PairedAt time.Time `json:"paired_at"`
	SeenAt   time.Time `json:"seen_at,omitzero"`
	Push     *PushTo   `json:"push,omitempty"`
}

// PushTo is what a phone's browser hands over for its nudges: the address
// at its maker's push service and the two keys a message is sealed with.
type PushTo struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

const phonesFile = "phones.json"

// ReadPhones returns the login's phones.
func ReadPhones(read func(path string) ([]byte, error), stateDir string) (Phones, error) {
	var p Phones
	err := ReadVersioned(read, filepath.Join(stateDir, phonesFile), FileVersion, &p)
	return p, err
}

// ChangePhones is the one writer of phones.json: the page's server for a
// pairing, a forgotten phone and the door, the nudge for what a phone hands
// over and for a phone its maker's service says is gone.
func ChangePhones(p Platform, fn func(*Phones) error) error {
	var list Phones
	return change(p, phonesFile, &list, func() error {
		list.Version = FileVersion
		return fn(&list)
	})
}

// ListProject adds a project folder to the login's list. The list says
// which folders this login set up itself: init adds one, and so does the
// store when it makes a project's folder of ours. A folder of ours that is
// in no list came from somewhere else and is not used (cli).
func ListProject(p Platform, root string) error {
	var list Projects
	return change(p, projectsFile, &list, func() error {
		list.Version = FileVersion
		if !slices.ContainsFunc(list.Roots, func(r string) bool { return p.PathKey(r) == p.PathKey(root) }) {
			list.Roots = append(list.Roots, root)
		}
		return nil
	})
}

// Loopback is the address of this machine to itself, written out once here
// for every package that listens on it or connects to it. The laptop's page
// answers under these host names and no other, the first where a browser
// takes it for this computer; PagePort is the local port
// connect forwards to the page's socket unless told another. LiveBeat is
// how often a held-open connection says a line, so that nothing between
// the page and a phone takes it for dead.
var (
	Loopback  = net.IPv4(127, 0, 0, 1).String()
	PageHosts = []string{"whaleshark.localhost", Loopback}
)

const (
	PagePort = 7780
	LiveBeat = 30 * time.Second
)

// The page's routes. Everything that changes something is a POST to RouteDo
// followed by the id of one row of Actions, as an ordinary form: one field
// for each placeholder of the row, named as the placeholder without its
// braces ("text" for {file}, which the server writes to a private file),
// and FieldToken, the request token of the session. RouteTask is followed
// by a task's id; RouteEvidence by an attempt and the number of a file in
// the list Evidence gives for it, never a name. RouteSites answers connect
// with the ports of live tasks' slots that have a site answering, as a JSON
// list of numbers. RoutePair is all an unpaired phone is ever shown;
// RoutePush answers a paired phone's script with Kit.PushKey and takes what
// the phone then hands over for its nudges, a PushTo.
const (
	RouteTeam     = "/"
	RoutePlan     = "/plan"
	RouteMessages = "/messages"
	RouteJobs     = "/jobs"
	RouteTask     = "/task/"
	RouteLead     = "/lead"
	RouteIn       = "/in"
	RouteDo       = "/do/"
	RouteLive     = "/live"
	RouteEvidence = "/evidence/"
	RouteSites    = "/sites"
	RouteAsset    = "/a/"
	RoutePair     = "/pair"
	RoutePush     = "/push"

	FieldToken = "t"
	FieldCode  = "code"
)

// DashURL is what `dash url --json` answers and connect reads: the page's
// socket file, a sign-in code that works once for a minute, and the
// server's version.
type DashURL struct {
	Socket  string `json:"socket"`
	Code    string `json:"code"`
	Version string `json:"version"`
}

// Envelope is what a command prints with --json. The page takes every
// screen but the team's from it: the result of show, graph, log, catchup or
// status --everywhere, run as a child marked as the page's.
type Envelope struct {
	OK     bool     `json:"ok"`
	Result any      `json:"result,omitempty"`
	Error  *Refusal `json:"error,omitempty"`
}

// Job is one open run of the login as status --everywhere lists it.
type Job struct {
	Root string `json:"root"`
	View *View  `json:"view"`
	Next string `json:"next"`
}

// The screens of the page.
const (
	ScreenTeam     = "team"
	ScreenPlan     = "plan"
	ScreenMessages = "messages"
	ScreenTask     = "task"
	ScreenJobs     = "jobs"
	ScreenLead     = "lead"
	ScreenPair     = "pair"
	ScreenRefused  = "refused"
)

// Page is everything one screen of the page is drawn from. The server fills
// it and the templates print it: they compute no order, count, word or
// colour, and show a button only for an action named in the view. Token is
// the session's request token for FieldToken. Sites is the answering port
// of a task's running site, by task, and is empty on a phone, which cannot
// reach one. Lead is the last screenful of the lead agent's tab as plain
// text. Pair is the number an unpaired phone is shown, with nothing else on
// the page. Said is how the last button's command ended, shown where the
// click was made. Notes are plain lines under the top line, such as the
// road naming nobody at a pairing.
type Page struct {
	Screen             string
	Phone              bool
	Login, Host, Theme string
	Token              string
	View               *View
	Plan               *Plan
	Messages           []Message
	Task               *TaskView
	Catchup            *Catchup
	Jobs               []Job
	Sites              map[string]int
	Lead, Pair         string
	Said               *Refusal
	Notes              []string
}
