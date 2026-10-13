package integrate_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// protectOn switches protected files on in the person's own settings, with
// the list as it comes.
func (r *run) protectOn() {
	r.t.Helper()
	dirs, err := r.k.Platform.Dirs()
	must(r.t, err)
	write(r.t, filepath.Join(dirs.Config, contract.ConfigFile), "[protect]\nfiles = true\n")
}

// collect is the command accept for a task with one commit of the files
// given, and what it printed.
func (r *run) collect(id string, files map[string]string) string {
	r.t.Helper()
	r.task(id, files)
	return play(r.t, r.p.Command(scenario.Orch, "accept", id), 0, id+" is done, checked")
}

// item is the open sign-off for protected files, or nil.
func (r *run) item() *contract.Question {
	s := r.state()
	if p := s.Run.Protect; p != nil && s.Questions[p.Item] != nil && s.Questions[p.Item].State == contract.QuestionOpen {
		return s.Questions[p.Item]
	}
	return nil
}

// yes is the person's approval typed in a tab of their own, and the sweep's
// step that uses it once it has settled.
func (r *run) yes() {
	r.t.Helper()
	id := r.item().ID
	play(r.t, r.p.Command(scenario.Human, "answer", id, "approve"), 0, "answered")
	r.change(func(s *contract.State) {
		if _, ok, err := r.k.Rules.Use(s, id, contract.Now()); !ok || err != nil {
			r.t.Fatalf("the approval of %s was not used: %v", id, err)
		}
	})
}

// refused holds both ways of landing to the refusal, for whoever asks, and
// the base branch to where it stood.
func (r *run) refused(who string, words ...string) {
	r.t.Helper()
	main := r.git(r.root, "rev-parse", "main")
	for _, how := range []string{"--local", "--pr"} {
		play(r.t, r.p.Command(who, "land", how), 5, append(words, "Nothing was landed", "protected files")...)
	}
	if s := r.state(); r.git(r.root, "rev-parse", "main") != main || s.Run.Integration.Landed != "" {
		r.t.Fatalf("a refused landing moved main, or was written down as landed %q", s.Run.Integration.Landed)
	}
}

// With the setting off a protected path is a path like any other: nothing
// is said, asked or written down, and the work lands.
func TestProtectedFilesOffChangeNothing(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n"})
	out := r.collect("A", map[string]string{"deploy/run.sh": "echo go\n", ".env.local": "KEY=1\n"})
	if s := r.state(); strings.Contains(out, "protected") || s.Run.Protect != nil || len(s.Questions) != 0 || strings.Contains(r.kinds(), "protected") {
		t.Fatalf("with the setting off the record has %+v, the events %s:\n%s", s.Run.Protect, r.kinds(), out)
	}
	play(t, r.p.Command(scenario.Human, "land", "--local"), 0, "merged into main")
}

// A settings file that does not read holds a landing, guard on or never
// on: the refusal says so, names the file and what to do, and the same
// landing passes once the file reads again.
func TestLandSaysThatTheSettingsFileDoesNotRead(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n"})
	r.collect("A", map[string]string{"a.txt": "two\n"})
	dirs, err := r.k.Platform.Dirs()
	must(t, err)
	file := filepath.Join(dirs.Config, contract.ConfigFile)
	write(t, file, "[ui\ntheme = \"kelp\"\n")
	play(t, r.p.Command(scenario.Human, "land", "--local", "--json"), 5, "settings_unread")
	play(t, r.p.Command(scenario.Human, "land", "--local"), 5, "Nothing was landed: your settings file "+file+" does not read", "protected files", "mend that line", "whaleshark doctor")
	write(t, file, "[ui]\ntheme = \"kelp\"\n")
	play(t, r.p.Command(scenario.Human, "land", "--local"), 0, "merged into main")
}

