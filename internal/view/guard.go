package view

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// stuckWord begins a card's news while the detector's verdict stands on it.
const stuckWord = "stuck "

// hours is a length of time in agent-hours, to one place: "31.5", "40".
func hours(d time.Duration) string {
	return strconv.FormatFloat(tenths(d), 'f', -1, 64)
}

// tenths is a span in agent-hours, to one place.
func tenths(d time.Duration) float64 { return math.Round(d.Hours()*10) / 10 }

// runs is the job's run and its fleets' runs: what the spending guard counts.
func (b *builder) runs() []*contract.State {
	return append(slices.Collect(maps.Values(b.in.Fleets)), b.s)
}

// spent is the spending guard's one line: "spent: 31.5 of 40 agent-hours",
// with "held" while the guard holds the job.
func (b *builder) spent() string {
	line, sp := "spent: "+hours(contract.Spent(b.in.Now, b.runs()...)), b.s.Run.Spend
	if limit := b.in.Person.SpendLimit(sp, false); limit != contract.Unlimited {
		line += fmt.Sprint(" of ", limit)
	}
	if line += " agent-hours"; sp.Held {
		line += " · held"
	}
	return line
}

// guarded words an item one of phase 6's guards raised, once for every
// screen: what stands under its first line, and what it offers beside its
// form's answers or in their place. A setting's item is the program's own
// words but for the two values.
func (b *builder) guarded(q *contract.Question, it *contract.Item, answer func(string) string) {
	act := func(label, id string) contract.Button { return contract.Button{Label: label, Action: id} }
	switch q.Cause {
	case contract.CausePlan:
		it.Buttons = append(it.Buttons, act("Open the plan", "open-plan"))
	case contract.CauseProtect:
		if p := b.s.Run.Protect; p != nil && len(p.Files) > 0 {
			for _, f := range p.Files {
				it.Text += "\n" + clean(f)
			}
			it.Text += "\nThe job cannot be landed until you approve " + map[bool]string{true: "this file as it is", false: fmt.Sprint("these ", len(p.Files), " files as they are")}[len(p.Files) == 1] + " now."
		}
		it.Buttons = append(it.Buttons, act("Show the changes", "show-changes"))
	case contract.CauseBudget:
		// The button says what the answer grants: the change the item carries.
		grant, open := cmp.Or(q.Set, &contract.SetAsk{}), 0
		from, _ := strconv.Atoi(grant.From)
		to, _ := strconv.Atoi(grant.To)
		for _, s := range b.runs() {
			for _, a := range s.Attempts {
				if a.State.Live() {
					open++
				}
			}
		}
		it.Text += "\n" + plural(open, "agent is", "agents are") + " held where they stand; those with nothing in hand were closed.\nNothing is lost. Nothing more is started until you say."
		more := fmt.Sprint(to-from, " more agent-hours")
		it.Buttons = []contract.Button{{Label: more, Key: "1", Answer: contract.AnswerMore}, {Label: "No limit for this job", Key: "2", Answer: contract.AnswerNoLimit}, {Label: "Leave it held", Key: "3", Answer: contract.AnswerLeave}}
		it.Next = []string{answer(contract.AnswerMore), answer(quote(contract.AnswerNoLimit)), answer(contract.AnswerLeave)}
	case contract.CauseSetting:
		if key, _ := contract.FindKey(cmp.Or(q.Set, &contract.SetAsk{}).Key); key != nil {
			it.Text = "Your agent wants to change a setting\n" + key.Label + ":  " + clean(q.Set.From) + " → " + clean(q.Set.To) + "\n" + key.Help
		}
		it.Buttons = []contract.Button{{Label: "Change it", Key: "y", Answer: contract.AnswerChange}, {Label: "Leave it", Key: "n", Answer: contract.AnswerLeave}}
		it.Next = []string{answer(contract.AnswerChange), answer(contract.AnswerLeave)}
	case contract.CauseStuck:
		it.Buttons = []contract.Button{act("Go there", "go"), act("Stop it", "stop"), act("Note to its leader", "note-leader")}
		if q.Fleet != "" { // a fleet's worker: its task here is the fleet's group, and stopping that parks the fleet
			it.Buttons = slices.Delete(it.Buttons, 1, 2)
		}
		it.Next = []string{"whaleshark jump " + quote(it.Task)}
	default:
		return
	}
	first, _, _ := strings.Cut(it.Text, "\n")
	it.Look, it.News = contract.LookNeedsYou, news(first)
}

// PlanText words a job's plan as its item shows it: the totals, then a line
// a fleet or a task of those named, forty lines at most. Once a plan was
// approved, what is named is what was added since.
func PlanText(up *contract.State, _ map[string]*contract.State, ids []string, limits contract.PersonConfig) string {
	fleets, more, per := up.Run.Mode == contract.ModeFleets, "", limits.Fleets.Agents
	if up.Run.Plan != nil && len(up.Run.Plan.Approved) > 0 {
		more = "more "
	}
	upTo := func(n int) string {
		if per == contract.Unlimited {
			return "no limit on agents"
		}
		return fmt.Sprint("up to ", n, " agents")
	}
	var rows, names []string
	things, tools, n := map[string]int{}, map[string]string{}, 0
	for _, id := range ids {
		t := up.Tasks[id]
		if t == nil {
			continue
		}
		n++
		key, tool := clean(t.Fleet), clean(cmp.Or(t.Agent, cmp.Or(up.Run.Fleets[t.Fleet], &contract.Fleet{}).Tool, limits.Fleets.Leader, contract.DefaultTool))
		if !fleets {
			key = clean(cmp.Or(t.Agent, contract.DefaultTool))
			row := "  " + clean(t.Name) + "\t" + strings.TrimSpace(key+" "+clean(t.Model))
			if after := unmet(up, t); len(after) > 0 {
				row += "\tafter " + clean(strings.Join(after, ", "))
			}
			rows = append(rows, row)
		}
		if things[key]++; things[key] == 1 {
			names, tools[key] = append(names, key), tool
		}
	}
	head, limit := fmt.Sprintf("Plan for %q · ", clean(up.Run.Objective)), "unlimited"
	if total := limits.Fleets.Total; total != contract.Unlimited {
		limit = strconv.Itoa(total)
	}
	switch {
	case !fleets:
		head += plural(n, more+"task", more+"tasks") + " · " + plural(n, "agent", "agents") + " (" + strings.Join(names, ", ") + ")"
	case more != "" && len(names) == 1:
		head += plural(n, "more thing", "more things") + " for " + names[0]
	default:
		head += plural(len(names), "fleet", "fleets") + " · " + plural(n, more+"thing", more+"things") + " · " + upTo(len(names)*(per+1)) + " (your limit: " + limit + ")"
	}
	for _, name := range names {
		if !fleets {
			break
		}
		row := "  " + name + "\t" + tools[name] + "\t" + plural(things[name], "thing", "things") + "\t" + upTo(per)
		if tool, _ := contract.FindTool(contract.BuiltIn(), tools[name]); !tool.Held() {
			row += "\t· " + tools[name] + " is not held by Stop all"
		}
		rows = append(rows, row)
	}
	if len(rows) > 39 {
		rows = append(rows[:38], fmt.Sprint("  … and ", len(rows)-38, " more: whaleshark graph"))
	}
	var out strings.Builder
	w := tabwriter.NewWriter(&out, 0, 0, 3, ' ', 0)
	fmt.Fprint(w, strings.Join(rows, "\n"))
	w.Flush()
	return strings.TrimRight(head+"\n"+out.String(), "\n")
}
