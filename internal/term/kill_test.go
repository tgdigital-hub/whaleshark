//go:build darwin || linux

package term

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The test program is its own pane program: started again with this set,
// it opens the terminal as a pane does and stays until it is killed.
const asPane = "WHALESHARK_TEST_PANE"

func TestMain(m *testing.M) {
	if os.Getenv(asPane) != "" {
		t, err := Open()
		if err != nil {
			os.Exit(3)
		}
		t.Put(0, 0, t.W, "pane", Style{})
		t.Flush()
		select {}
	}
	os.Exit(m.Run())
}

// The pane is put back to normal when its program is killed: by the
// request to end, by an interrupt, and by its terminal going away.
func TestKilledProgramPutsTheTerminalBack(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			outer, inner, err := openPTY()
			if err != nil {
				t.Skipf("no pseudo-terminal here: %v", err)
			}
			defer outer.Close()
			defer inner.Close()
			var mu sync.Mutex
			var sent strings.Builder
			go func() {
				b := make([]byte, 4096)
				for {
					n, err := outer.Read(b)
					mu.Lock()
					sent.Write(b[:n])
					mu.Unlock()
					if err != nil {
						return
					}
				}
			}()
			has := func(s string) bool {
				mu.Lock()
				defer mu.Unlock()
				return strings.Contains(sent.String(), s)
			}
			cooked := func() bool {
				tio, err := unix.IoctlGetTermios(int(inner.Fd()), getTermios)
				if err != nil {
					t.Fatal(err)
				}
				return tio.Lflag&unix.ICANON != 0 && tio.Lflag&unix.ECHO != 0
			}
			wait := func(what string, ok func() bool) {
				t.Helper()
				for range 2500 {
					if ok() {
						return
					}
					time.Sleep(2 * time.Millisecond)
				}
				t.Fatalf("%s; the terminal got %q", what, sent.String())
			}
			if err := unix.IoctlSetWinsize(int(inner.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: 40, Row: 10}); err != nil || !cooked() {
				t.Fatalf("a new terminal should take a size (%v), take whole lines and echo them", err)
			}

			cmd := exec.Command(os.Args[0])
			cmd.Env = append(os.Environ(), asPane+"=1", "TERM=xterm-256color")
			cmd.Stdin, cmd.Stdout = inner, inner
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer cmd.Process.Kill()
			wait("the pane never drew", func() bool { return has("pane") })
			if cooked() || !has("\x1b[?1049h") || !has("\x1b[?1003h") {
				t.Fatal("the pane did not take the terminal over")
			}

			cmd.Process.Signal(sig)
			err = cmd.Wait()
			if code := cmd.ProcessState.ExitCode(); code != 128+int(sig) {
				t.Errorf("ended with %v (code %d), want code %d", err, code, 128+int(sig))
			}
			wait("the screen and the mouse were not given back", func() bool {
				return has("\x1b[?1003l\x1b[?1002l\x1b[?1000l\x1b[0m\x1b[?25h\x1b[?1049l")
			})
			if !cooked() {
				t.Error("the terminal was left in raw mode")
			}
		})
	}
}
