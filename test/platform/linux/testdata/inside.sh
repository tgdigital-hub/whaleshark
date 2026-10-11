#!/bin/sh
# What one container does for the proof on Linux. The test beside this folder
# starts it as root with one word:
#
#   plain    the program file, and the tests that lean on git, as a login
#   server   first the server command for real, as far as a container holds
#            a server; then the same as that server's work login
#
# It finds this tree in /src, Go in /usr/local/go, the modules in /gomod and
# the program as it is shipped in /ws, all to read only, and a folder every
# container shares in /cache. docs/platform/linux.md says what each step
# proves and what a container cannot.
set -eu

say() { printf '\n== %s\n' "$*"; }
fail() {
	printf 'FAILED: %s\n' "$*" >&2
	exit 1
}

# is "OWNER MODE" FILE
is() {
	got=$(stat -c '%U %a' "$2")
	[ "$got" = "$1" ] || fail "$2 is $got, not $1"
}

home() { getent passwd "$1" | cut -d: -f6; }

# as LOGIN COMMAND...: the command as that login, in its own home.
as() {
	who=$1
	shift
	runuser -u "$who" -- env HOME="$(home "$who")" "$@"
}

# again WHAT COMMAND...: the command a second time must change nothing.
again() {
	what=$1
	shift
	if ! "$@" >/tmp/again 2>&1; then
		cat /tmp/again
		fail "$what failed the second time"
	fi
	cat /tmp/again
	! grep -q -e '^wrote ' -e '^gave ' -e '^installed ' -e 'newly installed' /tmp/again ||
		fail "$what changed something the second time"
}

# standin PROGRAM: a container has no system manager, may load no rule into
# the kernel's guard of programs and, without leave, none into its firewall.
# Such a program answers yes here, does nothing, and what it was asked is
# kept. Nothing it would have done counts as proven.
standin() {
	# shellcheck disable=SC2016 # the text of the stand-in, which is to expand when it runs
	printf '#!/bin/sh\necho "$0 $*" >>/var/log/stand-ins\nexit 0\n' >"/usr/local/sbin/$1"
	chmod 755 "/usr/local/sbin/$1"
	touch /var/log/stand-ins
	chmod 666 /var/log/stand-ins
}

# dial LOGIN WORD: what LOGIN is answered at the listener in robin's block.
dial() {
	got=$(as "$1" /usr/local/bin/whaleshark doctor --server dial 21005) || true
	case $got in
	"$2"*) echo "$1: $got" ;;
	*) fail "$1 at a port of robin's block: $got, not $2" ;;
	esac
}

server() {
	say "what a fresh server has and a container's image lacks: an SSH server, and the key root came in by"
	apt-get install -y -q --no-install-recommends openssh-server >/dev/null
	mkdir -p /root/.ssh /root/.local/bin
	ssh-keygen -q -t ed25519 -N '' -f /root/.ssh/id_ed25519
	cp /root/.ssh/id_ed25519.pub /root/.ssh/authorized_keys
	# Where install.sh leaves the program; it installs nothing until a release names its signer.
	cp /ws/whaleshark /root/.local/bin/whaleshark
	for p in systemctl loginctl apparmor_parser; do
		standin "$p"
	done

	say "server setup, on a machine with nothing"
	/root/.local/bin/whaleshark server setup --admin admin
	say "server setup again"
	again "server setup" /root/.local/bin/whaleshark server setup --admin admin

	say "what setup left"
	is "root 755" /usr/local/bin/whaleshark
	is "root 440" /etc/sudoers.d/whaleshark-admin
	is "root 644" /etc/whaleshark/server.toml
	[ -z "$(find /opt/whaleshark ! -user root | head -n 1)" ] || fail "not all of /opt/whaleshark is root's"
	[ -z "$(find /opt/whaleshark ! -type l -perm /022 | head -n 1)" ] || fail "somebody but root may write under /opt/whaleshark"
	cmp "$(home admin)/.ssh/authorized_keys" /root/.ssh/authorized_keys
	as admin sudo -n true
	/opt/whaleshark/node/bin/node --version
	dpkg -s claude-code | grep '^Version'
	cat /opt/whaleshark/browsers/.pin
	ls /opt/whaleshark/browsers

	say "the rule for who may connect to a login's ports"
	walls=real
	if
		nft -f - <<EOF
table inet probe {
	chain out {
		type filter hook output priority 0; policy accept;
		oifname "lo" tcp dport 1 meta skuid != { 0, 1 } reject with tcp reset
	}
}
EOF
	then
		nft delete table inet probe
		echo "this container loads such a rule: nft is the real one"
	else
		walls=
		standin nft
		echo "this container may load no such rule: nft is a stand-in from here"
	fi

	say "server add"
	whaleshark server add --user robin
	say "server add again"
	again "server add" whaleshark server add --user robin
	uid=$(id -u robin)
	is "robin 700" "$(home robin)"
	grep '^SocketBind' "/etc/systemd/system/user-$uid.slice.d/50-whaleshark.conf"
	grep -q "dport 21000-21999 meta skuid != { 0, $uid }" /etc/whaleshark/ports.nft || fail "the rule file does not keep robin's block robin's"
	cat /etc/whaleshark/server.toml
	whaleshark server status

	if [ -n "$walls" ]; then
		say "a listener of robin's in robin's block: who reaches it"
		lo=127.0.0
		as robin /opt/whaleshark/node/bin/node -e 'require("net").createServer(function (s) { s.end() }).listen(21005, process.argv[1])' "$lo.1" &
		sleep 2
		dial robin open
		dial root open
		dial admin closed
		kill $!
	fi

	say "init, as robin: outside the home and inside it"
	mkdir /srv/project "$(home robin)/project"
	chown robin /srv/project "$(home robin)/project"
	got=$(cd /srv/project && as robin sh -c 'git init -q . && exec whaleshark init --json' 2>&1) &&
		fail "init outside the home was not refused: $got"
	case $got in
	*outside_home*) echo "outside the home: refused, outside_home" ;;
	*) fail "init outside the home was refused for something else: $got" ;;
	esac
	got=$(cd "$(home robin)/project" && as robin sh -c 'git init -q . && exec whaleshark init --json' 2>&1) || true
	echo "inside the home: $got"
	case $got in
	*outside_home*) fail "init inside the home was refused as outside it" ;;
	esac

	say "server upgrade, with the next program where install.sh leaves it"
	cp /ws/whaleshark-next /root/.local/bin/whaleshark
	/root/.local/bin/whaleshark server upgrade
	cmp /usr/local/bin/whaleshark /ws/whaleshark-next
	is "root 755" /usr/local/bin/whaleshark

	say "doctor --server, as root"
	whaleshark doctor --server || echo "doctor --server ended with $?"
	say "doctor --server, as robin"
	as robin whaleshark doctor --server || echo "doctor --server ended with $?"
	say "what the stand-ins were asked"
	cat /var/log/stand-ins
}

