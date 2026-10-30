// Package rehearse walks the road of a plain user on a Linux server from end
// to end, with the program as install.sh puts it in place: the installer, the
// doctor, the keeper as a user service, and one job of two pull requests and
// a look at the code, on the real keeper. README.md says what it covers.
package rehearse

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/install"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/restore"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func TestMain(m *testing.M) { scenario.Main(m) }

// patience is how long a thing is given to come about; the machines this
// runs on are shared, and a job plays three agents on them.
const patience = time.Minute

// standInGH keeps its pull requests as files in one folder, as the stand-in
// of internal/integrate's tests does, and merges for real: the squash lands
// on the bare remote, so that the next pull request's base has moved. It also
// keeps the merge lock's file as it was at each merge.
const standInGH = `#!/bin/sh
d='{gh}'
remote='{remote}'
root='{root}'
echo "$*" >> "$d/calls"
case "$1 $2" in
"pr view")
	if [ "$5" = state,mergeCommit ]; then
		if [ -f "$d/merged-$3" ]; then echo "MERGED $(cat "$d/merged-$3")"; else echo "OPEN null"; fi
		exit 0
	fi
	n=$(cat "$d/branch-$(echo "$3" | tr / _)" 2>/dev/null) || exit 1
	state=OPEN; [ -f "$d/merged-$n" ] && state=MERGED
	echo "$n https://example.test/pull/$n $state" ;;
"pr create")
	n=$(( $(cat "$d/count" 2>/dev/null || echo 0) + 1 )); echo $n > "$d/count"
	head=; prev=
	for a in "$@"; do [ "$prev" = --head ] && head=$a; prev=$a; done
	echo $n > "$d/branch-$(echo "$head" | tr / _)"
	echo "$head" > "$d/head-$n"
	echo "$*" > "$d/args-$n"
	cat > "$d/body-$n"
	echo "https://example.test/pull/$n" ;;
"pr checks")
	if [ -f "$d/green" ]; then echo '[{"name":"ci","bucket":"pass"}]'; else echo '[{"name":"ci","bucket":"pending"}]'; fi ;;
"pr merge")
	cp "$root/.whaleshark/locks/merge.json" "$d/lock-$3"
	work="$d/clone-$3"
	git clone -q "$remote" "$work" || exit 1
	git -C "$work" merge -q --squash "origin/$(cat "$d/head-$3")" || exit 1
	git -C "$work" commit -q -m "pull request $3" || exit 1
	git -C "$work" push -q origin main || exit 1
	git -C "$work" rev-parse HEAD > "$d/merged-$3"
	rm -rf "$work"
	echo "$3" >> "$d/order" ;;
*) exit 1 ;;
esac
`

// installed builds the program as a release does, signs it with a key of the
// test's own and has the real install.sh put it into home/.local/bin. What
// the installer checks is checked, and it is the path this test goes on with.
func installed(t *testing.T, home string) (bin string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("NOT PROVEN HERE: the road of a plain user on a server is one of sh and of Linux")
	}
	module, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	dir, keys := t.TempDir(), t.TempDir()
	file := "whaleshark-" + runtime.GOOS + "-" + runtime.GOARCH
	build := exec.Command("go", "build", "-o", filepath.Join(dir, file), "./cmd/whaleshark")
	build.Dir, build.Env = strings.TrimSpace(string(module)), append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the program: %v\n%s", err, out)
	}
	program, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		t.Fatal(err)
	}
	list := filepath.Join(dir, "SHA256SUMS")
	write(t, list, fmt.Sprintf("%x  %s\n", sha256.Sum256(program), file))
	key := filepath.Join(keys, "release-key")
	run(t, "", nil, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "release", "-f", key)
	run(t, "", nil, "ssh-keygen", "-Y", "sign", "-f", key, "-n", "file", list)
	public, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	signer := `release namespaces="file" ` + strings.Join(strings.Fields(string(public))[:2], " ")
	script := filepath.Join(keys, "install.sh")
	write(t, script, strings.Replace(install.Installer, "\nSIGNER=''\n", "\nSIGNER='"+signer+"'\n", 1))
	out := run(t, "", []string{"PATH=/usr/bin:/bin", "HOME=" + home, "SHELL=/bin/sh"}, "sh", script, "--from", dir)
	bin = filepath.Join(home, ".local", "bin", "whaleshark")
	if info, err := os.Stat(bin); err != nil || info.Mode().Perm() != 0o755 || !strings.Contains(out, "installed "+bin) {
		t.Fatalf("the program after the installer: %v, %v\n%s", info, err, out)
	}
	if profile, err := os.ReadFile(filepath.Join(home, ".profile")); err != nil || !strings.Contains(string(profile), ".local/bin") {
		t.Errorf("the login's start file does not find the program: %v\n%s", err, profile)
	}
	return bin
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o700); err != nil {
		t.Fatal(err)
	}
}

