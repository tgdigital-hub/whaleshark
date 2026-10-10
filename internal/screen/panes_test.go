package screen_test

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/panes"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/internal/screen"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/test/fakeherdr"
)

// glass is a pane's terminal: what the pane sends goes through the reader,
// and after every frame the reader's picture must be the grid the pane
// drew from. The terminal layer sends a frame in one write and holds its
// grid still meanwhile, so the two are compared at that very moment.
type glass struct {
	t      *testing.T
	mu     sync.Mutex
	s      *screen.Screen
	term   *term.Term
	frames int
	text   string
}

func (g *glass) Write(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.s.Write(p)
	pic := g.s.Picture()
	// Nothing to compare before the pane has its terminal, after it gave
	// the terminal back, or in the moment between a new size and its news.
	if g.text = pic.Text(); g.term == nil || !g.s.Modes().Second || g.term.W != pic.W || g.term.H != pic.H {
		return len(p), nil
	}
	g.frames++
	for i, c := range pic.Cells {
		if want := g.term.At(i%pic.W, i/pic.W); c != want {
			g.t.Errorf("frame %d, column %d of row %d: the reader has %+v, the pane drew %+v", g.frames, i%pic.W, i/pic.W, c, want)
			return len(p), nil
		}
	}
	return len(p), nil
}

// shows waits until the reader's picture holds the text.
func (g *glass) shows(text string) {
	g.t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		g.mu.Lock()
		ok := strings.Contains(g.text, text)
		g.mu.Unlock()
		if ok {
			return
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.t.Fatalf("the pane never showed %q; it shows:\n%s", text, g.text)
}

// record is the fixture's record, read as the panes read it.
type record struct {
	contract.NoStore
	dir   string
	state *contract.State
}

func (r record) Read(string, string) (*contract.State, error) { return r.state, nil }
func (r record) Current(string) (string, error)               { return "r3", nil }
func (r record) Dir(_, run string) string                     { return filepath.Join(r.dir, run) }

// Our own panes are the exact case: the fleet, the action pane and the list
// of actions draw the evening from a grid of their own, and the picture
// the reader makes of what they send equals that grid cell for cell, at
// every size, through keys, a fold and a new size.
func TestOurPanesCellForCell(t *testing.T) {
	fx, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"fleet", "actions", "menu"} {
		for _, size := range [][2]int{{36, 30}, {80, 24}, {120, 40}, {50, 15}, {24, 8}} {
			t.Run(fmt.Sprintf("%s-%dx%d", kind, size[0], size[1]), func(t *testing.T) {
				t.Parallel()
				home := t.TempDir()
				sys := platform.New(runtime.GOOS)
				sys.Home, sys.Env = home, func(name string) string {
					switch name {
					case "APPDATA":
						return filepath.Join(home, "roaming")
					case "LOCALAPPDATA":
						return filepath.Join(home, "local")
					}
					return ""
				}
				k := contract.NewKit()
				k.Platform, k.Store = sys, record{dir: home, state: &fx.State}
				k.View = func(in contract.ViewInput) *contract.View {
					v := *fx.Views[contract.Human]
					v.Fresh = contract.Fresh{Checked: in.Checked, Notes: in.Notes}
					return &v
				}
				if err := os.MkdirAll(filepath.Join(home, "r3"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(home, "r3", "state.json"), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
				herdr, err := fakeherdr.New(k)
				if err != nil {
					t.Fatal(err)
				}
				defer herdr.Close()
				herdr.Load(fx.Terms)
				k.Terms = herdr

				w, h := size[0], size[1]
				g := &glass{t: t, s: screen.New(w, h)}
				keys, in := io.Pipe()
				tm := term.New(keys, g, w, h, term.FullColour)
				g.mu.Lock()
				g.term = tm
				g.mu.Unlock()
				done := make(chan bool)
				go func() {
					panes.Run(kind, tm, k, home, "")
					close(done)
				}()
				first, typed := "live", []string{"\x1b[B", "\x1b[B", "?", "\x1b", "x", "x", "\x1b[A"}
				if kind == "menu" { // Escape would end it; what is typed there is a search
					first, typed = "every action", []string{"\x1b[B", "\x1b[B", "s", "t", "\x7f", "\x1b[A"}
				}
				g.shows(first)
				for _, key := range typed {
					io.WriteString(in, key)
					time.Sleep(20 * time.Millisecond)
				}
				g.mu.Lock()
				g.s.Resize(w+7, h+3)
				g.mu.Unlock()
				tm.Post(term.Event{Kind: term.Resize, W: w + 7, H: h + 3})
				time.Sleep(50 * time.Millisecond)
				io.WriteString(in, "\x1b[B")
				time.Sleep(50 * time.Millisecond)
				in.Close()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Fatal("the pane did not end with its input")
				}
				tm.Close()
				g.mu.Lock()
				defer g.mu.Unlock()
				if g.frames < 3 {
					t.Errorf("%d frames compared at %dx%d", g.frames, w, h)
				}
				if g.s.Modes() != (screen.Modes{WheelKeys: true}) {
					t.Errorf("the pane left the modes %+v behind", g.s.Modes())
				}
			})
		}
	}
}
