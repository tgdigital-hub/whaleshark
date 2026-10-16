// Package install holds the installers of the tool. The server's is compiled
// into the program, so that nothing which runs as root is fetched on its own.
//
// Server is what `whaleshark server` runs as root, with sh reading it from
// standard input and the program's own file named in contract.EnvBin; its
// head names the commands and their words. The command in the program
// checks the arguments, works out a new login's block with
// contract.Server.Free, and is the one writer of contract.ServerFile; the
// script does the system's part and is safe to run twice. Its two service
// entries are contract.UnitKeeper and contract.UnitDash, the shared browser
// is at contract.ServerBrowsers with the Node at contract.ServerNode. Where
// the system's package manager installed the program, setup leaves the file
// to it and upgrade asks it for the new one.
//
// Installer is install.sh, the one-line install for one login without
// root: it fetches a release's file and its signed list, or takes them from
// a folder (--from), checks both, and renames the file into place. No
// command of the program reads it: it is here for this package's tests, and
// a person takes install.sh from a release.
package install

import _ "embed"

//go:embed server.sh
var Server string

//go:embed install.sh
var Installer string

// WindowsInstaller is install.ps1, install.sh's twin for one Windows login,
// here, as Installer is, for this package's tests.
//
//go:embed install.ps1
var WindowsInstaller string
