package integrate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// trailer names the line of a collected commit's message that says whose
// work it holds, as "r3/T3". An accept that was cut off after it moved the
// branch and before it wrote the record is known by it.
const trailer = "Whaleshark-Task"

const marks = "--format=%H %(trailers:key=" + trailer + ",valueonly,separator=%x20)"

type integrator struct{ k *contract.Kit }

// Base is the commit a run begins at: the branch, tag or commit named, or
// with no name the branch the project's own checkout is on. It is nil where
// the project has no git, or no commit yet and no name given.
func Base(root, name string) (*contract.GitRef, error) {
	if name == "" {
		name, _ = git(root, "symbolic-ref", "--quiet", "--short", "HEAD")
		oid, err := at(root, "HEAD")
		if err != nil {
			return nil, nil
		}
		return &contract.GitRef{Ref: name, OID: oid}, nil
	}
	oid, err := at(root, name)
	if err != nil {
		return nil, refuse(contract.ExitMissing, "no_base", "This project has no branch, tag or commit "+name+".")
	}
	return &contract.GitRef{Ref: name, OID: oid}, nil
}

func (g integrator) Begin(root string, s *contract.State) (*contract.GitRef, *contract.Integration, error) {
	base := s.Run.Base
	if base == nil || base.OID == "" {
		name := ""
		if base != nil {
			name = base.Ref
		}
		var err error
		if base, err = Base(root, name); base == nil {
			return nil, nil, err
		}
	}
	project, err := contract.ReadProjectFile(root)
	if err != nil {
		return nil, nil, err
	}
	branch := project.Worktrees.BranchPrefix + s.Run.ID
	if _, err := git(root, "check-ref-format", ref(branch)); err != nil {
		return nil, nil, refuse(contract.ExitRefused, "bad_branch", "git takes no branch named "+branch+": change branch_prefix in whaleshark.toml.")
	}
	// An empty old value lets git make the branch only where there is none.
	if _, err := git(root, "update-ref", ref(branch), base.OID, ""); err != nil {
		if there, _ := at(root, ref(branch)); there != base.OID {
			return nil, nil, refuse(contract.ExitRefused, "branch_exists", "The branch "+branch+" exists already and is somewhere else. Remove or rename it first.")
		}
	}
	return base, &contract.Integration{Branch: branch, Tip: base.OID}, nil
}

// folder is a task's own copy of the code, or nothing where it has none.
func folder(root string, s *contract.State, task string) string {
	t := s.Tasks[task]
	if t == nil || t.Worktree == nil || !t.Worktree.RemovedAt.IsZero() {
		return ""
	}
	if filepath.IsAbs(t.Worktree.Path) {
		return t.Worktree.Path
	}
	return filepath.Join(root, t.Worktree.Path)
}

// tipOf is the commit the run's branch is at. The record may be behind it by
// commits of our own, where an accept was cut off before it wrote; any other
// difference means the branch was moved by hand, and nothing is built on that.
func tipOf(root string, s *contract.State) (string, error) {
	in := s.Run.Integration
	now, err := at(root, ref(in.Branch))
	if err != nil {
		return "", refuse(contract.ExitRefused, "no_branch", "The branch "+in.Branch+" of run "+s.Run.ID+" is gone.")
	}
	if now == in.Tip {
		return now, nil
	}
	lines, err := git(root, "log", "--first-parent", marks, in.Tip+".."+now)
	ours := err == nil && lines != "" && within(root, in.Tip, now)
	for _, line := range strings.Split(lines, "\n") {
		_, mark, _ := strings.Cut(line, " ")
		ours = ours && strings.HasPrefix(mark, s.Run.ID+"/")
	}
	if !ours {
		return "", refuse(contract.ExitRefused, "branch_moved", fmt.Sprintf(
			"The branch %s was moved by hand: run %s left it at %s. Put it back: git branch -f %s %s", in.Branch, s.Run.ID, in.Tip, in.Branch, in.Tip))
	}
	return now, nil
}

// collected finds the commit that already holds a task's work: one of the
// branch's own line that is not in the task's history and carries its
// trailer. A commit that came in with the base branch is never looked at.
func collected(root string, s *contract.State, task, head, tip string) string {
	lines, _ := git(root, "log", "--first-parent", marks, head+".."+tip)
	for _, line := range strings.Split(lines, "\n") {
		if oid, mark, _ := strings.Cut(line, " "); mark == s.Run.ID+"/"+task {
			return oid
		}
	}
	return ""
}

