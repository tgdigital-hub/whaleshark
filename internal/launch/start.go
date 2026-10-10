package launch

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/guide"
)

const (
	defaultKind = "claude"
	// The terminals take a start timeout above three seconds and of at most
	// five minutes.
	startTimeout  = 3 * time.Minute
	promptTimeout = 30 * time.Second
	maxAgentName  = 32

	working     = "working"
	startFailed = "start_failed"
	refused     = "refused"
)

// outcome is how the start of one task ended. Tab is set while the tab
// exists: a failed start leaves it open to be looked at.
type outcome struct {
	Task    string            `json:"task"`
	Name    string            `json:"name"`
	Attempt string            `json:"attempt,omitempty"`
	Tab     string            `json:"tab,omitempty"`
	Resumed string            `json:"resumed,omitempty"` // the conversation it continues
	State   string            `json:"state"`
	Error   *contract.Refusal `json:"error,omitempty"`
}

// kind is how one kind of agent's program takes what a start hands it, by
// its own manual: the option that gives it one more folder, whether that
// option stands before each folder, and the words that continue a
// conversation, which its id follows. Codex's are read from its maker's
// pages and have not been run. A kind that is not here is started with its
// model and the project's arguments and nothing else.
type kind struct {
	dir    string
	each   bool
	resume []string
}

var kinds = map[string]kind{
	defaultKind: {"--add-dir", false, []string{"--resume"}},
	"codex":     {"--add-dir", true, []string{"resume"}},
}

const heldArgs = "[agent] args is not approved, so workers are started without it"

// heldSays is what a start tells the lead agent, once while it is unread, of
// each setting of the project that nobody approved and that is not used.
var heldSays = [][2]string{
	{"agent.args", heldArgs},
	{"worktrees.dir", "[worktrees] dir is not approved, so copies of the code are made in the usual place"},
	{"worktrees.share", "[worktrees] share is not approved, so nothing is shared into a copy of the code"},
	{"check.timeout_seconds", "[check] timeout_seconds is not approved, so a check is ended after the usual time"},
}

type launcher struct {
	c             *contract.Call
	state         string // the login's state folder, where the pause mark is
	self          string
	snap          *contract.Snapshot // taken for a retry: what the earlier attempt's pane holds now
	retry, single bool
	resume, allow bool // continue the earlier conversation; start beside a shared task on the same files
	agent, model  string
	project       contract.ProjectFile
	out           sync.Mutex
	first         bool      // the run has no worktree yet
	left          sync.Once // what a worktree lacks is said once
}

