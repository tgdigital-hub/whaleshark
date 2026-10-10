package screen

import (
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/test/corpus"
)

// text is a row of cells as a person reads it.
func text(row []contract.Cell) string {
	return (&contract.Picture{W: len(row), H: 1, Cells: row}).Text()
}

// history is the scroll-back as lines of text.
func history(s *Screen) string {
	var lines []string
	for i := range s.Lines() {
		lines = append(lines, text(s.Line(i)))
	}
	return strings.Join(lines, "|")
}

// What leaves the top of the first screen is kept, and nothing else is.
func TestScrollback(t *testing.T) {
	for _, c := range []shows{
		{"lines that scroll off", "1\r\n2\r\n3\r\n4\r\n5\r\n6", "1|2"},
		{"nothing has left yet", "1\r\n2\r\n3\r\n4", ""},
		{"a wrapped line is two", "0123456789ab\n\n\n\n", "0123456789|ab"},
		{"an empty line is kept", "a\r\n\r\nb\r\n\n\n\n", "a||b"},
		{"index as an escape", "a\x1b[4;1H\x1bD\x1bD", "a|"},
		{"scroll up", "a\r\nb\r\nc\x1b[2S", "a|b"},
		{"scroll up by more than the screen", "a\r\nb\x1b[99S", "a|b||"},
		{"scroll down keeps nothing", "a\r\nb\x1b[2T", ""},
		{"a region from the top row", "a\r\nb\r\nc\r\nd\x1b[1;3r\x1b[3;1H\n\n", "a|b"},
		{"a region lower down", "a\r\nb\r\nc\r\nd\x1b[2;4r\x1b[4;1H\n\n", ""},
		{"lines the program deletes", "a\r\nb\x1b[H\x1b[2M", ""},
		{"the second screen", "\x1b[?1049ha\n\n\n\n\n\n", ""},
		{"the first screen under the second", "a\n\n\n\n\x1b[?1049hx\n\n\n\n\n\x1b[?1049l\n", "a|"},
		{"erasing the screen keeps nothing", "a\r\nb\x1b[2J\x1b[3J", ""},
		{"a full reset keeps what was kept", "a\n\n\n\nb\x1bc", "a"},
	} {
		if got := history(read(10, 4, c.in)); got != c.want {
			t.Errorf("%s: %q keeps %q, want %q", c.name, c.in, got, c.want)
		}
	}

	// A line comes back cell for cell: styles, wide and joined characters,
	// a blank inside it; only the plain blanks at its end are gone.
	s := read(14, 2, "\x1b[1;3;38;2;1;2;3;48;5;200ma日\x1b[0;2;4;7;9;31mé 👨\u200d👩\u200d👧\x1b[mb  \x1b[44m \x1b[m  ")
	want := slices.Clone(s.Picture().Cells[:11])
	s.Write([]byte("\n\n"))
	if got := s.Line(0); !slices.Equal(got, want) {
		t.Errorf("the line came back as\n%+v\nwant\n%+v", got, want)
	}
	if s.Line(-1) != nil || s.Line(1) != nil {
		t.Error("a line that is not there is not nil")
	}

	// How many lines are kept is a setting; the oldest go first.
	s = New(10, 2)
	s.Scrollback(3)
	for _, l := range "abcdefg" {
		s.Write([]byte(string(l) + "\r\n"))
	}
	if got := history(s); got != "d|e|f" {
		t.Errorf("of seven lines, three kept: %q", got)
	}
	if s.Scrollback(1); history(s) != "f" {
		t.Errorf("cut to one line: %q", history(s))
	}
	s.Scrollback(0)
	if s.Write([]byte("x\r\ny\r\nz\r\n")); s.Lines() != 0 {
		t.Errorf("%d lines kept where none are to be", s.Lines())
	}
	check(t, s)

	// The bytes are bounded whatever a line holds: every cell full to its limit.
	s = New(maxSide, 2)
	s.Write([]byte(strings.Repeat("a"+strings.Repeat("́", 31), maxSide)))
	for range 300 {
		s.push(s.cells[:maxSide])
	}
	if check(t, s); s.packed > maxBack || s.Lines() < 100 {
		t.Errorf("%d bytes of scroll-back in %d lines", s.packed, s.Lines())
	}
}

