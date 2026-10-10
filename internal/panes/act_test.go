package panes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/term/termtest"
	"github.com/tgdigital-hub/whaleshark/internal/view"
)

// childMark makes this test program stand in for the real one: a pane under
// test starts it as it starts itself, with a row of the action table.
const childMark = "PANES_TEST_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(childMark) != "" {
		os.Exit(child(os.Args[1:]))
	}
	os.Setenv(childMark, "1")
	os.Exit(m.Run())
}

// child writes down how it was called, in children.log of the project, with
// the text of a file in the file's place. `set` really sets; `status` and
// `catchup` answer as the real ones do; a file named refuse makes every
// other command refuse with what the file says.
func child(args []string) int {
	var line, words []string
	root := ""
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--root", "--run":
			if i++; a == "--root" {
				root = args[i]
			}
		case "--file":
			i++
			text, _ := os.ReadFile(args[i])
			line, words = append(line, a, strconv.Quote(string(text))), append(words, string(text))
		default:
			if line = append(line, a); !strings.HasPrefix(a, "--") {
				words = append(words, a)
			}
		}
	}
	if os.Getenv(contract.EnvFrom) != contract.WherePane {
		line = append(line, "(not marked as from a pane)")
	}
	f, err := os.OpenFile(filepath.Join(root, "children.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 9
	}
	fmt.Fprintln(f, strings.Join(line, " "))
	f.Close()
	switch why, err := os.ReadFile(filepath.Join(root, "refuse")); {
	case args[0] == "set":
		k := contract.NewKit()
		k.Platform = system(root, nil)
		if _, err := set(&contract.Call{Kit: k, Command: contract.Find("set"), Args: words[1:], Now: time.Now(), Out: os.Stdout}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return contract.ExitUsage
		}
	case args[0] == "status":
		fmt.Println(`{"ok":true,"result":{"ran":true,"at":"2026-10-08T21:14:00Z","changed":false}}`)
	case err == nil:
		fmt.Fprint(os.Stderr, string(why))
		return contract.ExitRefused
	case args[0] == "catchup":
		at := time.Date(2026, 10, 8, 21, 16, 0, 0, time.UTC)
		json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "result": contract.Catchup{From: at, To: at.Add(64 * time.Minute),
			Waiting: contract.CatchLine{N: 1, Text: "a question from checkout form"}, Finished: contract.CatchLine{N: 5, Text: "login page, search box"},
			AnsweredLead: 3, StillBuilding: 4}})
	case args[0] == "team" && args[1] == "list":
		fmt.Println(`{"ok":true,"result":[{"name":"site-review","from":"yours","about":"Three reviewers and one writer","tasks":[{"title":"Read {goal}"},{"title":"Write it up"}]},` +
			`{"name":"lint-all","from":"project","held":true,"tasks":[{"title":"Run the linter"}]}]}`)
	case args[0] == "answer":
		fmt.Println(args[1] + " answered\nIt can be taken back for 4s: whaleshark answer " + args[1] + " --undo --human")
	default:
		fmt.Println("ran " + args[0])
	}
	return 0
}

// children is every command line the pane has started that begins so.
func (wd *world) children(prefix string) []string {
	data, _ := os.ReadFile(filepath.Join(wd.home, "children.log"))
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.HasPrefix(l, prefix) {
			lines = append(lines, l)
		}
	}
	return lines
}

// ran waits for a child the test has not yet seen whose line begins so.
func (wd *world) ran(prefix string) string {
	wd.t.Helper()
	for end := time.Now().Add(4 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		all := wd.children("")
		for i := wd.next; i < len(all); i++ {
			if strings.HasPrefix(all[i], prefix) {
				wd.next = i + 1
				return all[i]
			}
		}
	}
	wd.t.Fatalf("no child was started with %q; the pane started:\n%s\nand shows:\n%s", prefix, strings.Join(wd.children(""), "\n"), wd.screen())
	return ""
}

// asked waits for a call to the terminals the test has not yet seen that holds the words.
func (wd *world) asked(words string) {
	wd.t.Helper()
	for end := time.Now().Add(4 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		calls := wd.double.Calls()
		for i := wd.nextCall; i < len(calls); i++ {
			if strings.Contains(said(calls[i]), words) {
				wd.nextCall = i + 1
				return
			}
		}
	}
	wd.t.Fatalf("the terminals were never asked for %q:\n%+v", words, wd.double.Calls())
}

// keyGap is the time a test leaves before a key that is a command. It is
// well over burstGap, because the pane counts from when it takes a key, and
// on a busy machine that is some time after the test sent it.
const keyGap = burstGap + 150*time.Millisecond

// settle waits as a person does before a click: until the rows have stood
// still for longer than the click guard asks, however late the pane drew them.
func (wd *world) settle() { wd.s.Still(clickWait + 50*time.Millisecond) }