// run runs a program that must end well and returns all it said. env nil is
// the test's own surroundings.
func run(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env = dir, env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// soon waits until a thing is so, and says what it is instead when it never is.
func soon(t *testing.T, what string, so func() (bool, string)) {
	t.Helper()
	for end := time.Now().Add(patience); ; time.Sleep(50 * time.Millisecond) {
		ok, said := so()
		if ok {
			return
		}
		if time.Now().After(end) {
			t.Fatalf("%s did not come about in %v: %s", what, patience, said)
		}
	}
}

// login is the surroundings of a person logged in to home: the test's own
// without its pretended folders.
func login(home string) []string {
	env := []string{"HOME=" + home}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains([]string{"HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"}, name) {
			env = append(env, kv)
		}
	}
	return env
}

// The installer, the doctor, and the keeper as a user service. Only where
// REHEARSE_SERVICE is set (the gate's job does, with a login made for it and
// lingering on) are the real home and the real systemd --user touched: with
// nobody logged in, the service runs, the doctor says so, a killed keeper
// comes back. Anywhere else the unit is only read, and the test says so.
func TestInstallDoctorAndService(t *testing.T) {
	if os.Getenv("REHEARSE_SERVICE") == "" {
		home := t.TempDir()
		bin := installed(t, home)
		env := login(home)
		cmd := exec.Command(bin, "doctor")
		cmd.Dir, cmd.Env = home, env
		out, _ := cmd.CombinedOutput()
		t.Logf("the doctor says:\n%s", out)
		if !strings.Contains(string(out), "whaleshark") {
			t.Errorf("the doctor says:\n%s", out)
		}
		_, unit := restore.Service("linux", bin, func(string) string { return "" })
		for _, want := range []string{`ExecStart="` + bin + `" engine run`, "Restart=on-failure", "WantedBy=default.target"} {
			if !strings.Contains(unit, want) {
				t.Errorf("the unit lacks %q:\n%s", want, unit)
			}
		}
		t.Log("NOT PROVEN HERE: the unit was read and not run; the real systemd --user is played where REHEARSE_SERVICE is set")
		return
	}

	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if me.Uid == "0" {
		t.Fatal("this rehearses a plain user, and runs as root")
	}
	bin := installed(t, me.HomeDir)
	env := login(me.HomeDir)
	project, err := os.MkdirTemp(me.HomeDir, "project")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(project) })
	services := func(args ...string) string {
		cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
		cmd.Env = env
		out, _ := cmd.CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	unit := contract.UnitKeeper
	t.Cleanup(func() { services("disable", "--now", unit) })

	out := run(t, project, env, bin, "init", "--at-login")
	if !strings.Contains(out, "lingering is on") {
		t.Errorf("init --at-login says:\n%s", out)
	}
	if got := services("is-enabled", unit); got != "enabled" {
		t.Errorf("the service is %q, not enabled", got)
	}
	soon(t, "the service running", func() (bool, string) { s := services("is-active", unit); return s == "active", s })
	doctor := func() string {
		cmd := exec.Command(bin, "doctor")
		cmd.Dir, cmd.Env = project, env
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	said := doctor()
	for _, want := range []string{"keeper runs as a user service: yes", "keeper survives logout (linger): yes"} {
		if !strings.Contains(said, want) {
			t.Errorf("the doctor does not say %q:\n%s", want, said)
		}
	}

	// Killed, it comes back by itself, as another process.
	mainPID := func() int {
		n, _ := strconv.Atoi(services("show", "-p", "MainPID", "--value", unit))
		return n
	}
	first := mainPID()
	if first == 0 {
		t.Fatal("the service has no main process")
	}
	if err := syscall.Kill(first, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	soon(t, "the keeper back after it was killed", func() (bool, string) {
		s := services("is-active", unit)
		return s == "active" && mainPID() != 0 && mainPID() != first, s
	})
	soon(t, "the doctor seeing it back", func() (bool, string) {
		d := doctor()
		return strings.Contains(d, "keeper runs as a user service: yes"), d
	})

	out = run(t, project, env, bin, "init", "--remove", "--all")
	if !strings.Contains(out, "no longer one of your services") {
		t.Errorf("init --remove --all says:\n%s", out)
	}
	if got := services("is-enabled", unit); got == "enabled" {
		t.Error("the service is still enabled after --remove --all")
	}
}

// A job on a stale clone, in pr-per-task mode, on the real keeper and with
// the program as installed. The project's main is behind its remote. Two code
// tasks start at the remote's tip and each becomes its own pull request; one
// that only reads gets no copy, no setup and no ports. The merges go one at a
// time under the merge lock, and each gives up its tab.
func TestAJobOfPullRequests(t *testing.T) {
	bin := installed(t, t.TempDir())
	for _, pair := range testkit.GitEnv()[len(os.Environ()):] {
		name, value, _ := strings.Cut(pair, "=")
		t.Setenv(name, value)
	}
	gh, tools, remote, other, logs := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+tools+string(os.PathListSeparator)+os.Getenv("PATH"))

	p := scenario.PrepareOn(t, scenario.Keeper, nil, map[string]string{
		"A.1": `edit a.txt | commit | write-result | report done "A is in"`,
		"B.1": `edit b.txt | commit | write-result | report done "B is in"`,
		"R.1": `write-result | report done "read"`,
	})
	write(t, filepath.Join(tools, "gh"), strings.NewReplacer("{gh}", gh, "{remote}", remote, "{root}", p.Root).Replace(standInGH))
	said := func(file string) string {
		data, _ := os.ReadFile(filepath.Join(gh, file))
		return strings.TrimSpace(string(data))
	}

	// The remote and a clone of somebody else's, which moves it on.
	testkit.Git(t, remote, "init", "-q", "--bare", "-b", "main")
	testkit.Git(t, p.Root, "init", "-q", "-b", "main")
	brief := "# Target\nt\n# Change\nc\n# Constraints\nc\n# Ownership\no\n# Acceptance\na\n"
	stale := testkit.Commit(t, p.Root, "start", map[string]string{"b.md": brief, ".gitignore": "whaleshark.toml\n"})
	testkit.Git(t, p.Root, "remote", "add", "origin", remote)
	testkit.Git(t, p.Root, "push", "-q", "origin", "main")
	testkit.Git(t, other, "clone", "-q", remote, ".")
	tip := testkit.Commit(t, other, "elsewhere", map[string]string{"c.txt": "new\n"})
	testkit.Git(t, other, "push", "-q", "origin", "main")

	setup := filepath.Join(logs, "setup")
	write(t, filepath.Join(p.Root, "whaleshark.toml"), `[worktrees]
fetch_base = true
[land]
mode = "pr-per-task"
[tasks]
close_on_accept = true
[setup]
script = "pwd >> '`+setup+`'"
`)
	trust := p.Command(scenario.Human, "trust")
	trust.Stdin = strings.NewReader("yes\n")
	if out, err := trust.CombinedOutput(); err != nil {
		t.Fatalf("trust: %v\n%s", err, out)
	}

	orch := func(exit int, args ...string) string {
		t.Helper()
		out, err := p.Command(scenario.Orch, args...).CombinedOutput()
		if code := 0; err != nil {
			if e, ok := err.(*exec.ExitError); !ok {
				t.Fatalf("%v: %v", args, err)
			} else if code = e.ExitCode(); code != exit {
				t.Fatalf("%v: exit %d, expected %d\n%s", args, code, exit, out)
			}
		} else if exit != 0 {
			t.Fatalf("%v: exit 0, expected %d\n%s", args, exit, out)
		}
		return string(out)
	}
	orch(0, "init")
	orch(0, "run", "new", "a job on a stale clone")
	orch(0, "task", "add", "A", "the A part", "--brief", "b.md", "--check", "test -f a.txt", "--owns", "a.txt")
	orch(0, "task", "add", "B", "the B part", "--brief", "b.md", "--check", "test -f b.txt", "--owns", "b.txt")
	orch(0, "task", "add", "R", "a look", "--brief", "b.md", "--kind", "readonly")
	orch(0, "start", "A", "B", "R")
	soon(t, "three reports", func() (bool, string) {
		s, _ := p.Record()
		a, b, r := s.Attempts["A.1"], s.Attempts["B.1"], s.Attempts["R.1"]
		ok := a != nil && b != nil && r != nil && a.State == contract.AttemptReported && b.State == contract.AttemptReported && r.State == contract.AttemptReported
		return ok, fmt.Sprintf("%+v %+v %+v", a, b, r)
	})

	s, _ := p.Record()
	// The copies of the code begin at the remote's tip, with the worker's one
	// commit on top; the person's own checkout stays where it was.
	for _, id := range []string{"A", "B"} {
		w := s.Tasks[id].Worktree
		if w == nil || testkit.Git(t, w.Path, "rev-parse", "HEAD~1") != tip {
			t.Fatalf("the copy of %s is not cut from the remote's tip %s: %+v", id, tip, w)
		}
	}
	if testkit.Git(t, p.Root, "rev-parse", "main") != stale {
		t.Error("the person's own main was moved")
	}
	// The one that only reads has no copy, no setup and no ports.
	if s.Tasks["R"].Worktree != nil {
		t.Errorf("a readonly task has the copy %+v", s.Tasks["R"].Worktree)
	}
	dirs, err := p.Kit.Platform.Dirs()
	if err != nil {
		t.Fatal(err)
	}
	slots, err := contract.ReadSlots(os.ReadFile, dirs.State)
	if err != nil || len(slots) != 2 || slices.ContainsFunc(slots, func(s contract.Slot) bool { return s.Task == "R" }) {
		t.Errorf("the ports taken: %+v, %v", slots, err)
	}
	ran, _ := os.ReadFile(setup)
	if lines := strings.Fields(string(ran)); len(lines) != 2 || slices.Contains(lines, p.Root) {
		t.Errorf("the setup ran in:\n%s", ran)
	}

	// A lock taken twice: the second in line waits, and comes next.
	if out := orch(0, "lock", "take", "deploy", "--task", "A"); !strings.Contains(out, "You hold the lock deploy") {
		t.Errorf("the first taker of a lock is told:\n%s", out)
	}
	if out := orch(5, "lock", "take", "deploy", "--task", "B"); !strings.Contains(out, "number 1 in line") {
		t.Errorf("the second taker of a lock is told:\n%s", out)
	}
	orch(0, "lock", "release", "deploy", "--task", "A")
	orch(0, "lock", "take", "deploy", "--task", "B")
	orch(0, "lock", "release", "deploy", "--task", "B")

	// Accepted, the two are one pull request each, onto the plain branch name.
	orch(0, "accept", "R")
	if out := orch(0, "accept", "A"); !strings.Contains(out, "https://example.test/pull/1") {
		t.Errorf("accept A says:\n%s", out)
	}
	if out := orch(0, "accept", "B"); !strings.Contains(out, "https://example.test/pull/2") {
		t.Errorf("accept B says:\n%s", out)
	}
	for n, id := range map[string]string{"1": "A", "2": "B"} {
		if args := said("args-" + n); !strings.Contains(args, "--base main --head whaleshark/r1-"+id) {
			t.Errorf("gh was asked for %s: %s", id, args)
		}
	}
	// A look, past the watch's spacing: status sweeps each time, where a wait
	// with events to hand out hands them out first.
	look := func() {
		t.Helper()
		p.Clock(31 * time.Second)
		orch(0, "status")
	}
	look()
	if said("order") != "" {
		t.Fatalf("merged while the checks ran: %s", said("order"))
	}

	// All green, while somebody holds the merge lock: nothing merges, and
	// both stand in its line.
	orch(0, "lock", "take", "merge", "--task", "R")
	write(t, filepath.Join(gh, "green"), "")
	look()
	merge := func() contract.Lock {
		for _, l := range contract.ReadLocks(p.Kit.Platform.Peek, p.Root) {
			if l.Name == "merge" {
				return l
			}
		}
		return contract.Lock{}
	}
	if l := merge(); said("order") != "" || l.Holder != "r1/R" || len(l.Waiting) != 2 {
		t.Fatalf("with the merge lock held, the merges are %q and the lock %+v; gh was asked:\n%s", said("order"), l, said("calls"))
	}
	orch(0, "lock", "release", "merge", "--task", "R")

	// Let go, they merge one after the other, each holding it.
	soon(t, "both merged", func() (bool, string) {
		look()
		return len(strings.Fields(said("order"))) == 2, said("order")
	})
	for n, id := range map[string]string{"1": "A", "2": "B"} {
		if held := said("lock-" + n); !strings.Contains(held, `"holder": "r1/`+id+`"`) && !strings.Contains(held, `"holder":"r1/`+id+`"`) {
			t.Errorf("pull request %s was merged under the lock %s", n, held)
		}
	}
	if l := merge(); l.Holder != "" {
		t.Errorf("the merge lock is still held: %+v", l)
	}
	for _, file := range []string{"a.txt", "b.txt", "c.txt"} {
		testkit.Git(t, remote, "cat-file", "-e", "main:"+file)
	}

	// close_on_accept: every tab of the job is closed, and the copies are gone.
	soon(t, "the tabs closed and the copies gone", func() (bool, string) {
		look()
		s, _ := p.Record()
		snap, err := p.Terms().Snapshot(context.Background())
		if err != nil {
			return false, err.Error()
		}
		var left []string
		for _, id := range []string{"A.1", "B.1", "R.1"} {
			if slices.ContainsFunc(snap.Panes, func(pane contract.Pane) bool { return pane.ID == s.Attempts[id].Place.Pane }) {
				left = append(left, id)
			}
		}
		for _, id := range []string{"A", "B"} {
			if _, err := os.Stat(s.Tasks[id].Worktree.Path); err == nil {
				left = append(left, "the copy of "+id)
			}
		}
		return len(left) == 0, strings.Join(left, ", ")
	})
	s, _ = p.Record()
	for _, id := range []string{"A", "B", "R"} {
		if s.Tasks[id].Status != contract.TaskDone {
			t.Errorf("%s is %s", id, s.Tasks[id].Status)
		}
	}
}
