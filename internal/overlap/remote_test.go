package overlap

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// fakeList is a stand-in for gh that lists three open pull requests: the
// task's own, one on its files and one elsewhere.
const fakeList = `#!/bin/sh
[ "$1 $2" = "pr list" ] || exit 1
echo '[{"number":3,"headRefName":"whaleshark/r1-T1","files":[{"path":"shop/cart.txt"}]},
{"number":7,"headRefName":"feature/cart","files":[{"path":"shop/cart.txt"},{"path":"shop/pay.txt"},{"path":"docs/a.md"}]},
{"number":9,"headRefName":"feature/docs","files":[{"path":"docs/b.md"}]}]'
`

// A task's --owns is held against the open pull requests only once the
// project has asked for it and the person approved that: then the one other
// pull request on its files is an overlap event, and its own is not.
func TestRemote(t *testing.T) {
	f := start(t, base)
	if f.k.Platform.System() == "windows" {
		t.Skip("the stand-in for gh is a shell script")
	}
	tools := t.TempDir()
	must(t, os.WriteFile(filepath.Join(tools, "gh"), []byte(fakeList), 0o700)) // #nosec G306 -- a stand-in the test runs
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	f.task("T1", nil, "shop/**")
	overlaps := func() (out []contract.Event) {
		for _, e := range f.state().Inbox.Events {
			if e.Kind == "overlap" {
				out = append(out, e)
			}
		}
		return out
	}
	toml := filepath.Join(f.root, "whaleshark.toml")
	must(t, os.WriteFile(toml, []byte("[overlap]\nremote_prs = true\n"), 0o600))
	f.o.Remote(f.root, "r1", "T1")
	if got := overlaps(); len(got) != 0 {
		t.Fatalf("an approval was needed first, and it raised %+v", got)
	}
	lines, err := contract.TrustLines(f.root)
	must(t, err)
	if !slices.Contains(lines, "overlap.remote_prs = true") {
		t.Fatalf("trust shows %q", lines)
	}
	must(t, contract.WriteTrust(f.k.Platform, f.root, lines, contract.Now()))
	f.o.Remote(f.root, "r1", "page T1")
	got := overlaps()
	if len(got) != 1 || got[0].Task != "T1" || got[0].Data["pr"] != float64(7) && got[0].Data["pr"] != 7 {
		t.Fatalf("raised %+v", got)
	}
	want := "the open pull request #7 (feature/cart) changes shop/cart.txt and 1 more, which T1's --owns names"
	if got[0].Text != want {
		t.Fatalf("said %q", got[0].Text)
	}
}
