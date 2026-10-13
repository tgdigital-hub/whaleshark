// Package ownlogin gives the tests of a package a login of their own. The
// program keeps its settings, its list of projects and its engine's socket
// in the folders of whoever runs it, and a test is run by a person at their
// own computer: without this a test's made-up projects are written into that
// person's list, where their own window then shows them. Every package that
// has tests hands its TestMain to Main, or calls Fresh in it, and the gate
// refuses a package of tests that does neither.
package ownlogin

import (
	"os"
	"path/filepath"
	"testing"
)

// folders are the variables the program works a login's folders out from,
// on every system, each with the folder it gets under the login made here.
var folders = map[string]string{"XDG_CONFIG_HOME": "config", "XDG_STATE_HOME": "state", "XDG_CACHE_HOME": "cache",
	"APPDATA": "roaming", "LOCALAPPDATA": "local"}

// Fresh points this process, and with it every program it starts, at a
// login made for it in a temporary folder, whatever login it was started
// in, and names no engine of that login's. done removes the folder. The
// path is short: the engine's socket lies in it. The Go command a test
// starts keeps its cache and its settings where they were, since both are
// found by the same variables; a test that needs a login of a certain kind
// still names its own, over this one.
func Fresh() (done func()) {
	dir, err := os.MkdirTemp("", "ws")
	if err != nil {
		panic(err)
	}
	if cache, err := os.UserCacheDir(); err == nil && os.Getenv("GOCACHE") == "" {
		os.Setenv("GOCACHE", filepath.Join(cache, "go-build"))
	}
	if config, err := os.UserConfigDir(); err == nil && os.Getenv("GOENV") == "" {
		os.Setenv("GOENV", filepath.Join(config, "go", "env"))
	}
	for name, folder := range folders {
		os.Setenv(name, filepath.Join(dir, folder))
	}
	os.Unsetenv("WHALESHARK_SOCKET")
	return func() { os.RemoveAll(dir) }
}

// Main is the TestMain of a package whose tests need nothing else done
// first, and the last line of one that does.
func Main(m *testing.M) {
	done := Fresh()
	code := m.Run()
	done()
	os.Exit(code)
}
