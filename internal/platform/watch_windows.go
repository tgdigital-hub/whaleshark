package platform

import (
	"path/filepath"
	"sync"

	"golang.org/x/sys/windows"
)

// dirChanges asks Windows what changes in each folder, and a goroutine of
// the folder's own waits for each answer. Windows keeps what happens in a
// folder only from the first asking on, so add asks before it returns.
type dirChanges struct {
	ch   chan string
	done chan struct{}
	mu   sync.Mutex
	open map[string]*asking
}

// asking is one folder held open and what Windows answers into. Its
// goroutine alone closes it, and only once no asking is under way: Windows
// writes into this memory until then. over and a closed h are read and
// written under the mutex of dirChanges.
type asking struct {
	h    windows.Handle
	buf  []byte
	ov   windows.Overlapped
	over bool
}

const dirChangeMask = windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_DIR_NAME |
	windows.FILE_NOTIFY_CHANGE_SIZE | windows.FILE_NOTIFY_CHANGE_LAST_WRITE

func newNotifier() (notifier, error) {
	return &dirChanges{ch: make(chan string, 64), done: make(chan struct{}), open: map[string]*asking{}}, nil
}

func (a *asking) ask() error {
	return windows.ReadDirectoryChanges(a.h, &a.buf[0], uint32(len(a.buf)), false, dirChangeMask, nil, &a.ov, 0)
}

// stop ends an asking: the answer its goroutine waits for comes back refused.
func (a *asking) stop() {
	if a.over = true; a.h != 0 {
		windows.CancelIoEx(a.h, &a.ov)
	}
}

func (d *dirChanges) add(dir string) error {
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(name, windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return err
	}
	a := &asking{h: h, buf: make([]byte, 64<<10)}
	if err := a.ask(); err != nil {
		windows.CloseHandle(h)
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if old := d.open[dir]; old != nil {
		old.stop()
	}
	d.open[dir] = a
	go d.read(dir, a)
	return nil
}

func (d *dirChanges) file(string)           {}
func (d *dirChanges) exact() bool           { return true }
func (d *dirChanges) lost(string) bool      { return false }
func (d *dirChanges) events() <-chan string { return d.ch }

func (d *dirChanges) close() {
	close(d.done)
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, a := range d.open {
		a.stop()
	}
}

// read hands on what Windows answers, asking again before it does, until an
// answer is refused: the asking was stopped, or the folder is gone.
func (d *dirChanges) read(dir string, a *asking) {
	for asked := error(nil); asked == nil; {
		var n uint32
		err := windows.GetOverlappedResult(a.h, &a.ov, &n, true)
		if err != nil && err != windows.ERROR_NOTIFY_ENUM_DIR {
			break
		}
		paths := []string{dir} // nothing written means too much happened to list: look at the folder
		if err == nil && n > 0 {
			paths = dirChangeNames(a.buf[:n])
			for i, name := range paths {
				paths[i] = filepath.Join(dir, name)
			}
		}
		asked = a.ask()
		d.mu.Lock()
		if asked == nil && a.over { // stopped between two askings, with nothing to refuse
			windows.CancelIoEx(a.h, &a.ov)
		}
		d.mu.Unlock()
		for _, p := range paths {
			select {
			case d.ch <- p:
			case <-d.done:
			}
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	windows.CloseHandle(a.h)
	a.h = 0
}
