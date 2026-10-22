package panes

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Which tools are there to start is looked for beside the loop and told to
// it, and never by a drawing: along the path that is hundreds of files on
// Windows, and a pane that looked at every frame drew a change seconds late.
func TestADrawingLooksForNoProgram(t *testing.T) {
	p := screenOf(t, 120, 44)
	dirs, err := p.k.Platform.Dirs()
	if err != nil {
		t.Fatal(err)
	}
	bin, file := t.TempDir(), filepath.Join(dirs.Config, contract.ToolsDir, "hermit.toml")
	write := func(path, text string) {
		t.Helper()
		err := os.MkdirAll(filepath.Dir(path), 0o700)
		if err == nil {
			err = os.WriteFile(path, []byte(text), 0o700) // #nosec G306 -- a program of the test's own
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	write(file, "run = ['"+filepath.ToSlash(filepath.Join(bin, "hermit"))+"']\nhooks = 'none'\n")
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	p.bg = ctx
	told := func() {
		t.Helper()
		select {
		case m := <-p.heard:
			p.terms(m)
		case <-time.After(10 * time.Second):
			t.Fatal("the loop was told of no look")
		}
	}
	found := func() bool { return slices.Contains(p.found, "hermit") }

	// A tool that is wired in anew is looked for, beside the loop.
	p.load()
	if _, wired := contract.FindTool(p.tools, "hermit"); !wired {
		t.Fatalf("hermit is not wired in: %+v", p.tools)
	}
	if told(); found() {
		t.Fatal("a tool with no program was found")
	}
	// Its program comes: no drawing finds it, the next look does.
	for _, ending := range []string{"", ".exe"} {
		write(filepath.Join(bin, "hermit"+ending), "")
	}
	p.stale = true
	if p.draw(); found() {
		t.Error("a drawing looked for a tool's program")
	}
	p.look()
	if told(); !found() {
		t.Error("the look beside the loop did not find the program that came")
	}
	if p.seen != p.looks {
		t.Errorf("what was found is of look %d of %d", p.seen, p.looks)
	}
}