root() {
	# shellcheck disable=SC1091,SC2154 # the system's own file, and the name it gives itself there
	say "$(. /etc/os-release && echo "$PRETTY_NAME"), $(uname -m), as $kind"
	if command -v apk >/dev/null; then
		apk add --no-cache -q git
	else
		export DEBIAN_FRONTEND=noninteractive
		apt-get update -q >/dev/null
		apt-get install -y -q --no-install-recommends git ca-certificates >/dev/null
	fi
	[ "$kind" = plain ] || server
	if id robin >/dev/null 2>&1; then
		:
	elif command -v useradd >/dev/null; then
		useradd -m robin
	else
		adduser -D robin
	fi
	# The tree as a login's own, and one folder for what every container compiles.
	cp -R /src /work
	chown -R robin /work
	mkdir -p /cache/go
	chmod 1777 /cache /cache/go
	su -s /bin/sh robin -c "exec sh /work/test/platform/linux/testdata/inside.sh login $kind"
}

login() {
	export PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin
	export GOCACHE=/cache/go GOMODCACHE=/gomod GOFLAGS=-buildvcs=false GOPROXY=off GOTOOLCHAIN=local CGO_ENABLED=0
	cd /work || exit 1
	say "the program file, as $(id -un)"
	/ws/whaleshark version
	/ws/whaleshark help >/dev/null
	git --version

	# The first container to come here compiles and the others wait for it;
	# what it writes any login of any container may read and add to.
	set -- ./internal/overlap/ ./internal/gitwt/ ./internal/integrate/ ./test/scenario/phase2/ ./test/platform/linux/
	# Collecting work and the overlap scan merge in git's store alone, which
	# git can do from 2.38 on. Below that their tests fail, and are not run.
	v=$(git --version | cut -d' ' -f3)
	minor=${v#*.}
	if [ "${v%%.*}" -eq 2 ] && [ "${minor%%.*}" -lt 38 ]; then
		echo "NOT RUN: git $v is older than 2.38, which accept and the overlap scan need; their tests fail on this system"
		set -- ./test/platform/linux/
	fi
	say "compiling, once for every container"
	until mkdir /cache/lock 2>/dev/null; do
		sleep 2
	done
	trap 'rmdir /cache/lock' EXIT
	(umask 0 && go test -vet=off -run '^$' "$@")
	rmdir /cache/lock
	trap - EXIT

	say "the tests that lean on git, and this login's checks"
	go test -vet=off -count=1 "$@"
}

kind=${2:-$1}
case $1 in
plain | server) root ;;
login) login ;;
*) fail "plain or server" ;;
esac
