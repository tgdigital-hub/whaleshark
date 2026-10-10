package screen

import (
	"bytes"
	"flag"
	"math/rand/v2"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/test/corpus"
)

func read(w, h int, in string) *Screen {
	s := New(w, h)
	s.Write([]byte(in))
	return s
}

// A case is what is printed into a screen of 10 by 4 and the text it must show.
type shows struct{ name, in, want string }

func run(t *testing.T, cases []shows) {
	for _, c := range cases {
		if got := read(10, 4, c.in).Picture().Text(); got != c.want {
			t.Errorf("%s: %q shows\n%s\nwant\n%s", c.name, c.in, got, c.want)
		}
	}
}

func style(s *Screen, x, y int) contract.Style { return s.Picture().Cells[y*s.w+x].Style }

// The three traps: sequences that end in "m" and are no style. Each was
// seen in a recording; read as a style, the first would underline the screen.
func TestTraps(t *testing.T) {
	for _, in := range []string{"\x1b[>4;2m", "\x1b[>4m", "\x1b[>4;m", "\x1b[?4m", "\x1b[0%m"} {
		if s := read(10, 4, in+"x"); style(s, 0, 0) != (contract.Style{}) {
			t.Errorf("%q changed the style to %+v", in, style(s, 0, 0))
		}
		// And none of them is a reset either.
		if s := read(10, 4, "\x1b[1;4m"+in+"x"); !style(s, 0, 0).Bold || !style(s, 0, 0).Underline {
			t.Errorf("%q reset the style", in)
		}
	}
}

// The kinds of sequence no recording holds, each from its document.
func TestKindsNotRecorded(t *testing.T) {
	run(t, []shows{
		{"next line", "ab\x1b[2Ec", "ab\n\nc"},
		{"previous line", "\n\n\nab\x1b[2Fc", "\nc\n\nab"},
		{"index as an escape", "ab\x1bDc", "ab\n  c"},
		{"index scrolls at the bottom", "a\n\n\nb\x1bDc", "\n\n b\n  c"},
		{"next line as an escape", "ab\x1bEc", "ab\nc"},
		{"save and restore, the other form", "ab\x1b[s\n\ncd\x1b[uX", "abX\n\n  cd"},
		{"erase characters", "abcdef\x1b[4G\x1b[2X", "abc  f"},
		{"erase characters stops at the edge", "abcdef\x1b[4G\x1b[99X", "abc"},
		{"scroll up", "a\r\nb\r\nc\r\nd\x1b[2S", "c\nd"},
		{"scroll down", "a\r\nb\r\nc\r\nd\x1b[T", "\na\nb\nc"},
		{"repeat", "ab\x1b[3b", "abbbb"},
		{"repeat a wide character", "鯨\x1b[2b", "鯨鯨鯨"},
		{"origin mode counts from the region", "\x1b[2;3r\x1b[?6h\x1b[1;1Ha\x1b[9;1Hb", "\na\nb"},
		{"origin mode off counts from the top", "\x1b[2;3r\x1b[?6h\x1b[?6l\x1b[1;1Ha", "a"},
		{"the second screen, 47", "ab\x1b[?47hcd", "  cd"},
		{"47 and back keeps both", "ab\x1b[?47hcd\x1b[?47l", "ab"},
		{"47 does not clear", "\x1b[?47hcd\x1b[?47l\x1b[?47h", "cd"},
		{"1047 clears on the way back", "\x1b[?1047hcd\x1b[?1047l\x1b[?1047h", ""},
		{"1048 saves and restores", "ab\x1b[?1048h\n\ncd\x1b[?1048lX", "abX\n\n  cd"},
		{"wheel as arrows, read whole", "a\x1b[?1007hb\x1b[?1007lc", "abc"},
		{"line drawing", "\x1b(0lqk\x1b(Blqk", "┌─┐lqk"},
		{"line drawing by shift", "\x1b)0a\x0eqx\x0fq", "a─│q"},
		{"colour questions, read whole", "a\x1b]10;?\x07b\x1b]11;?\x1b\\c\x1b]4;1;?\x07d", "abcd"},
		{"the clipboard, read whole", "a\x1b]52;c;aGVsbG8=\x07b\x1b]52;c;?\x1b\\c", "abc"},
		{"a tab stop set", "\x1b[3G\x1bH\ra\tb", "a b"},
		{"a tab stop cleared", "\x1b[9G\x1b[g\ra\tb", "a        b"},
		{"all tab stops cleared", "\x1b[3G\x1bH\x1b[3g\ra\tb", "a        b"},
	})
	for in, want := range map[string]contract.Style{
		"\x1b[3m":                    {Italic: true},
		"\x1b[3;23m":                 {},
		"\x1b[9m":                    {Strike: true},
		"\x1b[9;29m":                 {},
		"\x1b[38:2::1:2:3m":          {Fg: contract.RGB(0x010203)},
		"\x1b[38:2:1:2:3m":           {Fg: contract.RGB(0x010203)},
		"\x1b[48:5:200m":             {Bg: contract.Indexed(200)},
		"\x1b[38:5:9;48:2::4:5:6;1m": {Fg: contract.Indexed(9), Bg: contract.RGB(0x040506), Bold: true},
	} {
		if s := read(10, 4, in+"x"); style(s, 0, 0) != want {
			t.Errorf("%q gives %+v, want %+v", in, style(s, 0, 0), want)
		}
	}
	for in, want := range map[string]string{
		"\x1b]1;one\x07":                              "one",
		"\x1b]2;two\x1b\\":                            "two",
		"\x1b]0;a\u202eb\u0085c\x07":                  "abc",
		"\x1b]2;" + strings.Repeat("é", 300) + "\x07": strings.Repeat("é", 100),
		"\x1b]2;kept\x07\x1b]2;" + strings.Repeat("x", maxStr+1) + "\x07": "kept",
		"\x1b]2;kept\x07\x1b]2;dropped\x18\x07":                           "kept",
		"\x1b]52;c;dGl0bGU=\x07":                                          "",
	} {
		if s := read(10, 4, in); s.Title() != want {
			t.Errorf("%.40q: the title is %.40q, want %.40q", in, s.Title(), want)
		}
	}
}

