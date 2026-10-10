#!/bin/sh
# What `whaleshark server` does to the system. The program runs this text
# with sh, as root, and never fetches it: it is compiled into the program.
#
#   setup   ADMIN [KEYFILE]
#   harden  ADMIN
#   add     LOGIN PORTS [KEYFILE] ADMIN
#   phone   LOGIN PHONE DOOR [off]
#   upgrade
#   status
#
# The program has checked each word and names its own file in
# WHALESHARK_BIN. ADMIN is the administrator login, PORTS the first port of
# a login's block, DOOR its last, PHONE the port Tailscale answers at; an
# empty KEYFILE stands for none. The program alone writes server.toml.
#
# It ends with 0, with 1 when a step failed (it says which), with 3 when
# this is not root or not a system it knows. Every step first looks whether
# it is done already, so running a command again changes nothing.
set -eu

# What is fetched from others, each held to one version. A checksum is
# written in two halves and joined.
NODE_VERSION=22.23.3
NODE_X64=df450af89261115ef9f9e3830c3eeb2c
NODE_X64=${NODE_X64}c9213b63c720b1af623cb5dcbe2e02de
NODE_ARM64=a44aeb94849a299b22df10b9e622ec2f
NODE_ARM64=${NODE_ARM64}605c2183501bc40590705131de7c740f
PLAYWRIGHT=1.64.0
# The fingerprint of the key Claude Code's maker signs its packages with, as
# its setup page prints it.
CLAUDE_KEY=31DDDE24DDFAB679F42D
CLAUDE_KEY=${CLAUDE_KEY}7BD2BAA929FF1A7ECACE

# The range every login's block of a thousand ports lies in, and what one
# login may take of the machine.
FIRST=21000
LAST=31999
MEMORY=80%
TASKS=16384

# Every place this script writes to. R is empty on a real machine; a test
# names a folder of its own there.
R=${WHALESHARK_TEST_ROOT:-}
ETC=$R/etc/whaleshark
UNITS=$R/etc/systemd/system
USERUNITS=$R/etc/systemd/user
SUDOERS=$R/etc/sudoers.d/whaleshark-admin
SSHCONF=$R/etc/ssh/sshd_config.d/00-whaleshark.conf
APTCONF=$R/etc/apt/apt.conf.d/52whaleshark
SYSCTL=$R/etc/sysctl.d/60-whaleshark.conf
NFT=$ETC/ports.nft
NODE=$R/opt/whaleshark/node
BROWSERS=$R/opt/whaleshark/browsers
KEEPER=whaleshark-keeper.service
DASH=whaleshark-dash.service
PROCGROUP=whaleshark-proc
LO=127.0.0
LO=$LO.1

say() { printf '%s\n' "$*"; }
die() {
	say "server: $*" >&2
	exit 1
}
number() {
	case $1 in
	"" | *[!0-9]*) return 1 ;;
	esac
}
name() { # name WHAT VALUE: a login's name as every system here takes it
	case $2 in
	"" | [!a-z_]* | *[!a-z0-9_-]*) die "$1 must be a login's name: small letters, digits, - and _" ;;
	esac
	[ ${#2} -le 32 ] || die "$1 is longer than a login's name may be"
}
home_of() { getent passwd "$1" | cut -d: -f6; }
installed() { dpkg -s "$1" 2>/dev/null | grep -q '^Status:.* ok installed'; }
of() { # of LOGIN ...: systemctl for that login's own services
	owner=$1
	shift
	systemctl --user -M "$owner@" "$@"
}

# put FILE MODE: standard input becomes FILE, by a rename. It fails when
# FILE held that text already.
put() {
	mkdir -p "${1%/*}"
	cat >"$1.new"
	if cmp -s "$1.new" "$1"; then
		rm -f "$1.new"
		return 1
	fi
	chmod "$2" "$1.new"
	mv -f "$1.new" "$1"
	say "wrote ${1#"$R"}"
}

# keys FILE: FILE holds public keys and nothing else, one a line with
# nothing before the key. They are left in $tmp/keys.
keys() {
	[ -f "$1" ] || return 1
	grep -v -e '^[[:space:]]*$' -e '^#' "$1" >"$tmp/keys" || return 1
	while IFS= read -r line; do
		case $line in
		ssh-* | ecdsa-* | sk-*) ;;
		*) return 1 ;;
		esac
		say "$line" >"$tmp/key"
		ssh-keygen -l -f "$tmp/key" >/dev/null 2>&1 || return 1
	done <"$tmp/keys"
}

