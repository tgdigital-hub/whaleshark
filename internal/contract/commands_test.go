package contract

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

func marks(w Who) string {
	if w&(W|O|H) == W|O|H {
		return "*"
	}
	s := ""
	for i, m := range []Who{W, O, H} {
		if w&m != 0 {
			s += string("WOH"[i])
		}
	}
	return s
}

// The table must say what the command set says, which is kept a second time
// in testdata, written by hand from the same source.
func TestTableMatchesCommandSet(t *testing.T) {
	want, err := os.ReadFile("testdata/commands.golden")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range Commands {
		exits := strings.Trim(strings.ReplaceAll(fmt.Sprint(c.Exits), " ", ","), "[]")
		got = append(got, strings.Join([]string{c.Name, c.Section, fmt.Sprint(c.Phase), marks(c.Who), exits}, " "))
	}
	var lines []string
	for line := range strings.Lines(string(want)) {
		if !strings.HasPrefix(line, "#") {
			lines = append(lines, strings.Join(strings.Fields(line), " "))
		}
	}
	if !slices.Equal(got, lines) {
		t.Errorf("the table and the command set differ:\ngot\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(lines, "\n"))
	}
}

func TestCounts(t *testing.T) {
	section, phase := map[string]int{}, map[int]int{}
	seen := map[string]bool{}
	for _, c := range Commands {
		if seen[c.Name] {
			t.Errorf("%s is in the table twice", c.Name)
		}
		seen[c.Name] = true
		section[c.Section]++
		phase[c.Phase]++
		if c.Usage == "" || c.Help == "" || !strings.HasPrefix(c.Usage, c.Name) {
			t.Errorf("%s: usage or help missing", c.Name)
		}
		if c.Example != "" && !strings.Contains(c.Example, "whaleshark "+c.Name) {
			t.Errorf("%s: the example runs another command", c.Name)
		}
		for _, f := range append(c.Flags, CommonFlags...) {
			if !strings.Contains(c.Usage, "--"+f.Name) && !slices.ContainsFunc(CommonFlags, func(g Flag) bool { return g.Name == f.Name }) && c.Name != "server" {
				t.Errorf("%s: --%s is not in its usage", c.Name, f.Name)
			}
		}
	}
	if len(Commands) != 40 {
		t.Errorf("%d commands, want 40", len(Commands))
	}
	for name, want := range map[string]int{"setup": 12, "runs": 15, "worker": 4, "views": 6, "code": 3} {
		if section[name] != want {
			t.Errorf("section %s has %d commands, want %d", name, section[name], want)
		}
	}
	if phase[1] != 29 || phase[2] != 8 || phase[3] != 3 {
		t.Errorf("by phase: %v, want 29, 8 and 3", phase)
	}
}

func TestCallers(t *testing.T) {
	var worker []string
	for i := range Commands {
		c := &Commands[i]
		if c.May(Worker, "") {
			worker = append(worker, c.Name)
		}
		if c.Who&O != 0 && !c.May(Human, "") {
			t.Errorf("%s: the human may run everything the orchestrator may", c.Name)
		}
	}
	if want := []string{"guide", "help", "version", "hook", "report", "progress", "ask", "mail"}; !slices.Equal(worker, want) {
		t.Errorf("a worker may run %v, want %v", worker, want)
	}
	for _, tc := range []struct {
		command, sub string
		kind         CallerKind
		want         bool
	}{
		{"run", "new", Unbound, true}, {"run", "takeover", Unbound, true}, {"run", "close", Unbound, false},
		{"tell", "lead", Orchestrator, false}, {"tell", "lead", Human, true}, {"tell", "", Orchestrator, true},
		{"pause", "", Orchestrator, false}, {"pause", "", Unbound, false}, {"pause", "", Human, true},
		{"status", "", Unbound, true}, {"start", "", Unbound, false}, {"start", "", Human, true},
		{"trust", "", Unbound, false}, {"set", "", Unbound, true}, {"accept", "", Worker, false},
	} {
		if got := Find(tc.command).May(tc.kind, tc.sub); got != tc.want {
			t.Errorf("%s %s by %s: %v, want %v", tc.command, tc.sub, tc.kind, got, tc.want)
		}
	}
	undo := Find("answer").Flags[1]
	if undo.Name != "undo" || undo.Who != H {
		t.Errorf("answer --undo is for the human only: %+v", undo)
	}
	if f := Find("need").Flags[1]; f.Name != "holds" || f.Phase != 2 {
		t.Errorf("need --holds belongs to phase 2: %+v", f)
	}
}

func TestActions(t *testing.T) {
	prefixes := map[string]bool{}
	for _, a := range Actions {
		if a.Prefix != "" {
			prefixes[a.Prefix] = true
		}
		if len(a.Run) == 0 || a.How == HowPoint {
			continue
		}
		c := Find(a.Run[0])
		if c == nil {
			t.Fatalf("action %s runs %q, which is no command", a.ID, a.Run[0])
		}
		line := strings.Join(a.Run, " ")
		for _, never := range Never {
			if strings.HasPrefix(line, never) {
				t.Errorf("action %s runs %q, which a pane may never run", a.ID, never)
			}
		}
		if human := slices.Contains(a.Run, "--human"); human != (c.Who&U == 0 || a.ID == "team") {
			t.Errorf("action %s: --human is %v on a command marked %s", a.ID, human, marks(c.Who))
		}
	}
	if len(prefixes) != 7 {
		t.Errorf("%d shortcuts work from anywhere, want 7: %v", len(prefixes), prefixes)
	}
}

func TestLooksAndColours(t *testing.T) {
	if len(Looks) != 12 || len(Colours) != 16 {
		t.Fatalf("%d looks and %d colours, want 12 and 16", len(Looks), len(Colours))
	}
	plain := ""
	for _, l := range Looks {
		plain += l.Plain + " "
		if !slices.Contains(Colours, l.Colour) {
			t.Errorf("%s has the colour %q, which no scheme names", l.Look, l.Colour)
		}
	}
	if plain != "! # x * * ~ ~ ? = - . v " {
		t.Errorf("plain marks: %q", plain)
	}
}

func TestListOnly(t *testing.T) {
	k := NewKit()
	var out, errw bytes.Buffer
	if code := k.Main(k, []string{"help"}, &out, &errw); code != ExitOK {
		t.Fatalf("help: exit %d", code)
	}
	if n := strings.Count(out.String(), "(not built yet)"); n != 40 || strings.Count(out.String(), "\n") != 40 {
		t.Errorf("help marks %d commands as not built, want 40", n)
	}
	if code := k.Main(k, []string{"start"}, &out, &errw); code != ExitFailed || !strings.Contains(errw.String(), "not built yet") {
		t.Errorf("start: exit %d, %q", code, errw.String())
	}
	if code := k.Main(k, []string{"strat"}, &out, &errw); code != ExitUsage {
		t.Errorf("an unknown command: exit %d, want %d", code, ExitUsage)
	}
}
