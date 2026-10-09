//go:build darwin || linux

// The recorder is a test tool. It starts a program in a pseudo-terminal of
// a given size, feeds it scripted input, and saves every byte the program
// prints with the time it arrived. It reads nothing of what the program
// prints as a terminal would: it answers a question a program asks only
// where the script says so, with the bytes the script gives.
//
//	recorder record -size 80x24 -script FILE -out FILE [-dir DIR] [-env K=V|K]... -- PROGRAM ARG...
//	recorder play FILE [-speed 4]     print the recording as it was printed
//	recorder bytes FILE               print the program's bytes with no pauses
//
// A script is one step a line:
//
//	wait 1.5                 pause for that many seconds
//	quiet 0.4 20             wait until nothing was printed for 0.4 s, at most 20 s
//	expect "text" 20         wait until the program has printed text, at most 20 s;
//	                         spaces and control sequences are left out of the comparison
//	send "ls\r"              type; the text is a Go string, so \r, \x1b and é work
//	reply "\x1b[c" "\x1b[?62c"  from now on answer every such question with that answer
//	size 100x30              change the size
//	end 5                    wait that long for the program to end, then hang up
//
// A recording is text, one event a line: the seconds since the start, then
// o (printed by the program), i (typed), s (a new size) or x (the exit code).
package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const header = "whaleshark recording 1"

func main() {
	if len(os.Args) < 3 {
		fail("usage: recorder record|play|bytes ... (see the comment at the top of main.go)")
	}
	var err error
	switch os.Args[1] {
	case "record":
		err = record(os.Args[2:])
	case "play", "bytes":
		err = play(os.Args[1] == "play", os.Args[2:])
	default:
		err = fmt.Errorf("no such command %q", os.Args[1])
	}
	if err != nil {
		fail(err.Error())
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "recorder:", msg)
	os.Exit(1)
}

type envList []string

func (e *envList) String() string     { return strings.Join(*e, " ") }
func (e *envList) Set(v string) error { *e = append(*e, v); return nil }

// tape is the recording being written, and everything printed so far.
type tape struct {
	mu      sync.Mutex
	start   time.Time
	out     *bufio.Writer
	all     []byte
	last    time.Time
	seen    int // how much of all the replies have looked at
	replies [][2]string
	outer   *os.File
}

func (t *tape) event(kind, rest string) {
	fmt.Fprintf(t.out, "%.6f %s %s\n", time.Since(t.start).Seconds(), kind, rest)
}

// printed takes what the program printed, and answers the questions the
// script named.
func (t *tape) printed(p []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.event("o", strconv.QuoteToASCII(string(p)))
	t.all = append(t.all, p...)
	t.last = time.Now()
	for {
		at, pick := -1, -1
		for i, r := range t.replies {
			if n := bytes.Index(t.all[t.seen:], []byte(r[0])); n >= 0 && (at < 0 || n < at) {
				at, pick = n, i
			}
		}
		if pick < 0 {
			break
		}
		t.seen += at + len(t.replies[pick][0])
		t.typed(t.replies[pick][1])
	}
	// A question cut in two by a read must still be found next time.
	if keep := len(t.all) - 32; keep > t.seen {
		t.seen = keep
	}
}

func (t *tape) typed(s string) {
	t.event("i", strconv.QuoteToASCII(s))
	t.last = time.Now() // "quiet" counts from here, so it waits for the answer
	t.outer.WriteString(s)
}

// letters is what was printed with the control sequences, the control
// characters and the spaces taken out. A program may place each word with a
// cursor move and print no space at all, so "expect" compares in this form.
func letters(p []byte) []byte {
	out := make([]byte, 0, len(p))
	for i := 0; i < len(p); i++ {
		switch c := p[i]; {
		case c == 0x1b && i+1 < len(p) && p[i+1] == '[':
			for i += 2; i < len(p) && (p[i] < 0x40 || p[i] > 0x7e); i++ {
			}
		case c == 0x1b && i+1 < len(p) && (p[i+1] == ']' || p[i+1] == 'P'):
			for i += 2; i < len(p) && p[i] != 0x07 && p[i] != 0x1b; i++ {
			}
			if i+1 < len(p) && p[i] == 0x1b {
				i++
			}
		case c == 0x1b:
			for i++; i < len(p) && p[i] < 0x30; i++ {
			}
		case c > ' ' && c != 0x7f:
			out = append(out, c)
		}
	}
	return out
}

func parseSize(s string) (*unix.Winsize, error) {
	var w, h uint16
	if _, err := fmt.Sscanf(s, "%dx%d", &w, &h); err != nil || w == 0 || h == 0 {
		return nil, fmt.Errorf("a size is columns x rows, like 80x24, not %q", s)
	}
	return &unix.Winsize{Col: w, Row: h}, nil
}

// words splits a script line into its words; a word in quotes is a Go string.
func words(line string) ([]string, error) {
	var out []string
	for line = strings.TrimSpace(line); line != ""; line = strings.TrimSpace(line) {
		if line[0] == '"' {
			q, err := strconv.QuotedPrefix(line)
			if err != nil {
				return nil, fmt.Errorf("bad quotes in %q", line)
			}
			s, _ := strconv.Unquote(q)
			out, line = append(out, s), line[len(q):]
			continue
		}
		n := strings.IndexAny(line+" ", " \t")
		out, line = append(out, line[:n]), line[n:]
	}
	return out, nil
}

func seconds(w []string, i int, def float64) time.Duration {
	if i < len(w) {
		if f, err := strconv.ParseFloat(w[i], 64); err == nil {
			def = f
		}
	}
	return time.Duration(def * float64(time.Second))
}

