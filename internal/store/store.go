package store

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const (
	ours        = ".whaleshark"
	lockFile    = "lock"
	turnFile    = "turn"
	currentFile = "current"
	runsDir     = "runs"
	stateFile   = "state.json"
	historyFile = "history.jsonl"
	gone        = ".gone-" // before a removed run's id; no id starts with a dot
)

// Store is the contract's Store on the files of 6.1.
type Store struct{ p contract.Platform }

// pointer is the file "current".
type pointer struct {
	contract.Versioned
	Run string `json:"run"`
}

func (st *Store) Dir(root, run string) string {
	if run == "" {
		return filepath.Join(root, ours)
	}
	return filepath.Join(root, ours, runsDir, run)
}

// locked runs fn on a run's folder, or on the project's own when run is
// empty, under the project's one lock. It makes no folder: a project that
// has none of ours has no run. Everybody waits for the lock holding the
// turn, a second lock that one process has at a time: a writer that waits
// for the readers inside to leave keeps every new reader out meanwhile, so
// readers that never pause cannot hold a writer off.
func (st *Store) locked(root, run string, exclusive bool, fn func(dir string) error) error {
	if run != "" {
		if err := cli.Valid("id", run, ""); err != nil {
			return err
		}
	}
	turn, err := st.p.Lock(filepath.Join(st.Dir(root, ""), turnFile), true)
	if errors.Is(err, fs.ErrNotExist) {
		return contract.ErrNoRun
	}
	if err != nil {
		return err
	}
	unlock, err := st.p.Lock(filepath.Join(st.Dir(root, ""), lockFile), exclusive)
	turn()
	if err != nil {
		return err
	}
	defer unlock()
	return fn(st.Dir(root, run))
}

// load reads a run's record and passes it through the validator again. data
// is the file as it stands.
func (st *Store) load(dir, run string) (*contract.State, []byte, error) {
	data, err := st.p.Peek(filepath.Join(dir, stateFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, contract.ErrNoRun
	}
	if err != nil {
		return nil, nil, err
	}
	s := new(contract.State)
	err = json.Unmarshal(data, s)
	// The version is asked first: a newer shape may not even decode.
	if s.Version > contract.StateVersion {
		return nil, nil, contract.ErrNewer
	}
	if err == nil {
		err = valid(s, run)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("%s of run %s: %w", stateFile, run, err)
	}
	return s, data, nil
}

// refused is how a command that writes ends on a newer file (6.1).
func refused(err error) error {
	if errors.Is(err, contract.ErrNewer) {
		return &contract.Refusal{Exit: contract.ExitEnv, Code: "newer_state",
			Message: "The record was written by a newer whaleshark, and this one will not change it."}
	}
	return err
}

// flush makes the names in a folder last through a power cut. Its error is
// dropped: the change already stands for every reader, and Windows has no
// such flush.
func flush(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}

// write replaces a file under the exclusive lock: a finished and flushed
// file beside it is moved over it, so a reader finds the old or the new one.
// One name serves every writer, so a killed one leaves nothing that stays.
func (st *Store) write(dir, name string, data []byte) error {
	tmp := filepath.Join(dir, name+".tmp")
	defer os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = st.p.Replace(tmp, filepath.Join(dir, name))
	}
	if err == nil {
		flush(dir)
	}
	return err
}

// save writes a record unless it is, byte for byte, the file as it stands.
func (st *Store) save(dir string, s *contract.State, old []byte) error {
	if err := valid(s, s.Run.ID); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", " ")
	if data = append(data, '\n'); err != nil || bytes.Equal(data, old) {
		return err
	}
	return st.write(dir, stateFile, data)
}

func (st *Store) Read(root, run string) (s *contract.State, err error) {
	err = st.locked(root, run, false, func(dir string) error {
		s, _, err = st.load(dir, run)
		return err
	})
	return s, err
}