// A protected file changed, approved, changed again, landed (20.2): accept
// goes on as ever and says one line, the person is asked, no agent can give
// the yes, and neither way of landing passes without a yes for exactly the
// files as they stand at the tip.
func TestAProtectedFileChangedApprovedChangedAgainLanded(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n", "db/migrations/0001_start.sql": "create table a;\n"})
	r.protectOn()
	// A project adds to the list with the one writer of its file, and
	// nobody has to approve that.
	play(t, r.p.Command(scenario.Human, "set", "protect.also", "legal/**", "--project"), 0)

	if out := r.collect("A", map[string]string{"a.txt": "two\n"}); strings.Contains(out, "protected") || r.state().Run.Protect != nil {
		t.Fatalf("a change to no protected file:\n%s", out)
	}
	const refund = "db/migrations/0042_refunds.sql"
	out := r.collect("B", map[string]string{refund: "create table refunds;\n", "legal/terms.txt": "new terms\n", "b.txt": "b\n"})
	s, q := r.state(), r.item()
	if !strings.Contains(out, "protected files changed: "+refund+", legal/terms.txt · the person is asked") || q == nil ||
		q.Cause != contract.CauseProtect || q.For != contract.ForHuman || !slices.Equal(s.Run.Protect.Files, []string{refund, "legal/terms.txt"}) ||
		s.Run.Protect.Seen == "" || s.Run.Protect.Print != "" || s.Tasks["B"].Status != contract.TaskDone || !strings.Contains(r.kinds(), "protected") {
		t.Fatalf("after B the record has %+v, the item %+v, the events %s:\n%s", s.Run.Protect, q, r.kinds(), out)
	}
	r.refused(scenario.Human, refund)
	// The lead agent cannot give the yes, relayed or not, and is told what
	// to do meanwhile.
	for _, more := range [][]string{nil, {"--relayed"}} {
		play(t, r.p.Command(scenario.Orch, append([]string{"answer", q.ID, "approve"}, more...)...), 5, "the person's own click", "Carry on with the work", "land waits", "whaleshark wait")
	}
	r.refused(scenario.Human)

	r.yes()
	first := r.state().Run.Protect
	if first.Print == "" || first.Print != first.Seen || r.item() != nil {
		t.Fatalf("after the yes the record has %+v", first)
	}
	// Changed again after the yes: the yes was for the file as it was.
	out = r.collect("C", map[string]string{refund: "create table refunds (id int);\n"})
	if now := r.state().Run.Protect; !strings.Contains(out, "the person is asked") || now.Seen == first.Print || now.Print != first.Print || r.item() == nil {
		t.Fatalf("after C the record has %+v:\n%s", now, out)
	}
	r.refused(scenario.Human, refund)
	// Where the project lets the orchestrator land, it is refused the same.
	r.approve("[land]\nwho = \"orchestrator\"\n\n[protect]\nalso = [\"legal/**\"]\n")
	r.refused(scenario.Orch, "the person has not approved")
	// A task that touches nothing protected asks nothing new, and the open
	// question stays.
	if out := r.collect("D", map[string]string{"d.txt": "d\n"}); strings.Contains(out, "protected") || r.item() == nil {
		t.Fatalf("D changed no protected file:\n%s", out)
	}
	r.yes()
	play(t, r.p.Command(scenario.Human, "land", "--local"), 0, "merged into main")
	if main := r.git(r.root, "rev-parse", "main"); main != r.branch() || r.state().Run.Integration.Landed != main {
		t.Fatalf("after the second yes main is at %s and the work at %s", main, r.branch())
	}
}

// One put back as it was: the question closes by itself and the work lands
// with no yes. What came in from the base branch is not the job's change.
func TestAProtectedFilePutBackAsksNothing(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n", ".env.example": "KEY=\n"})
	r.protectOn()
	r.collect("A", map[string]string{".env.example": "KEY=\nMORE=\n"})
	if r.item() == nil {
		t.Fatal("a changed .env.example asked nobody")
	}
	out := r.collect("B", map[string]string{".env.example": "KEY=\n", "a.txt": "two\n"})
	if s := r.state(); strings.Contains(out, "protected") || r.item() != nil || s.Run.Protect.Seen != "" || len(s.Run.Protect.Files) != 0 {
		t.Fatalf("put back, the record has %+v:\n%s", s.Run.Protect, out)
	}
	// Somebody else changes a protected file on main, and a worker brings
	// the collected work up to date with it.
	r.git(r.root, "checkout", "-q", "main")
	r.git(r.root, "commit", "-q", "--allow-empty", "-m", "nothing")
	write(t, filepath.Join(r.root, "deploy", "run.sh"), "echo theirs\n")
	r.git(r.root, "add", "deploy")
	r.git(r.root, "commit", "-q", "-m", "somebody else's deploy")
	dir := r.task("sync-main", nil)
	r.git(dir, "merge", "-q", "--no-edit", "main")
	if out := play(t, r.p.Command(scenario.Orch, "accept", "sync-main"), 0); strings.Contains(out, "protected") || r.item() != nil {
		t.Fatalf("main's own change was taken for the job's:\n%s", out)
	}
	play(t, r.p.Command(scenario.Human, "land", "--local"), 0, "merged into main")
}

// A group's accept in Fleet Mode: the look is at the orchestrator's accept,
// on the job's own branch, and the question names the group, whose fleet
// every screen titles it with. A leader's accept inside its fleet looks at
// nothing.
func TestAGroupsAcceptAsksAboutProtectedFiles(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n"})
	r.protectOn()
	billing := r.fleet("billing", "r2")
	write(t, filepath.Join(billing, "db", "migrations", "0042_refunds.sql"), "create table refunds;\n")
	r.git(billing, "add", "db")
	r.git(billing, "commit", "-q", "-m", "T1: the refund flow")
	// Inside the fleet: its run has a parent, and its branch is not what lands.
	low, err := r.k.Store.Read(r.root, "r2")
	must(t, err)
	if seen, files, err := r.k.Integrator.Protected(r.root, low, []string{"**/migrations/**"}); err != nil || seen == "" || r.k.Rules.Protect(low, "T1", seen, files, contract.Now()) || low.Run.Protect != nil {
		t.Fatalf("in the fleet's own run: %q %v %v, written down %+v", seen, files, err, low.Run.Protect)
	}

	r.group("G1", "billing")
	out := play(t, r.p.Command(scenario.Orch, "accept", "G1"), 0, "G1 is done, checked")
	s, q := r.state(), r.item()
	if !strings.Contains(out, "protected files changed: db/migrations/0042_refunds.sql · the person is asked") || q == nil || q.Task != "G1" || s.Tasks[q.Task].Fleet != "billing" {
		t.Fatalf("after the group the item is %+v:\n%s", q, out)
	}
	r.refused(scenario.Human, "db/migrations/0042_refunds.sql")
	r.yes()
	play(t, r.p.Command(scenario.Human, "land", "--local"), 0, "merged into main")
}