func conflict(task string, files []string) *contract.Refusal {
	r := refuse(contract.ExitRefused, "conflict", task+" does not merge with the collected work: "+strings.Join(files, ", "), "whaleshark sync "+task)
	r.Data = map[string]any{"task": task, "files": files}
	return r
}

func (g integrator) Prepare(root string, s *contract.State, task string) (contract.Checked, error) {
	dir := folder(root, s, task)
	if s.Run.Integration == nil || dir == "" {
		return contract.Checked{}, nil
	}
	tip, err := tipOf(root, s)
	if err != nil {
		return contract.Checked{}, err
	}
	// What is checked is what is collected, so nothing may wait beside it.
	waiting, err := git(dir, "status", "--porcelain")
	if err != nil {
		return contract.Checked{}, err
	}
	if waiting != "" {
		return contract.Checked{}, refuse(contract.ExitRefused, "uncommitted", fmt.Sprintf(
			"%s has %d files that are not committed, or not known to git. Its worker commits or removes them first.", task, strings.Count(waiting, "\n")+1),
			"whaleshark tell "+task+` "commit your work, or remove what does not belong to it"`)
	}
	head, err := at(dir, "HEAD")
	c := contract.Checked{Dir: dir, OID: head, Tip: tip}
	if err != nil || within(root, tip, head) || collected(root, s, task, head, tip) != "" {
		return c, err
	}
	tree, clash, err := simulate(root, tip, head)
	if err != nil {
		return contract.Checked{}, err
	}
	if len(clash) > 0 {
		return contract.Checked{}, conflict(task, clash)
	}
	// The merge that was simulated is the merge the folder gets: git only
	// moves the folder forward to it.
	if c.OID, err = commit(root, tree, "Merge the collected work of "+s.Run.ID, head, tip); err == nil {
		_, err = git(dir, "merge", "--quiet", "--ff-only", c.OID)
	}
	return c, err
}

func (g integrator) PrepareAll(root string, s *contract.State, tasks []string) ([]contract.Checked, error) {
	if s.Run.Integration == nil {
		return nil, refuse(contract.ExitRefused, "needs_git", "Several tasks are accepted together only where the project has git.")
	}
	tip, err := tipOf(root, s)
	if err != nil {
		return nil, err
	}
	dir, err := g.scratch(root, s)
	if err != nil {
		return nil, err
	}
	out, last := make([]contract.Checked, len(tasks)), tip
	for i, task := range tasks {
		from := folder(root, s, task)
		if from == "" {
			return nil, refuse(contract.ExitRefused, "needs_worktree", task+" has no copy of the code of its own: accept it alone.", "whaleshark accept "+task)
		}
		head, err := at(from, "HEAD")
		if err != nil {
			return nil, err
		}
		if collected(root, s, task, head, tip) == "" {
			tree, clash, err := simulate(root, last, head)
			if err != nil {
				return nil, err
			}
			if len(clash) > 0 {
				return nil, conflict(task, clash)
			}
			if last, err = commit(root, tree, "Merge "+task, last, head); err != nil {
				return nil, err
			}
		}
		out[i] = contract.Checked{Dir: dir, OID: last}
	}
	if len(out) == 0 {
		return nil, nil
	}
	out[0].Tip = tip
	return out, g.place(root, s, dir, last)
}

func (g integrator) Collect(root string, s *contract.State, task string, c contract.Checked) (string, error) {
	t, in := s.Tasks[task], s.Run.Integration
	if c == (contract.Checked{}) || t == nil || in == nil {
		return "", nil
	}
	if own := folder(root, s, task); own != "" {
		head, _ := at(own, "HEAD")
		if tip, _ := at(root, ref(in.Branch)); head != "" && tip != "" {
			if done := collected(root, s, task, head, tip); done != "" {
				return done, nil
			}
		}
	}
	// A task's own folder must stand at the commit that was checked. The
	// run's copy holds several tasks, each a step on the way to where it stands.
	head, err := at(c.Dir, "HEAD")
	if err != nil {
		return "", err
	}
	changed, err := git(c.Dir, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return "", err
	}
	ours, _ := g.scratch(root, s)
	if changed != "" || head != c.OID && (c.Dir != ours || !within(root, c.OID, head)) {
		return "", refuse(contract.ExitRefused, "moved", "The folder "+task+" was checked in has changed since. Nothing was collected.", "whaleshark accept "+task)
	}
	// One parent, the tip: a task is one commit. A task that merged the base
	// branch in keeps that commit as a second parent, or the base would
	// never be seen as part of the collected work and nothing could land.
	parents := []string{c.Tip}
	if s.Run.Base != nil && s.Run.Base.Ref != "" {
		if base, err := git(root, "merge-base", "--end-of-options", c.OID, s.Run.Base.Ref); err == nil && !within(root, base, c.Tip) {
			parents = append(parents, base)
		}
	}
	made, err := commit(root, c.OID+"^{tree}", t.ID+": "+t.Title+"\n\n"+trailer+": "+s.Run.ID+"/"+t.ID, parents...)
	if err != nil {
		return "", err
	}
	// Git moves the branch only from the commit the check was run on top of.
	if _, err := git(root, "update-ref", ref(in.Branch), made, c.Tip); err != nil {
		return "", refuse(contract.ExitRefused, "tip_moved", "The branch "+in.Branch+" moved while "+task+" was checked. Nothing was collected.", "whaleshark accept "+task)
	}
	return made, nil
}

