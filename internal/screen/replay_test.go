package screen_test

import (
	"io"
	"io/fs"
	"math/rand/v2"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/screen"
	"github.com/tgdigital-hub/whaleshark/test/corpus"
)

func whole(w io.Writer, p []byte) { w.Write(p) }

// eachRecording runs f on every recording of the public set, and of the
// private one where the variable names it.
func eachRecording(t *testing.T, f func(t *testing.T, rec *corpus.Recording, approved string)) {
	for _, set := range corpus.Sets() {
		recs, err := corpus.All(set)
		if err != nil {
			t.Fatal(err)
		}
		for _, rec := range recs {
			approved, err := fs.ReadFile(set, "pictures/"+rec.Name+".txt")
			if err != nil {
				t.Errorf("%s has no approved picture: %v", rec.Name, err)
				continue
			}
			t.Run(rec.Name, func(t *testing.T) { f(t, rec, string(approved)) })
		}
	}
}

// Every recording, replayed, shows the pictures a person approved.
func TestReplay(t *testing.T) {
	n := 0
	eachRecording(t, func(t *testing.T, rec *corpus.Recording, approved string) {
		n++
		if got := corpus.Shots(rec, screen.New(rec.W, rec.H), whole); got != approved {
			t.Errorf("the pictures differ from test/corpus/pictures/%s.txt; if the new ones are right, approve them with: go run ./test/corpus/replay approve", rec.Name)
		}
	})
	if n < 28 {
		t.Errorf("%d recordings replayed, want the 21 recorded and the 7 synthetic at least", n)
	}
}

// A recording cut after every byte, and cut at random, gives the same pictures.
func TestCutAnywhere(t *testing.T) {
	rnd := rand.New(rand.NewPCG(1, 2))
	eachRecording(t, func(t *testing.T, rec *corpus.Recording, approved string) {
		bytewise := func(w io.Writer, p []byte) {
			for i := range p {
				w.Write(p[i : i+1])
			}
		}
		if corpus.Shots(rec, screen.New(rec.W, rec.H), bytewise) != approved {
			t.Error("cut after every byte, the pictures differ")
		}
		for range 5 {
			random := func(w io.Writer, p []byte) {
				for len(p) > 0 {
					n := 1 + rnd.IntN(min(len(p), 7))
					w.Write(p[:n])
					p = p[n:]
				}
			}
			if corpus.Shots(rec, screen.New(rec.W, rec.H), random) != approved {
				t.Error("cut at random, the pictures differ")
			}
		}
	})
}

// The synthetic streams in the folder are the ones the generator writes,
// and after every frame the reader shows what the generator meant to draw.
func TestSynth(t *testing.T) {
	recs, err := corpus.All(corpus.Sets()[0])
	if err != nil {
		t.Fatal(err)
	}
	saved := map[string]string{}
	for _, rec := range recs {
		saved[rec.Name] = string(rec.Format())
	}
	for _, rec := range corpus.Synth() {
		if saved[rec.Name] != string(rec.Format()) {
			t.Errorf("%s.rec is not what the generator writes: go run ./test/corpus/replay synth", rec.Name)
		}
		s, frames := screen.New(rec.W, rec.H), 0
		for i, out := range rec.Out {
			s.Write(out)
			if want, ok := rec.Want[i+1]; ok {
				if frames++; s.Picture().Text() != want {
					t.Fatalf("%s after %d pieces:\n%s\nwant:\n%s", rec.Name, i+1, s.Picture().Text(), want)
				}
			}
		}
		if frames < 100 {
			t.Errorf("%s: %d frames checked", rec.Name, frames)
		}
	}
}

// BenchmarkReplay reads every recording of the public set once a round.
func BenchmarkReplay(b *testing.B) {
	recs, err := corpus.All(corpus.Sets()[0])
	if err != nil {
		b.Fatal(err)
	}
	n := 0
	for b.Loop() {
		for _, rec := range recs {
			s := screen.New(rec.W, rec.H)
			for _, out := range rec.Out {
				s.Write(out)
				n += len(out)
			}
		}
	}
	b.SetBytes(int64(n / b.N))
}

// BenchmarkScroll is the worst a pane does all day: short lines, each
// scrolling a large screen.
func BenchmarkScroll(b *testing.B) {
	line := []byte("\x1b[32mok\x1b[m  a short line of output\r\n")
	s := screen.New(200, 60)
	b.SetBytes(int64(len(line)))
	for b.Loop() {
		s.Write(line)
	}
}