// The fingerprint is of exact contents: the same files with the same
// contents give the same one, any other contents, another file or a file
// taken away give another, and a pattern is only ever matched, never run.
func TestTheFingerprintIsOfExactContents(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n", "deploy/old.sh": "old\n"})
	print := func(patterns ...string) (string, []string) {
		t.Helper()
		seen, files, err := r.k.Integrator.Protected(r.root, r.state(), patterns)
		must(t, err)
		return seen, files
	}
	list := []string{"deploy/**", ".env*", "--output=x", "$(touch gone)", "-z"}
	if seen, files := print(list...); seen != "" || files != nil {
		t.Fatalf("nothing changed, and yet %q %v", seen, files)
	}
	_, err := r.accept(r.name("A", map[string]string{"deploy/run.sh": "one\n", "sub/.env.live": "K=1\n", "notes.txt": "n\n"}), nil)
	must(t, err)
	one, files := print(list...)
	if again, _ := print(list...); one == "" || again != one || !slices.Equal(files, []string{"deploy/run.sh", "sub/.env.live"}) {
		t.Fatalf("the fingerprint %q then %q of %v", one, again, files)
	}
	if none, _ := print(); none != "" {
		t.Fatal("with no list there is a fingerprint")
	}
	_, err = r.accept(r.name("B", map[string]string{"deploy/run.sh": "two\n"}), nil)
	must(t, err)
	two, _ := print(list...)
	dir := r.task("C", nil)
	r.git(dir, "rm", "-q", "deploy/old.sh")
	r.git(dir, "commit", "-q", "-m", "the old script goes")
	_, err = r.accept("C", nil)
	must(t, err)
	three, files := print(list...)
	if two == one || three == two || !slices.Equal(files, []string{"deploy/old.sh", "deploy/run.sh", "sub/.env.live"}) {
		t.Fatalf("the fingerprints %q %q %q of %v", one, two, three, files)
	}
	if has(r.root, "gone") || has(r.root, "x") {
		t.Fatal("a pattern was run, or handed to git as an option")
	}
}

// name is task, answering the id.
func (r *run) name(id string, files map[string]string) string {
	r.task(id, files)
	return id
}

// Switched on after the work was collected: no accept has asked, so the
// refused landing asks the person itself, once, and passes after the yes.
func TestLandAsksWhereNoAcceptHas(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n"})
	r.collect("A", map[string]string{"deploy/run.sh": "echo go\n"})
	r.protectOn()
	if r.item() != nil {
		t.Fatal("an item before anybody looked")
	}
	r.refused(scenario.Human, "deploy/run.sh")
	s, q := r.state(), r.item()
	if q == nil || q.Cause != contract.CauseProtect || strings.Count(r.kinds(), "protected") != 1 || len(s.Questions) != 1 {
		t.Fatalf("after two refused landings the item is %+v, the events %s, %d items", q, r.kinds(), len(s.Questions))
	}
	r.yes()
	play(t, r.p.Command(scenario.Human, "land", "--local"), 0, "merged into main")
}

// Several accepted together: the look is after each collect, so the one
// line and the event name the task that brought the protected file.
func TestSeveralAcceptedTogetherAskOnce(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n"})
	r.protectOn()
	r.approve("[land]\ncheck = \"exit 0\"\n")
	r.task("A", map[string]string{"a.txt": "two\n"})
	r.task("B", map[string]string{"deploy/run.sh": "echo go\n"})
	r.task("C", map[string]string{"c.txt": "c\n"})
	out := play(t, r.p.Command(scenario.Orch, "accept", "A", "B", "C"), 0, "A is done", "B is done", "C is done")
	q := r.item()
	if strings.Count(out, "protected files changed: deploy/run.sh") != 1 || q == nil || q.Task != "B" || strings.Count(r.kinds(), "protected") != 1 ||
		strings.Index(out, "protected files changed") < strings.Index(out, "B is done") || strings.Index(out, "protected files changed") > strings.Index(out, "C is done") {
		t.Fatalf("after the three the item is %+v, the events %s:\n%s", q, r.kinds(), out)
	}
	r.refused(scenario.Human, "deploy/run.sh")
}