func TestStyles(t *testing.T) {
	for in, want := range map[string]contract.Style{
		"\x1b[1;2;4;7m":         {Bold: true, Dim: true, Underline: true, Reverse: true},
		"\x1b[1;2;22m":          {},
		"\x1b[4:3m":             {Underline: true},
		"\x1b[4m\x1b[4:0m":      {},
		"\x1b[21m":              {Underline: true},
		"\x1b[4;7;24;27m":       {},
		"\x1b[31;42m":           {Fg: contract.Indexed(1), Bg: contract.Indexed(2)},
		"\x1b[91;102m":          {Fg: contract.Indexed(9), Bg: contract.Indexed(10)},
		"\x1b[38;5;208m":        {Fg: contract.Indexed(208)},
		"\x1b[38;2;10;200;255m": {Fg: contract.RGB(0x0ac8ff)},
		"\x1b[48;2;1;2;3;1m":    {Bg: contract.RGB(0x010203), Bold: true},
		"\x1b[38;2;999;0;0m":    {Fg: contract.RGB(0xff0000)},
		"\x1b[31;39;41;49m":     {},
		"\x1b[1;31m\x1b[m":      {},
		"\x1b[58;2;1;2;3;1m":    {Bold: true},
		"\x1b[58:2::1:2:3;3m":   {Italic: true},
		"\x1b[58;5;1;9m":        {Strike: true},
		"\x1b[38;7m":            {Reverse: true},
		"\x1b[" + strings.Repeat("0;", maxArgs) + "1m":   {},
		"\x1b[" + strings.Repeat("0;", maxArgs-1) + "1m": {Bold: true},
	} {
		if s := read(10, 4, in+"x"); style(s, 0, 0) != want {
			t.Errorf("%.30q gives %+v, want %+v", in, style(s, 0, 0), want)
		}
	}
	// What is erased takes the background in force and nothing else of the style.
	s := read(10, 4, "\x1b[1;44mab\x1b[K\x1b[2;1H\x1b[L")
	if want := (contract.Style{Bg: contract.Indexed(4)}); style(s, 5, 0) != want || style(s, 0, 1) != want {
		t.Errorf("erased cells are %+v and %+v, want %+v", style(s, 5, 0), style(s, 0, 1), want)
	}
}

