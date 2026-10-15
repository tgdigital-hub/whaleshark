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
	"slices"
	"strings"
	"testing"
)

// folders are the variables the program works a login's folders out from,
// on every system, each with the folder it gets under the login made here.
var folders = map[string]string{"XDG_CONFIG_HOME": "config", "XDG_STATE_HOME": "state", "XDG_CACHE_HOME": "cache",
	"APPDATA": "roaming", "LOCALAPPDATA": "local"}

// Fresh points this process, and with it every program it starts, at a
// login made for it in a temporary folder, whatever login it was started
// in, names no engine of that login's, is no tab's and lets no shell keep a
// history. done removes the folder. The
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
	// A test run in a worker's tab would start the program as that worker.
	// One that wants a variable of ours sets it itself, after this.
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); ours(name) {
			os.Unsetenv(name)
		}
	}
	// A shell a test starts in a pane writes what was typed at it into the
	// history file of the home folder, which stays the person's own.
	os.Setenv("HISTFILE", os.DevNull)
	return func() { os.RemoveAll(dir) }
}

// NoTab is env without the variables of ours, which say whose tab a
// program runs in, at which engine and in which run, and how it was
// started: the gate runs the program with this, as Fresh leaves a test.
func NoTab(env []string) (out []string) {
	for _, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); !ours(name) {
			out = append(out, kv)
		}
	}
	return out
}

// tests are the beginnings of the variables of ours that are a test's own
// and no program's: what a test hands the test program it runs again, and
// the switches a person turns a test on with.
var tests = []string{"WHALESHARK_TEST_", "WHALESHARK_SOAK", "WHALESHARK_CONTAINERS", "WHALESHARK_INSIDE",
	"WHALESHARK_REAL_AGENT", "WHALESHARK_PAGE_OUT", "WHALESHARK_CORPUS_EXTRA"}

// ours is whether a variable is one the program reads of its own: every
// one that begins as ours does, a test's own aside.
func ours(name string) bool {
	name = strings.ToUpper(name)
	return strings.HasPrefix(name, "WHALESHARK_") && !slices.ContainsFunc(tests, func(t string) bool { return strings.HasPrefix(name, t) })
}

// Main is the TestMain of a package whose tests need nothing else done
// first, and the last line of one that does.
func Main(m *testing.M) {
	done := Fresh()
	code := m.Run()
	done()
	os.Exit(code)
}
