package term

import (
	"io"
	"time"
)

// EscapeWait is how long a lone Escape may still be the start of a report.
// Escape is a working key in the panes, so it cannot wait longer.
const EscapeWait = 50 * time.Millisecond

// Read sends what r delivers to out as events, and End when r ends.
func Read(r io.Reader, out chan<- Event, wait time.Duration) {
	chunks := make(chan []byte)
	go func() {
		defer close(chunks)
		for {
			b := make([]byte, 4096)
			n, err := r.Read(b)
			if n > 0 {
				chunks <- b[:n]
			}
			if err != nil {
				return
			}
		}
	}()
	var p Parser
	timer := time.NewTimer(wait)
	for open := true; open; {
		var evs []Event
		select {
		case b, ok := <-chunks:
			if open = ok; ok {
				evs = p.Feed(b)
			} else {
				evs = append(p.Flush(), Event{Kind: End})
			}
		case <-timer.C:
			evs = p.Flush()
		}
		for _, ev := range evs {
			out <- ev
		}
		if p.Pending() {
			timer.Reset(wait)
		} else {
			timer.Stop()
		}
	}
}
