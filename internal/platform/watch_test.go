package platform

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"
)

var here = New(runtime.GOOS)

// replaceFile changes a file as a command does: a new file put in its place
// by Replace, which waits where a scanner or a reader holds the old one.
func replaceFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path+".new", []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := here.Replace(path+".new", path); err != nil {
		t.Fatal(err)
	}
}

func real(t *testing.T, paths ...string) *watcher {
	t.Helper()
	n, err := newNotifier()
	if err != nil {
		t.Fatal(err)
	}
	w := watch(n, paths)
	t.Cleanup(func() { w.Close() })
	return w
}

// told waits until the watcher has told of path at a moment when ok holds.
func told(t *testing.T, w *watcher, within time.Duration, path string, ok func() bool) time.Duration {
	t.Helper()
	start, late := time.Now(), time.After(within)
	for {
		select {
		case p := <-w.Changes():
			if p == path && ok() {
				return time.Since(start)
			}
		case <-late:
			t.Fatalf("no word of %s within %v (slow: %v)", filepath.Base(path), within, w.Slow())
		}
	}
}

func holds(path, text string) func() bool {
	return func() bool {
		data, _ := readFile(path) // a look, with no patience for a file that is gone
		return string(data) == text
	}
}

// TestThousandChanges replaces one file a thousand times and wants word of
// every one, each well inside the second after which the self-check would
// have found it, and no doubt about the notices at the end.
func TestThousandChanges(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	replaceFile(t, state, "start")
	w := real(t, state)
	var worst time.Duration
	for i := 0; i < 1000; i++ {
		text := strconv.Itoa(i)
		replaceFile(t, state, text)
		worst = max(worst, told(t, w, checkEvery/2, state, holds(state, text)))
	}
	extra := 0
	for more := true; more; {
		select {
		case <-w.Changes():
			extra++
		case <-time.After(100 * time.Millisecond):
			more = false
		}
	}
	if w.Slow() {
		t.Error("the watcher stopped trusting the notices")
	}
	t.Logf("1000 changes, the slowest told after %v, %d told twice", worst, extra)
}

// TestAChangeAtOnce replaces a file the instant its watcher is there, fifty
// watchers over: a folder's notices are on before the watcher is handed out.
func TestAChangeAtOnce(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	replaceFile(t, state, "start")
	for i := range 50 {
		w := real(t, state)
		text := strconv.Itoa(i)
		replaceFile(t, state, text)
		told(t, w, checkEvery/2, state, holds(state, text))
		w.Close()
	}
}

// TestThousandChangesInAFolder spreads a thousand changes over twenty files
// of a watched folder, half replaced and half written in place, without
// waiting between them, and wants the last state of every file told.
func TestThousandChangesInAFolder(t *testing.T) {
	dir := t.TempDir()
	w := real(t, dir)
	seen, done := map[string]string{}, make(chan struct{})
	go func() {
		defer close(done)
		for p := range w.Changes() {
			data, _ := readFile(p)
			seen[p] = string(data)
		}
	}()
	want := map[string]string{}
	for i := 0; i < 1000; i++ {
		path, text := filepath.Join(dir, fmt.Sprintf("p%d.json", i%20)), strconv.Itoa(i)
		if i%2 == 0 {
			replaceFile(t, path, text)
		} else if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		want[path] = text
	}
	time.Sleep(checkEvery / 2)
	slow := w.Slow()
	w.Close()
	<-done
	for path, text := range want {
		if seen[path] != text {
			t.Errorf("%s: last told at %q, it holds %q", filepath.Base(path), seen[path], text)
		}
	}
	if slow {
		t.Error("the watcher stopped trusting the notices")
	}
}

