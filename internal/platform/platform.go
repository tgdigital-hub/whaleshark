package platform

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const (
	app        = "whaleshark"
	onWindows  = "windows"
	onMac      = "darwin"
	replaceFor = 2 * time.Second // how long Replace keeps trying
)

// ErrCannotTell: the system gives no way to know. ErrNotOurs: a lock in another login's folder.
var (
	ErrCannotTell = errors.New("cannot tell on this system")
	ErrNotOurs    = errors.New("the folder belongs to another login")
)

// System is one operating system as the program sees it. OS, Env and Home are
// fields so that the rules of each system can be tested on any of them.
type System struct {
	OS   string // "darwin", "linux" or "windows"
	Env  func(string) string
	Home string
}

// New returns the system the program runs on, called by its Go name.
func New(goos string) *System {
	home, _ := os.UserHomeDir()
	return &System{OS: goos, Env: os.Getenv, Home: home}
}

// join builds a path in the system's own spelling, whatever system this is.
func (s *System) join(parts ...string) string {
	if s.OS == onWindows {
		return strings.Join(parts, `\`)
	}
	return path.Join(parts...)
}

func (s *System) Dirs() (contract.Dirs, error) {
	if s.OS == onWindows {
		roaming, local := s.Env("APPDATA"), s.Env("LOCALAPPDATA")
		if roaming == "" || local == "" {
			return contract.Dirs{}, errors.New("APPDATA and LOCALAPPDATA must both be set")
		}
		ours := s.join(local, app)
		return contract.Dirs{Config: s.join(roaming, app), State: ours, Cache: s.join(ours, "cache")}, nil
	}
	if s.Home == "" {
		return contract.Dirs{}, errors.New("this login has no home folder")
	}
	dir := func(name, fallback string) string {
		// A relative value is not a folder of the login: the convention says to ignore it.
		if base := s.Env(name); path.IsAbs(base) {
			return s.join(base, app)
		}
		return s.join(s.Home, fallback, app)
	}
	return contract.Dirs{
		Config: dir("XDG_CONFIG_HOME", ".config"),
		State:  dir("XDG_STATE_HOME", ".local/state"),
		Cache:  dir("XDG_CACHE_HOME", ".cache"),
	}, nil
}

func (s *System) Lock(path string, exclusive bool) (func(), error) {
	f, err := lockFile(path, exclusive, true)
	if err != nil {
		return nil, err
	}
	if exclusive {
		// Who holds it, for a person who looks; nothing of ours reads it back.
		f.Truncate(0)
		f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return func() { f.Close() }, nil
}

func (s *System) TryLock(path string) (func(), bool, error) {
	f, err := lockFile(path, true, false)
	if f == nil {
		return nil, false, err
	}
	return func() { f.Close() }, true, nil
}

// heldOpen are the numbers Windows gives while another program holds a file
// open: access denied, a sharing violation, a lock violation.
var heldOpen = []syscall.Errno{5, 32, 33}

func (s *System) Replace(tmp, final string) error {
	return s.replace(tmp, final, os.Rename, time.Sleep)
}

func (s *System) replace(tmp, final string, rename func(string, string) error, sleep func(time.Duration)) error {
	waited, pause := time.Duration(0), 10*time.Millisecond
	for {
		err := rename(tmp, final)
		var code syscall.Errno
		if err == nil || s.OS != onWindows || !errors.As(err, &code) || !slices.Contains(heldOpen, code) {
			return err
		}
		if waited >= replaceFor {
			return &contract.Refusal{Exit: contract.ExitEnv, Code: "state_busy",
				Message: fmt.Sprintf("%s is held open by another program; nothing was changed", final)}
		}
		pause = min(pause, replaceFor-waited)
		sleep(pause)
		waited, pause = waited+pause, pause*2
	}
}

// Private does nothing on Windows, where a file takes the permissions of the
// person's own profile folder.
func (s *System) Private(path string) error {
	info, err := os.Stat(path)
	if err != nil || s.OS == onWindows {
		return err
	}
	if info.IsDir() {
		return os.Chmod(path, 0o700)
	}
	return os.Chmod(path, 0o600)
}

func (s *System) PrivateTemp() (string, error) {
	dirs, err := s.Dirs()
	if err != nil {
		return "", err
	}
	tmp := s.join(dirs.Cache, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return "", err
	}
	if err := s.Private(tmp); err != nil {
		return "", err
	}
	return os.MkdirTemp(tmp, "")
}

func (s *System) WritableByOthers(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	uid, ok := owner(info)
	if !ok || s.OS == onWindows {
		return false, ErrCannotTell
	}
	return othersCanWrite(info.Mode(), uid, uint32(os.Geteuid())), nil
}

func othersCanWrite(mode fs.FileMode, owner, me uint32) bool {
	return owner != me || mode.Perm()&0o022 != 0
}

// Where syncing services keep their folders. syncNames are first folders of a
// home, in lower case, and may go on after a space ("OneDrive - Example");
// syncFolders are where macOS puts them; syncVars name OneDrive's on Windows.
var (
	syncNames   = []string{"dropbox", "onedrive", "google drive", "googledrive", "my drive", "icloud drive", "iclouddrive"}
	syncFolders = []string{"library/mobile documents/", "library/cloudstorage/"}
	syncVars    = []string{"OneDrive", "OneDriveConsumer", "OneDriveCommercial"}
)

func (s *System) Synced(p string) bool {
	key := s.PathKey(p) + "/"
	if s.OS == onWindows {
		for _, name := range syncVars {
			if root := s.Env(name); root != "" && strings.HasPrefix(key, s.PathKey(root)+"/") {
				return true
			}
		}
	}
	rel, inHome := strings.CutPrefix(key, s.PathKey(s.Home)+"/")
	if !inHome || s.Home == "" {
		return false
	}
	rel = strings.ToLower(rel)
	first, _, _ := strings.Cut(rel, "/")
	for _, name := range syncNames {
		if first == name || strings.HasPrefix(first, name+" ") {
			return true
		}
	}
	for _, folder := range syncFolders {
		if s.OS == onMac && strings.HasPrefix(rel, folder) {
			return true
		}
	}
	return false
}

// PathKey leaves the letters as they are composed: the standard library
// cannot bring two spellings of one accented letter together.
func (s *System) PathKey(p string) string {
	if s.OS == onWindows {
		p = strings.ReplaceAll(p, `\`, "/")
	}
	if s.OS != "linux" {
		p = strings.ToLower(p)
	}
	return path.Clean(p)
}

func (s *System) Watch(paths ...string) (contract.Watcher, error) {
	n, err := newNotifier()
	if err != nil {
		n = silent{}
	}
	w := watch(n, paths)
	w.slow.Store(err != nil)
	return w, nil
}

// SelfPath does not follow links: the stable entry is the one to hand on.
func (s *System) SelfPath() (string, error) { return os.Executable() }
