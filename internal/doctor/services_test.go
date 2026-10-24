package doctor

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/internal/restore"
)

// On Linux two rows say whether the keeper is one of the login's services
// and whether it outlives a logout, each both ways, by what systemctl and
// loginctl of the test's own answer; a unit of another program file is a
// problem.
func TestTheKeepersServiceAndLingering(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-ins are shell scripts")
	}
	d := project(t)
	d.k.Platform = platform.New("linux")
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("USER", "me")
	d.write(filepath.Join(bin, "systemctl"), "#!/bin/sh\ncase $2 in is-enabled) cat "+bin+"/enabled;; is-active) cat "+bin+"/active;; esac\n")
	d.write(filepath.Join(bin, "loginctl"), "#!/bin/sh\n[ \"$*\" = 'show-user "+strconv.Itoa(os.Getuid())+" --property=Linger --value' ] && cat "+bin+"/linger\n")
	for _, name := range []string{"systemctl", "loginctl"} {
		if err := os.Chmod(filepath.Join(bin, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	answer := func(enabled, active, linger string) *exam {
		d.write(filepath.Join(bin, "enabled"), enabled+"\n")
		d.write(filepath.Join(bin, "active"), active+"\n")
		d.write(filepath.Join(bin, "linger"), linger+"\n")
		e, _ := d.doctor()
		return e
	}

	e := answer("disabled", "inactive", "no")
	d.want(e, note, "keeper runs as a user service: no", "whaleshark init --at-login")
	d.want(e, note, "keeper survives logout (linger): no; lingering is off for me", "as root: loginctl enable-linger me")

	e = answer("enabled", "active", "yes")
	d.want(e, ok, "keeper runs as a user service: yes")
	d.want(e, ok, "keeper survives logout (linger): yes")

	e = answer("enabled", "inactive", "no")
	d.want(e, note, "keeper runs as a user service: yes, and it is not running now (inactive)", openCmd)
	d.want(e, note, "keeper survives logout (linger): no", "loginctl enable-linger me")

	e = answer("disabled", "inactive", "yes")
	d.want(e, note, "keeper survives logout (linger): no; lingering is on, and the keeper is not one of your services", "whaleshark init --at-login")

	_, text := restore.Service("linux", "/elsewhere/whaleshark", os.Getenv)
	d.write(platform.UserUnit(d.dirs), text)
	e = answer("enabled", "active", "yes")
	d.want(e, problem, "keeper runs as a user service: yes, of another program file than this one", `"/elsewhere/whaleshark" engine run`)
	self, _ := os.Executable()
	_, text = restore.Service("linux", self, os.Getenv)
	d.write(platform.UserUnit(d.dirs), text)
	d.want(answer("enabled", "active", "yes"), ok, "keeper runs as a user service: yes")

	// Elsewhere neither row is said.
	d.k.Platform = platform.New("darwin")
	if e := answer("enabled", "active", "yes"); e.line("keeper runs as a user service") != nil || e.line("keeper survives logout") != nil {
		t.Errorf("a row of the service off Linux:\n%s", e.text())
	}
}
