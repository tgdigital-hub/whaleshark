package restore

import (
	"html"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// label names the keeper's entry among the login's services.
const label = "com.github.tgdigital-hub.whaleshark.engine"

// pageLabel names the page's entry there.
const pageLabel = "com.github.tgdigital-hub.whaleshark.page"

// carried are the variables of the person's own shell the entry hands to the
// keeper, when they are set where the entry is made. A service is started
// with next to nothing: without PATH a resumed agent's program is not
// found, without SHELL a pane has another shell than the person's, without
// the language a program believes the terminal cannot show its characters,
// and the three folders are where the keeper's own files are looked for.
var carried = []string{"PATH", "SHELL", "LANG", "LC_ALL", "LC_CTYPE", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"}

// Service is the login's own service entry for the keeper: the name of its
// file and the file's text. self is the program's full path. The entry
// starts the keeper when the login's services start and again whenever it
// dies; a keeper that was stopped by `engine stop` ended well and stays
// stopped. On "darwin" the file goes into Library/LaunchAgents of the home
// folder, on "linux" into systemd/user of the settings folder, where the
// login's services also need leave to run with nobody logged in. On
// "windows" the entry is no file: it is a name and a command line in the
// login's own list of what Windows starts at log-on (the registry's Run
// key), the one form there that asks no administrator, opens no window of
// a second program and sets no limit of its own on how long the keeper
// runs; nothing starts a keeper again that died, until the next log-on or
// the next `whaleshark open`. Any other system gets nothing.
func Service(system, self string, getenv func(string) string) (name, text string) {
	if system == "windows" {
		return label, `"` + self + `" engine run`
	}
	return entry(system, self, "engine", "run", getenv)
}

// PageService is the same for the page's server of a login with no server of
// `whaleshark server`, so that a phone's door comes back with the machine:
// on "linux" a unit beside the keeper's, on "darwin" an entry beside where
// the keeper's would lie, which starts at the login's next log-in. With no
// terminal `dash start` is the server itself; `dash stop` ends it well, and
// it stays stopped. Windows and any other system get nothing.
func PageService(system, self string, getenv func(string) string) (name, text string) {
	return entry(system, self, "dash", "start", getenv)
}

// Program is the part of an entry's text that names the program file, by
// which a doctor tells an entry of another program file.
func Program(system, self string) string {
	if system == "darwin" {
		return plistString(self)
	}
	return `ExecStart="` + unitProgram(self) + `"`
}

func plistString(s string) string { return "<string>" + html.EscapeString(s) + "</string>" }

// unitQuote is a value in double quotes, with the two signs that mean
// something inside them and the sign that starts a specifier.
var unitQuote = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "%", "%%")

// unitProgram is the program's path for a command line, where a dollar would
// also be read as a variable.
func unitProgram(self string) string {
	return strings.ReplaceAll(unitQuote.Replace(self), "$", "$$")
}

// entry is the text of an entry that runs this program with two words; the
// keeper's alone has the settings for its panes.
func entry(system, self, word, next string, getenv func(string) string) (name, text string) {
	keeper := word == "engine"
	var b strings.Builder
	switch system {
	case "darwin":
		str := func(s string) string { return plistString(s) + "\n" }
		id := pageLabel
		if keeper {
			id = label
		}
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	` + str(id) + `	<key>ProgramArguments</key>
	<array>
		` + str(self) + `		` + str(word) + `		` + str(next) + `	</array>
	<key>EnvironmentVariables</key>
	<dict>
`)
		for _, v := range carried {
			if getenv(v) != "" {
				b.WriteString("\t\t<key>" + v + "</key>\n\t\t" + str(getenv(v)))
			}
		}
		b.WriteString(`	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
`)
		if keeper {
			// Interactive: the system holds back the processor and the disk of
			// any other kind of entry, and with it every pane.
			b.WriteString("\t<key>ProcessType</key>\n\t<string>Interactive</string>\n")
		}
		b.WriteString("</dict>\n</plist>\n")
		return id + ".plist", b.String()
	case "linux":
		about, unit := "page: what a phone reaches", contract.UnitDash
		if keeper {
			about, unit = "keeper: every pane's terminal", contract.UnitKeeper // the one name a server's set-up and doctor know it by
		}
		b.WriteString("[Unit]\nDescription=WhaleShark " + about + " of this login\n\n[Service]\n")
		b.WriteString(Program(system, self) + " " + word + " " + next + "\n")
		for _, v := range carried {
			if getenv(v) != "" {
				b.WriteString(`Environment="` + v + "=" + unitQuote.Replace(getenv(v)) + `"` + "\n")
			}
		}
		b.WriteString("Restart=on-failure\n")
		if keeper {
			// mixed: a stop asks the keeper alone, which ends its panes in its
			// own order; what is left after that is ended by the system.
			b.WriteString("KillMode=mixed\n")
		}
		b.WriteString("\n[Install]\nWantedBy=default.target\n")
		return unit, b.String()
	}
	return "", ""
}
