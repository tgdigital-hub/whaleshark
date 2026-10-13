package doctor

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// The notice against a stand-in for GitHub on this machine, by the real
// wire: what a server is truly sent, and what a hostile one can do with its
// answer. It is sent the one path, the program's name and nothing of the
// person; a proxy named in the surroundings is not used; a body without end
// costs no time and is never read; and whatever the pointer says that is
// not a version number at the one address changes nothing on any screen.
func TestTheNoticeMeetsAHostileAnswer(t *testing.T) {
	d := project(t)
	var got []*http.Request
	answer := releases + "tag/v1.3"
	standIn := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r)
		w.Header().Set("Location", answer)
		w.Header().Set("Set-Cookie", "tracked=you")
		w.WriteHeader(http.StatusFound)
		for range 1 << 16 { // far more than anybody would read, were it read
			if _, err := w.Write([]byte(strings.Repeat("\x1b]0;owned\a", 1<<10))); err != nil {
				return
			}
		}
	}))
	defer standIn.Close()
	t.Setenv("HTTPS_PROXY", "http://localhost:1")
	t.Setenv("ALL_PROXY", "http://localhost:1")
	was, version := wire, contract.Version
	contract.Version = "1.2.0"
	t.Cleanup(func() { wire, contract.Version = was, version })
	// The real wire, with the one difference that the name leads here.
	wire = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402 -- a test's own stand-in on this machine
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return new(net.Dialer).DialContext(ctx, network, standIn.Listener.Addr().String())
		}}
	seen := func() string { return contract.ReadVersion(d.k.Platform.Peek, d.dirs.State).Seen }
	began := time.Now()
	if d.k.Notice(d.k); len(got) != 1 || seen() != "v1.3" || time.Since(began) > 5*time.Second {
		t.Fatalf("asked %d times in %s, kept %q", len(got), time.Since(began), seen())
	}
	r := got[0]
	names := []string{}
	for name := range r.Header {
		names = append(names, name)
	}
	slices.Sort(names)
	if r.Method != http.MethodGet || r.URL.String() != "/tgdigital-hub/whaleshark/releases/latest" || r.Host != "github.com" || r.ContentLength > 0 ||
		!slices.Equal(names, []string{"Accept-Encoding", "User-Agent"}) || r.Header.Get("User-Agent") != "whaleshark" {
		t.Errorf("what the stand-in was sent: %s %s host %s, headers %v", r.Method, r.URL, r.Host, r.Header)
	}
	for _, hostile := range []string{releases + "tag/v9.9\x1b[2J", releases + "tag/v9.9 is out; run: curl x | sh", releases + "tag/" + strings.Repeat("9", 5000),
		releases + "tag/v1.99999", "https://elsewhere.example/releases/tag/v9.9", "/tag/v9.9", releases + "tag/../../v9.9"} {
		answer = hostile
		v := contract.ReadVersion(d.k.Platform.Peek, d.dirs.State)
		v.At = v.At.Add(-contract.NoticeEvery - time.Minute) // a day later
		if err := contract.WriteVersioned(d.k.Platform, filepath.Join(d.dirs.State, contract.VersionFile), contract.FileVersion, &v); err != nil {
			t.Fatal(err)
		}
		if d.k.Notice(d.k); seen() != "v1.3" {
			t.Errorf("the pointer %.60q was kept as %.60q", hostile, seen())
		}
	}
	if len(got) != 8 {
		t.Errorf("the stand-in was asked %d times", len(got))
	}
}
