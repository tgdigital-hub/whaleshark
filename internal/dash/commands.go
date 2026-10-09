// Package dash is not built yet. Its owner replaces this file. Until then it
// holds an empty page, so that the program file is measured with a page in it.
package dash

import (
	"errors"
	"html/template"
	"net"
	"net/http"

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
	return nil, http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		empty.Execute(w, contract.ErrNotBuilt.Error())
	}))
}
