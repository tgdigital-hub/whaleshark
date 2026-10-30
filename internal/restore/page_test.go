package restore_test

import (
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/restore"
)

// The page's entry runs `dash start`, which with no terminal is the server
// itself, and comes back when it fails but not after `dash stop`. It has none
// of the keeper's settings for its panes, and hands on the same variables.
func TestThePagesEntry(t *testing.T) {
	const self = "/opt/me/bin/what$ever"
	env := func(k string) string { return map[string]string{"PATH": "/opt/me/bin"}[k] }

	name, text := restore.PageService("linux", self, env)
	if name != contract.UnitDash {
		t.Errorf("the unit's name: %q", name)
	}
	for _, want := range []string{`ExecStart="/opt/me/bin/what$$ever" dash start` + "\n", `Environment="PATH=/opt/me/bin"` + "\n",
		"Restart=on-failure\n", "[Install]\nWantedBy=default.target\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("the unit lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "KillMode") || !strings.Contains(text, restore.Program("linux", self)) {
		t.Errorf("the unit:\n%s", text)
	}

	name, text = restore.PageService("darwin", self, env)
	if name != "com.github.tgdigital-hub.whaleshark.page.plist" {
		t.Errorf("the entry's name: %q", name)
	}
	for _, want := range []string{"<string>dash</string>\n\t\t<string>start</string>", "<key>RunAtLoad</key>", "<key>SuccessfulExit</key>\n\t\t<false/>", "<key>PATH</key>"} {
		if !strings.Contains(text, want) {
			t.Errorf("the entry lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "ProcessType") || !strings.Contains(text, restore.Program("darwin", self)) {
		t.Errorf("the entry:\n%s", text)
	}

	for _, system := range []string{"windows", "plan9"} {
		if name, text := restore.PageService(system, self, env); name != "" || text != "" {
			t.Errorf("on %s: %q %q", system, name, text)
		}
	}
}