// key presses keys as a person gives commands: each long enough after the
// last that the typing guard does not take them for a sentence.
func (wd *world) key(names ...string) {
	for _, name := range names {
		time.Sleep(keyGap)
		wd.s.Key(name)
	}
}

// click clicks a text once the click guard has let the rows settle.
func (wd *world) click(text string) {
	wd.t.Helper()
	wd.shows(text)
	wd.settle()
	if !wd.s.ClickText(text) {
		wd.t.Fatalf("%q is not on the screen:\n%s", text, wd.screen())
	}
}

// over waits for the list of that name to lie over the pane: its name stands
// alone on the first row. A name is not looked for as a text anywhere, since
// the pane beneath may show the same words, and a row is read without the
// spaces it ends in.
func (wd *world) over(name string) {
	wd.t.Helper()
	for end := time.Now().Add(3 * time.Second); wd.s.Row(0) != " "+name; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			wd.t.Fatalf("the list %q never lay over the pane; it shows:\n%s", name, wd.screen())
		}
	}
}

// ours opens two panes beside the lead agent's in the double, writes
// them down in ui.json as `ui` does, and makes the pane under test one of them.
func ours(wd *world) {
	fl, _ := wd.double.Split("w1:p1", contract.Right, 0.7)
	ac, _ := wd.double.Split("w1:p1", contract.Down, 0.75)
	dirs, _ := wd.sys.Dirs()
	ui := contract.UIFile{Versioned: contract.Versioned{Version: contract.FileVersion}, FleetPane: fl.ID, ActionsPane: ac.ID, LastHere: wd.fx.Now}
	if err := contract.WriteVersioned(wd.sys, filepath.Join(dirs.State, "ui.json"), contract.FileVersion, ui); err != nil {
		wd.t.Fatal(err)
	}
	wd.p.me = map[string]string{fleet: fl.ID, actions: ac.ID}[wd.p.kind]
}

// outcome is what a row of the action table must lead to: the words typed
// where it asks for some, then a child, a call to the terminals or a list on the screen.
type outcome struct {
	typed, child, terms, shows string
	twice                      bool
}

// outcomes is every row of the action table, run on the question q7 of the
// task T2, which is what the tests below have selected.
var outcomes = map[string]outcome{
	"answer":         {typed: "in the morning", child: `answer q7 --file "in the morning" --human`},
	"undo":           {child: "answer q7 --undo --human"},
	"carry-on":       {terms: "point w1:p1 whaleshark: questions for the human"},
	"ask-lead":       {terms: "point w1:p1 whaleshark: a question is waiting"},
	"note-lead":      {typed: "look at the prices", child: `tell lead --file "look at the prices" --human`},
	"accept":         {child: "accept T2 --human"},
	"accept-by-hand": {typed: "read the diff", child: `accept T2 --by-hand --file "read the diff" --human`},
	"send-back":      {typed: "no tests", child: `reject T2 --file "no tests" --human`},
	"stop":           {twice: true, child: "stop T2 --human"},
	"start":          {child: "start T2 --human"},
	"retry":          {child: "start T2 --retry --human"},
	"note":           {typed: "see the sheet", child: `tell T2 --file "see the sheet" --human`},
	"close":          {child: "close T2 --human"},
	"stop-all":       {twice: true, child: "pause --everywhere --human"},
	"resume":         {child: "resume --everywhere --human"},
	"dnd":            {child: "set dnd on --human"},
	"mute":           {child: "set mute on --human"},
	"theme":          {child: "set theme "},
	"set":            {typed: "nudge.sound off", child: "set nudge.sound off --human"},
	"catchup":        {child: "catchup --json --human", shows: "WHILE YOU WERE AWAY  1h 04m"},
	"team":           {child: "team list --json --human", shows: " saved teams"},
	"go":             {terms: "tab-focus w1:t3"},
	"fleet":          {child: "ui fleet --human"},
	"actions":        {child: "ui actions --human"},
	"to-actions":     {child: "ui actions focus --human"},
	"narrower":       {terms: " right 0.02", child: "set fleet.width 120 --human"},
	"wider":          {terms: " left 0.02", child: "set fleet.width 120 --human"},
	"menu":           {shows: "every action"},
	"fold":           {child: "set folded on --human"},
	"show":           {shows: " answered: 0"},
	"keys":           {shows: "all keys"},
	"forget-phone":   {child: "dash phone forget q7 --human"},
}

