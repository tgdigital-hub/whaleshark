package keeper

import (
	"cmp"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/input"
	"github.com/tgdigital-hub/whaleshark/internal/pick"
	"github.com/tgdigital-hub/whaleshark/internal/restore"
	"github.com/tgdigital-hub/whaleshark/internal/screen"
	"github.com/tgdigital-hub/whaleshark/internal/watch"
)

const (
	// The size of a pane no window has given a place yet.
	firstW, firstH = 80, 24
	// Once a pane's program has ended, what it left running keeps the
	// terminal until nothing has been printed for this long.
	linger = time.Second
	// The screen reader lets a held picture go after a second; the pane
	// is drawn once more just after.
	heldFor = 1100 * time.Millisecond
)

// notPassed are the variables of the keeper's own start that say something
// about a terminal or a pane it was started in, which is no pane of its own.
var notPassed = []string{"TERM_PROGRAM", "TERM_PROGRAM_VERSION", "NO_COLOR", "COLUMNS", "LINES", contract.EnvTermActivePane}

// pane is one pane. rec, argv and tty change under the keeper's lock; tty,
// scr, the size and the person's view also under mu, which is all the
// pane's reader takes.
type pane struct {
	rec    contract.Pane
	argv   []string       // the program put there, an agent's with its kind first; none is the shell
	from   *restore.Pane  // what the layout file held of it, until it has started again
	watch  *watch.Watcher // follows the program in there now
	hooked bool           // that program's own hooks have been heard
	wish   string         // what it asked to copy, while the person is asked

	mu   sync.Mutex
	tty  contract.Pty
	scr  *screen.Screen
	pick pick.View
	w, h int
	back int // the lines of scroll-back the screen keeps

	count atomic.Uint64 // bytes read from the terminal in there now
	shown atomic.Bool   // in the tab that shows
}

// fresh is a new pane of a tab, with no program in it yet.
func (k *Keeper) fresh(tab, label, cwd string) *pane {
	k.nPane++
	p := &pane{rec: contract.Pane{ID: "p" + strconv.Itoa(k.nPane), Tab: tab, Label: label, Cwd: cwd, Status: contract.StatusUnknown}}
	k.panes[p.rec.ID] = p
	return p
}

// forget takes back the pane fresh made last, which never started: its
// number is free again.
func (k *Keeper) forget(p *pane) {
	delete(k.panes, p.rec.ID)
	k.nPane--
}

// settings are the person's own, or what holds when the file says nothing.
func (k *Keeper) settings() contract.Settings {
	s, _ := contract.ReadSettings(k.kit.Platform.Peek, k.dirs)
	return s
}

// shell is what a new pane starts: the person's setting, else the login's
// own shell.
func (k *Keeper) shell(s contract.Settings) []string {
	if len(s.Shell) > 0 {
		return s.Shell
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		return []string{sh}
	}
	if k.kit.Platform.System() == "windows" {
		return []string{"powershell.exe"}
	}
	return []string{"/bin/sh"}
}

// env is what a pane's program is started with: what the keeper itself was
// started with, the tab's own variables over that, and the keeper's three
// last. Nothing of a window is in it.
func (k *Keeper) env(p *pane) []string {
	set := append(slices.Clone(k.tabs[p.rec.Tab]), "TERM=xterm-256color", "COLORTERM=truecolor", contract.EnvTermPane+"="+p.rec.ID)
	if p == k.over {
		// A program drawn over the tab acts on the pane the person was in.
		set = append(set, contract.EnvTermActivePane+"="+k.focus)
	}
	name := func(v string) string { n, _, _ := strings.Cut(v, "="); return n }
	var env []string
	for _, v := range os.Environ() {
		if n := name(v); !slices.Contains(notPassed, n) && !slices.ContainsFunc(set, func(s string) bool { return name(s) == n }) {
			env = append(env, v)
		}
	}
	return append(env, set...)
}

