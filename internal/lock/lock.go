// Package lock is a project's named locks (M8): one file each in
// contract.LocksDir, changed only under the folder's own file lock, so that
// two takers are put in line and never both let through. Nothing here waits
// for a lock: a taker of a held one is refused with who holds it and is
// written in line, and the sweep tells the lead agent of a lock past its
// stale time. integrate binds the command and merges under the merge lock.
package lock

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Want is a taking: the lock's name, who takes it, for what, and for how
// long before it is stale (zero: the built-in time, else contract.LockStale).
type Want struct {
	Name, Holder, Task, Run, Note string
	Stale                         time.Duration
}

// The refusals of a lock, with the lock as it stands in their Data.
const (
	RefusedHeld     = "lock_held"
	RefusedNotYours = "lock_not_yours"
	RefusedNotStale = "lock_not_stale"
	RefusedNoLock   = "no_lock"
)

// change runs fn on one lock under the folder's file lock and writes what
// it leaves: a lock that nobody holds or waits for is removed.
func change(p contract.Platform, root, name string, fn func(l *contract.Lock) error) (contract.Lock, error) {
	dir := contract.LocksDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return contract.Lock{}, err
	}
	unlock, err := p.Lock(filepath.Join(dir, "lock"), true)
	if err != nil {
		return contract.Lock{}, err
	}
	defer unlock()
	file := filepath.Join(dir, name+".json")
	l := contract.Lock{Name: name}
	if err := contract.ReadVersioned(p.Read, file, contract.FileVersion, &l); err != nil {
		return l, err
	}
	was := l
	if err := fn(&l); err != nil {
		return was, err
	}
	if !l.Held() && len(l.Waiting) == 0 {
		if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
			return l, err
		}
		return l, nil
	}
	l.Version = contract.FileVersion
	return l, contract.WriteVersioned(p, file, contract.FileVersion, l)
}

func refuse(exit int, code, message string, l contract.Lock, next ...string) *contract.Refusal {
	return &contract.Refusal{Exit: exit, Code: code, Message: message, Next: next, Data: l}
}

// Take gives the lock to w where nobody holds it and nobody is before w in
// line, else writes w in line and refuses with who holds it. Whoever holds
// it already is given it again, unchanged. The first in line has one stale
// time from the lock's letting go to take it; after that anybody in line may.
func Take(p contract.Platform, root string, w Want, now time.Time) (contract.Lock, error) {
	var held error // the line is written, and then the taker refused
	l, err := change(p, root, w.Name, func(l *contract.Lock) error {
		if l.Holder == w.Holder {
			return nil
		}
		stale := cmp.Or(w.Stale, contract.BuiltinLocks[w.Name], contract.LockStale)
		at := slices.IndexFunc(l.Waiting, func(x contract.Waiter) bool { return x.Holder == w.Holder })
		turn := len(l.Waiting) == 0 || at == 0 || now.Sub(l.FreeSince) >= time.Duration(l.Stale)*time.Second
		if !l.Held() && turn {
			if at >= 0 {
				l.Waiting = slices.Delete(l.Waiting, at, at+1)
			}
			l.Holder, l.Task, l.Run, l.Note, l.Since, l.Stale = w.Holder, w.Task, w.Run, w.Note, now, int(stale/time.Second)
			l.FreeSince, l.Told, l.ToldIn, l.Item = time.Time{}, time.Time{}, "", ""
			return nil
		}
		if at < 0 {
			l.Waiting, at = append(l.Waiting, contract.Waiter{Holder: w.Holder, Task: w.Task, Run: w.Run, Since: now}), len(l.Waiting)
		}
		who := "nobody holds it, and " + l.Waiting[0].Holder + " is first in line"
		if l.Held() {
			who = fmt.Sprintf("%s holds it since %s, stale at %s", l.Holder, clock(l.Since), clock(l.StaleAt()))
		}
		held = refuse(contract.ExitRefused, RefusedHeld, fmt.Sprintf("The lock %s is not yours yet: %s. You are number %d in line; take it again once it is let go.", w.Name, who, at+1),
			*l, "whaleshark lock list")
		return nil
	})
	return l, cmp.Or(err, held)
}

