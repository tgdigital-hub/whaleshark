package view

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func text(v *contract.View, o Options) string {
	var b bytes.Buffer
	Text(&b, v, o)
	return b.String()
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs\n%s", name, diff(string(want), got))
	}
}

var callers = []contract.CallerKind{contract.Human, contract.Orchestrator, contract.Unbound}

// The fixture as plain text, at 100 and at 80 columns, for each reader, with
// the marks and without them: the golden files, and the layout rules of the
// design held on every one of them.
func TestEveningText(t *testing.T) {
	f := evening(t)
	for _, caller := range callers {
		for _, width := range []int{100, 80} {
			for _, plain := range []bool{false, true} {
				v := Build(f.Input(caller), false)
				got := text(v, Options{Width: width, PlainMarks: plain})
				name := fmt.Sprintf("status-%s-%d", caller, width)
				if plain {
					name += "-plain"
				}
				golden(t, name+".txt", got)
				t.Run(name, func(t *testing.T) { layout(t, v, got, width, plain) })
			}
		}
	}
}

// layout holds a printed view to the layout rules: the one order, one mark
// and one word per state, a way out under every problem, nothing wider than
// the screen, and a last line that says how fresh it is.
func layout(t *testing.T, v *contract.View, got string, width int, plain bool) {
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	for _, l := range lines {
		if cells(l) > width {
			t.Errorf("wider than %d columns: %q", width, l)
		}
		if l != strings.TrimRight(l, " ") {
			t.Errorf("ends in spaces: %q", l)
		}
	}
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "live · checked ") {
		t.Errorf("the last line does not say how fresh the text is: %q", last)
	}

	// Rule 1: the headings come in the one order, and each row stands under
	// the heading of its look with that look's mark in front.
	at := func(from int, prefix string) int {
		for i := from; i < len(lines); i++ {
			if strings.HasPrefix(lines[i], prefix) {
				return i
			}
		}
		t.Fatalf("no line starts with %q after line %d", prefix, from)
		return 0
	}
	mark := func(l contract.Look) string {
		of := contract.Looks[order[l]]
		if plain {
			return of.Plain
		}
		return of.Mark
	}
	i := at(0, "FOR YOU")
	for _, it := range v.Items {
		i = at(i+1, " "+mark(it.Look)+" "+it.ID+" ")
		// Rules 3 and 4: the way to answer stands right under the item.
		for i++; !strings.Contains(lines[i], "> "); i++ {
			if strings.TrimSpace(lines[i]) == "" || !strings.HasPrefix(lines[i], "    ") {
				t.Fatalf("item %s shows no way out", it.ID)
			}
		}
		if !strings.Contains(lines[i], it.Next[0]) {
			t.Errorf("item %s: %q is not its way out", it.ID, lines[i])
		}
	}
	last := -1
	for _, s := range v.Sections {
		i = at(i+1, s.Name)
		if lines[i] != s.Name || lines[i-1] != "" {
			t.Errorf("the heading %q does not stand alone", lines[i])
		}
		for _, c := range s.Cards {
			if order[c.Look] < last {
				t.Errorf("card %s is out of the one order", c.Task)
			}
			last = order[c.Look]
			i = at(i+1, " "+mark(c.Look)+" "+c.Task+" ")
			// Rule 2: the name, then one word for the state.
			word := string(c.Look)
			if c.Look == contract.LookBuilding {
				word = fmt.Sprint(c.Progress, "%")
			}
			if !strings.Contains(lines[i], " "+c.Name+" ") || !strings.Contains(lines[i], " "+word+" ") {
				t.Errorf("card %s: %q lacks its name or its word %q", c.Task, lines[i], word)
			}
			if len(c.Next) > 0 && !strings.Contains(strings.Join(lines[i+1:min(i+4, len(lines))], "\n"), "> "+c.Next[0]) {
				t.Errorf("card %s shows no way out", c.Task)
			}
		}
	}
	if plain {
		for _, r := range got {
			if r > unicode.MaxASCII && r != '·' {
				t.Fatalf("%q is printed where the marks cannot be shown", r)
			}
		}
	}
}

