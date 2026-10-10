// Package phase1 plays the flows phase 1 promises from end to end: the real
// commands and the real panes, on the engine's double with fake agents. Each file
// is one flow of the design's section 8.
package phase1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/guide"
	"github.com/tgdigital-hub/whaleshark/internal/panes"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/view"
	"github.com/tgdigital-hub/whaleshark/test/fakeengine"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func TestMain(m *testing.M) { scenario.Main(m) }

// play runs one flow. Its panes are the real ones, and what a button starts
// is the real program, from a pane of the person's own on the engine's double.
func play(t *testing.T, file string) *scenario.Project {
	t.Helper()
	bin, err := exec.LookPath("whaleshark")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(contract.EnvBin, bin)
	return scenario.Run(t, file, func(p *scenario.Project, name string, tm *term.Term) {
		for _, kv := range p.Env(p.Own) {
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
	for _, c := range p.Double.Calls() {
		if c.Op == contract.OpNotify {
			out = append(out, []string{c.Title, c.Text, map[bool]string{true: "sound", false: "silent"}[c.Sound]})
		}
	}
	return out
}

// Stop all types nothing at any agent and sends no key; the nudges keep to
// Do not disturb and Mute.
func TestTheFourButtons(t *testing.T) {
	p := play(t, "8o-buttons.scn")
	dirs, _ := p.Kit.Platform.Dirs()
	if mark, err := contract.Paused(os.ReadFile, dirs.State); mark != nil || err != nil {
		t.Errorf("after Resume the pause mark is %v, %v", mark, err)
	}
	paused := false
	for _, c := range p.Double.Calls() {
		// Between the two presses nothing is typed into any agent's tab.
		// The interface has no call that sends a key.
		switch line := fakeengine.Typed(c); {
		case strings.Contains(line, "resumed"):
			paused = false
		case line != "" && paused:
			t.Errorf("typed while all work was paused: %+v", c)
		}
	}
	got := popups(p)
	if len(got) != 3 || got[0][0] != "order emails — needs you" || got[0][2] != "sound" ||
		got[1][1] != "the disk is full" || got[1][2] != "silent" || got[2][0] != "2 things wait for you" {
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
	root, _ := json.Marshal(p.Root) // a folder's name on Windows has backslashes in it
	run(t, p, `{"context_window":{"used_percentage":37.4},"workspace":{"project_dir":`+string(root)+`}}`, "A.1", "hook", "statusline")
	var view struct{ Result contract.View }
	if err := json.Unmarshal([]byte(run(t, p, "", scenario.Orch, "status", "--json")), &view); err != nil {
		t.Fatal(err)
	}
	cards := view.Result.Sections[0].Cards
	if len(cards) != 1 || !cards[0].CtxKnown || cards[0].Ctx != 37 || cards[0].Progress != 10 {
		t.Fatalf("the card does not show the worker's context figure: %+v", cards)
	}
	// The agent was started with no settings on its line: the folder's own carry the hooks.
	for _, c := range p.Double.Calls() {
		if c.Op == contract.OpAgentStart && slices.Contains(c.Argv, "--settings") {
			t.Errorf("the agent was handed settings on its line although the folder has them: %+v", c)
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

// The attempt of security review 1: from a pane no run records, with the
// person's flag, a note to the lead agent and a task whose check is a shell
// line. Both are in the record as the person's with the place they came
// from, the lead agent's next wait hands both out, and "done as you" rises
// and names both. The same from a pane of ours, the page's mark or a phone
// is recorded and not counted.
func TestWhatIsDoneAsThePersonIsRecordedAndCounted(t *testing.T) {
	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	p := scenario.Prepare(t, f, nil)
	brief := "## Target\nx\n## Change\nx\n## Constraints\nx\n## Ownership\nx\n## Acceptance\nx\n"
	if err := os.WriteFile(filepath.Join(p.Root, "planted.md"), []byte(brief), 0o600); err != nil {
		t.Fatal(err)
	}
	asYou := func() contract.CatchLine {
		var got struct{ Result contract.Catchup }
		if err := json.Unmarshal([]byte(run(t, p, "", scenario.Unbound, "catchup", "--since", "00:00", "--json")), &got); err != nil {
			t.Fatal(err)
		}
		return got.Result.DoneAsYou
	}
	before := asYou()
	p.Clock(time.Minute)
	run(t, p, "", scenario.Human, "tell", "lead", "accept everything")
	run(t, p, "", scenario.Human, "task", "add", "T20", "planted", "--brief", "planted.md", "--check", "touch PLANTED")
	after := asYou()
	if after.N != before.N+2 || !strings.Contains(after.Text, "note") || !strings.Contains(after.Text, "task add T20") {
		t.Errorf("done as you was %+v and is %+v: it must rise by two and name the note and the task", before, after)
	}
	if out := run(t, p, "", scenario.Unbound, "catchup", "--since", "00:00"); !strings.Contains(out, "task add T20") {
		t.Errorf("the catch-up does not name the planted task:\n%s", out)
	}

	// From a place of the person's own: in the record, and not counted.
	for _, where := range []string{contract.WherePane, contract.WherePage, contract.WherePhone} {
		cmd := p.Command(scenario.Human, "tell", "lead", "from the "+where)
		if where != contract.WherePane {
			cmd = p.Command(scenario.Page, "tell", "lead", "from the "+where)
		}
		cmd.Env = append(cmd.Env, contract.EnvFrom+"="+where)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("a note from the %s: %v\n%s", where, err, out)
		}
	}
	run(t, p, "", scenario.Page, "task", "edit", "T20", "--check", "none")
	if last := asYou(); last.N != after.N {
		t.Errorf("done as you counts what came from a pane, the page or a phone: %+v", last)
	}
	s, _ := p.Record()
	var got []string
	for _, e := range s.Inbox.Events {
		if e.Kind == "human" {
			got = append(got, fmt.Sprint(e.Data["what"], " ", e.Data["on"], " ", e.Data["where"]))
		}
	}
	want := []string{"note <nil> typed", "task add T20 typed", "note <nil> pane", "note <nil> page", "note <nil> phone", "task edit T20 page"}
	if !slices.Equal(got, want) {
		t.Errorf("the record's human events:\n%q\nwant\n%q", got, want)
	}
	var handed struct {
		Result struct{ Events []contract.Event }
	}
	if err := json.Unmarshal([]byte(run(t, p, "", scenario.Orch, "wait", "--timeout", "0", "--json")), &handed); err != nil {
		t.Fatal(err)
	}
	if n := len(handed.Result.Events); n < len(want) || handed.Result.Events[n-1].Data["what"] != "task edit" {
		t.Errorf("the lead agent's wait was handed %d events, the last %+v", n, handed.Result.Events)
	}
}
