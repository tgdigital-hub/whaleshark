package view

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
)

// The stand-ins of a test: a record that is only read, the terminals' picture, a
// sweep that is counted, and a login folder of the test's own.
type record struct {
	contract.NoStore
	s     *contract.State
	reads *int
}

func (r record) Read(_, run string) (*contract.State, error) {
	if r.reads != nil {
		*r.reads++
	}
	if r.s == nil || run != r.s.Run.ID {
		return nil, contract.ErrNoRun
	}
	return r.s, nil
}

type picture struct {
	contract.NoTerminals
	snap *contract.Snapshot
}

func (p picture) Snapshot(context.Context) (*contract.Snapshot, error) {
	if p.snap == nil {
		return nil, contract.ErrEngineUnreachable
	}
	return p.snap, nil
}

type sweeps struct {
	n   int
	err error
}

func (s *sweeps) Sweep(_, _ string, now time.Time) (contract.Swept, *contract.Snapshot, error) {
	s.n++
	return contract.Swept{Ran: true, At: now}, nil, s.err
}

type login struct {
	contract.NoPlatform
	dir string
}

func (l login) Dirs() (contract.Dirs, error)  { return contract.Dirs{State: l.dir}, nil }
func (login) Replace(tmp, final string) error { return os.Rename(tmp, final) }

// call prepares status on the evening fixture, as the front of every
// command would hand it over, at 100 columns with the marks.
func call(t *testing.T, f *testkit.Fixture, caller contract.CallerKind, flags ...string) (*contract.Call, *bytes.Buffer, *sweeps) {
	t.Helper()
	t.Setenv("COLUMNS", "100")
	t.Setenv("WHALESHARK_MARKS", "utf8")
	dir := t.TempDir()
	write := func(path string, v any) {
		if err := contract.WriteVersioned(login{}, path, contract.FileVersion, v); err != nil {
			t.Fatal(err)
		}
	}
	for pane, ctx := range f.Ctx {
		write(contract.CtxPath(dir, pane), ctx)
	}
	write(filepath.Join(dir, "ui.json"), f.UI)
	k, sw := contract.NewKit(), &sweeps{}
	k.Store, k.Terms, k.Sweeper, k.Platform = record{s: &f.State}, picture{snap: f.Terms}, sw, login{dir: dir}
	Plug(k)
	out := new(bytes.Buffer)
	c := &contract.Call{Kit: k, Command: contract.Find("status"), Flags: map[string][]string{}, Caller: contract.Caller{Kind: caller},
		Root: t.TempDir(), Run: f.State.Run.ID, Now: f.Now, Out: out, Err: out}
	for _, flag := range flags {
		c.Flags[flag] = []string{""}
	}
	return c, out, sw
}

// status sweeps once, reads the record and the small files, and prints what
// the builder gives: the fixture's own view, but for the other jobs, which it
// does not count yet.
func TestStatus(t *testing.T) {
	f := evening(t)
	for _, caller := range []contract.CallerKind{contract.Human, contract.Orchestrator} {
		c, out, sw := call(t, f, caller)
		result, err := k(c)
		if err != nil || sw.n != 1 {
			t.Fatalf("%s: %v after %d sweeps", caller, err, sw.n)
		}
		want := *f.Views[caller]
		want.Counts.Elsewhere = 0
		if got, want := indent(t, result), indent(t, &want); got != want {
			t.Errorf("%s: the result is not the fixture's view\n%s", caller, diff(want, got))
		}
		if got, want := out.String(), text(&want, Options{Width: 100}); got != want {
			t.Errorf("%s: the text is not the printed view\n%s", caller, diff(want, got))
		}
	}
}

// A status takes the shared lock once and gives it back: a reader that read
// in a loop would have every command wait for its turn behind it.
func TestStatusReadsTheRecordOnce(t *testing.T) {
	f := evening(t)
	for _, flags := range [][]string{nil, {"all"}, {"items"}} {
		c, _, _ := call(t, f, contract.Human, flags...)
		reads := 0
		c.Kit.Store = record{s: &f.State, reads: &reads}
		if _, err := k(c); err != nil || reads != 1 {
			t.Errorf("status %v read the record %d times (%v)", flags, reads, err)
		}
	}
}

