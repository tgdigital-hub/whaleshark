// Package testkit is a pretended server for the tests of the server script
// and of the command that runs it: a root folder the script writes under,
// and a stand-in for every program of the system it calls.
package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

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

// Machine is the pretended server: Root is what the script takes for the
// system's top folder, Fake holds what the stand-ins know and the log of
// what they were asked to change, Self is the program's file, and Env is
// what a process needs to meet the stand-ins in the place of the system.
type Machine struct {
	Root, Fake, Self string
	Env              []string
}

// Pin returns a value the script writes in two halves, joined.
func Pin(t testing.TB, script, name string) string {
	m := regexp.MustCompile(`(?m)^` + name + `=(\w+)\n` + name + `=\$\{` + name + `\}(\w+)$`).FindStringSubmatch(script)
	if m == nil {
		t.Fatalf("the script pins no %s", name)
	}
	return m[1] + m[2]
}

// New makes a machine that is an Ubuntu with one login, "first", which a key
// of the test's own lets in, and root at the keyboard, come in over SSH.
func New(t testing.TB, script string) *Machine {
	if runtime.GOOS == "windows" {
		t.Skip("the scripts are for systems with sh")
	}
	dir := t.TempDir()
	m := &Machine{Root: filepath.Join(dir, "root"), Fake: filepath.Join(dir, "fake"), Self: filepath.Join(dir, "whaleshark")}
	bin := filepath.Join(dir, "bin")
	first := filepath.Join(m.Root, "people", "first")
	write := func(path, text string, mode os.FileMode) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(bin, "fake"), fakes, 0o755)
	for _, name := range strings.Fields(`id useradd getent groupadd dpkg apt-get systemctl ufw sshd ps sudo curl gpg
		sha256sum uname runuser loginctl tailscale pgrep chown visudo nft sysctl mount apparmor_parser tar npm playwright`) {
		if err := os.Symlink("fake", filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	write(m.Self, "the program, version one\n", 0o755)
	write(filepath.Join(m.Root, "etc", "os-release"), "ID=ubuntu\nVERSION_ID=\"24.04\"\n", 0o644)
	write(filepath.Join(m.Fake, "passwd"), "root:0:"+filepath.Join(m.Root, "root")+"\nfirst:1000:"+first+"\n", 0o644)
	write(filepath.Join(m.Fake, "uid"), "0\n", 0o644)
	write(filepath.Join(m.Fake, "via"), "sshd\n", 0o644)
	for _, empty := range []string{"sshd-first", "dpkg", "enabled", "started", "group", "log"} {
		write(filepath.Join(m.Fake, empty), "", 0o644)
	}
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "a test", "-f", filepath.Join(dir, "key")).CombinedOutput(); err != nil {
		t.Skipf("no ssh-keygen to make a key with: %v: %s", err, out)
	}
	key, _ := os.ReadFile(filepath.Join(dir, "key.pub"))
	write(filepath.Join(first, ".ssh", "authorized_keys"), string(key), 0o600)
	m.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + dir, "FAKE=" + m.Fake, "ROOT=" + m.Root,
		"WHALESHARK_TEST_ROOT=" + m.Root, "SUDO_USER=first",
		"FAKE_SUM=" + Pin(t, script, "NODE_X64"), "FAKE_FPR=" + Pin(t, script, "CLAUDE_KEY")}
	return m
}
