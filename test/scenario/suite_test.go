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

// Every scenario of the tree is played on herdr's stand-in, on the engine's
// double and on the real keeper, and what each run came to is held against
// the first: the backend changes nothing a flow can see. What the keeper
// cannot play yet is skipped by name.
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
			for _, on := range []Backend{Herdr, Double, Keeper} {
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
				if on != Herdr && logs[on] != nil && !slices.Equal(logs[on], logs[Herdr]) {
					t.Errorf("the run on the %s came to something else than on herdr's stand-in:\n%s\n\non herdr's stand-in:\n%s",
						on, strings.Join(logs[on], "\n"), strings.Join(logs[Herdr], "\n"))
				}
			}
		})
	}
}
