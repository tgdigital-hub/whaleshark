package restore

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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

// Pane is one pane in the file: its record and the program it was started
// with from its first argument on (none is the shell). Its last lines are
// in a file of their own, named after the pane: they change with every
// line printed, the layout with every tab shown. A running keeper gives
// Count, the bytes its program has printed, and Text, which reads the lines
// and is called with no lock of the keeper's held. Load sets Lines, from
// that file or from a layout file of before there was one, and Run: what
// to start in the pane now, none for the shell.
type Pane struct {
	Rec   contract.Pane   `json:"pane"`
	Argv  []string        `json:"argv,omitempty"`
	Lines []string        `json:"lines,omitempty"`
	Run   []string        `json:"-"`
	Count uint64          `json:"-"`
	Text  func() []string `json:"-"`
}

// old is the file of one pane's last lines: the text as gzip packs it,
// which is a third of it and less.
type old struct {
	contract.Versioned
	Text []byte `json:"text"`
}

// unread writes a file without reading the one that is there first: the
// keeper holds the login's lock, and Load saw that no file is a newer one.
type unread struct{ contract.Files }

func (unread) Read(string) ([]byte, error) { return nil, fs.ErrNotExist }

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
// unfinished files a keeper killed in the middle of a write left behind,
// and the lines of panes that are gone.
func Load(files contract.Files, d contract.Dirs) (*File, *layout.Layout, error) {
	f, lay := &File{}, &layout.Layout{}
	beside := map[string]bool{}
	left, _ := os.ReadDir(d.State)
	for _, e := range left {
		if end, ok := strings.CutPrefix(e.Name(), name+"."); ok && end != "bad" {
			beside[end] = true
		}
	}
	defer func() {
		for end := range beside {
			os.Remove(Path(d) + "." + end)
		}
	}()
	err := contract.ReadVersioned(files.Read, Path(d), contract.FileVersion, f)
	if errors.Is(err, contract.ErrNewer) {
		clear(beside)
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
		if merr := files.Replace(Path(d), filepath.Join(filepath.Dir(Path(d)), contract.LayoutBad)); merr != nil {
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
			// Only a file that is there: on Windows a missing one is waited for.
			if o := (old{}); beside[id] && contract.ReadVersioned(files.Read, Path(d)+"."+id, contract.FileVersion, &o) == nil {
				if z, err := gzip.NewReader(bytes.NewReader(o.Text)); err == nil {
					text, _ := io.ReadAll(io.LimitReader(z, most))
					p.Lines = strings.Split(string(text), "\n")
				}
				delete(beside, id)
			}
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
// at once when the layout changed, and a few seconds after output. Only
// then, and at the stop, are panes' lines written, each pane's when its
// program has printed since they last were. A write that failed is made
// again a few seconds later.
type Saver struct {
	files  contract.Files
	dirs   contract.Dirs
	pack   func() *File
	wrote  map[string]string // the terminal and its count of bytes a pane's lines were written at
	now    chan struct{}
	quit   chan struct{}
	done   chan struct{}
	flowed atomic.Bool
	mu     sync.Mutex
	err    error
}

// Start begins saving. pack gives the file as it is at that moment, with
// no pane's lines read; the keeper takes its own lock in it, and nothing of
// the Saver is called under that lock but Changed and Flowed.
func Start(files contract.Files, d contract.Dirs, pack func() *File) *Saver {
	s := &Saver{files: unread{files}, dirs: d, pack: pack, wrote: map[string]string{},
		now: make(chan struct{}, 1), quit: make(chan struct{}), done: make(chan struct{})}
	go s.run()
	return s
}

// save writes the layout file, and with lines the lines of every pane that
// printed since its own were written; those of a pane that is gone go.
func (s *Saver) save(lines bool) error {
	f := s.pack()
	err := Save(s.files, s.dirs, f)
	if !lines {
		return err
	}
	live := map[string]bool{}
	for _, p := range f.Panes {
		id, at := p.Rec.ID, p.Rec.Terminal+" "+strconv.FormatUint(p.Count, 10)
		if live[id] = true; p.Text == nil || s.wrote[id] == at {
			continue
		}
		var b bytes.Buffer
		z, _ := gzip.NewWriterLevel(&b, gzip.BestSpeed)
		io.WriteString(z, strings.Join(p.Text(), "\n"))
		z.Close()
		text := &old{contract.Versioned{Version: contract.FileVersion}, b.Bytes()}
		if werr := contract.WriteVersioned(s.files, Path(s.dirs)+"."+id, contract.FileVersion, text); werr != nil {
			err = werr
		} else {
			s.wrote[id] = at
		}
	}
	for id := range s.wrote {
		if !live[id] {
			os.Remove(Path(s.dirs) + "." + id)
			delete(s.wrote, id)
		}
	}
	return err
}

func (s *Saver) run() {
	defer close(s.done)
	tick := time.NewTicker(flow)
	defer tick.Stop()
	for last, end, lines := (time.Time{}), false, false; !end; {
		select {
		case <-s.now:
			time.Sleep(time.Until(last.Add(gap)))
			lines = false
		case <-tick.C:
			if lines = s.flowed.Swap(false); !lines {
				continue
			}
		case <-s.quit:
			end, lines = true, true
		}
		err := s.save(lines)
		last = time.Now()
		if err != nil {
			// Nothing may change again for hours: what was not written is
			// tried again in a few seconds, not at the next change.
			s.flowed.Store(true)
		}
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