func TestSilentNoticesAreFoundOut(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	replaceFile(t, state, "0")
	w := watch(mute{}, []string{state})
	defer w.Close()
	time.Sleep(1500 * time.Millisecond) // nothing changes: nothing to doubt
	if w.Slow() {
		t.Fatal("slow before anything changed")
	}

	start := time.Now()
	replaceFile(t, state, "1")
	found := told(t, w, 2*time.Second, state, holds(state, "1"))
	for !w.Slow() {
		if time.Since(start) > 2*time.Second {
			t.Fatal("not slow two seconds after a change no notice told of")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Logf("the change was found after %v, the notices given up after %v", found, time.Since(start))

	// From here it looks four times a second.
	replaceFile(t, state, "2")
	if took := told(t, w, time.Second, state, holds(state, "2")); took > 2*slowEvery {
		t.Errorf("once slow, a change took %v", took)
	}
}

// named is a system that names the file a notice is about.
type named struct {
	mute
	ch chan string
}

func (n named) events() <-chan string { return n.ch }
func (named) exact() bool             { return true }

// TestNamedNoticeIsBelieved: where the system names the file, word is passed
// on even when a look sees the same time and size as before.
func TestNamedNoticeIsBelieved(t *testing.T) {
	dir := t.TempDir()
	state, n := filepath.Join(dir, "state.json"), named{ch: make(chan string)}
	replaceFile(t, state, "0")
	w := watch(n, []string{state})
	defer w.Close()
	n.ch <- state
	told(t, w, checkEvery/2, state, holds(state, "0"))
	n.ch <- filepath.Join(dir, "state.json.new")
	n.ch <- dir
	select {
	case p := <-w.Changes():
		t.Errorf("told of %s, which did not change", filepath.Base(p))
	case <-time.After(100 * time.Millisecond):
	}
}

// ending is a system that names the file and can end a folder's notices.
type ending struct {
	named
	added chan string
	over  *atomic.Bool
}

func (e ending) add(dir string) error { e.over.Store(false); e.added <- dir; return nil }
func (e ending) lost(string) bool     { return e.over.Load() }

// TestFolderInsideAFolder: a folder named in a watched folder, removed and
// made again, is told of as an entry on the notice that names it, so the
// check finds nothing unheard; and a folder whose notices the system ended
// gets new ones though it looks like the folder it was.
func TestFolderInsideAFolder(t *testing.T) {
	root := t.TempDir()
	ctx := filepath.Join(root, "ctx")
	n := ending{named{ch: make(chan string)}, make(chan string, 16), new(atomic.Bool)}
	w := watch(n, []string{ctx})
	defer w.Close()
	for _, change := range []func(string) error{os.Remove, os.Remove} {
		os.Mkdir(ctx, 0o700)
		n.ch <- ctx
		told(t, w, checkEvery/2, ctx, func() bool { return true })
		time.Sleep(checkEvery + slowEvery) // by now the folder has its notices
		change(ctx)
		n.ch <- ctx
		told(t, w, checkEvery/2, ctx, func() bool { return true })
	}
	time.Sleep(checkEvery + 2*slowEvery)
	if w.Slow() {
		t.Fatal("the watcher stopped trusting the notices")
	}
	for len(n.added) > 0 {
		<-n.added
	}
	n.over.Store(true)
	select {
	case <-n.added:
	case <-time.After(2 * checkEvery):
		t.Error("ended notices were not started again")
	}
}

func TestWatchWithoutNotices(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	w := watch(silent{}, []string{state})
	defer w.Close()
	replaceFile(t, state, "1")
	told(t, w, 2*time.Second, state, holds(state, "1"))
	if w.Slow() {
		t.Error("a folder that never had notices counted against them")
	}
}

func TestWatchKinds(t *testing.T) {
	root := t.TempDir()
	named, later := filepath.Join(root, "paused"), filepath.Join(root, "ctx")
	w := real(t, named, later)
	within := checkEvery / 2

	// A named file that appears, is written in place, touched, and removed.
	os.WriteFile(named, []byte("a"), 0o600)
	told(t, w, within, named, holds(named, "a"))
	os.WriteFile(named, []byte("b"), 0o600)
	told(t, w, within, named, holds(named, "b"))
	stamp := time.Now().Add(time.Hour)
	os.Chtimes(named, stamp, stamp)
	told(t, w, within, named, func() bool { return true })
	os.Remove(named)
	told(t, w, within, named, func() bool { _, err := os.Stat(named); return err != nil })

	// A neighbour nobody named is not told of.
	other := filepath.Join(root, "other")
	os.WriteFile(other, nil, 0o600)
	for quiet := time.After(100 * time.Millisecond); quiet != nil; {
		select {
		case p := <-w.Changes():
			if p == other {
				t.Errorf("told of %s", filepath.Base(p))
			}
		case <-quiet:
			quiet = nil
		}
	}

	// A folder named before it is there is watched whole once it appears.
	os.Mkdir(later, 0o700)
	told(t, w, within, later, func() bool { return true })
	file := filepath.Join(later, "p9.json")
	os.WriteFile(file, []byte("1"), 0o600)
	told(t, w, 2*checkEvery, file, holds(file, "1"))
	time.Sleep(checkEvery + slowEvery) // by now the folder has its notices
	replaceFile(t, file, "2")
	told(t, w, within, file, holds(file, "2"))

	// A folder removed and made again gets new notices.
	os.RemoveAll(later)
	told(t, w, within, file, func() bool { return true })
	os.Mkdir(later, 0o700)
	time.Sleep(checkEvery + slowEvery)
	replaceFile(t, file, "3")
	told(t, w, within, file, holds(file, "3"))

	// What happens inside a folder of the folder is nobody's change.
	inner := filepath.Join(later, "evidence")
	os.Mkdir(inner, 0o700)
	told(t, w, within, inner, func() bool { return true })
	os.WriteFile(filepath.Join(inner, "shot.png"), nil, 0o600)
	time.Sleep(checkEvery + 2*slowEvery)

	if w.Slow() {
		t.Error("the watcher stopped trusting the notices")
	}
	w.Close()
	w.Close()
	for range w.Changes() {
	}
}

func TestInotifyEvents(t *testing.T) {
	event := func(watch int32, mask uint32, name string, pad int) []byte {
		b := make([]byte, 16, 16+len(name)+pad)
		binary.NativeEndian.PutUint32(b, uint32(watch))
		binary.NativeEndian.PutUint32(b[4:], mask)
		binary.NativeEndian.PutUint32(b[12:], uint32(len(name)+pad))
		return append(append(b, name...), make([]byte, pad)...)
	}
	buf := append(event(1, 0x80, "state.json", 6), event(2, 0x400, "", 0)...)
	buf = append(buf, event(-1, 0x4000, "", 0)...)
	buf = append(buf, event(3, 2, "cut-short", 7)[:20]...)
	var got []string
	inotifyEvents(buf, func(watch int32, mask uint32, name string) {
		got = append(got, fmt.Sprintf("%d %#x %q", watch, mask, name))
	})
	if want := []string{`1 0x80 "state.json"`, `2 0x400 ""`, `-1 0x4000 ""`}; !reflect.DeepEqual(got, want) {
		t.Errorf("read %v", got)
	}
}

func TestDirChangeNames(t *testing.T) {
	entry := func(name string, last bool) []byte {
		units := utf16.Encode([]rune(name))
		size := (12 + 2*len(units) + 3) &^ 3
		b := make([]byte, size)
		if !last {
			binary.LittleEndian.PutUint32(b, uint32(size))
		}
		binary.LittleEndian.PutUint32(b[4:], 3)
		binary.LittleEndian.PutUint32(b[8:], uint32(2*len(units)))
		for i, u := range units {
			binary.LittleEndian.PutUint16(b[12+2*i:], u)
		}
		return b
	}
	buf := append(entry("state.json", false), entry("été 😀.md", false)...)
	buf = append(buf, entry("p9.json", true)...)
	if got, want := dirChangeNames(buf), []string{"state.json", "été 😀.md", "p9.json"}; !reflect.DeepEqual(got, want) {
		t.Errorf("read %q", got)
	}
	if got := dirChangeNames(buf[:len(buf)-6]); len(got) != 2 {
		t.Errorf("a cut buffer gave %q", got)
	}
	if got := dirChangeNames(nil); got != nil {
		t.Errorf("nothing gave %q", got)
	}
}
