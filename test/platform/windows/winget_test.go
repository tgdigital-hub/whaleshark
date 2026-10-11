package windows

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// subject names the certificate this test makes, trusts and takes away.
const subject = "whaleshark winget proof"

// ownSite serves a folder over https on this machine, under a certificate
// made here and trusted by this machine for the length of the test: winget
// takes an installer's address only with https, and nothing is published.
func ownSite(t *testing.T, dir, prefix string) (address string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	about := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: subject},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), DNSNames: []string{"localhost"},
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true}
	der, err := x509.CreateCertificate(rand.Reader, about, about, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "site.cer")
	if err := os.WriteFile(file, der, 0o600); err != nil {
		t.Fatal(err)
	}
	store := `Cert:\LocalMachine\Root`
	t.Cleanup(func() {
		exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
			"Get-ChildItem "+store+" | Where-Object Subject -eq 'CN="+subject+"' | Remove-Item").Run()
	})
	if out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"Import-Certificate -FilePath '"+file+"' -CertStoreLocation "+store).CombinedOutput(); err != nil {
		t.Fatalf("trusting the test's own certificate: %v\n%s", err, out)
	}
	site, err := net.Listen("tcp", contract.Loopback+":0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { site.Close() })
	served := &http.Server{Handler: http.StripPrefix(prefix, http.FileServer(http.Dir(dir))),
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}}
	go served.ServeTLS(site, "", "")
	_, port, _ := net.SplitHostPort(site.Addr().String())
	return "https://localhost:" + port
}

// TestInstallFromTheWingetFiles makes a release's files with the tree's own
// release program, which writes the three files winget wants beside the
// program they name, and has winget install from those three files as they
// would be submitted. One thing differs from a submission: the address the
// program is fetched from is this machine's, since nothing is published;
// the checksum winget checks is the real one. Then the installed program
// must answer, and winget must take it away again. It installs on a hosted
// machine only, and publishes and submits nothing.
func TestInstallFromTheWingetFiles(t *testing.T) {
	onlyHosted(t)
	if _, err := exec.LookPath("winget"); err != nil {
		missing(t, "winget")
	}
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	root := strings.TrimSpace(string(out))
	const version = "0.0.1"
	dist := t.TempDir()
	address := ownSite(t, dist, "/v"+version+"/")
	// No step may hang the whole run: each has its own few minutes.
	run := func(dir, name string, args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir, cmd.WaitDelay = dir, 5*time.Second
		out, err := cmd.CombinedOutput()
		return string(bytes.TrimSpace(out)), err
	}
	must := func(dir, name string, args ...string) string {
		t.Helper()
		out, err := run(dir, name, args...)
		if err != nil {
			t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
		}
		return out
	}
	// A dry run's signer: the line's form with no key behind it.
	must(root, "go", "run", "./build/release", "-version", version, "-out", dist, "-site", address,
		"-signer", `release namespaces="file" ssh-ed25519 AAAA`)
	files := filepath.Join(dist, "winget")
	names, _ := filepath.Glob(filepath.Join(files, "*.yaml"))
	if len(names) != 3 {
		t.Fatalf("winget wants three files and the release made %v", names)
	}
	for _, name := range names {
		text, _ := os.ReadFile(name)
		t.Logf("FACT %s:\n%s", filepath.Base(name), text)
	}
	t.Logf("FACT winget --version: %s", must(dist, "winget", "--version"))
	t.Logf("FACT winget validate: %s", must(dist, "winget", "validate", "--manifest", files))
	must(dist, "winget", "settings", "--enable", "LocalManifestFiles")
	t.Cleanup(func() {
		run(dist, "winget", "uninstall", "--manifest", files, "--disable-interactivity", "--accept-source-agreements")
	})
	t.Logf("FACT winget install: %s", must(dist, "winget", "install", "--manifest", files, "--disable-interactivity",
		"--accept-source-agreements", "--accept-package-agreements"))
	// winget puts a portable program's name into a folder of links: the
	// login's own, or the machine's where it installs for everybody.
	link := ""
	for _, dir := range []string{filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WinGet", "Links"), filepath.Join(os.Getenv("ProgramFiles"), "WinGet", "Links")} {
		if fileIsThere(filepath.Join(dir, "whaleshark.exe")) {
			link = filepath.Join(dir, "whaleshark.exe")
		}
	}
	if link == "" {
		t.Fatalf("winget installed, and neither folder of links holds whaleshark.exe")
	}
	t.Logf("FACT the installed command is %s", link)
	if said := must(dist, link, "version"); !strings.Contains(said, version) {
		t.Errorf("the installed program says %q, not version %s", said, version)
	}
	t.Logf("FACT winget uninstall: %s", must(dist, "winget", "uninstall", "--manifest", files, "--disable-interactivity", "--accept-source-agreements"))
	if fileIsThere(link) {
		t.Errorf("%s is still there after the uninstall", link)
	}
}
