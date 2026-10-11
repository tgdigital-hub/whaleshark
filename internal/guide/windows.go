package guide

import (
	"os"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// browsers are the usual folders of Chrome on Windows, and of Edge for a PC
// with no Chrome: the variable that names a folder, and the file under it.
// Neither browser is on anybody's PATH there.
var browsers = [][2]string{
	{"ProgramFiles", `Google\Chrome\Application\chrome.exe`},
	{"ProgramFiles(x86)", `Google\Chrome\Application\chrome.exe`},
	{"LOCALAPPDATA", `Google\Chrome\Application\chrome.exe`},
	{"ProgramFiles(x86)", `Microsoft\Edge\Application\msedge.exe`},
	{"ProgramFiles", `Microsoft\Edge\Application\msedge.exe`},
}

// OnWindows writes the section's one command as Windows needs it. Nobody
// knows which shell a worker's tool types into, and no two of them spell a
// variable alike, so the line names no variable: the browser's file, the
// profile folder, the evidence folder and the port are written out, from
// the tab's own variables, by quote, which spells a line for every shell.
// PowerShell does not wait for a program with a window of its own, and the
// picture must be there when the line ends: so what the browser prints is
// handed to sort, a program or a word of every shell, which makes it wait;
// a line only PowerShell can read ends in its own word for that. cmd does
// not wait either, and reads no path with forward slashes before a pipe:
// it gets a line of its own under the first. With neither browser found
// the section offers no command.
func OnWindows(section string, quote func(shell string, argv []string) string, tab []string, getenv func(string) string) string {
	value := func(name string) string {
		for _, v := range tab {
			if rest, ok := strings.CutPrefix(v, name+"="); ok {
				return rest
			}
		}
		return ""
	}
	browser := ""
	for _, b := range browsers {
		file := getenv(b[0]) + `\` + b[1]
		if _, err := os.Stat(file); browser == "" && getenv(b[0]) != "" && err == nil {
			browser = file
		}
	}
	argv := []string{browser, "--headless",
		`--user-data-dir=` + getenv("TEMP") + `\whaleshark-browser-` + value(contract.EnvRun) + "-" + value(contract.EnvAttempt),
		`--screenshot=` + value(contract.EnvEvidence) + `\page.png`, "--window-size=1280,900", "http://" + contract.Loopback + ":" + value("PORT") + "/"}
	line := quote("", argv)
	if strings.HasPrefix(line, "&") {
		line += " | Out-Null"
	} else {
		line += " | sort"
	}
	var lines []string
	for _, l := range strings.Split(section, "\n") {
		switch {
		case strings.Contains(l, "--headless"):
			l = "  " + line + "\n  In cmd, this one in its place:\n  start \"\" /wait " + quote(contract.ShellCmd, argv)
			fallthrough
		case strings.Contains(l, "no browser tool"):
			if browser == "" {
				continue
			}
		}
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}
