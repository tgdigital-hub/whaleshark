package dash

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
)

var realComesBack = comesBack

// No other test of this package reaches the machine's real services.
func init() {
	comesBack = func(*contract.Kit, contract.Dirs, bool) string { return "" }
}

// `dash phone on` makes the page one of the login's services, and says what
// lingering means for it; off takes it out. Stand-ins answer on Linux, and on
// a Mac the file in a home of the test's own is all there is.
func TestThePageComesBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-ins are shell scripts")
	}
	home, bin := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	log := filepath.Join(bin, "calls")
	for _, name := range []string{"systemctl", "loginctl"} {
		script := "#!/bin/sh\necho \"" + name + " $*\" >> " + log + "\necho no\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	calls := func() string {
		text, _ := os.ReadFile(log)
		os.Remove(log)
		return string(text)
	}

	k := &contract.Kit{Platform: platform.New("linux")}
	dirs, err := k.Platform.Dirs()
	if err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(home, "config", "systemd", "user", contract.UnitDash)
	said := realComesBack(k, dirs, true)
	if text, err := os.ReadFile(unit); err != nil || !strings.Contains(string(text), " dash start\n") {
		t.Errorf("the unit: %v\n%s", err, text)
	}
	if got := calls(); !strings.Contains(got, "systemctl --user daemon-reload\nsystemctl --user enable "+contract.UnitDash+"\n") {
		t.Errorf("the calls:\n%s", got)
	}
	if !strings.Contains(said, "comes back with your services") || !strings.Contains(said, "loginctl enable-linger") {
		t.Errorf("with lingering off it says: %s", said)
	}
	realComesBack(k, dirs, false)
	if _, err := os.Stat(unit); !os.IsNotExist(err) {
		t.Errorf("the unit after off: %v", err)
	}
	if got := calls(); !strings.Contains(got, "systemctl --user disable "+contract.UnitDash+"\nsystemctl --user daemon-reload\n") {
		t.Errorf("the calls at off:\n%s", got)
	}
	// Off with no entry asks the system nothing.
	realComesBack(k, dirs, false)
	if got := calls(); got != "" {
		t.Errorf("the calls at a second off:\n%s", got)
	}

	k = &contract.Kit{Platform: platform.New("darwin")}
	agent := filepath.Join(home, "Library", "LaunchAgents", "com.github.tgdigital-hub.whaleshark.page.plist")
	if said = realComesBack(k, dirs, true); !strings.Contains(said, "next log-in") {
		t.Errorf("on a Mac it says: %s", said)
	}
	if text, err := os.ReadFile(agent); err != nil || !strings.Contains(string(text), "<string>dash</string>") {
		t.Errorf("the entry: %v\n%s", err, text)
	}
	realComesBack(k, dirs, false)
	if _, err := os.Stat(agent); !os.IsNotExist(err) {
		t.Errorf("the entry after off: %v", err)
	}
	if got := calls(); got != "" {
		t.Errorf("on a Mac the system was asked:\n%s", got)
	}

	k = &contract.Kit{Platform: platform.New("windows")}
	if said = realComesBack(k, dirs, true); !strings.Contains(said, "whaleshark dash start") {
		t.Errorf("where there is no entry it says: %s", said)
	}
}
