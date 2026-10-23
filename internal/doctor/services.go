package doctor

import (
	"os"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/internal/restore"
)

// services is what only Linux asks: whether the keeper is one of the
// login's own services, which start it again when it fails, and whether
// those run while the login is logged out.
func (e *exam) services() {
	p := e.k.Platform
	if p.System() != "linux" {
		return
	}
	const set = "whaleshark init --at-login"
	start := func(text string) string {
		_, rest, _ := strings.Cut(text, "\nExecStart=")
		line, _, _ := strings.Cut(rest, "\n")
		return line
	}
	self, _ := p.SelfPath()
	_, want := restore.Service(p.System(), self, os.Getenv)
	have, _ := p.Peek(platform.UserUnit(e.dirs))
	enabled, _ := platform.Services("is-enabled", contract.UnitKeeper)
	active, _ := platform.Services("is-active", contract.UnitKeeper)
	service := enabled == "enabled"
	switch {
	case service && len(have) > 0 && start(string(have)) != start(want):
		e.say(problem, set, "keeper runs as a user service: yes, of another program file than this one: %s", start(string(have)))
	case service && active == "active":
		e.say(ok, "", "keeper runs as a user service: yes")
	case service:
		e.say(note, openCmd, "keeper runs as a user service: yes, and it is not running now (%s)", active)
	default:
		e.say(note, set, "keeper runs as a user service: no; nothing starts it again when it fails or with the machine")
	}
	switch name, linger, err := platform.Lingering(); {
	case err != nil:
		e.say(note, "", "keeper survives logout (linger): could not be told (loginctl: %v)", err)
	case linger != "yes":
		e.say(note, "loginctl enable-linger; where that is refused, ask the server's administrator to run, as root: loginctl enable-linger "+name,
			"keeper survives logout (linger): no; lingering is off for %s, and your services stop when you log out", name)
	case !service:
		e.say(note, set, "keeper survives logout (linger): no; lingering is on, and the keeper is not one of your services")
	default:
		e.say(ok, "", "keeper survives logout (linger): yes")
	}
}