# give LOGIN: the keys of $tmp/keys the login does not have yet, written
# with the login's own rights, so that nothing planted in its home folder
# can turn root's hand.
give() {
	# shellcheck disable=SC2016
	runuser -u "$1" -- env HOME="$(home_of "$1")" sh -c '
		umask 077
		mkdir -p "$HOME/.ssh"
		touch "$HOME/.ssh/authorized_keys"
		while IFS= read -r key; do
			grep -qxF "$key" "$HOME/.ssh/authorized_keys" && continue
			printf "%s\n" "$key" >>"$HOME/.ssh/authorized_keys"
			echo "gave $1 a key"
		done' sh "$1" <"$tmp/keys"
}

# logins: every work login, as its number, its name and its block's first port.
logins() {
	for f in "$UNITS"/user-*.slice.d/50-whaleshark.conf; do
		[ -e "$f" ] || continue
		awk -v f="$f" 'NR == 1 { sub(/.*user-/, "", f); sub(/\.slice.*/, "", f); print f, $2, $4 }' "$f"
	done
}

packages() {
	doing="installing the system's own packages"
	missing=
	for p in "$@"; do
		installed "$p" || missing="$missing $p"
	done
	[ -n "$missing" ] || return 0
	apt-get update -q
	# shellcheck disable=SC2086
	DEBIAN_FRONTEND=noninteractive apt-get install -y -q --no-install-recommends $missing
}

administrator() {
	doing="making the administrator login $adm"
	id "$adm" >/dev/null 2>&1 || useradd -m -s /bin/bash "$adm"
	give "$adm"
	say "$adm ALL=(ALL) NOPASSWD:ALL" >"$tmp/sudoers"
	visudo -cf "$tmp/sudoers" >/dev/null
	put "$SUDOERS" 440 <"$tmp/sudoers" || true
}

# Security updates by themselves, and never a restart by itself: an update
# must not restart the machine under running agents.
updates() {
	doing="switching automatic security updates on"
	put "$APTCONF" 644 <<EOF || true
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
Unattended-Upgrade::Automatic-Reboot "false";
EOF
}

program() {
	doing="putting the program in place for every login"
	if [ -n "$packaged" ] || cmp -s "$self" "$BIN"; then
		return 0
	fi
	mkdir -p "${BIN%/*}"
	cp "$self" "$BIN.new"
	chown root:root "$BIN.new"
	chmod 755 "$BIN.new"
	mv -f "$BIN.new" "$BIN"
	say "wrote ${BIN#"$R"}"
}

