package pick

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf16"
)

// What the status mark says after a copy. The keeper cannot see whether an
// outer terminal obeyed, so only a route that is certain says Copied.
const (
	Copied = "copied"
	Sent   = "sent to your terminal"
)

// Board is the person's clipboard, as one window reaches it.
type Board struct {
	System string // the system the person sits at: "darwin", "linux" or "windows"
	// Remote: the window came over an ssh typed by hand, so the keeper is
	// not on the person's computer. Connect: it came through `connect`,
	// which is, and lifts the sequence out of the stream.
	Remote, Connect bool
}

// tools are each system's programs that take the clipboard's new text, in the order tried.
var tools = map[string][][]string{
	"darwin":  {{"pbcopy"}},
	"windows": {{"clip.exe"}},
	"linux":   {{"wl-copy"}, {"xclip", "-selection", "clipboard"}},
}

// Run gives a program its input and waits for it, at most two seconds. A
// test that copies replaces it, or it writes the clipboard of whoever runs it.
var Run = func(argv []string, in []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) // #nosec G204 -- a name out of tools, never a person's or a program's text
	cmd.Stdin = bytes.NewReader(in)
	return cmd.Run()
}

// Copy puts text on the person's clipboard by the first route that applies
// and returns the words for the status mark, and the sequence the window
// must be sent where there is one. On the person's own computer the
// system's clipboard program is run, which takes a moment: never under the
// keeper's lock. Through `connect`, over a bare ssh and where no program
// took the text, the outer terminal is asked with the clipboard sequence.
func (b Board) Copy(text string) (mark string, seq []byte) {
	if text == "" {
		return "", nil
	}
	in := []byte(text)
	if b.System == "windows" {
		// Its program reads the system's two-byte form, announced by a mark.
		in = binary.LittleEndian.AppendUint16(nil, 0xfeff)
		for _, u := range utf16.Encode([]rune(text)) {
			in = binary.LittleEndian.AppendUint16(in, u)
		}
	}
	for _, argv := range tools[b.System] {
		if !b.Remote && !b.Connect && Run(argv, in) == nil {
			return Copied, nil
		}
	}
	seq = []byte("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a")
	return map[bool]string{true: Copied, false: Sent}[b.Connect], seq
}

// CtrlC reports whether Ctrl+C copies a selection instead of interrupting,
// the rule of Windows alone: by the setting keys.style, or under "auto" by
// the system the person sits at, which over a bare ssh is not known.
func (b Board) CtrlC(style string) bool {
	return style == "windows" || style == "auto" && b.System == "windows" && !b.Remote
}

// Wish is what becomes of a program's own request to copy, by the setting
// clipboard.programs: "on" copies it now, "off" drops it, and otherwise the
// person is asked with these words while the caller keeps the text.
func Wish(setting, name, text string) (now bool, ask string) {
	if setting == "on" || setting == "off" || text == "" {
		return setting == "on" && text != "", ""
	}
	return false, fmt.Sprintf("%s wants to copy %d lines: allow?", name, strings.Count(text, "\n")+1)
}