// The scroll-back of what real programs print is about a byte a cell.
func TestScrollbackIsCompact(t *testing.T) {
	recs, err := corpus.All(corpus.Sets()[0])
	if err != nil {
		t.Fatal(err)
	}
	cells, bytes := 0, 0
	for _, rec := range recs {
		s := New(rec.W, rec.H)
		for _, out := range rec.Out {
			s.Write(out)
		}
		for i := range s.Lines() {
			cells += len(s.Line(i))
		}
		bytes += s.packed
	}
	if cells < 10000 || float64(bytes) > 1.5*float64(cells) {
		t.Errorf("%d cells of scroll-back in %d bytes", cells, bytes)
	}
}

// A person scrolling back sees the lines above the screen, then the screen.
func TestView(t *testing.T) {
	s := read(10, 3, "1\r\n2\r\n3\r\n4\r\n\x1b[31m日本語日本\x1b[m\r\n6\r\n7\x1b[H")
	if s.View(0).Text() != "日本語日本\n6\n7" || s.View(-3).Text() != s.View(0).Text() {
		t.Errorf("not scrolled, it shows %q", s.View(0).Text())
	}
	for back, want := range map[int]string{1: "4\n日本語日本\n6", 3: "2\n3\n4", 4: "1\n2\n3", 99: "1\n2\n3"} {
		if got := s.View(back).Text(); got != want {
			t.Errorf("scrolled back %d it shows %q, want %q", back, got, want)
		}
	}
	if v := s.View(1); !v.CurShown || v.CurY != 1 || v.Cells[10].Style.Fg != contract.Indexed(1) {
		t.Errorf("scrolled back one line: cursor row %d shown %v, colour %v", v.CurY, v.CurShown, v.Cells[10].Style.Fg)
	}
	if s.View(3).CurShown {
		t.Error("the cursor shows though its row has left the view")
	}
	// A line wider than the screen is cut, and never through a wide character.
	s = read(10, 2, "日本語日本\r\nx\r\ny")
	s.Resize(5, 2)
	if v := s.View(1); v.Text() != "日本\nx" || v.Cells[4].Text != " " {
		t.Errorf("after narrowing, scrolled back, it shows %q", v.Text())
	}
}

func TestResize(t *testing.T) {
	type size struct {
		in   string
		w, h int
		want string
	}
	for name, c := range map[string]size{
		"narrower cuts":                             {"abcdefghij\r\nxy", 4, 4, "abcd\nxy"},
		"wider pads":                                {"abcdefghij\r\nxy", 20, 4, "abcdefghij\nxy"},
		"a wide character is never cut in two":      {"abc日e", 4, 4, "abc"},
		"shorter cuts below the cursor":             {"a\r\nb\r\nc\r\nd\x1b[2;1H", 10, 2, "a\nb"},
		"shorter keeps the line being typed":        {"a\r\nb\r\nc\r\nd", 10, 2, "c\nd"},
		"taller pads at the bottom":                 {"a\r\nb\r\nc\r\nd", 10, 6, "a\nb\nc\nd"},
		"the second screen is cut at the bottom":    {"\x1b[?1049ha\r\nb\r\nc\r\nd", 10, 2, "a\nb"},
		"the first under the second keeps its line": {"a\r\nb\r\nc\r\nd\x1b[?1049hx", 10, 2, "c\nd"},
	} {
		s := read(10, 4, c.in)
		s.Resize(c.w, c.h)
		check(t, s)
		if strings.Contains(name, "under") {
			s.Write([]byte("\x1b[?1049l"))
		}
		if got := s.Picture().Text(); got != c.want {
			t.Errorf("%s: %q at %dx%d shows %q, want %q", name, c.in, c.w, c.h, got, c.want)
		}
	}
	// The rows that leave the top go to the scroll-back, and the cursor follows.
	s := read(10, 4, "a\r\nb\r\nc\r\nd")
	if s.Resize(10, 2); history(s) != "a|b" || s.y != 1 || s.x != 1 {
		t.Errorf("kept %q, cursor %d,%d", history(s), s.x, s.y)
	}
	// The region is the whole screen again unless it still fits.
	for in, want := range map[string][4]int{
		"\x1b[2;4r": {10, 3, 1, 2}, // one that reached the bottom still does
		"\x1b[2;3r": {10, 3, 1, 2},
		"":          {10, 8, 0, 7},
		"\x1b[3;4r": {10, 2, 0, 1},
	} {
		s := read(10, 4, in)
		if s.Resize(want[0], want[1]); s.top != want[2] || s.bot != want[3] {
			t.Errorf("%q at %dx%d: the region is %d to %d", in, want[0], want[1], s.top, s.bot)
		}
		check(t, s)
	}
	// A saved cursor moves with its screen and stays inside it.
	s = read(10, 4, "\x1b[4;10H\x1b7")
	s.Resize(5, 2)
	if s.Write([]byte("\x1b8X")); s.Picture().Text() != "\n    X" {
		t.Errorf("restored after a resize, the cursor wrote %q", s.Picture().Text())
	}
	// New columns have their tab stops; the old keep what the program set.
	for in, want := range map[string]string{"": "        X       X", "\x1b[3g": "                X  X"} {
		s := read(10, 4, in)
		s.Resize(20, 4)
		if s.Write([]byte("\tX\tX")); s.Picture().Text() != want {
			t.Errorf("%q, widened: tabs give %q, want %q", in, s.Picture().Text(), want)
		}
	}
	if s := New(10, 4); true {
		s.Resize(1<<30, -3)
		if check(t, s); s.w != maxSide || s.h != 1 {
			t.Errorf("a screen of %d by %d", s.w, s.h)
		}
	}
}

