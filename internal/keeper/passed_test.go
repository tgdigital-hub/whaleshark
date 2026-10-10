package keeper

import (
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/screen"
)

// Nothing a program prints is passed on. A window is sent only what the
// keeper composes from cells, so a pane's program cannot retitle the
// person's terminal, ask it questions, write its clipboard or send it a
// notice; and the program itself is answered in fixed shapes, never with a
// text it chose.
func TestNothingAProgramPrintsReachesAWindow(t *testing.T) {
	r := newRig(t)
	o := r.attach(100, 30)
	var mu sync.Mutex
	var raw []byte
	seen := screen.New(100, 30) // what the person's terminal shows of it
	go func() {
		for {
			kind, body, err := contract.ReadFrame(o.conn)
			if err != nil {
				return
			}
			if kind == contract.FrameBytes {
				mu.Lock()
				raw = append(raw, body...)
				seen.Write(body)
				mu.Unlock()
			}
		}
	}()
	sent := func() string {
		mu.Lock()
		defer mu.Unlock()
		return string(raw)
	}
	shows := func(text string) func() bool {
		// A frame holds only the cells that changed, so a text is looked for
		// on the terminal's screen and not in the bytes: a letter of it may
		// stand there already.
		return func() bool {
			mu.Lock()
			defer mu.Unlock()
			return strings.Contains(seen.Picture().Text(), text)
		}
	}
	r.until("the first frame", shows("pane=p1"))
	const e = "\x1b"
	asks := e + "]0;PLANTED title\a" + e + "]2;PLANTED title" + e + "\\" + e + "[21t" + e + "[20t" + // a title, and the title asked back
		e + "]52;c;UExBTlRFRA==\a" + e + "]52;c;?\a" + // the clipboard written and read
		e + "P$q m" + e + "\\" + e + "P+q544e" + e + "\\" + "\x05" + e + "]50;?\a" + // questions answered with text
		e + "[6n" + e + "[c" + e + "[>c" + e + "[>q" + e + "]10;?\a" + // questions answered with numbers
		e + "]8;;http://PLANTED.example/\alink" + e + "]8;;\a" + e + "]9;PLANTED notice\a" + e + "]777;notify;PLANTED;x\a" +
		e + "]1337;File=name=UExBTlRFRA==;inline=1:UExBTlRFRA==\a" +
		e + "[?1049h" + e + "[?1000h" + e + "[?2004h" + e + "[>4;2m" + e + "[>1u" + e + "[?1049l"
	hostile := e + "[" + strings.Repeat("1;", 5000) + "m" + e + "]2;" + strings.Repeat("T", 70<<10) + "\a" + // past the reader's limits
		e + "Pq" + strings.Repeat("#0;2;0;0;0", 500) + e + "\\" + e + "P" + strings.Repeat("D", 70<<10) + "\x18" + // a picture; a string never ended
		"\u009b31m\u009d0;PLANTED\u009c\u202eturned\u2066" + "\x9b31m\x9d0;PLANTED\x9c" + // the one-byte forms, and the turning marks
		e + "[999;999H" + e + "[2J" + e + "c"
	f := r.program("p1")
	// The questions first, until their answers are read back: the rig's
	// program prints what it is typed, and that must not fall into the
	// middle of a string that follows.
	f.print(asks)
	r.until("the answers", func() bool { return strings.Contains(f.got(), "\x1b[") })
	r.settle()
	for hostile += "\r\nthe-end-of-it\r\n"; hostile != ""; {
		// In pieces: the rig's program hands on one read's worth of each print.
		n := min(len(hostile), 16<<10)
		f.print(hostile[:n])
		hostile = hostile[n:]
	}
	r.until("the last line", shows("the-end-of-it"))
	got := sent()
	if !utf8.ValidString(got) {
		t.Error("the window was sent bytes that are no text")
	}
	for i := strings.IndexByte(got, 0x1b); i >= 0 && i < len(got)-1; {
		// Every sequence the keeper sends is a control sequence of its own
		// making: none sets a string, and none asks or reports anything.
		end := i + 2 + strings.IndexFunc(got[i+2:], func(c rune) bool { return c >= '@' && c <= '~' })
		if seq := got[i : end+1]; got[i+1] != '[' || strings.ContainsRune("cnqtuRSp", rune(got[end])) {
			t.Fatalf("the window was sent the sequence %q", seq)
		}
		next := strings.IndexByte(got[end:], 0x1b)
		if i = end + next; next < 0 {
			break
		}
	}
	for _, bad := range []string{"PLANTED", "\a", "\x05", "\u009b", "\u009d", "\u202e", "\u2066"} {
		if strings.Contains(got, bad) {
			t.Errorf("the window was sent %q", bad)
		}
	}
	if back := f.got(); strings.Contains(back, "PLANTED") || strings.Contains(back, "title") || !strings.Contains(back, "\x1b[") {
		t.Errorf("the program was answered %q", back)
	}
	boardMu.Lock()
	defer boardMu.Unlock()
	if len(board) != 0 {
		t.Errorf("the clipboard was given %q", board)
	}
}
