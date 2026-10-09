package platform

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// on is a made-up login on the named system.
func on(goos string, env map[string]string) *System {
	home := "/people/ada"
	switch goos {
	case onMac:
		home = "/Volumes/People/ada"
	case onWindows:
		home = `D:\People\ada`
	}
	return &System{OS: goos, Env: func(k string) string { return env[k] }, Home: home}
}

// modes skips a test of permission bits where the system has none.
func modes(t *testing.T) {
	if runtime.GOOS == onWindows {
		t.Skip("no permission bits on this system")
	}
}

func TestPlug(t *testing.T) {
	k := contract.NewKit()
	Plug(k)
	if s, ok := k.Platform.(*System); !ok || s.OS != runtime.GOOS {
		t.Fatalf("the kit holds %#v", k.Platform)
	}
}

func TestDirs(t *testing.T) {
	winEnv := map[string]string{"APPDATA": `D:\People\ada\AppData\Roaming`, "LOCALAPPDATA": `D:\People\ada\AppData\Local`}
	for _, c := range []struct {
		name string
		s    *System
		want contract.Dirs
	}{
		{"linux", on("linux", nil), contract.Dirs{Config: "/people/ada/.config/whaleshark", State: "/people/ada/.local/state/whaleshark", Cache: "/people/ada/.cache/whaleshark"}},
		{"mac", on(onMac, nil), contract.Dirs{Config: "/Volumes/People/ada/.config/whaleshark", State: "/Volumes/People/ada/.local/state/whaleshark", Cache: "/Volumes/People/ada/.cache/whaleshark"}},
		{"xdg", on("linux", map[string]string{"XDG_CONFIG_HOME": "/c", "XDG_STATE_HOME": "/s", "XDG_CACHE_HOME": "/k"}),
			contract.Dirs{Config: "/c/whaleshark", State: "/s/whaleshark", Cache: "/k/whaleshark"}},
		{"xdg relative is ignored", on("linux", map[string]string{"XDG_CACHE_HOME": "cache"}),
			contract.Dirs{Config: "/people/ada/.config/whaleshark", State: "/people/ada/.local/state/whaleshark", Cache: "/people/ada/.cache/whaleshark"}},
		{"windows", on(onWindows, winEnv), contract.Dirs{Config: `D:\People\ada\AppData\Roaming\whaleshark`,
			State: `D:\People\ada\AppData\Local\whaleshark`, Cache: `D:\People\ada\AppData\Local\whaleshark\cache`}},
	} {
		if got, err := c.s.Dirs(); err != nil || got != c.want {
			t.Errorf("%s: %+v, %v", c.name, got, err)
		}
	}
	if _, err := on(onWindows, nil).Dirs(); err == nil {
		t.Error("windows without its variables gave folders")
	}
	if _, err := (&System{OS: "linux", Env: func(string) string { return "" }}).Dirs(); err == nil {
		t.Error("a login without a home gave folders")
	}
}

