//go:build darwin || linux

package pty

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// What the test of a thousand shells needs of this system.
var (
	shell   = []string{"/bin/sh"}
	sayMark = "echo got-$WS_MARK"
	linger  = "sleep 600 & (trap '' HUP; sleep 600) &"
)

// watch gives a function that lists what is left of a terminal's processes.
func watch(_ *testing.T, p contract.Pty) func() []int {
	t := p.(*terminal)
	sid := t.sid.Load()
	return func() []int {
		other := &terminal{}
		other.sid.Store(sid)
		return other.members()
	}
}

func start(t *testing.T, argv ...string) contract.Pty {
	t.Helper()
	p, err := Start(contract.PtySpec{Argv: argv, Dir: t.TempDir(), Env: []string{"PATH=/usr/bin:/bin", "WS_X=7"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func TestEverythingPrintedIsReadThenTheEnd(t *testing.T) {
	p := start(t, "sh", "-c", "i=0; while [ $i -lt 20000 ]; do echo line-$i-$WS_X; i=$((i+1)); done; exit 3")
	out, err := io.ReadAll(p)
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(out, []byte("-7\r\n")); n != 20000 || !bytes.HasSuffix(out, []byte("line-19999-7\r\n")) {
		t.Fatalf("%d lines of 20000, ending %q", n, out[max(len(out)-30, 0):])
	}
	if code, err := p.Wait(); code != 3 || err != nil {
		t.Fatalf("exit %d, %v", code, err)
	}
}

func TestOnlyTheVariablesGivenAndTheFolder(t *testing.T) {
	t.Setenv("WS_FROM_OUTSIDE", "1")
	p := start(t, "/bin/sh", "-c", "env; pwd; tty")
	out, _ := io.ReadAll(p)
	text := string(out)
	if strings.Contains(text, "WS_FROM_OUTSIDE") || !strings.Contains(text, "WS_X=7") {
		t.Fatalf("variables: %q", text)
	}
	if !strings.Contains(text, "/dev/") || strings.Contains(text, "not a tty") {
		t.Fatalf("no terminal: %q", text)
	}
}

func TestSizeAndTheProgramInFront(t *testing.T) {
	p := start(t, "/bin/sh")
	read := reader(p)
	type_(t, p, "stty size\r")
	read.until(t, "24 80")
	if err := p.Resize(132, 50); err != nil {
		t.Fatal(err)
	}
	type_(t, p, "stty size\r")
	read.until(t, "50 132")
	// The shell names itself as it likes: one system's sh is another shell.
	inFront(t, p, func(name string) bool { return strings.HasSuffix(name, "sh") })
	type_(t, p, "sleep 600\r")
	inFront(t, p, func(name string) bool { return name == "sleep" })
	if p.Resize(0, 10) == nil || p.Resize(10, maxSide+1) == nil {
		t.Fatal("a size outside the bounds was taken")
	}
}

// inFront waits for a program of that name to be in front.
func inFront(t *testing.T, p contract.Pty, want func(string) bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		front, err := p.Front()
		if err == nil && want(front) {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("in front %q, %v", front, err)
		}
	}
}

func TestEndedBySignalAndCloseTwice(t *testing.T) {
	p := start(t, "/bin/sh", "-c", "kill -9 $$")
	if code, err := p.Wait(); code != -1 || err != nil {
		t.Fatalf("exit %d, %v", code, err)
	}
	if p.Close() != nil || p.Close() != nil {
		t.Fatal("close failed")
	}
	if _, err := p.Front(); err == nil {
		t.Fatal("a closed terminal named a program in front")
	}
	if p.Resize(80, 24) == nil {
		t.Fatal("a closed terminal took a size")
	}
}

func TestCloseEndsAReadThatWaits(t *testing.T) {
	p := start(t, "/bin/sh", "-c", "trap '' HUP; sleep 600")
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, p)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	began := time.Now()
	p.Close()
	if took := time.Since(began); took < grace || took > 2*grace {
		t.Fatalf("closing a program that ignores the hang-up took %v", took)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the read never ended")
	}
}

func TestNoFileLeftOpen(t *testing.T) {
	next := func() uintptr {
		f, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		return f.Fd()
	}
	before := next()
	for range 20 {
		p := start(t, "/bin/sh", "-c", "exit 0")
		io.Copy(io.Discard, p)
		p.Close()
	}
	if _, err := Start(contract.PtySpec{Argv: []string{"/nowhere/ws-none"}, Cols: 80, Rows: 24}); err == nil {
		t.Fatal("a program that is not there was started")
	}
	if after := next(); after != before {
		t.Fatalf("the next file's number went from %d to %d", before, after)
	}
}
