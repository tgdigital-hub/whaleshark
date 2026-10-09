// Package theme holds the colour schemes as data, for the panes and the
// page alike. A scheme gives a colour to each of the sixteen names of the
// contract and to nothing else.
package theme

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Plug binds nothing: a scheme is chosen with the set command.
func Plug(*contract.Kit) {}

// Scheme is one colour scheme. Colours are 0xRRGGBB by name. Ground is the
// background the scheme is made for: it is what the contrast of every
// colour is measured against, and the dark text on a lit button.
type Scheme struct {
	Name    string
	Ground  uint32
	Colours map[string]uint32
}

// None is the scheme without colour: every state is still told apart by its
// mark and its word. It is also what a terminal that wants no colour gets.
const None = "none"

// Schemes is every scheme a person can pick, in the order the key steps
// through them. The first is the default.
var Schemes = []Scheme{
	{Name: "reef", Ground: 0x0b1620, Colours: map[string]uint32{
		"text": 0xe6edf3, "dim": 0x8ea4b8, "frame": 0x5f7a94, "accent": 0x3fd8f0,
		"needs": 0xff7f6b, "blocked": 0xffb347, "failed": 0xff5c6c, "check": 0xb99cff,
		"building": 0x3ecf9a, "idle": 0xa9bdd0, "paused": 0xd4a8f0, "done": 0x5fae7c,
		"bar_empty": 0x56697d, "ctx_low": 0x35c9c0, "ctx_mid": 0xffb347, "ctx_high": 0xff5c6c,
	}},
	{Name: "paper", Ground: 0xfaf6ec, Colours: map[string]uint32{
		"text": 0x1f2933, "dim": 0x5a6672, "frame": 0x7d8791, "accent": 0x006f8a,
		"needs": 0xb83a1e, "blocked": 0x8a5a00, "failed": 0xb3202e, "check": 0x6b3fc4,
		"building": 0x0a6e52, "idle": 0x55616d, "paused": 0x7a4d9a, "done": 0x3d6b4c,
		"bar_empty": 0x7f8790, "ctx_low": 0x006d70, "ctx_mid": 0x8a5a00, "ctx_high": 0xb3202e,
	}},
	{Name: None},
}

// Get returns the scheme of that name, and the default for a name nobody has.
func Get(name string) Scheme {
	for _, s := range Schemes {
		if s.Name == name {
			return s
		}
	}
	return Schemes[0]
}

// Next is the name of the scheme after this one, going round.
func Next(name string) string {
	for i, s := range Schemes {
		if s.Name == name {
			return Schemes[(i+1)%len(Schemes)].Name
		}
	}
	return Schemes[0].Name
}
