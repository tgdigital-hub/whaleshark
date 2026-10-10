package windows

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/guide"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// What a worker may say, by what a shell could take for its own.
var said = []struct{ name, text string }{
	{"plain", "half done"},
	{"signs", `50% of $HOME & more | <in> ^up (x) ; !y! ~ * ? back\slash 'single' ` + "`tick`"},
	{"letters", "café señor — ünï ✓"},
	{"quotes", `she said "done" twice`},
}

// note is what the record holds as the worker's last word.
func note(p *scenario.Project) string {
	s, _ := p.Record()
	if at := s.Attempts[worker].Progress; at != nil {
		return at.Note
	}
	return ""
}

// TestWorkerCommandsThroughEveryShell types a worker's command into every
// shell of this machine, spelled for that shell by Quote, and reads what
// reached the record. Every text must arrive as it was written, but one:
// the PowerShell that comes with Windows hands a program no double quote
// inside an argument, and the text arrives without them, which is why free
// text can come from a file. That way must work in every shell. On a hosted
// Windows machine all four shells must be there.
func TestWorkerCommandsThroughEveryShell(t *testing.T) {
	p := evening(t)
	if p.Kit.Platform.System() == "windows" && len(shells(p.Kit)) < 4 {
		missing(t, "cmd, both PowerShells and git's bash (one of them)")
	}
	bin := strings.TrimPrefix(firstOf(p.Command(worker, "mail").Env, contract.EnvBin), contract.EnvBin+"=")
	for _, sh := range shells(p.Kit) {
		for i, s := range said {
			line := p.Kit.Platform.Quote(sh.dialect, []string{bin, "progress", "4" + string(rune('0'+i)), s.text})
			out, err := typed(p, sh, line)
			got := note(p)
			switch {
			case got == s.text:
				t.Logf("FACT %s, %s: arrived as written", sh.name, s.name)
			case sh.name == "powershell" && got == strings.ReplaceAll(s.text, `"`, ""):
				t.Logf("FACT %s, %s: arrived without its double quotes (the limit Quote names)", sh.name, s.name)
			default:
				t.Errorf("%s, %s: the record holds %q, not %q\nline: %s\n%v %s", sh.name, s.name, got, s.text, line, err, out)
			}
		}
		file := filepath.Join(t.TempDir(), "note.txt")
		text := said[1].text + " " + said[3].text + " " + said[2].text
		if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		line := p.Kit.Platform.Quote(sh.dialect, []string{bin, "progress", "50", "--file", file})
		if out, err := typed(p, sh, line); note(p) != text {
			t.Errorf("%s, from a file: the record holds %q, not %q\nline: %s\n%v %s", sh.name, note(p), text, line, err, out)
		}
		if out, err := typed(p, sh, p.Kit.Platform.Quote(sh.dialect, []string{bin, "mail"})); err != nil {
			t.Errorf("%s: mail: %v\n%s", sh.name, err, out)
		}
	}
}

func firstOf(env []string, name string) string {
	for _, kv := range env {
		if strings.HasPrefix(kv, name+"=") {
			return kv
		}
	}
	return ""
}

// TestThePromptsOwnLineInEveryShell types the line a worker's prompt gives
// for progress, as the prompt spells it on this system, into every shell.
// It must work in the shell the prompt is written for: PowerShell on
// Windows, the POSIX shell elsewhere. On Windows the other two shells must
// refuse it, cmd and git's bash, since the line begins with PowerShell's
// call sign: an agent whose tool runs one of those cannot use the line as
// it stands, and the day that changes this test must say so.
func TestThePromptsOwnLineInEveryShell(t *testing.T) {
	p := evening(t)
	bin := strings.TrimPrefix(firstOf(p.Command(worker, "mail").Env, contract.EnvBin), contract.EnvBin+"=")
	prompt := guide.Prompt{Bin: p.Kit.Platform.Quote("", []string{bin})}.Text()
	var line string
	for _, l := range strings.Split(prompt, "\n") {
		if strings.Contains(l, " progress 40 ") {
			line = strings.Replace(l, "<what is done so far>", "the prompt's own line", 1)
		}
	}
	if line == "" {
		t.Fatalf("the prompt has no line for progress:\n%s", prompt)
	}
	written := contract.ShellPosix
	if p.Kit.Platform.System() == "windows" {
		written = contract.ShellPowerShell
	}
	for i, sh := range shells(p.Kit) {
		// Each shell is asked for a number of its own, so that one's success
		// is not read as the next one's.
		asked := strings.Replace(line, " 40 ", " 6"+string(rune('0'+i))+" ", 1)
		out, err := typed(p, sh, asked)
		s, _ := p.Record()
		ok := s.Attempts[worker].Progress.Pct == 60+i && note(p) == "the prompt's own line"
		t.Logf("FACT the prompt's line in %s: works=%v", sh.name, ok)
		if !ok && sh.dialect == written {
			t.Errorf("%s is the shell the prompt is written for, and its line did not work there\nline: %s\n%v %s", sh.name, asked, err, out)
		}
		if ok && written == contract.ShellPowerShell && sh.dialect != written {
			t.Errorf("%s took the line written for PowerShell: the page of facts says it does not\nline: %s", sh.name, asked)
		}
	}
}
