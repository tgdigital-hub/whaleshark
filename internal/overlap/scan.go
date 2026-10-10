package overlap

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// What a finding's How says beyond git's own words.
const (
	loose   = "not committed"
	twin    = "same file on this disk"
	clean   = "merges cleanly"
	unknown = "merge not checked"
	astray  = "outside what it owns"
	atHome  = "in the project's own folder while workers run"
)

type scanner struct{ k *contract.Kit }

// work is what one task has changed since the collected work it began from.
type work struct {
	t     *contract.Task
	head  string            // the tip of its branch
	snap  string            // on a deep scan, a commit of its folder as it stands
	files map[string]bool   // every path it touched; true for one not committed
	moved map[string]string // a renamed file's old name, and its new one
}

// tree is what is merged for a task on a deep scan.
func (w *work) tree() string { return cmp.Or(w.snap, w.head) }

// Scan is contract.Overlap. It asks git what each task in flight changed,
// merges every pair, and each task with the collected work, in git's store
// alone, and says what it found; it touches no branch, index or file of the
// project. With tasks named only their pairs and their own findings are
// looked at anew, and the run's other findings are returned as recorded.
func (o scanner) Scan(ctx context.Context, root string, s *contract.State, tasks []string, deep bool) ([]contract.Finding, error) {
	if s.Run.Integration == nil {
		return nil, nil
	}
	g, err := open(ctx, o.k, root, s)
	if err != nil {
		return nil, err
	}
	at, tip := g.at, g.tip
	var live []*work
	byID := map[string]*work{}
	for _, id := range slices.Sorted(maps.Keys(s.Tasks)) {
		t := s.Tasks[id]
		if w := t.Worktree; w != nil && w.RemovedAt.IsZero() && at[w.Branch] != "" && t.Status != contract.TaskDone && t.Status != contract.TaskCancelled {
			byID[id] = &work{t: t, head: at[w.Branch], files: map[string]bool{}, moved: map[string]string{}}
			live = append(live, byID[id])
		}
	}
	named := map[string]bool{}
	for _, name := range tasks {
		t, err := o.k.Rules.FindTask(s, name)
		if err == nil && byID[t.ID] == nil {
			err = &contract.Refusal{Exit: contract.ExitFailed, Code: "no_worktree", Message: t.ID + " has no copy of the code of its own in flight, so there is nothing of it to scan."}
		}
		if err != nil {
			return nil, err
		}
		named[t.ID] = true
	}
	fresh := func(w *work) bool { return len(named) == 0 || named[w.t.ID] }

	// The first thing that went wrong is told at the end, with all that was found.
	var failed error
	var mu sync.Mutex
	note := func(err error) {
		mu.Lock()
		failed = cmp.Or(failed, err)
		mu.Unlock()
	}
	var wg sync.WaitGroup
	four := make(chan bool, 4)
	for _, w := range live {
		wg.Go(func() {
			four <- true
			note(g.look(w, tip, deep))
			<-four
		})
	}
	wg.Wait()

	alive, jobs := []string{tip}, [][2]string{}
	for i, a := range live {
		alive = append(alive, a.head, a.tree())
		if fresh(a) {
			jobs = append(jobs, [2]string{tip, a.head})
		}
		for _, b := range live[i+1:] {
			if fresh(a) || fresh(b) {
				jobs = append(jobs, [2]string{a.head, b.head}, [2]string{a.tree(), b.tree()})
			}
		}
	}
	note(g.merge(jobs))
	if !deep {
		alive = nil // a snapshot's answers are kept until the next deep scan
	}
	g.save(alive)

	var out []contract.Finding
	had := map[string]bool{}
	add := func(kind, how string, files []string, tasks ...string) {
		slices.Sort(files)
		f := contract.Finding{Kind: kind, Tasks: tasks, Files: slices.Compact(files), How: how}
		if key := fmt.Sprint(kind, tasks, f.Files); len(files) > 0 && !had[key] {
			had[key] = true
			out = append(out, f)
		}
	}
	// Every path by the platform's key: who touched it, and under which name.
	type hit struct {
		w    *work
		name string
	}
	index := map[string][]hit{}
	for _, w := range live {
		var off []string
		for name := range w.files {
			key := o.k.Platform.PathKey(name)
			index[key] = append(index[key], hit{w, name})
			if len(w.t.Owns) > 0 && !owned(w.t.Owns, name) {
				off = append(off, name)
			}
		}
		if fresh(w) {
			add("scope", astray, off, w.t.ID)
			if m := g.seen.Pairs[tip+" "+w.head]; len(m.Clashes) > 0 {
				kinds := slices.Sorted(maps.Keys(m.Clashes))
				add("lands", strings.Join(kinds, ", "), slices.Concat(slices.Collect(maps.Values(m.Clashes))...), w.t.ID)
			}
		}
	}
	both, twins := map[[2]*work][]string{}, map[[2]*work][]string{}
	for _, hits := range index {
		for i, a := range hits {
			for _, b := range hits[i+1:] {
				switch pair := [2]*work{a.w, b.w}; {
				case a.w == b.w:
				case a.name == b.name:
					both[pair] = append(both[pair], a.name)
				default:
					twins[pair] = append(twins[pair], a.name, b.name)
				}
			}
		}
	}
	for i, a := range live {
		for _, b := range live[i+1:] {
			if !fresh(a) && !fresh(b) {
				continue
			}
			shared := append(both[[2]*work{a, b}], both[[2]*work{b, a}]...)
			same := append(twins[[2]*work{a, b}], twins[[2]*work{b, a}]...)
			m, checked := g.seen.Pairs[a.head+" "+b.head]
			wip := g.seen.Pairs[a.tree()+" "+b.tree()]
			var clash []string
			for _, how := range slices.Sorted(maps.Keys(m.Clashes)) {
				clash = append(clash, m.Clashes[how]...)
			}
			first, second := order(a, b, slices.Concat(clash, same), s.Run.Findings)
			for _, how := range slices.Sorted(maps.Keys(m.Clashes)) {
				add("overlap", how, m.Clashes[how], first, second)
			}
			// What clashes only once work not committed is counted.
			for _, how := range slices.Sorted(maps.Keys(wip.Clashes)) {
				files := slices.DeleteFunc(slices.Clone(wip.Clashes[how]), func(f string) bool { return slices.Contains(clash, f) })
				add("overlap", how+", "+loose, files, first, second)
				clash = append(clash, files...)
			}
			add("overlap", twin, same, first, second)
			how := []string{clean}
			if !checked {
				how[0] = unknown
			}
			shared = slices.DeleteFunc(shared, func(f string) bool { return slices.Contains(clash, f) })
			for _, f := range shared {
				for _, w := range []*work{a, b} {
					if w.files[f] {
						how = append(how, w.t.ID+" has it "+loose)
					}
					if to := w.moved[f]; to != "" {
						how = append(how, w.t.ID+" moved "+f+" to "+to)
					}
				}
			}
			slices.Sort(how[1:])
			add("same", strings.Join(slices.Compact(how), "; "), shared, a.t.ID, b.t.ID)
		}
	}
	// A worker that left its folder edits the project's own; a task placed there may.
	running := func(own bool) bool {
		return slices.ContainsFunc(slices.Collect(maps.Values(s.Tasks)), func(t *contract.Task) bool {
			return t.Status == contract.TaskRunning && (t.Worktree != nil) == own
		})
	}
	if running(true) && !running(false) {
		text, err := g.git(root, "", "status", "--porcelain=v1", "-z", "--untracked-files=no")
		note(err)
		add("scope", atHome, uncommitted(text))
	}
	// What was not looked at anew stays as the run has it: all of it when the
	// scan was cut off, and a clash of work not committed outside a deep scan.
	for _, f := range s.Run.Findings {
		again, dirty := len(f.Tasks) == 0, false
		if slices.ContainsFunc(f.Tasks, func(id string) bool { return byID[id] == nil }) {
			continue
		}
		for _, id := range f.Tasks {
			w := byID[id]
			again, dirty = again || fresh(w), dirty || slices.ContainsFunc(f.Files, func(name string) bool { return w.files[name] })
		}
		if !again || ctx.Err() != nil || !deep && dirty && strings.HasSuffix(f.How, ", "+loose) {
			add(f.Kind, f.How, f.Files, f.Tasks...)
		}
	}
	return out, cmp.Or(ctx.Err(), failed)
}

