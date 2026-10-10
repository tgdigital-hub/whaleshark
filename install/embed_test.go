package install

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/install/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Both scripts are in the program, each one a plain sh can run, and each
// inside the size the design gives it.
func TestTheScriptsAreCompiledIn(t *testing.T) {
	for _, s := range []struct {
		name, text string
		most       int
	}{{"server.sh", Server, 1030}, {"install.sh", Installer, 150}} {
		if !strings.HasPrefix(s.text, "#!/bin/sh\n") {
			t.Errorf("%s starts with %.20q", s.name, s.text)
		}
		if n := strings.Count(s.text, "\n"); n > s.most {
			t.Errorf("%s has %d lines, more than %d", s.name, n, s.most)
		}
	}
}

// The names and places the server script writes are the contract's.
func TestTheServerScriptUsesTheContractsNames(t *testing.T) {
	for _, line := range []string{
		"ETC=$R" + filepath.ToSlash(filepath.Dir(contract.ServerFile)),
		"NODE=$R" + contract.ServerNode,
		"BROWSERS=$R" + contract.ServerBrowsers,
		"KEEPER=" + contract.UnitKeeper,
		"DASH=" + contract.UnitDash,
		fmt.Sprint("FIRST=", contract.ServerPorts.Base),
		fmt.Sprint("LAST=", contract.ServerPorts.Base+contract.ServerPorts.Count-1),
		"PLAYWRIGHT_BROWSERS_PATH=${BROWSERS",
	} {
		if !strings.Contains(Server, "\n"+line) && !strings.Contains(Server, " "+line) {
			t.Errorf("the server script lacks %q", line)
		}
	}
	if contract.BlockSize != 1000 || contract.EnvBrowsers != "PLAYWRIGHT_BROWSERS_PATH" {
		t.Error("the script's block of a thousand ports or the browser's variable is no longer the contract's")
	}
}

// machine is a pretended server: a root folder the script writes under, the
// fakes with what they know, and the script as a release carries it.
type machine struct {
	t                        *testing.T
	root, fake, self, script string
	env                      []string
}

func newMachine(t *testing.T) *machine {
	k := testkit.New(t, Server)
	return &machine{t: t, root: k.Root, fake: k.Fake, self: k.Self, script: testkit.Release(t, Server), env: k.Env}
}

func (m *machine) write(path, text string, mode os.FileMode) {
	m.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		m.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		m.t.Fatal(err)
	}
}

func (m *machine) read(path ...string) string {
	data, _ := os.ReadFile(filepath.Join(path...))
	return string(data)
}

// run gives the script to sh as the program does and returns what it said
// and how it ended. More of the environment comes first in args, as NAME=value.
func (m *machine) run(args ...string) (string, int) {
	m.t.Helper()
	env := m.env
	for len(args) > 0 && strings.Contains(args[0], "=") {
		env, args = append(env[:len(env):len(env)], args[0]), args[1:]
	}
	cmd := exec.Command("sh", append([]string{"-s", "--"}, args...)...)
	cmd.Stdin, cmd.Env = strings.NewReader(m.script), append(env[:len(env):len(env)], contract.EnvBin+"="+m.self)
	out, err := cmd.CombinedOutput()
	if _, exited := err.(*exec.ExitError); err != nil && !exited {
		m.t.Fatal(err)
	}
	return string(out), cmd.ProcessState.ExitCode()
}

// state is everything the script could have changed: each file under the
// root with its mode and text, and each call that would change the system.
func (m *machine) state() string { return m.files() + m.read(m.fake, "log") }

func (m *machine) files() string {
	var b strings.Builder
	filepath.WalkDir(m.root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			info, _ := d.Info()
			data, _ := os.ReadFile(path)
			fmt.Fprintf(&b, "%s %v %x\n", path, info.Mode(), sha256.Sum256(data))
		}
		return nil
	})
	return b.String()
}

// ok runs a command that must end well.
func (m *machine) ok(args ...string) string {
	m.t.Helper()
	out, code := m.run(args...)
	if code != 0 {
		m.t.Fatalf("server %v ended with %d:\n%s", args, code, out)
	}
	return out
}

