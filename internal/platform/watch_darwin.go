package platform

import (
	"sync"

	"golang.org/x/sys/unix"
)

// kqueue keeps one open descriptor for each folder and each file it is told
// of. A folder's notice says only that its list of entries changed.
type kqueue struct {
	fd   int
	ch   chan string
	done chan struct{}
	mu   sync.Mutex
	open map[string]int
	path map[int]string
}

func newNotifier() (notifier, error) {
	fd, err := unix.Kqueue()
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(fd)
	// An event of our own making, to wake the reader when it is time to stop.
	wake := []unix.Kevent_t{{Filter: unix.EVFILT_USER, Flags: unix.EV_ADD | unix.EV_CLEAR}}
	if _, err := unix.Kevent(fd, wake, nil, nil); err != nil {
		unix.Close(fd)
		return nil, err
	}
	k := &kqueue{fd: fd, ch: make(chan string, 64), done: make(chan struct{}),
		open: map[string]int{}, path: map[int]string{}}
	go k.read()
	return k, nil
}

func (k *kqueue) add(dir string) error {
	return k.watch(dir, unix.NOTE_WRITE|unix.NOTE_DELETE|unix.NOTE_RENAME)
}

// A file that is removed or replaced changes its folder, which tells of it.
func (k *kqueue) file(path string) {
	k.watch(path, unix.NOTE_WRITE|unix.NOTE_EXTEND|unix.NOTE_ATTRIB)
}

func (k *kqueue) watch(path string, what uint32) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if old, ok := k.open[path]; ok {
		unix.Close(old)
		delete(k.open, path)
		delete(k.path, old)
	}
	fd, err := unix.Open(path, unix.O_EVTONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	ev := []unix.Kevent_t{{Ident: uint64(fd), Filter: unix.EVFILT_VNODE, Flags: unix.EV_ADD | unix.EV_CLEAR, Fflags: what}}
	if _, err := unix.Kevent(k.fd, ev, nil, nil); err != nil {
		unix.Close(fd)
		return err
	}
	k.open[path], k.path[fd] = fd, path
	return nil
}

func (k *kqueue) exact() bool           { return false }
func (k *kqueue) events() <-chan string { return k.ch }

func (k *kqueue) close() {
	close(k.done)
	unix.Kevent(k.fd, []unix.Kevent_t{{Filter: unix.EVFILT_USER, Fflags: unix.NOTE_TRIGGER}}, nil, nil)
}

// read hands on the path behind each event, and closes everything at the end.
func (k *kqueue) read() {
	defer func() {
		k.mu.Lock()
		defer k.mu.Unlock()
		for fd := range k.path {
			unix.Close(fd)
		}
		unix.Close(k.fd)
	}()
	got := make([]unix.Kevent_t, 32)
	for {
		n, err := unix.Kevent(k.fd, nil, got, nil)
		if err != nil && err != unix.EINTR {
			return
		}
		for i := 0; i < n; i++ {
			k.mu.Lock()
			p, ok := k.path[int(got[i].Ident)]
			k.mu.Unlock()
			if got[i].Filter == unix.EVFILT_USER {
				return
			}
			if !ok {
				continue
			}
			select {
			case k.ch <- p:
			case <-k.done:
				return
			}
		}
	}
}
