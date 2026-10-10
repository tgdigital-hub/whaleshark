// Package dash is not built yet. Its owner replaces this file. Until then it
// holds an empty page, so that the program file is measured with a page in it.
//
// It binds dash. The laptop's page listens on dash.sock in the login's
// state folder and on nothing else; the phone's door is a second listener
// on the loopback port contract.ReadServer gives as Door, open only while
// contract.Phones.On. The routes are the contract's (RouteTeam and the
// rest). The team's screen is built with Kit.View over Kit.Reader on a file
// notice, never in a loop; every other screen is a read command's own
// result (show, graph, log, catchup, status --everywhere), run as a child
// with --json and read as a contract.Envelope. A screen is drawn with
// Kit.Page and a fixed file served from Kit.Asset. A button is one row of
// contract.Actions and nothing else, its id one the view lists, its text
// written to a private file; never one of contract.Never. Every child
// carries contract.EnvFrom as the page's or the phone's mark and neither
// EnvPane nor EnvAttempt. A sweep child runs every five seconds for each
// open run of the login's list of projects, browser or not. Evidence is
// served by its number through Kit.Evidence.Open. Who is calling at the
// door is contract.Identity on the Tailscale road and the checked token's
// subject on Cloudflare's, against contract.Server.Access; with that
// missing the door answers nothing. Phones are written through
// contract.ChangePhones alone, what a paired phone posts to RoutePush for
// its nudges included; the same route answers Kit.PushKey to its script. It imports neither the store nor the inbox.
package dash

import (
	"errors"
	"html/template"
	"net"
	"net/http"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

var empty = template.Must(template.New("empty").Parse(
	`<!doctype html><meta charset="utf-8"><title>WhaleShark</title><p>{{.}}</p>`))

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) { k.Handle("empty-page", emptyPage) }

// emptyPage serves one empty page on the socket file it is given.
func emptyPage(c *contract.Call) (any, error) {
	if len(c.Args) != 1 {
		return nil, errors.New("usage: whaleshark empty-page <socket file>")
	}
	l, err := net.Listen("unix", c.Args[0])
	if err != nil {
		return nil, err
	}
	page := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		empty.Execute(w, contract.ErrNotBuilt.Error())
	})
	// A caller that never finishes its request is not waited for.
	return nil, (&http.Server{Handler: page, ReadHeaderTimeout: 10 * time.Second}).Serve(l)
}