// reached checks what a row led to, and leaves the pane as it was.
func (wd *world) reached(a contract.Action, how string, again func()) {
	wd.t.Helper()
	o, ok := outcomes[a.ID]
	if !ok {
		wd.t.Fatalf("the action table has a row %q this test does not know", a.ID)
	}
	if o.twice {
		wd.shows("? press ")
		again()
	}
	if o.typed != "" {
		wd.shows(" " + a.Label + ":")
		wd.s.Type(o.typed)
		wd.s.Key("enter")
	}
	if o.terms != "" {
		wd.asked(o.terms)
	}
	if o.child != "" {
		if line := wd.ran(o.child); !strings.Contains(line, "--human") || strings.Contains(line, "not marked") {
			wd.t.Errorf("%s by %s started %q", a.ID, how, line)
		}
	}
	if o.shows != "" {
		wd.shows(o.shows)
		wd.s.Key("esc")
	}
	if a.ID == "fold" {
		wd.shows("[^]")
	}
}

// Every row of the action table is reached with keys alone and with clicks
// alone, through the list of every action: by its name typed and Enter, and
// by a click on the last line and a click on its line.
func TestEveryRowIsReachedByKeysAndByClicks(t *testing.T) {
	wd := startWith(t, actions, 110, 44, nil, ours)
	wd.s.Type("\x1b[I")
	wd.shows("› ▲ sign-up page")
	for i, a := range contract.Actions {
		// Which of the lines that the typed name leaves is this row's.
		nth := 0
		for _, b := range contract.Actions[:i] {
			if strings.Contains(strings.ToLower(b.Label), strings.ToLower(a.Label)) {
				nth++
			}
		}
		wd.key("/")
		wd.shows("type to search")
		wd.s.Type(a.Label)
		for range nth {
			wd.s.Key("down")
		}
		wd.s.Key("enter")
		wd.reached(a, "keys", func() { wd.s.Key("enter") })

		pick := func() {
			wd.settle()
			wd.s.Click(4, 2+i)
		}
		wd.shows("ACTIONS ")
		wd.settle()
		wd.s.Click(3, wd.h-1)
		wd.shows("type to search")
		pick()
		wd.reached(a, "clicks", pick)
	}
	if len(outcomes) != len(contract.Actions) {
		t.Errorf("the test knows %d rows, the action table has %d", len(outcomes), len(contract.Actions))
	}
}

// Every key of the action table does in a pane what its row says, on the
// card or the item that is selected.
func TestEveryKeyOfTheActionTable(t *testing.T) {
	for _, kind := range []string{fleet, actions} {
		t.Run(kind, func(t *testing.T) {
			wd := startWith(t, kind, 110, 44, nil, ours)
			if wd.s.Type("\x1b[I"); kind == fleet {
				wd.key("j")
				wd.shows("▌▲ sign-up page")
			}
			wd.shows("KEYS GO HERE")
			for _, a := range contract.Actions {
				if a.Key == "" || kind == fleet && (a.ID == "fold" || a.ID == "show") {
					continue
				}
				wd.key(a.Key)
				wd.reached(a, "its key", func() { wd.key(a.Key) })
			}
		})
	}
}

// What stands on the screen as a button is a row of the table too.
func TestTheButtonsOfTheFleet(t *testing.T) {
	wd := startWith(t, fleet, 60, 46, nil, ours)
	wd.click("[<]")
	wd.asked(" right 0.02")
	wd.ran("set fleet.width 120 --human")
	wd.click("[>]")
	wd.asked(" left 0.02")
	wd.click("[x]")
	wd.ran("ui fleet --human")
	wd.click("[show]")
	for end := time.Now().Add(3 * time.Second); !wd.all(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("[show] never asked the builder for the cards that are done")
		}
	}
	// A card's state word opens what can be done with that agent.
	x, y, _ := wd.s.Find("check failed")
	wd.settle()
	wd.s.Click(x+2, y)
	wd.over("email sender")
	wd.shows("Go there", "Send back", "Stop", "Send a note or a link", "enter runs it")
	wd.s.Key("down")
	wd.s.Key("enter")
	wd.shows(" Send back:")
	wd.s.Type("the bounce tests fail")
	wd.s.Key("enter")
	wd.ran(`reject T7 --file "the bounce tests fail" --human`)

}

func TestTheButtonsOfTheActionPane(t *testing.T) {
	ac := startWith(t, actions, 150, 20, nil, ours)
	for _, button := range [][2]string{{"[ Stop all ]", ""}, {"[ Catch me up ]", "catchup --json --human"}, {"[ Do not disturb ]", "set dnd on --human"},
		{"[ Mute ]", "set mute on --human"}, {"[ Yes ]", `answer q7 --file "yes" --human`}, {"[v]", "set folded on --human"}} {
		text, child := button[0], button[1]
		ac.click(text)
		switch {
		case child == "":
			ac.shows("Stop all 12 agents? press S again")
			ac.click(text)
			child = "pause --everywhere --human"
		case text == "[ Catch me up ]":
			ac.shows("Waiting for you now    1   a question from checkout form", "Still building         4   Stopped 0   Failed 0", "any key closes")
			ac.key("q")
		}
		ac.ran(child)
	}
	// Folding asked the terminals to take the pane down to the least it is left,
	// and what a command refuses is said where the click was made.
	ac.asked(" down 0.5")
	ac.shows("[^]")
	ac.click("[^]")
	ac.asked(" up 0.15")
	os.WriteFile(filepath.Join(ac.home, "refuse"), []byte("The run is paused.\nNext: whaleshark resume --human\n"), 0o600)
	ac.click("[ No ]")
	ac.shows("The run is paused. · Next: whaleshark resume --human")
}