// start brings up a worker for each task named, or for every ready one, side
// by side, each in a tab that carries the task's name.
func start(c *contract.Call) (any, error) {
	ready := c.Flags["ready"] != nil
	switch {
	case ready == (len(c.Args) > 0):
		return nil, usage(c, "start takes tasks, or --ready.")
	case c.Flags["resume"] != nil && c.Flags["retry"] == nil:
		return nil, usage(c, "--resume goes with --retry: it continues the conversation of the attempt before.")
	case os.Getenv(contract.EnvDepth) != "":
		return nil, refuse(contract.ExitRefused, "worker", "A worker starts no workers.")
	}
	k := c.Kit
	l := &launcher{c: c, retry: c.Flags["retry"] != nil, resume: c.Flags["resume"] != nil, allow: c.Flags["allow-overlap"] != nil,
		agent: last(c, "agent"), model: last(c, "model")}
	s, err := read(c)
	if err == nil {
		l.project, err = contract.ReadProjectFile(c.Root)
	}
	if err != nil {
		return nil, refusal(err)
	}
	l.first = !slices.ContainsFunc(slices.Collect(maps.Values(s.Tasks)), func(t *contract.Task) bool { return t.Worktree != nil })
	var ids, argv, names []string
	waiting := 0
	if ready {
		live := 0
		for _, a := range s.Attempts {
			if a.State.Live() {
				live++
			}
		}
		for id, t := range s.Tasks {
			if t.Status == contract.TaskReady && (l.retry || len(t.Attempts) == 0) {
				ids = append(ids, id)
			}
		}
		slices.SortFunc(ids, byID)
		if free := max(s.Run.Limit-live, 0); len(ids) > free {
			ids, waiting = ids[:free], len(ids)-free
		}
		argv, names = []string{"--ready"}, []string{"ready"}
	}
	for _, arg := range c.Args {
		t, err := k.Rules.FindTask(s, arg)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(ids, t.ID) {
			ids, argv, names = append(ids, t.ID), append(argv, t.ID), append(names, t.Name)
		}
	}
	for _, name := range []string{"retry", "resume", "allow-overlap"} {
		if c.Flags[name] != nil {
			argv = append(argv, "--"+name)
		}
	}
	if l.agent != "" {
		argv = append(argv, "--agent", l.agent)
	}
	if l.model != "" {
		if err := plain(l.model); err != nil {
			return nil, err
		}
		argv = append(argv, "--model", l.model)
	}
	if moved, err := OwnTab(c, strings.Join(names, ", "), argv...); moved != nil || err != nil {
		return moved, err
	}
	if l.retry {
		if l.snap, err = k.Terms.Snapshot(context.Background()); err != nil {
			return nil, refusal(err)
		}
	}
	dirs, err := k.Platform.Dirs()
	if err == nil {
		l.state = dirs.State
		l.self, err = k.Platform.SelfPath()
	}
	if err != nil {
		return nil, err
	}

	if len(ids) == 0 {
		fmt.Fprintln(c.Out, "Nothing is ready to start.")
	}
	out := make([]outcome, len(ids))
	l.single = len(ids) == 1
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Go(func() {
			out[i] = l.bring(id)
			l.say(out[i])
		})
	}
	wg.Wait()
	if waiting > 0 {
		fmt.Fprintf(c.Out, "%d more ready tasks wait for a free place: the limit is %d agents.\n", waiting, s.Run.Limit)
	}
	var worst *outcome
	bad, created := 0, []outcome{}
	// The surroundings failing outranks a start that failed, and that one a refusal.
	rank := func(o *outcome) int {
		switch {
		case o.Error.Exit == contract.ExitEnv:
			return 2
		case o.State == startFailed:
			return 1
		}
		return 0
	}
	for i := range out {
		o := &out[i]
		if o.Error == nil {
			continue
		}
		if o.State == startFailed {
			created = append(created, *o)
		}
		if bad++; worst == nil || rank(o) > rank(worst) {
			worst = o
		}
	}
	switch {
	case bad == 0:
		CloseOwnTab(c)
		return map[string]any{"started": out, "waiting": waiting}, nil
	case len(out) == 1:
		return nil, worst.Error
	}
	r := refuse(worst.Error.Exit, worst.Error.Code, "%d of %d tasks did not start.", bad, len(out))
	r.Next, r.Data = []string{line(c, "status")}, map[string]any{"created": created, "tasks": out}
	return nil, r
}

// plain refuses a model's name that is more than a word: it goes onto an
// agent's command line, and with a button onto ours in a tab.
func plain(model string) error {
	if cli.Valid("pane", model, "") != nil {
		return refuse(contract.ExitUsage, "invalid_value", "The model %.40q is not a plain word of letters, digits, dots, colons and dashes.", model)
	}
	return nil
}

func last(c *contract.Call, flag string) string {
	if v := c.Flags[flag]; len(v) > 0 {
		return v[len(v)-1]
	}
	return ""
}

// say prints how one task's start ended, as each settles.
func (l *launcher) say(o outcome) {
	l.out.Lock()
	defer l.out.Unlock()
	if o.Error == nil {
		how := ""
		if o.Resumed != "" {
			how = ", in the conversation " + o.Resumed
		} else if l.resume {
			how = ", in a new conversation: the attempt before left none that this agent can go on in"
		}
		fmt.Fprintf(l.c.Out, "%s %s: working (%s)%s\n", o.Task, o.Name, o.Attempt, how)
	} else if !l.single { // a single one is the command's own error
		fmt.Fprintf(l.c.Out, "%s %s: %s\n", o.Task, o.Name, cli.Plain(o.Error.Message))
	}
}