// look asks git what a task changed: its commits since they left the
// collected work, a rename under both names, and what waits in its folder.
func (g *repo) look(w *work, tip string, deep bool) error {
	out, err := g.git(g.root, "", "diff", "--name-status", "-z", "-M", tip+"..."+w.head)
	if err != nil {
		return err
	}
	tok := strings.Split(out, "\x00")
	for i := 0; i+1 < len(tok) && tok[i] != ""; i += 2 {
		switch tok[i][0] {
		case 'R':
			w.files[tok[i+1]], w.moved[tok[i+1]] = false, tok[i+2]
			i++
		case 'C':
			i++
		}
		w.files[tok[i+1]] = false
	}
	if out, err = g.git(w.t.Worktree.Path, "", "status", "--porcelain=v1", "-z", "--untracked-files=all"); err != nil {
		return err
	}
	waits := uncommitted(out)
	for _, name := range waits {
		if _, committed := w.files[name]; !committed {
			w.files[name] = true
		}
	}
	if deep && len(waits) > 0 {
		w.snap, err = g.snapshot(w.t.Worktree.Path, w.head, w.t.ID)
	}
	return err
}

// uncommitted reads git's short status: every path that is not as it was
// committed, a moved one under both names.
func uncommitted(out string) (paths []string) {
	tok := strings.Split(out, "\x00")
	for i := 0; i < len(tok); i++ {
		if len(tok[i]) < 4 {
			continue
		}
		paths = append(paths, tok[i][3:])
		if strings.ContainsAny(tok[i][:2], "RC") && i+1 < len(tok) {
			i++
			paths = append(paths, tok[i])
		}
	}
	return paths
}

