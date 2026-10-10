package pty

import (
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// fakeHost stands in for Windows. As there, what the pseudo-console shows
// ends only when it is shut, and shutting it waits until its last picture
// has been read.
type fakeHost struct {
	mu     sync.Mutex
	calls  []string
	fail   string // the call that fails
	exit   chan int
	ended  chan struct{}
	typed  *io.PipeReader
	shown  *io.PipeWriter
	closed []string
}

func newHost(fail string) *fakeHost {
	return &fakeHost{fail: fail, exit: make(chan int, 1), ended: make(chan struct{})}
}

func (h *fakeHost) call(name string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, name)
	if strings.HasPrefix(name, h.fail+" ") {
		return errors.New(h.fail + " failed")
	}
	return nil
}

func (h *fakeHost) log() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.calls, "; ")
}

// end notes that one end of a pipe was closed.
type end struct {
	io.Closer
	h    *fakeHost
	name string
}

func (e end) Close() error {
	e.h.mu.Lock()
	e.h.closed = append(e.h.closed, e.name)
	e.h.mu.Unlock()
	return e.Closer.Close()
}

func (h *fakeHost) Console(cols, rows int) (uintptr, io.WriteCloser, io.ReadCloser, error) {
	if err := h.call("console " + size(cols, rows)); err != nil {
		return 0, nil, nil, err
	}
	typed, in := io.Pipe()
	out, shown := io.Pipe()
	h.typed, h.shown = typed, shown
	return 7, struct {
		io.Writer
		io.Closer
	}{in, end{in, h, "in"}}, struct {
		io.Reader
		io.Closer
	}{out, end{out, h, "out"}}, nil
}

func size(cols, rows int) string { return string(rune('0'+cols)) + "x" + string(rune('0'+rows)) }

func (h *fakeHost) Resize(pc uintptr, cols, rows int) error {
	return h.call("resize " + size(cols, rows))
}

func (h *fakeHost) Shut(pc uintptr) {
	h.call("shut " + string(rune('0'+pc)))
	h.shown.Write([]byte("last picture"))
	h.shown.Close()
}

func (h *fakeHost) Start(pc uintptr, path, line, dir string, env []uint16) (uintptr, uintptr, error) {
	vars := strings.ReplaceAll(string(utf16.Decode(env)), "\x00", "|")
	if err := h.call("start " + path + " [" + line + "] in " + dir + " with " + vars); err != nil {
		return 0, 0, err
	}
	return 8, 9, nil
}

func (h *fakeHost) Wait(proc uintptr) (int, error) {
	select {
	case code := <-h.exit:
		return code, h.call("wait")
	case <-h.ended:
		return 1, h.call("wait")
	}
}

func (h *fakeHost) End(job uintptr) {
	h.call("end " + string(rune('0'+job)))
	if job != 0 {
		close(h.ended)
	}
}

func (h *fakeHost) Free(handles ...uintptr) {
	for _, handle := range handles {
		h.call("free " + string(rune('0'+handle)))
	}
}

var agent = contract.PtySpec{Argv: []string{"bin/agent.exe", "--say", "two words"}, Dir: "work",
	Env: []string{"A=1", "B=é"}, Cols: 9, Rows: 5}

func TestConsoleFromStartToItsProgramsEnd(t *testing.T) {
	h := newHost("nothing")
	p, err := startConsole(h, agent)
	if err != nil {
		t.Fatal(err)
	}
	if front, err := p.Front(); front != "" || !errors.Is(err, contract.ErrCannotTell) {
		t.Fatalf("in front %q, %v", front, err)
	}
	go io.Copy(io.Discard, h.typed)
	if _, err := p.Write([]byte("typed")); err != nil {
		t.Fatal(err)
	}
	if err := p.Resize(8, 4); err != nil {
		t.Fatal(err)
	}
	if p.Resize(0, 4) == nil {
		t.Fatal("a size of nothing was taken")
	}
	go h.shown.Write([]byte("printed, "))
	first := make([]byte, 9)
	if _, err := io.ReadFull(p, first); string(first) != "printed, " || err != nil {
		t.Fatalf("read %q, %v", first, err)
	}
	h.exit <- 5
	if code, err := p.Wait(); code != 5 || err != nil {
		t.Fatalf("exit %d, %v", code, err)
	}
	if out, err := io.ReadAll(p); string(out) != "last picture" || err != nil {
		t.Fatalf("read %q, %v", out, err)
	}
	if err := p.Resize(8, 4); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("a size after the end: %v", err)
	}
	p.Close()
	p.Close()
	want := `console 9x5; start bin/agent.exe [bin/agent.exe --say "two words"] in work with A=1|B=é||; ` +
		"resize 8x4; wait; shut 7; end 9; free 8; free 9"
	if got := h.log(); got != want {
		t.Fatalf("the calls were\n%s\nwant\n%s", got, want)
	}
	if slices.Sort(h.closed); !slices.Equal(h.closed, []string{"in", "out"}) {
		t.Fatalf("closed: %v", h.closed)
	}
}

