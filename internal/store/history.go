package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// version holds the history to the version of the record beside it, the one
// version a run has: the lines of a run this program is too old for are
// neither read nor added to. A run with no record is no run.
func version(dir string) error {
	err := contract.ReadVersioned(filepath.Join(dir, stateFile), contract.StateVersion, new(contract.Versioned))
	if _, missing := os.Stat(filepath.Join(dir, stateFile)); err == nil && missing != nil {
		return contract.ErrNoRun
	}
	return err
}

// History reads history.jsonl, one event to a line. A last line with no end
// is one a writer was cut off in, and is left out.
func (st *Store) History(root, run string) (events []contract.Event, err error) {
	err = st.locked(root, run, false, func(dir string) error {
		if err := version(dir); err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(dir, historyFile))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		for n := 1; err == nil; n++ {
			line, rest, whole := bytes.Cut(data, []byte{'\n'})
			if !whole {
				break
			}
			var e contract.Event
			if err = json.Unmarshal(line, &e); err != nil {
				err = fmt.Errorf("%s of run %s, line %d: %w", historyFile, run, n, err)
			}
			events, data = append(events, e), rest
		}
		return err
	})
	if err != nil {
		events = nil
	}
	return events, err
}

// Append adds events to the end of the history and flushes them to disk,
// cutting a torn last line off first.
func (st *Store) Append(root, run string, events []contract.Event) error {
	var lines bytes.Buffer
	for _, e := range events {
		if err := json.NewEncoder(&lines).Encode(e); err != nil {
			return err
		}
	}
	if len(events) == 0 {
		return nil
	}
	return st.locked(root, run, true, func(dir string) error {
		if err := version(dir); err != nil {
			return refused(err)
		}
		path := filepath.Join(dir, historyFile)
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		end, last := info.Size(), []byte{'\n'}
		if end > 0 {
			if _, err := f.ReadAt(last, end-1); err != nil {
				return err
			}
		}
		if last[0] != '\n' {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			end = int64(bytes.LastIndexByte(data, '\n') + 1)
			if err := f.Truncate(end); err != nil {
				return err
			}
		}
		if _, err = f.WriteAt(lines.Bytes(), end); err == nil {
			err = f.Sync()
		}
		if end == 0 {
			flush(dir)
		}
		return err
	})
}
