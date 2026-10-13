package contract

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// ReportLine is where a person reports a problem: the last lines of help
// and of doctor, and the README. The program sends nothing and opens no
// browser: it prints this.
const ReportLine = `Problems and questions: the repository's Issues page (github.com/tgdigital-hub/whaleshark/issues). A security flaw, privately: the repository's Security tab, "Report a vulnerability".`

// VersionSeen is version.json in the login's state folder: the latest
// release Kit.Notice was told of, and when it asked. Nothing else of the
// answer is kept, and nothing that is no version number is kept at all.
type VersionSeen struct {
	Versioned
	Seen string    `json:"seen"`
	At   time.Time `json:"at"`
}

const (
	VersionFile = "version.json"
	NoticeEvery = 24 * time.Hour
)

var versionForm = regexp.MustCompile(`^v?\d{1,4}(\.\d{1,4}){0,3}$`)

// ReadVersion is the login's version.json, empty where there is none.
func ReadVersion(read func(path string) ([]byte, error), stateDir string) (v VersionSeen) {
	ReadVersioned(read, filepath.Join(stateDir, VersionFile), FileVersion, &v)
	return v
}

// Newer reports whether seen is a later release than the program's own
// version; an answer or a build that is no plain version number is neither.
func Newer(seen, mine string) bool {
	if !versionForm.MatchString(seen) || !versionForm.MatchString(mine) {
		return false
	}
	a, b := strings.Split(strings.TrimPrefix(seen, "v"), "."), strings.Split(strings.TrimPrefix(mine, "v"), ".")
	for i := range max(len(a), len(b)) {
		var x, y int
		if i < len(a) {
			x, _ = strconv.Atoi(a[i])
		}
		if i < len(b) {
			y, _ = strconv.Atoi(b[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// VersionLine is the one dim line of the left section, of version and of
// doctor; empty while the program is the newest it has heard of. It names
// no command to upgrade with: the program is one file whichever way it was
// installed, and where that file lies does not say which way it was.
func VersionLine(seen, mine string) string {
	if !Newer(seen, mine) {
		return ""
	}
	return "version " + strings.TrimPrefix(seen, "v") + " is out · upgrade it the way you installed it"
}

// The interface an app speaks: the phone's door of the page's server, under
// a prefix that carries a number. Everything under APIv1 keeps its shape
// for as long as 1 is served, held by testdata/api1.json; a change that
// breaks an app is a second prefix beside it. RouteAPI itself answers an
// AppRoot. Every route is behind the road's pass and a paired session, as
// the page's are; RouteAPI and APIPair alone answer an unpaired caller.
//
// An app has no cookie: it sends the secret it made at pairing in
// HeaderApp on every request, checked against Phone.Session as a cookie
// is, and the origin check of a browser's buttons is not asked of it. What
// an app may do is the rows of Actions a phone has and no more: it never
// types keys into a tab, runs a command, sets a key of SetPerson, approves
// trust, pairs another phone or reads a file of a project.
const (
	RouteAPI    = "/api"
	APIv1       = "/api/1"
	APIView     = "/view"     // GET: every open job of the login, a View each
	APITask     = "/task/"    // GET <run>/<id>: a TaskView
	APIChat     = "/chat/"    // GET <run>/<lead or task>: an AppChat
	APILive     = "/live"     // GET, held open: one line each time the view changes
	APIDo       = "/do/"      // POST <action>: one row of Actions, its fields as the page's form
	APITell     = "/tell"     // POST an AppTell: a message to the lead agent, never typed into its tab
	APISettings = "/settings" // GET: an AppSetting for every key an app may show
	APISet      = "/set"      // POST an AppSet: a key of SetFree or SetAsks, as the person
	APIPair     = "/pair"     // POST an AppPair: answered with the number both screens show
	APIPush     = "/push"     // POST: answered with what it waits for, the relay of the apps; nothing is kept

	HeaderApp = "Whaleshark-Session"
	ChatLines = 400
	TellMax   = 1 << 20
)

// APIVersions is every version this program serves, the newest last.
var APIVersions = []int{1}

type AppRoot struct {
	Versions []int  `json:"versions"`
	Newest   int    `json:"newest"`
	Program  string `json:"program"`
	Paired   bool   `json:"paired"`
}

// AppSetting is one setting with its value now; a tool's variable is Dots.
type AppSetting struct {
	Key
	Value string `json:"value"`
	Agent string `json:"agent"` // SetFree, SetAsks or SetPerson
}

type AppChat struct {
	Run   string   `json:"run"`
	Who   string   `json:"who"`
	Lines []string `json:"lines"`
}

type AppTell struct {
	Text string `json:"text"`
}

type AppSet struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// AppPair is what an app presents: the one-time word it was shown or
// scanned, the hash (TokenHash) of a secret it made itself, and its name.
type AppPair struct {
	Word    string `json:"word"`
	Session string `json:"session"`
	Name    string `json:"name"`
}

// PairCode is the pairing code as phones.json keeps it: the hash of its
// one-time word and until when it is good. `dash phone on` and the settings
// screen show PairText, of which a QR code is a picture. The word is good
// for PairCodeLife and for one use, and even then pairs nothing: the app
// becomes Phones.Asking with its number, which the person approves at the
// computer as for any phone. It holds no secret that outlives it.
type PairCode struct {
	Hash  string    `json:"hash"`
	Until time.Time `json:"until"`
}

const PairCodeLife = time.Minute

// PairText is the code as it is shown: the door's public name and the word.
func PairText(name, word string) string { return "whaleshark://pair?at=" + name + "&word=" + word }

// Good reports whether word is this code's and its minute is not over.
func (c *PairCode) Good(word string, now time.Time) bool {
	return c != nil && word != "" && now.Before(c.Until) && TokenHash(word) == c.Hash
}

// Profile is a person's settings as one text file to carry to another
// computer: config.toml, the engine's settings.toml and each tool's
// description. It holds no secret by construction: no nudge address, no
// value of a tool's variable (the names stay, empty), nothing of a phone,
// of trust, of a project, of a server or of ui.json.
type Profile struct {
	Version int             `toml:"version"`
	Config  PersonConfig    `toml:"config"`
	Engine  Settings        `toml:"engine"`
	Tools   map[string]Tool `toml:"tools,omitempty"`
}

// SaveProfile gathers the login's profile, and names what it left out for
// the person to set by hand: "the nudge address", "2 variables of hermes".
func SaveProfile(p Platform, d Dirs) (pr Profile, left []string, err error) {
	if pr.Config, err = ReadPerson(p.Peek, d.Config); err == nil {
		pr.Engine, err = ReadSettings(p.Peek, d)
	}
	if pr.Version = FileVersion; pr.Config.Notify.URL != "" {
		pr.Config.Notify.URL, left = "", append(left, "the nudge address")
	}
	tools, _ := Tools(p, d)
	for _, t := range tools {
		if !t.Mine {
			continue
		}
		names := map[string]string{}
		for name := range t.Env {
			names[name] = ""
		}
		if t.Env, t.Tried = names, (ToolTried{}); len(names) > 0 {
			left = append(left, strconv.Itoa(len(names))+" variables of "+t.Name)
		}
		if pr.Tools == nil {
			pr.Tools = map[string]Tool{}
		}
		pr.Tools[t.Name] = t
	}
	return pr, left, err
}

// ReadProfile reads a profile as data: nothing of it is run. skipped names
// what the file holds that this program does not know. Loading one is the
// caller's: every key whose value here is not Current's is shown as "what:
// from → to", with the warning for a guard switched off or a limit lifted,
// and then set with Set as the person, a key of AsksRuns asked about by
// itself. So every value passes its key's own check, a tool's program is
// looked up afresh and refused inside a project, and what runs a program is
// flagged on every screen. No variable of a tool is set: a profile has none.
func ReadProfile(data []byte) (pr Profile, skipped []string, err error) {
	meta, err := toml.Decode(string(data), &pr)
	for _, key := range meta.Undecoded() {
		skipped = append(skipped, key.String())
	}
	if err == nil && pr.Version > FileVersion {
		err = ErrNewer
	}
	pr.Config.Notify.URL = ""
	return pr, skipped, err
}
