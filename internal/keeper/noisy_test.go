//go:build unix

package keeper

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/pty"
)

var noisy = flag.Duration("noisy", 3*time.Second, "how long the twenty noisy shells print; the row's check is 10m")

// Twenty panes of noisy shells, in real terminals, with a window attached
// that is switched between tabs and resized: every shell counts the lines
// it printed, and the keeper must have read exactly that many bytes from
// each, with the last line it printed the last on the pane's screen.
func TestTwentyNoisyShellsLoseNoByte(t *testing.T) {
	const (
		panes = 20
		words = "the quick brown fox jumps over the lazy dog, again and again"
		// A burst of a thousand numbered lines, a short rest, and so on
		// until the time is up; then the count goes to the file named, and
		// the shell stays so that the pane does. The terminal is told to
		// pass every byte as it is: left to turn a line feed into two
		// bytes itself, the terminal of macOS now and then sends the
		// carriage return twice when the keeper makes the shell wait
		// (seen here: one line in 18,000 ended CR CR LF), and then the
		// count is the system's and not the shell's.
		script = `stty -opost; i=0; end=$(($(date +%%s)+%d))
while [ "$(date +%%s)" -lt "$end" ]; do
  n=0; while [ $n -lt 1000 ]; do printf '%%08d %s\r\n' $i; i=$((i+1)); n=$((n+1)); done
  sleep 0.1
done
printf 'END %%d\r\n' $i; printf %%d $i > "$0"; exec sleep 86411`
	)
	kit := login(t)
	pty.Plug(kit)
	t.Setenv("SHELL", "/bin/sh")
	k, err := open(kit)
	if err != nil {
		t.Fatal(err)
	}
	go k.serve()
	t.Cleanup(k.stop)
	r := &rig{t: t, k: k, kit: kit}
	c := r.dial()
	w := r.window(160, 50)
	dir := filepath.Dir(os.Getenv(contract.EnvSocket))
	file := func(i int) string { return filepath.Join(dir, fmt.Sprint("count-", i)) }
	for i := range panes {
		pane := c.call(contract.WireCall{Op: contract.OpTabCreate, Label: fmt.Sprint("shell ", i), Cwd: dir}).Pane
		run := contract.WireCall{Op: contract.OpRun, Pane: pane.ID, Argv: []string{"/bin/sh", "-c", fmt.Sprintf(script, int(noisy.Seconds()), words), file(i)}}
		if reply := c.call(run); reply.Err != "" {
			t.Fatalf("%+v", reply)
		}
	}
	// While they print: another tab every fifth of a second, another size
	// of the window now and then, and a call that reads a screen.
	for i, end := 0, time.Now().Add(*noisy-time.Second); time.Now().Before(end); i++ {
		c.call(contract.WireCall{Op: contract.OpTabFocus, Tab: fmt.Sprint("t", i%panes+1)})
		c.call(contract.WireCall{Op: contract.OpScreen, Pane: fmt.Sprint("p", (i*7)%panes+1)})
		if i%25 == 24 {
			w.send(contract.WireCall{Op: contract.OpWindowSize, W: float64(120 + i%3*20), H: float64(40 + i%2*10)})
		}
		time.Sleep(200 * time.Millisecond)
	}
	var total uint64
	wait := 10 * time.Second
	for i := range panes {
		id := fmt.Sprint("p", i+1)
		var lines uint64
		r.until("the shell of "+id+" to say how much it printed", func() bool {
			data, err := os.ReadFile(file(i))
			if err != nil || len(data) == 0 {
				return false
			}
			lines, err = strconv.ParseUint(string(data), 10, 64)
			return err == nil
		})
		last := fmt.Sprintf("END %d", lines)
		want := lines*uint64(len("00000000 ")+len(words)+2) + uint64(len(last)+2)
		// What the keeper has read of it, and the program in front there.
		var got uint64
		front := "is gone with its pane"
		read := func() bool {
			return r.locked(func() bool {
				p := k.panes[id]
				if p == nil {
					return true
				}
				got = p.count.Load()
				name, err := p.tty.Front()
				front = fmt.Sprintf("is %q (%v)", name, err)
				return got >= want
			})
		}
		for end := time.Now().Add(wait); !read() && time.Now().Before(end); {
			time.Sleep(time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond)
		read()
		text := strings.Split(c.call(contract.WireCall{Op: contract.OpScreen, Pane: id}).Text, "\n")
		n := len(text)
		if got != want || lines == 0 || n < 2 || text[n-1] != last || text[n-2] != fmt.Sprintf("%08d %s", lines-1, words) {
			t.Errorf("%s: the shell printed %d lines, which is %d bytes, and the keeper read %d (%+d); the program in front %s; the screen ends with %q",
				id, lines, want, got, int64(got-want), front, text[max(n-2, 0):])
			wait = time.Second // the next pane says the same sooner
		}
		total += got
	}
	t.Logf("%d panes for %v: %d bytes read, none lost", panes, *noisy, total)
}