func record(args []string) error {
	fs := flag.NewFlagSet("record", flag.ExitOnError)
	size := fs.String("size", "80x24", "columns x rows")
	script := fs.String("script", "", "the script file")
	outPath := fs.String("out", "", "the recording to write")
	dir := fs.String("dir", "", "the folder the program starts in")
	var env envList
	fs.Var(&env, "env", "a variable for the program: K=V, or K alone to hand on the recorder's own value without writing it into the recording; it gets these and no others")
	fs.Parse(args)
	if fs.NArg() == 0 || *script == "" || *outPath == "" {
		return errors.New("record needs -script, -out and a program after --")
	}
	ws, err := parseSize(*size)
	if err != nil {
		return err
	}
	steps, err := os.ReadFile(*script)
	if err != nil {
		return err
	}
	outer, inner, err := openPTY()
	if err != nil {
		return err
	}
	if err := unix.IoctlSetWinsize(int(outer.Fd()), unix.TIOCSWINSZ, ws); err != nil {
		return err
	}
	file, err := os.Create(*outPath)
	if err != nil {
		return err
	}
	defer file.Close()
	t := &tape{out: bufio.NewWriter(file), outer: outer}
	defer t.out.Flush()
	fmt.Fprintln(t.out, header)
	fmt.Fprintf(t.out, "size %dx%d\n", ws.Col, ws.Row)
	fmt.Fprintf(t.out, "argv %q\n", fs.Args())
	fmt.Fprintf(t.out, "env %q\n", []string(env))

	cmd := exec.Command(fs.Arg(0), fs.Args()[1:]...)
	cmd.Dir = *dir
	for _, kv := range env {
		if !strings.Contains(kv, "=") {
			kv += "=" + os.Getenv(kv)
		}
		cmd.Env = append(cmd.Env, kv)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inner, inner, inner
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	t.start, t.last = time.Now(), time.Now()
	if err := cmd.Start(); err != nil {
		return err
	}
	inner.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 32<<10)
		for {
			n, err := outer.Read(buf)
			if n > 0 {
				t.printed(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	var waitErr error
	gone := false
	until := func(limit time.Duration, ok func() bool) bool {
		for end := time.Now().Add(limit); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
			t.mu.Lock()
			yes := ok()
			t.mu.Unlock()
			if yes {
				return true
			}
		}
		return false
	}
	for n, line := range strings.Split(string(steps), "\n") {
		w, err := words(line)
		if err != nil {
			return err
		}
		if len(w) == 0 || strings.HasPrefix(w[0], "#") {
			continue
		}
		bad := fmt.Errorf("script line %d: %s", n+1, line)
		switch {
		case w[0] == "wait":
			time.Sleep(seconds(w, 1, 1))
		case w[0] == "quiet":
			gap := seconds(w, 1, 0.5)
			until(seconds(w, 2, 30), func() bool { return time.Since(t.last) >= gap })
		case w[0] == "expect" && len(w) >= 2:
			want := []byte(strings.ReplaceAll(w[1], " ", ""))
			if !until(seconds(w, 2, 30), func() bool { return bytes.Contains(letters(t.all), want) }) {
				fmt.Fprintf(os.Stderr, "recorder: line %d: %q never came\n", n+1, w[1])
			}
		case w[0] == "send" && len(w) == 2:
			t.mu.Lock()
			t.typed(w[1])
			t.mu.Unlock()
		case w[0] == "reply" && len(w) == 3:
			t.mu.Lock()
			t.replies = append(t.replies, [2]string{w[1], w[2]})
			t.mu.Unlock()
		case w[0] == "size" && len(w) == 2:
			ws, err := parseSize(w[1])
			if err != nil {
				return err
			}
			t.mu.Lock()
			t.event("s", fmt.Sprintf("%dx%d", ws.Col, ws.Row))
			t.last = time.Now()
			t.mu.Unlock()
			unix.IoctlSetWinsize(int(outer.Fd()), unix.TIOCSWINSZ, ws)
		case w[0] == "end":
			select {
			case waitErr = <-exited:
				gone = true
			case <-time.After(seconds(w, 1, 5)):
			}
		default:
			return bad
		}
	}
	if !gone {
		// Still running at the end of the script: hang up, as a closed window does.
		syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP)
		select {
		case waitErr = <-exited:
		case <-time.After(3 * time.Second):
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			waitErr = <-exited
		}
	}
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
	}
	code := 0
	if waitErr != nil {
		code = cmd.ProcessState.ExitCode()
	}
	t.mu.Lock()
	t.event("x", strconv.Itoa(code))
	t.mu.Unlock()
	return nil
}

func play(timed bool, args []string) error {
	fs := flag.NewFlagSet("play", flag.ExitOnError)
	speed := fs.Float64("speed", 1, "how many times faster than it happened")
	file := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		file, args = args[0], args[1:]
	}
	fs.Parse(args)
	if file == "" {
		return errors.New("which recording?")
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	if !sc.Scan() || sc.Text() != header {
		return fmt.Errorf("%s is not a recording", file)
	}
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	start := time.Now()
	for sc.Scan() {
		p := strings.SplitN(sc.Text(), " ", 3)
		at, err := strconv.ParseFloat(p[0], 64)
		if err != nil || len(p) < 3 {
			if timed && strings.HasPrefix(sc.Text(), "size ") {
				fmt.Fprintf(os.Stderr, "recorder: recorded at %s; a window of another size shows it wrongly\n", p[1])
			}
			continue
		}
		if p[1] != "o" {
			continue
		}
		s, err := strconv.Unquote(p[2])
		if err != nil {
			return fmt.Errorf("%s: a damaged line at %s", file, p[0])
		}
		if timed {
			out.Flush()
			time.Sleep(time.Until(start.Add(time.Duration(at / *speed * float64(time.Second)))))
		}
		out.WriteString(s)
	}
	return sc.Err()
}
