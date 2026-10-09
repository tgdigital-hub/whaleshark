package fakeherdr

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A measurement, not a test: it fails on purpose so that the hosted Windows
// machine prints what it found. It is removed once it has been read.
func TestMeasureACutOnWindows(t *testing.T) {
	var out strings.Builder
	say := func(name string, d []time.Duration) {
		slices.Sort(d)
		fmt.Fprintf(&out, "%-46s least %v  middle %v  most %v\n", name, d[0], d[len(d)/2], d[len(d)-1])
	}

	// Two bare connections on a socket file: how long until the reader on
	// one side sees the other side close, with and without a read waiting
	// on the side that closes.
	l, err := net.Listen("unix", filepath.Join(t.TempDir(), "s"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for _, waiting := range []bool{false, true} {
		var dial, closing, seen []time.Duration
		for range 30 {
			began := time.Now()
			client, err := net.Dial("unix", l.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			server, err := l.Accept()
			if err != nil {
				t.Fatal(err)
			}
			dial = append(dial, time.Since(began))
			if waiting {
				go server.Read(make([]byte, 1))
			}
			end := make(chan time.Time)
			go func() {
				client.Read(make([]byte, 1))
				end <- time.Now()
			}()
			time.Sleep(5 * time.Millisecond)
			began = time.Now()
			server.Close()
			closing = append(closing, time.Since(began))
			select {
			case at := <-end:
				seen = append(seen, at.Sub(began))
			case <-time.After(5 * time.Second):
				seen = append(seen, time.Hour)
			}
			client.Close()
		}
		kind := fmt.Sprintf("bare, a read waiting on the closer %v: ", waiting)
		say(kind+"dial", dial)
		say(kind+"close returns", closing)
		say(kind+"the other side sees it", seen)
	}

	// The fake itself: how long a subscription takes, and how long until
	// its channel ends after a cut.
	f := start(t)
	var subscribe, cut []time.Duration
	for range 30 {
		began := time.Now()
		_, events, err := f.Events(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		subscribe = append(subscribe, time.Since(began))
		time.Sleep(5 * time.Millisecond)
		began = time.Now()
		f.Cut()
		ended := make(chan bool)
		go func() {
			for range events {
			}
			close(ended)
		}()
		select {
		case <-ended:
			cut = append(cut, time.Since(began))
		case <-time.After(5 * time.Second):
			cut = append(cut, time.Hour)
		}
	}
	say("the fake: a subscription", subscribe)
	say("the fake: a cut is seen", cut)
	t.Fatalf("measured:\n%s", out.String())
}