// Every question that has an answer, and the answer, in the order asked.
func TestAnswers(t *testing.T) {
	for in, want := range map[string]string{
		"\x1b[c":                              "\x1b[?62;22c",
		"\x1b[0c":                             "\x1b[?62;22c",
		"\x1b[>c":                             "\x1b[>1;10;0c",
		"\x1b[>0q":                            "\x1bP>|whaleshark 0\x1b\\",
		"\x1b[5n":                             "\x1b[0n",
		"\x1b[6n":                             "\x1b[1;1R",
		"\x1b[3;7H\x1b[6n":                    "\x1b[3;7R",
		"0123456789\x1b[6n":                   "\x1b[1;10R",
		"\x1b[2;4r\x1b[?6h\x1b[2;2H\x1b[6n":   "\x1b[2;2R",
		"\x1b[?2026$p":                        "\x1b[?2026;2$y",
		"\x1b[?2026h\x1b[?2026$p":             "\x1b[?2026;1$y",
		"\x1b[?1016$p":                        "\x1b[?1016;0$y",
		"\x1b[?25$p\x1b[?25l\x1b[?25$p":       "\x1b[?25;1$y\x1b[?25;2$y",
		"\x1b[?1049$p\x1b[?1049h\x1b[?1049$p": "\x1b[?1049;2$y\x1b[?1049;1$y",
		"\x1b[?1002h\x1b[?1000$p\x1b[?1002$p": "\x1b[?1000;2$y\x1b[?1002;1$y",
		"\x1b[?2004$p\x1b[?7$p\x1b[?1007$p":   "\x1b[?2004;2$y\x1b[?7;1$y\x1b[?1007;1$y",
		"\x1b[?u":                             "\x1b[?0u",
		"\x1b[>5u\x1b[?u":                     "\x1b[?1u",
		"\x1b[>4u\x1b[?u":                     "\x1b[?0u",
		"\x1b]10;?\a":                         "\x1b]10;rgb:e5e5/e5e5/e5e5\a",
		"\x1b]11;?\x1b\\":                     "\x1b]11;rgb:0000/0000/0000\x1b\\",
		"\x1b]4;1;?\a":                        "\x1b]4;1;rgb:cdcd/0000/0000\a",
		"\x1b]4;12;?;196;?;255;?\a":           "\x1b]4;12;rgb:5555/5555/ffff\a\x1b]4;196;rgb:ffff/0000/0000\a\x1b]4;255;rgb:eeee/eeee/eeee\a",
		"\x1b]4;256;?;x;?;7\a":                "",
		// What an agent asks as it starts: the answer to "what are you" comes last.
		"\x1b[>0q\x1b[?u\x1b]7501;?\a\x1b[c": "\x1bP>|whaleshark 0\x1b\\\x1b[?0u\x1b[?62;22c",
		// Never answered: the cell size, the key form, the picture probe, the
		// title, the clipboard, a status string, a font, an unknown command,
		// and the forms of a question that mean something else.
		"\x1b[16t\x1b[?4m\x1b_Gi=31,a=q;AAAA\x1b\\\x1b[21t\x1b]52;c;?\a\x1bP$qm\x1b\\\x1b]50;?\a\x1b]7501;?\a\x1b[1c\x1b[>1q\x1b[?6n\x1b]10;x\a\x05": "",
	} {
		s := read(10, 4, in)
		if got := string(s.Reply()); got != want {
			t.Errorf("%q is answered %q, want %q", in, got, want)
		}
		if again := s.Reply(); len(again) != 0 {
			t.Errorf("%q is answered twice", in)
		}
	}
	s := New(10, 4)
	s.Fg, s.Bg = 0x102030, 0xfffefd
	s.Write([]byte("\x1b]10;?\a\x1b]11;?\a"))
	if got := string(s.Reply()); got != "\x1b]10;rgb:1010/2020/3030\a\x1b]11;rgb:ffff/fefe/fdfd\a" {
		t.Errorf("the colours set are answered %q", got)
	}
	// A program that asks without end is not answered without end.
	s.Write([]byte(strings.Repeat("\x1b[>0q", 100000)))
	if check(t, s); len(s.Reply()) > maxStr+100 {
		t.Error("answers pile up past the limit")
	}
}

