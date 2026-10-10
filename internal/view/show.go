package view

import (
	"cmp"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// show prints one task in full, what to do next on top, and marks it seen.
func show(c *contract.Call) (any, error) {
	diff, stat := c.Flags["diff"] != nil, c.Flags["stat"] != nil
	switch {
	case len(c.Args) != 1:
		return nil, usage("show takes one task, or one try of it: T3 or T3.2.")
	case stat && !diff:
		return nil, usage("--stat goes with --diff.")
	}
	s, _, v, err := look(c)
	if err != nil {
		return nil, err
	}
	// A name may hold a dot, so the whole word is tried as a task first.
	arg, only := c.Args[0], ""
	t, err := c.Kit.Rules.FindTask(s, arg)
	if i := strings.LastIndex(arg, "."); err != nil && i > 0 {
		if t, err = c.Kit.Rules.FindTask(s, arg[:i]); err == nil {
			only = t.ID + arg[i:]
		}
	}
	if err != nil {
		return nil, err
	}
	if only != "" && !slices.Contains(t.Attempts, only) {
		return nil, missing("no_such_attempt", "%s has no try %s.", t.ID, only)
	}
	if diff {
		// What the task changed, as git says it and with nothing in it that
		// could rewrite the terminal.
		text, err := c.Kit.Integrator.Diff(c.Root, s, t.ID, stat)
		if _, ok := err.(*contract.Refusal); err != nil && !ok {
			err = missing("no_diff", "What %s changed cannot be shown: %v.", t.ID, err)
		}
		if err != nil {
			return nil, err
		}
		text = strings.TrimRight(cli.Plain(text), "\n") + "\n"
		io.WriteString(c.Out, text)
		looked(c, t.ID)
		return map[string]string{"task": t.ID, "diff": text}, nil
	}
	dir := c.Kit.Reader().Dir(c.Root, c.Run)
	if rel, err := filepath.Rel(c.Root, dir); err == nil {
		dir = rel
	}
	tv := taskView(v, s, t, only, filepath.ToSlash(dir))
	// Only work of the task's own is counted: a shared folder holds everybody's.
	if t.Worktree != nil || t.Accepted != nil && t.Accepted.Commit != "" {
		stat, _ := c.Kit.Integrator.Diff(c.Root, s, t.ID, true)
		tv.Changed = changed(stat)
	}
	if c.Flags["screen"] != nil {
		tv.Screen = "its tab is closed"
		if tv.Card.Tab != "" && len(tv.Tries) > 0 {
			if tv.Screen, err = c.Kit.Terms.Screen(s.Attempts[tv.Tries[0].Attempt].Place.Pane); err != nil {
				tv.Screen = "the screen cannot be read: " + err.Error()
			} else if strings.TrimSpace(tv.Screen) == "" {
				tv.Screen = "nothing on its screen"
			}
		}
	}
	taskText(c.Out, tv, c.Now, options(c))
	looked(c, t.ID)
	return tv, nil
}

var statLine = regexp.MustCompile(`(\d+) files? changed(?:, (\d+) insertions?\(\+\))?(?:, (\d+) deletions?\(-\))?\s*$`)

// changed is the last line of git's count in our words: "9 files, +410 -12".
func changed(stat string) string {
	m := statLine.FindStringSubmatch(stat)
	if m == nil {
		return ""
	}
	n, _ := strconv.Atoi(m[1])
	return fmt.Sprintf("%s, +%s -%s", plural(n, "file", "files"), cmp.Or(m[2], "0"), cmp.Or(m[3], "0"))
}

// taskView is one task in full: its card of the view, then each try, the
// newest first, or the one try that was named, each with the moments its
// record holds. dir is the run's folder.
func taskView(v *contract.View, s *contract.State, t *contract.Task, only, dir string) *contract.TaskView {
	tv := &contract.TaskView{Card: *cardOf(v, t.ID), Run: s.Run.ID, Check: cli.Plain(t.Check), Owns: t.Owns, Decisions: t.Decisions}
	if t.Brief != "" {
		tv.Brief = path.Join(dir, filepath.ToSlash(t.Brief))
	}
	// The card's way out without the line that leads here.
	tv.Card.Next = slices.DeleteFunc(slices.Clone(tv.Card.Next), func(line string) bool { return line == "whaleshark show "+t.ID })
	for _, it := range append(slices.Clone(v.Items), v.Held...) {
		if it.Task == t.ID {
			tv.Items = append(tv.Items, it)
		}
	}
	if a := t.Accepted; a != nil && a.How == contract.AcceptByHand {
		tv.ByHand = cli.Plain(a.Note)
	}
	for i := len(t.Attempts) - 1; i >= 0; i-- {
		a := s.Attempts[t.Attempts[i]]
		if a == nil || only != "" && a.ID != only {
			continue
		}
		try := contract.Try{Attempt: a.ID, State: a.State}
		add := func(at time.Time, text string) {
			try.History = append(try.History, contract.Event{At: at, Attempt: a.ID, Text: cli.Plain(text)})
		}
		add(a.StartedAt, "started")
		if p := a.Progress; p != nil {
			add(p.At, fmt.Sprintf("%d%% %s", p.Pct, p.Note))
		}
		if r := a.Report; r != nil {
			add(r.At, r.Outcome)
			try.Result, try.Evidence = path.Join(dir, "attempts", a.ID, "result.md"), r.Evidence
		}
		if k := a.Check; k != nil {
			add(k.At, map[bool]string{true: "check passed", false: "check failed"}[k.OK])
			try.CheckLog = path.Join(dir, "attempts", a.ID, "check.log")
		}
		if !a.State.Live() {
			add(a.EndedAt, strings.ReplaceAll(string(a.State), "_", " "))
		}
		slices.SortStableFunc(try.History, func(x, y contract.Event) int { return x.At.Compare(y.At) })
		if len(tv.Tries) == 0 && a.Report != nil {
			tv.Says = cli.Plain(a.Report.Summary)
		}
		if len(tv.Tries) == 0 {
			tv.Result = a.Check
		}
		tv.Tries = append(tv.Tries, try)
	}
	return tv
}

// taskText prints a task in full: what to do, what the worker says, the
// proof, then the background.
func taskText(w io.Writer, tv *contract.TaskView, now time.Time, o Options) {
	p := newPrinter(&contract.View{At: now}, o)
	c, clock := tv.Card, func(t time.Time) string { return t.In(now.Location()).Format("15:04") }
	var newest contract.Try
	parts := []string{p.mark(c.Look) + " " + clean(wordOf(c))}
	if len(tv.Tries) > 0 {
		newest = tv.Tries[0]
		ctx := "unknown"
		if c.CtxKnown {
			ctx = fmt.Sprint(c.Ctx, "%")
		}
		parts = append(parts, fmt.Sprint("try ", c.Try), clean(c.Agent), age(now.Sub(newest.History[0].At)), "context "+ctx)
	}
	p.line(pad(clean(c.Task)+"  "+clean(c.Name), 28), strings.Join(parts, " · "), "run "+clean(tv.Run))
	if len(c.Next) > 0 {
		p.out.WriteByte('\n')
		p.steps(" ", c.Next)
	}
	p.out.WriteByte('\n')
	says, check := "", clean(tv.Check)
	if tv.Says != "" {
		says = fmt.Sprintf("%q", clean(tv.Says))
	}
	if r := tv.Result; r != nil {
		check += " · " + map[bool]string{true: "passed ", false: "FAILED "}[r.OK] + clock(r.At) + " · " + clean(r.Tail)
	}
	var evidence []string
	for _, e := range newest.Evidence {
		evidence = append(evidence, clean(e.Name))
	}
	for _, f := range [][2]string{
		{"Says", says}, {"Changed", tv.Changed}, {"Check", check}, {"By hand", clean(tv.ByHand)}, {"May edit", clean(strings.Join(tv.Owns, ", "))}, {"Brief", clean(tv.Brief)},
		{"Result", clean(newest.Result)}, {"Check log", clean(newest.CheckLog)}, {"Evidence", strings.Join(evidence, ", ")},
	} {
		if f[1] != "" {
			p.line(" "+pad(f[0], 10), f[1], "")
		}
	}
	for _, d := range tv.Decisions {
		p.line(" "+pad("Decided", 10), clean(d.Question+" "+d.Answer), "")
	}
	if len(tv.Items) > 0 {
		rows := make([]row, len(tv.Items))
		for i, it := range tv.Items {
			rows[i] = p.item(it)
		}
		p.block("FOR YOU", rows)
	}
	p.out.WriteByte('\n')
	for _, try := range tv.Tries {
		moments := make([]string, len(try.History))
		for i, e := range try.History {
			moments[i] = clock(e.At) + " " + clean(e.Text)
		}
		label := " "
		if len(tv.Tries) > 1 {
			label = " " + clean(try.Attempt) + " " + clean(string(try.State)) + ": "
		}
		p.line(label, strings.Join(moments, " · "), "")
	}
	if tv.Screen != "" {
		p.out.WriteString("\nSCREEN\n")
		for _, l := range strings.Split(strings.TrimRight(cli.Plain(tv.Screen), "\n"), "\n") {
			p.out.WriteString(strings.TrimRight(" | "+strings.ReplaceAll(l, "\t", " "), " ") + "\n")
		}
	}
	io.WriteString(w, p.out.String())
}
