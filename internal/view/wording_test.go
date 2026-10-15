package view

import (
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// A task not yet started goes by the word task add gave it, on its card and
// in the count of the plan; a task that waits on another is pending.
func TestNotStartedWords(t *testing.T) {
	f := later(t)
	f.State.Tasks["T14"].After, f.State.Tasks["T14"].Status = nil, contract.TaskReady
	in := f.Input(contract.Human)
	in.All = true
	v := Build(in)
	for task, want := range map[string]string{"T13": "pending", "T14": "ready"} {
		if c, _ := card(t, v, task); c.Look != contract.LookQueued || c.Word != want {
			t.Errorf("%s: %s %q, want queued %q", task, c.Look, c.Word, want)
		}
	}
	got := text(v, Options{Width: 100})
	for _, want := range []string{" ○ T13  whole site         pending ", " ○ T14  go live            ready "} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is missing in\n%s", want, got)
		}
	}
	if strings.Contains(got, " queued ") {
		t.Errorf("a card still says queued\n%s", got)
	}
}

// Before anything has been seen there is no time to be stale since: the
// footer says that no sweep has run, once. Under --all the done tasks are
// listed, and no hint points at --all.
func TestFooterWords(t *testing.T) {
	v := Build(later(t).Input(contract.Human))
	v.Fresh = contract.Fresh{Notes: []string{"the engine is not running", noSweep}}
	got := text(v, Options{Width: 100})
	if !strings.HasSuffix(got, "\nno sweep yet · the engine is not running\n") || strings.Contains(got, "STALE") {
		t.Errorf("never swept, the footer reads\n%s", got[strings.LastIndex(strings.TrimSuffix(got, "\n"), "\n")+1:])
	}
	v.Fresh.Checked = v.At.Add(-time.Minute)
	if got = text(v, Options{Width: 100}); !strings.Contains(got, "\nSTALE since "+v.Fresh.Checked.Format("15:04")+" · the engine") {
		t.Errorf("swept a minute ago, the footer reads\n%s", got)
	}
	if !strings.Contains(got, "done today (whaleshark status --all)") {
		t.Errorf("status without --all lacks the hint\n%s", got)
	}
	if got = text(v, Options{Width: 100, All: true}); strings.Contains(got, "(whaleshark status --all)") {
		t.Errorf("status --all points at itself\n%s", got)
	}
}

// The TEAM line counts what FOR YOU lists, so the two say the same number.
func TestHeaderCountsWhatForYouLists(t *testing.T) {
	v := Build(later(t).Input(contract.Human))
	got := text(v, Options{Width: 130})
	n := plural(v.Counts.Waiting, "", "")
	if !strings.Contains(got, " · "+n+"for you · ") || !strings.Contains(got, "FOR YOU   "+n+"waiting") {
		t.Errorf("%d items: the two counts differ\n%s", v.Counts.Waiting, got)
	}
}

// catchup gives each waiting item a clause of its own, kept apart by
// semicolons: what it is and whom from, then what it is about.
func TestCatchupWaitingClauses(t *testing.T) {
	now := time.Date(2026, 10, 8, 21, 14, 0, 0, time.UTC)
	v := &contract.View{At: now, Items: []contract.Item{
		{ID: "n1", Name: "first file", Source: "a question from the lead agent"},
		{ID: "n2", Source: "sign-off asked by the lead agent"},
		{ID: "n3", Name: leadName, Source: "something to do, from the lead agent"},
		{ID: "q4", Name: "checkout form", Source: sourceWorker},
		{ID: "n5", Fleet: "billing", Name: "refund flow", Source: "passed up by the orchestrator"},
	}}
	c := away(v, &contract.State{Tasks: map[string]*contract.Task{}}, nil, nil, contract.ProjectFile{}, now.Add(-time.Hour))
	want := "a question from the lead agent about first file; sign-off asked by the lead agent; something to do, from the lead agent; " +
		"a question from checkout form; passed up by the orchestrator about billing › refund flow"
	if c.Waiting.N != 5 || c.Waiting.Text != want {
		t.Errorf("waiting: %d\n got %q\nwant %q", c.Waiting.N, c.Waiting.Text, want)
	}
}
