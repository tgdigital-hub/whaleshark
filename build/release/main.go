// Command release makes a release's files from the commit it is run in, the
// same bytes every time: the program for five systems, install.sh and
// install.ps1 with the version and the release key filled in, the list of
// their checksums, and the same files packed for apt, Homebrew and winget.
// It signs nothing and publishes nothing. Run it from the top of the
// repository:
//
//	go run ./build/release -version 1.2.3
//
// README.md beside this file says how a release is signed and checked.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	module = "github.com/tgdigital-hub/whaleshark"
	home   = "https://" + module
)

var (
	notices = []string{"LICENSE", "NOTICE"}
	// installers are the one-line installs a release carries: one for
	// macOS and Linux, one for Windows.
	installers = []string{"install.sh", "install.ps1"}
	targets    = []string{"darwin-arm64", "darwin-amd64", "linux-amd64", "linux-arm64", "windows-amd64"}
	versionRE  = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9a-z.]+)?$`)
	signerRE   = regexp.MustCompile(`^release namespaces="file" [a-z0-9@.-]+ [A-Za-z0-9+/=]+$`)
	pinRE      = regexp.MustCompile(`^[0-9A-Za-z.~+:-]*$`)
)

type release struct {
	root, out, version, site string
	set                      map[string]string
	when                     time.Time // every file's time, and the apt index's date
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("release", flag.ContinueOnError)
	version := fs.String("version", "", "the release's version, as 1.2.3")
	out := fs.String("out", "dist", "the folder the files are written to")
	signer := fs.String("signer", "", "an allowed-signers line, in place of the one in settings.txt")
	site := fs.String("site", home+"/releases/download", "where a release's files are fetched from")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	r := &release{root: root, out: *out, version: *version, site: *site}
	if r.set, err = settings(filepath.Join(root, "build", "release", "settings.txt")); err != nil {
		return err
	}
	if *signer != "" {
		r.set["signer"] = *signer
	}
	r.set["connector-key"] = strings.ReplaceAll(r.set["connector-key"], " ", "")
	switch {
	case !versionRE.MatchString(r.version):
		return errors.New("give -version as 1.2.3 or 1.2.3-word")
	case r.set["signer"] == "":
		return errors.New("no release key: settings.txt has no signer line and -signer gave none")
	case !signerRE.MatchString(r.set["signer"]):
		return errors.New(`the signer is not one line of the form: release namespaces="file" <key type> <key>`)
	case !pinRE.MatchString(r.set["connector"]) || !pinRE.MatchString(r.set["connector-key"]):
		return errors.New("the connector's version or key in settings.txt holds a character a version cannot")
	}
	if err := toolchain(root); err != nil {
		return err
	}
	if r.when, err = when(root); err != nil {
		return err
	}
	scripts, err := r.scripts()
	if err != nil {
		return err
	}
	for _, name := range installers {
		if err := r.put(name, []byte(scripts[name]), 0o644); err != nil {
			return err
		}
	}
	// The licences of what is compiled into the program go with it.
	for _, name := range notices {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err == nil {
			err = r.put(name, data, 0o644)
		}
		if err != nil {
			return err
		}
	}
	if err := r.compile(scripts); err != nil {
		return err
	}
	return r.pack()
}

// settings reads one setting a line: a name, then its value.
func settings(file string) (map[string]string, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	set := map[string]string{}
	for line := range strings.SplitSeq(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			name, value, _ := strings.Cut(line, " ")
			set[name] = strings.TrimSpace(value)
		}
	}
	return set, nil
}

// toolchain refuses any Go but the one go.mod names: another compiler, or
// another compressor in its library, would give other bytes.
func toolchain(root string) error {
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return err
	}
	m := regexp.MustCompile(`(?m)^go (\S+)$`).FindSubmatch(mod)
	if m == nil {
		return errors.New("go.mod names no Go")
	}
	if want := "go" + string(m[1]); runtime.Version() != want {
		return fmt.Errorf("this is %s and go.mod names %s: run with GOTOOLCHAIN=%s", runtime.Version(), want, want)
	}
	return nil
}

// when is the time of the commit, or what SOURCE_DATE_EPOCH says: the
// variable many projects' repeatable builds read.
func when(root string) (time.Time, error) {
	text := os.Getenv("SOURCE_DATE_EPOCH")
	if text == "" {
		cmd := exec.Command("git", "log", "-1", "--format=%ct")
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			return time.Time{}, fmt.Errorf("the commit's time: %w (or set SOURCE_DATE_EPOCH)", err)
		}
		text = strings.TrimSpace(string(out))
	}
	sec, err := strconv.ParseInt(text, 10, 64)
	return time.Unix(sec, 0).UTC(), err
}

// scripts gives the three scripts with the release's values in them, by
// their names: the two installers and the server's. A line that is not
// there exactly once fails the release: a script that changed must not
// ship unfilled. The root script's test folder goes too.
func (r *release) scripts() (map[string]string, error) {
	var errs []error
	texts := map[string]string{}
	for _, name := range append(installers, "server.sh") {
		data, err := os.ReadFile(filepath.Join(r.root, "install", name))
		errs = append(errs, err)
		texts[name] = string(data)
	}
	stamp := func(name, old, new string) {
		text := texts[name]
		if strings.Count(text, old) != 1 {
			errs = append(errs, fmt.Errorf("%s does not hold the line %q once", name, strings.TrimSpace(old)))
		}
		texts[name] = strings.Replace(text, old, new, 1)
	}
	stamp("install.sh", "\nVERSION=dev\n", "\nVERSION="+r.version+"\n")
	stamp("install.sh", "\nSIGNER=''\n", "\nSIGNER='"+r.set["signer"]+"'\n")
	stamp("install.ps1", "\n$VERSION = 'dev'\n", "\n$VERSION = '"+r.version+"'\n")
	stamp("install.ps1", "\n$SIGNER = ''\n", "\n$SIGNER = '"+r.set["signer"]+"'\n")
	stamp("server.sh", "\nR=${WHALESHARK_TEST_ROOT:-}\n", "\nR=\n")
	stamp("server.sh", "\nCONNECTOR=''\n", "\nCONNECTOR='"+r.set["connector"]+"'\n")
	stamp("server.sh", "\nCONNECTOR_KEY=''\n", "\nCONNECTOR_KEY='"+r.set["connector-key"]+"'\n")
	return texts, errors.Join(errs...)
}

// compile builds the five files with the filled-in scripts laid over the
// tree's own, so the tree is not touched. Nothing of the machine goes in:
// no path, no debug symbols, no word of the checkout's state.
func (r *release) compile(scripts map[string]string) error {
	tmp, err := os.MkdirTemp("", "whaleshark-release")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	over := map[string]string{}
	for name, text := range scripts {
		over[filepath.Join(r.root, "install", name)] = filepath.Join(tmp, name)
		if err := os.WriteFile(filepath.Join(tmp, name), []byte(text), 0o600); err != nil {
			return err
		}
	}
	overlay, _ := json.Marshal(map[string]any{"Replace": over})
	if err := os.WriteFile(filepath.Join(tmp, "overlay.json"), overlay, 0o600); err != nil {
		return err
	}
	for _, target := range targets {
		goos, goarch, _ := strings.Cut(target, "-")
		// #nosec G204 -- always the program go, with a list of arguments and no shell
		cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-overlay", filepath.Join(tmp, "overlay.json"),
			"-ldflags=-s -w -X "+module+"/internal/cli.Version="+r.version, "-o", filepath.Join(r.out, file(target)), "./cmd/whaleshark")
		cmd.Dir = r.root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch,
			"GOFLAGS=", "GOWORK=off", "GOAMD64=v1", "GOARM64=v8.0")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("building for %s: %w\n%s", target, err, out)
		}
	}
	return nil
}

// file is the name a system's program has in a release.
func file(target string) string {
	if strings.HasPrefix(target, "windows") {
		return "whaleshark-" + target + ".exe"
	}
	return "whaleshark-" + target
}

func (r *release) put(name string, data []byte, mode os.FileMode) error {
	path := filepath.Join(r.out, filepath.FromSlash(name))
	// #nosec G301 G306 G703 -- a release's files are for everyone to read, in the folder the command names
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode) // #nosec G306 G703 -- as above
}

func sum(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
