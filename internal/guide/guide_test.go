package guide

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// phase is the phase these texts are written for: they name nothing later.
const phase = 1

func fullPrompt() Prompt {
	return Prompt{
		Bin: "whaleshark", Run: "r3", Task: "T3", Name: "url parser",
		Dir: "/work/app", Brief: "/work/app/briefs/T3.md", Result: "/work/app/result.md",
		Owns: []string{"src/url/**", "docs/url.md"}, Shared: true,
		Decisions: []contract.Decision{{Question: "Which base branch?", Answer: "main", At: time.Unix(0, 0)}},
		Sync:      "You are four commits behind.",
		Browser:   "Save screenshots as .png files.",
	}
}

// texts is everything an agent is given to read, with who reads it.
func texts() map[string]struct {
	text   string
	reader contract.CallerKind
} {
	type t = struct {
		text   string
		reader contract.CallerKind
	}
	return map[string]t{
		"lead guide":    {Lead, contract.Orchestrator},
		"worker guide":  {Worker, contract.Worker},
		"worker prompt": {fullPrompt().Text(), contract.Worker},
		"bare prompt":   {Prompt{Bin: "whaleshark"}.Text(), contract.Worker},
		"rules block":   {Block, contract.Orchestrator},
		"first message": {FirstMessage, contract.Orchestrator},
	}
}

var (
	quoted   = regexp.MustCompile("`([^`\n]+)`")
	flagWord = regexp.MustCompile(`--[a-z][a-z-]*`)
)

