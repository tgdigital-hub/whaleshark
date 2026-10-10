package install

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Both scripts are in the program, each one a plain sh can run, and each
// inside the size the design gives it.
func TestTheScriptsAreCompiledIn(t *testing.T) {
	for _, s := range []struct {
		name, text string
		most       int
	}{{"server.sh", Server, 900}, {"install.sh", Installer, 150}} {
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

// fakes stands in for every program of the system the server script calls.
// One that would change the machine writes a line to the log instead, and
// the few the script asks questions of answer from files of the test.
const fakes = `#!/bin/sh
F=$FAKE
me=${0##*/}
log() { echo "$me $*" >>"$F/log"; }
for last; do :; done
case $me in
id)
	case ${1:-} in
	-u) if [ $# -eq 1 ]; then cat "$F/uid"; else awk -F: -v n="$2" '$1 == n { print $2; f = 1 } END { exit !f }' "$F/passwd"; fi ;;
	-nG) echo "$2 $(cat "$F/groups" 2>/dev/null)" ;;
	*) grep -q "^$1:" "$F/passwd" ;;
	esac ;;
useradd)
	mkdir -p "$ROOT/people/$last"
	echo "$last:$((1000 + $(wc -l <"$F/passwd"))):$ROOT/people/$last" >>"$F/passwd"
	log "$@" ;;
getent)
	case $1 in
	passwd) awk -F: -v n="$2" '$1 == n { print $1 ":x:" $2 ":" $2 "::" $3 ":/bin/sh"; f = 1 } END { exit !f }' "$F/passwd" ;;
	group) grep "^$2:" "$F/group" ;;
	esac ;;
groupadd) echo "$last:x:900:" >>"$F/group"; log "$@" ;;
dpkg)
	case $1 in
	-s) grep -qx "$2" "$F/dpkg" && echo "Status: install ok installed" ;;
	-S) [ -e "$F/packaged" ] && echo "whaleshark: $2" ;;
	esac ;;
apt-get)
	log "$@"
	[ "$1" = update ] || for a; do case $a in -* | install) ;; *) echo "$a" >>"$F/dpkg" ;; esac; done ;;
systemctl)
	case " $* " in
	*" is-enabled "*) grep -qx "$3 $last" "$F/enabled" ;;
	*" --user "*" enable "*) echo "$3 $last" >>"$F/enabled"; log "$@" ;;
	*" --user "*" is-active "*) echo active ;;
	*" is-active "*) grep -qx "$last" "$F/started" ;;
	*" start "*) echo "$last" >>"$F/started"; log "$@" ;;
	*" show "*) echo 7 ;;
	*) log "$@" ;;
	esac ;;
ufw)
	case "$*" in
	"status verbose") cat "$F/ufw" 2>/dev/null ;;
	"--force enable") printf 'Status: active\nDefault: deny (incoming), allow (outgoing)\n\n22/tcp     ALLOW IN    Anywhere\n' >"$F/ufw"; log "$@" ;;
	*) log "$@" ;;
	esac ;;
sshd)
	cat "$F/sshd-first"
	echo "port 22"
	conf=$ROOT/etc/ssh/sshd_config.d/00-whaleshark.conf
	[ ! -e "$conf" ] || awk 'FILENAME == ARGV[1] { seen[$1] = 1; next } { k = tolower($1) } !(k in seen) { $1 = k; print }' "$F/sshd-first" "$conf" ;;
ps) case "$*" in *comm=*) cat "$F/via" ;; *) echo 1 ;; esac ;;
sudo) [ ! -e "$F/nosudo" ] ;;
curl)
	log "$last"
	while [ $# -gt 1 ]; do [ "$1" != -o ] || echo fetched >"$2"; shift; done ;;
gpg) printf 'pub:-:\nfpr:::::::::%s:\n' "$FAKE_FPR" ;;
sha256sum) echo "$FAKE_SUM  $1" ;;
uname) echo x86_64 ;;
runuser) shift 3; exec "$@" ;;
loginctl) mkdir -p "$ROOT/var/lib/systemd/linger"; : >"$ROOT/var/lib/systemd/linger/$2"; log "$@" ;;
tailscale)
	case "$*" in
	"status --json") echo '  "CertDomains": [' ;;
	status) ;;
	"serve status") cat "$F/serve" 2>/dev/null ;;
	*off) : >"$F/serve"; log "$@" ;;
	*) echo "|-- / proxy $last" >"$F/serve"; log "$@" ;;
	esac ;;
pgrep) exit 1 ;;
visudo) ;;
*) log "$@" ;;
esac
`

// machine is a pretended server: a root folder the script writes under, and
// the fakes with what they know.
type machine struct {
	t                *testing.T
	root, fake, self string
	env              []string
}

// pin returns a value the script writes in two halves, joined.
func pin(t *testing.T, name string) string {
	m := regexp.MustCompile(`(?m)^` + name + `=(\w+)\n` + name + `=\$\{` + name + `\}(\w+)$`).FindStringSubmatch(Server)
	if m == nil {
		t.Fatalf("the script pins no %s", name)
	}
	return m[1] + m[2]
}

func newMachine(t *testing.T) *machine {
	if runtime.GOOS == "windows" {
		t.Skip("the scripts are for systems with sh")
	}
	dir := t.TempDir()
	m := &machine{t: t, root: filepath.Join(dir, "root"), fake: filepath.Join(dir, "fake"), self: filepath.Join(dir, "whaleshark")}
	bin := filepath.Join(dir, "bin")
	first := filepath.Join(m.root, "people", "first")
	m.write(filepath.Join(bin, "fake"), fakes, 0o755)
	for _, name := range strings.Fields(`id useradd getent groupadd dpkg apt-get systemctl ufw sshd ps sudo curl gpg
		sha256sum uname runuser loginctl tailscale pgrep chown visudo nft sysctl mount apparmor_parser tar npm playwright`) {
		if err := os.Symlink("fake", filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	m.write(m.self, "the program, version one\n", 0o755)
	m.write(filepath.Join(m.root, "etc", "os-release"), "ID=ubuntu\nVERSION_ID=\"24.04\"\n", 0o644)
	m.write(filepath.Join(m.fake, "passwd"), "root:0:"+filepath.Join(m.root, "root")+"\nfirst:1000:"+first+"\n", 0o644)
	m.write(filepath.Join(m.fake, "uid"), "0\n", 0o644)
	m.write(filepath.Join(m.fake, "via"), "sshd\n", 0o644)
	for _, empty := range []string{"sshd-first", "dpkg", "enabled", "started", "group", "log"} {
		m.write(filepath.Join(m.fake, empty), "", 0o644)
	}
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "a test", "-f", filepath.Join(dir, "key")).CombinedOutput(); err != nil {
		t.Skipf("no ssh-keygen to make a key with: %v: %s", err, out)
	}
	key, _ := os.ReadFile(filepath.Join(dir, "key.pub"))
	m.write(filepath.Join(first, ".ssh", "authorized_keys"), string(key), 0o600)
	m.env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + dir, "FAKE=" + m.fake, "ROOT=" + m.root,
		"WHALESHARK_TEST_ROOT=" + m.root, "SUDO_USER=first",
		"FAKE_SUM=" + pin(t, "NODE_X64"), "FAKE_FPR=" + pin(t, "CLAUDE_KEY")}
	return m
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
	cmd.Stdin, cmd.Env = strings.NewReader(Server), append(env[:len(env):len(env)], contract.EnvBin+"="+m.self)
	out, err := cmd.CombinedOutput()
	if _, exited := err.(*exec.ExitError); err != nil && !exited {
		m.t.Fatal(err)
	}
	return string(out), cmd.ProcessState.ExitCode()
}

// state is everything the script could have changed: each file under the
// root with its mode and text, and each call that would change the system.
func (m *machine) state() string {
	var b strings.Builder
	filepath.WalkDir(m.root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			info, _ := d.Info()
			data, _ := os.ReadFile(path)
			fmt.Fprintf(&b, "%s %v %x\n", path, info.Mode(), sha256.Sum256(data))
		}
		return nil
	})
	return b.String() + m.read(m.fake, "log")
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
