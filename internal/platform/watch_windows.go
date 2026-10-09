package platform

import (
	"path/filepath"
	"sync"

	"golang.org/x/sys/windows"
)

// dirChanges reads the changes of each folder in a goroutine of its own:
// the call waits, and names the entries that changed.
type dirChanges struct {
	ch   chan string
	done chan struct{}
	mu   sync.Mutex
	open map[string]windows.Handle
}

const dirChangeMask = windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_DIR_NAME |
	windows.FILE_NOTIFY_CHANGE_SIZE | windows.FILE_NOTIFY_CHANGE_LAST_WRITE

func newNotifier() (notifier, error) {
	return &dirChanges{ch: make(chan string, 64), done: make(chan struct{}), open: map[string]windows.Handle{}}, nil
}

func (d *dirChanges) add(dir string) error {
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(name, windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if old, ok := d.open[dir]; ok {
		stop(old)
	}
	d.open[dir] = h
	go d.read(dir, h)
	return nil
}

// stop ends the read that waits on a folder; the next one fails on the closed handle.
func stop(h windows.Handle) {
	windows.CancelIoEx(h, nil)
	windows.CloseHandle(h)
}

func (d *dirChanges) file(string)           {}
func (d *dirChanges) exact() bool           { return true }
func (d *dirChanges) lost(string) bool      { return false }
func (d *dirChanges) events() <-chan string { return d.ch }

func (d *dirChanges) close() {
	close(d.done)
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, h := range d.open {
		stop(h)
	}
}

func (d *dirChanges) read(dir string, h windows.Handle) {
	buf := make([]byte, 64<<10)
	for {
		var n uint32
		if windows.ReadDirectoryChanges(h, &buf[0], uint32(len(buf)), false, dirChangeMask, &n, nil, 0) != nil {
			return
		}
		paths := []string{dir} // nothing written means too much happened to list: look at the folder
		if n > 0 {
			paths = dirChangeNames(buf[:n])
			for i, name := range paths {
				paths[i] = filepath.Join(dir, name)
			}
		}
		for _, p := range paths {
			select {
			case d.ch <- p:
			case <-d.done:
				return
			}
		}
	}
}
