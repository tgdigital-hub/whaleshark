package windows

import (
	"bytes"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// TestInstallFromTheWingetFiles makes a release's files with the tree's own
// release program, which writes the three files winget wants beside the
// program they name, and has winget install from those three files as they
// would be submitted. One thing differs from a submission: the address the
// program is fetched from is this machine's, since nothing is published;
// the checksum winget checks is the real one. Then the installed program
// must answer, and winget must take it away again.
func TestInstallFromTheWingetFiles(t *testing.T) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	root := strings.TrimSpace(string(out))
	if _, err := os.Stat(filepath.Join(root, "build", "release", "main.go")); err != nil {
		t.Skip("NOT PROVEN: this tree has no build/release yet, so there are no winget files and nothing was installed")
	}
	onlyHosted(t)
	if _, err := exec.LookPath("winget"); err != nil {
		t.Skip("NOT PROVEN HERE: this machine has no winget")
	}
	site, err := net.Listen("tcp", contract.Loopback+":0")
	if err != nil {
		t.Fatal(err)
	}
	defer site.Close()
	const version = "0.0.1"
	dist := t.TempDir()
	go http.Serve(site, http.StripPrefix("/v"+version+"/", http.FileServer(http.Dir(dist))))
	run := func(dir, name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	// A dry run's signer: the line's form with no key behind it.
	run(root, "go", "run", "./build/release", "-version", version, "-out", dist, "-site", "http://"+site.Addr().String(),
		"-signer", `release namespaces="file" ssh-ed25519 AAAA`)
	files := filepath.Join(dist, "winget")
	if names, _ := filepath.Glob(filepath.Join(files, "*.yaml")); len(names) != 3 {
		t.Fatalf("winget wants three files and the release made %v", names)
	}
	t.Logf("FACT winget validate: %s", bytes.TrimSpace([]byte(run(dist, "winget", "validate", "--manifest", files))))
	run(dist, "winget", "settings", "--enable", "LocalManifestFiles")
	t.Cleanup(func() { exec.Command("winget", "uninstall", "--manifest", files, "--disable-interactivity").Run() })
	t.Logf("FACT winget install: %s", run(dist, "winget", "install", "--manifest", files, "--disable-interactivity", "--accept-source-agreements"))
	// winget puts a portable program's name into a folder of links of its own.
	link := filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WinGet", "Links", "whaleshark.exe")
	if said := run(dist, link, "version"); !strings.Contains(said, version) {
		t.Errorf("the installed program says %q, not version %s", said, version)
	}
	run(dist, "winget", "uninstall", "--manifest", files, "--disable-interactivity")
	if _, err := os.Stat(link); err == nil {
		t.Errorf("%s is still there after the uninstall", link)
	}
}
