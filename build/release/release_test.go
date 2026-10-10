package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testSigner = `release namespaces="file" ssh-ed25519 AAAAexample`

// made is a release packed from five small stand-in files, with every file
// it wrote by its name.
func made(t *testing.T) (*release, map[string]string) {
	t.Helper()
	r := &release{root: filepath.Join("..", ".."), out: t.TempDir(), version: "1.2.3-rc.1", site: "https://example.com/dl",
		set: map[string]string{"signer": testSigner}, when: time.Unix(1700000000, 0).UTC()}
	for _, name := range append(names(), "install.sh") {
		if err := r.put(name, []byte("the file "+name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.pack(); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	err := filepath.WalkDir(r.out, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			data, _ := os.ReadFile(path)
			rel, _ := filepath.Rel(r.out, path)
			files[filepath.ToSlash(rel)] = string(data)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return r, files
}

func TestTheListNamesItsVersionAndEveryFile(t *testing.T) {
	_, files := made(t)
	want := "# whaleshark 1.2.3-rc.1\n"
	for _, name := range append(names(), "install.sh") {
		want += sum([]byte("the file "+name)) + "  " + name + "\n"
	}
	if files["SHA256SUMS"] != want {
		t.Errorf("the list is\n%s\nwant\n%s", files["SHA256SUMS"], want)
	}
}

func TestAPackageHoldsTheSameFileAndTheIndexNamesIt(t *testing.T) {
	_, files := made(t)
	for _, arch := range []string{"amd64", "arm64"} {
		deb := files["apt/pool/whaleshark_1.2.3~rc.1_"+arch+".deb"]
		parts := unar(t, deb)
		if parts["debian-binary"] != "2.0\n" {
			t.Errorf("%s: the format note is %q", arch, parts["debian-binary"])
		}
		program, mode := untar(t, parts["data.tar.gz"], "./usr/bin/whaleshark")
		if program != "the file whaleshark-linux-"+arch || mode != 0o755 {
			t.Errorf("%s: the package holds %q with mode %o", arch, program, mode)
		}
		control, _ := untar(t, parts["control.tar.gz"], "./control")
		packages := files["apt/dists/stable/main/binary-"+arch+"/Packages"]
		for _, line := range []string{control, "Version: 1.2.3~rc.1\n", "Architecture: " + arch + "\n", "SHA256: " + sum([]byte(deb)) + "\n"} {
			if !strings.Contains(packages, line) {
				t.Errorf("%s: the index lacks %q:\n%s", arch, line, packages)
			}
		}
		if line := " " + sum([]byte(packages)) + " "; !strings.Contains(files["apt/dists/stable/Release"], line) {
			t.Errorf("%s: Release does not name the index's checksum", arch)
		}
	}
}

func TestTheFormulaAndWingetNameAddressAndChecksum(t *testing.T) {
	_, files := made(t)
	for _, name := range names() {
		text, s := files["brew/whaleshark.rb"], sum([]byte("the file "+name))
		if strings.HasSuffix(name, ".exe") {
			text, s = files["winget/"+winget+".installer.yaml"], strings.ToUpper(s)
		}
		if !strings.Contains(text, "https://example.com/dl/v1.2.3-rc.1/"+name) || !strings.Contains(text, s) {
			t.Errorf("%s: its address or checksum is missing:\n%s", name, text)
		}
	}
	if len(files) != 16 {
		t.Errorf("%d files, want 16: six, the list, two packages, three of the index, one formula, three for winget", len(files))
	}
}

func TestASecondPackingGivesTheSameBytes(t *testing.T) {
	_, first := made(t)
	_, second := made(t)
	for name, data := range first {
		if second[name] != data {
			t.Errorf("%s differs between two packings", name)
		}
	}
}

func TestTheScriptsAreFilledIn(t *testing.T) {
	r, _ := made(t)
	r.set["connector"] = "2026.1.0"
	installer, server, err := r.scripts()
	if err != nil {
		t.Fatal(err)
	}
	for text, lines := range map[string][]string{
		installer: {"\nVERSION=1.2.3-rc.1\n", "\nSIGNER='" + testSigner + "'\n"},
		server:    {"\nR=\n", "\nCONNECTOR='2026.1.0'\n", "\nCONNECTOR_KEY=''\n"},
	} {
		for _, line := range lines {
			if !strings.Contains(text, line) {
				t.Errorf("a script lacks the line %q", line)
			}
		}
	}
	if strings.Contains(installer+server, "VERSION=dev") || strings.Contains(server, "WHALESHARK_TEST_ROOT") {
		t.Error("a script still holds what a release takes out")
	}
}

func TestARunWithABadVersionOrKeyIsRefused(t *testing.T) {
	t.Chdir(filepath.Join("..", ".."))
	for _, args := range [][]string{
		{"-version", "v1.2.3", "-signer", testSigner},
		{"-version", "1.2.3", "-signer", "ssh-ed25519 AAAAexample"},
		{"-version", "1.2.3", "-signer", testSigner + "' x='"},
	} {
		if err := run(append(args, "-out", t.TempDir())); err == nil {
			t.Errorf("%q was not refused", args)
		}
	}
}

func unar(t *testing.T, data string) map[string]string {
	t.Helper()
	parts := map[string]string{}
	rest, ok := strings.CutPrefix(data, "!<arch>\n")
	for ok && len(rest) >= 60 {
		var size int
		for _, c := range strings.TrimSpace(rest[48:58]) {
			size = size*10 + int(c-'0')
		}
		parts[strings.TrimSpace(rest[:16])] = rest[60 : 60+size]
		rest = rest[60+size+size%2:]
	}
	if !ok || rest != "" || len(parts) != 3 {
		t.Fatalf("not an archive of three members: %d found, %d bytes left", len(parts), len(rest))
	}
	return parts
}

func untar(t *testing.T, data, name string) (string, int64) {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewBufferString(data))
	if err != nil {
		t.Fatal(err)
	}
	for tr := tar.NewReader(zr); ; {
		h, err := tr.Next()
		if err != nil {
			t.Fatalf("%s is not in the archive: %v", name, err)
		}
		if h.Name == name {
			body, _ := io.ReadAll(tr)
			return string(body), h.Mode
		}
	}
}
