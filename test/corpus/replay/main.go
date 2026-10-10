// The replay tool is a test tool. It feeds recordings to the screen reader
// and shows or saves the pictures that come out. Run it from the top of
// the repository:
//
//	replay show FILE...   print the last picture of each recording
//	replay approve        write pictures/NAME.txt for every recording of
//	                      test/corpus, and of the folder WHALESHARK_CORPUS_EXTRA
//	                      names when it is set: read them before committing
//	replay synth          write the synthetic streams into test/corpus/recordings
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/screen"
	"github.com/tgdigital-hub/whaleshark/test/corpus"
)

const home = "test/corpus"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "replay:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	switch {
	case len(args) > 1 && args[0] == "show":
		for _, file := range args[1:] {
			data, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			rec, err := corpus.Parse(strings.TrimSuffix(filepath.Base(file), ".rec"), data)
			if err != nil {
				return err
			}
			s := screen.New(rec.W, rec.H)
			for _, out := range rec.Out {
				s.Write(out)
			}
			fmt.Printf("%s, title %q\n%s", rec.Name, s.Title(), corpus.Render(s.Picture()))
		}
	case len(args) == 1 && args[0] == "approve":
		dirs := []string{home}
		if extra := os.Getenv(testkit.EnvCorpus); extra != "" {
			dirs = append(dirs, extra)
		}
		for _, dir := range dirs {
			recs, err := corpus.All(os.DirFS(dir))
			if err != nil {
				return err
			}
			for _, rec := range recs {
				text := corpus.Shots(rec, screen.New(rec.W, rec.H), func(w io.Writer, p []byte) { w.Write(p) })
				if err := write(filepath.Join(dir, "pictures", rec.Name+".txt"), []byte(text)); err != nil {
					return err
				}
			}
		}
	case len(args) == 1 && args[0] == "synth":
		for _, rec := range corpus.Synth() {
			if err := write(filepath.Join(home, "recordings", rec.Name+".rec"), rec.Format()); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("usage: replay show FILE... | approve | synth")
	}
	return nil
}

func write(file string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		return err
	}
	return os.WriteFile(file, data, 0o600)
}
