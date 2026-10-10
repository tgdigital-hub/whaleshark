package pty

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// The Windows half. What is decided here is the order of the system's calls
// and who waits for whom; the calls themselves are behind host, so that the
// order is tested on every system.

// host is Windows as a pane's terminal needs it. The numbers are handles.
type host interface {
	// Console makes a pseudo-console of that size: what is typed is written
	// to in, and what it shows is read from out.
	Console(cols, rows int) (pc uintptr, in io.WriteCloser, out io.ReadCloser, err error)
	Resize(pc uintptr, cols, rows int) error
	// Shut closes a pseudo-console, which ends the programs attached to it.
	// It may not return before its last picture has been read from out.
	Shut(pc uintptr)
	// Start starts the program attached to the pseudo-console, in a job that
	// holds it and everything it starts.
	Start(pc uintptr, path, line, dir string, env []uint16) (proc, job uintptr, err error)
	Wait(proc uintptr) (code int, err error)
	// End ends every process of the job.
	End(job uintptr)
	Free(handles ...uintptr)
}

type console struct {
	ending
	sys       host
	in        io.WriteCloser
	out       io.ReadCloser
	proc, job uintptr
	mu        sync.Mutex
	pc        uintptr       // 0 once it is shut
	shut      chan struct{} // closed once the pseudo-console is
	close     sync.Once
}

func startConsole(sys host, s contract.PtySpec) (contract.Pty, error) {
	path, err := check(s)
	if err != nil {
		return nil, err
	}
	if ext := strings.ToLower(filepath.Ext(path)); ext == ".bat" || ext == ".cmd" {
		// Windows would hand a script's arguments to its command
		// interpreter, which reads them by rules of its own.
		return nil, errors.New("pty: " + path + " is a script; start its interpreter with it as an argument")
	}
	c := &console{ending: ending{done: make(chan struct{})}, sys: sys, shut: make(chan struct{})}
	if c.pc, c.in, c.out, err = sys.Console(s.Cols, s.Rows); err != nil {
		return nil, err
	}
	if c.proc, c.job, err = sys.Start(c.pc, path, commandLine(s.Argv), s.Dir, envBlock(s.Env)); err != nil {
		go c.release()
		c.Close()
		return nil, err
	}
	go func() {
		c.code, c.err = sys.Wait(c.proc)
		close(c.done)
		// Only shutting the pseudo-console ends what out gives, so the
		// terminal ends with its program.
		c.release()
	}()
	return c, nil
}

// release shuts the pseudo-console, once.
func (c *console) release() {
	c.mu.Lock()
	pc := c.pc
	c.pc = 0
	c.mu.Unlock()
	c.sys.Shut(pc)
	close(c.shut)
}

func (c *console) Read(p []byte) (int, error)  { return c.out.Read(p) }
func (c *console) Write(p []byte) (int, error) { return c.in.Write(p) }

func (c *console) Resize(cols, rows int) error {
	if err := sized(cols, rows); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pc == 0 {
		return os.ErrClosed
	}
	return c.sys.Resize(c.pc, cols, rows)
}

// Front cannot be told: Windows has no program in front.
func (*console) Front() (string, error) { return "", contract.ErrCannotTell }

func (c *console) Close() error {
	c.close.Do(func() {
		c.sys.End(c.job)
		// With nobody reading, the pseudo-console would never finish shutting.
		go func() { _, _ = io.Copy(io.Discard, c.out) }()
		<-c.shut
		c.in.Close()
		c.out.Close()
		c.sys.Free(c.proc, c.job)
	})
	return nil
}

// commandLine joins arguments into the one line a Windows program is
// started with, so that the rules its start-up code splits the line by
// (Microsoft, "Parsing C command-line arguments") give each back unchanged:
// an argument with a blank or a quote goes between quotes, a quote gets a
// backslash, and backslashes are doubled only where a quote follows them.
func commandLine(argv []string) string {
	var b strings.Builder
	for i, arg := range argv {
		if i > 0 {
			b.WriteByte(' ')
		}
		if arg != "" && !strings.ContainsAny(arg, " \t\"") {
			b.WriteString(arg)
			continue
		}
		b.WriteByte('"')
		slashes := 0
		for _, r := range arg {
			if r == '"' {
				b.WriteString(strings.Repeat(`\`, slashes+1))
			}
			if slashes++; r != '\\' {
				slashes = 0
			}
			b.WriteRune(r)
		}
		b.WriteString(strings.Repeat(`\`, slashes) + `"`)
	}
	return b.String()
}

// envBlock is the variables as Windows takes them: each ended by a zero,
// and the whole by one more.
func envBlock(env []string) []uint16 {
	var block []uint16
	for _, v := range env {
		block = append(append(block, utf16.Encode([]rune(v))...), 0)
	}
	if len(env) == 0 {
		block = append(block, 0)
	}
	return append(block, 0)
}