// Release lets the lock go, or takes holder out of its line. Only the holder
// may let it go, but force, which is the person's, and a lead agent's for a
// task of its own run.
func Release(p contract.Platform, root, name, holder, run string, force bool, now time.Time) (contract.Lock, error) {
	return change(p, root, name, func(l *contract.Lock) error {
		if at := slices.IndexFunc(l.Waiting, func(x contract.Waiter) bool { return x.Holder == holder }); at >= 0 && l.Holder != holder {
			l.Waiting = slices.Delete(l.Waiting, at, at+1)
			return nil
		}
		switch {
		case !l.Held():
			return refuse(contract.ExitMissing, RefusedNoLock, "Nobody holds the lock "+name+".", *l, "whaleshark lock list")
		case l.Holder != holder && !force && (run == "" || l.Run != run):
			return refuse(contract.ExitRefused, RefusedNotYours, "The lock "+name+" is held by "+l.Holder+", not by you.", *l, "whaleshark lock list")
		}
		free(l, now)
		return nil
	})
}

// Steal takes a held lock from its holder for w: the person's at any time,
// and a lead agent's once lock_stale was raised for this holding.
func Steal(p contract.Platform, root string, w Want, person bool, now time.Time) (was contract.Lock, err error) {
	_, err = change(p, root, w.Name, func(l *contract.Lock) error {
		switch was = *l; {
		case !l.Held():
			return refuse(contract.ExitMissing, RefusedNoLock, "Nobody holds the lock "+w.Name+": take it.", *l, "whaleshark lock take "+w.Name)
		case !person && l.Told.IsZero():
			return refuse(contract.ExitRefused, RefusedNotStale, fmt.Sprintf("The lock %s is held by %s since %s and is not stale before %s: only the person steals it before.", w.Name, l.Holder, clock(l.Since), clock(l.StaleAt())), *l)
		}
		free(l, now)
		l.FreeSince = time.Time{}
		l.Holder, l.Task, l.Run, l.Note, l.Since = w.Holder, w.Task, w.Run, w.Note, now
		l.Waiting = slices.DeleteFunc(l.Waiting, func(x contract.Waiter) bool { return x.Holder == w.Holder })
		return nil
	})
	return was, err
}

// free ends a holding; the line stays.
func free(l *contract.Lock, now time.Time) {
	l.Holder, l.Task, l.Run, l.Note, l.Since = "", "", "", "", time.Time{}
	l.FreeSince, l.Told, l.ToldIn, l.Item = now, time.Time{}, "", ""
}

// Due is the sweep of one run: each lock of that run, or of none, past its
// stale time and not yet told is handed to tell, and each one told by this
// run one more stale time ago and still held to climb, which answers the
// person's item it opened. What they did is written into the lock.
func Due(p contract.Platform, root, run string, now time.Time, tell func(contract.Lock) bool, climb func(contract.Lock) string) {
	for _, l := range contract.ReadLocks(p.Peek, root) {
		told := l.Told.IsZero() && (l.Run == run || l.Run == "")
		climbs := l.ToldIn == run && l.Item == "" && now.Sub(l.Told) >= time.Duration(l.Stale)*time.Second
		if !l.Held() || now.Before(l.StaleAt()) || !told && !climbs {
			continue
		}
		change(p, root, l.Name, func(at *contract.Lock) error {
			switch {
			case at.Holder != l.Holder || !at.Since.Equal(l.Since):
			case at.Told.IsZero():
				if tell(*at) {
					at.Told, at.ToldIn = now, run
				}
			case at.Item == "" && at.ToldIn == run:
				at.Item = climb(*at)
			}
			return nil
		})
	}
}

func clock(t time.Time) string { return t.Local().Format("15:04") }