// refused runs a command that must end with code and change nothing.
func (m *machine) refused(code int, args ...string) string {
	m.t.Helper()
	before := m.state()
	out, got := m.run(args...)
	if got != code {
		m.t.Errorf("server %v ended with %d, not %d:\n%s", args, got, code, out)
	}
	if after := m.state(); after != before {
		m.t.Errorf("server %v was refused and still changed something:\n%s\nbefore:\n%s\nafter:\n%s", args, out, before, after)
	}
	return out
}

// Every command of the script, run a second time, changes no file and
// asks the system for no change.
func TestASecondRunChangesNothing(t *testing.T) {
	m := newMachine(t)
	for _, args := range [][]string{
		{"setup", "boss", ""},
		{"SUDO_USER=boss", "harden", "boss"},
		{"add", "ana", "21000", "", "boss"},
		{"add", "ben", "22000", "", "boss"},
		{"phone", "ana", "8443", "21999", ""},
		{"tunnel", "team"},
		{"public", "ben", "22999", "ws2.example.com", "0a1b"},
		{"public", "ben", "22999", "", "", "off"},
		{"tunnel", "", "off"},
	} {
		m.ok(args...)
		before := m.state()
		m.ok(args...)
		if after := m.state(); after != before {
			t.Errorf("server %v changed something the second time:\nbefore:\n%s\nafter:\n%s", args, before, after)
		}
	}

	if got := m.read(m.root, "usr/local/bin/whaleshark"); got != m.read(m.self) {
		t.Errorf("the program in place is %q", got)
	}
	if got := m.read(m.root, "etc/sudoers.d/whaleshark-admin"); got != "boss ALL=(ALL) NOPASSWD:ALL\n" {
		t.Errorf("the administrator's sudo line is %q", got)
	}
	keeper := m.read(m.root, "etc/systemd/user", contract.UnitKeeper)
	if !strings.Contains(keeper, "ExecStart=/usr/local/bin/whaleshark engine run\n") || !strings.Contains(keeper, "KillMode=mixed\n") {
		t.Errorf("the keeper's service entry is\n%s", keeper)
	}
	if dash := m.read(m.root, "etc/systemd/user", contract.UnitDash); !strings.Contains(dash, "ExecStart=/usr/local/bin/whaleshark dash start\n") {
		t.Errorf("the page's service entry is\n%s", dash)
	}
	slice := m.read(m.root, "etc/systemd/system/user-1004.slice.d/50-whaleshark.conf")
	if !strings.Contains(slice, "SocketBindDeny=21000-31999\nSocketBindAllow=22000-22999\n") {
		t.Errorf("ben's share of the machine is\n%s", slice)
	}
	nft := m.read(m.root, "etc/whaleshark/ports.nft")
	for _, rule := range []string{"dport 21000-21999 meta skuid != { 0, 1003 } reject", "dport 22000-22999 meta skuid != { 0, 1004 } reject"} {
		if !strings.Contains(nft, rule) {
			t.Errorf("the rules for who may connect lack %q:\n%s", rule, nft)
		}
	}
	ssh := m.read(m.root, "etc/ssh/sshd_config.d/00-whaleshark.conf")
	if !strings.Contains(ssh, "PasswordAuthentication no\n") || !strings.Contains(ssh, "PermitOpen "+contract.Loopback+":* [::1]:*\n") {
		t.Errorf("the SSH settings are\n%s", ssh)
	}
	key := m.read(m.root, "people/first/.ssh/authorized_keys")
	for _, login := range []string{"boss", "ana", "ben"} {
		if got := m.read(m.root, "people", login, ".ssh/authorized_keys"); got != key {
			t.Errorf("%s is let in by %q", login, got)
		}
	}
	log := m.read(m.fake, "log")
	for _, call := range []string{
		"apt-get install -y -q claude-code",
		"playwright install --with-deps chromium",
		"apparmor_parser -r",
		"systemctl reload ssh",
		"ufw default deny incoming",
		"ufw allow 22/tcp",
		"loginctl enable-linger ana",
		"systemctl --user -M ana@ enable -q --now " + contract.UnitKeeper,
		"systemctl --user -M ben@ enable -q --now " + contract.UnitDash,
		"tailscale serve --bg --https=8443 http://" + contract.Loopback + ":21999",
	} {
		if !strings.Contains(log, call) {
			t.Errorf("the system was never asked for %q:\n%s", call, log)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(m.root, "root", ".whaleshark-server.*")); len(left) > 0 {
		t.Errorf("leftovers in root's folder: %v", left)
	}

	out := m.ok("status")
	if !strings.Contains(out, "ana  ports 21000-21999  keeper active  page active  7 processes") || strings.Contains(out, "restart") {
		t.Errorf("status says\n%s", out)
	}
	m.write(filepath.Join(m.root, "var/run/reboot-required"), "", 0o644)
	if out := m.ok("status"); !strings.Contains(out, "restart needed since 2") {
		t.Errorf("status with a restart wanted says\n%s", out)
	}

	m.ok("phone", "ana", "8443", "21999", "off")
	if !strings.HasSuffix(m.read(m.fake, "log"), "tailscale serve --https=8443 off\n") {
		t.Errorf("the phone's door was not closed:\n%s", m.read(m.fake, "log"))
	}
}

// A file that is not a public key stops setup and add before anything is
// changed, and so do a system or a caller the script is not for.
func TestWhatStopsSetupAndAdd(t *testing.T) {
	m := newMachine(t)
	notKey := filepath.Join(t.TempDir(), "not-a-key")
	m.write(notKey, "ssh-ed25519 this is no key\n", 0o600)
	m.refused(1, "setup", "admin", notKey)
	trap := filepath.Join(t.TempDir(), "trap")
	m.write(trap, `command="echo no" `+m.read(m.root, "people/first/.ssh/authorized_keys"), 0o600)
	m.refused(1, "setup", "admin", trap)
	m.refused(1, "setup", "admin", filepath.Join(t.TempDir(), "missing"))
	m.refused(1, "setup", "Not A Name")
	m.refused(1, "setup")

	m.write(filepath.Join(m.fake, "uid"), "1000\n", 0o644)
	m.refused(3, "setup", "admin")
	if out := m.ok("status"); out != "" {
		t.Errorf("status on a machine with nobody says %q", out)
	}
	m.write(filepath.Join(m.fake, "uid"), "0\n", 0o644)
	m.write(filepath.Join(m.root, "etc/os-release"), "ID=debian\nVERSION_ID=\"11\"\n", 0o644)
	m.refused(3, "setup", "admin")
	m.write(filepath.Join(m.root, "etc/os-release"), "ID=debian\nVERSION_ID=\"12\"\n", 0o644)

	m.ok("setup", "admin")
	if m.read(m.root, "etc/sudoers.d/whaleshark-admin") == "" || m.read(m.root, "etc/apparmor.d/whaleshark-browser") != "" {
		t.Error("on Debian 12 the administrator has no sudo line, or a rule for the browser was written")
	}
	m.refused(1, "add", "ana", "21000", notKey, "admin")
	m.refused(1, "add", "ana", "21500", "", "admin")
	m.refused(1, "add", "ana", "32000", "", "admin")
	m.refused(1, "add", "admin", "21000", "", "admin")
	m.refused(1, "add", "ana", "21000", "")
	m.refused(1, "tunnel")
	m.write(filepath.Join(m.fake, "passwd"), m.read(m.fake, "passwd")+"cy:1009:"+filepath.Join(m.root, "people/cy")+"\n", 0o644)
	m.write(filepath.Join(m.fake, "groups"), "sudo\n", 0o644)
	m.refused(1, "add", "cy", "21000", "", "admin")
}

// A fetched file that is not the one pinned is refused: Node's archive is
// not unpacked, and a key that is not Claude Code's maker's adds no source.
func TestATamperedDownloadIsRefused(t *testing.T) {
	m := newMachine(t)
	out, code := m.run("FAKE_SUM="+strings.Repeat("0", 64), "setup", "admin")
	if code != 1 || !strings.Contains(out, "failed while installing the shared browser") || strings.Contains(m.read(m.fake, "log"), "tar ") {
		t.Errorf("a changed archive of Node ended with %d:\n%s\n%s", code, out, m.read(m.fake, "log"))
	}
	if m.read(m.root, "opt/whaleshark/browsers/.pin") != "" {
		t.Error("the browser counts as installed")
	}

	m = newMachine(t)
	out, code = m.run("FAKE_FPR="+strings.Repeat("A", 40), "setup", "admin")
	if code != 1 || !strings.Contains(out, "not the one its maker publishes") || m.read(m.root, "etc/apt/sources.list.d/claude-code.list") != "" {
		t.Errorf("another key for Claude Code ended with %d:\n%s", code, out)
	}
}

// harden changes nothing from the console, for any login but the
// administrator, or for one that cannot use sudo without a password; and
// where another file of sshd's sets a value first it leaves SSH as it was.
func TestHardenOnlyThroughTheNewDoor(t *testing.T) {
	m := newMachine(t)
	m.ok("setup", "boss")

	m.refused(1, "SUDO_USER=first", "harden", "boss")
	m.refused(1, "SUDO_USER=root", "harden", "boss")
	m.refused(1, "SUDO_USER=", "harden", "boss")
	m.refused(1, "SUDO_USER=", "harden", "")
	m.write(filepath.Join(m.fake, "via"), "login\n", 0o644)
	m.refused(1, "SUDO_USER=boss", "harden", "boss")
	m.write(filepath.Join(m.fake, "via"), "sshd-session\n", 0o644)
	m.write(filepath.Join(m.fake, "nosudo"), "", 0o644)
	m.refused(1, "SUDO_USER=boss", "harden", "boss")

	if err := os.Rename(filepath.Join(m.fake, "nosudo"), filepath.Join(m.fake, "sudo-works")); err != nil {
		t.Fatal(err)
	}
	m.write(filepath.Join(m.fake, "sshd-first"), "passwordauthentication yes\n", 0o644)
	if out := m.refused(1, "SUDO_USER=boss", "harden", "boss"); !strings.Contains(out, "[passwordauthentication no]") {
		t.Errorf("with passwords switched on by another file, harden says\n%s", out)
	}

	m.write(filepath.Join(m.fake, "sshd-first"), "", 0o644)
	m.ok("SUDO_USER=boss", "harden", "boss")
}

// A program its package manager installed is left to it: setup copies
// nothing and upgrade asks the package manager for that one package.
// Otherwise upgrade puts the program it runs from in place and restarts
// each login's page, and never a keeper.
func TestUpgrade(t *testing.T) {
	m := newMachine(t)
	m.ok("setup", "admin")
	m.ok("add", "ana", "21000", "", "admin")
	m.self = filepath.Join(m.root, "usr/local/bin/whaleshark")
	if out := m.refused(1, "upgrade"); !strings.Contains(out, "install.sh") {
		t.Errorf("an upgrade to the program in place says\n%s", out)
	}
	m.self = filepath.Join(t.TempDir(), "whaleshark")
	m.write(m.self, "the program, version two\n", 0o755)
	out := m.ok("upgrade")
	log := m.read(m.fake, "log")
	if m.read(m.root, "usr/local/bin/whaleshark") != "the program, version two\n" || !strings.Contains(out, "ana  ports 21000-21999") ||
		!strings.HasSuffix(log, "systemctl --user -M ana@ restart "+contract.UnitDash+"\n") || strings.Contains(log, "restart "+contract.UnitKeeper) {
		t.Errorf("upgrade said\n%s\nand asked the system for\n%s", out, log)
	}

	m = newMachine(t)
	m.write(filepath.Join(m.fake, "packaged"), "", 0o644)
	m.ok("setup", "admin")
	if _, err := os.Stat(filepath.Join(m.root, "usr/local/bin/whaleshark")); err == nil {
		t.Error("setup copied a program that belongs to the package manager")
	}
	if keeper := m.read(m.root, "etc/systemd/user", contract.UnitKeeper); !strings.Contains(keeper, "ExecStart="+m.self+" engine run\n") {
		t.Errorf("the keeper's entry does not start the packaged program:\n%s", keeper)
	}
	m.ok("upgrade")
	if log := m.read(m.fake, "log"); !strings.Contains(log, "apt-get install -y -q --only-upgrade whaleshark\n") {
		t.Errorf("upgrade did not ask the package manager:\n%s", log)
	}
}

// The Cloudflare way from nothing and back again: the connector at the
// pinned version under a login of its own, a routing file that leads each
// name to its login's door and nowhere else, no certificate of the account
// left after any command, and a machine that is as it was once it is off.
func TestTheTunnelAndBack(t *testing.T) {
	m := newMachine(t)
	const cf, id, tag = "etc/whaleshark/cloudflared", "0f0f-aaaa-bbbb", "0a1b2c3d"
	m.ok("setup", "boss", "")
	m.ok("add", "ana", "21000", "", "boss")
	m.ok("add", "ben", "22000", "", "boss")
	before := m.files()
	clean := func(after string) {
		t.Helper()
		filepath.WalkDir(m.root, func(path string, d fs.DirEntry, _ error) error {
			if d != nil && d.Name() == "cert.pem" {
				t.Errorf("after %s the account's certificate is still on the machine: %s", after, path)
			}
			return nil
		})
	}
	logged := func(calls ...string) {
		t.Helper()
		for _, call := range calls {
			if log := m.read(m.fake, "log"); !strings.Contains(log, call) {
				t.Errorf("the system was never asked for %q:\n%s", call, log)
			}
		}
	}

	// A copy no release gave its pins, a key that is not the pinned one, and
	// a name before there is a tunnel: each changes nothing.
	m.script = Server
	if out := m.refused(1, "tunnel", "team"); !strings.Contains(out, "pins no version of Cloudflare's connector") {
		t.Errorf("a copy without pins says\n%s", out)
	}
	m.script = testkit.Release(t, Server)
	if out, code := m.run("FAKE_SUM="+strings.Repeat("0", 64), "tunnel", "team"); code != 1 || !strings.Contains(out, "not the one this version pins") || m.files() != before {
		t.Errorf("another key for Cloudflare's packages ended with %d and something changed:\n%s", code, out)
	}
	m.refused(1, "public", "ana", "21999", "ws1.example.com", tag)
	m.refused(1, "tunnel", "Not A Team")

	out := m.ok("tunnel", "team")
	clean("tunnel")
	if said := strings.Index(out, "a program that is not ours"); said < 0 || said > strings.Index(out, "the-link-to-open") || !strings.Contains(out, "audience tag") {
		t.Errorf("tunnel does not say first what it installs, show the link, and end with the clicks:\n%s", out)
	}
	logged("apt-get install -y -q --allow-change-held-packages cloudflared="+testkit.Connector, "apt-mark hold cloudflared",
		"useradd -r -M -d /nonexistent -s /usr/sbin/nologin whaleshark-tunnel", "systemctl enable -q --now whaleshark-tunnel.service")
	if info, err := os.Stat(filepath.Join(m.root, cf, "tunnel.json")); err != nil || info.Mode().Perm() != 0o400 {
		t.Errorf("the tunnel's credential: %v %v", info, err)
	}
	unit := m.read(m.root, "etc/systemd/system/whaleshark-tunnel.service")
	for _, line := range []string{"User=whaleshark-tunnel\n", " --no-autoupdate ", " --config /" + cf + "/config.yml run\n", "Restart=always\n"} {
		if !strings.Contains(unit, line) {
			t.Errorf("the connector's service entry lacks %q:\n%s", line, unit)
		}
	}
	head := "tunnel: " + id + "\ncredentials-file: /" + cf + "/tunnel.json\ningress:\n"
	rule := func(name string, door int, tag string) string {
		return fmt.Sprintf("  - hostname: %s\n    service: http://%s:%d\n    originRequest:\n      access:\n        required: true\n        teamName: team\n        audTag: [%s]\n",
			name, contract.Loopback, door, tag)
	}
	const last = "  - service: http_status:404\n"
	if got := m.read(m.root, cf, "config.yml"); got != head+last {
		t.Errorf("a tunnel with nobody routes\n%s", got)
	}

	m.ok("public", "ana", "21999", "ws1.example.com", tag)
	clean("a person's name")
	m.ok("public", "ben", "22999", "ws2.example.com", tag+"ff")
	if got := m.read(m.root, cf, "config.yml"); got != head+rule("ws1.example.com", 21999, tag)+rule("ws2.example.com", 22999, tag+"ff")+last {
		t.Errorf("the routing file for two people is\n%s", got)
	}
	logged("cloudflared route dns "+id+" ws1.example.com", "cloudflared route dns "+id+" ws2.example.com", "systemctl restart whaleshark-tunnel.service")
	// The connector's login is the sixth of the pretended machine.
	nft := m.read(m.root, "etc/whaleshark/ports.nft")
	door, block := strings.Index(nft, "dport 21999 meta skuid 1005 accept"), strings.Index(nft, "dport 21000-21999 meta skuid != { 0, 1003 } reject")
	if door < 0 || door > block || strings.Count(nft, "accept\n") != 2 {
		t.Errorf("the connector is not let through to the two doors alone, before each block's refusal:\n%s", nft)
	}
	if out := m.ok("status"); !strings.Contains(out, "the tunnel's connector") {
		t.Errorf("status with a tunnel says\n%s", out)
	}

	// A routing file Cloudflare's own check refuses never comes into place.
	m.write(filepath.Join(m.fake, "invalid"), "", 0o644)
	routes := m.read(m.root, cf, "config.yml")
	if out, code := m.run("public", "ana", "21999", "ws9.example.com", tag); code != 1 || !strings.Contains(out, "refuses the routing file") || m.read(m.root, cf, "config.yml") != routes {
		t.Errorf("a routing file the connector refuses ended with %d:\n%s", code, out)
	}
	clean("a refused routing file")
	if err := os.Rename(filepath.Join(m.fake, "invalid"), filepath.Join(m.fake, "valid-again")); err != nil {
		t.Fatal(err)
	}
	m.ok("public", "ana", "21999", "ws1.example.com", tag)
	for _, bad := range [][]string{{"ana", "21999", "ws1", tag}, {"ana", "21999", "ws1.example.com", "not a tag"}, {"ana", "door", "ws1.example.com", tag},
		{"ana", "21999", "ws1.example.com/x", tag}, {"Ana", "21999", "ws1.example.com", tag}} {
		m.refused(1, append([]string{"public"}, bad...)...)
	}

	m.ok("public", "ana", "21999", "", "", "off")
	if got := m.read(m.root, cf, "config.yml"); got != head+rule("ws2.example.com", 22999, tag+"ff")+last {
		t.Errorf("with ana's name taken off the routing file is\n%s", got)
	}
	if nft := m.read(m.root, "etc/whaleshark/ports.nft"); strings.Contains(nft, "dport 21999 meta") || !strings.Contains(nft, "dport 22999 meta skuid 1005 accept") {
		t.Errorf("with ana's name taken off the connector reaches\n%s", nft)
	}

	if out := m.ok("tunnel", "", "off"); !strings.Contains(out, "delete there yourself") {
		t.Errorf("tunnel off does not say what is left at Cloudflare:\n%s", out)
	}
	clean("tunnel off")
	logged("systemctl disable -q --now whaleshark-tunnel.service", "userdel whaleshark-tunnel", "apt-mark unhold cloudflared", "apt-get purge -y -q cloudflared")
	if after := m.files(); after != before {
		t.Errorf("tunnel off did not leave the machine as it was:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// A release that pins a newer connector moves it at the upgrade and starts
// it again; one that pins the same leaves it running.
func TestUpgradeMovesTheConnector(t *testing.T) {
	m := newMachine(t)
	m.ok("setup", "admin")
	m.ok("tunnel", "team")
	m.write(m.self, "the program, version two\n", 0o755)
	m.ok("upgrade")
	if log := m.read(m.fake, "log"); strings.Contains(log, "restart whaleshark-tunnel") || strings.Count(log, "cloudflared=") != 1 {
		t.Errorf("an upgrade with the same connector asked the system for\n%s", log)
	}
	m.write(m.self, "the program, version three\n", 0o755)
	m.script = strings.Replace(m.script, "'"+testkit.Connector+"'", "'2000.1.0'", 1)
	m.ok("upgrade")
	if log := m.read(m.fake, "log"); !strings.Contains(log, "cloudflared=2000.1.0\n") || !strings.Contains(log, "systemctl restart whaleshark-tunnel.service\n") {
		t.Errorf("an upgrade with a newer connector asked the system for\n%s", log)
	}
}

// release makes a folder with a release for this system, signed with a key
// of the test's own, and install.sh as that release would carry it.
func release(t *testing.T) (dir, script, home string) {
	if runtime.GOOS == "windows" {
		t.Skip("the scripts are for systems with sh")
	}
	dir, home = t.TempDir(), t.TempDir()
	keygen := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("ssh-keygen", args...).CombinedOutput(); err != nil {
			t.Skipf("ssh-keygen %v: %v: %s", args, err, out)
		}
	}
	file := "whaleshark-" + runtime.GOOS + "-" + runtime.GOARCH
	program := []byte("#!/bin/sh\necho the program\n")
	list := fmt.Sprintf("%x  %s\n%x  install.sh\n", sha256.Sum256(program), file, sha256.Sum256([]byte(Installer)))
	for name, data := range map[string][]byte{file: program, "SHA256SUMS": []byte(list)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	key := filepath.Join(home, "release-key")
	keygen("-q", "-t", "ed25519", "-N", "", "-C", "release", "-f", key)
	keygen("-Y", "sign", "-f", key, "-n", "file", filepath.Join(dir, "SHA256SUMS"))
	public, _ := os.ReadFile(key + ".pub")
	signer := `release namespaces="file" ` + strings.Join(strings.Fields(string(public))[:2], " ")
	if !strings.Contains(Installer, "\nSIGNER=''\n") {
		t.Fatal("install.sh has no empty line for the release's key")
	}
	script = filepath.Join(home, "install.sh")
	if err := os.WriteFile(script, []byte(strings.Replace(Installer, "\nSIGNER=''\n", "\nSIGNER='"+signer+"'\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, script, home
}

func install(home string, args ...string) (string, error) {
	cmd := exec.Command("sh", args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// install.sh puts a release's file in place after checking the list's
// signature and the file's checksum, and leaves nothing else behind.
func TestTheInstallerChecksAndInstalls(t *testing.T) {
	dir, script, home := release(t)
	out, err := install(home, script, "--from", dir)
	if err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	bin := filepath.Join(home, ".local", "bin")
	if got, _ := os.ReadFile(filepath.Join(bin, "whaleshark")); !bytes.Contains(got, []byte("echo the program")) {
		t.Errorf("the installed file is %q", got)
	}
	if info, err := os.Stat(filepath.Join(bin, "whaleshark")); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("the installed file: %v, %v", info, err)
	}
	if left, _ := os.ReadDir(bin); len(left) != 1 {
		t.Errorf("%d entries beside the program", len(left)-1)
	}
	if !strings.Contains(out, "SHA256:") || !strings.Contains(out, "ED25519") {
		t.Errorf("the key's fingerprint is not shown:\n%s", out)
	}
}

// A release with one byte changed, in the file or in the list, or signed
// with another key, installs nothing; nor does a copy that carries no key.
func TestTheInstallerRefusesATamperedRelease(t *testing.T) {
	nothing := func(what, home, out string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: installed:\n%s", what, out)
		}
		if left, _ := os.ReadDir(filepath.Join(home, ".local", "bin")); len(left) != 0 {
			t.Errorf("%s: %d entries were left behind", what, len(left))
		}
	}
	change := func(path string) {
		data, _ := os.ReadFile(path)
		data[len(data)/2] ^= 1
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir, script, home := release(t)
	change(filepath.Join(dir, "whaleshark-"+runtime.GOOS+"-"+runtime.GOARCH))
	out, err := install(home, script, "--from", dir)
	nothing("a changed program", home, out, err)
	if !strings.Contains(out, "not the file the release signed") {
		t.Errorf("a changed program is refused with\n%s", out)
	}

	dir, script, home = release(t)
	change(filepath.Join(dir, "SHA256SUMS"))
	out, err = install(home, script, "--from", dir)
	nothing("a changed list", home, out, err)

	dir, _, home = release(t)
	_, other, _ := release(t)
	out, err = install(home, other, "--from", dir)
	nothing("another key", home, out, err)

	dir, _, home = release(t)
	plain := filepath.Join(home, "plain.sh")
	if err := os.WriteFile(plain, []byte(Installer), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = install(home, plain, "--from", dir)
	nothing("no key", home, out, err)
	out, err = install(home, script)
	nothing("no folder and no version", home, out, err)
}