func (wd *world) all() bool {
	wd.mu.Lock()
	defer wd.mu.Unlock()
	return wd.in.All
}

func TestTheTypingLineAndItsDrafts(t *testing.T) {
	wd := start(t, actions, 104, 24, nil)
	wd.s.Type("\x1b[I")
	wd.key("j")
	wd.shows("› ▲ photo upload")
	// Pasted text goes nowhere while no line is open.
	wd.s.Paste("yes\r")
	wd.key("o")
	time.Sleep(keyGap)
	wd.s.Type("small and ")
	wd.s.Paste("large\nones")
	wd.shows("> small and large ones")
	wd.s.Key("esc")
	wd.ran(`set draft.q9 --file "small and large ones" --human`)
	wd.shows("KEYS GO HERE", "> small and large ones")
	dirs, _ := wd.sys.Dirs()
	for end := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var ui contract.UIFile
		if contract.ReadVersioned(wd.sys.Read, filepath.Join(dirs.State, "ui.json"), contract.FileVersion, &ui); ui.Drafts["q9"] == "small and large ones" {
			break
		} else if time.Now().After(end) {
			t.Fatalf("ui.json holds the drafts %v", ui.Drafts)
		}
	}
	if n := len(wd.children("answer")); n != 0 {
		t.Fatalf("leaving the line answered: %v", wd.children("answer"))
	}
	// The draft is the line's text when it is opened again, here by a click
	// on it; Enter sends it and the draft goes.
	wd.click("> small and large ones")
	wd.s.Key("backspace")
	wd.s.Key("enter")
	wd.ran(`answer q9 --file "small and large one" --human`)
	wd.ran("set draft.q9 --human")

	// Other and Send back open the line for the rest of the answer; an empty
	// line sends nothing.
	wd.key("k", "o")
	wd.shows("other: >")
	wd.s.Key("enter")
	wd.shows("nothing is typed yet")
	time.Sleep(keyGap)
	wd.s.Type("ask the shop owner")
	wd.s.Key("enter")
	wd.ran(`answer q7 --file "other: ask the shop owner" --human`)
	wd.click("[ Send back ]")
	wd.shows("send back: >")
	wd.s.Type("the totals are off")
	wd.s.Key("esc")
	wd.ran(`set draft.n4 --file "the totals are off" --human`)
	wd.shows("[ Send back ]  · typed: the totals are off")

	// Answered somewhere else meanwhile: the draft stays in view until dismissed.
	wd.change(func(v *contract.View) {
		gone := v.Items[2]
		gone.Answer, gone.By, gone.Buttons = "approve", &contract.Origin{Caller: contract.Human, Where: contract.WherePage}, nil
		v.Items, v.Answered = v.Items[:2], []contract.Item{gone}
	})
	wd.shows("answered on the page: approve · your draft: the totals are off", "answered: 1")
	wd.click("[ dismiss ]")
	wd.ran("set draft.n4 --human")
	for end := time.Now().Add(3 * time.Second); strings.Contains(wd.screen(), "your draft"); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("the dismissed draft is still shown:\n%s", wd.screen())
		}
	}
}

