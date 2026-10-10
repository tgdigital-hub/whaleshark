// Package connect is not built yet. Its owner replaces this file.
//
// It binds connect, which runs on the person's own computer and speaks to
// the server through ssh alone. It keeps one master connection with a
// control socket, keep-alive and no agent forwarding; runs
// `whaleshark dash url --json` there and reads a contract.Envelope holding
// a contract.DashURL; forwards contract.PagePort, or --port, to that socket
// and opens http://<contract.PageHosts[0]>:<port><contract.RouteIn>?code=...
// in the browser. Every five seconds it asks the page contract.RouteSites
// for the ports of live tasks' sites, forwards each new one under its own
// number on the loopback address and drops those that are gone; a port
// that is not in that answer is never forwarded. A clock that jumped means
// the computer slept: it tests the link at once. Retries after 1, 2, 5, 10
// and then every 30 seconds. --terminals runs `ssh -t <target> whaleshark
// open` in this window with contract.EnvSystem set to this computer's
// system, lifts copied text out of the stream onto this computer's
// clipboard, and runs it again when it drops. --at-login and --not-at-login
// write and remove a login item of the person's own, on macOS. It imports
// neither the store nor the inbox (the gate checks) and needs no keeper.
package connect

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(*contract.Kit) {}