# unit NAME WHAT ARGUMENTS [LINE]: a service entry every login has and each
# work login switches on for itself. A service starts with next to nothing,
# so it is told where programs are and that the terminal shows every letter.
unit() {
	put "$USERUNITS/$1" 644 <<EOF
[Unit]
Description=WhaleShark: $2

[Service]
ExecStart=${BIN#"$R"} $3
Environment=PATH=%h/.local/bin:/usr/local/bin:/usr/bin:/bin
Environment=LANG=C.UTF-8
Restart=on-failure
${4:-}
[Install]
WantedBy=default.target
EOF
}
# mixed: a stop asks the keeper alone, which ends its panes in its own
# order; what is left after that the system ends.
units() {
	doing="writing the two service entries"
	unit "$KEEPER" "the keeper of this login's terminals" "engine run" KillMode=mixed || true
	unit "$DASH" "this login's page" "dash start" || true
}

claude() {
	doing="installing Claude Code from its maker's signed package source"
	installed claude-code && return 0
	curl -fsSL --proto '=https' --tlsv1.2 -o "$tmp/claude.asc" https://downloads.claude.ai/keys/claude-code.asc
	gpg --show-keys --with-colons "$tmp/claude.asc" >"$tmp/claude.key"
	if [ "$(grep -c '^pub:' "$tmp/claude.key")" != 1 ] ||
		[ "$(awk -F: '$1 == "fpr" { print $10; exit }' "$tmp/claude.key")" != "$CLAUDE_KEY" ]; then
		die "the key fetched for Claude Code is not the one its maker publishes; it was not installed"
	fi
	put "$R/etc/apt/keyrings/claude-code.asc" 644 <"$tmp/claude.asc" || true
	say "deb [signed-by=/etc/apt/keyrings/claude-code.asc] https://downloads.claude.ai/claude-code/apt/stable stable main" |
		put "$R/etc/apt/sources.list.d/claude-code.list" 644 || true
	apt-get update -q
	DEBIAN_FRONTEND=noninteractive apt-get install -y -q claude-code
}

# The headless browser, once for the machine, owned by root and readable by
# all. On Ubuntu 24.04 a program outside the system's own folders may not
# start its sandbox without a rule that names it.
browser() {
	doing="installing the shared browser"
	if [ "$VERSION_ID" = 24.04 ]; then
		if put "$R/etc/apparmor.d/whaleshark-browser" 644 <<EOF; then
abi <abi/4.0>,
include <tunables/global>
profile whaleshark-browser ${BROWSERS#"$R"}/**/{chrome,headless_shell} flags=(unconfined) {
  userns,
  include if exists <local/whaleshark-browser>
}
EOF
			apparmor_parser -r "$R/etc/apparmor.d/whaleshark-browser"
		fi
	fi
	pin="node $NODE_VERSION playwright $PLAYWRIGHT"
	[ "$(cat "$BROWSERS/.pin" 2>/dev/null)" != "$pin" ] || return 0
	case $(uname -m) in
	x86_64) arch=x64 sum=$NODE_X64 ;;
	aarch64) arch=arm64 sum=$NODE_ARM64 ;;
	*) die "no Node is pinned for the processor $(uname -m)" ;;
	esac
	file=node-v$NODE_VERSION-linux-$arch.tar.xz
	curl -fsSL --proto '=https' --tlsv1.2 -o "$tmp/$file" "https://nodejs.org/dist/v$NODE_VERSION/$file"
	got=$(sha256sum "$tmp/$file")
	[ "${got%% *}" = "$sum" ] || die "$file is not the file this version pins; it was not unpacked"
	rm -rf "$NODE.new" "$BROWSERS"
	mkdir -p "$NODE.new" "$BROWSERS"
	tar -xJf "$tmp/$file" -C "$NODE.new" --strip-components=1
	rm -rf "$NODE"
	mv "$NODE.new" "$NODE"
	PATH=$NODE/bin:$PATH npm install -g --no-fund --no-audit --ignore-scripts "playwright@$PLAYWRIGHT"
	PATH=$NODE/bin:$PATH PLAYWRIGHT_BROWSERS_PATH=${BROWSERS#"$R"} playwright install --with-deps chromium
	chown -R root:root "$R/opt/whaleshark"
	chmod -R a+rX,go-w "$R/opt/whaleshark"
	say "$pin" >"$BROWSERS/.pin"
	say "installed the shared browser"
}

# A best effort that nothing relies on: nobody states it is supported with
# services that outlive a session, and doctor reports it as a warning.
hide() {
	getent group "$PROCGROUP" >/dev/null || groupadd -r "$PROCGROUP"
	gid=$(getent group "$PROCGROUP" | cut -d: -f3)
	put "$UNITS/systemd-logind.service.d/50-whaleshark.conf" 644 <<EOF && systemctl daemon-reload
[Service]
SupplementaryGroups=$PROCGROUP
EOF
	grep -q '^proc /proc proc .*hidepid=2' "$R/etc/fstab" 2>/dev/null && return 0
	say "proc /proc proc defaults,hidepid=2,gid=$gid 0 0" >>"$R/etc/fstab"
	mount -o "remount,hidepid=2,gid=$gid" /proc
}