func k(c *contract.Call) (any, error) { return c.Kit.Handler("status")(c) }

func TestStatusFlags(t *testing.T) {
	f := evening(t)

	c, out, _ := call(t, f, contract.Human, "items")
	if _, err := k(c); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.HasPrefix(got, "\nFOR YOU   3 waiting") || strings.Contains(got, "TEAM") || strings.Contains(got, "BUILDING") || !strings.HasSuffix(got, "live · checked 0s ago\n") {
		t.Errorf("--items prints\n%s", got)
	}

	c, out, _ = call(t, f, contract.Orchestrator, "all")
	if _, err := k(c); err != nil {
		t.Fatal(err)
	}
	golden(t, "status-all-100.txt", out.String())

	// --sweep --quiet sweeps and prints nothing; what the sweep did is the result.
	c, out, sw := call(t, f, contract.Unbound, "sweep", "quiet")
	result, err := k(c)
	if err != nil || sw.n != 1 || out.Len() != 0 {
		t.Errorf("--sweep --quiet: %v, %d sweeps, printed %q", err, sw.n, out)
	}
	if got := indent(t, result); !strings.Contains(got, `"ran": true`) || !strings.Contains(got, `"changed": false`) {
		t.Errorf("--sweep --quiet results in %s", got)
	}
	c, out, sw = call(t, f, contract.Unbound, "sweep", "quiet")
	sw.err = errors.New("the sweep failed")
	if _, err := k(c); err != sw.err || out.Len() != 0 {
		t.Errorf("a failed sweep ends with %v and prints %q", err, out)
	}

	for _, flag := range []string{"brief", "everywhere"} {
		c, _, sw := call(t, f, contract.Human, flag)
		var r *contract.Refusal
		if _, err := k(c); !errors.As(err, &r) || r.Code != "not_built" || sw.n != 0 {
			t.Errorf("--%s: %v", flag, err)
		}
	}
	c, _, _ = call(t, f, contract.Human)
	c.Args = []string{"T1"}
	var r *contract.Refusal
	if _, err := k(c); !errors.As(err, &r) || r.Exit != contract.ExitUsage {
		t.Errorf("an argument: %v", err)
	}
}

// With no run there is nothing to sweep or to read, and the text says how to
// start one. With the keeper away the record is still shown, and the age line
// counts from when the terminals were last seen.
func TestStatusWithout(t *testing.T) {
	f := evening(t)
	for _, run := range []string{"", "r9"} {
		c, out, sw := call(t, f, contract.Human)
		c.Run = run
		if result, err := k(c); result != nil || err != nil || !strings.Contains(out.String(), "> whaleshark run new") || (run == "" && sw.n != 0) {
			t.Errorf("run %q: %v, %v, %q", run, result, err, out)
		}
	}

	c, out, _ := call(t, f, contract.Human)
	c.Kit.Terms, c.Now = picture{}, f.Now.Add(3*time.Minute)
	if _, err := k(c); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.HasPrefix(got, "the engine is not running: `whaleshark open` starts it\n") || !strings.HasSuffix(got, "STALE since 21:14 · the engine is not running\n") || !strings.Contains(got, " ◌ T12 ") {
		t.Errorf("without the keeper status prints\n%s", got)
	}

	// A context file from a newer program counts as absent.
	c, _, _ = call(t, f, contract.Human)
	dirs, _ := c.Kit.Platform.Dirs()
	if err := os.WriteFile(contract.CtxPath(dirs.State, "w1:p2"), []byte(`{"version": 99, "pct": 5, "known": true, "at": "2026-10-08T21:13:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := k(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range result.(*contract.View).Sections {
		for _, card := range s.Cards {
			if card.Task == "T1" && card.CtxKnown {
				t.Error("a newer context file was read")
			}
		}
	}
}