func (st *Store) Create(root string, s *contract.State) error {
	run := s.Run.ID
	if err := valid(s, run); err != nil {
		return err
	}
	base := st.Dir(root, "")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return err
	}
	return st.locked(root, run, true, func(dir string) error {
		if _, err := os.Stat(filepath.Join(dir, stateFile)); err == nil {
			return fmt.Errorf("run %s exists already", run)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		s.Version = contract.StateVersion
		err := st.save(dir, s, nil)
		flush(filepath.Dir(dir))
		flush(base)
		return err
	})
}

func (st *Store) Change(root, run string, fn func(*contract.State) error) error {
	return st.locked(root, run, true, func(dir string) error {
		s, old, err := st.load(dir, run)
		if err != nil {
			return refused(err)
		}
		if err := fn(s); err != nil {
			return err
		}
		if s.Version < contract.StateVersion {
			// The first write over an older file leaves that file beside it.
			if err := st.write(dir, fmt.Sprintf("state.v%d.json", s.Version), old); err != nil {
				return err
			}
			s.Version = contract.StateVersion
		}
		return st.save(dir, s, old)
	})
}

// Remove takes the folder out of the runs by one rename, so a removal that
// is cut off never leaves half a run under its id.
func (st *Store) Remove(root, run string) error {
	return st.locked(root, run, true, func(dir string) error {
		s, _, err := st.load(dir, run)
		if err != nil {
			return refused(err)
		}
		if s.Run.ClosedAt.IsZero() {
			return fmt.Errorf("run %s is still open", run)
		}
		// What an earlier removal was cut off in, of any run, goes first.
		away := filepath.Join(filepath.Dir(dir), gone+run)
		left, _ := filepath.Glob(filepath.Join(filepath.Dir(dir), gone+"*"))
		for _, old := range append(left, away) {
			if err := os.RemoveAll(old); err != nil {
				return err
			}
		}
		if err := st.p.Replace(dir, away); err != nil {
			return err
		}
		flush(filepath.Dir(dir))
		return os.RemoveAll(away)
	})
}

// Runs counts a folder as a run once it holds a record. Ids are counted, so
// the shorter is the older, and of two as long the one that sorts first.
func (st *Store) Runs(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(st.Dir(root, ""), runsDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var ids []string
	for _, e := range entries {
		if cli.Valid("id", e.Name(), "") != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(st.Dir(root, e.Name()), stateFile)); err == nil {
			ids = append(ids, e.Name())
		}
	}
	slices.SortFunc(ids, func(a, b string) int { return cmp.Or(cmp.Compare(len(a), len(b)), cmp.Compare(a, b)) })
	return ids, err
}

// Current answers nothing when the pointer is newer than this program or
// names a run that is no longer there.
func (st *Store) Current(root string) (string, error) {
	var c pointer
	err := contract.ReadVersioned(st.p.Peek, filepath.Join(st.Dir(root, ""), currentFile), contract.FileVersion, &c)
	if errors.Is(err, contract.ErrNewer) || err == nil && c.Run == "" {
		return "", nil
	}
	if err == nil {
		err = cli.Valid("id", c.Run, "")
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", currentFile, err)
	}
	if _, err := os.Stat(filepath.Join(st.Dir(root, c.Run), stateFile)); err != nil {
		return "", nil
	}
	return c.Run, nil
}

// SetCurrent points at a run, or at none when run is empty.
func (st *Store) SetCurrent(root, run string) error {
	base := st.Dir(root, "")
	return st.locked(root, run, true, func(dir string) error {
		if _, err := os.Stat(filepath.Join(dir, stateFile)); run != "" && err != nil {
			return contract.ErrNoRun
		}
		// A pointer from a newer program is not written over; the file is
		// written under the one name the store's writers share, so a writer
		// killed here leaves nothing that stays.
		if err := contract.ReadVersioned(st.p.Peek, filepath.Join(base, currentFile), contract.FileVersion, new(contract.Versioned)); err != nil {
			return refused(err)
		}
		data, err := json.MarshalIndent(pointer{contract.Versioned{Version: contract.FileVersion}, run}, "", " ")
		if err != nil {
			return err
		}
		return st.write(base, currentFile, append(data, '\n'))
	})
}
