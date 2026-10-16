#!/bin/sh
# WhaleShark's installer for one login. It needs no root and runs nothing it
# fetched: it takes the program file for this system and the release's signed
# list, checks the list's signature against the key below, checks the file
# against the list, and only then renames the file into place.
#
#   curl -fsSL https://github.com/tgdigital-hub/whaleshark/releases/latest/download/install.sh | sh
#   sh install.sh                the release this script belongs to
#   sh install.sh --from DIR     the same checks on the release files in DIR
#
# Piped to sh it reads nothing of itself and nothing from its input: all of
# it is one function, called on the last line, so a download cut short runs
# nothing. Where ~/.local/bin is not on PATH, one line for it goes into the
# start files of the login's shell, once; running it again only upgrades.
#
# A release is: whaleshark-<system>-<processor> for each system, SHA256SUMS
# with a line for each of them and "# whaleshark <version>", and
# SHA256SUMS.sig, made with `ssh-keygen -Y sign -n file`. The release fills
# in the two lines below.
set -eu

VERSION=dev
# One allowed-signers line: release namespaces="file" <key type> <key>
SIGNER=''

SITE=https://github.com/tgdigital-hub/whaleshark/releases/download
DEST=${HOME:?}/.local/bin
# The line a start file gets, and the mark by which a second run knows it.
MARK='# added by whaleshark install.sh'
LINE='case ":$PATH:" in *":$HOME/.local/bin:"*) ;; *) PATH="$HOME/.local/bin:$PATH"; export PATH ;; esac '$MARK

say() { printf '%s\n' "$*"; }
die() {
	say "install: $*" >&2
	exit 1
}

# addline puts LINE at the end of the start file $1, unless it is there.
addline() {
	! grep -qF "$MARK" "$1" 2>/dev/null || return 0
	if printf '\n%s\n' "$LINE" >>"$1"; then
		say "added $DEST to PATH in $1"
		added=1
	else
		say "could not change $1: add $DEST to PATH yourself"
	fi
}

# onpath makes a new terminal find the program, for the login's own shell.
onpath() {
	case :$PATH: in *:"$DEST":*) return 0 ;; esac
	added=
	shell=${SHELL:-sh}
	case ${shell##*/} in
	zsh)
		# A Mac's terminal starts a login shell, which reads both.
		addline "$HOME/.zprofile"
		addline "$HOME/.zshrc"
		;;
	bash)
		# A login shell reads the first of these three that is there.
		if [ -f "$HOME/.bash_profile" ]; then
			addline "$HOME/.bash_profile"
		elif [ -f "$HOME/.bash_login" ]; then
			addline "$HOME/.bash_login"
		else
			addline "$HOME/.profile"
		fi
		addline "$HOME/.bashrc"
		;;
	fish)
		if WS_DEST=$DEST fish -c 'contains -- $WS_DEST $fish_user_paths' </dev/null; then
			:
		elif WS_DEST=$DEST fish -c 'fish_add_path -U -- $WS_DEST' </dev/null; then
			say "added $DEST to fish's PATH with fish_add_path"
			added=1
		else
			say "could not run fish_add_path: add $DEST to PATH yourself"
		fi
		;;
	*) addline "$HOME/.profile" ;;
	esac
	[ -z "$added" ] || say "Open a new terminal, so that it finds whaleshark."
}

main() {
	from=
	while [ $# -gt 0 ]; do
		case $1 in
		--from)
			[ $# -ge 2 ] || die "--from needs a folder"
			from=$2
			shift 2
			;;
		*) die "usage: sh install.sh [--from DIR]" ;;
		esac
	done

	[ -n "$SIGNER" ] || die "this copy carries no release key: take install.sh from a release"
	[ -n "$from" ] || [ "$VERSION" != dev ] || die "this copy names no release: take install.sh from a release, or give --from DIR"
	command -v ssh-keygen >/dev/null || die "ssh-keygen is missing; it comes with OpenSSH"

	case $(uname -s) in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*) die "no release for $(uname -s); on Windows use install.ps1, as the README says" ;;
	esac
	case $(uname -m) in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) die "no release for the processor $(uname -m)" ;;
	esac
	file=whaleshark-$os-$arch

	# The work folder lies beside the program's place, private to this login, so
	# that the last step is a rename and nothing is ever in a shared temp folder.
	mkdir -p "$DEST"
	# mv would put the file inside a folder of that name and say nothing.
	[ ! -d "$DEST/whaleshark" ] || die "$DEST/whaleshark is a folder, where the program goes; nothing was installed"
	work=$(mktemp -d "$DEST/.whaleshark-install.XXXXXX")
	trap 'rm -rf "$work"' EXIT
	trap 'exit 1' HUP INT TERM

	for f in "$file" SHA256SUMS SHA256SUMS.sig; do
		if [ -n "$from" ]; then
			cp "$from/$f" "$work/$f" || die "$f is not in $from"
		else
			command -v curl >/dev/null || die "curl is missing"
			curl -fsSL --proto '=https' --tlsv1.2 -o "$work/$f" "$SITE/v$VERSION/$f" ||
				die "could not fetch $f of version $VERSION"
		fi
	done

	say "$SIGNER" >"$work/signers"
	ssh-keygen -Y verify -f "$work/signers" -I release -n file \
		-s "$work/SHA256SUMS.sig" <"$work/SHA256SUMS" >/dev/null 2>&1 ||
		die "the list of checksums is not signed by the release key; nothing was installed"
	# An older release, rightly signed, must not pass for this one.
	[ "$VERSION" = dev ] || grep -qxF "# whaleshark $VERSION" "$work/SHA256SUMS" ||
		die "the signed list is not version $VERSION's; nothing was installed"

	if command -v sha256sum >/dev/null; then
		sum=$(sha256sum "$work/$file")
	else
		sum=$(shasum -a 256 "$work/$file")
	fi
	want=$(awk -v f="$file" '$2 == f || $2 == "*" f { print $1 }' "$work/SHA256SUMS")
	[ -n "$want" ] || die "the signed list has no line for $file"
	[ "${sum%% *}" = "$want" ] || die "$file is not the file the release signed; nothing was installed"

	chmod 755 "$work/$file"
	# A browser's download carries a mark that stops a Mac from running the
	# file; curl sets none, and a file checked as above needs none.
	[ "$os" != darwin ] || xattr -d com.apple.quarantine "$work/$file" 2>/dev/null || true
	mv -f "$work/$file" "$DEST/whaleshark"

	say "installed $DEST/whaleshark"
	say "The release key, to compare with the README:"
	say "${SIGNER#* namespaces=\"file\" }" >"$work/key.pub"
	ssh-keygen -l -f "$work/key.pub"
	onpath
	# The line for root is one a person pastes: a path with a blank or a sign in
	# it is written as the one word a shell reads it back as.
	case $DEST in
	*[!A-Za-z0-9/._-]*) bin=\'$(say "$DEST/whaleshark" | sed "s/'/'\\\\''/g")\' ;;
	*) bin=$DEST/whaleshark ;;
	esac
	say "Next: type whaleshark in a project. On a server: sudo $bin server setup"
}

main "$@"