// Nobody reads here, and the program does not end by itself.
func TestConsoleClosedWithItsProgramRunning(t *testing.T) {
	h := newHost("nothing")
	p, err := startConsole(h, agent)
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() {
		p.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("close never came back")
	}
	if code, err := p.Wait(); code != 1 || err != nil {
		t.Fatalf("exit %d, %v", code, err)
	}
	if got := h.log(); !strings.HasSuffix(got, "end 9; wait; shut 7; free 8; free 9") {
		t.Fatalf("the calls were %s", got)
	}
}

func TestConsoleThatCannotStartLeavesNothing(t *testing.T) {
	h := newHost("console")
	if _, err := startConsole(h, agent); err == nil || h.log() != "console 9x5" {
		t.Fatalf("%v after %s", err, h.log())
	}
	h = newHost("start")
	if _, err := startConsole(h, agent); err == nil || !strings.Contains(h.log(), "shut 7") {
		t.Fatalf("%v after %s", err, h.log())
	}
	if slices.Sort(h.closed); !slices.Equal(h.closed, []string{"in", "out"}) {
		t.Fatalf("closed: %v", h.closed)
	}
	for _, bad := range []contract.PtySpec{
		{Argv: []string{"bin/tool.CMD"}, Cols: 9, Rows: 5},
		{Argv: []string{"bin/tool.bat"}, Cols: 9, Rows: 5},
		{Cols: 9, Rows: 5},
		{Argv: []string{"bin/agent.exe"}},
		{Argv: []string{"bin/agent.exe", "a\x00b"}, Cols: 9, Rows: 5},
		{Argv: []string{"ws-no-such-program"}, Cols: 9, Rows: 5},
	} {
		h = newHost("nothing")
		if _, err := startConsole(h, bad); err == nil || h.log() != "" {
			t.Fatalf("%q: %v after %s", bad.Argv, err, h.log())
		}
	}
}

// What Microsoft's rules make of each line is beside it.
func TestCommandLine(t *testing.T) {
	for _, c := range []struct {
		argv []string
		line string
	}{
		{[]string{`C:\bin\a.exe`, "plain", `back\slash`}, `C:\bin\a.exe plain back\slash`},
		{[]string{"a", ""}, `a ""`},
		{[]string{"two words", "tab\there"}, "\"two words\" \"tab\there\""},
		{[]string{`say "hi"`}, `"say \"hi\""`},
		{[]string{`ends in\`, `a b\`}, `"ends in\\" "a b\\"`},
		{[]string{`a\"b`, `a\\"b`}, `"a\\\"b" "a\\\\\"b"`},
		{[]string{`a\\b c`}, `"a\\b c"`},
	} {
		if got := commandLine(c.argv); got != c.line {
			t.Errorf("%q gave %s, want %s", c.argv, got, c.line)
		}
	}
}

func TestEnvBlock(t *testing.T) {
	if got := envBlock(nil); !slices.Equal(got, []uint16{0, 0}) {
		t.Fatalf("no variables gave %v", got)
	}
	if got := envBlock([]string{"A=1", "B=😀"}); !slices.Equal(got, []uint16{'A', '=', '1', 0, 'B', '=', 0xD83D, 0xDE00, 0, 0}) {
		t.Fatalf("got %v", got)
	}
}
