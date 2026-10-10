package gitwt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// What a worktree's record says of its setup.
const (
	setupNone      = contract.SetupNone
	setupOK        = contract.SetupOK
	setupFailed    = contract.SetupFailed
	setupUntrusted = contract.SetupUntrusted
)

func (t *trees) Place(root string, s *contract.State, task string) (dir string, w *contract.Worktree, err error) {
	tk := s.Tasks[task]
	if tk == nil {
		return "", nil, fmt.Errorf("no task %s", task)
	}
	if dir, ok := strings.CutPrefix(tk.Placement, contract.PlaceCwd); ok {
		return dir, nil, nil
	}
	if tk.Placement != contract.PlaceWorktree {
		return root, nil, nil
	}
	// A project without git has one folder. within is where the project
	// lies in its repository when it is not all of it.
	within, err := git(root, "rev-parse", "--show-prefix")
	if err != nil {
		return root, nil, nil
	}
	if w = tk.Worktree; w != nil && w.RemovedAt.IsZero() {
		if _, err := os.Stat(w.Path); err == nil {
			return filepath.Join(w.Path, within), w, nil
		}
	}
	p, err := contract.ReadProjectFile(root)
	if err != nil {
		return "", nil, err
	}
	copies, links, err := t.carried(root, p)
	if err != nil {
		return "", nil, err
	}
	holder, err := t.holder(root, p)
	if err != nil {
		return "", nil, err
	}
	fresh := contract.Worktree{}
	if w != nil {
		fresh = *w
	}
	w = &fresh
	w.Path, w.CreatedAt, w.RemovedAt = filepath.Join(holder, s.Run.ID+"-"+tk.ID), contract.Now(), time.Time{}
	if w.Branch == "" {
		w.Branch = p.Worktrees.BranchPrefix + strings.Trim(s.Run.ID+"-"+tk.ID+"-"+slug(tk.Name), "-")
	}
	if _, err := git(root, "check-ref-format", "--branch", w.Branch); err != nil {
		return "", nil, refuse("bad_branch", "%q cannot be a branch's name: see branch_prefix in whaleshark.toml.", w.Branch)
	}
	if open, _ := t.open(root); slices.Contains(open, w.Path) { // an interrupted removal of an earlier one
		if err := t.finish(root, w.Path); err != nil {
			return "", nil, err
		}
	}
	if _, err := os.Stat(holder); err != nil && !inside(root, holder) {
		fmt.Fprintf(t.warn, "Worktrees are made in %s, outside the project: each new one stops at its agent's trust prompt until you have trusted that folder once.\n", holder)
	}
	if err = os.MkdirAll(holder, 0o700); err == nil {
		err = t.k.Platform.Private(holder)
	}
	if err != nil {
		return "", nil, err
	}
	made, cut := false, false
	defer func(path, branch string) { // a call that fails leaves nothing it made
		if err == nil {
			return
		}
		if made {
			t.drop(root, path)
		}
		if cut {
			git(root, "branch", "-D", "--", branch)
		}
	}(w.Path, w.Branch)
	err = t.alone(root, func() (err error) {
		if _, gone := os.Stat(filepath.Join(w.Path, ".git")); gone != nil {
			if cut, err = add(root, s, w); err != nil {
				return err
			}
			if made = true; cut {
				if _, err = git(root, "config", "branch."+w.Branch+".base", w.BaseRef); err != nil {
					return err
				}
			}
		}
		return writeMark(w.Path, s.Run.ID, tk.ID, links)
	})
	if err != nil {
		return "", nil, err
	}
	dir = filepath.Join(w.Path, within)
	if err = t.carry(root, dir, copies, links); err != nil {
		return "", nil, err
	}
	w.Setup = t.setup(p, dir, w.Path+setupLog)
	return dir, w, nil
}

