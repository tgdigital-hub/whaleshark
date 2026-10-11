package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	about   = "Runs a team of coding agents in terminal tabs"
	contact = "WhaleShark <tgdigital-hub@users.noreply.github.com>"
	winget  = "WhaleShark.WhaleShark"
	aptKey  = "whaleshark.asc"
)

// pack writes the list of checksums and, from the same five files, what
// each system's package manager reads.
func (r *release) pack() error {
	sums := map[string]string{}
	list := "# whaleshark " + r.version + "\n"
	for _, name := range listed() {
		data, err := os.ReadFile(filepath.Join(r.out, name))
		if err != nil {
			return err
		}
		sums[name] = sum(data)
		list += sums[name] + "  " + name + "\n"
	}
	if err := r.put("SHA256SUMS", []byte(list), 0o644); err != nil {
		return err
	}
	if err := r.apt(); err != nil {
		return err
	}
	if err := r.put("brew/whaleshark.rb", []byte(r.formula(sums)), 0o644); err != nil {
		return err
	}
	for name, text := range r.winget(sums) {
		if err := r.put("winget/"+name, []byte(text), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// listed is every file the signed list names: the five programs, the
// installer and the two texts of the licences.
func listed() []string { return append(append(names(), "install.sh"), notices...) }

func names() []string {
	var all []string
	for _, target := range targets {
		all = append(all, file(target))
	}
	return all
}

func (r *release) url(name string) string { return r.site + "/v" + r.version + "/" + name }

// apt writes a repository of static files: a package for each Linux file
// and the index apt reads. The index's signature is made by whoever holds
// the repository's key, as InRelease beside Release.
func (r *release) apt() error {
	// Debian sorts a word after a tilde before the version without it.
	version := strings.Replace(r.version, "-", "~", 1)
	index := fmt.Sprintf("Origin: WhaleShark\nLabel: WhaleShark\nSuite: stable\nCodename: stable\nDate: %s\nArchitectures: amd64 arm64\nComponents: main\nSHA256:\n",
		r.when.Format(time.RFC1123))
	// A package says whose it is and under which licences, where Debian
	// looks for it: the two texts every release carries, one after the other.
	var rights []byte
	for _, name := range notices {
		text, err := os.ReadFile(filepath.Join(r.out, name))
		if err != nil {
			return err
		}
		rights = append(append(rights, text...), '\n')
	}
	for _, arch := range []string{"amd64", "arm64"} {
		program, err := os.ReadFile(filepath.Join(r.out, "whaleshark-linux-"+arch))
		if err != nil {
			return err
		}
		control := fmt.Sprintf("Package: whaleshark\nVersion: %s\nArchitecture: %s\nMaintainer: %s\nInstalled-Size: %d\nRecommends: git\nSection: utils\nPriority: optional\nHomepage: %s\nDescription: %s\n",
			version, arch, contact, (len(program)+len(rights)+1023)/1024, home, about)
		deb := ar(r.when,
			member{"debian-binary", []byte("2.0\n"), 0},
			member{"control.tar.gz", r.tgz(member{"./control", []byte(control), 0o644}), 0},
			member{"data.tar.gz", r.tgz(member{"./usr/bin/whaleshark", program, 0o755},
				member{"./usr/share/doc/whaleshark/copyright", rights, 0o644}), 0})
		pool := "pool/whaleshark_" + version + "_" + arch + ".deb"
		packages := fmt.Sprintf("%sFilename: %s\nSize: %d\nSHA256: %s\n", control, pool, len(deb), sum(deb))
		listed := "main/binary-" + arch + "/Packages"
		index += fmt.Sprintf(" %s %d %s\n", sum([]byte(packages)), len(packages), listed)
		if err := r.put("apt/"+pool, deb, 0o644); err != nil {
			return err
		}
		if err := r.put("apt/dists/stable/"+listed, []byte(packages), 0o644); err != nil {
			return err
		}
	}
	// The public half of the key the index is signed with: the file a
	// machine is given before its first install, at the repository's top.
	key, err := os.ReadFile(filepath.Join(r.root, "build", "release", aptKey))
	if err != nil {
		return err
	}
	if err := r.put("apt/"+aptKey, key, 0o644); err != nil {
		return err
	}
	return r.put("apt/dists/stable/Release", []byte(index), 0o644)
}

// tgz is a gzipped tar archive of the files and the folders above them,
// each folder once and before what lies in it, all root's and all of the
// release's time.
func (r *release) tgz(files ...member) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	made := map[string]bool{}
	for _, f := range files {
		dirs := []string{"./"}
		for dir := path.Dir(f.name); dir != "."; dir = path.Dir(dir) {
			dirs = slices.Insert(dirs, 1, "./"+dir+"/")
		}
		for _, dir := range dirs {
			if !made[dir] {
				made[dir] = true
				tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: dir, Mode: 0o755, ModTime: r.when, Uname: "root", Gname: "root", Format: tar.FormatUSTAR})
			}
		}
		tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.data)), ModTime: r.when, Uname: "root", Gname: "root", Format: tar.FormatUSTAR})
		tw.Write(f.data)
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

