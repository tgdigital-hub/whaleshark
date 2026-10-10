package theme

import (
	"math"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Text and marks need 4.5 to 1 against the background; a line or the empty
// part of a bar, which nobody reads, needs 3 to 1.
var floor = map[string]float64{"frame": 3, "bar_empty": 3}

func luminance(c uint32) float64 {
	part := func(v uint32) float64 {
		f := float64(v&0xff) / 255
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*part(c>>16) + 0.7152*part(c>>8) + 0.0722*part(c)
}

func contrast(a, b uint32) float64 {
	la, lb := luminance(a), luminance(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

func TestContrastFormula(t *testing.T) {
	if c := contrast(0x000000, 0xffffff); math.Abs(c-21) > 0.01 {
		t.Fatalf("black on white is %v, want 21", c)
	}
}

func TestEverySchemeNamesTheSixteenAndIsReadable(t *testing.T) {
	for _, s := range Schemes {
		if s.Name == None {
			if len(s.Colours) != 0 {
				t.Errorf("none has colours")
			}
			continue
		}
		if len(s.Colours) != len(contract.Colours) {
			t.Errorf("%s: %d colours, want %d", s.Name, len(s.Colours), len(contract.Colours))
		}
		for _, name := range contract.Colours {
			c, ok := s.Colours[name]
			if !ok {
				t.Errorf("%s: no colour for %s", s.Name, name)
				continue
			}
			want := 4.5
			if f, ok := floor[name]; ok {
				want = f
			}
			if s.Name == "mono" && (c>>16 != c&0xff || c>>8&0xff != c&0xff) {
				t.Errorf("mono: %s is %06x, which is no grey", name, c)
			}
			if got := contrast(c, s.Ground); got < want {
				t.Errorf("%s: %s is %.2f to 1 against its background, want %.1f", s.Name, name, got, want)
			}
		}
	}
}

func TestGetAndNext(t *testing.T) {
	var names []string
	for _, s := range Schemes {
		names = append(names, s.Name)
	}
	if got := strings.Join(names, " "); got != "reef paper kelp ember mono none" {
		t.Errorf("the schemes are %s", got)
	}
	if Schemes[0].Name != contract.PersonDefaults().UI.Theme {
		t.Errorf("the first scheme is %s, the default setting is %s", Schemes[0].Name, contract.PersonDefaults().UI.Theme)
	}
	if Get("no such").Name != "reef" || Get("paper").Name != "paper" || Get(None).Colours != nil {
		t.Errorf("Get picks wrongly")
	}
	name, seen := "reef", map[string]bool{}
	for range Schemes {
		seen[name] = true
		name = Next(name)
	}
	if name != "reef" || len(seen) != len(Schemes) || Next("no such") != "reef" {
		t.Errorf("Next does not go round every scheme: %v, back at %s", seen, name)
	}
}
