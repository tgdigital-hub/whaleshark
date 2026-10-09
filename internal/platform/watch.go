package platform

import (
	"bytes"
	"encoding/binary"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"
)

// notifier is the system's own word that something changed. What it sends is
// a path to look at: a file, or a folder when it cannot say which file.
type notifier interface {
	// add starts the notices of a folder, in place of any it had.
	add(dir string) error
	// file is told of each file as it appears or is replaced, for the system
	// whose folder notices leave out a change made inside a file.
	file(path string)
	// exact: a file is named only when it changed, so the notice is believed
	// even where a look cannot tell the difference.
	exact() bool
	events() <-chan string
	close()
}

// silent is the notifier of a system that gave none.
type silent struct{}

func (silent) add(string) error      { return fs.ErrInvalid }
func (silent) file(string)           {}
func (silent) events() <-chan string { return nil }
func (silent) close()                {}
func (silent) exact() bool           { return false }

// mute is a system whose notices start without complaint and never come:
// what a watcher is given when the notices are switched off for a test.
type mute struct{ silent }

func (mute) add(string) error { return nil }

const checkEvery, slowEvery = time.Second, time.Second / 4

// folder is one folder under notice. info is the folder as it was when its
// notices began, nil while it has none; whole means every entry is watched,
// not only the files that were named.
type folder struct {
	info  fs.FileInfo
	whole bool
}

type watcher struct {
	n     notifier
	out   chan string
	done  chan struct{}
	once  sync.Once
	slow  atomic.Bool
	dirs  map[string]*folder
	files map[string]fs.FileInfo // each watched file as last seen; nil when absent
	// unheard are changes the last check found where a notice was due.
	unheard map[string]bool
}

// watch reports every change to the named files and to the entries of the
// named folders. Notices say when to look; a look once a second checks the
// notices, and takes their place four times a second once one has failed.
func watch(n notifier, paths []string) *watcher {
	w := &watcher{n: n, out: make(chan string), done: make(chan struct{}),
		dirs: map[string]*folder{}, files: map[string]fs.FileInfo{}, unheard: map[string]bool{}}
	for _, p := range paths {
		p = filepath.Clean(p)
		d := filepath.Dir(p)
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			d = p
		} else {
			w.files[p] = nil
		}
		if w.dirs[d] == nil {
			w.dirs[d] = &folder{}
		}
		w.dirs[d].whole = w.dirs[d].whole || d == p
	}
	for d, f := range w.dirs {
		w.attach(d, f)
		for _, p := range w.entries(d, f) {
			w.see(p)
		}
	}
	go w.run()
	return w
}

func (w *watcher) Changes() <-chan string { return w.out }
func (w *watcher) Slow() bool             { return w.slow.Load() }

func (w *watcher) Close() error {
	w.once.Do(func() { close(w.done) })
	return nil
}

func (w *watcher) run() {
	defer close(w.out)
	defer w.n.close()
	tick := time.NewTicker(checkEvery)
	defer tick.Stop()
	for {
		select {
		case <-w.done:
			return
		case p := <-w.n.events():
			if f := w.dirs[p]; f != nil {
				for q := range w.unheard {
					if filepath.Dir(q) == p {
						delete(w.unheard, q)
					}
				}
				w.scan(p, f)
			} else if f := w.dirs[filepath.Dir(p)]; f != nil {
				delete(w.unheard, p)
				if _, named := w.files[p]; (named || f.whole) && (w.see(p) || w.n.exact()) {
					w.tell(p)
				}
			}
		case <-tick.C:
			if len(w.unheard) > 0 {
				w.slow.Store(true)
				clear(w.unheard)
			}
			for d, f := range w.dirs {
				trusted := !w.attach(d, f) && f.info != nil && !w.slow.Load()
				for _, p := range w.scan(d, f) {
					if trusted {
						w.unheard[p] = true
					}
				}
			}
			// A change found here gets a quarter of a second for its notice to arrive.
			if w.slow.Load() || len(w.unheard) > 0 {
				tick.Reset(slowEvery)
			} else {
				tick.Reset(checkEvery)
			}
		}
	}
}

// attach gives a folder its notices when it has none, or when the folder is
// no longer the one they were started on. It reports whether it had to.
func (w *watcher) attach(d string, f *folder) bool {
	info, err := os.Stat(d)
	if err == nil && f.info != nil && os.SameFile(info, f.info) {
		return false
	}
	f.info = nil
	if err == nil && w.n.add(d) == nil {
		f.info = info
	}
	return true
}

// entries are the watched files of a folder: the named ones, and for a whole
// folder everything that is in it now.
func (w *watcher) entries(d string, f *folder) []string {
	var list []string
	for p := range w.files {
		if filepath.Dir(p) == d {
			list = append(list, p)
		}
	}
	if f.whole {
		names, _ := os.ReadDir(d)
		for _, e := range names {
			if _, named := w.files[filepath.Join(d, e.Name())]; !named {
				list = append(list, filepath.Join(d, e.Name()))
			}
		}
	}
	return list
}

// scan looks at every watched file of a folder and tells of those that changed.
func (w *watcher) scan(d string, f *folder) []string {
	var changed []string
	for _, p := range w.entries(d, f) {
		if w.see(p) {
			changed = append(changed, p)
			w.tell(p)
		}
	}
	return changed
}

// see looks at one file, remembers it, and reports whether it differs from
// the last look: there or not, another file under the name, time or size.
func (w *watcher) see(p string) bool {
	old := w.files[p]
	now, _ := os.Stat(p)
	replaced := (old == nil) != (now == nil) || now != nil && !os.SameFile(old, now)
	if replaced {
		w.n.file(p)
		now, _ = os.Stat(p) // again: a write before the file had its own notices is seen here
	}
	parent := w.dirs[filepath.Dir(p)]
	if now == nil && parent.whole {
		delete(w.files, p)
	} else {
		w.files[p] = now
	}
	if now == nil || now.IsDir() {
		// A folder's own time says nothing here. One that was named before it
		// was there is watched whole from the next check on.
		if now != nil && !parent.whole && w.dirs[p] == nil {
			w.dirs[p] = &folder{whole: true}
		}
		return replaced
	}
	return replaced || !now.ModTime().Equal(old.ModTime()) || now.Size() != old.Size()
}

func (w *watcher) tell(p string) {
	select {
	case w.out <- p:
	case <-w.done:
	}
}

// inotifyEvents reads what Linux wrote about its watches: for each event the
// watch, what happened, and the name of the entry, empty for the folder itself.
func inotifyEvents(buf []byte, each func(watch int32, mask uint32, name string)) {
	for len(buf) >= 16 {
		n := 16 + int(binary.NativeEndian.Uint32(buf[12:]))
		if n > len(buf) {
			return
		}
		name := bytes.TrimRight(buf[16:n], "\x00")
		each(int32(binary.NativeEndian.Uint32(buf)), binary.NativeEndian.Uint32(buf[4:]), string(name))
		buf = buf[n:]
	}
}

// dirChangeNames reads the names out of what Windows wrote about a folder.
func dirChangeNames(buf []byte) []string {
	var names []string
	for len(buf) >= 12 {
		next, size := int(binary.LittleEndian.Uint32(buf)), int(binary.LittleEndian.Uint32(buf[8:]))
		if 12+size > len(buf) {
			break
		}
		name := make([]uint16, size/2)
		for i := range name {
			name[i] = binary.LittleEndian.Uint16(buf[12+2*i:])
		}
		names = append(names, string(utf16.Decode(name)))
		if next == 0 || next > len(buf) {
			break
		}
		buf = buf[next:]
	}
	return names
}