// What the terminal of a recording answered, the reader answers too, in the
// same order: every program recorded went on from those very bytes.
func TestAnswersAsRecorded(t *testing.T) {
	files, answered := 0, 0
	for _, set := range corpus.Sets() {
		names, _ := fs.Glob(set, "recordings/*.rec")
		for _, name := range names {
			data, err := fs.ReadFile(set, name)
			if err != nil {
				t.Fatal(err)
			}
			var given, want []string
			s := New(1, 1)
			for i, row := range strings.Split(string(data), "\n") {
				_, rest, _ := strings.Cut(row, " ")
				if i == 1 {
					var w, h int
					fmt.Sscanf(row, "size %dx%d", &w, &h)
					s = New(w, h)
				} else if i > 3 && (strings.HasPrefix(rest, "o ") || strings.HasPrefix(rest, "i ")) {
					b, err := strconv.Unquote(rest[2:])
					if err != nil {
						t.Fatal(name, err)
					}
					if rest[0] == 'o' {
						s.Write([]byte(b))
						given = append(given, string(s.Reply()))
					} else if b != "" && fixed.MatchString(b) {
						want = append(want, fixed.FindAllString(b, -1)...)
					}
				}
			}
			all := strings.Join(given, "")
			for _, w := range want {
				at := strings.Index(all, w)
				if at < 0 {
					t.Errorf("%s: its terminal answered %q; the reader, from there on, only %q", name, w, all)
					break
				}
				all = all[at+len(w):]
			}
			files, answered = files+1, answered+len(want)
		}
	}
	if files < 28 || answered < 3 {
		t.Errorf("%d recordings read, %d answers compared", files, answered)
	}
	t.Logf("%d recordings, %d answers", files, answered)
}