// agentName is the name an attempt's agent has in the terminals, where it
// must be unique for the login: two projects can both have a run r3 with a
// task T3, so it begins with a mark of the project's folder.
func agentName(root, run, attempt string) string {
	sum := sha256.Sum256([]byte(root))
	return strings.ToLower(fmt.Sprintf("h%x-%s-%s", sum[:2], run, strings.ReplaceAll(attempt, ".", "-")))
}

var errPaused = refuse(contract.ExitRefused, "paused", "All work is paused.")

// step is one short locked step of a start. It reads the login's pause mark
// first, which Stop all writes before it marks any run.
func (l *launcher) step(fn func(*contract.State) error) error {
	if p, err := contract.Paused(l.c.Kit.Platform.Peek, l.state); err != nil {
		return err
	} else if p != nil {
		return errPaused
	}
	return change(l.c, fn)
}

// bring is the staged start of one task: the attempt is written as starting
// under the lock, and then, outside it and under the attempt's start.lock,
// the tab is opened, the agent started and its first prompt delivered, each
// result written back in a step that gives up when the attempt is no longer
// starting or the person has pressed Stop all.
func (l *launcher) bring(task string) outcome {
	c, k := l.c, l.c.Kit
	r := outcome{Task: task, State: refused}
	fail := func(err error) outcome {
		r.Error = refusal(err)
		return r
	}
	s, err := k.Store.Read(c.Root, c.Run)
	if err != nil {
		return fail(err)
	}
	t := s.Tasks[task]
	r.Name = t.Name
	var prev *contract.Attempt
	if n := len(t.Attempts); n > 0 {
		prev = s.Attempts[t.Attempts[n-1]]
	}
	holds := false
	if prev != nil {
		was := paneOf(l.snap, prev.Place.Pane)
		holds = was != nil && was.Agent != ""
	}
	agent := contract.Agent{Kind: cmp.Or(l.agent, t.Agent, defaultKind), Model: cmp.Or(l.model, t.Model)}
	// A conversation is continued by the kind that held it, under the id
	// the keeper reported; one that could be read as an option is nobody's.
	var args []string
	if how := kinds[agent.Kind].resume; l.resume && prev != nil && prev.Agent.Kind == agent.Kind && len(how) > 0 &&
		prev.Agent.Session != "" && prev.Agent.Session[0] != '-' {
		agent.Session, r.Resumed = prev.Agent.Session, prev.Agent.Session
		args = append(slices.Clone(how), agent.Session)
	}
	if agent.Model != "" {
		if err := plain(agent.Model); err != nil {
			return fail(err)
		}
	}
	secret := make([]byte, 32)
	rand.Read(secret)
	token := hex.EncodeToString(secret)

	// Tried first on our own copy of the record: it says whether the start
	// would be refused, and which attempt it will be, before anything exists.
	id, err := k.Rules.Start(s, task, l.retry, holds, contract.TokenHash(token), agent, false, c.Now)
	if err != nil {
		return fail(err)
	}
	if agent.Name = agentName(c.Root, c.Run, id); len(agent.Name) > maxAgentName {
		return fail(refuse(contract.ExitRefused, "name_too_long", "The agent of %s would be named %s, which is longer than %d characters.", id, agent.Name, maxAgentName))
	}
	run := k.Store.Dir(c.Root, c.Run)
	dir := contract.AttemptDir(run, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fail(err)
	}
	unlock, ok, err := k.Platform.TryLock(filepath.Join(dir, "start.lock"))
	if err != nil {
		return fail(err)
	} else if !ok {
		return fail(refuse(contract.ExitRefused, "starting", "%s is being started by another command.", id))
	}
	defer unlock()
	had := t.Worktree != nil && t.Worktree.RemovedAt.IsZero()
	folder, tree, err := k.Placement.Place(c.Root, s, task)
	if err != nil {
		return fail(err)
	}
	if !filepath.IsAbs(folder) {
		folder = filepath.Join(c.Root, folder)
	}
	// The task's own copy of the code exists before its attempt does. A start
	// that is refused from here on takes away the copy this call made, and
	// the ports of a task that has never had an attempt.
	var slot contract.Slot
	undo := func(err error) outcome {
		if tree != nil && !had {
			t.Worktree = tree
			k.Placement.Remove(c.Root, s, task, true)
		}
		if prev == nil {
			contract.FreeSlots(k.Platform, func(o contract.Slot) bool { return o == slot })
		}
		return fail(err)
	}
	if tree != nil && tree.Setup == contract.SetupFailed {
		return undo(refuse(contract.ExitFailed, "setup_failed", "The setup of the copy of the code for %s failed. Its log: %s", task, tree.Path+".setup.log"))
	}
	if tree != nil && !had && l.first {
		l.left.Do(l.lacks)
	}
	// The task's ports are its own whether or not it has a copy of the code.
	slot, err = contract.TakeSlot(k.Platform, contract.DefaultPorts, c.Root, c.Run, task, l.project.Worktrees.PortBlock)
	if errors.Is(err, contract.ErrNoSlot) {
		r := refuse(contract.ExitRefused, "no_slot", "No port is free for %s: every port of this login is some task's. Closing settled tasks gives theirs back.", task)
		r.Next = []string{line(c, "close --settled")}
		err = r
	}
	if err != nil {
		return undo(err)
	}
	if tree != nil {
		tree.Slot = slot.N
	}
	// The hooks go into the folder's own settings only where the folder is
	// this worker's alone, and git is told not to see that file there; else
	// they ride on the agent's command line.
	setup, err := k.AgentSettings.Ensure(agent.Kind, folder, dir, tree != nil)
	if err == nil && tree != nil && setup.File != "" {
		err = k.Placement.Hide(c.Root, folder, setup.File)
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, "token"), []byte(token+"\n"), 0o600)
	}
	if err != nil {
		return undo(err)
	}
	err = l.step(func(s *contract.State) error {
		got, err := k.Rules.Start(s, task, l.retry, holds, contract.TokenHash(token), agent, setup.Gated, contract.Now())
		if err == nil && got != id {
			err = refuse(contract.ExitRefused, "starting", "%s was started by another command.", task)
		}
		if other := shares(s, s.Tasks[task]); err == nil && tree == nil && !l.allow && other != "" {
			r := refuse(contract.ExitRefused, "shared_overlap", "%s would work in one folder with %s, which is at work on files %s may change too.", task, other, task)
			r.Next = []string{line(c, "start "+task+" --allow-overlap")}
			err = r
		}
		if err != nil {
			return err
		}
		for _, say := range heldSays {
			told := func(e contract.Event) bool { return e.Kind == "untrusted" && e.Text == say[1] }
			if slices.Contains(l.project.Held, say[0]) && (tree != nil || !strings.HasPrefix(say[0], "worktrees.")) && !slices.ContainsFunc(s.Inbox.Events, told) {
				k.Rules.Raise(s, contract.Event{Kind: "untrusted", Text: say[1]}, contract.Now())
			}
		}
		if tree == nil {
			return nil
		}
		said := slices.ContainsFunc(slices.Collect(maps.Values(s.Tasks)), func(t *contract.Task) bool {
			return t.Worktree != nil && t.Worktree.Setup == contract.SetupUntrusted
		})
		if tree.Setup == contract.SetupUntrusted && !said {
			k.Rules.Raise(s, contract.Event{Kind: "untrusted", Text: "[setup] script is not approved, so no copy of the code is set up by it"}, contract.Now())
		}
		return k.Rules.SetWorktree(s, task, tree, contract.Now())
	})
	if err != nil {
		return undo(err)
	}
	r.Attempt = id

	env := []string{contract.EnvRoot + "=" + c.Root, contract.EnvRun + "=" + c.Run, contract.EnvTask + "=" + task,
		contract.EnvAttempt + "=" + id, contract.EnvDepth + "=1", contract.EnvBin + "=" + l.self}
	if slot.Size > 0 {
		env = append(env, slot.Env(contract.DefaultPorts)...)
	}
	pane, err := k.Terms.TabCreate(folder, t.Name, env)
	if err != nil {
		return l.failed(r, err, "the tab could not be opened", false)
	}
	r.Tab = pane.Tab
	place := contract.Place{Tab: pane.Tab, Pane: pane.ID, Terminal: pane.Terminal, Workspace: pane.Workspace, Cwd: folder, OpenedByUs: true}
	placed := func(s *contract.State) error { return k.Rules.Placed(s, id, place, contract.Now()) }
	if err := l.step(placed); err != nil {
		return l.failed(r, err, "the start stopped", false)
	}
	args = append(args, setup.Args...)
	// In a copy of the code of its own, the attempt's files and the brief
	// lie outside the agent's folder: it is given the two folders, or it
	// stops to ask before it reads its own prompt.
	if how := kinds[agent.Kind]; tree != nil && how.dir != "" {
		for i, folder := range []string{dir, filepath.Dir(filepath.Join(run, t.Brief))} {
			if i == 0 || how.each {
				args = append(args, how.dir)
			}
			args = append(args, folder)
		}
	}
	// The project's own arguments, which are empty until a person approved
	// them. A project cannot know where this program lies on each machine,
	// and a worker is told to call it by its path: {whaleshark} stands for it.
	for _, arg := range l.project.Agent.Args {
		args = append(args, strings.ReplaceAll(arg, "{whaleshark}", l.self))
	}
	if agent.Model != "" {
		args = append(args, "--model", agent.Model)
	}
	if err := k.Terms.AgentStart(agent.Name, agent.Kind, pane.ID, args, startTimeout); err != nil {
		return l.failed(r, err, "the agent did not start", errors.Is(err, contract.ErrAgentNotReady))
	}
	prompt := filepath.Join(dir, "prompt.md")
	// A task that was sent to bring its copy up to date and has not done so
	// is told again by whoever takes it up.
	var sync []byte
	if t.SyncBrief != "" {
		sync, _ = k.Platform.Peek(filepath.Join(run, t.SyncBrief))
	}
	text := guide.Prompt{Bin: k.Platform.Quote("", []string{l.self}), Run: c.Run, Task: task, Name: t.Name, Dir: folder,
		Brief: filepath.Join(run, t.Brief), Result: filepath.Join(dir, "result.md"), Owns: t.Owns,
		Shared: tree == nil, Decisions: t.Decisions, Sync: strings.TrimSpace(string(sync))}.Text() + earlier(prev)
	if err = os.WriteFile(prompt, []byte(text), 0o600); err == nil {
		err = l.step(placed) // nothing is typed once the attempt was stopped or the person pressed Stop all
	}
	if err != nil {
		return l.failed(r, err, "the start stopped", false)
	}
	if err := k.Terms.Prompt(agent.Name, "Read and follow "+prompt, promptTimeout); err != nil {
		return l.failed(r, err, "the first prompt was not delivered", errors.Is(err, contract.ErrAgentBlocked))
	}
	if err := l.step(func(s *contract.State) error { return k.Rules.Working(s, id, contract.Now()) }); err != nil {
		return l.failed(r, err, "the start stopped", false)
	}
	r.State = working
	return r
}

