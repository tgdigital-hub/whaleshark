// Package phase1 plays the flows phase 1 promises from end to end: the real
// commands and the real panes, on the fake herdr with fake agents. Each file
// is one flow of the design's section 8.
package phase1

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/guide"
	"github.com/tgdigital-hub/whaleshark/internal/panes"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/view"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func TestMain(m *testing.M) { scenario.Main(m) }

// play runs one flow. Its panes are the real ones, and what a button starts
// is the real program, from a pane of the person's own on the fake herdr.
func play(t *testing.T, file string) *scenario.Project {
	t.Helper()
	bin, err := exec.LookPath("whaleshark")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(contract.EnvBin, bin)
	return scenario.Run(t, file, func(p *scenario.Project, name string, tm *term.Term) {
		for _, kv := range append(p.Herdr.Env(), contract.EnvPane+"="+p.Own) {
			k, v, _ := strings.Cut(kv, "=")
			t.Setenv(k, v)
		}
		view.Plug(p.Kit)
		panes.Run(name, tm, p.Kit, p.Root, "")
	})
}

func TestHandOutThreeTasks(t *testing.T)             { play(t, "8a-tasks.scn") }
func TestAQuestionAndItsAnswer(t *testing.T)         { play(t, "8b-question.scn") }
func TestAResultSentBackComesBackFixed(t *testing.T) { play(t, "8a-sent-back.scn") }

// An agent at a prompt is an item first and a nudge second, once.
func TestWorkersThatStall(t *testing.T) {
	p := play(t, "8e-stalls.scn")
	got := popups(p)
	if len(got) != 1 || got[0][0] != "part two — at a prompt" {
		t.Errorf("the pop-ups: %q", got)
	}
	s, _ := p.Record()
	for _, q := range s.Questions {
		if q.Cause == contract.CausePrompt && q.ShownAt.IsZero() {
			t.Errorf("the item was nudged about and not stamped as shown: %+v", q)
		}
	}
}

// popups are the pop-ups the terminals were asked for: title, body, sound.
func popups(p *scenario.Project) (out [][]string) {
	for _, call := range p.Herdr.Calls() {
		if len(call) == 7 && call[0] == "notification" {
			out = append(out, []string{call[2], call[4], call[6]})
		}
	}
	return out
}

// Stop all types nothing at any agent and sends no key; the nudges keep to
// Do not disturb and Mute.
func TestTheFourButtons(t *testing.T) {
	p := play(t, "8o-buttons.scn")
	dirs, _ := p.Kit.Platform.Dirs()
	if mark, err := contract.Paused(dirs.State); mark != nil || err != nil {
		t.Errorf("after Resume the pause mark is %v, %v", mark, err)
	}
	paused := false
	for _, call := range p.Herdr.Calls() {
		line := strings.Join(call, " ")
		if strings.Contains(line, "send-keys") {
			t.Errorf("a key was sent: %v", call)
		}
		// Between the two presses nothing is typed into any agent's tab.
		switch typed := call[0] == "agent" && call[1] == "prompt"; {
		case typed && strings.Contains(line, "resumed"):
			paused = false
		case typed && paused:
			t.Errorf("typed while all work was paused: %v", call)
		case typed && strings.Contains(line, "events waiting"):
		}
	}
	got := popups(p)
	if len(got) != 3 || got[0][0] != "order emails — needs you" || got[0][2] != "request" ||
		got[1][1] != "the disk is full" || got[1][2] != "none" || got[2][0] != "2 things wait for you" {
		t.Errorf("the pop-ups under Do not disturb and Mute: %q", got)
	}
}

// run runs one more real command in a flow's project and returns what it printed.
func run(t *testing.T, p *scenario.Project, stdin, who string, args ...string) string {
	t.Helper()
	cmd := p.Command(who, args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", who, args, err, out)
	}
	return string(out)
}

// The rules block and the hooks are where the agent tool reads them, the
// status line of a real worker's tab reaches the view as its context figure,
// and --remove takes out exactly what init wrote.
func TestTheRulesAndTheHooks(t *testing.T) {
	p := play(t, "8q-rules.scn")
	home, _ := os.UserHomeDir()
	rules, settings := filepath.Join(home, ".claude", "CLAUDE.md"), filepath.Join(p.Root, ".claude", "settings.local.json")
	if data, _ := os.ReadFile(rules); !bytes.Contains(data, []byte(strings.TrimSpace(guide.Block))) {
		t.Fatalf("the rules block is not in the lead agent's instruction file:\n%s", data)
	}
	data, _ := os.ReadFile(settings)
	if !bytes.Contains(data, []byte(" hook gate")) || !bytes.Contains(data, []byte(" hook statusline")) {
		t.Fatalf("the project's settings lack a hook:\n%s", data)
	}
	// The worker's own harness hands its status line the figure.
	run(t, p, `{"context_window":{"used_percentage":37.4},"workspace":{"project_dir":"`+p.Root+`"}}`, "A.1", "hook", "statusline")
	var view struct{ Result contract.View }
	if err := json.Unmarshal([]byte(run(t, p, "", scenario.Orch, "status", "--json")), &view); err != nil {
		t.Fatal(err)
	}
	cards := view.Result.Sections[0].Cards
	if len(cards) != 1 || !cards[0].CtxKnown || cards[0].Ctx != 37 || cards[0].Progress != 10 {
		t.Fatalf("the card does not show the worker's context figure: %+v", cards)
	}
	// The agent was started with no settings on its line: the folder's own carry the hooks.
	for _, call := range p.Herdr.Calls() {
		if len(call) > 1 && call[0] == "agent" && call[1] == "start" && slices.Contains(call, "--settings") {
			t.Errorf("the agent was handed settings on its line although the folder has them: %v", call)
		}
	}
	if out := run(t, p, "", scenario.Human, "init", "--remove"); !strings.Contains(out, "the rules block is out of") {
		t.Fatalf("init --remove said:\n%s", out)
	}
	if _, err := os.Stat(rules); err == nil {
		t.Error("the instruction file init created is still there")
	}
	if data, _ := os.ReadFile(settings); bytes.Contains(data, []byte(" hook ")) {
		t.Errorf("the hooks are still in the project's settings:\n%s", data)
	}
}

func TestTheFourFormsOfAnItem(t *testing.T) { play(t, "8n-forms.scn") }
