package scenario

import (
	"io/fs"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/panes"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/view"
)

// Every scenario of the tree is played on the engine's double and on the
// real keeper, and what the run on the keeper came to is held against the
// double's: the two change nothing a flow can see.
func TestEveryScenarioOnEveryBackend(t *testing.T) {
	bin, err := exec.LookPath("whaleshark")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(contract.EnvBin, bin)
	var files []string
	filepath.WalkDir(module, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && strings.HasPrefix(d.Name(), ".") && path != module {
			return filepath.SkipDir
		}
		if err == nil && filepath.Ext(path) == ".scn" {
			files = append(files, path)
		}
		return nil
	})
	if len(files) == 0 {
		t.Fatal("no scenario found under " + module)
	}
	for _, file := range files {
		name, _ := filepath.Rel(module, file)
		t.Run(filepath.ToSlash(name), func(t *testing.T) {
			logs := map[Backend][]string{}
			for _, on := range []Backend{Double, Keeper} {
				t.Run(string(on), func(t *testing.T) {
					// The two scenarios of the runner itself draw a pane of
					// this package; every other opens the real ones, or none.
					pane := tabs
					if base := filepath.Base(file); base != "self.scn" && base != "restart.scn" {
						pane = func(p *Project, name string, tm *term.Term) {
							for _, kv := range p.Env(p.Own) {
								k, v, _ := strings.Cut(kv, "=")
								t.Setenv(k, v)
							}
							view.Plug(p.Kit)
							panes.Run(name, tm, p.Kit, p.Root, "")
						}
					}
					logs[on] = RunOn(t, on, file, pane).Log
					t.Log("\n" + strings.Join(logs[on], "\n"))
				})
				if on != Double && logs[on] != nil && !slices.Equal(logs[on], logs[Double]) {
					t.Errorf("the run on the %s came to something else than on the double:\n%s\n\non the double:\n%s",
						on, strings.Join(logs[on], "\n"), strings.Join(logs[Double], "\n"))
				}
			}
		})
	}
}