// shares names a task at work in a shared folder on files t may change too,
// or nothing. Two patterns meet when the fixed beginning of one, up to its
// first wildcard, begins the other's: that never misses a file both match,
// and can take two patterns for meeting that share none.
func shares(s *contract.State, t *contract.Task) string {
	fixed := func(p string) string {
		if i := strings.IndexAny(p, "*?["); i >= 0 {
			return p[:i]
		}
		return strings.TrimSuffix(p, "/") + "/"
	}
	for _, a := range s.Attempts {
		o := s.Tasks[a.Task]
		if !a.State.Live() || o.ID == t.ID || o.Worktree != nil && o.Worktree.RemovedAt.IsZero() {
			continue
		}
		for _, p := range t.Owns {
			for _, q := range o.Owns {
				if p, q := fixed(p), fixed(q); strings.HasPrefix(p, q) || strings.HasPrefix(q, p) {
					return o.ID
				}
			}
		}
	}
	return ""
}

// lacks says, at the first copy of the code a run makes, what git ignores in
// the project and such a copy therefore does not have, and how to change that.
func (l *launcher) lacks() {
	n, some, err := l.c.Kit.Placement.Left(l.c.Root)
	if err != nil || n == 0 {
		return
	}
	more := ""
	if n > len(some) {
		more = ", ..."
	}
	l.out.Lock()
	defer l.out.Unlock()
	fmt.Fprintf(l.c.Out, "%d ignored files or folders of the project are not in a task's own copy of the code (%s%s). "+
		"To copy one there, name it on a line of .worktreeinclude; to link one, name it in whaleshark.toml, [worktrees] share = [\"%s\"], and have the person run whaleshark trust.\n",
		n, cli.Plain(strings.Join(some, ", ")), more, cli.Plain(some[0]))
}

