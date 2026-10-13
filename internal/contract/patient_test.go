package contract

import (
	"errors"
	"io"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// untold is a connection as Windows now and then leaves one: a read that
// is waiting when the other side closes is never told, and waits out its
// own deadline; a read begun after the close finds the end at once.
type untold struct {
	net.Conn
	closed atomic.Bool
	until  atomic.Pointer[time.Time]
}

func (u *untold) SetReadDeadline(t time.Time) error { u.until.Store(&t); return nil }

func (u *untold) Read([]byte) (int, error) {
	if u.closed.Load() {
		return 0, io.EOF
	}
	for t := u.until.Load(); t == nil || t.IsZero() || time.Now().Before(*t); t = u.until.Load() {
		time.Sleep(time.Millisecond)
	}
	return 0, os.ErrDeadlineExceeded
}

// No read waits for ever for an end it was not told of; a deadline of the
// reader's own still holds, to the moment; and what arrives after a long
// silence arrives whole.
func TestAPatientReadFindsTheEnd(t *testing.T) {
	t.Run("an end that was not told", func(t *testing.T) {
		t.Parallel()
		u := &untold{}
		time.AfterFunc(50*time.Millisecond, func() { u.closed.Store(true) })
		began := time.Now()
		if _, _, err := ReadFrame(Patient(u)); !errors.Is(err, io.EOF) {
			t.Errorf("the end was read as %v", err)
		}
		if took := time.Since(began); took > EndWait+time.Second {
			t.Errorf("the end was found after %v", took)
		}
	})
	t.Run("the reader's own deadline", func(t *testing.T) {
		t.Parallel()
		for _, d := range []time.Duration{EndWait / 4, EndWait + EndWait/2} {
			near, far := net.Pipe()
			defer far.Close()
			p := Patient(near)
			began := time.Now()
			p.SetDeadline(began.Add(d))
			if _, _, err := ReadFrame(p); !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Errorf("a deadline of %v ended the read with %v", d, err)
			}
			if took := time.Since(began); took < d || took > d+time.Second {
				t.Errorf("a deadline of %v ended the read after %v", d, took)
			}
		}
	})
	t.Run("a frame after a long silence", func(t *testing.T) {
		t.Parallel()
		near, far := net.Pipe()
		go func() {
			time.Sleep(EndWait + EndWait/4)
			far.Write([]byte{FrameBytes, 0, 0})
			time.Sleep(EndWait + EndWait/4)
			far.Write([]byte{0, 3, 'a', 'b', 'c'})
			far.Close()
		}()
		p := Patient(near)
		if kind, body, err := ReadFrame(p); err != nil || kind != FrameBytes || string(body) != "abc" {
			t.Errorf("the frame was read as %q %q, %v", kind, body, err)
		}
		if _, _, err := ReadFrame(p); !errors.Is(err, io.EOF) {
			t.Errorf("the end after it was read as %v", err)
		}
	})
}
