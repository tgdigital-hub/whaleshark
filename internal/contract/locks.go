package contract

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Lock is a named lock of a project (M8), one file of LocksDir each, seen by
// every run, every worker and the person: who holds it, for which task of
// which run, since when, for how long before it is stale, and who waits for
// it in turn. Free, Holder is empty and the file stays while somebody waits.
// Told is when lock_stale was raised for this holding, ToldIn the run it was
// raised in and Item the person's to-do of CauseLock there once it climbed;
// they go when the holding ends.
type Lock struct {
	Versioned
	Name    string    `json:"name"`
	Holder  string    `json:"holder,omitempty"`
	Task    string    `json:"task,omitempty"`
	Run     string    `json:"run,omitempty"`
	Since   time.Time `json:"since,omitzero"`
	Stale   int       `json:"stale_seconds"`
	Note    string    `json:"note,omitempty"`
	Waiting []Waiter  `json:"waiting,omitempty"`
	// FreeSince is when it was last let go: the first in line has one stale
	// time from then to take it before the next one may.
	FreeSince time.Time `json:"free_since,omitzero"`
	Told      time.Time `json:"told,omitzero"`
	ToldIn    string    `json:"told_in,omitempty"`
	Item      string    `json:"item,omitempty"`
}

// Waiter is one taker in line for a lock.
type Waiter struct {
	Holder string    `json:"holder"`
	Task   string    `json:"task,omitempty"`
	Run    string    `json:"run,omitempty"`
	Since  time.Time `json:"since"`
}

// The built-in locks and their stale times: pr-per-task merges under merge,
// and deploy is the name a project's deploys share. Any other name is a
// project's own, stale after LockStale unless taken with --stale.
var BuiltinLocks = map[string]time.Duration{"merge": 10 * time.Minute, "deploy": 45 * time.Minute}

const (
	LockStale = 30 * time.Minute
	CauseLock = "lock"
)

// LocksDir is the folder of a project's locks.
func LocksDir(root string) string { return filepath.Join(root, ProjectDir, "locks") }

// HolderOf names who takes a lock: a task of a run, or the person or the
// lead agent of a run where no task is named.
func HolderOf(kind CallerKind, run, task string) string {
	switch {
	case task != "" && run != "":
		return run + "/" + task
	case task != "":
		return task
	case kind == Human:
		return "the person"
	}
	return strings.TrimPrefix(run+" "+string(kind), " ")
}

// Held reports whether somebody holds the lock.
func (l Lock) Held() bool { return l.Holder != "" }

// StaleAt is when the holding turns stale.
func (l Lock) StaleAt() time.Time { return l.Since.Add(time.Duration(l.Stale) * time.Second) }

// Line is the lock as status, the fleet pane and lock list say it.
func (l Lock) Line(now time.Time) string {
	line := "lock " + l.Name + ": free"
	if l.Held() {
		line = "lock " + l.Name + ": " + l.Holder + " since " + l.Since.Local().Format("15:04")
		if !now.Before(l.StaleAt()) {
			line += ", stale since " + l.StaleAt().Local().Format("15:04")
		}
		if l.Note != "" {
			line += " (" + l.Note + ")"
		}
	}
	var in []string
	for _, w := range l.Waiting {
		in = append(in, w.Holder)
	}
	if len(in) > 0 {
		line += "; waiting: " + strings.Join(in, ", ")
	}
	return line
}

// ReadLocks returns every lock of a project that is held or waited for, by
// name; a file that does not read is left out.
func ReadLocks(read func(path string) ([]byte, error), root string) []Lock {
	entries, _ := os.ReadDir(LocksDir(root))
	var out []Lock
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".json"); ok {
			var l Lock
			if ReadVersioned(read, filepath.Join(LocksDir(root), e.Name()), FileVersion, &l) == nil && l.Name == name {
				out = append(out, l)
			}
		}
	}
	slices.SortFunc(out, func(a, b Lock) int { return strings.Compare(a.Name, b.Name) })
	return out
}
