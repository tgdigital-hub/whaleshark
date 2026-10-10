#!/bin/sh
# WhaleShark's installer for one login. It needs no root and runs nothing it
# fetched: it takes the program file for this system and the release's signed
# list, checks the list's signature against the key below, checks the file
# against the list, and only then renames the file into place.
#
#   sh install.sh                the release this script belongs to
#   sh install.sh --from DIR     the same checks on the release files in DIR
#
# A release is: whaleshark-<system>-<processor> for each system, SHA256SUMS
# with a line for each of them, and SHA256SUMS.sig, made with
# `ssh-keygen -Y sign -n file`. The release fills in the two lines below.
set -eu

VERSION=dev
# One allowed-signers line: release namespaces="file" <key type> <key>
SIGNER=''

SITE=https://github.com/tgdigital-hub/whaleshark/releases/download
DEST=${HOME:?}/.local/bin

say() { printf '%s\n' "$*"; }
die() {
	say "install: $*" >&2
	exit 1
}

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
*) die "no release for $(uname -s); on Windows use winget" ;;
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
case :$PATH: in
*:"$DEST":*) ;;
*) say "$DEST is not on your PATH: add it, or type the whole path." ;;
esac
say "Next: type whaleshark in a project. On a server: sudo $DEST/whaleshark server setup"
