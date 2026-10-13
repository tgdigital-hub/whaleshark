package dash

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// No request of a browser wires a program in: the page's `set` takes a key
// an agent could ask for, at the computer as from a phone, and never one
// that names a program, an address that reaches outside or a mark of our
// own panes. The page draws no form for one; this is the request made by hand.
func TestThePageWiresNoProgramIn(t *testing.T) {
	b := evening(t).signIn()
	set := func(key, value string) int {
		return b.ask("POST", contract.RouteDo+"set", url.Values{contract.FieldToken: {b.token()}, "key": {key}, "value": {value}}, nil).Code
	}
	dirs, _ := b.s.k.Platform.Dirs()
	for _, key := range []string{"shell", "shortcut.z", "notify.url", "tools.hermes.run", "tools.claude.rules", "tools.claude.env.V", "clipboard.programs", "seen", "draft.q9", "no.such.key"} {
		if code := set(key, "/bin/echo"); code != http.StatusConflict {
			t.Errorf("the page set %s: %d", key, code)
		}
		if now := contract.Current(b.s.k.Platform, dirs, key); now == "/bin/echo" {
			t.Errorf("%s is now %s", key, now)
		}
	}
	if code := set("theme", "kelp"); code != http.StatusSeeOther {
		t.Errorf("a plain setting from the page: %d", code)
	}
}
