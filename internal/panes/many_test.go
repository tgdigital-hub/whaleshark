package panes

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/view"
)

// apart is the left section's record with each project's folder of ours
// inside its own folder, as the store has it.
type apart struct{ listed }

func (a apart) Dir(root, run string) string {
	if root == a.dir {
		return a.listed.Dir(root, run)
	}
	if run == "" {
		return filepath.Join(root, contract.ProjectDir)
	}
	return filepath.Join(root, contract.ProjectDir, "runs", run)
}

// A login's list of projects can hold thousands of folders, most of them
// gone from the disk: the left section is drawn all the same within two
// seconds of the pane's start, where once it took thirty, and a folder that is gone is not among its rows.
func TestTheBarIsDrawnAtOnceOverThousandsOfProjectsMostlyGone(t *testing.T) {
	const gone, there = 6000, 20
	var began time.Time
	wd := startWith(t, side, 32, 40, nil, func(wd *world) {
		dirs, _ := wd.sys.Dirs()
		roots := []string{wd.home}
		for i := range gone {
			roots = append(roots, filepath.Join(wd.home, "gone", fmt.Sprintf("TestRun%04d", i)))
		}
		for i := range there {
			root := filepath.Join(wd.home, "there", fmt.Sprintf("kept%02d", i))
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			roots = append(roots, root)
		}
		if err := contract.WriteProjects(wd.sys, dirs.State, roots); err != nil {
			t.Fatal(err)
		}
		k := wd.p.k
		k.Store = apart{listed{k.Store.(reader)}}
		k.Side = view.Side
		began = time.Now()
	})
	took, bound := time.Since(began), slow*2*time.Second
	if t.Logf("the bar was first drawn %v after the pane began", took); took > bound {
		t.Errorf("the bar was first drawn %v after the pane began, over %v", took, bound)
	}
	wd.shows("AGENTS · ")
	if _, _, ok := wd.s.Find("TestRun"); ok {
		t.Errorf("a folder that is gone has a row:\n%s", wd.screen())
	}
	given := make(chan int, 1)
	go wd.p.tell(news{do: func() { given <- len(wd.p.in.Projects) }})
	if n := <-given; n != 1+there {
		t.Errorf("the left section was given %d projects, want the %d that are there", n, 1+there)
	}
}