setup() {
	adm=${1:-} key=${2:-}
	name "the administrator's login" "$adm"
	[ -n "$key" ] || key=$(home_of "${SUDO_USER:-root}")/.ssh/authorized_keys
	keys "$key" || die "$key is not a file of public keys, one a line with nothing before the key. Give one with --admin-key FILE. Nothing was changed"
	say "The administrator login $adm is let in by:"
	ssh-keygen -l -f "$tmp/keys"

	packages git curl ufw nftables unattended-upgrades gnupg xz-utils sudo ca-certificates
	administrator
	updates
	program
	units
	claude
	browser
	doing=
	(hide) || say "note: other logins' processes could not be hidden; nothing relies on it"

	say "Done, and nothing was closed: the way you came in still works, and so does"
	say "your provider's console, now and after the next step."
	say "Next, from your own computer, so that only someone the new key lets in can close the old doors:"
	say "  ssh $adm@<server> sudo whaleshark server harden"
}

# over_ssh: one of the programs this one was started from is the SSH server.
over_ssh() {
	p=$$
	while [ "${p:-0}" -gt 1 ]; do
		case $(ps -o comm= -p "$p") in
		sshd*) return 0 ;;
		esac
		p=$(ps -o ppid= -p "$p" | tr -d ' ')
	done
	return 1
}

# sshd uses the first value it meets, so the file is named to sort first,
# and what counts is what sshd says is in force, not what the file says.
doors() {
	doing="closing every door of SSH but the key"
	wrote=
	if put "$SSHCONF" 644 <<EOF; then
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin no
PubkeyAuthentication yes
AllowTcpForwarding local
PermitOpen $LO:* [::1]:*
AllowStreamLocalForwarding local
GatewayPorts no
X11Forwarding no
AllowAgentForwarding no
ClientAliveInterval 30
EOF
		wrote=1
	fi
	wrong=
	sshd -T >"$tmp/sshd" 2>/dev/null || wrong=" sshd refuses its settings"
	while IFS= read -r want; do
		grep -qix "$want" "$tmp/sshd" || wrong="$wrong [$want]"
	done <<EOF
passwordauthentication no
kbdinteractiveauthentication no
permitrootlogin no
pubkeyauthentication yes
allowtcpforwarding local
permitopen $LO:\* \[::1\]:\*
allowstreamlocalforwarding local
gatewayports no
x11forwarding no
allowagentforwarding no
clientaliveinterval 30
EOF
	if [ -n "$wrong" ]; then
		[ -z "$wrote" ] || rm -f "$SSHCONF"
		die "not in force after our file:$wrong. Another file of sshd's sets it first. SSH was left as it was"
	fi
	[ -z "$wrote" ] || systemctl reload ssh
}

firewall() {
	doing="refusing everything from outside but SSH"
	port=$(awk '$1 == "port" { print $2; exit }' "$tmp/sshd")
	ufw status verbose >"$tmp/ufw" 2>/dev/null || true
	if ! grep -q '^Status: active' "$tmp/ufw" || ! grep -q 'deny (incoming)' "$tmp/ufw" ||
		! grep -q "^$port/tcp .*ALLOW IN" "$tmp/ufw"; then
		ufw default deny incoming
		ufw allow "$port/tcp"
		ufw --force enable
	fi
	# No connection going out is handed a port of anybody's block by chance.
	if say "net.ipv4.ip_local_reserved_ports = $FIRST-$LAST" | put "$SYSCTL" 644; then
		sysctl -q -p "$SYSCTL"
	fi
}

harden() {
	adm=${1:-}
	name "the administrator's login" "$adm"
	if [ "${SUDO_USER:-}" != "$adm" ] || ! over_ssh; then
		die "harden closes the old doors, so it runs only for someone the new one let in. From your own computer: ssh $adm@<server> sudo whaleshark server harden. Nothing was changed"
	fi
	sudo -n -u "$adm" sudo -n true 2>/dev/null ||
		die "$adm cannot use sudo without a password. Run server setup again. Nothing was changed"
	doors
	firewall
	say "Hardened: keys only, no login as root, nothing from outside but SSH."
	say "Next: ssh $adm@<server> sudo whaleshark server add --user NAME"
}

