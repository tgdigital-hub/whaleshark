package windows

import (
	"os"
	"path/filepath"
	"strconv"
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
	{"quotes and signs", `a "b" & c | d > e ^ 100% (f) !g! \"h\" <i> end\`},
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
// reached the record: what arrives must be what was meant, sign for sign,
// which is more than the line running. One limit stands, and is Windows'
// own: the PowerShell that comes with Windows hands a program no double
// quote inside an argument, so there a text with them arrives without
// them (or, with a backslash before one, in pieces); that is why free
// text can come from a file, which must work in every shell. On a hosted
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
			before := note(p)
			out, err := typed(p, sh, line)
			got := note(p)
			switch {
			case got == s.text:
				t.Logf("FACT %s, %s: arrived as written", sh.name, s.name)
			case sh.name == "powershell" && got == strings.ReplaceAll(s.text, `"`, ""):
				t.Logf("FACT %s, %s: arrived without its double quotes (the limit Quote names)", sh.name, s.name)
			case sh.name == "powershell" && i == 4:
				t.Logf("FACT %s, %s: the record holds %q (%q before): %v %s", sh.name, s.name, got, before, err, out)
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
// for progress, as the prompt spells it on this system, into every shell
// of this machine, and it must work in each: nobody knows which shell an
// agent's tool types into, so on Windows the one line is written for cmd,
// both PowerShells and git's bash alike. As one side of a pipe, which an
// agent is free to make of it, it must work in every shell but cmd, which
// reads no path with forward slashes there: that limit is held here as it
// is. The same holds for a line the tool hands on with words of its own. Elsewhere the line
// is written for the POSIX shell, and a PowerShell there is not asked for
// more than the plain line.
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
	t.Logf("FACT the prompt's line: %s", line)
	if p.Kit.Platform.System() == "windows" && strings.HasPrefix(line, "&") {
		t.Errorf("the program's path, %s, has no spelling every shell reads, so the line is PowerShell's alone: %s", bin, line)
	}
	windows := p.Kit.Platform.System() == "windows"
	pct := func() int { s, _ := p.Record(); return s.Attempts[worker].Progress.Pct }
	for i, sh := range shells(p.Kit) {
		// Each shell is asked for a number of its own, so that one's success
		// is not read as the next one's.
		asked := strings.Replace(line, " 40 ", " 6"+string(rune('0'+i))+" ", 1)
		out, err := typed(p, sh, asked)
		if pct() != 60+i || note(p) != "the prompt's own line" {
			t.Errorf("the prompt's line did not work in %s\nline: %s\n%v %s", sh.name, asked, err, out)
		}
		if !windows && sh.dialect != contract.ShellPosix {
			continue
		}
		piped := strings.Replace(line, " 40 ", " 8"+string(rune('0'+i))+" ", 1) + " | sort"
		if out, err := typed(p, sh, piped); (pct() == 80+i) == (sh.name == "cmd") {
			t.Errorf("the prompt's line as one side of a pipe in %s: works=%v, and the page of facts says it works in every shell but cmd\nline: %s\n%v %s", sh.name, pct() == 80+i, piped, err, out)
		}
		text := "a line (handed on); with 'words' & <signs> of " + sh.name
		handed := p.Kit.Platform.Quote("", []string{bin, "progress", "7" + string(rune('0'+i)), text})
		if out, err := typed(p, sh, handed); note(p) != text {
			t.Errorf("a line the tool hands on did not work in %s: the record holds %q\nline: %s\n%v %s", sh.name, note(p), handed, err, out)
		}
		// What each way of writing the program's path does in this shell,
		// alone and before a pipe: the facts the one spelling was chosen by.
		for j, way := range []string{strings.ReplaceAll(bin, `\`, "/"), strings.ReplaceAll(bin, `\`, `\\`), bin, `"` + bin + `"`} {
			for k, pipe := range []string{"", " | sort"} {
				n := 20 + 2*j + k
				typed(p, sh, way+" progress "+strconv.Itoa(n)+" facts"+pipe)
				if windows {
					t.Logf("FACT in %s, %s progress ...%s: works=%v", sh.name, strings.Replace(way, bin[:max(strings.LastIndexAny(bin, `\/`), 0)], "<folder>", 1), pipe, pct() == n)
				}
			}
		}
	}
}
