package doctor

import (
	"os"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/internal/restore"
)

// windows is what only Windows asks: whether the engine starts at log-on,
// whether git takes a path longer than 260 characters, and whether the
// program's own path can be typed into every shell an agent's tool may run.
// Nothing here is mended by doctor: git's settings are the person's own.
func (e *exam) windows() {
	p := e.k.Platform
	if p.System() != "windows" {
		return
	}
	const set = "whaleshark init --at-login"
	self, _ := p.SelfPath()
	name, want := restore.Service(p.System(), self, os.Getenv)
	switch have, err := platform.LogOn(name); {
	case err != nil:
		e.say(note, "", "whether the engine starts when you log on could not be read: %v", err)
	case have == want:
		e.say(ok, "", "the engine starts when you log on")
	case have != "":
		e.say(problem, set, "the engine's entry at log-on starts another program file than this one: %s", have)
	default:
		e.say(note, set, "the engine does not start when you log on: after a restart of the PC the first `whaleshark open` brings the tabs back")
	}
	if long, _ := e.git("config", "--get", "core.longpaths"); long == "true" {
		e.say(ok, "", "git takes a path longer than 260 characters (core.longpaths)")
	} else {
		e.say(note, "git config --global core.longpaths true", "git refuses a path longer than 260 characters until core.longpaths is set: a task in a project with such a file gets no copy of the code")
	}
	if strings.HasPrefix(p.Quote("", []string{self}), "&") {
		e.say(note, "", "%s has a blank or a sign in its path, and the disk keeps no short name for it: only PowerShell can run the lines a worker is given. Put the program in a folder with a plain name", self)
	}
}
