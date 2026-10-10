package restore_test

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/layout"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/internal/restore"
	"github.com/tgdigital-hub/whaleshark/internal/screen"
)

// The test's own program plays three parts beside the test, by this
// variable: a stand-in keeper, a program in one of its panes, and a writer
// of the layout file that does nothing else.
const (
	envRole   = "RESTORE_TEST_ROLE"
	envReport = "RESTORE_TEST_REPORT"
	envGone   = "RESTORE_TEST_GONE"
	envLost   = "RESTORE_TEST_LOST"
)

func TestMain(m *testing.M) {
	switch os.Getenv(envRole) {
	case "keeper":
		standIn()
	case "program":
		program()
	case "writer":
		writer()
	default:
		os.Exit(m.Run())
	}
}

// program is what runs in a pane of the stand-in keeper: an agent when it
// is given a model, a pane of ours after "ui run", a command run once after
// "once", else a shell. It says how it was started, prints sixty lines and
// then one every few moments, and ends when its terminal does.
func program() {
	pane, args := os.Getenv(contract.EnvPane), os.Args[1:]
	// Printing into a terminal that has ended must not end the program
	// before it has said that it saw the end.
	signal.Ignore(syscall.SIGPIPE)
	say := func(format string, a ...any) { fmt.Printf(format+"\r\n", a...) }
	say("args: %s", strings.Join(args, " "))
	say("env: %s %s %s", os.Getenv("TEAM"), os.Getenv(contract.EnvTask), pane)
	switch i := slices.Index(args, "--resume"); {
	case i >= 0:
		say("resumed %s", args[i+1])
		say("session: %s", args[i+1])
	case slices.Contains(args, "--mute"):
	case slices.Contains(args, "--model"):
		say("session: sess-%s-%d", pane, os.Getpid())
	}
	for i := 1; i <= 60; i++ {
		say("%s line %d", pane, i)
	}
	say("ready")
	go func() {
		for i := 1; ; i++ {
			time.Sleep(20 * time.Millisecond)
			say("%s tick %d", pane, i)
		}
	}()
	io.Copy(io.Discard, os.Stdin)
	os.WriteFile(filepath.Join(os.Getenv(envGone), pane+"."+strconv.Itoa(os.Getpid())), nil, 0o600)
}

// pipes is a pane's terminal for the stand-in keeper: the program on two
// pipes, which every system has and which end with the keeper as a real
// terminal does.
type pipes struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out io.ReadCloser
}

func startPipes(spec contract.PtySpec) (contract.Pty, error) {
	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir, cmd.Env = spec.Dir, spec.Env
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &pipes{cmd, in, out}, nil
}

func (p *pipes) Read(b []byte) (int, error)  { return p.out.Read(b) }
func (p *pipes) Write(b []byte) (int, error) { return p.in.Write(b) }
func (p *pipes) Resize(int, int) error       { return nil }
func (p *pipes) Wait() (int, error)          { return 0, p.cmd.Wait() }
func (p *pipes) Front() (string, error)      { return "", contract.ErrCannotTell }
func (p *pipes) Close() error                { p.in.Close(); return p.cmd.Process.Kill() }

// held is one pane of the stand-in keeper, with the fields the real
// keeper's pane has and what the test wants to see of it.
type held struct {
	rec     contract.Pane
	argv    []string
	mu      sync.Mutex
	scr     *screen.Screen
	tty     contract.Pty
	started []string
	ready   bool
	count   atomic.Uint64
}

// keeper is the stand-in: the real keeper's state that a restart is about,
// held the same way, and the calls into this package the real one makes.
type keeper struct {
	kit      *contract.Kit
	dirs     contract.Dirs
	self     string
	instance string
	saver    *restore.Saver

	mu    sync.Mutex
	lay   layout.Layout
	tabs  map[string][]string
	panes map[string]*held
	nTab  int
	nPane int
}

// What the stand-in tells the test once every pane's program is ready.
type report struct {
	Instance    string
	NTab, NPane int
	Layout      json.RawMessage
	Vars        map[string][]string
	Panes       []seen
}

type seen struct {
	Rec           contract.Pane
	W, H          int
	Argv, Started []string
	Lines         []string
}

const keep = 2000