// member is one file of an archive; the librarian's format keeps no mode of ours.
type member struct {
	name string
	data []byte
	mode int64
}

// ar writes the archive format of the Unix librarian, which a Debian
// package is: a mark, then for each member a line of sixty characters and
// its bytes, padded to an even length.
func ar(when time.Time, members ...member) []byte {
	buf := bytes.NewBufferString("!<arch>\n")
	for _, m := range members {
		fmt.Fprintf(buf, "%-16s%-12d%-6d%-6d%-8s%-10d`\n", m.name, when.Unix(), 0, 0, "100644", len(m.data))
		buf.Write(m.data)
		if len(m.data)%2 == 1 {
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes()
}

// formula is the one file of a Homebrew tap: each file's address and its
// checksum, and nothing to build.
func (r *release) formula(sums map[string]string) string {
	on := func(cpu, name string) string {
		return fmt.Sprintf("    on_%s do\n      url \"%s\"\n      sha256 \"%s\"\n    end\n", cpu, r.url(name), sums[name])
	}
	return fmt.Sprintf(`class Whaleshark < Formula
  desc "%s"
  homepage "%s"
  version "%s"
  license "MIT"

  on_macos do
%s%s  end

  on_linux do
%s%s  end

  def install
    bin.install Dir["whaleshark-*"].first => "whaleshark"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/whaleshark version")
  end
end
`, about, home, r.version,
		on("arm", "whaleshark-darwin-arm64"), on("intel", "whaleshark-darwin-amd64"),
		on("arm", "whaleshark-linux-arm64"), on("intel", "whaleshark-linux-amd64"))
}

// winget gives the three files a version of a package is in Windows'
// package list: the version, the installer and the words.
func (r *release) winget(sums map[string]string) map[string]string {
	exe := "whaleshark-windows-amd64.exe"
	head := "PackageIdentifier: " + winget + "\nPackageVersion: " + r.version + "\n"
	tail := "ManifestVersion: 1.6.0\n"
	return map[string]string{
		winget + ".yaml": head + "DefaultLocale: en-US\nManifestType: version\n" + tail,
		winget + ".installer.yaml": head + "InstallerType: portable\nCommands:\n- whaleshark\nInstallers:\n- Architecture: x64\n  InstallerUrl: " +
			r.url(exe) + "\n  InstallerSha256: " + strings.ToUpper(sums[exe]) + "\nManifestType: installer\n" + tail,
		winget + ".locale.en-US.yaml": head + "PackageLocale: en-US\nPublisher: WhaleShark\nPackageName: WhaleShark\nPackageUrl: " + home +
			"\nLicense: MIT\nShortDescription: " + about + "\nManifestType: defaultLocale\n" + tail,
	}
}