func TestAnAnswerSettlesAndCanBeUndone(t *testing.T) {
	wd := start(t, actions, 104, 24, nil)
	wd.s.Type("\x1b[I")
	wd.shows("› ▲ sign-up page")
	wd.key("y")
	wd.ran(`answer q7 --file "yes" --human`)
	// The record now holds the answer, to be used in four seconds: the item
	// stays where it stood and counts down.
	answered := func(used time.Time) {
		wd.change(func(v *contract.View) {
			it := wd.fx.Views[contract.Human].Items[0]
			it.Answer, it.Settles, it.Used = "yes", wd.fx.Now.Add(4*time.Second), used
			it.Buttons = []contract.Button{{Label: "Undo", Key: "u", Action: "undo"}}
			v.Items, v.Answered = wd.fx.Views[contract.Human].Items[1:], []contract.Item{it}
		})
	}
	answered(time.Time{})
	wd.shows("yes · u to undo · 4  [ Undo ]", "answered: 1")
	if first, second := wd.row("› ▲ sign-up page"), wd.row("▲ photo upload"); first > second {
		t.Fatalf("the settling item moved: it stands in row %d, the next item in row %d", first, second)
	}
	wd.clock.Store(int64(1500 * time.Millisecond))
	wd.shows("yes · u to undo · 3")
	wd.key("u")
	wd.ran("answer q7 --undo --human")
	wd.click("[ Undo ]")
	wd.ran("answer q7 --undo --human")

	// Settled, it leaves the list and is one of the last answers, which can
	// still be taken back until something has used it.
	wd.clock.Store(int64(5 * time.Second))
	for end := time.Now().Add(3 * time.Second); strings.Contains(wd.screen(), "sign-up page"); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("the settled item is still listed:\n%s", wd.screen())
		}
	}
	wd.key("u")
	wd.ran("answer q7 --undo --human")
	wd.click("[show]")
	wd.shows(" answered: 1", "sign-up page · Must the old sign-up link keep working? → yes", "[ Undo ]")
	wd.s.Key("enter")
	wd.ran("answer q7 --undo --human")
	answered(wd.fx.Now.Add(4 * time.Second))
	wd.shows("used at ")
	wd.s.Key("enter")
	wd.s.Key("esc")
	wd.shows("ACTIONS ")
	time.Sleep(200 * time.Millisecond)
	if n := len(wd.children("answer q7 --undo")); n != 4 {
		t.Errorf("an answer that was used was taken back: %v", wd.children("answer"))
	}
}

// Two letters within three tenths of a second are a sentence meant for the
// lead agent: nothing is answered, and what the first letter did is undone.
func TestABurstOfLettersAnswersNothing(t *testing.T) {
	wd := start(t, actions, 120, 24, nil)
	wd.s.Type("\x1b[I")
	wd.shows("› ▲ sign-up page")
	time.Sleep(keyGap)
	wd.s.Type("yes do that, and stop the rest\r")
	wd.shows(typingSaid)
	time.Sleep(500 * time.Millisecond)
	// The answer and its taking back have ended by now, and what they said
	// has not taken the warning's place.
	if !strings.Contains(wd.last(), typingSaid) {
		t.Fatalf("the warning did not stay: %q", wd.last())
	}
	lines := wd.children("")
	lines = slices.DeleteFunc(lines, func(l string) bool { return strings.HasPrefix(l, "status") || strings.HasPrefix(l, "set here") })
	if got := strings.Join(lines, "\n"); got != "answer q7 --file \"yes\" --human\nanswer q7 --undo --human" && got != "" {
		t.Fatalf("a typed sentence started:\n%s", got)
	}
	// A sentence that begins with the key for the typing line types nothing
	// into it, and its Enter sends nothing.
	time.Sleep(keyGap)
	wd.s.Type("ok then\r")
	time.Sleep(keyGap + 500*time.Millisecond)
	if n := len(wd.children("answer")); n > 2 || strings.Contains(wd.screen(), "other: >") {
		t.Fatalf("a sentence went into the typing line: %v\n%s", wd.children("answer"), wd.screen())
	}
	// Stepping through the list is no sentence, and one key after a pause is a command.
	wd.s.Type("jj")
	wd.shows("› ● price list")
	wd.key("y")
	wd.ran(`answer n4 --file "approve" --human`)
}

func TestAClickJustAfterTheListMovedIsRefused(t *testing.T) {
	wd := start(t, actions, 104, 30, nil)
	x, y, _ := wd.s.Find("[ Approve ]")
	wd.settle()
	wd.change(func(v *contract.View) {
		v.Items = append([]contract.Item{{ID: "n9", Form: "choice", Look: contract.LookNeedsYou, Name: "help pages", Source: "a question from the worker",
			Since: wd.fx.Now, Text: "Drop the old pages?", Buttons: []contract.Button{{Label: "Yes", Key: "y", Answer: "yes"}}}}, v.Items...)
	})
	wd.shows("Drop the old pages?")
	// Where Approve stood there is now another item's button.
	wd.s.Click(x, y)
	wd.shows("the list moved; click again")
	wd.settle()
	if n := len(wd.children("answer")); n != 0 || strings.Contains(wd.screen(), "›") {
		t.Fatalf("the refused click acted: %v\n%s", wd.children("answer"), wd.screen())
	}
	// The same click again acts on what stands there now: the next item's typing line.
	wd.s.Click(x, y)
	wd.shows("› ▲ photo upload")
	wd.click("[ Approve ]")
	wd.ran(`answer n4 --file "approve" --human`)
}

