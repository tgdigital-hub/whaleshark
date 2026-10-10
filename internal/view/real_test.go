package view_test

import (
	"path/filepath"
	"testing"

	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func TestMain(m *testing.M) { scenario.Main(m) }

// The three commands as the real program: what the person looked at is
// marked seen, the tab asked for is the one in front, and the lead agent is
// told that the person asked to be caught up, once, and not that it asked itself.
func TestViewsAsTheProgram(t *testing.T) {
	p := scenario.Run(t, filepath.Join("testdata", "views.scn"), nil)
	s, _ := p.Record()
	if seen := s.Tasks["T7"].SeenAt; !seen.Equal(s.Tasks["T3"].SeenAt) || seen.Format("15:04") != "21:14" {
		t.Errorf("T7 was seen at %s and T3 at %s", seen, s.Tasks["T3"].SeenAt)
	}
	asked := 0
	for _, e := range s.Inbox.Events {
		if e.Kind == "human" && e.Data["what"] == "catchup" {
			asked++
		}
	}
	if asked != 1 {
		t.Errorf("the lead agent was told %d times that the person asked", asked)
	}
}
