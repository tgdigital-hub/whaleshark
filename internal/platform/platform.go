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
	replaceFor = 2 * time.Second // how long Replace and Read keep trying
)

// System is one operating system as the program sees it. OS, Env, Home and
// Etc are fields so that the rules of each system can be tested on any of them.
type System struct {
	OS   string // "darwin", "linux" or "windows"
	Env  func(string) string
	Home string
	Etc  string // where Linux keeps its passwd and group files
}

// New returns the system the program runs on, called by its Go name.
func New(goos string) *System {
	home, _ := os.UserHomeDir()
	return &System{OS: goos, Env: os.Getenv, Home: home, Etc: "/etc"}
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
	return s.replace(tmp, final, moveOver, time.Sleep)
}

func (s *System) replace(tmp, final string, rename func(string, string) error, sleep func(time.Duration)) error {
	return s.patient(final, heldOpen, func() error { return rename(tmp, final) }, sleep)
}

// Read opens the file so that a replace goes on under it, and waits out
// another program's hold on it as Replace does.
func (s *System) Read(path string) (data []byte, err error) {
	err = s.patient(path, heldOpen[1:], func() error { data, err = readFile(path); return err }, time.Sleep)
	return data, err
}

// patient tries again, with growing pauses, what Windows refused with one of
// the codes because another program holds the file open.
func (s *System) patient(path string, codes []syscall.Errno, try func() error, sleep func(time.Duration)) error {
	waited, pause := time.Duration(0), 10*time.Millisecond
	for {
		err := try()
		var code syscall.Errno
		if err == nil || s.OS != onWindows || !errors.As(err, &code) || !slices.Contains(codes, code) {
			return err
		}
		if waited >= replaceFor {
			return &contract.Refusal{Exit: contract.ExitEnv, Code: "state_busy",
				Message: fmt.Sprintf("%s is held open by another program; nothing was changed", path)}
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
		return os.Chmod(path, 0o700) // #nosec G302 -- a folder: without its own last bit nobody can enter it
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
	uid, gid, ok := owner(info)
	if !ok || s.OS == onWindows {
		return false, contract.ErrCannotTell
	}
	return s.othersCanWrite(info.Mode(), uid, gid, uint32(os.Geteuid())), nil // #nosec G115 -- where a login has a number it is never below nought
}

// othersCanWrite: another login owns it, anyone may write it, or its group
// may and is not the owner's alone. Only Linux says who is in a group
// without asking a directory; anywhere else a group that may write counts.
func (s *System) othersCanWrite(mode fs.FileMode, owner, group, me uint32) bool {
	if owner != me || mode.Perm()&0o002 != 0 {
		return true
	}
	return mode.Perm()&0o020 != 0 && (s.OS != "linux" || shared(s.Etc, owner, group))
}

// shared reports whether a group has a member besides one login: another
// login whose own group it is, or another name on its line of the group
// file. Files that cannot be read count as shared.
func shared(etc string, uid, gid uint32) bool {
	passwd, err := os.ReadFile(path.Join(etc, "passwd"))
	groups, err2 := os.ReadFile(path.Join(etc, "group"))
	if err != nil || err2 != nil {
		return true
	}
	u, g, name := strconv.Itoa(int(uid)), strconv.Itoa(int(gid)), ""
	for line := range strings.Lines(string(passwd)) {
		if f := strings.Split(strings.TrimSpace(line), ":"); len(f) > 3 && f[2] == u {
			name = f[0]
		} else if len(f) > 3 && f[3] == g {
			return true
		}
	}
	for line := range strings.Lines(string(groups)) {
		if f := strings.Split(strings.TrimSpace(line), ":"); len(f) > 3 && f[2] == g {
			for member := range strings.SplitSeq(f[3], ",") {
				if member != "" && member != name {
					return true
				}
			}
		}
	}
	return false
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
	if s.Env(contract.EnvNotices) == contract.NoticesOff {
		return watch(mute{}, paths), nil
	}
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
