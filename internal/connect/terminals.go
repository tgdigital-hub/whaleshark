package connect

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/pick"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// The clipboard sequence as xterm's list of control sequences gives it (OSC
// 52): this start, the selection's letter, a semicolon, the text in base 64
// and a bell or ESC \ to end it. No copied text is longer than liftMax.
const (
	osc52   = "\x1b]52;"
	liftMax = 2 << 20
)

// lift passes a window's stream on and takes every clipboard sequence out
// of it: this program runs on the computer whose clipboard is meant, so the
// text gets there whatever the terminal around it does with the sequence.
// copy takes the text and returns what the terminal is still to be sent.
type lift struct {
	out  io.Writer
	copy func(text string) []byte
	held []byte // the start of what may be a clipboard sequence, not yet whole
}

func (l *lift) Write(p []byte) (int, error) {
	in := append(l.held, p...)
	var out []byte
	for len(in) > 0 {
		i := bytes.IndexByte(in, 0x1b)
		if i < 0 {
			i = len(in)
		}
		out, in = append(out, in[:i]...), in[i:]
		if len(in) < len(osc52) && strings.HasPrefix(osc52, string(in)) {
			break
		}
		if !bytes.HasPrefix(in, []byte(osc52)) {
			out, in = append(out, in[0]), in[1:]
			continue
		}
		end, n := bytes.IndexByte(in, '\a'), 1
		if st := bytes.Index(in, []byte("\x1b\\")); st >= 0 && (end < 0 || st < end) {
			end, n = st, 2
		}
		if end < 0 {
			if len(in) > liftMax {
				out, in = append(out, in...), nil
			}
			break
		}
		_, data, _ := bytes.Cut(in[len(osc52):end], []byte(";"))
		if text, err := base64.StdEncoding.DecodeString(string(data)); err == nil && len(text) > 0 {
			out = append(out, l.copy(string(text))...)
		}
		in = in[end+n:]
	}
	l.held = append(l.held[:0], in...)
	_, err := l.out.Write(out)
	return len(p), err
}

// terminals is the second window: `ssh -t <target> whaleshark open` with
// this computer's system named, for the keys of copy and paste, run again
// whenever it drops; the keeper has the tab the person left.
func terminals(ctx context.Context, c *contract.Call, target string) error {
	system := c.Kit.Platform.System()
	for try, since := 0, now(); ; {
		l := &lift{out: c.Out, copy: func(text string) []byte {
			// Where this computer has no clipboard program the terminal is asked.
			_, seq := pick.Board{System: system}.Copy(text)
			return seq
		}}
		run, end := context.WithCancel(ctx)
		cmd := ssh(run, slices.Concat([]string{"-t"}, keep, []string{target, "env", contract.EnvSystem + "=" + system, remote, "open"})...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdin, l, c.Err
		// Asked to end, ssh puts the terminal back as it was; killed, it cannot.
		cmd.Cancel, cmd.WaitDelay = func() error { return cmd.Process.Signal(syscall.SIGTERM) }, time.Second
		if err := cmd.Start(); err != nil {
			end()
			return err
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		began, slept, ended := now(), false, false
		for last := began; !ended; last = now() {
			select {
			case <-done:
				ended = true
			case <-time.After(tick):
				// The computer slept: run again now, not when ssh gives up.
				if now().Sub(last) > 3*tick {
					slept = true
					end()
				}
			}
		}
		end()
		l.out.Write(l.held)
		switch n := cmd.ProcessState.ExitCode(); {
		case ctx.Err() != nil || n == 0 && !slept:
			return nil
		case n != 255 && !slept:
			return refuse(contract.ExitEnv, "window_ended", "The window on "+target+" ended with exit "+strconv.Itoa(n)+".",
				"ssh -t "+target+" whaleshark open")
		}
		// The window could not give back what it asked of this terminal.
		io.WriteString(c.Out, term.Leave)
		if slept || now().Sub(began) > time.Minute {
			try, since = 0, now()
		}
		if !slept && !pause(ctx, c.Out, since, &try) {
			return nil
		}
	}
}
