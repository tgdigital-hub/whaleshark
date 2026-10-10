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
	{Name: "kelp", Ground: 0x0d1a12, Colours: map[string]uint32{
		"text": 0xe8f0e4, "dim": 0x93aa98, "frame": 0x5f7d66, "accent": 0xe6c34a,
		"needs": 0xff8a65, "blocked": 0xf0b429, "failed": 0xff6b6b, "check": 0xc3a6ff,
		"building": 0x6fcf73, "idle": 0xa8bfae, "paused": 0xd0a8e8, "done": 0x69a86f,
		"bar_empty": 0x55705c, "ctx_low": 0x5fd1a4, "ctx_mid": 0xf0b429, "ctx_high": 0xff6b6b,
	}},
	{Name: "ember", Ground: 0x1c120e, Colours: map[string]uint32{
		"text": 0xf5e9df, "dim": 0xb39f92, "frame": 0x8a6f60, "accent": 0xffb454,
		"needs": 0xff7a59, "blocked": 0xffd166, "failed": 0xff5d5d, "check": 0xd9a6ff,
		"building": 0xf29e4c, "idle": 0xc4b0a3, "paused": 0xe0a3c8, "done": 0xa3b565,
		"bar_empty": 0x77605a, "ctx_low": 0xe8c170, "ctx_mid": 0xff9f43, "ctx_high": 0xff5d5d,
	}},
	{Name: "mono", Ground: 0x111111, Colours: map[string]uint32{
		"text": 0xe4e4e4, "dim": 0x9a9a9a, "frame": 0x6e6e6e, "accent": 0xffffff,
		"needs": 0xffffff, "blocked": 0xd6d6d6, "failed": 0xf2f2f2, "check": 0xc4c4c4,
		"building": 0xb0b0b0, "idle": 0x9a9a9a, "paused": 0xbcbcbc, "done": 0x8c8c8c,
		"bar_empty": 0x666666, "ctx_low": 0xb0b0b0, "ctx_mid": 0xd6d6d6, "ctx_high": 0xffffff,
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