// earlier is what a retry is told of the attempt before it: the prompt's own
// text has no place for it, so it follows it.
func earlier(a *contract.Attempt) string {
	if a == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n## The attempt before this one\n%s ended as %s.\n", a.ID, a.State)
	if p := a.Progress; p != nil {
		fmt.Fprintf(&b, "Its last progress note: %d%% %s\n", p.Pct, p.Note)
	}
	if rep := a.Report; rep != nil {
		fmt.Fprintf(&b, "Its last report: %s. %s\n", rep.Outcome, rep.Summary)
	}
	return b.String()
}

// failed ends a start that got no further. The attempt becomes start_failed
// and its tab stays open to be looked at; a start that was overtaken, by a
// stop or by another command, closes what it opened instead. atPrompt is the
// agent waiting at a question of its own, which only the person may answer:
// that is no failure of the terminals, and it becomes a to-do item.
func (l *launcher) failed(r outcome, cause error, what string, atPrompt bool) outcome {
	c, k := l.c, l.c.Kit
	e := refuse(contract.ExitFailed, startFailed, "%s: %v", what, cause)
	var given *contract.Refusal
	switch {
	case atPrompt:
		e = refuse(contract.ExitFailed, "at_prompt", "the agent is waiting at a prompt in its tab, which only the person answers")
	case errors.Is(cause, contract.ErrPromptStalled):
		e = refuse(contract.ExitFailed, "prompt_stalled", "the prompt was typed and the agent did not start on it; nothing is typed again")
	case errors.Is(cause, contract.ErrEngineUnreachable):
		e = refuse(contract.ExitEnv, "unreachable", "%v", cause)
	case errors.Is(cause, contract.ErrNoPane):
		e = refuse(contract.ExitFailed, "no_pane", "its tab or its agent is gone")
	case errors.As(cause, &given):
		e = refuse(contract.ExitFailed, given.Code, "%s", strings.TrimSuffix(given.Message, "."))
	}
	why := e.Message
	err := k.Store.Change(c.Root, c.Run, func(s *contract.State) error {
		err := k.Rules.StartFailed(s, r.Attempt, why, contract.Now())
		if err == nil && atPrompt {
			_, err = k.Rules.Need(s, contract.Question{Form: contract.FormTodo, From: contract.FromTool, Cause: contract.CausePrompt,
				Task: r.Task, Attempt: r.Attempt, Text: r.Name + " is waiting at a prompt in its tab."}, contract.Now())
		}
		return err
	})
	if err != nil {
		r.Error = refusal(err)
		if r.Error.Code == "not_starting" && r.Tab != "" && k.Terms.TabClose(r.Tab) == nil {
			r.Tab = ""
		}
		return r
	}
	r.State = startFailed
	e.Message = fmt.Sprintf("%s did not start: %s.", r.Attempt, why)
	e.Data = map[string]any{"created": []outcome{r}}
	// At a prompt there is nothing for an agent to run: the item is the person's.
	if !atPrompt {
		e.Next = []string{line(c, "close "+r.Task), line(c, "start "+r.Task+" --retry")}
	}
	r.Error = e
	return r
}
