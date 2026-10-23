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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/guide"
)

const (
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

const heldArgs = "[agent] args is not approved, so workers are started without it"

// heldSays is what a start tells the lead agent, once while it is unread, of
// each setting of the project that nobody approved and that is not used.
var heldSays = [][2]string{
	{"agent.args", heldArgs},
	{"worktrees.dir", "[worktrees] dir is not approved, so copies of the code are made in the usual place"},
	{"worktrees.share", "[worktrees] share is not approved, so nothing is shared into a copy of the code"},
	{"check.timeout_seconds", "[check] timeout_seconds is not approved, so a check is ended after the usual time"},
	{"limits.agents", "[limits] agents is above the usual number and not approved, so no more agents than usual run at once"},
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
	server        bool
	block         contract.Ports // the login's ports: its block on a server
	room, cap     int            // on a server with a cap: the agents this run may hold, and the login
	// In a job of fleets: the orchestrator's run, and in a fleet's own run
	// the fleet's name. tool is the one of an agent nobody named one for,
	// depth how deep its tab is.
	up, fleet, tool, depth string
	person                 contract.PersonConfig // the limits are the person's, read at this start
	tools                  []contract.Tool
}

func live(s *contract.State) (n int) {
	seen := map[string]bool{} // a leader with two groups is two attempts in one pane, and one agent
	for _, a := range s.Attempts {
		if p := a.Place.Pane; a.State.Live() && !seen[p] {
			n, seen[p] = n+1, p != ""
		}
	}
	return n
}

// over refuses a run that holds more agents than the server leaves it.
func (l *launcher) over(s *contract.State, room int) error {
	if l.server && live(s) > room {
		return refuse(contract.ExitRefused, "limit", "This login has as many agents live as this server lets it run.")
	}
	return nil
}

// others counts the agents live in every other run of the login.
func (l *launcher) others() (n int, err error) {
	c, k := l.c, l.c.Kit
	all, err := targets(c, l.state, true)
	for _, t := range all {
		if o, err := k.Store.Read(t.root, t.run); err == nil && !(t.run == c.Run && k.Platform.PathKey(t.root) == k.Platform.PathKey(c.Root)) {
			n += live(o)
		}
	}
	return n, err
}

// exact is the room this run has at the moment a start is written. Where a
// server caps the login, the other projects are counted again under a lock
// over the login, held until unlock: two projects started in one instant
// would else each count the other before it had written. A start in a job
// of fleets takes the same lock, for the person's limits count over runs.
func (l *launcher) exact() (room int, unlock func(), err error) {
	if l.cap == 0 && l.up == "" {
		return l.room, func() {}, nil
	}
	if unlock, err = contract.LoginLock(l.c.Kit.Platform); err != nil {
		return l.room, func() {}, err
	}
	if unlock = sync.OnceFunc(unlock); l.cap == 0 {
		return l.room, unlock, nil
	}
	n, err := l.others()
	return l.cap - n, unlock, err
}

// limits reads the runs the person's limits are counted over and returns the
// question a step asks with its own run as it holds it: is there room for a
// group of fleet, or with worker for that task of the fleet's own run. The
// store is read here, since a step calls nothing outside.
func (l *launcher) limits(fleet, worker string) (func(*contract.State) error, error) {
	c, k := l.c, l.c.Kit
	if l.up == "" {
		return func(*contract.State) error { return nil }, nil
	}
	up, err := k.Store.Read(c.Root, l.up)
	if err != nil {
		return nil, err
	}
	lows := map[string]*contract.State{}
	for name, f := range up.Run.Fleets {
		if low, err := k.Store.Read(c.Root, f.Run); err == nil {
			lows[name] = low
		}
	}
	return func(s *contract.State) error {
		if worker == "" {
			up = s
		} else {
			lows[fleet] = s
		}
		return k.Rules.Room(up, lows, fleet, worker, l.person, l.cap)
	}, nil
}

// fleetRun makes the run that is a new fleet, from the tip of what the
// orchestrator has collected so far, under the lowest number that is free.
// Its caller holds the login's lock: two leaders get a number each.
func (l *launcher) fleetRun(name string) (string, error) {
	c, k := l.c, l.c.Kit
	up, err := k.Store.Read(c.Root, c.Run)
	if err != nil {
		return "", err
	}
	var base *contract.GitRef
	if in := up.Run.Integration; in != nil {
		base = &contract.GitRef{Ref: in.Branch}
	}
	runs, _ := k.Store.Runs(c.Root)
	for n, taken := 1, new(*contract.Refusal); ; n++ {
		s := k.Rules.NewRun("r"+strconv.Itoa(n), name, l.project.Limits.Agents, base, contract.Binding{}, contract.Now())
		if slices.Contains(runs, s.Run.ID) {
			continue
		}
		from, in, err := k.Integrator.Begin(c.Root, s)
		if errors.As(err, taken) && (*taken).Code == "branch_exists" {
			continue // a removed run left work on the branch of that number
		}
		if err == nil && in != nil {
			s.Run.Base = from
			err = k.Rules.Integrated(s, *in)
		}
		if err == nil {
			err = k.Store.Create(c.Root, s)
		}
		return s.Run.ID, err
	}
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
	case os.Getenv(contract.EnvDepth) != "" && (c.Caller.Kind != contract.Leader || os.Getenv(contract.EnvDepth) != "1"):
		return nil, refuse(contract.ExitRefused, "worker", "A worker starts no workers.")
	}
	k := c.Kit
	l := &launcher{c: c, retry: c.Flags["retry"] != nil, resume: c.Flags["resume"] != nil, allow: c.Flags["allow-overlap"] != nil,
		agent: last(c, "agent"), model: last(c, "model")}
	s, err := read(c)
	if err != nil && refusal(err).Code == "paused" {
		if job, _ := k.Store.Read(c.Root, c.Run); guarded(job) {
			err = overBudget(c, job, "Nothing is started")
		}
	}
	if err == nil {
		l.project, err = contract.ReadProjectFile(c.Root)
	}
	if err != nil {
		return nil, refusal(err)
	}
	l.first = !slices.ContainsFunc(slices.Collect(maps.Values(s.Tasks)), func(t *contract.Task) bool { return t.Worktree != nil })
	dirs, err := k.Platform.Dirs()
	if err == nil {
		l.state = dirs.State
		l.self, err = k.Platform.SelfPath()
	}
	if err == nil {
		l.person, err = contract.ReadPerson(k.Platform.Peek, dirs.Config)
	}
	if err != nil {
		return nil, err
	}
	l.tools, _ = contract.Tools(k.Platform, dirs)
	l.tool, l.depth = contract.DefaultTool, "1"
	if p := s.Run.Parent; p != nil {
		l.up, l.fleet, l.tool, l.depth = p.Run, p.Fleet, l.person.Fleets.Worker, strconv.Itoa(contract.MaxDepth)
		if up, err := k.Store.Read(c.Root, p.Run); err == nil && l.tool == contract.FleetWorkerLeader && up.Run.Fleets[p.Fleet] != nil {
			l.tool = up.Run.Fleets[p.Fleet].Tool
		}
	} else if s.Run.Mode == contract.ModeFleets {
		l.up, l.tool = c.Run, l.person.Fleets.Leader
	}
	// A server's file says which ports are this login's and how many agents
	// it may run, over all its projects; a machine without one is a person's own.
	srv, err := contract.ReadServer(k.Platform.Peek)
	if err != nil {
		return nil, refuse(contract.ExitEnv, "server_file", "Nothing is started on a server whose file cannot be used: %v.", err)
	}
	me, limit := contract.Me(), s.Run.Limit
	if n := l.person.Fleets.Agents; l.fleet != "" && n != contract.Unlimited {
		limit = min(limit, n) // --ready starts what fits in the fleet
	}
	own := limit
	var listed bool
	if l.block, listed = srv.Block(me); !listed {
		return nil, refuse(contract.ExitEnv, "no_block", "The login %s is not one of this server's, so it starts nothing here. The administrator adds it with whaleshark server add.", me)
	}
	if l.cap, l.server = srv.Cap(me), srv != nil; l.cap > 0 {
		n, err := l.others()
		if err != nil {
			return nil, refusal(err)
		}
		l.room = l.cap - n
		limit = min(limit, l.room)
	} else {
		l.room = limit
	}
	var ids, argv, names []string
	waiting := 0
	if ready {
		for id, t := range s.Tasks {
			if t.Status == contract.TaskReady && (l.retry || len(t.Attempts) == 0) {
				ids = append(ids, id)
			}
		}
		slices.SortFunc(ids, byID)
		if free := max(limit-live(s), 0); len(ids) > free {
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
	// A start reaches no agent while the engine is not running: it is
	// refused before an attempt is written, so it counts as no try.
	if len(ids) > 0 {
		snap, err := k.Terms.Snapshot(context.Background())
		if err != nil {
			return nil, down(c, err)
		}
		if l.retry {
			l.snap = snap
		}
	}
	if moved, err := OwnTab(c, strings.Join(names, ", "), argv...); moved != nil || err != nil {
		return moved, err
	}
	// Plan first: in a job nothing starts that the person has not approved,
	// and their own start is their yes. The refusal is kept: the item it
	// opened or brought up to date is saved, and the start ends with it.
	if l.person.Plan.First && s.Run.Parent == nil && len(contract.Unplanned(s, ids)) > 0 {
		var kept *contract.Refusal
		err := change(c, func(s *contract.State) error {
			text := k.PlanText(s, nil, contract.Unplanned(s, ids), l.person)
			kept = k.Rules.Planned(s, ids, text, &contract.Origin{Caller: c.Caller.Kind, Where: c.Caller.Where}, c.Now)
			return nil
		})
		if err != nil {
			return nil, err
		} else if kept != nil {
			return nil, kept
		}
	}

	if len(ids) == 0 {
		fmt.Fprintln(c.Out, "Nothing is ready to start.")
	}
	out := make([]outcome, len(ids))
	l.single = len(ids) == 1
	var wg sync.WaitGroup
	var groups []func()
	for i, id := range ids {
		bring := func() {
			out[i] = l.bring(id)
			l.say(out[i])
		}
		// A group holds the login's lock for the whole of its start, so
		// groups go one after another whatever is done here: in the order
		// they were named, and where the leaders' limit ends them it is the
		// last named that wait.
		if l.fleet == "" && s.Tasks[id].Fleet != "" {
			groups = append(groups, bring)
		} else {
			wg.Go(bring)
		}
	}
	wg.Go(func() {
		for _, bring := range groups {
			bring()
		}
	})
	wg.Wait()
	if wait := map[bool]string{true: " waits", false: "s wait"}[waiting == 1]; waiting > 0 && limit < own {
		fmt.Fprintf(c.Out, "%d more ready task%s for a free place: this server lets this login run %d agents.\n", waiting, wait, srv.Cap(me))
	} else if waiting > 0 {
		fmt.Fprintf(c.Out, "%d more ready task%s for a free place: the limit is %d agents.\n", waiting, wait, limit)
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

// step is one short locked step of a start, with low named on the run and
// that fleet's run together. It reads the login's pause mark first, which
// Stop all writes before it marks any run.
func (l *launcher) step(low string, fn func(s, low *contract.State) error) error {
	if p, err := contract.Paused(l.c.Kit.Platform.Peek, l.state); err != nil {
		return err
	} else if p != nil {
		return errPaused
	}
	if low != "" {
		return both(l.c, low, fn)
	}
	return change(l.c, func(s *contract.State) error { return fn(s, nil) })
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
	// The room is counted under the login's lock, held until the start is
	// written. A group holds it from here until its leader is in the record:
	// a second group of a new fleet then finds the leader that sits, and no
	// two leaders are counted as one.
	room, free := l.room, func() {}
	defer func() { free() }()
	group := l.fleet == "" && s.Tasks[task].Fleet != ""
	if group {
		if room, free, err = l.exact(); err == nil {
			s, err = k.Store.Read(c.Root, c.Run)
		}
		if err != nil {
			return fail(err)
		}
	}
	t := s.Tasks[task]
	r.Name = t.Name
	var prev *contract.Attempt
	if n := len(t.Attempts); n > 0 {
		prev = s.Attempts[t.Attempts[n-1]]
	}
	// A group goes to its fleet's leader: the one that sits in its tab, or a
	// new one, in a run of the fleet's own unless the fleet has one open.
	fleet, worker := cmp.Or(l.fleet, t.Fleet), ""
	if !group {
		worker = task
	}
	f := s.Run.Fleets[t.Fleet]
	if f != nil && !f.EndedAt.IsZero() {
		f = nil
	}
	sits := f != nil && f.Pane != ""
	holds := false
	if prev != nil && !sits {
		was := paneOf(l.snap, prev.Place.Pane)
		holds = was != nil && was.Agent != ""
	}
	agent := contract.Agent{Kind: cmp.Or(l.agent, t.Agent, l.tool), Model: cmp.Or(l.model, t.Model)}
	if sits {
		agent = contract.Agent{Kind: f.Tool, Model: f.Model}
	}
	// A tool is named anywhere and described by the login alone: one that
	// is not wired in is refused before anything exists.
	tool, wired := contract.FindTool(l.tools, agent.Kind)
	if !wired || !tool.Found() {
		return fail(refuse(contract.ExitRefused, "no_tool", "%s", contract.NotWired(agent.Kind)))
	}
	// A conversation is continued by the tool that held it, under the id
	// the keeper reported; one that could be read as an option is nobody's.
	var args []string
	if l.resume && prev != nil && !sits && prev.Agent.Kind == agent.Kind && len(tool.Resume) > 0 &&
		prev.Agent.Session != "" && prev.Agent.Session[0] != '-' {
		agent.Session, r.Resumed = prev.Agent.Session, prev.Agent.Session
		args = contract.Fill(tool.Resume, "session", agent.Session)
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
	fits, err := l.limits(fleet, worker)
	if err == nil {
		err = fits(s)
	}
	var id string
	if err == nil {
		id, err = k.Rules.Start(s, task, l.retry, holds, contract.TokenHash(token), agent, false, c.Now)
	}
	if err == nil {
		err = l.over(s, l.room)
	}
	if err != nil {
		return fail(err)
	}
	if agent.Name = agentName(c.Root, c.Run, id); len(agent.Name) > maxAgentName {
		return fail(refuse(contract.ExitRefused, "name_too_long", "The agent of %s would be named %s, which is longer than %d characters.", id, agent.Name, maxAgentName))
	}
	run := k.Store.Dir(c.Root, c.Run)
	dir := contract.AttemptDir(run, id)
	evidence := filepath.Join(dir, "evidence") // what the worker's browser check saves (8l)
	if err := os.MkdirAll(evidence, 0o700); err != nil {
		return fail(err)
	}
	unlock, ok, err := k.Platform.TryLock(filepath.Join(dir, "start.lock"))
	if err != nil {
		return fail(err)
	} else if !ok {
		return fail(refuse(contract.ExitRefused, "starting", "%s is being started by another command.", id))
	}
	defer unlock()
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte(token+"\n"), 0o600); err != nil {
		return fail(err)
	}
	if sits {
		return l.hand(r, t, f.Run, id, contract.TokenHash(token), agent, tool.Held())
	}
	// A leader changes no code: it reads in the project's folder, and has
	// no copy of the code and no ports of its own.
	had := t.Worktree != nil && t.Worktree.RemovedAt.IsZero()
	folder, tree := c.Root, (*contract.Worktree)(nil)
	if !group {
		// With fetch_base the base is fetched before each start. In
		// pr-per-task mode a task is cut from the base itself, as fetched,
		// and never from the run's collected work.
		view, far := s, k.Integrator.Fetch(c.Root, s)
		if b := s.Run.Base; l.project.Land.Mode == contract.LandPR && b != nil && b.Ref != "" {
			v := *s
			v.Run.Integration, v.Run.Base = nil, &contract.GitRef{Ref: cmp.Or(far, b.Ref)}
			view = &v
		}
		if folder, tree, err = k.Placement.Place(c.Root, view, task); err != nil {
			return fail(err)
		}
	}
	if !filepath.IsAbs(folder) {
		folder = filepath.Join(c.Root, folder)
	}
	// The task's own copy of the code exists before its attempt does. A start
	// that is refused from here on takes away the copy this call made, and
	// the ports of a task that has never had an attempt.
	var slot contract.Slot
	var tmp string
	undo := func(err error) outcome {
		os.RemoveAll(tmp)
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
	if !group {
		slot, err = contract.TakeSlot(k.Platform, l.block, c.Root, c.Run, task, l.project.Worktrees.PortBlock)
	}
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
	// On a server the tab gets the browser every login shares and a temp
	// folder of the login's own, so nothing an agent's tools write lies in a
	// shared one. A person's own machine has both already. A leader's tab
	// is as deep as a worker's, and its own workers' one deeper: the last.
	env := []string{contract.EnvRoot + "=" + c.Root, contract.EnvRun + "=" + c.Run, contract.EnvTask + "=" + task,
		contract.EnvAttempt + "=" + id, contract.EnvDepth + "=" + l.depth, contract.EnvBin + "=" + l.self, contract.EnvEvidence + "=" + evidence}
	if err == nil && l.server {
		tmp, err = k.Platform.PrivateTemp()
		env = append(env, contract.EnvBrowsers+"="+contract.ServerBrowsers, "TMPDIR="+tmp)
	}
	if err != nil {
		return undo(err)
	}
	if !group {
		room, free, err = l.exact()
	}
	if err == nil {
		fits, err = l.limits(fleet, worker)
	}
	if err != nil {
		return undo(err)
	}
	err = l.step("", func(s, _ *contract.State) error {
		if err := fits(s); err != nil {
			return err
		}
		got, err := k.Rules.Start(s, task, l.retry, holds, contract.TokenHash(token), agent, setup.Gated, contract.Now())
		if err == nil && got != id {
			err = refuse(contract.ExitRefused, "starting", "%s was started by another command.", task)
		}
		if err == nil {
			err = l.over(s, room)
		}
		if other := shares(s, s.Tasks[task]); err == nil && tree == nil && !group && !l.allow && other != "" {
			r := refuse(contract.ExitRefused, "shared_overlap", "%s would work in one folder with %s, which is at work on files %s may change too.", task, other, task)
			r.Next = []string{line(c, "start "+task+" --allow-overlap")}
			err = r
		}
		if err != nil {
			return err
		}
		s.Attempts[id].Temp = tmp // recorded, so that stop, close and run rm take it away
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
	if !group {
		free()
	}
	if err != nil {
		return undo(err)
	}
	r.Attempt = id

	if slot.Size > 0 {
		env = append(env, slot.Env(l.block)...)
	}
	var found []string
	// The tab is of the project's workspace and, a leader's, the first of
	// its fleet's; opened from a pane it is that pane's, whatever is said.
	pane, err := k.Terms.TabCreate(l.c.Root, t.Fleet, folder, tabName(t), env)
	if err != nil {
		return l.failed(r, err, "the tab could not be opened", false)
	}
	r.Tab = pane.Tab
	place := contract.Place{Tab: pane.Tab, Pane: pane.ID, Terminal: pane.Terminal, Workspace: pane.Workspace, Cwd: folder, OpenedByUs: true}
	placed := func(s, _ *contract.State) error { return k.Rules.Placed(s, id, place, contract.Now()) }
	err = l.step("", placed)
	// The new leader's tab is its fleet's run's: the run the fleet has
	// open, which it takes over, or a new one.
	if low := ""; err == nil && group {
		if f != nil {
			low = f.Run
		} else {
			low, err = l.fleetRun(t.Fleet)
		}
		if err == nil {
			err = l.step(low, func(up, low *contract.State) error {
				branch := ""
				if in := low.Run.Integration; in != nil {
					branch = in.Branch
				}
				for _, a := range low.Attempts {
					if a.Place.OpenedByUs && a.Place.Tab != "" {
						found = append(found, a.Place.Tab)
					}
				}
				return k.Rules.Lead(up, low, id, contract.Binding{Pane: pane.ID, Workspace: pane.Workspace}, branch, contract.Now())
			})
		}
	}
	free()
	if err != nil {
		return l.failed(r, err, "the start stopped", false)
	}
	args = append(args, setup.Args...)
	// In a copy of the code of its own, the attempt's files and the brief
	// lie outside the agent's folder: it is given the two folders, or it
	// stops to ask before it reads its own prompt.
	if tree != nil {
		args = append(args, tool.Folders(dir, filepath.Dir(filepath.Join(run, t.Brief)))...)
	}
	// The project's own arguments, which are empty until a person approved
	// them, and are for the one tool they were approved for. A project cannot
	// know where this program lies on each machine, and a worker is told to
	// call it by its path: {whaleshark} stands for it.
	for _, arg := range l.project.Agent.Args {
		if tool.Name == l.project.Agent.Tool {
			args = append(args, strings.ReplaceAll(arg, "{whaleshark}", l.self))
		}
	}
	if agent.Model != "" {
		args = append(args, contract.Fill(tool.Model, "model", agent.Model)...)
	}
	if err := k.Terms.AgentStart(agent.Name, agent.Kind, pane.ID, args, startTimeout); err != nil {
		return l.failed(r, err, "the agent did not start", errors.Is(err, contract.ErrAgentNotReady))
	}
	// The tabs the leader before opened for its workers are the new leader's
	// now, to prompt, stop and close; a tab that is gone is nobody's. Only
	// now: a pane's start of an agent ends what that pane was given before.
	for _, tab := range found {
		k.Terms.TabGive(tab, pane.ID)
	}
	prompt := filepath.Join(dir, "prompt.md")
	// A task that was sent to bring its copy up to date and has not done so
	// is told again by whoever takes it up.
	var sync []byte
	if t.SyncBrief != "" {
		sync, _ = k.Platform.Peek(filepath.Join(run, t.SyncBrief))
	}
	browser := guide.Browser(t.BrowserCheck, k.Platform.System(), l.server)
	if k.Platform.System() == "windows" {
		browser = guide.OnWindows(browser, k.Platform.Quote, env, os.Getenv)
	}
	pr := ""
	if l.project.Land.Mode == contract.LandPR && tree != nil {
		pr = filepath.Join(dir, contract.PRBody)
	}
	text := guide.Prompt{Bin: k.Platform.Quote("", []string{l.self}), Run: c.Run, Task: task, Name: t.Name, Dir: folder,
		Brief: filepath.Join(run, t.Brief), Result: filepath.Join(dir, "result.md"), Owns: t.Owns,
		Shared: tree == nil, Decisions: t.Decisions, Sync: strings.TrimSpace(string(sync)), Browser: browser,
		Fleet: t.Fleet, Person: l.person, Tools: l.tools, PR: pr}.Text() + earlier(prev)
	if err = os.WriteFile(prompt, []byte(text), 0o600); err == nil {
		err = l.step("", placed) // nothing is typed once the attempt was stopped or the person pressed Stop all
	}
	if err != nil {
		return l.failed(r, err, "the start stopped", false)
	}
	if err := k.Terms.Prompt(agent.Name, "Read and follow "+prompt, promptTimeout); err != nil {
		return l.failed(r, err, "the first prompt was not delivered", errors.Is(err, contract.ErrAgentBlocked))
	}
	if err := l.step("", func(s, _ *contract.State) error { return k.Rules.Working(s, id, contract.Now()) }); err != nil {
		return l.failed(r, err, "the start stopped", false)
	}
	r.State = working
	return r
}

// newWork is the message a leader that sits is handed a group with. The
// line typed into its tab is the pointer to its mail.
const newWork = "New work for your fleet: group %s %q. Its brief: %s  Its result file: %s  It is yours now: plan it and start its workers at once, as you did your first group, and report it with --task %[1]s."

// hand gives a group to the leader that sits in its fleet's tab. No agent
// starts, so no tab is opened: the attempt takes the leader's place, the
// group is put into the leader's mail and one line is typed that says so.
// The attempt's start.lock is held: no other command makes this attempt.
func (l *launcher) hand(r outcome, t *contract.Task, low, id, hash string, agent contract.Agent, gated bool) outcome {
	c, k := l.c, l.c.Kit
	run := k.Store.Dir(c.Root, c.Run)
	text := fmt.Sprintf(newWork, t.ID, t.Name, filepath.Join(run, t.Brief), filepath.Join(contract.AttemptDir(run, id), "result.md"))
	seq, pane := 0, ""
	err := l.step(low, func(up, low *contract.State) error {
		_, err := k.Rules.Start(up, t.ID, l.retry, false, hash, agent, gated, contract.Now())
		if err == nil {
			err = k.Rules.Lead(up, low, id, contract.Binding{}, "", contract.Now())
		}
		if err == nil {
			err = k.Rules.Working(up, id, contract.Now())
		}
		if err == nil {
			seq, err = k.Rules.Tell(up, t.ID, text, "", contract.Now())
			r.Tab, pane = up.Attempts[id].Place.Tab, up.Attempts[id].Place.Pane
			// A leader that waits on its fleet hears it there: a line typed
			// into a tab whose agent sits in a long wait is read after it.
			k.Rules.Raise(low, contract.Event{Kind: "work", Text: fmt.Sprintf("the orchestrator handed your fleet group %s: whaleshark mail", t.ID)}, contract.Now())
		}
		return err
	})
	if err != nil {
		r.Tab, r.Error = "", refusal(err)
		return r
	}
	r.Attempt, r.State = id, working
	if k.Terms.Point(pane, contract.PointFleet, "") == nil {
		l.step("", func(s, _ *contract.State) error { return k.Rules.Pointed(s, id, seq, contract.Now()) })
	}
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
		n, cli.Line(strings.Join(some, ", ")), more, cli.Line(some[0]))
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
	switch {
	case errors.Is(cause, contract.ErrEngineUnreachable):
		e.Next = []string{opener(c)}
	case !atPrompt:
		e.Next = []string{line(c, "close "+r.Task), line(c, "start "+r.Task+" --retry")}
	}
	r.Error = e
	return r
}