func standIn() {
	k := &keeper{kit: contract.NewKit(), tabs: map[string][]string{}, panes: map[string]*held{}}
	k.kit.Platform, k.kit.Pty = platform.New(runtime.GOOS), startPipes
	k.dirs, _ = k.kit.Platform.Dirs()
	k.self, _ = os.Executable()
	id := make([]byte, 6)
	rand.Read(id)
	k.instance = hex.EncodeToString(id)

	// From here to the end of the block is what the real keeper does when
	// it starts: read the file, take its state, start saving, and put a
	// program into every pane over its old lines.
	f, lay, err := restore.Load(k.kit.Platform, k.dirs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "stand-in keeper:", err)
	}
	if f != nil {
		k.lay, k.tabs, k.nTab, k.nPane = *lay, f.Vars, f.NTab, f.NPane
	}
	k.saver = restore.Start(k.kit.Platform, k.dirs, k.pack)
	if f != nil {
		k.mu.Lock()
		for i := range f.Panes {
			sp := &f.Panes[i]
			p := &held{rec: sp.Rec, argv: sp.Argv}
			k.panes[p.rec.ID] = p
			if err := k.start(p, sp.Run, sp.Lines, true); err != nil {
				sp.Shell()
				p.rec, p.argv = sp.Rec, sp.Argv
				k.start(p, nil, sp.Lines, true)
			}
		}
		k.mu.Unlock()
	} else {
		k.first()
	}
	k.saver.Changed()

	for all := false; !all; time.Sleep(10 * time.Millisecond) {
		k.mu.Lock()
		all = true
		for _, p := range k.panes {
			all = all && p.ready
		}
		k.mu.Unlock()
	}
	// The test kills this keeper as soon as it has the report, and a change
	// is on disk a fifth of a second after it at the latest, later on a busy
	// machine: so the report waits for a write made after every pane was
	// ready, and for every pane's lines written after it, which come a few
	// seconds after output.
	for ready, f, all := time.Now(), (restore.File{}), false; f.At.Before(ready) || !all; time.Sleep(10 * time.Millisecond) {
		k.saver.Changed()
		data, _ := os.ReadFile(restore.Path(k.dirs))
		json.Unmarshal(data, &f)
		all = true
		for id := range k.panes {
			info, err := os.Stat(restore.Path(k.dirs) + "." + id)
			all = all && err == nil && info.ModTime().After(ready)
		}
	}
	k.tell()
	// A change every few moments, so that the file is being written when
	// the keeper is killed.
	go func() {
		for {
			time.Sleep(3 * time.Millisecond)
			k.saver.Changed()
		}
	}()
	io.Copy(io.Discard, os.Stdin)
	// A stop on purpose: the last save first, while the panes are there.
	k.saver.Stop()
	for _, p := range k.panes {
		p.tty.Close()
	}
}

// pack is the file as it is now: the real keeper's will be these lines.
func (k *keeper) pack() *restore.File {
	k.mu.Lock()
	defer k.mu.Unlock()
	f := &restore.File{W: k.lay.W, H: k.lay.H, NTab: k.nTab, NPane: k.nPane, Vars: k.tabs}
	f.Layout, _ = json.Marshal(&k.lay)
	for _, p := range k.panes {
		f.Panes = append(f.Panes, restore.Pane{Rec: p.rec, Argv: p.argv, Count: p.count.Load(), Text: func() []string {
			p.mu.Lock()
			defer p.mu.Unlock()
			return restore.Text(p.scr, keep)
		}})
	}
	return f
}

// start puts a program into a pane, at the size of the pane's place: run,
// or with none the shell. A pane that came back shows its old lines first.
func (k *keeper) start(p *held, run, old []string, back bool) error {
	w, h := 80, 24
	for _, at := range k.lay.Places(k.lay.Of(p.rec.ID)) {
		if at.Pane == p.rec.ID && at.W > 0 && at.H > 0 {
			w, h = at.W, at.H
		}
	}
	if run == nil {
		run = []string{k.self}
	}
	env := append(os.Environ(), k.tabs[p.rec.Tab]...)
	env = append(env, envRole+"=program", contract.EnvPane+"="+p.rec.ID)
	tty, err := k.kit.Pty(contract.PtySpec{Argv: run, Dir: p.rec.Cwd, Env: env, Cols: w, Rows: h})
	if err != nil {
		return err
	}
	scr := screen.New(w, h)
	scr.Scrollback(5000)
	if back {
		restore.Show(scr, old, time.Now())
	}
	p.mu.Lock()
	p.tty, p.scr, p.started = tty, scr, run
	p.mu.Unlock()
	go k.read(p, tty)
	return nil
}

// read feeds a pane's screen, and hears the two things an agent's hook
// would tell the real keeper: its session, and that it is ready.
func (k *keeper) read(p *held, tty contract.Pty) {
	lines := bufio.NewReader(tty)
	for {
		line, err := lines.ReadString('\n')
		if err != nil {
			return
		}
		p.mu.Lock()
		p.scr.Write([]byte(line))
		p.mu.Unlock()
		p.count.Add(uint64(len(line))) // #nosec G115 -- a length
		k.saver.Flowed()
		line = strings.TrimSpace(line)
		if id, ok := strings.CutPrefix(line, "session: "); ok || line == "ready" {
			k.mu.Lock()
			if p.ready = p.ready || !ok; ok && p.rec.Agent != "" {
				p.rec.Session = id
			}
			k.mu.Unlock()
			k.saver.Changed()
		}
	}
}

