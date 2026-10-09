package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const (
	changerDir = "PLATFORM_TEST_CHANGER_DIR"
	changers   = 20
	changesOf  = 200
)

// change adds one to the number in the folder's file, as a command changes
// a run: take the lock, load, write a new file, rename it over the old one.
func change(s *System, dir string) error {
	unlock, err := s.Lock(filepath.Join(dir, "lock"), true)
	if err != nil {
		return err
	}
	defer unlock()
	final := filepath.Join(dir, "count")
	data, err := s.Read(final)
	if err != nil {
		return err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return err
	}
	tmp := final + "." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(n+1)+"\n"), 0o600); err != nil {
		return err
	}
	return s.Replace(tmp, final)
}

// TestChanger is the body of one of the twenty processes; by itself it does nothing.
func TestChanger(t *testing.T) {
	dir := os.Getenv(changerDir)
	if dir == "" {
		return
	}
	s := New(runtime.GOOS)
	for i := 0; i < changesOf; i++ {
		if err := change(s, dir); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTwentyProcesses(t *testing.T) {
	s, dir := New(runtime.GOOS), t.TempDir()
	count := filepath.Join(dir, "count")
	if err := os.WriteFile(count, []byte("0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Readers run throughout, reading as the program does, with Read: under
	// the shared lock the number never goes back, and without any lock the
	// file is never missing, half written or refused.
	var stop atomic.Bool
	var readers sync.WaitGroup
	var reads atomic.Int64
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func(locked bool) {
			defer readers.Done()
			last := 0
			for !stop.Load() {
				unlock := func() {}
				if locked {
					var err error
					if unlock, err = s.Lock(filepath.Join(dir, "lock"), false); err != nil {
						t.Error(err)
						return
					}
				}
				data, err := s.Read(count)
				unlock()
				n, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
				if err != nil || convErr != nil || n < last {
					t.Errorf("a reader saw %q after %d: %v", data, last, err)
					return
				}
				last = n
				reads.Add(1)
			}
		}(r%2 == 0)
	}

	var procs sync.WaitGroup
	for p := 0; p < changers; p++ {
		procs.Add(1)
		go func() {
			defer procs.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestChanger$")
			cmd.Env = append(os.Environ(), changerDir+"="+dir)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("a process failed: %v\n%s", err, out)
			}
		}()
	}
	procs.Wait()
	stop.Store(true)
	readers.Wait()

	data, _ := os.ReadFile(count)
	if got := strings.TrimSpace(string(data)); got != strconv.Itoa(changers*changesOf) {
		t.Fatalf("%d processes of %d changes gave %s", changers, changesOf, got)
	}
	left, _ := filepath.Glob(count + ".*")
	if len(left) != 0 {
		t.Errorf("temporary files are left: %v", left)
	}
	t.Logf("%d changes, %d reads beside them", changers*changesOf, reads.Load())
}

func TestLocks(t *testing.T) {
	s, path := New(runtime.GOOS), filepath.Join(t.TempDir(), "wait.lock")
	unlock, ok, err := s.TryLock(path)
	if err != nil || !ok {
		t.Fatalf("a free lock: %v, %v", ok, err)
	}
	if _, again, err := s.TryLock(path); again || err != nil {
		t.Errorf("a held lock was taken again: %v, %v", again, err)
	}
	unlock()

	shared, err := s.Lock(path, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Lock(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.TryLock(path); ok {
		t.Error("the lock was taken from under two readers")
	}
	got := make(chan struct{})
	go func() {
		unlock, _ := s.Lock(path, true)
		close(got)
		unlock()
	}()
	shared()
	select {
	case <-got:
		t.Fatal("a writer got the lock while a reader held it")
	default:
	}
	second()
	<-got

	if info, _ := os.Stat(path); runtime.GOOS != onWindows && info.Mode().Perm() != 0o600 {
		t.Errorf("the lock file is %v", info.Mode().Perm())
	}
	if data, _ := os.ReadFile(path); strings.TrimSpace(string(data)) != strconv.Itoa(os.Getpid()) {
		t.Errorf("the lock file says %q", data)
	}
	if _, err := s.Lock(filepath.Join(t.TempDir(), "missing", "lock"), true); err == nil {
		t.Error("a lock in a folder that is not there was given")
	}
}
