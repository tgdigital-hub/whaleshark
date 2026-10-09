package term

import "testing"

func typed(l *Line, keys ...string) {
	for _, k := range keys {
		l.Key(key(k))
	}
}

func shown(l *Line, w int, cursor bool) (string, int) {
	g := NewGrid(w, 1)
	l.Draw(g, 0, 0, w, Style{}, cursor)
	at := -1
	for x := range w {
		if g.At(x, 0).Style.Reverse && at < 0 {
			at = x
		}
	}
	return g.Row(0), at
}

func TestLineEditing(t *testing.T) {
	var l Line
	typed(&l, "k", "e", "e", "p", "space", "t", "h", "e", "space", "o", "l", "d", "space", "l", "i", "n", "k")
	if l.String() != "keep the old link" {
		t.Fatalf("typed %q", l.String())
	}
	typed(&l, "ctrl+w", "alt+backspace")
	if l.String() != "keep the " {
		t.Fatalf("two words deleted: %q", l.String())
	}
	typed(&l, "home", "delete", "K", "end", "n", "e", "w", "left", "left", "backspace", "right", "right", "right", "!")
	if l.String() != "Keep the ew!" {
		t.Fatalf("after moving about: %q", l.String())
	}
	typed(&l, "ctrl+a", "right", "right", "ctrl+k")
	if l.String() != "Ke" {
		t.Fatalf("cut to the end: %q", l.String())
	}
	typed(&l, "ctrl+u", "ctrl+u", "backspace", "delete", "left")
	if l.String() != "" || l.at != 0 {
		t.Fatalf("emptied: %q at %d", l.String(), l.at)
	}
	for _, k := range []string{"enter", "esc", "tab", "up", "f5", "ctrl+c"} {
		if l.Key(key(k)) {
			t.Errorf("the line took %s", k)
		}
	}
	if l.Key(Event{Kind: Mouse}) || l.String() != "" {
		t.Errorf("the line took a mouse report")
	}
}

func TestLineWideAndCombinedCharacters(t *testing.T) {
	var l Line
	// e and its accent arrive as two keys and are one character; so are the
	// parts of a joined picture.
	typed(&l, "c", "a", "f", "e", "́", "漢", "👍", "\U0001F3FD", "x")
	if len(l.chars) != 7 || l.String() != "café漢👍🏽x" {
		t.Fatalf("%d characters: %q", len(l.chars), l.chars)
	}
	if row, at := shown(&l, 20, true); row != "café漢👍🏽x" || at != 9 {
		t.Fatalf("drawn %q with the cursor at %d", row, at)
	}
	typed(&l, "left", "left")
	if _, at := shown(&l, 20, true); at != 6 {
		t.Fatalf("one step left over a wide character: cursor at %d", at)
	}
	typed(&l, "backspace", "backspace")
	if l.String() != "caf👍🏽x" {
		t.Fatalf("backspace takes a whole character: %q", l.String())
	}
	l.Click(1)
	typed(&l, "é")
	l.Click(5) // the right half of the picture
	typed(&l, "delete")
	l.Click(99)
	typed(&l, "!")
	if l.String() != "céafx!" {
		t.Fatalf("after three clicks: %q", l.String())
	}
}

func TestLinePasteNeverSends(t *testing.T) {
	var l Line
	l.Set("yes")
	l.Key(Event{Kind: Paste, Text: " and\r\nno\x1b[31m\tmore\x00"})
	if l.String() != "yes and no more" {
		t.Fatalf("pasted: %q", l.String())
	}
	typed(&l, "home")
	l.Key(Event{Kind: Paste, Text: "¡"})
	if l.String() != "¡yes and no more" || l.at != 1 {
		t.Fatalf("pasted at the start: %q at %d", l.String(), l.at)
	}
}

func TestLineLongerThanItsPlace(t *testing.T) {
	var l Line
	l.Set("0123456789")
	if row, at := shown(&l, 6, true); row != "56789" || at != 5 {
		t.Fatalf("the end in view: %q, cursor at %d", row, at)
	}
	typed(&l, "home")
	if row, at := shown(&l, 6, true); row != "012345" || at != 0 {
		t.Fatalf("the start in view: %q, cursor at %d", row, at)
	}
	typed(&l, "end", "left", "left")
	shown(&l, 6, true)
	l.Click(0)
	typed(&l, "x")
	if l.String() != "012x3456789" {
		t.Fatalf("a click counts from what is drawn: %q", l.String())
	}
	if row, at := shown(&l, 6, false); at != -1 || row == "" {
		t.Fatalf("no cursor asked for, one drawn at %d", at)
	}
}
