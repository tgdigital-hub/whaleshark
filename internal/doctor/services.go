package doctor

import (
	"os"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/internal/restore"
)

// pageBack says, where a phone's port is picked and no server is, whether the
// page comes back after a restart of the machine: an entry of this program
// file, switched on, and on Linux lingering, without which a login's services
// wait for the login.
func (e *exam) pageBack() {
	p := e.k.Platform
	cfg, _ := contract.ReadPerson(p.Peek, e.dirs.Config)
	if srv, err := contract.ReadServer(p.Peek); err != nil || srv != nil || cfg.Phone.Port == 0 {
		return
	}
	const row, on = "page comes back after a restart: ", "whaleshark dash phone on"
	system := p.System()
	path := platform.PageEntry(system, e.dirs)
	self, _ := p.SelfPath()
	have, _ := p.Peek(path)
	phones, _ := contract.ReadPhones(p.Peek, e.dirs.State)
	enabled := system != "linux"
	if !enabled {
		state, _ := platform.Services("is-enabled", contract.UnitDash)
		enabled = state == "enabled"
	}
	switch {
	case !phones.On:
		e.say(note, on, row+"no; phone access is off")
	case path == "":
		e.say(note, "whaleshark dash start", row+"no; nothing starts it on this system")
	case len(have) == 0 || !enabled:
		e.say(note, on, row+"no; it is not one of your services")
	case !strings.Contains(string(have), restore.Program(system, self)):
		e.say(problem, on, row+"no; its entry names another program file than this one: %s", path)
	case system != "linux":
		e.say(ok, "", row+"yes, at your next log-in")
	default:
		switch name, linger, err := platform.Lingering(); {
		case err != nil:
			e.say(note, "", row+"could not be told (loginctl: %v)", err)
		case linger != "yes":
			e.say(note, "loginctl enable-linger; where that is refused, ask the server's administrator to run, as root: loginctl enable-linger "+name,
				row+"not until you log in; lingering is off for %s", name)
		default:
			e.say(ok, "", row+"yes, as a user service")
		}
	}
}

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
