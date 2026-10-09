package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tree writes files into a fresh folder; each pair is a path and its content.
func tree(t *testing.T, files ...string) string {
	t.Helper()
	root := t.TempDir()
	for i := 0; i < len(files); i += 2 {
		path := filepath.Join(root, files[i])
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(files[i+1]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// gateSays runs one step and wants its failure to hold each of the words,
// or wants it to pass when there are none.
func gateSays(t *testing.T, root, step string, want ...string) {
	t.Helper()
	err := run(root, []string{step}, io.Discard)
	if len(want) == 0 && err != nil {
		t.Fatalf("%s should pass: %v", step, err)
	}
	var out strings.Builder
	if len(want) > 0 {
		if err == nil {
			t.Fatalf("%s should fail", step)
		}
		run(root, []string{step}, &out)
	}
	for _, w := range want {
		if !strings.Contains(out.String(), w) {
			t.Errorf("%s should say %q, it says:\n%s", step, w, out.String())
		}
	}
}

const counted = "count .go\nskip _test.go testdata\ntotal 100\n"

func TestLines(t *testing.T) {
	check := func(root string, want ...string) { t.Helper(); gateSays(t, root, "lines", want...) }
	limits := "lines 10 big internal/big\n" + counted
	big := func(n int) string { return "package big\n" + strings.Repeat("\n", n-1) }

	check(tree(t, "build/ci/limits.txt", limits, "internal/big/a.go", big(11)))
	check(tree(t, "build/ci/limits.txt", limits, "internal/big/a.go", big(12)),
		"big has 12 lines, more than a tenth over its budget of 10")
	check(tree(t, "build/ci/limits.txt", limits, "internal/big/a.go", big(5),
		"internal/big/a_test.go", big(50), "internal/big/testdata/b.go", big(50)))
	check(tree(t, "build/ci/limits.txt", limits, "internal/big/a.go", big(5), "internal/new/a.go", big(1)),
		"internal/new has no lines line")
	check(tree(t, "build/ci/limits.txt", "lines 200 big internal/big\n"+counted, "internal/big/a.go", big(101)),
		"101 lines in all, the cap is 100")
}

func TestImports(t *testing.T) {
	check := func(root string, want ...string) { t.Helper(); gateSays(t, root, "imports", want...) }
	const mod = "module example.test/m\n\ngo 1.27\n"
	limits := "noimport internal/panes internal/store\nonly runtime.GOOS internal/platform\n" + counted
	files := []string{
		"build/ci/limits.txt", limits, "go.mod", mod,
		"internal/store/s.go", "package store\n\nfunc Save() {}\n",
		"internal/middle/m.go", "package middle\n\nfunc Do() {}\n",
		"internal/panes/p.go", "package panes\n\nimport \"example.test/m/internal/middle\"\n\nfunc Draw() { middle.Do() }\n",
	}
	check(tree(t, files...))

	through := "package middle\n\nimport \"example.test/m/internal/store\"\n\nfunc Do() { store.Save() }\n"
	check(tree(t, append(files, "internal/middle/m.go", through)...),
		"internal/panes may not import internal/store: internal/panes -> internal/middle -> internal/store")

	kit := "package middle\n\ntype Kit struct{ Store interface{ Change() } }\n\nfunc Do() { Kit{}.Store.Change() }\n"
	check(tree(t, append(files, "internal/middle/m.go", kit)...),
		"internal/middle/m.go:5: internal/panes reaches the kit's Store")
	held := "package middle\n\ntype Kit struct{ Store interface{ Change() } }\n\nfunc Do() {\n\ts := Kit{}.Store\n\ts.Change()\n}\n"
	check(tree(t, append(files, "internal/middle/m.go", held)...),
		"internal/middle/m.go:6: internal/panes reaches the kit's Store")
	reads := "package middle\n\nimport \"sync/atomic\"\n\ntype Kit struct{ n atomic.Int64 }\n\nfunc (Kit) Reader() int { return 0 }\n\nfunc Do() {\n\tvar k Kit\n\tk.n.Store(1)\n\t_ = k.Reader()\n}\n"
	check(tree(t, append(files, "internal/middle/m.go", reads)...))

	system := "package middle\n\nimport \"runtime\"\n\nfunc Do() { _ = runtime.GOOS }\n"
	check(tree(t, append(files, "internal/middle/m.go", system)...),
		"internal/middle/m.go:5: runtime.GOOS belongs in internal/platform only")
	check(tree(t, append(files, "internal/platform/p.go", strings.Replace(system, "middle", "platform", 1))...))
}

// The planted marks are put together here, so that this file holds none.
func TestPublish(t *testing.T) {
	home := "/ho" + "me/sam/notes.txt"
	key := "-----BEGIN RSA PRIV" + "ATE KEY-----"
	token := "gh" + "p_" + strings.Repeat("a1", 18)
	planted := []struct{ file, text, want string }{
		{"notes.txt", "see " + home + "\n", "notes.txt:1: a path under a home folder"},
		{"notes.txt", "\nroot = \"/Us" + "ers/sam\"\n", "notes.txt:2: a path under a home folder"},
		{"id.pem", key + "\n", "id.pem:1: a private key"},
		{"env", "TOKEN=" + token + "\n", "env:1: a token"},
		{"env", "KEY=AK" + "IA" + strings.Repeat("Q", 16) + "\n", "env:1: an access key"},
		{"AUTHORS", "Sam <sam@" + "corp.test>\n", "AUTHORS:1: an email address"},
		{"CLAUDE.md", "rules\n", "CLAUDE.md: a private file"},
	}
	for _, p := range planted {
		root := tree(t, "build/ci/limits.txt", "", p.file, p.text)
		if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
		gateSays(t, root, "publish", p.want)
	}

	clean := "Sam <1+sam@users.noreply.github.com>, sam@example.com, git@github.com:sam/x.git\n" +
		"https://example.com/ho" + "me/page ~/.config/whaleshark golang.org/x/sys@v0.49.0\n"
	root := tree(t, "build/ci/limits.txt", "", "notes.txt", clean)
	exec.Command("git", "-C", root, "init", "-q").Run()
	gateSays(t, root, "publish")

	msg := filepath.Join(t.TempDir(), "msg")
	os.WriteFile(msg, []byte("Fix the parser\n\nfound in "+home+"\n"), 0o644)
	if err := run(root, []string{"message", msg}, io.Discard); err == nil {
		t.Error("a message with a home-folder path should fail")
	}
	if errs := marks("staged", added("+++ b/x\n+ok\n-"+key+"\n+"+key+"\n")); len(errs) != 1 {
		t.Errorf("only the added key should be found, got %v", errs)
	}
}
