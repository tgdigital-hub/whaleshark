package launch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// A task with a card: its tab reads the card and the title, cut where
// ui.tab_title_chars says, its worker is told the card, and close finds the
// tab by the label it was opened with.
func TestACardInTheTabAndThePrompt(t *testing.T) {
	p := evening(t, 2, func(f *testkit.Fixture) {
		for _, id := range []string{"R1", "R2"} {
			f.State.Tasks[id].Ref, f.State.Tasks[id].Title = "C-42", "Own the order emails and the receipts of the shop"
		}
	})
	must(t, p.Command(scenario.Orch, "start", "R1"), 0, "working (R1.1)")
	labels := func() (out []string) {
		for _, c := range calls(p, contract.OpTabCreate) {
			out = append(out, c.Label)
		}
		return out
	}
	if got := labels(); len(got) != 1 || got[0] != "C-42 Own the order emails and the receip" {
		t.Fatalf("the tab was opened as %q", got)
	}
	s, dir := p.Record()
	if a := s.Attempts["R1.1"]; a.Place.Label != "C-42 Own the order emails and the receip" {
		t.Errorf("the attempt keeps the label %q", a.Place.Label)
	}
	if data, _ := os.ReadFile(filepath.Join(contract.AttemptDir(dir, "R1.1"), "prompt.md")); !strings.Contains(string(data), "This task is card C-42") {
		t.Errorf("the worker is not told its card:\n%s", data)
	}
	dirs, err := p.Kit.Platform.Dirs()
	if err == nil {
		err = os.MkdirAll(dirs.Config, 0o700)
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(dirs.Config, "config.toml"), []byte("[ui]\ntab_title_chars = 16\n"), 0o600)
	}
	if err != nil {
		t.Fatal(err)
	}
	must(t, p.Command(scenario.Orch, "start", "R2"), 0, "working (R2.1)")
	if got := labels(); len(got) != 2 || got[1] != "C-42 Own the ord" {
		t.Fatalf("with 16 characters the tab was opened as %q", got)
	}
	if l := title(&contract.Task{Name: "sign-up page"}, 40); l != "sign-up page" {
		t.Errorf("a task without a card is its name: %q", l)
	}
}

// Over [limits] load_max times the cores nothing new starts, and the lead
// agent hears why; under it, or with no line, the load is not asked.
func TestTheLoadHoldsStarts(t *testing.T) {
	was := loadNow
	defer func() { loadNow = was }()
	load := 1e6
	loadNow = func() (float64, bool) { return load, true }
	l := &launcher{project: contract.ProjectDefaults()}
	if l.loaded() != "" {
		t.Error("with no line the load held a start")
	}
	l.project.Limits.LoadMax = 2
	if got := l.loaded(); !strings.Contains(got, "over 2 times the") {
		t.Errorf("over the load: %q", got)
	}
	load = 0.5
	if got := l.loaded(); got != "" {
		t.Errorf("under the load: %q", got)
	}
	if _, err := os.Stat("/proc/loadavg"); err == nil {
		if _, ok := was(); !ok {
			t.Error("the load of a Linux machine could not be read")
		}
	}
}