func (g integrator) Diff(root string, s *contract.State, task string, stat bool) (string, error) {
	t, in := s.Tasks[task], s.Run.Integration
	if t == nil {
		return "", refuse(contract.ExitMissing, "no_task", "There is no task "+task+".")
	}
	args, dir := []string{"diff", "--no-color", "--no-ext-diff"}, root
	if stat {
		args = append(args, "--stat")
	}
	switch own := folder(root, s, task); {
	case t.Accepted != nil && t.Accepted.Commit != "":
		args = append(args, t.Accepted.Commit+"^!")
	case in != nil && own != "":
		args, dir = append(args, "--merge-base", in.Tip), own
	case in != nil && t.Worktree != nil:
		args = append(args, "--merge-base", in.Tip, ref(t.Worktree.Branch))
	default:
		// A shared folder: what is not committed there, whoever changed it.
		if sub, ok := strings.CutPrefix(t.Placement, contract.PlaceCwd); ok {
			dir = filepath.Join(root, sub)
			if filepath.IsAbs(sub) {
				dir = sub
			}
		}
		args = append(args, "HEAD")
	}
	out, err := git(dir, args...)
	if err != nil {
		return "", refuse(contract.ExitRefused, "needs_git", "A diff needs git, and a commit to compare with.")
	}
	return out, nil
}

func (g integrator) Drop(root string, s *contract.State) error {
	in := s.Run.Integration
	if in == nil {
		return nil
	}
	dir, err := g.scratch(root, s)
	if err != nil {
		return err
	}
	git(root, "worktree", "remove", "--force", dir)
	os.Remove(dir + ready)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	// Git deletes the branch only where it still stands at what was landed:
	// work collected since is kept.
	if !s.Run.ClosedAt.IsZero() && in.Landed != "" {
		git(root, "update-ref", "-d", ref(in.Branch), in.Landed)
	}
	return nil
}

// ready is the ending of the file beside the run's copy that says it was set up.
const ready = ".ready"

// scratch names the run's own copy of the code: a folder in the login's
// private cache, kept from the first use until Drop.
func (g integrator) scratch(root string, s *contract.State) (string, error) {
	dirs, err := g.k.Platform.Dirs()
	sum := sha256.Sum256([]byte(g.k.Platform.PathKey(root)))
	return filepath.Join(dirs.Cache, "tmp", "run-"+hex.EncodeToString(sum[:6])+"-"+s.Run.ID), err
}

// place puts a commit into the run's copy. The first time, the copy is made
// as a worktree on no branch and equipped as a task's own is, once; later
// only its tracked files move, so what the setup left stays.
func (g integrator) place(root string, s *contract.State, dir, oid string) error {
	if _, err := os.Stat(dir + ready); err == nil {
		if _, err = git(dir, "checkout", "--quiet", "--force", "--detach", oid); err == nil {
			_, err = git(dir, "clean", "--quiet", "-fd")
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return err
	}
	if err := g.k.Platform.Private(filepath.Dir(dir)); err != nil {
		return err
	}
	// A copy that is gone or cannot be used, and what a try that was cut off
	// left, is taken away first.
	os.Remove(dir + ready)
	git(root, "worktree", "remove", "--force", dir)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	git(root, "worktree", "prune")
	if _, err := git(root, "worktree", "add", "--quiet", "--detach", dir, oid); err != nil {
		return err
	}
	// The copy is given what a task's own copy is given, the setup included.
	log := filepath.Join(g.k.Store.Dir(root, s.Run.ID), "copy-setup.log")
	how, err := g.k.Placement.Equip(root, dir, log)
	if err == nil && how == contract.SetupFailed {
		err = fmt.Errorf("the setup of the run's copy of the code failed. The whole log: %s", log)
	}
	if err != nil {
		return err
	}
	return os.WriteFile(dir+ready, nil, 0o600)
}
