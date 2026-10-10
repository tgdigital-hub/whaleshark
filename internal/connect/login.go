package connect

import (
	"bufio"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// label names the login item among the person's own.
const label = "com.github.tgdigital-hub.whaleshark.connect"

// keychain is what macOS needs in the person's ssh settings to keep a key's
// passphrase in the keychain (both words are in the ssh_config manual macOS
// carries). At the end, under every host, a value set earlier still wins.
const keychain = "\n# whaleshark connect --at-login\nHost *\n\tAddKeysToAgent yes\n\tUseKeychain yes\n"

// places are the two files of the person's own that --at-login writes.
func places(c *contract.Call) (item, settings string, err error) {
	if c.Kit.Platform.System() != "darwin" {
		return "", "", refuse(contract.ExitEnv, "macos_only", "--at-login and --not-at-login are for macOS; on this system, start connect from your own login's start-up list.")
	}
	home, err := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), filepath.Join(home, ".ssh", "config"), err
}

// atLogin writes a login item of the person's own, which starts connect for
// the page when they log in, and offers the two keychain lines.
func atLogin(c *contract.Call, target string, port int) error {
	item, settings, err := places(c)
	if err != nil {
		return err
	}
	self, err := c.Kit.Platform.SelfPath()
	str := func(s string) string { return "<string>" + html.EscapeString(s) + "</string>" }
	text := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key>` + str(label) + `
<key>ProgramArguments</key><array>` + str(self) + str("connect") + str(target) + str("--port") + str(strconv.Itoa(port)) + `</array>
<key>RunAtLoad</key><true/>
</dict></plist>
`
	os.MkdirAll(filepath.Dir(item), 0o700)
	if err == nil {
		err = os.WriteFile(item, []byte(text), 0o600)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(c.Out, "From your next login on, connect to %s starts by itself and brings back the page.\nIt is this file: %s\nUndo: whaleshark connect %s --not-at-login\n", target, item, target)
	have, err := c.Kit.Platform.Read(settings)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if low := strings.ToLower(string(have)); c.JSON || strings.Contains(low, "addkeystoagent") && strings.Contains(low, "usekeychain") {
		return nil
	}
	fmt.Fprintf(c.Out, "\nA key with a passphrase is unlocked without typing only if your ssh settings say so:\n%s\nAdd these lines to %s? [y/N] ", keychain, settings)
	answer, _ := bufio.NewReader(c.Stdin).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		return nil
	}
	os.MkdirAll(filepath.Dir(settings), 0o700)
	// Written through the file's own name, so a link stays a link.
	return os.WriteFile(settings, append(have, keychain...), 0o600)
}

// notAtLogin removes the login item and the lines, exactly as written.
func notAtLogin(c *contract.Call) error {
	item, settings, err := places(c)
	if err != nil {
		return err
	}
	if err = os.Remove(item); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	have, err := c.Kit.Platform.Read(settings)
	if without := strings.Replace(string(have), keychain, "", 1); err == nil && without != string(have) {
		if err = os.WriteFile(settings, []byte(without), 0o600); err != nil {
			return err
		}
	}
	fmt.Fprintln(c.Out, "connect no longer starts at login; your ssh settings hold no line it added.")
	return nil
}
