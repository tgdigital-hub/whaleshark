package doctor

import (
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// releases is the public repository's releases, and releases+"latest" the
// one address this program ever asks by itself. GitHub answers it by
// pointing at the newest release's page, whose last word is the version;
// the pointer is read and never followed, so nothing is downloaded.
const releases = "https://github.com/tgdigital-hub/whaleshark/releases/"

// wire carries the request, by no proxy; a test puts its own in its place.
var wire http.RoundTripper = &http.Transport{}

// notice is Kit.Notice: the window's question, which a server's login and a
// person who switched it off never ask.
func notice(k *contract.Kit) {
	p := k.Platform
	dirs, err := p.Dirs()
	person, _ := contract.ReadPerson(p.Peek, dirs.Config)
	if s, _ := contract.ReadServer(p.Peek); err == nil && s == nil && person.Update.Notice {
		newest(p, dirs.State)
	}
}

// newest is the latest release the login has heard of, asked for when the
// last answer is older than a day. The request carries the program's name
// as its sender and nothing else: no version, no system, no id. An answer
// is kept only if it is a version number, and a request that fails is not
// made again until the next day.
func newest(p contract.Platform, state string) string {
	v := contract.ReadVersion(p.Peek, state)
	if contract.Now().Sub(v.At) < contract.NoticeEvery {
		return v.Seen
	}
	// The day is written first: where it cannot be kept, nothing is asked.
	file := filepath.Join(state, contract.VersionFile)
	if v.Version, v.At = contract.FileVersion, contract.Now(); contract.WriteVersioned(p, file, contract.FileVersion, &v) != nil {
		return v.Seen
	}
	req, _ := http.NewRequest(http.MethodGet, releases+"latest", nil)
	req.Header.Set("User-Agent", "whaleshark")
	client := http.Client{Transport: wire, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
		// Newer than nought is the contract's own test of a version number.
		if tag, ok := strings.CutPrefix(resp.Header.Get("Location"), releases+"tag/"); ok && contract.Newer(tag, "0") {
			v.Seen = tag
			contract.WriteVersioned(p, file, contract.FileVersion, &v)
		}
	}
	return v.Seen
}

// version says which program this is and, off a server, the newer release
// the window heard of: doctor itself asks nobody.
func (e *exam) version() {
	seen := contract.ReadVersion(e.k.Platform.Peek, e.dirs.State).Seen
	if s, _ := contract.ReadServer(e.k.Platform.Peek); s != nil {
		seen = ""
	}
	if line := contract.VersionLine(seen, contract.Version); line != "" {
		e.say(note, "", "this is whaleshark %s: %s", contract.Version, line)
	} else {
		e.say(ok, "", "this is whaleshark %s", contract.Version)
	}
}
