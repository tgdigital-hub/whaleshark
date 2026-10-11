package platform

import (
	"os/exec"
	"strings"
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
