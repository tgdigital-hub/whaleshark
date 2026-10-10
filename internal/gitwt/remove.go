package gitwt

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// markName is the file in git's own folder for a worktree that says the
// worktree is ours: its path, its run, its task, then one line for each path
// linked into it, which git may show as new and which is no work.
const markName = "whaleshark"

type mark struct {
	path, run, task string
	links           []string
	at              time.Time
}

func writeMark(path, run, task string, links []string) error {
	at, err := git(path, "rev-parse", "--path-format=absolute", "--git-path", markName)
	if err != nil {
		return err
	}
	lines := append([]string{path, run, task}, links...)
	return os.WriteFile(at, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func readMark(file string) (m mark, ok bool) {
	// #nosec G304 -- a file of ours in git's own folder
	data, err := os.ReadFile(file)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if err != nil || len(lines) < 3 {
		return m, false
	}
	if info, err := os.Stat(file); err == nil {
		m.at = info.ModTime()
	}
	m.path, m.run, m.task, m.links = lines[0], lines[1], lines[2], lines[3:]
	return m, true
}

// marks lists every worktree of ours the repository still knows.
func marks(root string) (all []mark) {
	common, err := git(root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil
	}
	dirs, _ := os.ReadDir(filepath.Join(common, "worktrees"))
	for _, d := range dirs {
		if m, ok := readMark(filepath.Join(common, "worktrees", d.Name(), markName)); ok {
			all = append(all, m)
		}
	}
	return all
}

func (t *trees) Clean(dir string) (bool, error) {
	at, err := git(dir, "rev-parse", "--path-format=absolute", "--git-path", markName)
	if err != nil {
		return true, nil
	}
	m, ours := readMark(at)
	if !ours {
		return true, nil
	}
	out, err := git(dir, "status", "--porcelain", "-z")
	for entry := range strings.SplitSeq(out, "\x00") {
		if name, fresh := strings.CutPrefix(entry, "?? "); entry != "" && !(fresh && slices.Contains(m.links, name)) {
			return false, err
		}
	}
	return true, err
}

func (t *trees) Remove(root string, s *contract.State, task string, discard bool) (*contract.Worktree, error) {
	tk := s.Tasks[task]
	if tk == nil || tk.Worktree == nil {
		return nil, nil
	}
	w := *tk.Worktree
	if !w.RemovedAt.IsZero() {
		return &w, nil
	}
	if !discard {
		if clean, err := t.Clean(w.Path); err != nil {
			return nil, err
		} else if !clean {
			r := refuse("uncommitted", "%s holds work that is not committed, in %s.", tk.ID, w.Path)
			r.Next = []string{"whaleshark close " + tk.ID + " --discard"}
			return nil, r
		}
	}
	if err := t.take(root, w.Path); err != nil {
		return nil, err
	}
	w.RemovedAt = contract.Now()
	return &w, nil
}

// intent is one line of removals.jsonl: a removal about to begin, or with
// Done the same removal finished.
type intent struct {
	contract.Versioned
	Path string    `json:"path"`
	At   time.Time `json:"at"`
	Done bool      `json:"done,omitempty"`
}

const intentFile = "removals.jsonl"

// note adds a line to the intent log and has it on disk before it returns.
// The line starts on a line of its own, whatever a killed writer left.
func note(root, path string, done bool) error {
	line, err := json.Marshal(intent{contract.Versioned{Version: contract.FileVersion}, path, contract.Now(), done})
	if err != nil {
		return err
	}
	dir := filepath.Join(root, contract.ProjectDir)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// #nosec G304 -- the project's own folder of ours
	f, err := os.OpenFile(filepath.Join(dir, intentFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(f, "\n%s\n", line); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// open lists the removals that were begun and never finished. A line this
// program cannot read, or of a newer one, is as good as absent.
func (t *trees) open(root string) (paths []string, err error) {
	data, err := t.k.Platform.Peek(filepath.Join(root, contract.ProjectDir, intentFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	for line := range strings.Lines(string(data)) {
		var in intent
		if json.Unmarshal([]byte(line), &in) != nil || in.Path == "" || in.Version > contract.FileVersion {
			continue
		}
		paths = slices.DeleteFunc(paths, func(p string) bool { return p == in.Path })
		if !in.Done {
			paths = append(paths, in.Path)
		}
	}
	return paths, err
}

// take removes a worktree: the intent first, so that a removal cut off
// halfway can be finished by anyone who reads the log.
func (t *trees) take(root, path string) error {
	if err := note(root, path, false); err != nil {
		return err
	}
	return t.finish(root, path)
}

// finish deletes what is left of a worktree and writes down that it is
// gone. It deletes only what carries our mark: the log lies in the project,
// and a path in it proves nothing.
func (t *trees) finish(root, path string) error {
	key := t.k.Platform.PathKey
	if slices.ContainsFunc(marks(root), func(m mark) bool { return key(m.path) == key(path) }) {
		if err := t.drop(root, path); err != nil {
			return err
		}
		os.Remove(path + setupLog)
	}
	return note(root, path, true)
}

// drop deletes a worktree and all in it, by git or, where git will not,
// by hand.
func (t *trees) drop(root, path string) error {
	return t.alone(root, func() error {
		if _, err := git(root, "worktree", "remove", "--force", "--", path); err == nil {
			return nil
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		_, err := git(root, "worktree", "prune")
		return err
	})
}

func (t *trees) Forget(root string, s *contract.State) (kept []string, err error) {
	for _, id := range slices.Sorted(maps.Keys(s.Tasks)) {
		w := s.Tasks[id].Worktree
		if w == nil {
			continue
		}
		if w.RemovedAt.IsZero() {
			if clean, err := t.Clean(w.Path); err != nil {
				return kept, err
			} else if !clean {
				kept = append(kept, w.Branch+" (work not committed in "+w.Path+")")
				continue
			}
			if err := t.take(root, w.Path); err != nil {
				return kept, err
			}
		}
		tip, err := git(root, "rev-parse", "--verify", "-q", "refs/heads/"+w.Branch)
		switch {
		case err != nil: // gone already
		case w.KeepBranch || !merged(root, s, w, tip):
			kept = append(kept, w.Branch)
		default:
			if _, err := git(root, "update-ref", "-d", "refs/heads/"+w.Branch, tip); err != nil {
				return kept, err
			}
			git(root, "config", "--remove-section", "branch."+w.Branch)
		}
	}
	return kept, nil
}

// merged reports whether a branch's work is proven to be in the run's
// collected work, in the run's base or in what the branch was cut from, by
// any of three proofs, each good alone.
func merged(root string, s *contract.State, w *contract.Worktree, tip string) bool {
	into := []string{w.BaseRef}
	if in := s.Run.Integration; in != nil {
		into = append(into, in.Branch)
	}
	if b := s.Run.Base; b != nil {
		into = append(into, b.Ref)
	}
	for _, ref := range into {
		tree, err := git(root, "rev-parse", "--verify", "-q", "--end-of-options", ref+"^{tree}")
		if ref == "" || err != nil {
			continue
		}
		// One: the branch's last commit is in its history.
		if _, err := git(root, "merge-base", "--is-ancestor", tip, ref); err == nil {
			return true
		}
		// Two: every commit of the branch has its like there, change for
		// change, as after a rebase or a pick. A merge has no like.
		if n, err := git(root, "rev-list", "--count", "--cherry-pick", "--right-only", ref+"..."+tip); err == nil && n == "0" {
			return true
		}
		// Three: merging the branch into it would change nothing, as after
		// its work went in as one commit.
		if after, err := git(root, "merge-tree", "--write-tree", ref, tip); err == nil && after == tree {
			return true
		}
	}
	return false
}

func (t *trees) Repair(root string, runs []*contract.State, fix bool) (found []string, err error) {
	open, err := t.open(root)
	if err != nil {
		return nil, err
	}
	for _, path := range open {
		line := "The removal of " + path + " was cut off"
		if fix {
			if err := t.finish(root, path); err != nil {
				return found, err
			}
			line += ": finished"
		}
		found = append(found, line+".")
	}
	key, known := t.k.Platform.PathKey, map[string]bool{}
	for _, s := range runs {
		for _, tk := range s.Tasks {
			if w := tk.Worktree; w != nil && w.RemovedAt.IsZero() {
				known[key(w.Path)] = true
			}
		}
	}
	// A worktree exists before its record does, for as long as its setup
	// runs: one that young is being made, not left over.
	p, _ := contract.ReadProjectFile(root)
	young := time.Duration(p.Setup.TimeoutSeconds)*time.Second + time.Minute
	for _, m := range marks(root) {
		if known[key(m.path)] || slices.Contains(open, m.path) || time.Since(m.at) < young {
			continue
		}
		line := fmt.Sprintf("The worktree %s (%s %s) is in no run's record", m.path, m.run, m.task)
		if clean, err := t.Clean(m.path); err != nil {
			return found, err
		} else if !clean {
			line += " and holds work that is not committed"
		} else if fix {
			if err := t.take(root, m.path); err != nil {
				return found, err
			}
			line += ": removed"
		}
		found = append(found, line+".")
	}
	return found, nil
}
