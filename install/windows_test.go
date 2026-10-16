package install

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// install.ps1 is plain ASCII, which both PowerShells read alike from a file
// with no byte-order mark, is inside the size install.sh has, holds each
// line a release fills in once, and never ends the PowerShell it runs in.
func TestTheWindowsInstallerIsPlain(t *testing.T) {
	for i, c := range []byte(WindowsInstaller) {
		if c > 126 || c < 32 && c != '\n' && c != '\t' {
			t.Fatalf("install.ps1 holds the byte %#x at %d", c, i)
		}
	}
	if n := strings.Count(WindowsInstaller, "\n"); n > 150 {
		t.Errorf("install.ps1 has %d lines, more than 150", n)
	}
	for _, line := range []string{"\n$VERSION = 'dev'\n", "\n$SIGNER = ''\n"} {
		if strings.Count(WindowsInstaller, line) != 1 {
			t.Errorf("install.ps1 does not hold %q once", line)
		}
	}
	// Under `irm ... | iex` an exit would close the person's window.
	if regexp.MustCompile(`(?im)^[^#]*\bexit\b`).MatchString(WindowsInstaller) {
		t.Error("install.ps1 calls exit")
	}
}

// powershells runs try once in each PowerShell of this Windows machine:
// the Windows PowerShell 5.1 every Windows has, and PowerShell 7 where it
// is there. The script writes the login's PATH, so it is tried on a hosted
// Windows machine only, as the proof in test/platform/windows is.
func powershells(t *testing.T, try func(t *testing.T, shell string)) {
	t.Helper()
	if runtime.GOOS != "windows" || os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Skip("NOT PROVEN HERE: install.ps1 changes the login's PATH, so it runs on a hosted Windows machine only")
	}
	for _, name := range []string{"powershell", "pwsh"} {
		path, err := exec.LookPath(name)
		if err != nil && name == "powershell" {
			t.Fatal("no Windows PowerShell on this Windows machine")
		}
		if err == nil {
			t.Run(name, func(t *testing.T) { try(t, path) })
		}
	}
}