func TestCursorAndEditing(t *testing.T) {
	run(t, []shows{
		{"up, down, forward, back", "\x1b[3B\x1b[5Ca\x1b[2A\x1b[3Db", "\n   b\n\n     a"},
		{"moves stop at the edges", "\x1b[99B\x1b[99Ca\x1b[99A\x1b[99Db", "b\n\n\n         a"},
		{"column and row", "\x1b[5Ga\x1b[3db", "    a\n\n     b"},
		{"position, both spellings", "\x1b[2;3Ha\x1b[3;2fb", "\n  a\n b"},
		{"words placed, no space printed", "Do\x1b[4Gyou\x1b[8Gsee", "Do you see"},
		{"backspace and return", "abc\b\bX\rY", "YXc"},
		{"tab", "a\tb\t\tc", "a       bc"},
		{"line feed keeps the column", "ab\ncd", "ab\n  cd"},
		{"the screen scrolls", "1\r\n2\r\n3\r\n4\r\n5", "2\n3\n4\n5"},
		{"reverse index scrolls down", "a\r\nb\x1b[H\x1bMc", "c\na\nb"},
		{"save and restore", "ab\x1b7\x1b[1;31m\n\ncd\x1b8X", "abX\n\n  cd"},
		{"restore with nothing saved goes home", "\n\nab\x1b8X", "X\n\nab"},
		{"erase to the end of the line", "abcdef\x1b[3G\x1b[K", "ab"},
		{"erase to the start of the line", "abcdef\x1b[3G\x1b[1K", "   def"},
		{"erase the line", "abcdef\x1b[2K", ""},
		{"erase below", "ab\r\ncd\r\nef\x1b[2;2H\x1b[J", "ab\nc"},
		{"erase above", "ab\r\ncd\r\nef\x1b[2;2H\x1b[1J", "\n\nef"},
		{"erase everything, cursor stays", "ab\r\ncd\x1b[2JX", "\n  X"},
		{"erasing the scroll-back erases nothing", "ab\x1b[3J", "ab"},
		{"insert characters", "abcdef\x1b[3G\x1b[2@", "ab  cdef"},
		{"insert pushes off the edge", "abcdefghij\x1b[3G\x1b[3@", "ab   cdefg"},
		{"delete characters", "abcdef\x1b[3G\x1b[2P", "abef"},
		{"insert lines", "a\r\nb\r\nc\x1b[2;2H\x1b[LX", "a\nX\nb\nc"},
		{"delete lines", "a\r\nb\r\nc\x1b[1;2H\x1b[2MX", "X"},
		{"insert mode", "abc\x1b[2G\x1b[4hXY\x1b[4lZ", "aXYZc"},
		{"a region scrolls alone", "1\r\n2\r\n3\r\n4\x1b[2;3r\x1b[3;1H\nX", "1\n3\nX\n4"},
		{"a region set moves home", "ab\x1b[2;3rX", "Xb"},
		{"a region of one line is refused", "ab\x1b[2;2rX", "abX"},
		{"lines inserted in a region stay in it", "1\r\n2\r\n3\r\n4\x1b[1;3r\x1b[2;1H\x1b[L", "1\n\n2\n4"},
		{"lines outside the region are not inserted", "1\r\n2\r\n3\r\n4\x1b[1;2r\x1b[4;1H\x1b[L", "1\n2\n3\n4"},
		{"up stops at the region", "\x1b[2;3r\x1b[3;1H\x1b[9AX", "\nX"},
		{"down stops at the region", "\x1b[2;3r\x1b[9BX", "\n\nX"},
		{"the second screen and back", "ab\x1b[?1049hcd\x1b[?1049lX", "abX"},
		{"the second screen starts empty", "\x1b[?1049hcd\x1b[?1049l\x1b[?1049hX", "X"},
		{"a control character inside a sequence is obeyed", "abc\x1b[2\rCX", "abX"},
		{"cancel ends a sequence", "a\x1b[31\x18mb\x1b]2;x\x1ac", "ambc"},
		{"an escape ends a sequence and starts one", "a\x1b[31\x1b[2Cb", "a  b"},
		{"a sequence with two intermediates is dropped", "a\x1b[1 !mb", "ab"},
		{"numbers after an intermediate spoil it", "a\x1b[ 5Cb", "ab"},
		{"strings nothing reads", "a\x1bPzz\x1b\\b\x1b_Gi=1;AAAA\x1b\\c\x1b^x\x1b\\d\x1bXy\x1b\\e", "abcde"},
		{"links are dropped, their text stays", "\x1b]8;id=1;https://example.com\x07link\x1b]8;;\x07", "link"},
		{"notices are dropped", "a\x1b]9;hello\x07\x1b]777;notify;t;b\x07\x1b]99;i=1;x\x1b\\b", "ab"},
		{"modes of no interest", "\x1b[?12;1034;2031;2004hab\x1b=\x1b>\x1b[22;2t\x1b[<u\x1b[>5uc", "abc"},
		{"questions draw nothing", "a\x1b[c\x1b[>c\x1b[6n\x1b[>0q\x1b[?u\x1b[?2026$p\x1b[16tb", "ab"},
		{"upper control characters are never drawn", "a\u0085\u009bb\x7fc", "abc"},
		{"bytes that are no text", "a\xffb\xe2\x82c", "a�b��c"},
	})
	if s := read(10, 4, "\x1b[?25l"); s.Picture().CurShown {
		t.Error("the cursor shows after it was hidden")
	}
	if p := read(10, 4, "\x1b[?25l\x1b[?25h\x1b[2;5H").Picture(); !p.CurShown || p.CurX != 4 || p.CurY != 1 {
		t.Errorf("the cursor is at %d,%d shown %v", p.CurX, p.CurY, p.CurShown)
	}
}