// A frame the program asked to have shown in one go is not seen half drawn.
func TestHold(t *testing.T) {
	now := time.Unix(0, 0)
	s := New(10, 4)
	s.now = func() time.Time { return now }
	s.Write([]byte("ab\x1b[?2026h\x1b[?25l\rXY\r\ncd"))
	if p := s.Picture(); !s.Held() || p.Text() != "ab" || p.CurX != 2 || p.CurY != 0 || !p.CurShown {
		t.Errorf("inside a frame the picture is %q, cursor %d,%d", p.Text(), p.CurX, p.CurY)
	}
	if s.Write([]byte("\x1b[?2026l")); s.Held() || s.Picture().Text() != "XY\ncd" || s.Picture().CurShown {
		t.Errorf("after the frame the picture is %q", s.Picture().Text())
	}
	// A frame that never ends is shown after a second, and a second mark
	// inside it does not start the second again.
	s.Write([]byte("\x1b[?2026h!"))
	now = now.Add(holdFor - time.Millisecond)
	s.Write([]byte("\x1b[?2026h?"))
	if !s.Held() || s.Picture().Text() != "XY\ncd" {
		t.Errorf("just under a second into a frame: held %v, %q", s.Held(), s.Picture().Text())
	}
	if now = now.Add(time.Millisecond); s.Held() || s.Picture().Text() != "XY\ncd!?" {
		t.Errorf("a second into a frame: held %v, %q", s.Held(), s.Picture().Text())
	}
	// A new size and either reset end a frame.
	for _, end := range []func(){func() { s.Resize(12, 4) }, func() { s.Write([]byte("\x1bc")) }, func() { s.Write([]byte("\x1b[!p")) }} {
		s.Write([]byte("\x1b[?2026hz"))
		if end(); s.Held() {
			t.Error("a frame is still held")
		}
		check(t, s)
	}
}

// What a program asks for is known to whoever hands it keys and the mouse.
func TestModes(t *testing.T) {
	plain := Modes{WheelKeys: true}
	for in, want := range map[string]Modes{
		"":                                    plain,
		"\x1b[?1h\x1b=":                       {WheelKeys: true, CursorKeys: true, Keypad: true},
		"\x1b[?1h\x1b=\x1b[?1l\x1b>":          plain,
		"\x1b[?1000h":                         {WheelKeys: true, Mouse: 1000},
		"\x1b[?1000;1002;1003;1006h":          {WheelKeys: true, Mouse: 1003, MouseSGR: true},
		"\x1b[?1003h\x1b[?1000l":              plain,
		"\x1b[?1016h\x1b[?1016l":              plain,
		"\x1b[?1004;2004h":                    {WheelKeys: true, Focus: true, Paste: true},
		"\x1b[?1004;2004h\x1b[?1004;2004l":    plain,
		"\x1b[?1007l":                         {},
		"\x1b[?1007l\x1b[?1007h":              plain,
		"\x1b[?1049h":                         {WheelKeys: true, Second: true},
		"\x1b[?47h\x1b[?47l":                  plain,
		"\x1b[>1u":                            {WheelKeys: true, Keys: 1},
		"\x1b[>5u":                            {WheelKeys: true, Keys: 1},
		"\x1b[>1u\x1b[<u":                     plain,
		"\x1b[>1u\x1b[>0u\x1b[<u":             {WheelKeys: true, Keys: 1},
		"\x1b[>1u\x1b[>1u\x1b[<2u":            plain,
		"\x1b[>1u\x1b[<9u":                    plain,
		"\x1b[<u":                             plain,
		"\x1b[=1u":                            {WheelKeys: true, Keys: 1},
		"\x1b[=1u\x1b[=1;3u":                  plain,
		"\x1b[=1;2u\x1b[=0;2u":                {WheelKeys: true, Keys: 1},
		"\x1b[>4;2m":                          plain,
		"\x1b[?1;1004h\x1b=\x1b[>1u\x1b[!p":   plain,
		"\x1b[?1049;1003h\x1b[?1007l\x1bc":    plain,
		"\x1b[?1049h\x1b[?2004h\x1b[!p":       {WheelKeys: true, Second: true},
		strings.Repeat("\x1b[>1u", 40) + "\a": {WheelKeys: true, Keys: 1},
	} {
		s := read(10, 4, in)
		if got := s.Modes(); got != want {
			t.Errorf("%.40q: the modes are %+v, want %+v", in, got, want)
		}
		check(t, s)
	}
}

