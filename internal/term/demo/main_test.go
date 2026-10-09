package main

import (
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/term/termtest"
)

func TestTheDemoSaysWhatWasClicked(t *testing.T) {
	s := termtest.New(50, 12)
	done := make(chan bool)
	go func() {
		run(s.Term)
		done <- true
	}()
	expect := func(text string) {
		t.Helper()
		if !s.Wait(text) {
			t.Fatalf("the demo never showed %q; its last rows are %q and %q", text, s.Row(10), s.Row(11))
		}
	}
	expect("reef · 50x12 · full colour")
	if !s.ClickText("building") {
		t.Fatal("no cell named building")
	}
	expect("clicked building at column 1, row 5")
	s.Click(49, 0)
	expect("clicked nothing at column 49, row 0")
	s.Type("\x1b[<64;14;2M")
	expect("wheel up on dim at column 13, row 1")
	s.Type("\x1b[<2;14;2M") // the right button: nothing happens
	s.Key("y")
	s.Paste("es\nplease")
	expect("> yes please")
	expect("pasted 9 cells of text")
	s.Key("esc")
	expect("key esc")
	s.Key("up")
	expect("key up")
	s.Key("tab")
	expect("paper · 50x12")
	s.Resize(60, 14)
	expect("paper · 60x14")
	if s.Row(13) != "key up" || s.Row(12) != "> yes please" {
		t.Fatalf("after the resize the last rows are %q and %q", s.Row(12), s.Row(13))
	}
	s.Key("ctrl+c")
	<-done
}