// The wrap is held at the last column until the next character: a line
// that exactly fills the row does not move the cursor to the next.
func TestWrapHeld(t *testing.T) {
	run(t, []shows{
		{"a full row and a return", "0123456789\rX", "X123456789"},
		{"a full row and a line break", "0123456789\r\nX", "0123456789\nX"},
		{"the next character wraps", "0123456789X", "0123456789\nX"},
		{"the shell's mark for a missing line break", "%         \r \rX", "X"},
		{"backspace after a full row", "0123456789\bX", "01234567X9"},
		{"a move lets go of the wrap", "0123456789\x1b[1CX", "012345678X"},
		{"wrap switched off", "\x1b[?7l0123456789XY", "012345678Y"},
		{"wrap at the bottom scrolls", "\n\n\n0123456789X", "\n\n0123456789\nX"},
	})
}

func TestWideAndJoined(t *testing.T) {
	run(t, []shows{
		{"wide characters", "日本語ab", "日本語ab"},
		{"a wide character at the edge wraps whole", "12345678\x1b[10G日", "12345678\n日"},
		{"an accent joins its letter", "éx", "éx"},
		{"an emoji joined from four", "👨‍👩‍👧‍👦x", "👨‍👩‍👧‍👦x"},
		{"a flag", "🇩🇪🇫🇷x", "🇩🇪🇫🇷x"},
		{"a mark with nothing to join is dropped", "\x1b[3Ǵx", "  x"},
		{"writing on the right half blanks the left", "日\x1b[2Gx", " x"},
		{"writing on the left half blanks the right", "日\x1b[1Gx", "x"},
		{"erasing half a wide character", "a日b\x1b[3G\x1b[K", "a"},
		{"deleting half a wide character", "日本\x1b[2G\x1b[P", " 本"},
		{"inserting inside a wide character", "日本\x1b[2G\x1b[@", "   本"},
		{"a wide character pushed half off the edge", "12345678日\x1b[1G\x1b[@", " 12345678"},
	})
	s := read(10, 4, "日a👨‍👩‍👧b☺️c")
	p := s.Picture()
	for x, want := range []string{"日", "", "a", "👨‍👩‍👧", "", "b", "☺️", "", "c"} {
		if p.Cells[x].Text != want {
			t.Errorf("cell %d holds %q, want %q", x, p.Cells[x].Text, want)
		}
	}
	if p.CurX != 9 {
		t.Errorf("the cursor is at %d, want 9", p.CurX)
	}
	// Marks piled on one letter stop growing the cell.
	if s := read(10, 4, "a"+strings.Repeat("́", 1000)+"b"); len(s.cells[0].Text) > maxCell || s.cells[1].Text != "b" {
		t.Errorf("a cell of %d bytes, then %q", len(s.cells[0].Text), s.cells[1].Text)
	}
}

