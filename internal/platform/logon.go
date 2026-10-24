package platform

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// runKey is the list of programs Windows starts for a login when it logs
// on, in the login's own part of the registry: reading and writing it asks
// no administrator and shows no prompt.
const runKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`

// LogOn is the command line Windows starts under that name when this login
// logs on, empty where there is none. Only Windows has the list.
func LogOn(name string) (string, error) {
	// #nosec G204 -- Windows' own tool, asked for one name of ours
	out, err := exec.Command("reg", "query", runKey, "/v", name).Output()
	if _, missing := err.(*exec.ExitError); missing {
		return "", nil
	}
	_, line, _ := strings.Cut(string(out), "REG_SZ")
	line, _, _ = strings.Cut(line, "\n")
	return strings.TrimSpace(line), err
}

// SetLogOn writes the command line Windows starts under that name at
// log-on, and with an empty line takes the name out of the list again.
func SetLogOn(name, line string) error {
	args := []string{"delete", runKey, "/v", name, "/f"}
	if line != "" {
		args = []string{"add", runKey, "/v", name, "/t", "REG_SZ", "/d", line, "/f"}
	} else if have, err := LogOn(name); have == "" {
		return err
	}
	// #nosec G204 -- Windows' own tool, with a name and a line of ours
	return exec.Command("reg", args...).Run()
}

// UserUnit is where the keeper's unit lies among the login's own services
// on Linux, in systemd/user of the settings folder beside ours. Their
// manager, systemctl --user, starts it with the machine and again when it
// fails, and asks no root.
func UserUnit(dirs contract.Dirs) string {
	return filepath.Join(filepath.Dir(dirs.Config), "systemd", "user", contract.UnitKeeper)
}

// Services runs systemctl --user and returns what it printed.
func Services(args ...string) (string, error) {
	// #nosec G204 -- the system's own tool, with words of ours
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Lingering is this login's name and whether its services run while it is
// not logged in, as loginctl says it: "yes" or "no". loginctl is asked by
// the login's number, which the system gives, and not by the name in the
// environment.
func Lingering() (name, linger string, err error) {
	if name = os.Getenv("USER"); name == "" {
		if me, err := user.Current(); err == nil {
			name = me.Username
		}
	}
	// #nosec G204 -- the system's own tool, asked of this login's own number
	out, err := exec.Command("loginctl", "show-user", strconv.Itoa(os.Getuid()), "--property=Linger", "--value").Output()
	return name, strings.TrimSpace(string(out)), err
}

// StartKeeper has the login's services start the keeper where it is one of
// them, and says whether they did.
func StartKeeper() bool {
	if _, err := Services("is-enabled", "-q", contract.UnitKeeper); err != nil {
		return false
	}
	_, err := Services("start", contract.UnitKeeper)
	return err == nil
}