// spans returns every command line a text shows: what stands between
// backticks, and every line that is a command by itself.
func spans(text string) []string {
	var out []string
	for _, m := range quoted.FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	for line := range strings.Lines(text) {
		if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "whaleshark ") {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

func findFlag(list []contract.Flag, name string) *contract.Flag {
	for i := range list {
		if list[i].Name == name {
			return &list[i]
		}
	}
	return nil
}

// named checks the spans of a text against the command table and returns the
// commands it names.
func named(t *testing.T, what, text string, reader contract.CallerKind) []string {
	t.Helper()
	var names []string
	for _, span := range spans(text) {
		rest, prefixed := strings.CutPrefix(span, "whaleshark ")
		words := strings.Fields(rest)
		if strings.HasPrefix(rest, "--") {
			name := strings.TrimPrefix(words[0], "--")
			if name == "human" {
				continue // TestHumanOnlyForbidden
			}
			known := findFlag(contract.CommonFlags, name) != nil
			for _, c := range contract.Commands {
				f := findFlag(c.Flags, name)
				known = known || f != nil && c.Phase <= phase && f.Phase <= phase && c.May(reader, "")
			}
			if !known {
				t.Errorf("%s: `%s`: no command this reader may run now takes that flag", what, span)
			}
			continue
		}
		c := contract.Find(words[0])
		if c == nil {
			if prefixed {
				t.Errorf("%s: `%s`: no such command", what, span)
			}
			continue
		}
		names = append(names, c.Name)
		sub := ""
		if len(words) > 1 {
			sub = words[1]
		}
		if c.Phase > phase {
			t.Errorf("%s: `%s`: built in phase %d", what, span, c.Phase)
		}
		if !c.May(reader, sub) {
			t.Errorf("%s: `%s`: not allowed for the %s", what, span, reader)
		}
		for _, s := range c.Subs {
			if s.Name == sub && s.Phase > phase {
				t.Errorf("%s: `%s`: that form is built in phase %d", what, span, s.Phase)
			}
		}
		for _, f := range flagWord.FindAllString(rest, -1) {
			flag := findFlag(c.Flags, f[2:])
			if flag == nil && f != "--human" {
				flag = findFlag(contract.CommonFlags, f[2:])
			}
			switch {
			case flag == nil:
				t.Errorf("%s: `%s`: %s does not take %s", what, span, c.Name, f)
			case flag.Phase > phase:
				t.Errorf("%s: `%s`: %s is built in phase %d", what, span, f, flag.Phase)
			case flag.Who == contract.H:
				t.Errorf("%s: `%s`: %s is the human's alone", what, span, f)
			}
		}
	}
	return names
}

func TestEveryCommandNamedExistsAndIsAllowed(t *testing.T) {
	got := map[string][]string{}
	for what, x := range texts() {
		got[what] = named(t, what, x.text, x.reader)
	}
	// The visitor the block describes reads with status and the guide.
	for _, name := range []string{"status", "guide"} {
		if !contract.Find(name).May(contract.Unbound, "") {
			t.Errorf("rules block: a visitor may not run %s", name)
		}
	}
	// Nothing a reader needs is left out.
	for _, c := range contract.Commands {
		if c.Phase > phase {
			continue
		}
		if c.Section == "runs" && c.Who&contract.O != 0 && !slices.Contains(got["lead guide"], c.Name) {
			t.Errorf("the lead guide does not name %s", c.Name)
		}
		if c.Section == "worker" {
			for _, what := range []string{"worker guide", "worker prompt", "bare prompt"} {
				if !slices.Contains(got[what], c.Name) {
					t.Errorf("the %s does not name %s", what, c.Name)
				}
			}
		}
	}
	for _, name := range []string{"status", "guide", "task", "start", "need", "accept", "wait"} {
		if !slices.Contains(got["rules block"], name) {
			t.Errorf("the rules block does not name %s", name)
		}
	}
}

func TestHumanOnlyForbidden(t *testing.T) {
	forbids := regexp.MustCompile("(?i)\\bnever add `--human`")
	for what, x := range texts() {
		if what != "first message" && !strings.Contains(x.text, "--human") {
			t.Errorf("%s does not forbid --human", what)
		}
		for line := range strings.Lines(x.text) {
			if n := strings.Count(line, "--human"); n > 0 && n != len(forbids.FindAllString(line, -1)) {
				t.Errorf("%s names --human other than to forbid it: %q", what, line)
			}
		}
	}
}

func TestLeadGuideUnder90Lines(t *testing.T) {
	if n := strings.Count(Lead, "\n"); n >= 90 {
		t.Errorf("the lead guide has %d lines", n)
	}
	for _, kind := range []string{"done", "failed", "question", "exited", "start_failed", "blocked",
		"quiet", "check_failed", "stuck", "human", "answered", "paused", "resumed"} {
		if !slices.Contains(contract.EventKinds, kind) {
			t.Errorf("%s is not an event kind", kind)
		}
		if !strings.Contains(Lead, "`"+kind+"`") {
			t.Errorf("the lead guide does not say what to do on %s", kind)
		}
	}
	for _, heading := range []string{"Target", "Change", "Constraints", "Ownership", "Acceptance"} {
		if !strings.Contains(Lead, heading) {
			t.Errorf("the lead guide does not name the brief's heading %s", heading)
		}
	}
}

func TestBlockHasMarkersAndNoVersion(t *testing.T) {
	lines := strings.Split(strings.TrimSuffix(Block, "\n"), "\n")
	if !strings.HasPrefix(lines[0], beginMark) || !strings.HasPrefix(lines[len(lines)-1], endMark) {
		t.Error("the block does not begin and end with its marker lines")
	}
	if !strings.HasSuffix(Block, "-->\n") || strings.Contains(Block, "\r") {
		t.Error("the block must end with its last marker and one line break")
	}
	if regexp.MustCompile(`(?i)version|\bv?\d+\.\d+`).MatchString(Block) {
		t.Error("the block carries a version")
	}
	if !strings.Contains(Block, "`whaleshark guide lead`") {
		t.Error("the block does not point at the guide")
	}
}

func TestPromptSlots(t *testing.T) {
	bare := Prompt{Bin: "/opt/ws/whaleshark", Run: "r3", Task: "T3", Name: "url parser", Dir: "/work/app"}.Text()
	for _, absent := range []string{"## ", "You may change only", "Other workers", "\n\n\n"} {
		if strings.Contains(bare, absent) {
			t.Errorf("an empty slot left %q behind", absent)
		}
	}
	for _, want := range []string{
		`task T3 "url parser" (run r3)`, "Work in:      /work/app ",
		"/opt/ws/whaleshark report done", contract.PausedReason,
	} {
		if !strings.Contains(bare, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	full := fullPrompt().Text()
	order := []string{
		"You may change only:  src/url/**, docs/url.md\n", "Other workers are working",
		"\n## Decisions\n", "- Asked: Which base branch?\n  Decided: main\n",
		"\n## Sync\nYou are four commits behind.\n", "\n## Browser check\nSave screenshots as .png files.\n",
		"\nwhaleshark progress 40",
	}
	at := 0
	for _, want := range order {
		i := strings.Index(full[at:], want)
		if i < 0 {
			t.Fatalf("the prompt lacks %q after byte %d:\n%s", want, at, full)
		}
		at += i
	}
	if strings.Contains(full, "\n\n\n") {
		t.Error("the prompt has two empty lines in a row")
	}
}

func TestGuideCommand(t *testing.T) {
	k := contract.NewKit()
	Plug(k)
	for _, c := range []struct {
		caller contract.CallerKind
		args   []string
		want   string
	}{
		{contract.Orchestrator, nil, Lead}, {contract.Unbound, nil, Lead}, {contract.Worker, nil, Worker},
		{contract.Worker, []string{"lead"}, Lead}, {contract.Human, []string{"worker"}, Worker},
		{contract.Human, []string{"both"}, ""}, {contract.Human, []string{"lead", "worker"}, ""},
	} {
		var out bytes.Buffer
		result, err := k.Handler("guide")(&contract.Call{Kit: k, Args: c.args, Caller: contract.Caller{Kind: c.caller}, Out: &out})
		if c.want == "" {
			var r *contract.Refusal
			if !errors.As(err, &r) || r.Exit != contract.ExitUsage || out.Len() > 0 {
				t.Errorf("guide %v: want a usage error and no output, got %v", c.args, err)
			}
			continue
		}
		if err != nil || out.String() != c.want || result.(map[string]string)["text"] != c.want {
			t.Errorf("guide %v as %s printed the wrong guide (%v)", c.args, c.caller, err)
		}
	}
}

func TestFiles(t *testing.T) {
	agent := regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	seen := map[string]bool{}
	for _, f := range Files {
		if !agent.MatchString(f.Kind) || seen[f.Kind] || f.Dir == "" || f.Name == "" {
			t.Errorf("bad row %+v", f)
		}
		if f.Confirmed != (f.Source != "") {
			t.Errorf("%s: a confirmed row names its source, and only a confirmed row", f.Kind)
		}
		seen[f.Kind] = true
	}
	none := func(string) string { return "" }
	for kind, want := range map[string]string{"claude": "/people/ana/.claude/CLAUDE.md", "codex": "/people/ana/.codex/AGENTS.md"} {
		if got, ok := File(kind, "/people/ana", none); !ok || got != filepath.FromSlash(want) {
			t.Errorf("File(%s) = %q, %v", kind, got, ok)
		}
	}
	env := func(name string) string { return map[string]string{"CODEX_HOME": "/srv/codex"}[name] }
	if got, _ := File("codex", "/people/ana", env); got != filepath.FromSlash("/srv/codex/AGENTS.md") {
		t.Errorf("File(codex) with its variable set = %q", got)
	}
	if got, ok := File("nosuch", "/people/ana", none); ok || got != "" {
		t.Errorf("File(nosuch) = %q, %v", got, ok)
	}
}

// renamer is the platform stand-in with a working Replace.
type renamer struct{ contract.NoPlatform }

func (renamer) Replace(tmp, final string) error { return os.Rename(tmp, final) }

func TestBlockWrittenAndRemovedLeavesTheFileAsItWas(t *testing.T) {
	for name, content := range map[string]string{
		"ends with a line break": "# My rules\n\nBe brief.\n",
		"no line break at end":   "# My rules\n\nBe brief.",
		"empty file":             "",
		"only line breaks":       "\n\n",
		"windows line ends":      "# My rules\r\n\r\nBe brief.\r\n",
		"not text":               "\x00\xff\xfe binary \x1b[31m",
		"another marked block":   "<!-- other:begin -->\nx\n<!-- other:end -->\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "CLAUDE.md")
			if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
				t.Fatal(err)
			}
			was, _ := os.Stat(path) // 0640 where the system keeps such bits
			before, created, err := Write(renamer{}, path)
			if err != nil || before != Absent || created {
				t.Fatalf("Write = %v, %v, %v", before, created, err)
			}
			if got, _ := os.ReadFile(path); !strings.HasPrefix(string(got), content) || !strings.HasSuffix(string(got), Block) {
				t.Fatalf("after Write the file is %q", got)
			}
			if s, _ := Look(path); s != Ours {
				t.Errorf("Look after Write = %v", s)
			}
			if again, _, err := Write(renamer{}, path); err != nil || again != Ours {
				t.Errorf("a second Write = %v, %v", again, err)
			}
			if before, err := Remove(renamer{}, path, false); err != nil || before != Ours {
				t.Fatalf("Remove = %v, %v", before, err)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != content {
				t.Errorf("after Remove the file is %q, want %q (%v)", got, content, err)
			}
			if info, _ := os.Stat(path); info.Mode().Perm() != was.Mode().Perm() {
				t.Errorf("the file's permissions became %v", info.Mode().Perm())
			}
			if left, _ := os.ReadDir(filepath.Dir(path)); len(left) != 1 {
				t.Errorf("%d files left in the folder", len(left))
			}
			if before, err := Remove(renamer{}, path, false); err != nil || before != Absent {
				t.Errorf("a second Remove = %v, %v", before, err)
			}
		})
	}
}

func TestBlockInAFileOfItsOwn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tool", "AGENTS.md")
	if s, err := Look(path); err != nil || s != Absent {
		t.Fatalf("Look at no file = %v, %v", s, err)
	}
	if before, created, err := Write(renamer{}, path); err != nil || before != Absent || !created {
		t.Fatalf("Write = %v, %v, %v", before, created, err)
	}
	if got, _ := os.ReadFile(path); string(got) != Block {
		t.Fatalf("the new file is %q", got)
	}
	// Text the person added afterwards keeps the file.
	if err := os.WriteFile(path, []byte(Block+"Mine.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(renamer{}, path, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "Mine.\n" {
		t.Fatalf("the person's text became %q", got)
	}
	// A file init made and nothing else is in is deleted.
	Write(renamer{}, path)
	os.WriteFile(path, []byte(Block), 0o600)
	if before, err := Remove(renamer{}, path, true); err != nil || before != Ours {
		t.Fatalf("Remove = %v, %v", before, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the file init made is still there (%v)", err)
	}
	// A file that was there before is kept, even when empty.
	os.WriteFile(path, nil, 0o600)
	Write(renamer{}, path)
	Remove(renamer{}, path, false)
	if got, err := os.ReadFile(path); err != nil || len(got) != 0 {
		t.Errorf("the person's empty file: %q, %v", got, err)
	}
}

func TestEditedBlockIsLeftAloneAndReported(t *testing.T) {
	for name, edit := range map[string]func(string) string{
		"a rule changed":      func(b string) string { return strings.Replace(b, "three words", "five words", 1) },
		"a line added":        func(b string) string { return strings.Replace(b, endMark, "7. Be kind.\n"+endMark, 1) },
		"end marker gone":     func(b string) string { return b[:strings.Index(b, endMark)] },
		"begin marker gone":   func(b string) string { return b[strings.Index(b, "\n")+1:] },
		"windows line ends":   func(b string) string { return strings.ReplaceAll(b, "\n", "\r\n") },
		"pasted inside a row": func(b string) string { return "> " + b },
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "CLAUDE.md")
			content := "# Mine\n\n" + edit(Block) + "After.\n"
			os.WriteFile(path, []byte(content), 0o600)
			if s, err := Look(path); err != nil || s != Edited {
				t.Errorf("Look = %v, %v", s, err)
			}
			if before, created, err := Write(renamer{}, path); err != nil || before != Edited || created {
				t.Errorf("Write = %v, %v, %v", before, created, err)
			}
			if before, err := Remove(renamer{}, path, true); err != nil || before != Edited {
				t.Errorf("Remove = %v, %v", before, err)
			}
			if got, _ := os.ReadFile(path); string(got) != content {
				t.Errorf("an edited block was touched: %q", got)
			}
		})
	}
}

