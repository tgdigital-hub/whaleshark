// Package phase3 plays the flows of phase 3 that need no server: a browser
// check from the task to its acceptance, and an upgrade of the program
// under a running job.
package phase3

import (
	"testing"

	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func TestMain(m *testing.M) { scenario.Main(m) }

// A browser check end to end (8l), with a fake agent that saves a
// screenshot: the report lists the file as the picture it is.
func TestABrowserCheck(t *testing.T) {
	p := scenario.Run(t, "8l-browser-check.scn", nil)
	s, _ := p.Record()
	got := s.Attempts["A.1"].Report.Evidence
	if len(got) != 1 || got[0].Name != "home.png" || got[0].Kind != "png" || got[0].Size == 0 {
		t.Errorf("the report of A.1 lists %+v", got)
	}
	if said := s.Attempts["A.1"].Report.Summary; said != "the home page shows its menu" {
		t.Errorf("the report that was taken is %q: the one before it had no screenshot", said)
	}
	for _, e := range s.Inbox.Events {
		if e.Kind == "rejected" {
			t.Errorf("a report the worker could mend was kept as an event: %+v", e)
		}
	}
}
