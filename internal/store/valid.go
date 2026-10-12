package store

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// valid passes through the one validator (18.4) everything in a record that
// becomes part of a path, a name in the keeper or a word on a command line, and
// holds every entry to the id it is filed under. Free text is data and is
// not looked at. Paths are checked for their form alone: whether one still
// lies inside the project is asked where it is used.
func valid(s *contract.State, run string) (err error) {
	must := func(kind string, values ...string) {
		for _, v := range values {
			if err == nil {
				err = cli.Valid(kind, v, "")
			}
		}
	}
	may := func(kind string, values ...string) {
		for _, v := range values {
			if v != "" {
				must(kind, v)
			}
		}
	}
	filed := func(what, key string, ok bool) {
		if err == nil && !ok {
			err = fmt.Errorf("%s %.40q does not carry its own id", what, key)
		}
	}

	must("id", run)
	filed("run", run, s.Run.ID == run)
	if o := s.Run.Orchestrator; o != nil {
		may("pane", o.Pane)
	}
	// A fleet's name becomes a tab's label and its tool a program's name.
	if up := s.Run.Parent; up != nil {
		must("id", up.Run)
		must("name", up.Fleet)
	}
	for name, f := range s.Run.Fleets {
		if filed("fleet", name, f != nil); f != nil {
			must("name", name)
			must("id", f.Run)
			may("agent", f.Tool)
			may("pane", f.Pane)
			must("id", f.Groups...)
		}
	}
	// Phase 6: what an approval names is an id of this record or a key of
	// the settings table; a protected file's path is data, shown and never
	// an argument.
	for _, a := range []*contract.Approval{s.Run.Plan, s.Run.Protect} {
		if a != nil {
			must("id", a.Approved...)
			may("id", a.Item)
		}
	}
	for id, t := range s.Tasks {
		if filed("task", id, t != nil && t.ID == id); t == nil {
			continue
		}
		must("id", id)
		must("name", t.Name)
		must("id", t.After...)
		must("attempt", t.Attempts...)
		must("path", t.Owns...)
		may("title", t.Title)
		may("agent", t.Agent)
		may("name", t.Fleet)
		if dir, ok := strings.CutPrefix(t.Placement, contract.PlaceCwd); ok {
			must("path", dir)
		}
	}
	for id, a := range s.Attempts {
		if filed("attempt", id, a != nil && a.ID == id); a == nil {
			continue
		}
		must("attempt", id)
		must("id", a.Task)
		may("agent", a.Agent.Kind, a.Agent.Name)
		may("pane", a.Place.Pane)
		may("id", a.Leads)
	}
	for id, q := range s.Questions {
		if filed("item", id, q != nil && q.ID == id); q == nil {
			continue
		}
		must("id", id)
		may("id", q.Task)
		may("attempt", q.Attempt)
		must("id", q.Tasks...)
		if key, _ := contract.FindKey(cmp.Or(q.Set, &contract.SetAsk{Key: "theme"}).Key); key == nil {
			filed("the setting of item", id, false)
		}
		for _, twin := range []*contract.Twin{q.Up, q.Down} {
			if twin != nil {
				must("id", twin.Run, twin.ID)
			}
		}
	}
	return err
}
