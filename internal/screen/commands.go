// Package screen is the screen reader: what a program prints, read into a
// picture of cells.
//
// Written from public documents and from nothing else: ECMA-48 (5th edition)
// for the grammar of sequences; the state diagram of a DEC-compatible reader
// at vt100.net/emu/dec_ansi_parser for how to arrange it; the VT100 User
// Guide (chapter 3) and the VT220 Programmer Reference (chapter 4) for what
// each sequence does; XTerm Control Sequences for colours, the second screen
// and titles; Unicode UAX 11 and UAX 29, through the uniseg module, for where
// a character ends and how wide it is.
package screen

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Plug binds this package's part into the kit: it has no command and stands
// behind no interface. The keeper makes a Screen for each pane.
func Plug(*contract.Kit) {}