# walls: who may connect to a login's block on this machine: that login and
# the system, which is how Tailscale hands a phone's connection to the page.
walls() {
	doing="keeping each login's ports its own"
	{
		say "table inet whaleshark"
		say "delete table inet whaleshark"
		say "table inet whaleshark {"
		say "	chain out {"
		say "		type filter hook output priority 0; policy accept;"
		logins | while read -r n _ first; do
			say "		oifname \"lo\" tcp dport $first-$((first + 999)) meta skuid != { 0, $n } reject with tcp reset"
		done
		say "	}"
		say "}"
	} >"$tmp/nft"
	if put "$NFT" 644 <"$tmp/nft"; then
		nft -f "$NFT"
	fi
	if put "$UNITS/whaleshark-ports.service" 644 <<EOF; then
[Unit]
Description=WhaleShark: each login's ports are its own

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/sbin/nft -f ${NFT#"$R"}

[Install]
WantedBy=multi-user.target
EOF
		systemctl daemon-reload
		systemctl enable -q whaleshark-ports.service
	fi
}

add() {
	login=${1:-} ports=${2:-} key=${3:-} adm=${4:-}
	name "the work login" "$login"
	name "the administrator's login" "$adm"
	[ "$login" != "$adm" ] || die "$adm is the administrator login, which never runs an agent. Nothing was changed"
	number "$ports" || ports=0
	if [ "$ports" -lt "$FIRST" ] || [ "$ports" -gt $((LAST - 999)) ] || [ $(((ports - FIRST) % 1000)) -ne 0 ]; then
		die "$ports is not the first port of a block between $FIRST and $LAST"
	fi
	[ -x "$BIN" ] || die "the program is not in place: run server setup first. Nothing was changed"
	[ -n "$key" ] || say "No --key: $login is let in by the keys of $adm."
	[ -n "$key" ] || key=$(home_of "$adm")/.ssh/authorized_keys
	keys "$key" ||
		die "$key is not a file of public keys, one a line with nothing before the key. Nothing was changed"

	doing="making the work login $login"
	if id "$login" >/dev/null 2>&1; then
		if id -nG "$login" | tr ' ' '\n' | grep -qx -e sudo -e admin -e wheel; then
			die "$login may use sudo, and a work login never does. Nothing was changed"
		fi
	else
		useradd -m -s /bin/bash "$login"
	fi
	chmod 700 "$(home_of "$login")"
	give "$login"
	uid=$(id -u "$login")

	# Who may open a port there, and a fair share of the machine.
	doing="giving $login its block of ports and its share of the machine"
	if put "$UNITS/user-$uid.slice.d/50-whaleshark.conf" 644 <<EOF; then
# $login ports $ports
[Slice]
SocketBindDeny=$FIRST-$LAST
SocketBindAllow=$ports-$((ports + 999))
MemoryMax=$MEMORY
CPUWeight=100
TasksMax=$TASKS
EOF
		systemctl daemon-reload
	fi
	walls

	doing="starting the keeper and the page of $login"
	units
	[ -e "$R/var/lib/systemd/linger/$login" ] || loginctl enable-linger "$login"
	systemctl is-active -q "user@$uid.service" || systemctl start "user@$uid.service"
	for u in "$KEEPER" "$DASH"; do
		of "$login" is-enabled -q "$u" 2>/dev/null || of "$login" enable -q --now "$u"
	done

	say "$login is set up: ports $ports to $((ports + 999)), the keeper and the page switched on."
	say "What only $login can do, once, in an SSH session of their own:"
	say "  claude                                   sign in; it shows a code to paste"
	say "  git config --global user.name \"...\""
	say "  git config --global user.email \"...\""
	say "  a GitHub token that reaches only their own repositories and cannot push to main"
}

# The administrator runs Tailscale's own command for one person; people
# never set Tailscale up themselves, and this script never installs it.
phone() {
	login=${1:-} phone=${2:-} door=${3:-} off=${4:-}
	name "the work login" "$login"
	number "$phone" || die "$phone is not a port"
	number "$door" || die "$door is not a port"
	command -v tailscale >/dev/null ||
		die "Tailscale is not on this machine, and WhaleShark does not install it: use its maker's own line, sign the machine in, then run this again"
	tailscale status >/dev/null 2>&1 || die "Tailscale is not signed in on this machine: sudo tailscale up"
	doing="setting the door for the phone of $login"
	if [ -n "$off" ]; then
		! tailscale serve status 2>/dev/null | grep -q "proxy http://$LO:$door\$" ||
			tailscale serve --https="$phone" off
		say "$login has no door for a phone any more."
		return 0
	fi
	tailscale status --json | grep -q '"CertDomains": *\[' ||
		die "the private network hands out no certificates yet. Its owner switches on MagicDNS and HTTPS certificates in Tailscale's admin page; that publishes this machine's name and the network's name in a public list, so name the machine after nobody. Nothing was changed"
	tailscale serve status 2>/dev/null | grep -q "proxy http://$LO:$door\$" ||
		tailscale serve --bg --https="$phone" "http://$LO:$door"
	say "The door for the phone of $login answers at port $phone of this machine's Tailscale name."
	say "Next, as $login: whaleshark dash phone on"
}

status() {
	logins | while read -r n who first; do
		line="$who  ports $first-$((first + 999))"
		line="$line  keeper $(of "$who" is-active "$KEEPER" 2>/dev/null || true)"
		line="$line  page $(of "$who" is-active "$DASH" 2>/dev/null || true)"
		line="$line  $(systemctl show "user-$n.slice" -p TasksCurrent --value 2>/dev/null || true) processes"
		! pgrep -u "$who" -f 'whaleshark accept' >/dev/null 2>&1 || line="$line  accept in progress"
		say "$line"
	done
	[ ! -e "$R/var/run/reboot-required" ] ||
		say "restart needed since $(date -r "$R/var/run/reboot-required" +%Y-%m-%d): pick the moment, nothing restarts by itself"
}

# Every process that runs keeps the file it started with. The page's server
# loses nothing by a restart; a keeper is never restarted here, because that
# ends every agent of its login.
upgrade() {
	status
	if [ -n "$packaged" ]; then
		doing="asking the package manager for the new program"
		apt-get update -q
		DEBIAN_FRONTEND=noninteractive apt-get install -y -q --only-upgrade "$packaged"
	elif cmp -s "$self" "$BIN"; then
		die "this is the program that is in place already. Fetch the new version's install.sh, run it as yourself, then: sudo ~/.local/bin/whaleshark server upgrade. Nothing was changed"
	else
		program
	fi
	units
	doing="restarting each login's page"
	logins | while read -r _ who _; do
		of "$who" daemon-reload
		of "$who" restart "$DASH"
	done
	say "Upgraded. Each keeper runs on as it was: whaleshark doctor says where one wants a restart."
}

verb=${1:-}
[ $# -eq 0 ] || shift
self=${WHALESHARK_BIN:-}

if [ "$verb" != status ] && [ "$(id -u)" != 0 ]; then
	say "server: this needs root: sudo whaleshark server $verb" >&2
	exit 3
fi
ID='' VERSION_ID=''
# shellcheck disable=SC1090,SC1091
[ ! -r "$R/etc/os-release" ] || . "$R/etc/os-release"
case $ID:$VERSION_ID in
ubuntu:22.04 | ubuntu:24.04 | debian:12) ;;
*)
	say "server: this is for Ubuntu 22.04, Ubuntu 24.04 and Debian 12; nothing was changed" >&2
	exit 3
	;;
esac
[ -f "$self" ] || die "the program did not say where its own file is"

# A program its package manager put there stays that manager's.
packaged=$(dpkg -S "$self" 2>/dev/null | cut -d: -f1) || true
BIN=$R/usr/local/bin/whaleshark
[ -z "$packaged" ] || BIN=$self

# status changes nothing and is anybody's to ask; without root it knows less.
if [ "$verb" = status ]; then
	status
	exit 0
fi

# Leftovers, a fetched file or a given key, lie in a private folder of
# root's and go when the script ends.
mkdir -p "$R/root"
tmp=$(mktemp -d "$R/root/.whaleshark-server.XXXXXX")
doing=
trap 'rc=$?
	rm -rf "$tmp"
	[ "$rc" -eq 0 ] || [ -z "$doing" ] || say "server: failed while $doing" >&2
	[ "$rc" -eq 0 ] || [ "$rc" -eq 3 ] || rc=1
	exit "$rc"' EXIT

case $verb in
setup | harden | add | phone | upgrade) "$verb" "$@" ;;
*) die "there is no server $verb" ;;
esac
