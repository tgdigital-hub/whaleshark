// Package leash starts a program for a test so that it ends when the test's
// own process does, however that process ends. A time limit, a kill and a
// panic in another goroutine run none of a test's cleanups, and a keeper
// started by such a test lived on for hours with nobody's tabs in it.
//
// The program is started by a second process of the test's own program,
// the holder, which the test hands the far end of a pipe. When the pipe
// ends, because the test let go of it or because the test's process is no
// longer there, the holder ends the program and then itself: first as a
// person at the keyboard would, and after five seconds outright.
package leash

import (
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// mark is the holder's first word, which no test and no flag is named;
// after is the first word of a holder that runs its program at the end.
const (
	mark  = "whaleshark-test-leash"
	after = mark + "-after"
)

// patience is how long a program gets to end by itself once it was asked.
const patience = 5 * time.Second

// Command is exec.Command for a program on the leash. The command is the
// holder's: its surroundings and what it prints are the program's, and Wait
// gives the program's end. let ends the program as the end of the test's
// process would; a test that stops its program its own way never calls it.
func Command(name string, args ...string) (cmd *exec.Cmd, let func()) {
	return holder(mark, name, args)
}

// After is a command that runs the program once, when the test's process
// has ended or let is called: for what a test cannot hold, such as a keeper
// the system itself started apart from everything, and can only ask to end.
func After(name string, args ...string) (cmd *exec.Cmd, let func()) {
	return holder(after, name, args)
}

func holder(word, name string, args []string) (*exec.Cmd, func()) {
	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
	}
	cmd := exec.Command(self, append([]string{word, name}, args...)...) // #nosec G204 -- this test's own program
	held, err := cmd.StdinPipe()
	if err != nil {
		panic(err) // a command that was just made has no standard input yet
	}
	return cmd, func() { held.Close() }
}

// A test program that imports this package is the holder when it is
// started as one, before any test of its own is looked at.
func init() {
	if len(os.Args) > 2 && (os.Args[1] == mark || os.Args[1] == after) {
		os.Exit(hold(os.Args[1] == after, os.Args[2], os.Args[3:]))
	}
}

func hold(atTheEnd bool, name string, args []string) int {
	// What ends the test from its terminal must not end the one process
	// that tidies up after it. The signals are taken and dropped, not set
	// to be ignored: that would be handed down to the program and to all
	// it starts.
	signal.Notify(make(chan os.Signal, 1), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	program := exec.Command(name, args...) // #nosec G204 -- the program the test named
	program.Stdout, program.Stderr = os.Stdout, os.Stderr
	if atTheEnd {
		_, _ = io.Copy(io.Discard, os.Stdin)
		_ = program.Run()
		return 0
	}
	if err := program.Start(); err != nil {
		os.Stderr.WriteString("leash: " + err.Error() + "\n")
		return 1
	}
	ended := make(chan error, 1)
	go func() { ended <- program.Wait() }()
	let := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		close(let)
	}()
	select {
	case <-ended:
	case <-let:
		_ = program.Process.Signal(os.Interrupt) // not on every system; the limit below is
		select {
		case <-ended:
		case <-time.After(patience):
			_ = program.Process.Kill()
			<-ended
		}
	}
	if code := program.ProcessState.ExitCode(); code >= 0 {
		return code
	}
	return 1 // ended by a signal
}