func TestBlockThroughALink(t *testing.T) {
	dir := t.TempDir()
	real, link := filepath.Join(dir, "dotfiles.md"), filepath.Join(dir, "CLAUDE.md")
	os.WriteFile(real, []byte("Mine.\n"), 0o600)
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	if _, _, err := Write(renamer{}, link); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Lstat(link); info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced by a file")
	}
	if got, _ := os.ReadFile(real); string(got) != "Mine.\n\n"+Block {
		t.Fatalf("the linked file is %q", got)
	}
	Remove(renamer{}, link, false)
	if got, _ := os.ReadFile(real); string(got) != "Mine.\n" {
		t.Errorf("the linked file became %q", got)
	}
}

func TestFailedReplaceLeavesNothingBehind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	os.WriteFile(path, []byte("Mine.\n"), 0o600)
	if _, _, err := Write(contract.NoPlatform{}, path); !errors.Is(err, contract.ErrNotBuilt) {
		t.Fatalf("Write = %v", err)
	}
	if left, _ := os.ReadDir(filepath.Dir(path)); len(left) != 1 {
		t.Errorf("%d files left in the folder", len(left))
	}
	if got, _ := os.ReadFile(path); string(got) != "Mine.\n" {
		t.Errorf("the file became %q", got)
	}
}
