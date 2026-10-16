package install

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// shell runs sh with the script on its standard input, as `curl ... | sh`
// does, in a login whose shell is login (none when empty).
func shell(home, login, stdin string, args ...string) (string, error) {
	cmd := exec.Command("sh", args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home}
	if login != "" {
		cmd.Env = append(cmd.Env, "SHELL="+login)
	}
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func text(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Piped to sh, the script installs, and leaves its input to sh: a command
// after it still runs. A download cut short runs nothing; a release signed
// with another key, or a changed file, is refused piped as well.
func TestTheInstallerWorksPipedToSh(t *testing.T) {
	dir, script, home := release(t)
	out, err := shell(home, "", text(t, script)+"echo sh read on\n", "-s", "--", "--from", dir)
	if err != nil || !strings.Contains(out, "installed "+filepath.Join(home, ".local", "bin", "whaleshark")) {
		t.Fatalf("%v:\n%s", err, out)
	}
	if !strings.HasSuffix(out, "sh read on\n") {
		t.Errorf("something read the script's input:\n%s", out)
	}

	dir, script, home = release(t)
	was, _ := os.ReadDir(home)
	whole := text(t, script)
	cut := whole[:strings.LastIndex(whole, "\nmain \"$@\"")]
	if out, err := shell(home, "", cut[:len(cut)*2/3], "-s", "--", "--from", dir); strings.Contains(out, "installed") {
		t.Errorf("two thirds of the script installed (%v):\n%s", err, out)
	}
	if out, err := shell(home, "", cut, "-s", "--", "--from", dir); err != nil || out != "" {
		t.Errorf("the script without its last line did something (%v):\n%s", err, out)
	}
	if left, _ := os.ReadDir(home); len(left) != len(was) {
		t.Errorf("a script cut short left %d entries in the home folder", len(left)-len(was))
	}

	dir, _, home = release(t)
	_, other, _ := release(t)
	if out, err := shell(home, "", text(t, other), "-s", "--", "--from", dir); err == nil || !strings.Contains(out, "not signed by the release key") {
		t.Errorf("another key, piped: %v:\n%s", err, out)
	}
	dir, script, home = release(t)
	program := filepath.Join(dir, "whaleshark-"+runtime.GOOS+"-"+runtime.GOARCH)
	os.WriteFile(program, []byte("#!/bin/sh\necho another program\n"), 0o644)
	if out, err := shell(home, "", text(t, script), "-s", "--", "--from", dir); err == nil || !strings.Contains(out, "not the file the release signed") {
		t.Errorf("a changed program, piped: %v:\n%s", err, out)
	}
}

// Where ~/.local/bin is not on PATH, the login's shell gets one line for it
// in the start files it reads, and a second run, an upgrade, changes none.
// The line puts the folder on PATH once, however often it is read.
func TestTheInstallerPutsTheProgramOnPathOnce(t *testing.T) {
	for _, c := range []struct {
		shell string
		there []string // start files there before
		want  []string // start files that get the line
	}{
		{"/bin/zsh", nil, []string{".zprofile", ".zshrc"}},
		{"/bin/zsh", []string{".zshrc"}, []string{".zprofile", ".zshrc"}},
		{"/bin/bash", nil, []string{".profile", ".bashrc"}},
		{"/usr/local/bin/bash", []string{".bash_profile", ".profile"}, []string{".bash_profile", ".bashrc"}},
		{"/bin/bash", []string{".profile"}, []string{".profile", ".bashrc"}},
		{"/bin/ksh", nil, []string{".profile"}},
	} {
		dir, script, home := release(t)
		for _, f := range c.there {
			os.WriteFile(filepath.Join(home, f), []byte("# the person's own\n"), 0o644)
		}
		before := starts(t, home)
		out, err := shell(home, c.shell, "", script, "--from", dir)
		if err != nil {
			t.Fatalf("%s: %v:\n%s", c.shell, err, out)
		}
		after := starts(t, home)
		var changed []string
		for _, f := range startFiles {
			if after[f] != before[f] {
				changed = append(changed, f)
				if !strings.HasPrefix(after[f], before[f]) || strings.Count(after[f], MarkLine) != 1 {
					t.Errorf("%s: %s is\n%s", c.shell, f, after[f])
				}
				if !strings.Contains(out, "added "+filepath.Join(home, ".local", "bin")+" to PATH in "+filepath.Join(home, f)) {
					t.Errorf("%s: it does not say it changed %s:\n%s", c.shell, f, out)
				}
			}
		}
		if strings.Join(changed, " ") != strings.Join(c.want, " ") {
			t.Errorf("%s with %v: it changed %v, not %v", c.shell, c.there, changed, c.want)
		}
		if !strings.Contains(out, "Open a new terminal") {
			t.Errorf("%s: it does not say to open a new terminal:\n%s", c.shell, out)
		}

		out, err = shell(home, c.shell, "", script, "--from", dir)
		if err != nil || strings.Contains(out, "added") || strings.Contains(out, "new terminal") {
			t.Errorf("%s: the second run: %v:\n%s", c.shell, err, out)
		}
		if again := starts(t, home); !maps.Equal(again, after) {
			t.Errorf("%s: the second run changed a start file", c.shell)
		}

		// What a new terminal reads, twice over, finds the program, and
		// names its folder in PATH once.
		var reads []string
		for _, f := range c.want {
			reads = append(reads, `. "$HOME/`+f+`"`)
		}
		cmd := exec.Command("sh", "-c", strings.Join(append(reads, reads...), "; ")+`; command -v whaleshark; echo "$PATH"`)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home}
		found, err := cmd.CombinedOutput()
		bin := filepath.Join(home, ".local", "bin")
		if err != nil || string(found) != filepath.Join(bin, "whaleshark")+"\n"+bin+":/usr/bin:/bin\n" {
			t.Errorf("%s: a new terminal reads (%v):\n%s", c.shell, err, found)
		}
	}
}

// A login whose PATH already has the folder gets no start file changed.
func TestTheInstallerLeavesAPathThatHasTheFolder(t *testing.T) {
	dir, script, home := release(t)
	cmd := exec.Command("sh", script, "--from", dir)
	cmd.Env = []string{"PATH=/usr/bin:" + filepath.Join(home, ".local", "bin") + ":/bin", "HOME=" + home, "SHELL=/bin/zsh"}
	out, err := cmd.CombinedOutput()
	if err != nil || strings.Contains(string(out), "added") || strings.Contains(string(out), "new terminal") {
		t.Errorf("%v:\n%s", err, out)
	}
	if got := starts(t, home); len(got) != 0 {
		t.Errorf("start files were made: %v", got)
	}
}

// MarkLine is the end of the line install.sh puts in a start file.
var MarkLine = "# added by whaleshark install.sh"

var startFiles = []string{".zprofile", ".zshrc", ".bash_profile", ".bash_login", ".profile", ".bashrc"}

func starts(t *testing.T, home string) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, f := range startFiles {
		if data, err := os.ReadFile(filepath.Join(home, f)); err == nil {
			got[f] = string(data)
		}
	}
	return got
}
