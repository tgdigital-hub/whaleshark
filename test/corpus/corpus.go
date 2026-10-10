// Package corpus reads the recordings and their approved pictures, and
// writes the synthetic streams. It is test code; nothing shipped imports it.
package corpus

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
)

//go:embed recordings/*.rec pictures/*.txt
var public embed.FS

// Sets are the folders of recordings: this one, and the private one when
// the variable testkit.EnvCorpus names it. Each holds recordings/NAME.rec
// and pictures/NAME.txt.
func Sets() []fs.FS {
	sets := []fs.FS{public}
	if dir := os.Getenv(testkit.EnvCorpus); dir != "" {
		sets = append(sets, os.DirFS(dir))
	}
	return sets
}

// Recording is what one program printed into a terminal of W by H.
type Recording struct {
	Name string
	W, H int
	Out  [][]byte // each piece as it arrived
	// Want is what a synthetic stream's own model says the screen shows, as
	// contract.Picture.Text gives it, by the number of pieces read so far.
	Want map[int]string
}

// All is every recording of a set, in the order of their names.
func All(set fs.FS) ([]*Recording, error) {
	names, err := fs.Glob(set, "recordings/*.rec")
	if err != nil {
		return nil, err
	}
	var recs []*Recording
	for _, name := range names {
		data, err := fs.ReadFile(set, name)
		if err != nil {
			return nil, err
		}
		rec, err := Parse(strings.TrimSuffix(path.Base(name), ".rec"), data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		recs = append(recs, rec)
	}
	return recs, nil
}

// Parse reads a recording in the format the README describes.
func Parse(name string, data []byte) (*Recording, error) {
	rec := &Recording{Name: name}
	rows := strings.Split(string(data), "\n")
	if len(rows) < 4 || rows[0] != "whaleshark recording 1" {
		return nil, fmt.Errorf("not a recording")
	}
	if _, err := fmt.Sscanf(rows[1], "size %dx%d", &rec.W, &rec.H); err != nil {
		return nil, err
	}
	for _, row := range rows[4:] {
		if _, rest, ok := strings.Cut(row, " o "); ok {
			out, err := strconv.Unquote(rest)
			if err != nil {
				return nil, err
			}
			rec.Out = append(rec.Out, []byte(out))
		}
	}
	return rec, nil
}

// Format writes a recording in the same format, for the synthetic streams.
func (r *Recording) Format() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "whaleshark recording 1\nsize %dx%d\nargv [\"synthetic\"]\nenv []\n", r.W, r.H)
	for i, out := range r.Out {
		fmt.Fprintf(&b, "%d.%06d o %s\n", i/50, i%50*20000, strconv.QuoteToASCII(string(out)))
	}
	fmt.Fprintf(&b, "%d.000000 x 0\n", len(r.Out)/50+1)
	return []byte(b.String())
}

// Reader is the screen reader as a replay needs it.
type Reader interface {
	io.Writer
	Picture() *contract.Picture
}

// Shots replays a recording into a reader and returns the pictures it showed
// after each quarter of the pieces, as the text of an approved file. feed
// hands one piece to the reader, whole or cut up.
func Shots(rec *Recording, r Reader, feed func(io.Writer, []byte)) string {
	var b strings.Builder
	for i, out := range rec.Out {
		feed(r, out)
		if n := len(rec.Out); (i+1)*4/n > i*4/n {
			fmt.Fprintf(&b, "== %s after %d of %d ==\n%s", rec.Name, i+1, n, Render(r.Picture()))
		}
	}
	return b.String()
}

// Render is a picture as text a person can read and compare: the cursor,
// each row between bars, then the cells that are not plain, as runs. The
// right half of a wide character takes no room in its row.
func Render(p *contract.Picture) string {
	var b strings.Builder
	shown := "shown"
	if !p.CurShown {
		shown = "hidden"
	}
	fmt.Fprintf(&b, "cursor %d,%d %s\n", p.CurX, p.CurY, shown)
	for y := range p.H {
		b.WriteByte('|')
		for _, c := range p.Cells[y*p.W : (y+1)*p.W] {
			b.WriteString(c.Text)
		}
		b.WriteString("|\n")
	}
	for y := range p.H {
		row, from := p.Cells[y*p.W:(y+1)*p.W], 0
		for x := 1; x <= p.W; x++ {
			if x < p.W && row[x].Style == row[from].Style {
				continue
			}
			if row[from].Style != (contract.Style{}) {
				fmt.Fprintf(&b, "%d:%d-%d%s\n", y, from, x-1, styleText(row[from].Style))
			}
			from = x
		}
	}
	return b.String()
}

func styleText(s contract.Style) string {
	var b strings.Builder
	for i, c := range []contract.Colour{s.Fg, s.Bg} {
		switch c >> 24 {
		case 1:
			fmt.Fprintf(&b, " %s=#%06x", "fgbg"[i*2:i*2+2], uint32(c)&0xffffff)
		case 2:
			fmt.Fprintf(&b, " %s=%d", "fgbg"[i*2:i*2+2], uint32(c)&0xff)
		}
	}
	for i, on := range []bool{s.Bold, s.Dim, s.Italic, s.Underline, s.Reverse, s.Strike} {
		if on {
			b.WriteString(" " + []string{"bold", "dim", "italic", "underline", "reverse", "strike"}[i])
		}
	}
	return b.String()
}