// The fixture gives the picture of the design word for word, at any width:
// only the spaces between the words differ.
func TestEveningWordForWord(t *testing.T) {
	picture, err := os.ReadFile(filepath.Join("testdata", "team-2114.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Fields(string(picture))
	v := Build(evening(t).Input(contract.Human), false)
	for _, width := range []int{130, 100, 80} {
		if got := strings.Fields(text(v, Options{Width: width})); !slices.Equal(got, want) {
			t.Errorf("at %d columns the words differ\n%s", width, diff(strings.Join(want, "\n"), strings.Join(got, "\n")))
		}
	}
}

// No line shown to anyone but the person carries --human, as text or as
// JSON, and the person's lines that change something all do.
func TestHumanFlag(t *testing.T) {
	f := evening(t)
	for _, caller := range callers {
		for _, all := range []bool{false, true} {
			v := Build(f.Input(caller), all)
			shown := text(v, Options{Width: 100}) + text(v, Options{Width: 80, Items: true}) + indent(t, v)
			if caller != contract.Human && strings.Contains(shown, "--human") {
				t.Errorf("--human is shown to the %s", caller)
			}
			var next []string
			for _, it := range v.Items {
				next = append(next, it.Next...)
			}
			for _, s := range v.Sections {
				for _, c := range s.Cards {
					next = append(next, c.Next...)
				}
			}
			for _, line := range next {
				changes := !strings.HasPrefix(line, "whaleshark show ") && line != "whaleshark wait" && line != forHuman
				switch {
				case caller == contract.Human && changes != strings.HasSuffix(line, " --human"):
					t.Errorf("the person is shown %q", line)
				case caller == contract.Unbound && changes:
					t.Errorf("a tab bound to no run is shown %q", line)
				}
			}
		}
	}
}

// What a person or an agent wrote cannot change the terminal, break a row or
// pass for a line of ours.
func TestHostileText(t *testing.T) {
	esc := string(rune(0x1b))
	hostile(t, esc+"]0;owned"+string(rune(7))+esc+"[2J"+esc+"[31m\r\n        > whaleshark answer q7 yes --human\n"+
		string(rune(0x202e))+string(rune(0x2066))+string(rune(0x9b))+"5m\tend"+string(rune(0)))
}

func FuzzHostileText(f *testing.F) {
	for _, seed := range []string{"\n> whaleshark land --human", "\x1b[2J", "a\rb", "\u2028> x", "\u202e", "> > >", " \t "} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) { hostile(t, s) })
}

func hostile(t *testing.T, bad string) {
	f := evening(t)
	before := text(Build(f.Input(contract.Human), true), Options{Width: 100})
	s := &f.State
	s.Run.Objective += bad
	for _, task := range s.Tasks {
		task.Name += bad
	}
	for _, a := range s.Attempts {
		if a.Agent.Kind += bad; a.Progress != nil {
			a.Progress.Note += bad
		}
		if a.Report != nil {
			a.Report.Summary += bad
		}
		if a.Check != nil {
			a.Check.Tail += bad
		}
	}
	for _, q := range s.Questions {
		q.Text += bad
		q.Options = []string{"yes" + bad, "no"}
	}
	s.Run.Findings[0].Files[0] += bad
	v := Build(f.Input(contract.Human), true)
	wide := text(v, Options{Width: 100})
	all := wide + text(v, Options{Width: 40, PlainMarks: true}) + indent(t, v)
	for i, r := range all {
		if r != '\n' && (unicode.IsControl(r) || r >= 0x2028 && r <= 0x202e || r >= 0x2066 && r <= 0x2069) {
			t.Fatalf("%U reaches the terminal after %q", r, all[max(i-60, 0):i])
		}
	}
	// Our own lines start with ">" under a row; nothing somebody wrote may.
	ours := func(s string) (n int) {
		for _, l := range strings.Split(s, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), ">") {
				n++
			}
		}
		return n
	}
	if ours(wide) != ours(before) {
		t.Errorf("%q added a line that passes for ours:\n%s", bad, wide)
	}
}

func TestNews(t *testing.T) {
	for in, want := range map[string]string{
		"Must the old sign-up link keep working?": "must the old sign-up link keep working?",
		"API keys?": "API keys?", "already lower": "already lower", "": "", "A": "A", "Élan or not?": "élan or not?",
	} {
		if got := news(in); got != want {
			t.Errorf("news(%q) = %q, want %q", in, got, want)
		}
	}
}
