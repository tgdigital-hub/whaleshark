package term

import (
	"io"
	"reflect"
	"testing"
	"time"
	"unicode/utf8"
)

func key(name string) Event {
	ev := Event{Kind: KeyPress, Key: name}
	if name == "space" {
		ev.Rune = ' '
	} else if utf8.RuneCountInString(name) == 1 {
		ev.Rune, _ = utf8.DecodeRuneInString(name)
	}
	return ev
}

func mouseAt(b Button, a Action, x, y int) Event {
	return Event{Kind: Mouse, Button: b, Action: a, X: x, Y: y}
}

// recorded is what a pane program received through herdr 0.9.1 when these
// things were done to it, byte for byte, and the events they must become;
// with them, the right button and two reports that herdr does not pass on.
var recorded = []struct {
	bytes string
	want  []Event
}{
	{"\x1b[I", []Event{{Kind: Focus, Focused: true}}},
	{"\x1b[<0;5;3M\x1b[<0;5;3m", []Event{mouseAt(Left, Press, 4, 2), mouseAt(Left, Release, 4, 2)}},
	{"\x1b[<64;6;4M\x1b[<65;6;4M", []Event{mouseAt(WheelUp, Press, 5, 3), mouseAt(WheelDown, Press, 5, 3)}},
	{"\x1b[<35;7;5M", []Event{mouseAt(NoButton, Move, 6, 4)}},
	{"\x1b[<0;5;3M\x1b[<32;10;6M\x1b[<32;15;8M\x1b[<0;15;8m", []Event{
		mouseAt(Left, Press, 4, 2), mouseAt(Left, Move, 9, 5), mouseAt(Left, Move, 14, 7), mouseAt(Left, Release, 14, 7)}},
	{"\x1b[<4;5;3M", []Event{{Kind: Mouse, Button: Left, Action: Press, X: 4, Y: 2, Shift: true}}},
	{"\x1b[<8;5;3M", []Event{{Kind: Mouse, Button: Left, Action: Press, X: 4, Y: 2, Alt: true}}},
	{"\x1b[<16;5;3m", []Event{{Kind: Mouse, Button: Left, Action: Release, X: 4, Y: 2, Ctrl: true}}},
	{"\x1b[<1;5;3M", []Event{mouseAt(Middle, Press, 4, 2)}},
	{"\x1b[<2;5;3M\x1b[<2;5;3m\x1b[<34;6;3M", nil}, // the right button, pressed, let go and dragged
	{"a\r\t\x1b[Z", []Event{key("a"), key("enter"), key("tab"), key("shift+tab")}},
	{"\x1b[A\x1b[B\x1b[C\x1b[D", []Event{key("up"), key("down"), key("right"), key("left")}},
	{"\x03\x04\x1a\x16\x01", []Event{key("ctrl+c"), key("ctrl+d"), key("ctrl+z"), key("ctrl+v"), key("ctrl+a")}},
	{"\x1bx\x1bOP\x1b[15~\x1b[24~", []Event{key("alt+x"), key("f1"), key("f5"), key("f12")}},
	{"\x1b[5~\x1b[H\x1b[3~", []Event{key("pageup"), key("home"), key("delete")}},
	{"\x1b[1;5D\x1b[1;2A\x1b[1;3A", []Event{key("ctrl+left"), key("shift+up"), key("alt+up")}},
	{"[]/? S", []Event{key("["), key("]"), key("/"), key("?"), key("space"), key("S")}},
	{"é漢\x7f", []Event{key("é"), key("漢"), key("backspace")}},
	{"\x1b[200~x\ny\x1b[201~", []Event{{Kind: Paste, Text: "x\ny"}}},
	{"\x1b[200~y\x1b[A\x03\x1b[201~n", []Event{{Kind: Paste, Text: "y\x1b[A\x03"}, key("n")}},
	{"\x1b[O", []Event{{Kind: Focus}}},
	{"\x1b[?62;4c\x1b[99~q", []Event{key("q")}}, // reports nobody asked for are dropped
}

func TestRecordedStream(t *testing.T) {
	var stream string
	var want []Event
	for _, r := range recorded {
		var p Parser
		if got := p.Feed([]byte(r.bytes)); !reflect.DeepEqual(got, r.want) || p.Pending() {
			t.Errorf("%q gives %+v (pending %v), want %+v", r.bytes, got, p.Pending(), r.want)
		}
		stream += r.bytes
		want = append(want, r.want...)
	}
	// However a read cuts the stream, the events are the same.
	for cut := 0; cut <= len(stream); cut++ {
		var p Parser
		got := append(p.Feed([]byte(stream[:cut])), p.Feed([]byte(stream[cut:]))...)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("cut at byte %d: %+v, want %+v", cut, got, want)
		}
	}
	var p Parser
	var got []Event
	for i := range len(stream) {
		got = append(got, p.Feed([]byte{stream[i]})...)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("byte by byte: %+v, want %+v", got, want)
	}
}

