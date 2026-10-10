package dash

import (
	"context"
	"path/filepath"
	"slices"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const (
	// Every sweepEvery the sweep is started for each open run, browser or
	// not; while a tab is held open the terminals' picture is held against
	// the last one on the same clock.
	sweepEvery = 5 * time.Second
	// A browser holds few connections to one host open at once. Past
	// tabsMost held-open tabs the oldest is let go; its script asks again
	// when the tab is shown.
	tabsMost = 4
)

// join holds one more tab open. The first one starts the looking: with no
// browser connected the server watches no file and no terminal.
func (s *server) join() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	news := make(chan struct{}, 1)
	if s.tabs = append(s.tabs, news); len(s.tabs) > tabsMost {
		close(s.tabs[0])
		s.tabs = s.tabs[1:]
	}
	if s.unlook == nil {
		var ctx context.Context
		ctx, s.unlook = context.WithCancel(s.ctx)
		go s.look(ctx)
		go s.listen(ctx)
	}
	return news
}

// leave lets a tab go, and with the last one the looking ends.
func (s *server) leave(news chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tabs = slices.DeleteFunc(s.tabs, func(c chan struct{}) bool { return c == news }); len(s.tabs) == 0 && s.unlook != nil {
		s.unlook()
		s.unlook = nil
	}
}

// wake tells every held-open tab that something changed.
func (s *server) wake() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.woken = true
	for _, news := range s.tabs {
		select {
		case news <- struct{}{}:
		default:
		}
	}
}

// look is the first source of news and the slow clock: a notice for each
// file a screen is read from, and every few seconds one picture of the
// terminals held against the last, so that a line the second source lost
// is found. Without notices it says so and looks on the clock alone.
func (s *server) look(ctx context.Context) {
	var w contract.Watcher
	var watched []string
	var last []contract.Pane
	defer func() {
		if w != nil {
			w.Close()
		}
	}()
	tick := time.NewTicker(sweepEvery)
	defer tick.Stop()
	for first := true; ; first = false {
		paths := []string{filepath.Join(s.dirs.State, "ctx"), filepath.Join(s.dirs.State, contract.UIFileName),
			filepath.Join(s.dirs.State, "paused"), filepath.Join(s.dirs.Config, contract.ConfigFile)}
		for _, j := range s.open() {
			paths = append(paths, filepath.Join(s.k.Reader().Dir(j.root, j.run), contract.StateFile))
		}
		if w == nil || !slices.Equal(paths, watched) {
			if w != nil {
				w.Close()
			}
			w, _ = s.k.Platform.Watch(paths...)
			watched = paths
		}
		var panes []contract.Pane
		if snap, err := s.k.Terms.Snapshot(ctx); err == nil {
			panes = snap.Panes
		}
		s.mu.Lock()
		s.slow = w == nil || w.Slow()
		lost := !first && !s.woken && (s.slow || !slices.Equal(panes, last))
		s.woken, last = false, panes
		s.mu.Unlock()
		if lost {
			s.wake()
		}
		var changes <-chan string
		if w != nil {
			changes = w.Changes()
		}
		for waiting := true; waiting; {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				waiting = false
			case _, more := <-changes:
				if s.wake(); !more {
					w, changes = nil, nil
				}
			}
		}
	}
}

// listen is the second source of news: the keeper's own line for every
// change it sees. The connection is opened again when it ends.
func (s *server) listen(ctx context.Context) {
	for up := true; ctx.Err() == nil; {
		_, events, err := s.k.Terms.Events(ctx)
		if was := up; (err == nil) != was {
			up = err == nil
			s.wake()
		}
		if err != nil {
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
			}
			continue
		}
		for range events {
			s.wake()
		}
	}
}

// sweeps starts the sweep for every open run of the login, one after
// another, every five seconds for as long as the server runs: it is what
// watches a run while no pane is open and no lead agent waits. The looking
// and the writing are the command's; the server keeps only what it said.
func (s *server) sweeps() {
	tick := time.NewTicker(sweepEvery)
	defer tick.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-tick.C:
		}
		all := s.open()
		for _, j := range all {
			var swept contract.Swept
			if s.read(s.ctx, j, &swept, "", "status", "--sweep", "--quiet") == nil {
				s.mu.Lock()
				s.swept[j.id()] = swept
				s.mu.Unlock()
			}
		}
		s.mu.Lock()
		s.ran = len(all)
		s.mu.Unlock()
	}
}
