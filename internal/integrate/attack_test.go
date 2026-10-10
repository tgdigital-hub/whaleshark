package integrate_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

// The prefix of a run's branch comes from the project's own file, which a
// repository brings along and nobody approves. One that begins with a dash
// would make the branch's name an option, for git and for gh: no run begins
// on it and no such branch is made.
func TestABranchPrefixIsNeverAnOption(t *testing.T) {
	r := open(t, map[string]string{"a.txt": "one\n"})
	for _, prefix := range []string{"--repo=elsewhere/x-", "-R", "--web-"} {
		write(t, filepath.Join(r.root, "whaleshark.toml"), "[worktrees]\nbranch_prefix = \""+prefix+"\"\n")
		s := r.state()
		s.Run.ID, s.Run.Base = "r2", nil
		if _, in, err := r.k.Integrator.Begin(r.root, s); code(err) != "bad_branch" || in != nil {
			t.Errorf("the prefix %q: a run began on %+v, %v", prefix, in, err)
		}
		out := play(t, r.p.Command(scenario.Orch, "run", "new", "another", "--park"), contract.ExitRefused, "branch_prefix")
		if refs := r.git(r.root, "for-each-ref", "--format=%(refname)"); strings.Contains(refs, "refs/heads/-") {
			t.Errorf("the prefix %q left a branch:\n%s\n%s", prefix, refs, out)
		}
	}
}
