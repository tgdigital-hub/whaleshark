package platform

import (
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

// inotify names the entry that changed, so it needs nothing for a single file.
type inotify struct {
	fd   int
	f    *os.File
	ch   chan string
	done chan struct{}
	mu   sync.Mutex
	dir  map[int32]string
}

const inotifyMask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MOVED_FROM | unix.IN_MOVED_TO |
	unix.IN_MODIFY | unix.IN_ATTRIB | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF

func newNotifier() (notifier, error) {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, err
	}
	// Read through a file of the runtime's, so that closing it ends the read.
	i := &inotify{fd: fd, f: os.NewFile(uintptr(fd), "inotify"), ch: make(chan string, 64),
		done: make(chan struct{}), dir: map[int32]string{}}
	go i.read()
	return i, nil
}

func (i *inotify) add(dir string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	wd, err := unix.InotifyAddWatch(i.fd, dir, inotifyMask)
	if err != nil {
		return err
	}
	i.dir[int32(wd)] = dir
	return nil
}

func (i *inotify) file(string)           {}
func (i *inotify) exact() bool           { return true }
func (i *inotify) events() <-chan string { return i.ch }

func (i *inotify) close() {
	close(i.done)
	i.f.Close()
}

func (i *inotify) read() {
	buf := make([]byte, 64<<10)
	for {
		n, err := i.f.Read(buf)
		if err != nil {
			return
		}
		var paths []string
		i.mu.Lock()
		inotifyEvents(buf[:n], func(watch int32, mask uint32, name string) {
			switch dir, ok := i.dir[watch]; {
			case mask&unix.IN_Q_OVERFLOW != 0: // some were lost: look everywhere
				for _, d := range i.dir {
					paths = append(paths, d)
				}
			case ok && name == "":
				paths = append(paths, dir)
			case ok:
				paths = append(paths, filepath.Join(dir, name))
			}
		})
		i.mu.Unlock()
		for _, p := range paths {
			select {
			case i.ch <- p:
			case <-i.done:
				return
			}
		}
	}
}