// The limits: nothing a program prints makes the reader hold more than a
// fixed amount, or work long for a short sequence.
func TestLimits(t *testing.T) {
	s := New(80, 24)
	start := time.Now()
	for range 200 {
		s.Write([]byte("x\x1b[65535b\x1b[999999999S\x1b[999999999@\x1b[999999999L\x1b[99999999999999999999C"))
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("huge counts took %v", took)
	}
	s.Write([]byte("\x1b]0;" + strings.Repeat("y", 1<<20)))
	s.Write([]byte("\x1b[" + strings.Repeat("1;", 1<<16)))
	s.Write([]byte("\x1bP" + strings.Repeat("z", 1<<20)))
	if cap(s.str) > 2*maxStr {
		t.Errorf("a string of a megabyte left %d bytes held", cap(s.str))
	}
	check(t, s)
	if big := New(1<<30, -5); big.w != maxSide || big.h != 1 {
		t.Errorf("a screen of %d by %d", big.w, big.h)
	}
}

// check fails if the screen has left its bounds: the cursor outside the
// grid, a control character or an overlong text in a cell, or half a wide
// character with no other half.
func check(t testing.TB, s *Screen) {
	t.Helper()
	if s.x < 0 || s.x >= s.w || s.y < 0 || s.y >= s.h || s.top < 0 || s.top >= s.bot && s.h > 1 || s.bot >= s.h {
		t.Fatalf("cursor %d,%d and region %d-%d on a screen of %d by %d", s.x, s.y, s.top, s.bot, s.w, s.h)
	}
	for g := range s.grids {
		if len(s.grids[g]) != s.w*s.h {
			t.Fatalf("a grid of %d cells on a screen of %d by %d", len(s.grids[g]), s.w, s.h)
		}
		for i, c := range s.grids[g] {
			if len(c.Text) > maxCell || !utf8.ValidString(c.Text) {
				t.Fatalf("cell %d holds %q", i, c.Text)
			}
			for _, r := range c.Text {
				if r < 0x20 || r >= 0x7f && r < 0xa0 {
					t.Fatalf("cell %d holds the control character %U", i, r)
				}
			}
			if c.Text == "" && (i%s.w == 0 || s.grids[g][i-1].Text == "") {
				t.Fatalf("cell %d is half a wide character with no other half", i)
			}
		}
	}
	if utf8.RuneCountInString(s.title) > 100 || cap(s.str) > 2*maxStr || len(s.part) > 3 {
		t.Fatalf("held: a title of %d, a string of %d, %d bytes of a character", len(s.title), cap(s.str), len(s.part))
	}
	for _, c := range []cursor{s.cursor, s.saved[0], s.saved[1]} {
		if c.x < 0 || c.x >= s.w || c.y < 0 || c.y >= s.h {
			t.Fatalf("a cursor at %d,%d on a screen of %d by %d", c.x, c.y, s.w, s.h)
		}
	}
	bytes := 0
	for _, line := range s.back[s.first:] {
		bytes += len(line)
	}
	if s.Lines() > s.keep || bytes != s.packed || bytes > maxBack || len(s.tabs) != s.w {
		t.Fatalf("held: %d lines of scroll-back where %d are kept, %d bytes counted as %d, %d tab stops", s.Lines(), s.keep, bytes, s.packed, len(s.tabs))
	}
	if len(s.reply) > maxStr+100 || len(s.events) > maxEvents || len(s.keys) > maxKeys || !fixed.Match(s.reply) {
		t.Fatalf("held: %d events, %d keyboard levels, and the answers %q", len(s.events), len(s.keys), s.reply)
	}
	if p := s.Picture(); len(p.Cells) != s.w*s.h || p.CurX >= s.w || p.CurY >= s.h {
		t.Fatalf("a picture of %d cells, cursor %d,%d, on a screen of %d by %d", len(p.Cells), p.CurX, p.CurY, s.w, s.h)
	}
}

// fixed is every answer the reader may give, and nothing else: numbers,
// and the engine's own name.
var fixed = regexp.MustCompile(`^(\x1b\[\?62;22c|\x1b\[>1;10;0c|\x1b\[0n|\x1b\[\d+;\d+R|\x1b\[\?\d+;[012]\$y|\x1b\[\?[01]u|\x1bP>\|whaleshark 0\x1b\\|\x1b\](10|11|4;\d+);rgb:[0-9a-f]{4}/[0-9a-f]{4}/[0-9a-f]{4}(\x07|\x1b\\))*$`)

