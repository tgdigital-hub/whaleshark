package soak

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"math/rand/v2"
	"os"
	"strconv"
	"sync"
	"time"

	"golang.org/x/term"
)

const (
	words = "the whale shark swims slowly through warm water"
	// How an agent ends: two plain lines, the last with no line end, so
	// that they are the last two on its screen.
	lastButOne, last = "agent %d has printed everything", "END %d"
	// An agent prints no faster than this, bytes a second.
	fastest = 1 << 20
)

// printer is an agent's output: every byte counted and summed, whichever
// goroutine prints it.
type printer struct {
	mu sync.Mutex
	tally
	key  string // the key typed last, which every line shows from then on
	done bool
}

func (p *printer) write(b []byte) {
	n, _ := os.Stdout.Write(b)
	p.N, p.Sum = p.N+uint64(n), crc32.Update(p.Sum, crc32.IEEETable, b[:n]) // #nosec G115 -- a length
}

// line is one more line of an agent's output: coloured, numbered, and now
// and then a line further up drawn again in place.
func line(b *bytes.Buffer, rng *rand.Rand, i int, key string) {
	switch rng.IntN(6) {
	case 0:
		fmt.Fprintf(b, "\x1b[3A\r\x1b[2K\x1b[%dG\x1b[7m step %d \x1b[0m\x1b[3B\r", 1+rng.IntN(40), i)
	case 1:
		fmt.Fprintf(b, "\x1b[38;2;%d;%d;%dm%08d 鯨 %s\x1b[0m %s\r\n", rng.IntN(256), rng.IntN(256), rng.IntN(256), i, words, key)
	default:
		fmt.Fprintf(b, "\x1b[38;5;%dm%08d\x1b[0m \x1b[1;4m%s\x1b[0m %s\r\n", rng.IntN(256), i, words, key)
	}
}

// agent is the program in a pane: bursts of up to a megabyte, at a megabyte
// a second at most, with rests between, until the moment given; every key
// typed at it is printed back at once. Then it writes down how much it
// printed and stays until its terminal ends. Its arguments are its number,
// the moment to end as seconds, and the file for the count.
func agent() {
	seed, _ := strconv.ParseUint(os.Args[1], 10, 64)
	end, _ := strconv.ParseInt(os.Args[2], 10, 64)
	// The terminal passes every byte as it is. Left to turn a line feed
	// into two bytes itself, the terminal of macOS now and then sends the
	// carriage return twice, and then the count is the system's.
	if _, err := term.MakeRaw(int(os.Stdin.Fd())); err != nil { // #nosec G115 -- a file's number
		fmt.Fprintln(os.Stderr, "soak agent:", err)
		os.Exit(1)
	}
	var p printer
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		for buf := make([]byte, 64); ; {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}
			p.mu.Lock()
			if !p.done {
				p.key = string(buf[:n])
				p.write(buf[:n])
			}
			p.mu.Unlock()
		}
	}()
	rng := rand.New(rand.NewPCG(seed, 17)) // #nosec G404 -- noise, the same at every run
	var b bytes.Buffer
	for i := 0; time.Now().Unix() < end; {
		size, began, sent := 64<<10+rng.IntN(960<<10), time.Now(), 0
		for sent < size {
			p.mu.Lock()
			for b.Reset(); b.Len() < 16<<10; i++ {
				line(&b, rng, i, p.key)
			}
			p.write(b.Bytes())
			p.mu.Unlock()
			sent += b.Len()
			time.Sleep(time.Until(began.Add(time.Duration(sent) * time.Second / fastest)))
		}
		time.Sleep(time.Duration(100+rng.IntN(1400)) * time.Millisecond)
	}
	p.mu.Lock()
	p.done = true
	p.write(fmt.Appendf(nil, "\x1b[0m\r\n"+lastButOne+"\r\n"+last, seed, seed))
	count := fmt.Appendf(nil, "%d %d", p.N, p.Sum)
	p.mu.Unlock()
	// Under another name first: the test reads the file as soon as it is there.
	if os.WriteFile(os.Args[3]+".part", count, 0o600) == nil {
		os.Rename(os.Args[3]+".part", os.Args[3])
	}
	<-gone
}
