// Package install holds the two shell scripts of the tool, compiled into
// the program so that nothing which runs as root is fetched on its own.
// Neither is built yet; their owner replaces the two files.
//
// Server is what `whaleshark server` runs with sh and its arguments, as
// root: setup, harden, add, phone, tunnel, upgrade, status. The command in
// the program checks the caller and the arguments, works out a new login's
// block with contract.Server.Free, and is the one writer of
// contract.ServerFile; the script does the system's part and is safe to run
// twice. It installs the two service entries of a work login under the
// names contract.UnitKeeper and contract.UnitDash, and the shared browser
// at contract.ServerBrowsers with the Node at contract.ServerNode. Where
// the program was installed by the system's package manager it leaves the
// program file to that: setup does not copy it and upgrade asks the
// package manager for the new one.
//
// Installer is install.sh, the one-line install for one login without
// root: it fetches a release's file and its signed list, checks both, and
// puts the file in place by renaming it. It is also published beside each
// release as it stands here.
package install

import _ "embed"

//go:embed server.sh
var Server string

//go:embed install.sh
var Installer string