// The pane starts the sweep, a command, every five seconds, and so the age
// line stops saying that nobody is watching.
func TestTheSweepIsAChildEveryFiveSeconds(t *testing.T) {
	wd := start(t, fleet, 60, 30, nil)
	// The time is counted from the first sweep to the second, not from the
	// start of the test: how long the pane had run by then is not known.
	var first time.Time
	for end := time.Now().Add(14 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		n := len(wd.children("status --sweep --quiet --json --human"))
		if n == 1 && first.IsZero() {
			first = time.Now()
		}
		if n >= 2 {
			break
		}
		if time.Now().After(end) {
			t.Fatalf("no two sweeps within fourteen seconds: %v", wd.children(""))
		}
	}
	if first.IsZero() {
		t.Error("two sweeps at once")
	} else if apart := time.Since(first); apart < 4*time.Second {
		t.Errorf("two sweeps %v apart", apart)
	}
	wd.touch()
	for end := time.Now().Add(3 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		wd.mu.Lock()
		in := wd.in
		wd.mu.Unlock()
		if in.Swept.Ran && in.Watched {
			break
		}
		if time.Now().After(end) {
			t.Fatalf("the builder was told %+v, watched %v", in.Swept, in.Watched)
		}
	}
}

// The record's side cut: the pane says so and no longer calls itself live.
func TestTheRecordCutIsSaidAndGoesStale(t *testing.T) {
	wd := start(t, fleet, 60, 30, nil)
	wd.fails.Store(true)
	wd.touch()
	wd.shows("the record cannot be read: the disk is gone", "not checked yet")
	if strings.Contains(wd.last(), "live") {
		t.Errorf("without the record the last line is %q", wd.last())
	}
}

// A change made through the tool is on the screen within 200 ms, 19 times
// out of 20, with twenty agents.
func TestAChangeIsDrawnWithin200ms(t *testing.T) {
	wd := start(t, fleet, 60, 70, nil)
	wd.change(func(v *contract.View) {
		for i := range 8 {
			c := v.Sections[len(v.Sections)-1].Cards[0]
			c.Task, c.Name = fmt.Sprint("X", i), fmt.Sprint("extra ", i)
			v.Sections[len(v.Sections)-1].Cards = append(v.Sections[len(v.Sections)-1].Cards, c)
		}
	})
	wd.shows("extra 7")
	late := 0
	for i := range 20 {
		note := fmt.Sprint("step ", i, " done")
		at := time.Now()
		wd.change(func(v *contract.View) { v.Sections[0].Cards[0].News = note })
		if !wd.within(200*time.Millisecond-time.Since(at), note) {
			late++
			wd.shows(note)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if late > 1 {
		t.Errorf("%d of 20 changes took longer than 200 ms to be drawn", late)
	}
}

// Both panes show what `status` prints, row for row: the same agents and the
// same items, in the same order, each with the same mark, from the same builder.
func TestTheGridEqualsStatusRowForRow(t *testing.T) {
	rows := func(text string, pattern string) []string {
		var out []string
		for _, m := range regexp.MustCompile(pattern).FindAllStringSubmatch(text, -1) {
			out = append(out, m[1]+" "+strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(m[2]), "▲")))
		}
		return out
	}
	for _, kind := range []string{fleet, actions} {
		t.Run(kind, func(t *testing.T) { gridEqualsStatus(t, kind, rows) })
	}
}

func gridEqualsStatus(t *testing.T, kind string, rows func(text, pattern string) []string) {
	{
		wd := startWith(t, kind, 70, 60, nil, func(wd *world) {
			wd.p.make = func(in contract.ViewInput) contract.View {
				wd.mu.Lock()
				defer wd.mu.Unlock()
				wd.in = in
				return *view.Build(in)
			}
		})
		for range 2 {
			wd.touch()
			time.Sleep(300 * time.Millisecond)
			wd.mu.Lock()
			var status strings.Builder
			view.Text(&status, view.Build(wd.in), view.Options{Width: 100})
			wd.mu.Unlock()
			pattern, lines := `(?m)^ (\S) T\d+ +(.{18})`, strings.Join(strings.Split(wd.screen(), "\n")[1:], "\n")
			in := `(?m)^.(\S) (.+?)   +\S`
			if kind == actions {
				pattern, in = `(?m)^ (\S) [qn]\d+ +(.{18})`, `(?m)^ [ ›] (\S) (.+?) · `
			}
			want, got := rows(status.String(), pattern), rows(lines, in)
			if len(want) == 0 || !slices.Equal(got, want) {
				t.Errorf("the %s pane shows\n%v\nstatus prints\n%v", kind, got, want)
			}
			// An item gone from the record: the agent it held up is building again.
			wd.mu.Lock()
			delete(wd.fx.State.Questions, "q7")
			wd.mu.Unlock()
		}
	}
}

