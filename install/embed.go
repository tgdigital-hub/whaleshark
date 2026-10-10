// Package install holds the two shell scripts of the tool, compiled into
// the program so that nothing which runs as root is fetched on its own.
//
// Server is what `whaleshark server` runs as root, with sh reading it from
// standard input and the program's own file named in contract.EnvBin:
// `sh -s -- setup ADMIN [KEYFILE]`, `harden ADMIN`,
// `add LOGIN PORTS [KEYFILE] ADMIN`, `phone LOGIN PHONE DOOR [off]`,
// `upgrade`, `status`. ADMIN is the administrator login, PORTS the first
// port of the login's block, DOOR its last, PHONE the port Tailscale
// answers at; an empty KEYFILE stands for none. The
// command in the program checks the arguments, works out a new login's
// block with contract.Server.Free, and is the one writer of
// contract.ServerFile; the script does the system's part, is safe to run
// twice, and ends with 0, with 1 when a step failed, with 3 when it is not
// root or not a system it knows. The two service entries, named
// contract.UnitKeeper and contract.UnitDash, are written once for the
// machine and switched on by each work login; the shared browser is at
// contract.ServerBrowsers with the Node at contract.ServerNode. Where the
// program was installed by the system's package manager it leaves the
// program file to that: setup does not copy it and upgrade asks the
// package manager for the new one. Otherwise upgrade puts the file it was
// started from in place, which install.sh has checked.
//
// Installer is install.sh, the one-line install for one login without
// root: it fetches a release's file and its signed list, or takes them from
// a folder (--from), checks both, and puts the file in place by renaming
// it. A release gives it its version and the release key.
package install

import _ "embed"

//go:embed server.sh
var Server string

//go:embed install.sh
var Installer string