func TestPathKey(t *testing.T) {
	for _, c := range []struct{ goos, in, want string }{
		{"linux", "/people/ada/Code/", "/people/ada/Code"},
		{"linux", "/people/ada//x/../Code", "/people/ada/Code"},
		{onMac, "/Volumes/People/Ada/Code/", "/volumes/people/ada/code"},
		{onWindows, `D:\People\Ada\Code\`, "d:/people/ada/code"},
		{onWindows, `D:/People/ada/./code`, "d:/people/ada/code"},
	} {
		if got := on(c.goos, nil).PathKey(c.in); got != c.want {
			t.Errorf("%s %q: %q", c.goos, c.in, got)
		}
	}
}

func TestSynced(t *testing.T) {
	win := map[string]string{"OneDriveCommercial": `E:\Work Files`}
	for _, c := range []struct {
		goos, path string
		want       bool
	}{
		{onMac, "/Volumes/People/ada/Library/Mobile Documents/com~apple~CloudDocs/site", true},
		{onMac, "/Volumes/People/ada/Library/CloudStorage/Dropbox/site", true},
		{onMac, "/Volumes/People/ada/Dropbox", true},
		{onMac, "/Volumes/People/ada/dropbox (Personal)/site", true},
		{onMac, "/Volumes/People/ada/Google Drive/My Drive/site", true},
		{onMac, "/Volumes/People/ada/code/Dropbox", false},
		{onMac, "/Volumes/People/ada/dropbox-tools/site", false},
		{onMac, "/Volumes/People/ada/Library/Caches/site", false},
		{onMac, "/Volumes/People/grace/Dropbox/site", false},
		{"linux", "/people/ada/Dropbox/site", true},
		{"linux", "/people/ada/OneDrive", true},
		{"linux", "/people/ada/Library/CloudStorage/x", false},
		{"linux", "/people/ada/code/site", false},
		{onWindows, `D:\People\ada\OneDrive - Example\site`, true},
		{onWindows, `D:\PEOPLE\ADA\iCloudDrive\site`, true},
		{onWindows, `e:\work files\site`, true},
		{onWindows, `E:\Work Files 2\site`, false},
		{onWindows, `D:\People\ada\code\site`, false},
	} {
		if got := on(c.goos, win).Synced(c.path); got != c.want {
			t.Errorf("%s %q: %v", c.goos, c.path, got)
		}
	}
}

var hostile = []string{"", "plain-id_7", "two words", "it's", `say "hi"`, "$(reboot)", "`reboot`", "a;b&c|d>e<f",
	"*.go", "-rf", "~", "50%", "%PATH%", `C:\dir\`, `tail\\"x`, "caret^(x)", "a\u2019b", "\tx", "é!#"}

func TestQuotePosix(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on this system")
	}
	args := append([]string{"line\nbreak"}, hostile...)
	line := on("linux", nil).Quote(args)
	out, err := exec.Command("sh", "-c", "set -- "+line+`; printf '%s\0' "$@"`).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00"); !reflect.DeepEqual(got, args) {
		t.Errorf("the shell read\n%q\nfrom\n%s", got, line)
	}
	if got := Quote(Posix, []string{"/opt/bin/whaleshark", "report", "T3"}); got != "/opt/bin/whaleshark report T3" {
		t.Errorf("plain words were quoted: %s", got)
	}
}

func TestQuotePowerShell(t *testing.T) {
	got := on(onWindows, nil).Quote([]string{`C:\Program Files\ws.exe`, "it's", "a\u2019b", "$env:X `n", ""})
	want := `& 'C:\Program Files\ws.exe' 'it''s' 'a` + "\u2019\u2019" + `b' '$env:X ` + "`n" + `' ''`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	for _, arg := range hostile {
		q := strings.TrimPrefix(Quote(PowerShell, []string{arg}), "& ")
		inner := q[1 : len(q)-1]
		for _, mark := range []string{"'", "\u2018", "\u2019", "\u201a", "\u201b"} {
			inner = strings.ReplaceAll(inner, mark+mark, "")
		}
		if q[0] != '\'' || q[len(q)-1] != '\'' || strings.ContainsAny(inner, "'\u2018\u2019\u201a\u201b") {
			t.Errorf("%q can end its string: %s", arg, q)
		}
	}
}

// cmdReads is the test's own model of the two readers of a cmd line: cmd
// removes a caret outside double quotes and must meet nothing else of its own
// there; the program then splits the line by the rules of its start-up code.
func cmdReads(t *testing.T, line string) []string {
	var afterCmd strings.Builder
	quoted := false
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '"':
			quoted = !quoted
			afterCmd.WriteByte(c)
		case c == '^' && !quoted:
			i++
			afterCmd.WriteByte(line[i])
		case !quoted && strings.IndexByte("&|<>()%!\n", c) >= 0:
			t.Errorf("cmd would read %q as its own in: %s", c, line)
		default:
			afterCmd.WriteByte(c)
		}
	}
	var args []string
	var arg strings.Builder
	rest, inArg := afterCmd.String(), false
	quoted = false
	for i := 0; i < len(rest); i++ {
		slashes := 0
		for i < len(rest) && rest[i] == '\\' {
			slashes, i = slashes+1, i+1
		}
		if i < len(rest) && rest[i] == '"' {
			arg.WriteString(strings.Repeat(`\`, slashes/2))
			inArg = true
			switch {
			case slashes%2 == 1:
				arg.WriteByte('"')
			case quoted && i+1 < len(rest) && rest[i+1] == '"':
				arg.WriteByte('"')
				i++
			default:
				quoted = !quoted
			}
			continue
		}
		arg.WriteString(strings.Repeat(`\`, slashes))
		if slashes > 0 {
			inArg = true
		}
		if i == len(rest) {
			break
		}
		if rest[i] == ' ' && !quoted {
			if inArg {
				args = append(args, arg.String())
				arg.Reset()
				inArg = false
			}
			continue
		}
		arg.WriteByte(rest[i])
		inArg = true
	}
	if inArg {
		args = append(args, arg.String())
	}
	return args
}

func TestQuoteCmd(t *testing.T) {
	if got, want := Quote(Cmd, []string{`C:\bin\ws.exe`, "50%", `say "hi"`}), `"C:\bin\ws.exe" "50"^%"" "say ""hi"""`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	line := Quote(Cmd, hostile)
	if got := cmdReads(t, line); !reflect.DeepEqual(got, hostile) {
		t.Errorf("the model read\n%q\nfrom\n%s", got, line)
	}
}

func TestReplaceRetriesOnWindows(t *testing.T) {
	sharing := &os.LinkError{Op: "rename", Err: syscall.Errno(32)}
	var slept []time.Duration
	sleep := func(d time.Duration) { slept = append(slept, d) }
	calls := 0
	busyFor := func(n int) func(string, string) error {
		return func(string, string) error {
			if calls++; calls <= n {
				return sharing
			}
			return nil
		}
	}

	if err := on(onWindows, nil).replace("a", "b", busyFor(3), sleep); err != nil || calls != 4 {
		t.Fatalf("held open three times: %v after %d tries", err, calls)
	}
	if want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond}; !reflect.DeepEqual(slept, want) {
		t.Errorf("the pauses were %v", slept)
	}

	calls, slept = 0, nil
	err := on(onWindows, nil).replace("a", "b", busyFor(1<<30), sleep)
	var refusal *contract.Refusal
	if !errors.As(err, &refusal) || refusal.Code != "state_busy" || refusal.Exit != contract.ExitEnv {
		t.Fatalf("held open for good: %v", err)
	}
	var total time.Duration
	for i, d := range slept {
		if total += d; i > 0 && i < len(slept)-1 && d <= slept[i-1] {
			t.Errorf("pause %d did not grow: %v", i, slept)
		}
	}
	if total != 2*time.Second {
		t.Errorf("gave up after %v, not 2s", total)
	}

	calls = 0
	gone := &os.LinkError{Op: "rename", Err: syscall.Errno(2)}
	if err := on(onWindows, nil).replace("a", "b", func(string, string) error { calls++; return gone }, sleep); err != gone || calls != 1 {
		t.Errorf("another error was tried %d times: %v", calls, err)
	}
	calls = 0
	if err := on("linux", nil).replace("a", "b", busyFor(1), sleep); err != sharing || calls != 1 {
		t.Errorf("linux tried %d times: %v", calls, err)
	}
}

func TestReplace(t *testing.T) {
	dir := t.TempDir()
	tmp, final := filepath.Join(dir, "state.json.1"), filepath.Join(dir, "state.json")
	os.WriteFile(final, []byte("old"), 0o600)
	os.WriteFile(tmp, []byte("new"), 0o600)
	if err := New(runtime.GOOS).Replace(tmp, final); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(final); string(data) != "new" {
		t.Errorf("the file holds %q", data)
	}
	if _, err := os.Stat(tmp); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the temporary file is still there: %v", err)
	}
}

func TestPrivate(t *testing.T) {
	modes(t)
	s, dir := New(runtime.GOOS), t.TempDir()
	file := filepath.Join(dir, "token")
	os.WriteFile(file, nil, 0o666)
	os.Chmod(file, 0o666)
	os.Chmod(dir, 0o777)
	for path, want := range map[string]fs.FileMode{file: 0o600, dir: 0o700} {
		if open, err := s.WritableByOthers(path); err != nil || !open {
			t.Errorf("%s: writable by others %v, %v", path, open, err)
		}
		if err := s.Private(path); err != nil {
			t.Fatal(err)
		}
		if info, _ := os.Stat(path); info.Mode().Perm() != want {
			t.Errorf("%s is %v", path, info.Mode().Perm())
		}
		if open, err := s.WritableByOthers(path); err != nil || open {
			t.Errorf("%s once private: writable by others %v, %v", path, open, err)
		}
	}
	if err := s.Private(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing file: %v", err)
	}
}

func TestWritableByOthers(t *testing.T) {
	for _, c := range []struct {
		mode      fs.FileMode
		owner, me uint32
		want      bool
	}{
		{0o700, 501, 501, false},
		{0o755, 501, 501, false},
		{0o775, 501, 501, true},
		{0o757, 501, 501, true},
		{0o700, 0, 501, true},
		{fs.ModeDir | 0o1777, 501, 501, true},
	} {
		if got := othersCanWrite(c.mode, c.owner, c.me); got != c.want {
			t.Errorf("%v owned by %d, asked by %d: %v", c.mode, c.owner, c.me, got)
		}
	}
	if _, err := on(onWindows, nil).WritableByOthers(t.TempDir()); err != ErrCannotTell {
		t.Errorf("windows answered: %v", err)
	}
}

func TestPrivateTemp(t *testing.T) {
	modes(t)
	cache := t.TempDir()
	s := &System{OS: runtime.GOOS, Home: "/people/ada", Env: func(k string) string {
		return map[string]string{"XDG_CACHE_HOME": cache}[k]
	}}
	a, err := s.PrivateTemp()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.PrivateTemp()
	want := filepath.Join(cache, "whaleshark", "tmp")
	if a == b || filepath.Dir(a) != want || filepath.Dir(b) != want {
		t.Errorf("the folders are %s and %s", a, b)
	}
	for _, dir := range []string{a, want} {
		if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
			t.Errorf("%s is %v", dir, info.Mode().Perm())
		}
	}
}

func TestSelfPath(t *testing.T) {
	p, err := New(runtime.GOOS).SelfPath()
	if err != nil || !filepath.IsAbs(p) {
		t.Fatalf("%q, %v", p, err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Error(err)
	}
}