// owned reports whether one of a task's patterns covers a file: the file
// itself, a folder above it (with or without a closing /, /* or /**), or a
// pattern of the shell's kind for the whole path or for its last part.
func owned(patterns []string, file string) bool {
	for _, p := range patterns {
		p = strings.TrimSuffix(strings.TrimSuffix(path.Clean(p), "/**"), "/*")
		whole, _ := path.Match(p, file)
		last, _ := path.Match(p, path.Base(file))
		if p == "." || p == "**" || p == file || strings.HasPrefix(file, p+"/") || whole || last {
			return true
		}
	}
	return false
}

// order puts first the task that keeps its place: the one whose work is
// reported, else the one that owns the files, else the one the run already
// has first, so that a pair is not announced again the other way round,
// else the one that changed fewer files, else the one with the earlier id.
func order(a, b *work, files []string, had []contract.Finding) (first, second string) {
	n := func(yes bool) int {
		if yes {
			return 0
		}
		return 1
	}
	owns := func(w *work) bool {
		return len(w.t.Owns) > 0 && !slices.ContainsFunc(files, func(f string) bool { return !owned(w.t.Owns, f) })
	}
	was := func(x, y *work) bool {
		return slices.ContainsFunc(had, func(f contract.Finding) bool {
			return f.Kind == "overlap" && slices.Equal(f.Tasks, []string{x.t.ID, y.t.ID})
		})
	}
	if cmp.Or(cmp.Compare(n(a.t.Status == contract.TaskReview), n(b.t.Status == contract.TaskReview)), cmp.Compare(n(owns(a)), n(owns(b))),
		cmp.Compare(n(was(a, b)), n(was(b, a))), cmp.Compare(len(a.files), len(b.files))) > 0 {
		a, b = b, a
	}
	return a.t.ID, b.t.ID
}
