package term

import "testing"

func TestColourAndItsTwoFallbacks(t *testing.T) {
	st := Style{Fg: RGB(0xff7f6b), Bg: RGB(0x0b1620), Bold: true, Reverse: true}
	for mode, want := range map[Mode]string{
		FullColour: "\x1b[0;1;7;38;2;255;127;107;48;2;11;22;32m",
		Colour256:  "\x1b[0;1;7;38;5;209;48;5;233m",
		NoColour:   "\x1b[0;1;7m",
	} {
		if got := string(mode.style(nil, st)); got != want {
			t.Errorf("mode %d: %q, want %q", mode, got, want)
		}
	}
	if got := string(FullColour.style(nil, Style{Dim: true, Underline: true, Strike: true})); got != "\x1b[0;2;4;9m" {
		t.Errorf("no colour set: %q", got)
	}
	if RGB(0) == 0 {
		t.Errorf("black must not be the terminal's own colour")
	}
}

func TestNearestOf256(t *testing.T) {
	for rgb, want := range map[[3]int]int{
		{0, 0, 0}: 16, {255, 255, 255}: 231, {255, 0, 0}: 196, {0, 0, 255}: 21, {95, 135, 175}: 67,
		{128, 128, 128}: 244, {8, 8, 8}: 232, {238, 238, 238}: 255, {63, 216, 240}: 81,
	} {
		if got := nearest256(rgb[0], rgb[1], rgb[2]); got != want {
			t.Errorf("%v is %d, want %d", rgb, got, want)
		}
	}
}

func TestModeOf(t *testing.T) {
	for _, c := range []struct {
		term, colorterm, no string
		want                Mode
	}{
		{"xterm-256color", "truecolor", "", FullColour},
		{"xterm-256color", "24bit", "", FullColour},
		{"xterm-256color", "", "", Colour256},
		{"xterm", "", "", Colour256},
		{"xterm-256color", "truecolor", "1", NoColour},
		{"dumb", "truecolor", "", NoColour},
		{"", "", "", NoColour},
	} {
		env := map[string]string{"TERM": c.term, "COLORTERM": c.colorterm, "NO_COLOR": c.no}
		if got := ModeOf(func(k string) string { return env[k] }); got != c.want {
			t.Errorf("%+v: mode %d, want %d", c, got, c.want)
		}
	}
}