// start puts a program into a pane, at the size of the pane's place: its
// own, or the shell. A pane that came back from the layout file starts what
// the file says, an agent with its option to resume, over its old lines.
// Whatever ran there before is the caller's to end.
func (k *Keeper) start(p *pane) error {
	w, h := firstW, firstH
	if at := k.overAt(); p == k.over && at.W > 0 && at.H > 0 {
		w, h = at.W, at.H
	} else if t := k.lay.Of(p.rec.ID); t != nil {
		for _, at := range k.lay.Places(t) {
			if at.Pane == p.rec.ID && at.W > 0 && at.H > 0 {
				w, h = at.W, at.H
			}
		}
	}
	argv := p.argv
	if p.from != nil && p.from.Run != nil {
		argv = p.from.Run
	}
	if argv == nil {
		argv = k.shell(k.set)
	}
	tty, err := k.kit.Pty(contract.PtySpec{Argv: argv, Dir: p.rec.Cwd, Env: k.env(p), Cols: w, Rows: h})
	if err != nil {
		return err
	}
	k.nTerm++
	p.rec.Terminal, p.rec.Status = k.instance+"."+strconv.Itoa(k.nTerm), contract.StatusUnknown
	// A pane program of ours is known by how the tool starts one.
	p.rec.Ours = p.rec.Agent == "" && len(argv) > 3 && argv[1] == "ui" && argv[2] == "run"
	scr := screen.New(w, h)
	scr.Scrollback(k.set.Scrollback.Lines)
	if p.from != nil {
		restore.Show(scr, p.from.Lines, time.Now())
	}
	p.quit()
	p.watch = watch.New(eye{p, tty}, p.rec.Agent, p.rec.Name, func(s watch.State) { k.state(p, tty, s) })
	p.mu.Lock()
	p.tty, p.scr, p.w, p.h, p.back, p.pick = tty, scr, w, h, k.set.Scrollback.Lines, pick.View{}
	p.count.Store(0)
	p.mu.Unlock()
	p.from, p.hooked, p.wish = nil, false, ""
	go k.read(p, tty)
	return nil
}

// eye is a pane as the Watcher of one program in it sees it.
type eye struct {
	p   *pane
	tty contract.Pty
}

func (e eye) Text() string           { return e.p.text() }
func (e eye) Count() uint64          { return e.p.count.Load() }
func (e eye) Front() (string, error) { return e.tty.Front() }
func (e eye) Type(b []byte) error    { _, err := e.tty.Write(b); return err }
func (e eye) Paste() bool {
	e.p.mu.Lock()
	defer e.p.mu.Unlock()
	return e.p.scr.Modes().Paste
}

// state takes what a pane's Watcher has worked out into the pane's record
// and tells the readers of events. A session is the hooks' word alone:
// until one has been heard, an agent that came back keeps the one it had.
func (k *Keeper) state(p *pane, tty contract.Pty, s watch.State) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.panes[p.rec.ID] != p || p.tty != tty {
		return
	}
	was := p.rec
	p.rec.Agent, p.rec.Name, p.rec.Status, p.rec.Cwd = s.Agent, s.Name, s.Status, cmp.Or(s.Cwd, p.rec.Cwd)
	if p.hooked {
		p.rec.Session = s.Session
	}
	kind := contract.EvFolder
	switch now := p.rec; {
	case now == was:
		return
	case now.Status != was.Status || now.Agent != was.Agent || now.Name != was.Name:
		kind = contract.EvState
	case now.Session != was.Session:
		kind = contract.EvSession
	}
	k.emit(kind, p.rec)
	k.saved()
}

// quit ends the following of a pane's program.
func (p *pane) quit() {
	if p.watch != nil {
		p.watch.Close()
	}
}

// saved tells the saver of a change the layout file holds.
func (k *Keeper) saved() {
	if k.saver != nil {
		k.saver.Changed()
	}
}

// told writes a pane's program that the keys came or went, if it asked.
func (p *pane) told(on bool) {
	p.mu.Lock()
	text, tty := input.Focus(on, p.scr.Modes()), p.tty
	p.mu.Unlock()
	if text != "" {
		go io.WriteString(tty, text)
	}
}

