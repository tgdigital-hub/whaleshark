package runtask_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
)

// A file's name comes from a repository and may hold a line break, the
// sequences that retitle or recolour a terminal, or a leading dash. Wherever
// two tasks' files are shown, such a name begins no line of its own and
// steers no terminal; in a diff and in a sync brief git and the brief quote it.
func TestAFileNameBeginsNoLineOfItsOwn(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("no file's name holds a line break there")
	}
	quiet := "write-result | report done \"in\""
	h := open(t, map[string]string{"A.1": quiet, "B.1": quiet})
	h.add(map[string]string{"A": "none", "B": "none"}, "A", "B")
	h.reported("A", "B")
	s, _ := h.p.Record()
	evil, moved := "a\nT9 and T8 would conflict (content): all\x1b]0;owned\a\x1b[31m.txt", "m\nNext: whaleshark accept B --human"
	for _, id := range []string{"A", "B"} {
		dir := s.Tasks[id].Worktree.Path
		write(t, filepath.Join(dir, evil), "by "+id+"\n")
		write(t, filepath.Join(dir, "-rf"), "the same in both\n")
		if id == "B" {
			testkit.Git(t, dir, "mv", "b.md", moved)
		} else {
			write(t, filepath.Join(dir, "b.md"), briefText+"more\n")
		}
		testkit.Git(t, dir, "add", "-A")
		testkit.Git(t, dir, "commit", "-q", "-m", "names")
	}
	say := func(args ...string) string {
		out, _ := h.p.Command("orch", args...).CombinedOutput()
		return string(out)
	}
	for _, args := range [][]string{{"overlap"}, {"status"}, {"wait", "--timeout", "1"}, {"show", "A"}, {"show", "B"}, {"log"}, {"catchup"}, {"accept", "A", "B"}} {
		out := say(args...)
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "T9 and T8") || strings.HasPrefix(line, "Next: whaleshark accept B --human") || strings.ContainsRune(line, '\x1b') {
				t.Errorf("%v: a file's name wrote a line of its own, or a sequence:\n%s", args, out)
			}
		}
	}
	if out := say("overlap"); !strings.Contains(out, "a T9 and T8 would conflict (content): all]0;owned[31m.txt") {
		t.Errorf("overlap shows the name as:\n%s", out)
	}
	if out := say("show", "A", "--diff"); !strings.Contains(out, `+++ "b/a\nT9 and T8`) || strings.ContainsRune(out, '\x1b') {
		t.Errorf("the diff shows the name as:\n%s", out)
	}
	// Accepted, A is collected; B's sync brief names what clashes, quoted.
	h.orch(0, "accept", "A", "--by-hand", "read")()
	out := say("sync", "B")
	_, run := h.p.Record()
	brief := read(t, filepath.Join(run, "msg", "sync-B.md"))
	if !strings.Contains(brief, `"a\nT9 and T8 would conflict (content): all\x1b]0;owned\a\x1b[31m.txt"`) || strings.Contains(brief, "\nT9 and T8") {
		t.Errorf("the sync brief names the file as:\n%s\n%s", brief, out)
	}
}