// winRelease makes a folder with a made-up Windows release of the version
// given, signed with a key of the test's own, and install.ps1 as that
// release would carry it.
func winRelease(t *testing.T, version string) (dir, script string) {
	t.Helper()
	dir, home := t.TempDir(), t.TempDir()
	keygen := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("ssh-keygen", args...).CombinedOutput(); err != nil {
			t.Skipf("ssh-keygen %v: %v: %s", args, err, out)
		}
	}
	program := []byte("MZ the program")
	list := fmt.Sprintf("# whaleshark %s\n%x  whaleshark-windows-amd64.exe\n%x  install.ps1\n", version, sha256.Sum256(program), sha256.Sum256([]byte(WindowsInstaller)))
	for name, data := range map[string][]byte{"whaleshark-windows-amd64.exe": program, "SHA256SUMS": []byte(list)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	key := filepath.Join(home, "release-key")
	keygen("-q", "-t", "ed25519", "-N", "", "-C", "release", "-f", key)
	keygen("-Y", "sign", "-f", key, "-n", "file", filepath.Join(dir, "SHA256SUMS"))
	public, _ := os.ReadFile(key + ".pub")
	signer := `release namespaces="file" ` + strings.Join(strings.Fields(string(public))[:2], " ")
	text := strings.Replace(WindowsInstaller, "\n$SIGNER = ''\n", "\n$SIGNER = '"+signer+"'\n", 1)
	text = strings.Replace(text, "\n$VERSION = 'dev'\n", "\n$VERSION = '1.2.3'\n", 1)
	script = filepath.Join(home, "install.ps1")
	if err := os.WriteFile(script, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, script
}

// winInstall runs install.ps1 as a file, with the login's LOCALAPPDATA at
// local and, where path is given, that PATH, and returns what it said.
func winInstall(t *testing.T, shell, local, path, script string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(shell, append([]string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script}, args...)...)
	cmd.Env = append(os.Environ(), "LOCALAPPDATA="+local)
	if path != "" {
		cmd.Env = append(cmd.Env, "PATH="+path)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestTheWindowsInstallerChecksAndInstalls(t *testing.T) { powershells(t, checksAndInstalls) }

func checksAndInstalls(t *testing.T, shell string) {
	dir, script := winRelease(t, "1.2.3")
	// The mark a browser puts on a download.
	exe := filepath.Join(dir, "whaleshark-windows-amd64.exe")
	if err := os.WriteFile(exe+":Zone.Identifier", []byte("[ZoneTransfer]\r\nZoneId=3\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	local := t.TempDir()
	for range 2 {
		out, err := winInstall(t, shell, local, "", script, "-From", dir)
		if err != nil {
			t.Fatalf("%v:\n%s", err, out)
		}
		if !strings.Contains(out, "SHA256:") || !strings.Contains(out, "ED25519") || !strings.Contains(out, "open a new terminal and type whaleshark") {
			t.Errorf("the key's fingerprint or the last line is missing:\n%s", out)
		}
	}
	place := filepath.Join(local, "Programs", "whaleshark")
	if got, _ := os.ReadFile(filepath.Join(place, "whaleshark.exe")); string(got) != "MZ the program" {
		t.Errorf("the installed file is %q", got)
	}
	if left, _ := os.ReadDir(place); len(left) != 1 {
		t.Errorf("%d entries beside the program: %v", len(left)-1, left)
	}
	if _, err := os.Stat(filepath.Join(place, "whaleshark.exe") + ":Zone.Identifier"); err == nil {
		t.Error("the installed file still carries the mark of the web")
	}
	// Two installs, one entry.
	out, _ := exec.Command("reg", "query", `HKCU\Environment`, "/v", "Path").CombinedOutput()
	n := 0
	for _, part := range strings.Split(strings.TrimSpace(string(out)), ";") {
		if strings.EqualFold(strings.TrimSpace(part), place) || strings.HasSuffix(strings.ToLower(part), " "+strings.ToLower(place)) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the login's PATH names the program's folder %d times:\n%s", n, out)
	}
}

// A wrong checksum, a changed list, another key, an older release's list,
// a copy with no key and a machine with no ssh-keygen: nothing installed.
func TestTheWindowsInstallerRefuses(t *testing.T) { powershells(t, refuses) }

func refuses(t *testing.T, shell string) {
	change := func(path string) {
		data, _ := os.ReadFile(path)
		data[len(data)/2] ^= 1
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	refused := func(what, want, path, script, dir string) {
		t.Helper()
		local := t.TempDir()
		out, err := winInstall(t, shell, local, path, script, "-From", dir)
		if err == nil {
			t.Errorf("%s: installed:\n%s", what, out)
		}
		if !strings.Contains(out, want) {
			t.Errorf("%s: refused without %q:\n%s", what, want, out)
		}
		if left, _ := os.ReadDir(filepath.Join(local, "Programs", "whaleshark")); len(left) != 0 {
			t.Errorf("%s: %d entries were left behind", what, len(left))
		}
	}

	dir, script := winRelease(t, "1.2.3")
	change(filepath.Join(dir, "whaleshark-windows-amd64.exe"))
	refused("a wrong checksum", "not the file the release signed", "", script, dir)

	dir, script = winRelease(t, "1.2.3")
	change(filepath.Join(dir, "SHA256SUMS"))
	refused("a changed list", "not signed by the release key", "", script, dir)

	dir, _ = winRelease(t, "1.2.3")
	_, other := winRelease(t, "1.2.3")
	refused("another key", "not signed by the release key", "", other, dir)

	dir, script = winRelease(t, "1.2.2")
	refused("an older release", "not version 1.2.3's", "", script, dir)

	plain := filepath.Join(t.TempDir(), "install.ps1")
	if err := os.WriteFile(plain, []byte(WindowsInstaller), 0o644); err != nil {
		t.Fatal(err)
	}
	refused("no key", "carries no release key", "", plain, dir)

	// ssh-keygen is in a folder of its own under System32.
	dir, script = winRelease(t, "1.2.3")
	refused("no ssh-keygen", "ssh-keygen is missing", filepath.Join(os.Getenv("SystemRoot"), "System32"), script, dir)
}

// Under `irm ... | iex` a refusal ends the script and not the PowerShell
// it was typed into, and leaves nothing of its own there.
func TestTheWindowsInstallerLeavesTheWindowOpen(t *testing.T) { powershells(t, leavesTheWindowOpen) }

func leavesTheWindowOpen(t *testing.T, shell string) {
	plain := filepath.Join(t.TempDir(), "install.ps1")
	if err := os.WriteFile(plain, []byte(WindowsInstaller), 0o644); err != nil {
		t.Fatal(err)
	}
	line := "try { Get-Content -Raw -LiteralPath '" + plain + "' | Invoke-Expression } catch { 'refused: ' + $_ }; 'still here ' + $ErrorActionPreference"
	cmd := exec.Command(shell, "-NoProfile", "-NonInteractive", "-Command", line)
	cmd.Env = append(os.Environ(), "LOCALAPPDATA="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "refused: install: this copy carries no release key") || !strings.Contains(string(out), "still here Continue") {
		t.Errorf("%v:\n%s", err, out)
	}
}
