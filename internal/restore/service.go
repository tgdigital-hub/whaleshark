package restore

import (
	"html"
	"strings"
)

// label names the keeper's entry among the login's services.
const label = "com.github.tgdigital-hub.whaleshark.engine"

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
// login's services also need leave to run with nobody logged in. Any other
// system has no entry of ours and gets nothing.
func Service(system, self string, getenv func(string) string) (name, text string) {
	var b strings.Builder
	switch system {
	case "darwin":
		str := func(s string) string { return "<string>" + html.EscapeString(s) + "</string>\n" }
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	` + str(label) + `	<key>ProgramArguments</key>
	<array>
		` + str(self) + `		<string>engine</string>
		<string>run</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
`)
		for _, v := range carried {
			if getenv(v) != "" {
				b.WriteString("\t\t<key>" + v + "</key>\n\t\t" + str(getenv(v)))
			}
		}
		// Interactive: the system holds back the processor and the disk of
		// any other kind of entry, and with it every pane.
		b.WriteString(`	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ProcessType</key>
	<string>Interactive</string>
</dict>
</plist>
`)
		return label + ".plist", b.String()
	case "linux":
		// A value in double quotes, with the two signs that mean something
		// inside them and the sign that starts a specifier.
		quote := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "%", "%%")
		b.WriteString("[Unit]\nDescription=WhaleShark keeper: every pane's terminal of this login\n\n[Service]\n")
		// A dollar in the command line would be read as a variable.
		b.WriteString(`ExecStart="` + strings.ReplaceAll(quote.Replace(self), "$", "$$") + `" engine run` + "\n")
		for _, v := range carried {
			if getenv(v) != "" {
				b.WriteString(`Environment="` + v + "=" + quote.Replace(getenv(v)) + `"` + "\n")
			}
		}
		// mixed: a stop asks the keeper alone, which ends its panes in its
		// own order; what is left after that is ended by the system.
		b.WriteString("Restart=on-failure\nKillMode=mixed\n\n[Install]\nWantedBy=default.target\n")
		return "whaleshark-engine.service", b.String()
	}
	return "", ""
}
