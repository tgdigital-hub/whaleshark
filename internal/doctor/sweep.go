package doctor

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// logDays is how many days of the command log are kept.
const logDays = 30

// slots finds the port slots held for a task that is gone: its run was
// removed or closed, or the run no longer has the task. A slot of a folder
// whose records were not all read is never judged.
func (e *exam) slots() {
	p := e.k.Platform
	gone := func(s contract.Slot) bool {
		if !e.whole[s.Root] {
			return false
		}
		for _, st := range e.runs[s.Root] {
			if st.Run.ID == s.Run {
				return !st.Run.ClosedAt.IsZero() || st.Tasks[s.Task] == nil
			}
		}
		return true
	}
	taken, err := contract.ReadSlots(p.Peek, e.dirs.State)
	if err != nil {
		e.say(problem, "", "the list of port slots cannot be read: %v", err)
		return
	}
	var names []string
	for _, s := range taken {
		if gone(s) {
			names = append(names, s.Run+" "+s.Task)
		}
	}
	if len(taken) == 0 {
		e.say(ok, "", "no port slot is in use")
		return
	} else if len(names) == 0 {
		e.say(ok, "", "%s in use, none for a task that is gone", count(len(taken), "port slot"))
		return
	}
	e.mend(problem, count(len(names), "port slot")+" held for a task that is gone ("+list(names)+")", "given back", func() error {
		_, err := contract.FreeSlots(p, gone)
		return err
	})
}

// strays lists what a writer that was killed left beside the small files
// of ours in a folder: a finished file is moved over its name from one
// called <file>.<digits>, and one older than a minute has no writer.
func strays(dir string, now time.Time) (found []string) {
	entries, _ := os.ReadDir(dir)
	for _, d := range entries {
		dot := strings.LastIndexByte(d.Name(), '.')
		if dot < 0 || !strings.HasSuffix(d.Name()[:dot], ".json") || strings.Trim(d.Name()[dot:], "0123456789") != "." {
			continue
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() && now.Sub(info.ModTime()) > time.Minute {
			found = append(found, filepath.Join(dir, d.Name()))
		}
	}
	return found
}

// remove deletes files and says of all of them what went wrong.
func remove(paths []string) (err error) {
	for _, path := range paths {
		err = errors.Join(err, os.Remove(path))
	}
	return err
}

// leftovers are what nothing else ever removes: unfinished files of killed
// writers, the command log's old days, and the half-typed answers to items
// that are gone.
func (e *exam) leftovers() {
	p, state := e.k.Platform, e.dirs.State
	left := append(strays(state, e.c.Now), strays(filepath.Join(state, "ctx"), e.c.Now)...)
	if ours := e.k.Store.Dir(e.c.Root, ""); ours != "" {
		left = append(left, strays(ours, e.c.Now)...)
	}
	if len(left) > 0 {
		e.mend(problem, count(len(left), "unfinished file")+" of a writer that was killed", "deleted", func() error { return remove(left) })
	}

	var old []string
	days, _ := os.ReadDir(filepath.Join(state, "log"))
	for _, d := range days {
		day, err := time.Parse(time.DateOnly, strings.TrimSuffix(d.Name(), ".log"))
		if err == nil && e.c.Now.Sub(day) > (logDays+1)*24*time.Hour {
			old = append(old, filepath.Join(state, "log", d.Name()))
		}
	}
	if len(old) > 0 {
		e.mend(note, fmt.Sprintf("the command log holds %s older than %d days", count(len(old), "day"), logDays), "deleted", func() error { return remove(old) })
	}

	file, ui, open := filepath.Join(state, contract.UIFileName), contract.UIFile{}, map[string]bool{}
	for key, runs := range e.runs {
		if !e.whole[key] {
			return // an answer may belong to a record that was not read
		}
		for _, s := range runs {
			for id, q := range s.Questions {
				if s.Run.ClosedAt.IsZero() && (q.State == contract.QuestionOpen || q.State == contract.QuestionAnswered) {
					open[id] = true
				}
			}
		}
	}
	if contract.ReadVersioned(p.Read, file, contract.FileVersion, &ui) != nil {
		return
	}
	gone := slices.DeleteFunc(slices.Sorted(maps.Keys(ui.Drafts)), func(id string) bool { return open[id] })
	if len(gone) > 0 {
		e.mend(note, count(len(gone), "half-typed answer")+" to an item that is gone ("+list(gone)+")", "dropped", func() error {
			for _, id := range gone {
				delete(ui.Drafts, id)
			}
			return contract.WriteVersioned(p, file, contract.FileVersion, ui)
		})
	}
}