// first is a keeper with nothing to read back: five tabs as an evening of
// work leaves them. Twelve agents in three tabs; the two panes of ours
// beside a shell; and four panes that cannot come back as they were: an
// agent of a kind that has no way to resume, one whose program will be
// gone, one that never told its session, and a command that was run once.
func (k *keeper) first() {
	k.mu.Lock()
	defer k.mu.Unlock()
	work := filepath.Dir(k.dirs.State)
	agent := func(model string, more ...string) []string {
		return append([]string{k.self, "--model", model, "--settings", filepath.Join(work, "hooks.json")}, more...)
	}
	tab := func(label string) (string, *held) {
		k.nTab++
		id := "t" + strconv.Itoa(k.nTab)
		k.tabs[id] = []string{"TEAM=reef-" + id, contract.EnvTask + "=T" + strconv.Itoa(k.nTab)}
		p := k.fresh(id, label)
		k.lay.Add(id, label, p.rec.ID)
		return id, p
	}
	split := func(of *held, dir string, ratio float64) *held {
		p := k.fresh(of.rec.Tab, of.rec.Label)
		if err := k.lay.Split(of.rec.ID, dir, ratio, p.rec.ID); err != nil {
			panic(err)
		}
		return p
	}
	k.lay.Fit(150, 48)

	_, shell := tab("lead")
	fleet := split(shell, contract.Right, 36)
	fleet.argv = []string{k.self, "ui", "run", "fleet", "--root", work}
	actions := split(shell, contract.Up, 8)
	actions.argv = []string{k.self, "ui", "run", "actions", "--root", work}

	for n, label := range []string{"sign-up page", "billing", "search"} {
		_, a := tab("draft")
		b := split(a, contract.Right, 0.5)
		c, d := split(a, contract.Down, 0.6), split(b, contract.Down, 0.3)
		for i, p := range []*held{a, b, c, d} {
			p.rec.Agent, p.rec.Name = "claude", fmt.Sprintf("%s-%d", strings.Fields(label)[0], i+1)
			p.argv = agent("m" + strconv.Itoa(n+1))
			p.rec.Label = label
		}
		k.lay.Of(a.rec.ID).Label = label
	}

	closed, _ := tab("closed again")
	k.lay.Remove(closed)
	delete(k.tabs, closed)
	delete(k.panes, "p"+strconv.Itoa(k.nPane))

	_, other := tab("odd ones")
	other.rec.Agent, other.rec.Name, other.argv = "other", "stranger", agent("m9")
	lost := split(other, contract.Right, 0.5)
	lost.rec.Agent, lost.rec.Name, lost.argv = "claude", "lost", agent("m9")
	lost.argv[0] = os.Getenv(envLost)
	mute := split(other, contract.Down, 0.5)
	mute.rec.Agent, mute.rec.Name, mute.argv = "claude", "mute", agent("m9", "--mute")
	once := split(lost, contract.Down, 0.5)
	once.argv = []string{k.self, "once", "accept", "T7"}

	k.lay.Focus("p7")
	k.lay.Show("t3")
	for _, p := range k.panes {
		if err := k.start(p, p.argv, nil, false); err != nil {
			panic(err)
		}
	}
}

func (k *keeper) fresh(tab, label string) *held {
	k.nPane++
	p := &held{rec: contract.Pane{ID: "p" + strconv.Itoa(k.nPane), Tab: tab, Label: label,
		Cwd: filepath.Dir(k.dirs.State), Status: contract.StatusUnknown}}
	k.panes[p.rec.ID] = p
	return p
}

// tell writes the report where the test looks for it, whole or not at all.
func (k *keeper) tell() {
	k.mu.Lock()
	r := report{Instance: k.instance, NTab: k.nTab, NPane: k.nPane, Vars: k.tabs}
	r.Layout, _ = json.Marshal(&k.lay)
	for _, t := range k.lay.Tabs {
		for _, at := range k.lay.Places(t) {
			p := k.panes[at.Pane]
			p.mu.Lock()
			r.Panes = append(r.Panes, seen{p.rec, at.W, at.H, p.argv, p.started, restore.Text(p.scr, keep)})
			p.mu.Unlock()
		}
	}
	k.mu.Unlock()
	slices.SortFunc(r.Panes, func(a, b seen) int { return strings.Compare(a.Rec.ID, b.Rec.ID) })
	data, _ := json.Marshal(r)
	path := os.Getenv(envReport)
	os.WriteFile(path+".part", data, 0o600)
	os.Rename(path+".part", path)
}

// writer writes a large layout file again and again until it is killed.
func writer() {
	p := platform.New(runtime.GOOS)
	dirs, _ := p.Dirs()
	f := bigFile()
	for {
		if err := restore.Save(p, dirs, f); err != nil {
			fmt.Fprintln(os.Stderr, "writer:", err)
			os.Exit(1)
		}
	}
}

// bigFile is a sound file of one tab and one pane with a megabyte of lines.
func bigFile() *restore.File {
	lay := layout.Layout{}
	lay.Add("t1", "one", "p1")
	f := &restore.File{NTab: 1, NPane: 1, Panes: []restore.Pane{{Rec: contract.Pane{ID: "p1"}}}}
	f.Layout, _ = json.Marshal(&lay)
	for i := range 10000 {
		f.Panes[0].Lines = append(f.Panes[0].Lines, strings.Repeat("reef ", 20)+strconv.Itoa(i))
	}
	return f
}