func TestTheListOfEveryActionIsAProgramOfItsOwn(t *testing.T) {
	fx, _ := testkit.Load(testkit.Evening)
	s := termtest.New(70, 20)
	k := contract.NewKit()
	k.Platform = system(t.TempDir(), nil)
	p := newPane(menu, s.Term, k, t.TempDir(), "", func() time.Time { return fx.Now })
	left := make(chan bool)
	go func() { left <- p.loop() }()
	if !s.Wait("every action") || !s.Wait("type to search · enter runs it · esc closes") {
		t.Fatalf("ui menu shows %q", s.Row(0))
	}
	s.Type("sto")
	if !s.Wait(" > sto") || !s.Wait("Stop all") || !s.Wait("2 actions") {
		t.Fatalf("typing did not narrow the list: %q, %q", s.Row(1), s.Row(19))
	}
	s.Key("esc")
	select {
	case again := <-left:
		if again {
			t.Error("the list asked to be started again")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the list did not close on Escape")
	}
}

// A pane whose program file was replaced leaves, to be started again from the
// new file, and what was half typed is saved first.
func TestAPaneLeavesWhenItsProgramIsReplaced(t *testing.T) {
	fx, _ := testkit.Load(testkit.Evening)
	home := t.TempDir()
	k := contract.NewKit()
	k.Platform, k.Store = system(home, nil), reader{dir: home, state: &fx.State, reads: new(atomic.Int32), fails: new(atomic.Bool)}
	p := newPane(actions, termtest.New(100, 20).Term, k, home, "", func() time.Time { return fx.Now })
	self, _ := os.Executable()
	p.self, p.make = filepath.Join(home, filepath.Base(self)), func(contract.ViewInput) contract.View { return *fx.Views[contract.Human] }
	if err := os.Link(self, p.self); err != nil {
		data, _ := os.ReadFile(self)
		os.WriteFile(p.self, data, 0o700)
	}
	left := make(chan bool)
	go func() { left <- p.loop() }()
	post := func(ev term.Event) { p.t.Post(ev) }
	post(term.Event{Kind: term.Focus, Focused: true})
	time.Sleep(keyGap)
	post(term.Event{Kind: term.KeyPress, Key: "j", Rune: 'j'})
	time.Sleep(keyGap)
	post(term.Event{Kind: term.KeyPress, Key: "o", Rune: 'o'})
	time.Sleep(keyGap)
	post(term.Event{Kind: term.Paste, Text: "three sizes"})
	time.Sleep(100 * time.Millisecond)
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(p.self, later, later); err != nil {
		t.Fatal(err)
	}
	p.tell(news{do: func() { p.tick(lookEvery) }})
	select {
	case again := <-left:
		if !again {
			t.Fatal("the pane left without asking to be started again")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the pane did not leave when its program file changed")
	}
	data, _ := os.ReadFile(filepath.Join(home, "children.log"))
	if !strings.Contains(string(data), `set draft.q9 --file "three sizes" --human`) {
		t.Errorf("the draft was not saved before the pane left: %q", data)
	}
}

func TestSet(t *testing.T) {
	d := newDesk(t)
	dirs, _ := d.k.Platform.Dirs()
	now := time.Date(2026, 10, 8, 21, 14, 0, 0, time.UTC)
	run := func(args ...string) (string, error) {
		var out strings.Builder
		_, err := set(&contract.Call{Kit: d.k, Command: contract.Find("set"), Args: args, Caller: contract.Caller{Pane: d.me}, Now: now, Out: &out})
		return out.String(), err
	}
	exit := func(err error) int {
		var r *contract.Refusal
		if errors.As(err, &r) {
			return r.Exit
		}
		return -1
	}
	for _, args := range [][]string{{"theme", "paper"}, {"actions", "top"}, {"nudge.sound", "off"}, {"nudge.phone", "off"}, {"mute", "on"},
		{"dnd", "until", "9:00"}, {"fleet.width", "48"}, {"folded", "on"}, {"draft.q7", "half an answer"}, {"draft.q9", "x"}, {"draft.q9"}, {"here", "now"}} {
		if out, err := run(args...); err != nil || (out == "") != (args[0] == "folded" || args[0] == "here" || strings.HasPrefix(args[0], "draft.")) {
			t.Errorf("set %v: %q, %v", args, out, err)
		}
	}
	var ui contract.UIFile
	if err := contract.ReadVersioned(d.k.Platform.Read, filepath.Join(dirs.State, "ui.json"), contract.FileVersion, &ui); err != nil {
		t.Fatal(err)
	}
	until := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	if !ui.Mute || !ui.DND || !ui.DNDUntil.Equal(until) || ui.FleetWidth != 48 || !ui.Folded || !ui.LastHere.Equal(now) ||
		len(ui.Drafts) != 1 || ui.Drafts["q7"] != "half an answer" || ui.Version != contract.FileVersion {
		t.Errorf("ui.json holds %+v", ui)
	}
	cfg := person(d.k, dirs)
	if cfg.UI.Theme != "paper" || cfg.UI.Actions != "top" || cfg.Nudge.Sound || cfg.Nudge.Phone || !cfg.Nudge.Popup || cfg.UI.SettleSeconds != 4 {
		t.Errorf("config.toml holds %+v", cfg)
	}
	if _, err := run("dnd", "off"); err != nil {
		t.Fatal(err)
	}
	var off contract.UIFile
	if contract.ReadVersioned(d.k.Platform.Read, filepath.Join(dirs.State, "ui.json"), contract.FileVersion, &off); off.DND || !off.DNDUntil.IsZero() || !off.Mute {
		t.Errorf("after dnd off ui.json holds %+v", off)
	}
	// A shortcut cannot know which way a switch stands: it says toggle.
	if out, err := run("mute", "toggle"); err != nil || !strings.Contains(out, "mute: off") {
		t.Errorf("set mute toggle: %q, %v", out, err)
	}
	if out, err := run("mute", "toggle"); err != nil || !strings.Contains(out, "mute: on") {
		t.Errorf("set mute toggle, again: %q, %v", out, err)
	}
	for _, args := range [][]string{nil, {"theme", "neon"}, {"actions", "left"}, {"mute", "maybe"}, {"dnd", "until", "soon"}, {"fleet.width", "wide"}, {"volume", "11"}} {
		if _, err := run(args...); exit(err) != contract.ExitUsage {
			t.Errorf("set %v: %v", args, err)
		}
	}
	// The engine's settings come with the engine.
	for _, key := range contract.SettingKeys {
		if _, err := run(key, "x"); err == nil || exit(err) == contract.ExitUsage || !strings.Contains(err.Error(), contract.ErrNotBuilt.Error()) {
			t.Errorf("set %s: %v", key, err)
		}
	}
}

// The keys go from one of ours to the other and back to the conversation
// through the pane beside one in a direction; an action pane on top is
// split off upward; a pane of ours can open the other.
func TestTheKeysGoBetweenThePanes(t *testing.T) {
	d := newDesk(t)
	o, err := d.ui(d.me)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := screen(d.k)
	for _, step := range []struct{ to, want string }{{actions, o.Actions}, {fleet, o.Fleet}, {"", d.me}, {fleet, o.Fleet}, {actions, o.Actions}} {
		if err := hand(d.k.Terms, file, step.to); err != nil || d.focus() != step.want {
			t.Errorf("the keys to %q went to %s, want %s (%v)", step.to, d.focus(), step.want, err)
		}
	}
	c := &contract.Call{Kit: d.k, Command: contract.Find("ui"), Args: []string{actions, "focus"}, Caller: contract.Caller{Pane: d.me}, Out: io.Discard}
	if _, err := ui(c); err != nil || d.focus() != o.Actions {
		t.Errorf("ui actions focus: the keys are in %s (%v)", d.focus(), err)
	}
	// From the fleet, the action pane is closed and opened again beside the conversation.
	d.did()
	if _, err := d.ui(o.Fleet, actions, "off"); err != nil || !strings.Contains(d.did(), "pane-close "+o.Actions) {
		t.Errorf("ui actions off from the fleet: %v", err)
	}
	on, err := d.ui(o.Fleet, actions)
	if got := d.did(); err != nil || on.Actions == "" || !strings.Contains(got, "split "+d.me+" down 0.75") {
		t.Errorf("ui actions from the fleet: %+v, %v\n%s", on, err, got)
	}
	c.Args = []string{actions, "off"}
	ui(c)
	if _, err := ui(&contract.Call{Kit: d.k, Command: contract.Find("ui"), Args: []string{actions, "focus"}, Out: io.Discard}); err == nil {
		t.Error("ui actions focus with the action pane closed did not say so")
	}
	// On top: the pane is split off upward, in one call.
	set(&contract.Call{Kit: d.k, Command: contract.Find("set"), Args: []string{actions, "top"}, Out: io.Discard})
	d.did()
	top, err := d.ui(d.me, actions, "on")
	if got := d.did(); err != nil || !strings.Contains(got, "split "+d.me+" up 0.75") || strings.Contains(got, contract.OpSwap) {
		t.Errorf("ui actions on, on top: %v\n%s", err, got)
	}
	if err := shrink(d.k.Terms, top.Actions, true, true); err != nil || !strings.Contains(d.did(), " up 0.5") {
		t.Errorf("folding a pane on top: %v", err)
	}
	// Asked from the action pane, the fleet opens beside the conversation,
	// the asking pane stays, and the keys are given back to it.
	d.ui(top.Actions, fleet, "off")
	d.did()
	back, err := d.ui(top.Actions, fleet, "on")
	if got := d.did(); err != nil || back.Actions != top.Actions || strings.Contains(got, "pane-close") || !strings.Contains(got, "split "+d.me+" right") {
		t.Errorf("ui fleet on from the action pane: %+v, %v\n%s", back, err, got)
	}
	if snap, _ := d.double.Snapshot(context.Background()); len(snap.Panes) != 3 {
		t.Errorf("panes: %+v", snap.Panes)
	}
}