// read feeds everything a pane's terminal gives to its screen, until the
// terminal ends; then the pane goes, unless another program is in it by then.
func (k *Keeper) read(p *pane, tty contract.Pty) {
	go func() {
		tty.Wait()
		for n := p.count.Load(); ; n = p.count.Load() {
			if time.Sleep(linger); p.count.Load() == n {
				break
			}
		}
		tty.Close()
	}()
	// A program inside a frame it asked to have shown in one go holds its
	// picture; one that never ends the frame is drawn all the same, a
	// little after the screen reader has let the picture go.
	again := time.AfterFunc(time.Hour, func() {
		k.mu.Lock()
		k.wake()
		k.mu.Unlock()
	})
	defer again.Stop()
	buf := make([]byte, 32<<10)
	for {
		n, err := tty.Read(buf)
		if n > 0 {
			reply, wish, bell, held, ok := p.feed(tty, buf[:n])
			if len(reply) > 0 {
				tty.Write(reply)
			}
			if held {
				again.Reset(heldFor)
			}
			if k.saver != nil {
				k.saver.Flowed()
			}
			if ok && (bell || p.shown.Load()) {
				k.mu.Lock()
				for _, w := range k.wins {
					if bell {
						w.later([]byte{'\a'})
					}
				}
				k.wake()
				k.mu.Unlock()
			}
			if ok && wish != "" {
				k.wished(p, wish)
			}
		}
		if err != nil {
			break
		}
	}
	tty.Close()
	k.mu.Lock()
	if k.panes[p.rec.ID] == p && p.tty == tty {
		k.drop(p)
		k.changed()
	}
	k.mu.Unlock()
}

// feed hands the screen what was read and reports whether the terminal is
// still the pane's, with what the screen has for the caller: its answers to
// the program's questions, which go to this pane's program and to no other,
// what the program wants on the clipboard, whether the bell rang, and
// whether the picture is held. The screen reader does not catch its own
// faults: one costs this pane its picture, and nothing else.
func (p *pane) feed(tty contract.Pty, b []byte) (reply []byte, wish string, bell, held, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tty != tty {
		return nil, "", false, false, false
	}
	defer func() {
		if recover() != nil {
			p.scr = screen.New(p.w, p.h)
			p.scr.Scrollback(p.back)
			p.scr.Write([]byte("whaleshark: this pane's picture was lost to a fault and starts again here\r\n"))
			reply, wish, bell, held, ok = nil, "", false, false, true
		}
	}()
	p.count.Add(uint64(len(b))) // #nosec G115 -- a length
	p.scr.Write(b)
	for _, ev := range p.scr.Events() {
		if bell = bell || ev.Kind == screen.Bell; ev.Kind == screen.Copy {
			wish = ev.Text
		}
	}
	return p.scr.Reply(), wish, bell, p.scr.Held(), true
}

// wished is a program's own request to copy, by the person's setting: done
// at once, dropped, or kept while the person is asked. The answer is the
// command key and then y, in that pane.
func (k *Keeper) wished(p *pane, text string) {
	k.mu.Lock()
	now, ask := pick.Wish(k.set.Clipboard.Programs, cmp.Or(p.rec.Name, p.rec.Label, p.rec.ID), text)
	w := k.last
	if ask != "" && w != nil {
		p.wish = text
		k.popup(ask, "the command key, then y, in its pane")
	}
	k.mu.Unlock()
	if now && w != nil {
		k.copy(w, text)
	}
}

// copy puts text on the clipboard of a window's person and shows how it
// went. It runs a program or waits for one: never under a lock.
func (k *Keeper) copy(w *window, text string) {
	mark, seq := w.board.Copy(text)
	if len(seq) > 0 {
		w.later(seq)
	}
	if mark != "" {
		k.mu.Lock()
		k.popup(mark, "")
		k.mu.Unlock()
	}
}

// size gives a pane a new size and tells its program.
func (p *pane) size(w, h int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if w != p.w || h != p.h {
		p.w, p.h = w, h
		p.scr.Resize(w, h)
		p.tty.Resize(w, h)
	}
}

// text is what a pane shows, as lines.
func (p *pane) text() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scr.Picture().Text()
}

// drop takes a pane out of the layout and tells the readers of events. Its
// terminal is the caller's to close, outside the lock.
func (k *Keeper) drop(p *pane) {
	delete(k.panes, p.rec.ID)
	p.quit()
	if p == k.over {
		k.over = nil
	} else if k.lay.Close(p.rec.ID) {
		delete(k.tabs, p.rec.Tab)
	}
	k.emit(contract.EvClosed, p.rec)
}
