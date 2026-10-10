package overlap

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// wipeAfter is the number of simulated merges after which the scratch
// folder is emptied: each leaves about a dozen small objects in it. answers
// is the file beside those objects that holds what the merges said.
const wipeAfter, answers = 5000, "pairs.json"

// listed names a folder in a list git divides at colons: quoted, with the
// two characters the quoting itself gives a meaning escaped.
var listed = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

// simulation merges two commits in git's store alone (git 2.38); with batch
// one process does every pair it is given on its input (git 2.40).
var (
	simulation = []string{"merge-tree", "--write-tree", "--name-only", "--messages", "-z"}
	batch      = "--stdin"
)

// merged is how a simulated merge ended: Clashes has, under git's own word
// for each kind of clash, the files it names, and is empty for a clean one.
type merged struct {
	Clashes map[string][]string `json:"clashes,omitempty"`
}

// seen is what is kept between scans beside the scratch objects: the answer
// for every pair of commits already merged, so that only a pair in which a
// branch has moved is merged again.
type seen struct {
	contract.Versioned
	Made  int               `json:"made"`
	Pairs map[string]merged `json:"pairs"`
}

// repo is the project's git for one scan. Every call ends with the scan's
// context, asks nothing, takes no lock it can do without, and writes the
// objects it makes into a scratch folder of the login's private cache: the
// project's own store is only read.
type repo struct {
	ctx   context.Context
	k     *contract.Kit
	root  string
	dir   string
	env   []string
	seen  seen
	blind bool              // this git cannot simulate a merge
	at    map[string]string // the commit of every branch, by its name
	tip   string            // the collected work
}

func open(ctx context.Context, k *contract.Kit, root string, s *contract.State) (*repo, error) {
	dirs, err := k.Platform.Dirs()
	if err != nil || s.Run.Integration == nil {
		return nil, cmp.Or(err, errors.New("run "+s.Run.ID+" collects no work"))
	}
	sum := sha256.Sum256([]byte(k.Platform.PathKey(root)))
	g := &repo{ctx: ctx, k: k, root: root, dir: filepath.Join(dirs.Cache, "overlap", hex.EncodeToString(sum[:6])), at: map[string]string{},
		env: append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")}
	store, err := g.git(root, "", "rev-parse", "--path-format=absolute", "--git-path", "objects")
	if err != nil {
		return nil, err
	}
	objects := filepath.Join(g.dir, "objects")
	if contract.ReadVersioned(k.Platform.Read, filepath.Join(g.dir, answers), contract.FileVersion, &g.seen) != nil || g.seen.Made > wipeAfter {
		os.RemoveAll(objects)
		g.seen = seen{}
	}
	if g.seen.Pairs == nil {
		g.seen.Pairs = map[string]merged{}
	}
	if err = cmp.Or(os.MkdirAll(objects, 0o700), k.Platform.Private(g.dir)); err != nil {
		return nil, err
	}
	g.env = append(g.env, "GIT_OBJECT_DIRECTORY="+objects, `GIT_ALTERNATE_OBJECT_DIRECTORIES="`+listed.Replace(store)+`"`)
	out, err := g.git(root, "", "for-each-ref", "--format=%(objectname) %(refname)", "refs/heads/")
	for _, line := range strings.Split(out, "\n") {
		oid, name, _ := strings.Cut(line, " refs/heads/")
		g.at[name] = oid
	}
	if g.tip = g.at[s.Run.Integration.Branch]; err == nil && g.tip == "" {
		err = fmt.Errorf("the branch %s of run %s is gone", s.Run.Integration.Branch, s.Run.ID)
	}
	return g, err
}

// save keeps the answers for the next scan; alive, when given, is every
// commit still worth an answer.
func (g *repo) save(alive []string) {
	for key := range g.seen.Pairs {
		a, b, _ := strings.Cut(key, " ")
		if alive != nil && !(slices.Contains(alive, a) && slices.Contains(alive, b)) {
			delete(g.seen.Pairs, key)
		}
	}
	g.seen.Version = contract.FileVersion
	contract.WriteVersioned(g.k.Platform, filepath.Join(g.dir, answers), contract.FileVersion, g.seen)
}

func (g *repo) git(dir, stdin string, args ...string) (string, error) {
	// #nosec G204 -- git, with a list of arguments the tool built
	cmd := exec.CommandContext(g.ctx, "git", args...)
	var said strings.Builder
	cmd.Dir, cmd.Env, cmd.Stdin, cmd.Stderr = dir, g.env, strings.NewReader(stdin), &said
	out, err := cmd.Output()
	if err != nil {
		err = fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(said.String()))
	}
	return strings.TrimRight(string(out), "\n"), err
}