func TestEscapeIsKeptUntilTheTimerSaysSo(t *testing.T) {
	var p Parser
	if got := p.Feed([]byte("\x1b")); got != nil || !p.Pending() {
		t.Fatalf("a lone Escape must wait: %+v", got)
	}
	if got := p.Flush(); !reflect.DeepEqual(got, []Event{key("esc")}) || p.Pending() {
		t.Fatalf("after the wait it is the Escape key: %+v", got)
	}
	p.Feed([]byte("\x1b"))
	if got := p.Feed([]byte("[A")); !reflect.DeepEqual(got, []Event{key("up")}) {
		t.Fatalf("Escape and the rest of an arrow: %+v", got)
	}
	if got := p.Feed([]byte("\x1b\x1b")); !reflect.DeepEqual(got, []Event{key("esc")}) || !p.Pending() {
		t.Fatalf("two Escapes: %+v", got)
	}
	p.Flush()
	// An unfinished paste waits for its end, however long that takes.
	p.Feed([]byte("\x1b[200~half"))
	if p.Pending() || p.Flush() != nil {
		t.Fatalf("a paste must not be cut off by the timer")
	}
	if got := p.Feed([]byte(" done\x1b[201~")); !reflect.DeepEqual(got, []Event{{Kind: Paste, Text: "half done"}}) {
		t.Fatalf("paste in two reads: %+v", got)
	}
}

// A bare Escape and an arrow key are told apart on a real clock.
func TestBareEscapeAndArrowThroughTheReader(t *testing.T) {
	const wait = 150 * time.Millisecond
	r, w := io.Pipe()
	out := make(chan Event, 8)
	go Read(r, out, wait)
	next := func(what string) Event {
		t.Helper()
		select {
		case ev := <-out:
			return ev
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: no event", what)
			return Event{}
		}
	}

	start := time.Now()
	w.Write([]byte("\x1b"))
	if ev := next("bare escape"); ev.Key != "esc" || time.Since(start) < wait {
		t.Fatalf("bare Escape: %+v after %v, want esc once %v had passed", ev, time.Since(start), wait)
	}
	w.Write([]byte("\x1b"))
	time.Sleep(wait / 10)
	w.Write([]byte("[A"))
	if ev := next("arrow in two reads"); ev.Key != "up" {
		t.Fatalf("arrow in two reads: %+v", ev)
	}
	w.Write([]byte("\x1b[B"))
	if ev := next("arrow in one read"); ev.Key != "down" {
		t.Fatalf("arrow in one read: %+v", ev)
	}
	w.Write([]byte("\x1b"))
	w.Close()
	if a, b := next("escape at the end"), next("end"); a.Key != "esc" || b.Kind != End {
		t.Fatalf("at the end of the input: %+v then %+v", a, b)
	}
	select {
	case ev := <-out:
		t.Fatalf("an event too many: %+v", ev)
	default:
	}
}

func FuzzParser(f *testing.F) {
	for _, r := range recorded {
		f.Add([]byte(r.bytes), 3)
	}
	f.Add([]byte("\x1b[<999999999999999999999;1;1M\x1bO\x1b[;;;~\xff\xc3"), 1)
	f.Add([]byte("\x1b[97:65;2:3;97u\x1b[99999999999;;u\x1b[-5:;:u\x1b[57414;9u"), 9)
	f.Fuzz(func(t *testing.T, b []byte, cut int) {
		var p Parser
		cut = min(max(cut, 0), len(b))
		p.Feed(b[:cut])
		p.Feed(b[cut:])
		p.Flush()
		if p.Pending() {
			t.Fatalf("%q is still kept after the flush", p.buf)
		}
	})
}

// The extended form: Escape with no wait, the keys the old form cannot
// say, the keypad, and nothing for a release or an answer to a question.
func TestExtendedKeys(t *testing.T) {
	for in, want := range map[string]string{
		"\x1b[27u": "esc", "\x1b[13;2u": "shift+enter", "\x1b[9;2u": "shift+tab", "\x1b[127;5u": "ctrl+backspace",
		"\x1b[99;9u": "super+c", "\x1b[118;9u": "super+v", "\x1b[99;5u": "ctrl+c", "\x1b[99;6u": "ctrl+shift+c",
		"\x1b[97;3u": "alt+a", "\x1b[97;4u": "alt+A", "\x1b[97:65;2u": "A", "\x1b[97;2u": "A", "\x1b[49:33;2u": "!",
		"\x1b[32;5u": "ctrl+space", "\x1b[32u": "space", "\x1b[228u": "ä", "\x1b[97;1:1u": "a", "\x1b[97;129u": "a",
		"\x1b[57414u": "enter", "\x1b[57404u": "5", "\x1b[57404;5u": "ctrl+5", "\x1b[57417;2u": "shift+left",
		"\x1b[1;2A": "shift+up", "\x1b[1;9A": "up", "\x1b[13;13u": "super+ctrl+enter",
		"\x1b[97;1:3u": "", "\x1b[?1u": "", "\x1b[57441u": "", "\x1b[u": "", "\x1b[1u": "", "\x1b[155u": "", "\x1b[99999999999u": "",
	} {
		var p Parser
		evs := p.Feed([]byte(in))
		got := ""
		if len(evs) == 1 && evs[0].Kind == KeyPress {
			got = evs[0].Key
		}
		if got != want || len(evs) > 1 || p.Pending() {
			t.Errorf("%q gave %+v, want the key %q", in, evs, want)
		}
	}
	var p Parser
	for in, want := range map[string]rune{"\x1b[97u": 'a', "\x1b[97;2u": 'A', "\x1b[32u": ' ', "\x1b[57404u": '5', "\x1b[97;5u": 0, "\x1b[13u": 0} {
		if evs := p.Feed([]byte(in)); len(evs) != 1 || evs[0].Rune != want {
			t.Errorf("%q typed %+v, want %q", in, evs, want)
		}
	}
}