// same fails unless the input gives one picture, one scroll-back and the
// same answers and events however it is cut. Half way through, the screen
// is given the size w2 by h2.
func same(t testing.TB, w, h, w2, h2 int, in []byte) {
	t.Helper()
	whole, bytewise := New(w, h), New(w, h)
	whole.Write(in[:len(in)/2])
	whole.Resize(w2, h2)
	whole.Write(in[len(in)/2:])
	for i := range in {
		if i == len(in)/2 {
			bytewise.Resize(w2, h2)
		}
		bytewise.Write(in[i : i+1])
	}
	if len(in) == 0 {
		bytewise.Resize(w2, h2)
	}
	check(t, whole)
	check(t, bytewise)
	if a, b := corpus.Render(whole.Picture()), corpus.Render(bytewise.Picture()); a != b || whole.title != bytewise.title {
		t.Fatalf("%q read whole shows\n%s\nand read byte by byte\n%s", in, a, b)
	}
	if !slices.EqualFunc(whole.back[whole.first:], bytewise.back[bytewise.first:], bytes.Equal) {
		t.Fatalf("%q read whole and byte by byte leaves two scroll-backs", in)
	}
	if string(whole.reply) != string(bytewise.reply) || !slices.Equal(whole.events, bytewise.events) || whole.Modes() != bytewise.Modes() {
		t.Fatalf("%q read whole answers %q, and byte by byte %q; events %v and %v", in, whole.reply, bytewise.reply, whole.events, bytewise.events)
	}
	for i := range whole.Lines() {
		row := whole.Line(i)
		for x, c := range row {
			if len(row) > maxSide || c.Text == "" && (x == 0 || row[x-1].Text == "") || strings.ContainsFunc(c.Text, unicode.IsControl) {
				t.Fatalf("line %d of the scroll-back unpacks to %+v", i, row)
			}
		}
	}
}

func FuzzScreen(f *testing.F) {
	f.Add(uint8(80), uint8(24), uint8(40), uint8(10), []byte("a\x1b[1;31mb\x1b[2;3H日本\x1b[?1049h\x1b]0;t\x07\x1b[2;5r\x1bM\x1b[4h"))
	f.Add(uint8(3), uint8(2), uint8(9), uint8(1), []byte("👨\u200d👩\u200d👧é\x1b[@\x1b[P\x1b[3b\x1b(0qq\x0e\r\n\n\n\x1b[c\x1b[6n\x1b[?2026h\x1b]52;c;aGk=\x07\x1bc"))
	f.Fuzz(func(t *testing.T, w, h, w2, h2 uint8, in []byte) {
		same(t, int(w), int(h), int(w2), int(h2), in)
	})
}

var random = flag.Duration("random", 2*time.Second, "how long TestRandom feeds the reader random input")

// The reader survives random input: for an hour with -random 1h.
func TestRandom(t *testing.T) {
	rnd := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 7))
	pieces := []string{"\x1b[", "\x1b]", "\x1bP", "\x1b_", "\x1b", "\x07", "\x1b\\", "\x18", ";", ":", "?", ">", "$", " ", "m", "H", "r",
		"h", "l", "\r", "\n", "\b", "\t", "\x0e", "\x0f", "日", "🙂", "́", "\u200d", "️", "🇩", "\xe2", "\x9b", "1049", "38", "2", "5", "4", "6", "7", "0",
		"2026", "1007", "c", "n", "u", "q", "p", "!", "<", "=", "S", "T", "L", "M", "\x1bc", "]52;c;aGk=", "]4;1;?", "]11;?", "\n\n\n"}
	var in []byte
	n, long := 0, New(40, 12) // one screen takes everything, so that every state meets every input
	for start := time.Now(); time.Since(start) < *random; n++ {
		in = in[:0]
		for range 1 + rnd.IntN(60) {
			switch rnd.IntN(4) {
			case 0:
				in = append(in, byte(rnd.IntN(256)))
			case 1:
				in = append(in, byte(0x20+rnd.IntN(0x5f)))
			case 2:
				in = append(in, pieces[rnd.IntN(len(pieces))]...)
			default:
				in = utf8.AppendRune(in, rune(rnd.IntN(100000)))
			}
		}
		same(t, 1+rnd.IntN(40), 1+rnd.IntN(12), 1+rnd.IntN(40), 1+rnd.IntN(12), in)
		if long.Write(in); rnd.IntN(8) == 0 {
			long.Resize(1+rnd.IntN(60), 1+rnd.IntN(20))
		}
		check(t, long)
		long.Reply()
		long.Events()
	}
	t.Logf("%d random inputs", n)
}