// The bell, and a program's copy to the clipboard, are handed on; nothing
// else a program prints is.
func TestEvents(t *testing.T) {
	for in, want := range map[string][]Event{
		"a\ab":                                {{Kind: Bell}},
		"\a\a\a":                              {{Kind: Bell}},
		"\x1b]0;title\a\x1b]9;notice\a":       nil,
		"\x1b]52;c;aGVsbG8=\a":                {{Kind: Copy, Text: "hello"}},
		"\x1b]52;;aGVsbG8=\x1b\\":             {{Kind: Copy, Text: "hello"}},
		"\x1b]52;c;?\a":                       nil,
		"\x1b]52;c;\a":                        nil,
		"\x1b]52;c;not base64\a":              nil,
		"\x1b]52;c;YQpiCWMbWzMxbQdk/w==\a":    {{Kind: Copy, Text: "a\nb\tc[31md"}},
		"\a\x1b]52;c;YQ==\a\a\a\x1b[?1049h\a": {{Kind: Bell}, {Kind: Copy, Text: "a"}, {Kind: Bell}},
		"\x1b]52;c;" + strings.Repeat("YWFh", maxStr/4) + "\a": nil,
	} {
		s := read(10, 4, in)
		if got := s.Events(); !slices.Equal(got, want) {
			t.Errorf("%.40q: the events are %q, want %q", in, got, want)
		}
		if len(s.Events()) != 0 {
			t.Errorf("%.40q: the events are handed on twice", in)
		}
	}
	s := read(10, 4, strings.Repeat("\x1b]52;c;YQ==\a", 1000))
	if check(t, s); len(s.Events()) != maxEvents {
		t.Error("events pile up past the limit")
	}
}

// A full reset empties the pane and a soft one puts the modes back: after
// either, what is printed next is plain text from where a program expects.
func TestResets(t *testing.T) {
	mess := "ab\r\ncd\x1b[1;31;44m\x1b[2;3r\x1b[?6h\x1b[?25l\x1b[?7l\x1b[4h\x1b(0\x1b[3g\x1b7"
	s := read(10, 4, "\x1b]2;kept\a"+mess+"\x1b[?1049hzz\x1bcq\tq")
	if p := s.Picture(); p.Text() != "q       q" || !p.CurShown || s.Modes() != (Modes{WheelKeys: true}) || s.Title() != "kept" {
		t.Errorf("after a full reset: %q, modes %+v, title %q", p.Text(), s.Modes(), s.Title())
	}
	if style(s, 0, 0) != (contract.Style{}) || style(s, 5, 2) != (contract.Style{}) {
		t.Errorf("after a full reset a cell is drawn %+v and a blank %+v", style(s, 0, 0), style(s, 5, 2))
	}
	if s.Write([]byte("\x1b[?47h")); s.Picture().Text() != "" {
		t.Errorf("a full reset left the second screen showing %q", s.Picture().Text())
	}
	// A soft reset leaves what is drawn and where the cursor stands.
	s = read(10, 4, mess+"\x1b[!pq0123456789\x1b8X\x1b[4;1HY")
	if got := s.Picture().Text(); got != "Xb\nq012345678\n9\nY" {
		t.Errorf("after a soft reset: %q", got)
	}
	if style(s, 0, 1) != (contract.Style{}) || !s.Picture().CurShown {
		t.Errorf("after a soft reset a cell is drawn %+v", style(s, 0, 1))
	}
	check(t, s)
}

// The four kinds no recording holds that were only read whole until now:
// each does what its document says.
func TestKindsDeepened(t *testing.T) {
	s := read(10, 4, "\x1b[?1007l")
	if s.Modes().WheelKeys {
		t.Error("the wheel is still arrow keys after a program switched that off")
	}
	if s.Write([]byte("\x1b[?1007h")); !s.Modes().WheelKeys {
		t.Error("the wheel is not arrow keys after a program asked for it")
	}
	s.Write([]byte("\x1b]10;?\a\x1b]11;?\a\x1b]4;2;?\a\x1b]52;c;Y29waWVk\a\x1b]52;c;?\a"))
	if got := string(s.Reply()); got != "\x1b]10;rgb:e5e5/e5e5/e5e5\a\x1b]11;rgb:0000/0000/0000\a\x1b]4;2;rgb:0000/cdcd/0000\a" {
		t.Errorf("the colour questions are answered %q", got)
	}
	if got := s.Events(); !slices.Equal(got, []Event{{Copy, "copied"}}) {
		t.Errorf("the clipboard sequence gives %q", got)
	}
}