// alone runs fn while no other call of ours changes the repository's list
// of worktrees: git reads that list whole, and stumbles over an entry that
// is half made or half gone.
func (t *trees) alone(root string, fn func() error) error {
	dir := filepath.Join(root, contract.ProjectDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	unlock, err := t.k.Platform.Lock(filepath.Join(dir, "worktrees.lock"), true)
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

// add makes the worktree: on the task's branch where that exists, else on a
// new one cut from the base, which its caller writes into git's settings so
// that the record can be made again from git alone. A branch that was not
// ours is adopted and never deleted.
func add(root string, s *contract.State, w *contract.Worktree) (cut bool, err error) {
	git(root, "worktree", "prune") // a folder deleted by hand is still on git's list
	if _, err := git(root, "rev-parse", "--verify", "-q", "refs/heads/"+w.Branch); err == nil {
		if _, err := git(root, "config", "--get", "branch."+w.Branch+".base"); err != nil {
			w.KeepBranch = true
		}
		_, err = git(root, "worktree", "add", "--", w.Path, w.Branch)
		return false, err
	}
	if w.BaseRef, w.BaseOID, err = base(root, s); err != nil {
		return false, err
	}
	if _, err = git(root, "worktree", "add", "--no-track", "-b", w.Branch, "--", w.Path, w.BaseOID); err != nil {
		git(root, "branch", "-D", "--", w.Branch) // git makes the branch first
	}
	return err == nil, err
}

// base is what a task's branch is cut from: the run's collected work, so
// that a task sees what was accepted before it; else the run's base; else
// the main branch of where the project came from, else the branch the
// project is on. A base that lives elsewhere is fetched first, and a fetch
// that fails changes nothing.
func base(root string, s *contract.State) (ref, oid string, err error) {
	var refs []string
	if in := s.Run.Integration; in != nil {
		refs = append(refs, in.Branch)
	}
	if b := s.Run.Base; b != nil {
		refs = append(refs, b.Ref)
	}
	from, _ := git(root, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD")
	on, _ := git(root, "symbolic-ref", "-q", "--short", "HEAD")
	for _, ref = range append(refs, from, on, "HEAD") {
		if ref == "" {
			continue
		}
		full, _ := git(root, "rev-parse", "--verify", "-q", "--symbolic-full-name", "--end-of-options", ref)
		if far, ok := strings.CutPrefix(full, "refs/remotes/"); ok {
			remote, branch, _ := strings.Cut(far, "/")
			ctx, cancel := context.WithTimeout(context.Background(), fetchWait)
			run(ctx, root, "fetch", "-q", "--no-tags", "--", remote, branch)
			cancel()
		}
		if oid, err = git(root, "rev-parse", "--verify", "-q", "--end-of-options", ref+"^{commit}"); err == nil {
			return ref, oid, nil
		}
	}
	return "", "", refuse("no_commit", "The project has no commit yet, so there is nothing to cut a worktree from.")
}

// holder is the folder the project's worktrees lie in: inside the project,
// under a folder its agents already trust; for a project in a syncing folder
// under the login's cache instead; or where an approved setting says.
func (t *trees) holder(root string, p contract.ProjectFile) (string, error) {
	dir := p.Worktrees.Dir
	if dir == contract.ProjectDefaults().Worktrees.Dir && t.k.Platform.Synced(root) {
		dirs, err := t.k.Platform.Dirs()
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256([]byte(t.k.Platform.PathKey(root)))
		dir = filepath.Join(dirs.Cache, "worktrees", filepath.Base(root)+"-"+hex.EncodeToString(sum[:4]))
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return dir, nil
}

func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// slug is a task's name as part of a branch's name.
func slug(name string) string {
	parts := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	return strings.Join(parts, "-")
}

const setupLog = ".setup.log"

// setup runs the project's setup line in a new worktree, when a person has
// approved it, and says how it went. All it printed is in a file beside the
// worktree. A line that runs past its time is ended with all it started.
func (t *trees) setup(p contract.ProjectFile, dir, log string) string {
	key, line := "setup.script", p.Setup.Script
	if t.k.Platform.System() == "windows" {
		key, line = "setup.script_windows", p.Setup.ScriptWindows
	}
	if slices.Contains(p.Held, key) {
		return setupUntrusted
	}
	if line == "" {
		return setupNone
	}
	// #nosec G302 G304 -- a file of ours beside the worktree, private to the login
	out, err := os.OpenFile(log, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return setupFailed
	}
	defer out.Close()
	cmd := t.k.Platform.Shell(line)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, out, out
	if err = cmd.Start(); err == nil {
		ended := make(chan error, 1)
		go func() { ended <- cmd.Wait() }()
		limit := time.Duration(p.Setup.TimeoutSeconds) * time.Second
		if limit <= 0 {
			limit = time.Duration(contract.ProjectDefaults().Setup.TimeoutSeconds) * time.Second
		}
		select {
		case err = <-ended:
		case <-time.After(limit):
			t.k.Platform.EndTree(cmd)
			err = fmt.Errorf("stopped after %v (%v)", limit, <-ended)
		}
	}
	if err != nil {
		fmt.Fprintln(out, err)
		return setupFailed
	}
	return setupOK
}
