package contract

import (
	"strings"
	"testing"
	"unicode"
)

// Every character there is, put into a note one at a time: none comes out
// of SayText as a control character, a line or paragraph break, a mark that
// turns or joins invisibly, a private or unassigned one, or any space but
// the plain one; and whatever the note begins with, the typed line begins
// with the tool's own words, so nothing of the note stands where an agent
// tool reads a command, a shell escape or a memory mark.
func TestNoCharacterOfANoteIsMoreThanAWord(t *testing.T) {
	bad := func(r rune) bool {
		return r != ' ' && unicode.In(r, unicode.C, unicode.Z)
	}
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if r >= 0xd800 && r <= 0xdfff {
			continue
		}
		if out := SayText("a" + string(r) + "b"); strings.ContainsFunc(out, bad) || len([]rune(out)) > 3 {
			t.Fatalf("U+%04X comes out of a note as %q", r, out)
		}
	}
	for _, r := range []rune{'\n', '\r', 0x1b, 0, 0x7f, 0x85, 0x9b, 0x9d, 0xa0, 0xad, 0x61c, 0x200b, 0x200d, 0x200e, 0x200f, 0x2028, 0x2029, 0x202e, 0x2060, 0x2066, 0x2069, 0xfeff, 0xe0001, 0xe007f, 0xe000} {
		if out := SayText("a" + string(r) + "b"); out != "a b" && out != "ab" {
			t.Errorf("U+%04X: %q", r, out)
		}
	}
	for _, head := range []string{"/exit", "!rm -r x", "#remember this", "\n/exit", "\x1b[200~/exit", "\u202e/exit", " /exit", "\t!x", "\u200b#x"} {
		for where, want := range sayFrom {
			line := Said(where, head)
			if rest, ok := strings.CutPrefix(line, want); !ok || strings.ContainsAny(line[:1], "/!#") || strings.ContainsAny(rest, "\n\r\x1b") {
				t.Errorf("a note %q from %s is typed as %q", head, where, line)
			}
		}
		if line := Said("pane", head) + Said("", head) + Said("relayed", head) + Said("typed\n", head); line != "" {
			t.Errorf("a note from no place of the person's is typed: %q", line)
		}
	}
	if long := []rune(Said(WherePhone, strings.Repeat("a\n", 1<<20))); len(long) > SayMax+len(sayFrom[WherePhone]) || !strings.HasSuffix(string(long), sayCut) {
		t.Errorf("a megabyte is typed as %d characters", len(long))
	}
}