// merge simulates every pair of commits it has no answer for yet and keeps
// the answers: four processes side by side, each given its share of the
// pairs at once. Cut off, it keeps the answers git had finished. An older
// git gets one process a pair; one that cannot simulate at all leaves the
// scan blind and is no error.
func (g *repo) merge(jobs [][2]string) error {
	var todo [][2]string
	for _, j := range jobs {
		if _, ok := g.seen.Pairs[j[0]+" "+j[1]]; !ok && !slices.Contains(todo, j) {
			todo = append(todo, j)
		}
	}
	if len(todo) == 0 || g.blind {
		return nil
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var old atomic.Bool
	keep := func(j [2]string, m merged) {
		mu.Lock()
		g.seen.Pairs[j[0]+" "+j[1]] = m
		g.seen.Made++
		mu.Unlock()
	}
	for part := range slices.Chunk(todo, (len(todo)+3)/4) {
		wg.Go(func() {
			var lines strings.Builder
			for _, j := range part {
				lines.WriteString(j[0] + " " + j[1] + "\n")
			}
			out, err := g.git(g.root, lines.String(), append(slices.Clone(simulation), batch)...)
			tok := strings.Split(out, "\x00")
			for i, n := 0, 0; n < len(part) && i+2 < len(tok); n++ {
				m, next, whole := parse(tok[:len(tok)-1], i+2)
				if !whole {
					break
				}
				keep(part[n], m)
				i = next
			}
			if err != nil {
				old.Store(true)
			}
		})
	}
	wg.Wait()
	if !old.Load() || g.ctx.Err() != nil {
		return g.ctx.Err()
	}
	for _, j := range todo {
		if _, ok := g.seen.Pairs[j[0]+" "+j[1]]; ok {
			continue
		}
		out, err := g.git(g.root, "", append(slices.Clone(simulation), j[0], j[1])...)
		var ended *exec.ExitError
		switch {
		case g.ctx.Err() != nil:
			return g.ctx.Err()
		case errors.As(err, &ended) && ended.ExitCode() == 129:
			g.blind = true
			return nil
		case err != nil && (ended == nil || ended.ExitCode() != 1):
			return err
		}
		m, _, _ := parse(strings.Split(out, "\x00"), 1)
		keep(j, m)
	}
	return nil
}

// parse reads one simulated merge from i on: the names git lists, which the
// messages make redundant, then the messages. The kind of a clash is taken
// from the message, which says "add/add" where the type field says only
// "contents", and a file is filed under the first kind that names it. A name
// git made up for a file it had to move aside is left out. whole is false
// when the text ends before the merge does.
func parse(tok []string, i int) (m merged, next int, whole bool) {
	first := i
	for i < len(tok) && tok[i] != "" {
		i++
	}
	names := tok[first:i]
	if len(names) > 0 {
		m.Clashes = map[string][]string{}
	}
	had := map[string]bool{}
	for i++; i < len(tok) && tok[i] != ""; {
		n, err := strconv.Atoi(tok[i])
		if err != nil || n < 0 || i+n+2 >= len(tok) {
			return m, i, false
		}
		paths, kind, text := tok[i+1:i+1+n], tok[i+1+n], tok[i+2+n]
		i += n + 3
		if !strings.HasPrefix(text, "CONFLICT (") {
			text = kind
		}
		how, _, ok := strings.Cut(strings.TrimPrefix(text, "CONFLICT ("), ")")
		if !ok || !strings.HasPrefix(text, "CONFLICT (") {
			continue
		}
		for _, p := range paths {
			if cut := strings.LastIndexByte(p, '~'); had[p] || cut > 0 && slices.Contains(paths, p[:cut]) {
				continue
			}
			had[p] = true
			m.Clashes[how] = append(m.Clashes[how], p)
		}
	}
	// A clash git lists and says nothing of is still one.
	if len(names) > 0 && len(m.Clashes) == 0 {
		m.Clashes["conflict"] = names
	}
	return m, i + 1, i < len(tok)
}

// snapshot is a commit of everything in a task's folder as it stands,
// committed or not, made through an index of our own: the folder, its real
// index and the project's store stay as they were. The same files give the
// same commit, so its merges are answered once.
func (g *repo) snapshot(dir, head, name string) (string, error) {
	index := filepath.Join(g.dir, "index-"+name)
	real, err := g.git(dir, "", "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return "", err
	}
	// Starting from a copy of the real index, only changed files are read.
	if data, err := g.k.Platform.Read(real); err != nil || os.WriteFile(index, data, 0o600) != nil {
		os.Remove(index)
	}
	own := *g
	own.env = append(slices.Clone(g.env), "GIT_INDEX_FILE="+index)
	for _, who := range []string{"AUTHOR", "COMMITTER"} {
		own.env = append(own.env, "GIT_"+who+"_NAME=whaleshark", "GIT_"+who+"_EMAIL=whaleshark@localhost", "GIT_"+who+"_DATE=2000-01-01T00:00:00Z")
	}
	_, err = own.git(dir, "", "add", "-A")
	tree, wrote := own.git(dir, "", "write-tree")
	if err = cmp.Or(err, wrote); err != nil {
		return "", err
	}
	return own.git(dir, "", "commit-tree", tree, "-p", head, "-m", "work not committed yet")
}
