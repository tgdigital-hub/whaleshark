package restore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/layout"
)

const (
	// The file is written this long after output at the latest.
	flow = 3 * time.Second
	// Changes of the layout closer together than this are one write: a
	// dividing line being dragged is a change at every cell.
	gap = 200 * time.Millisecond
)

// ErrUnusable is a layout file that cannot be read as one. It is moved
// aside, and the keeper comes back empty.
var ErrUnusable = errors.New("the layout file could not be used and was moved aside")

// Pane is one pane in the file: its record, the program it was started with
// from its first argument on (none is the shell) and its last lines as text.
// Run is set by Load: what to start in the pane now, none for the shell.
type Pane struct {
	Rec   contract.Pane `json:"pane"`
	Argv  []string      `json:"argv,omitempty"`
	Lines []string      `json:"lines,omitempty"`
	Run   []string      `json:"-"`
}

// File is the layout file, private to the login: the layout as it marshals,
// the size of the window it was last fitted to, each tab's own variables,
// the numbers the last tab and pane were given, and every pane.
type File struct {
	contract.Versioned
	At     time.Time           `json:"at"`
	W      int                 `json:"w"`
	H      int                 `json:"h"`
	NTab   int                 `json:"tabs_made"`
	NPane  int                 `json:"panes_made"`
	Layout json.RawMessage     `json:"layout"`
	Vars   map[string][]string `json:"vars,omitempty"`
	Panes  []Pane              `json:"panes"`
}

const name = "layout.json"

// Path is the layout file of a login.
func Path(d contract.Dirs) string { return filepath.Join(d.State, name) }

// Save replaces the layout file.
func Save(files contract.Files, d contract.Dirs, f *File) error {
	f.Version, f.At = contract.FileVersion, contract.Now()
	return contract.WriteVersioned(files, Path(d), contract.FileVersion, f)
}

// Load reads the layout file back for a keeper that starts: nothing when
// there is none. What it returns is in order and can be used as it is: the
// layout is sound, there is exactly one pane for each place in it, in the
// order of the tabs, each with its tab's id and name and with what to start
// in it now; no number of a tab or pane will be given twice. A file that
// is not sound is moved aside and answered with ErrUnusable; one of a newer
// program is left alone and answered with contract.ErrNewer. Load is for
// the keeper alone, which holds the login's lock: it also removes the
// unfinished files a keeper killed in the middle of a write left behind.
func Load(files contract.Files, d contract.Dirs) (*File, *layout.Layout, error) {
	f, lay := &File{}, &layout.Layout{}
	left, _ := os.ReadDir(d.State)
	for _, e := range left {
		if end, ok := strings.CutPrefix(e.Name(), name+"."); ok && end != "bad" {
			os.Remove(filepath.Join(d.State, e.Name()))
		}
	}
	err := contract.ReadVersioned(files.Read, Path(d), contract.FileVersion, f)
	if errors.Is(err, contract.ErrNewer) {
		return nil, nil, err
	}
	if err == nil && f.Version == 0 {
		return nil, nil, nil
	}
	if err == nil {
		err = json.Unmarshal(f.Layout, lay)
	}
	if err != nil || !lay.Sound() {
		// Left where it is, it would be in the way of every later save.
		if merr := files.Replace(Path(d), Path(d)+".bad"); merr != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrUnusable, merr)
		}
		return nil, nil, ErrUnusable
	}
	saved, vars := map[string]Pane{}, map[string][]string{}
	for _, p := range f.Panes {
		saved[p.Rec.ID] = p
	}
	f.Panes = nil
	for _, t := range lay.Tabs {
		f.NTab = max(f.NTab, number(t.ID))
		if v, ok := f.Vars[t.ID]; ok {
			vars[t.ID] = v
		}
		for _, id := range t.Panes() {
			p := saved[id]
			p.Rec = contract.Pane{ID: id, Tab: t.ID, Label: t.Label, Cwd: p.Rec.Cwd, Agent: p.Rec.Agent,
				Name: p.Rec.Name, Session: p.Rec.Session, Status: contract.StatusUnknown}
			p.plan()
			f.Panes, f.NPane = append(f.Panes, p), max(f.NPane, number(id))
		}
	}
	f.Vars = vars
	lay.Fit(f.W, f.H)
	return f, lay, nil
}

// number is the number in an id of ours, "t3" or "p7".
func number(id string) int {
	n, _ := strconv.Atoi(id[1:])
	return n
}

// Saver writes the layout file for a running keeper, one write at a time:
// at once when the layout changed, and a few seconds after output.
type Saver struct {
	save   func() error
	now    chan struct{}
	quit   chan struct{}
	done   chan struct{}
	flowed atomic.Bool
	mu     sync.Mutex
	err    error
}

// Start begins saving. pack gives the file as it is at that moment; the
// keeper takes its own lock in it, and nothing of the Saver is called under
// that lock but Changed and Flowed.
func Start(files contract.Files, d contract.Dirs, pack func() *File) *Saver {
	s := &Saver{save: func() error { return Save(files, d, pack()) },
		now: make(chan struct{}, 1), quit: make(chan struct{}), done: make(chan struct{})}
	go s.run()
	return s
}

func (s *Saver) run() {
	defer close(s.done)
	tick := time.NewTicker(flow)
	defer tick.Stop()
	for last, end := (time.Time{}), false; !end; {
		select {
		case <-s.now:
			time.Sleep(time.Until(last.Add(gap)))
		case <-tick.C:
			if !s.flowed.Swap(false) {
				continue
			}
		case <-s.quit:
			end = true
		}
		err := s.save()
		last = time.Now()
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
	}
}

// Changed says the layout changed: a tab, a pane, a split, a name, the
// focus, a program, an agent's session.
func (s *Saver) Changed() {
	select {
	case s.now <- struct{}{}:
	default:
	}
}

// Flowed says a pane printed something.
func (s *Saver) Flowed() { s.flowed.Store(true) }

// Err is what the last write failed with, if it did.
func (s *Saver) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Stop writes the file a last time and ends the saving. A keeper that
// stops calls it while its panes are still there, so that they come back.
func (s *Saver) Stop() {
	close(s.quit)
	<-s.done
}
